// Package wikimarkup validates and renders the supported Confluence storage
// format. It is pure Go and shared by the server and WASM renderer.
package wikimarkup

import (
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/url"
	"regexp"
	"strings"
)

// macroElements and macroAttributes are Confluence's structured macro markup:
// the macro, the parameters that configure it, and the body it wraps.
var macroElements = map[string]bool{
	"structured-macro": true, "parameter": true, "rich-text-body": true, "plain-text-body": true,
	"task-list": true, "task": true, "task-id": true, "task-uuid": true, "task-status": true, "task-body": true,
	"layout": true, "layout-section": true, "layout-cell": true,
}

// panelMacros are Confluence's panel macros: a box around a body, with the
// title it is given shown above it.
var panelMacros = map[string]bool{"info": true, "note": true, "warning": true, "tip": true, "panel": true}

// statusColours are the colours Confluence's status macro is drawn in. A
// status whose colour is anything else is drawn grey, as Confluence draws it.
var statusColours = map[string]bool{"grey": true, "red": true, "yellow": true, "green": true, "blue": true, "purple": true}

// layoutTypes are the column arrangements a layout section takes.
var layoutTypes = map[string]bool{
	"single": true, "two_equal": true, "two_left_sidebar": true, "two_right_sidebar": true,
	"three_equal": true, "three_with_sidebars": true,
}

// tocMarker stands in for a table of contents while the page renders: the
// headings it lists are only all known once the whole body has been read. It
// carries the macro's own identifier, and cannot come from a page body,
// because a NUL is not valid XML.
var tocMarker = regexp.MustCompile("\x00wiki-toc:([^\x00]*)\x00")

// storageDate is the only form a time element's datetime takes in storage.
var storageDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

var macroAttributes = map[string]bool{
	"name": true, "macro-id": true, "schema-version": true, "local-id": true, "type": true,
}

// macroFrame is one structured macro being read, with the parameters that
// describe how it is drawn.
type macroFrame struct {
	name     string
	id       string
	title    string
	colour   string
	language string
}

// macroIDAttribute keeps a macro's own identifier on the element it renders
// as, so a body that goes through the editor comes back with the identifier
// Confluence gave it.
func macroIDAttribute(id string) string {
	if id == "" {
		return ""
	}
	return ` data-macro-id="` + html.EscapeString(id) + `"`
}

// tocEntry is one heading a table of contents links to.
type tocEntry struct {
	level int
	id    string
	text  string
}

// acAttribute reads one of a Confluence element's ac attributes.
func acAttribute(start xml.StartElement, name string) string {
	for _, attr := range start.Attr {
		if attr.Name.Local == name {
			return strings.TrimSpace(attr.Value)
		}
	}
	return ""
}

// renderTableOfContents lists the headings a page holds, in the order they
// appear, each linking to the heading itself.
func renderTableOfContents(headings []tocEntry, id string) string {
	open := `<nav class="wiki-toc" data-macro="toc"` + macroIDAttribute(id) + ` aria-label="Contents">`
	if len(headings) == 0 {
		return open + `<p>This page has no headings yet.</p></nav>`
	}
	var b strings.Builder
	b.WriteString(open + `<ol>`)
	for _, heading := range headings {
		b.WriteString(fmt.Sprintf(`<li class="wiki-toc-level-%d"><a href="#%s">%s</a></li>`,
			heading.level, html.EscapeString(heading.id), html.EscapeString(heading.text)))
	}
	b.WriteString("</ol></nav>")
	return b.String()
}

// A mention is Confluence's user link: an ac:link holding an ri:user that names
// the account, optionally with the name to show in an ac:plain-text-link-body.
// It renders as a link to the person, and nothing else in the ri namespace is
// accepted.
var mentionAttributes = map[string]bool{"account-id": true, "userkey": true, "local-id": true}

var tags = map[string]bool{"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "ul": true, "ol": true, "li": true, "blockquote": true, "pre": true, "code": true, "strong": true, "em": true, "b": true, "i": true, "u": true, "s": true, "a": true, "br": true, "hr": true, "table": true, "thead": true, "tbody": true, "tr": true, "th": true, "td": true}

// Render rejects unsupported markup rather than silently dropping content.
// Every emitted tag is allowlisted and every text/attribute is escaped.
func Render(storage string) (string, error) {
	if len(storage) > 1<<20 {
		return "", fmt.Errorf("page body must be at most 1 MiB")
	}
	d := xml.NewDecoder(strings.NewReader("<root>" + storage + "</root>"))
	var b strings.Builder
	depth := 0
	suppressed := 0
	rootSeen := false
	taskStatus, inTaskStatus := "", false
	// The macros being read, the parameter one of them is being told, and the
	// headings a table of contents would list.
	var macros []macroFrame
	var parameter strings.Builder
	parameterName, inParameter := "", false
	var headings []tocEntry
	var headingText strings.Builder
	headingLevel := 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("invalid storage markup: %w", err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			if depth > 100 {
				return "", fmt.Errorf("page markup is nested too deeply")
			}
			if depth == 1 {
				if rootSeen {
					return "", fmt.Errorf("storage must be one content fragment")
				}
				rootSeen = true
				continue
			}
			tag := t.Name.Local
			// Confluence's storage format carries macros in the ac namespace.
			// They are structure rather than markup: a macro renders as the
			// body it holds, and its parameters describe it without being
			// shown, so none of these elements reach the HTML.
			if t.Name.Space == "ac" && tag == "link" {
				mention, consumed, err := readMention(d, t)
				if err != nil {
					return "", err
				}
				depth--
				_ = consumed
				b.WriteString(mention)
				continue
			}
			if t.Name.Space == "ri" {
				return "", fmt.Errorf("unsupported storage element: ri:%s", tag)
			}
			if t.Name.Space == "ac" {
				if !macroElements[tag] {
					return "", fmt.Errorf("unsupported storage element: ac:%s", tag)
				}
				for _, a := range t.Attr {
					if !macroAttributes[a.Name.Local] {
						return "", fmt.Errorf("unsupported attribute %s on ac:%s", a.Name.Local, tag)
					}
				}
				switch tag {
				case "structured-macro":
					name := strings.ToLower(acAttribute(t, "name"))
					id := acAttribute(t, "macro-id")
					macros = append(macros, macroFrame{name: name, id: id})
					switch {
					case panelMacros[name]:
						b.WriteString(`<div class="wiki-panel wiki-panel-` + name + `" data-macro="` + name + `"` + macroIDAttribute(id) + `>`)
					case name == "toc":
						if strings.ContainsRune(id, 0) {
							return "", fmt.Errorf("a macro identifier cannot hold a NUL")
						}
						b.WriteString("\x00wiki-toc:" + id + "\x00")
					}
				case "parameter":
					suppressed++
					inParameter = true
					parameterName = strings.ToLower(acAttribute(t, "name"))
					parameter.Reset()
				case "plain-text-body":
					// A code macro shows its body as the code it is, in the
					// language it says it is written in.
					if len(macros) > 0 && macros[len(macros)-1].name == "code" {
						b.WriteString(`<pre class="wiki-code" data-macro="code"` + macroIDAttribute(macros[len(macros)-1].id))
						if language := macros[len(macros)-1].language; language != "" {
							b.WriteString(` data-language="` + html.EscapeString(language) + `"`)
						}
						b.WriteString(`>`)
					}
				case "layout":
					b.WriteString(`<div class="wiki-layout" data-macro="layout">`)
				case "layout-section":
					layout := strings.ToLower(acAttribute(t, "type"))
					if !layoutTypes[layout] {
						return "", fmt.Errorf("unsupported layout section type %q", layout)
					}
					b.WriteString(`<div class="wiki-layout-section wiki-layout-` + layout + `" data-layout-type="` + layout + `">`)
				case "layout-cell":
					b.WriteString(`<div class="wiki-layout-cell">`)
				case "task-id", "task-uuid":
					suppressed++
				case "task-status":
					suppressed++
					inTaskStatus, taskStatus = true, ""
				case "task-list":
					b.WriteString("<ul>")
				case "task":
					b.WriteString("<li>")
					taskStatus = ""
				case "task-body":
					// A task shows its state the way a checklist does.
					if strings.TrimSpace(taskStatus) == "complete" {
						b.WriteString("☑ ")
					} else {
						b.WriteString("☐ ")
					}
				}
				continue
			}
			if t.Name.Space == "" && tag == "time" {
				// A date, such as when a task is due, shown as written.
				if len(t.Attr) != 1 || t.Attr[0].Name.Space != "" || t.Attr[0].Name.Local != "datetime" || !storageDate.MatchString(t.Attr[0].Value) {
					return "", fmt.Errorf("a time element carries only a datetime date")
				}
				date := t.Attr[0].Value
				b.WriteString(`<time datetime=` + quotedAttribute(date) + `>` + html.EscapeString(date))
				suppressed++
				continue
			}
			if t.Name.Space != "" || !tags[tag] {
				return "", fmt.Errorf("unsupported storage element: %s", tag)
			}
			// A heading is given an identifier so a table of contents, or a
			// link somebody shares, can reach it.
			if len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
				headingLevel = int(tag[1] - '0')
				headingText.Reset()
				headings = append(headings, tocEntry{level: headingLevel, id: fmt.Sprintf("wiki-heading-%d", len(headings)+1)})
				b.WriteString("<" + tag + ` id="` + headings[len(headings)-1].id + `"`)
				b.WriteString(">")
				continue
			}
			b.WriteString("<" + tag)
			for _, a := range t.Attr {
				if tag != "a" || a.Name.Local != "href" || a.Name.Space != "" {
					return "", fmt.Errorf("unsupported attribute %s on %s", a.Name.Local, tag)
				}
				u, err := url.Parse(a.Value)
				if err != nil || u.User != nil || strings.HasPrefix(a.Value, "//") || strings.ContainsAny(a.Value, "\\\r\n\t") || (u.Scheme != "" && u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "mailto") {
					return "", fmt.Errorf("links must use http, https, mailto or a relative page URL")
				}
				b.WriteString(` href="` + html.EscapeString(a.Value) + `"`)
			}
			b.WriteString(">")
		case xml.EndElement:
			if t.Name.Space == "ac" {
				switch t.Name.Local {
				case "structured-macro":
					frame := macros[len(macros)-1]
					macros = macros[:len(macros)-1]
					switch {
					case panelMacros[frame.name]:
						b.WriteString("</div>")
					case frame.name == "status":
						// A status is a word in a colour. Confluence draws one
						// whose colour it does not know in grey.
						colour := frame.colour
						if !statusColours[colour] {
							colour = "grey"
						}
						b.WriteString(`<span class="wiki-status wiki-status-` + colour + `" data-macro="status" data-colour="` + colour + `"` + macroIDAttribute(frame.id) + `>` + html.EscapeString(frame.title) + `</span>`)
					}
				case "parameter":
					suppressed--
					inParameter = false
					if len(macros) > 0 {
						frame := &macros[len(macros)-1]
						value := strings.TrimSpace(parameter.String())
						switch {
						case panelMacros[frame.name] && parameterName == "title" && value != "":
							b.WriteString(`<p class="wiki-panel-title">` + html.EscapeString(value) + `</p>`)
						case frame.name == "status" && parameterName == "title":
							frame.title = value
						case frame.name == "status" && parameterName == "colour":
							frame.colour = strings.ToLower(value)
						case frame.name == "code" && parameterName == "language":
							frame.language = value
						}
					}
					parameterName = ""
				case "plain-text-body":
					if len(macros) > 0 && macros[len(macros)-1].name == "code" {
						b.WriteString("</pre>")
					}
				case "layout", "layout-section", "layout-cell":
					b.WriteString("</div>")
				case "task-id", "task-uuid":
					suppressed--
				case "task-status":
					suppressed--
					inTaskStatus = false
				case "task-list":
					b.WriteString("</ul>")
				case "task":
					b.WriteString("</li>")
				}
				depth--
				continue
			}
			if t.Name.Space == "" && t.Name.Local == "time" {
				suppressed--
			}
			if headingLevel > 0 && t.Name.Local == fmt.Sprintf("h%d", headingLevel) {
				headings[len(headings)-1].text = strings.Join(strings.Fields(headingText.String()), " ")
				headingLevel = 0
			}
			if depth > 1 && t.Name.Local != "br" && t.Name.Local != "hr" {
				b.WriteString("</" + t.Name.Local + ">")
			}
			depth--
		case xml.CharData:
			// A parameter names how a macro behaves; it is not part of what a
			// reader sees, so its text is kept in the stored body and left out
			// of the rendering.
			if inTaskStatus {
				taskStatus += string(t)
			}
			if inParameter {
				parameter.Write(t)
			}
			if headingLevel > 0 && suppressed == 0 {
				headingText.Write(t)
			}
			if suppressed == 0 {
				b.WriteString(html.EscapeString(string(t)))
			}
		case xml.Comment:
			return "", fmt.Errorf("storage comments are not supported")
		default:
			return "", fmt.Errorf("storage directives are not supported")
		}
	}
	rendered := tocMarker.ReplaceAllStringFunc(b.String(), func(marker string) string {
		return renderTableOfContents(headings, tocMarker.FindStringSubmatch(marker)[1])
	})
	return rendered, nil
}

// Text returns readable plain text from validated storage markup for search
// excerpts and accessible summaries.
func Text(storage string) (string, error) {
	if _, err := Render(storage); err != nil {
		return "", err
	}
	decoder := xml.NewDecoder(strings.NewReader("<root>" + storage + "</root>"))
	var value strings.Builder
	// A task's id and status describe it rather than being part of its text.
	hidden := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if value.Len() > 0 && typed.Name.Local != "root" && typed.Name.Local != "a" && typed.Name.Local != "strong" && typed.Name.Local != "em" && typed.Name.Local != "b" && typed.Name.Local != "i" && typed.Name.Local != "u" && typed.Name.Local != "s" {
				value.WriteByte(' ')
			}
			if typed.Name.Space == "ac" && taskMetadata[typed.Name.Local] {
				hidden++
			}
		case xml.EndElement:
			if typed.Name.Space == "ac" && taskMetadata[typed.Name.Local] {
				hidden--
			}
		case xml.CharData:
			if hidden == 0 {
				value.Write(typed)
			}
		}
	}
	return strings.Join(strings.Fields(value.String()), " "), nil
}

// readMention consumes one ac:link and renders it. Only a link to a user is
// supported: it becomes a link to that person, labelled with the name the
// link carries, or with "@user" when it carries none.
func readMention(d *xml.Decoder, start xml.StartElement) (string, bool, error) {
	for _, a := range start.Attr {
		if !mentionAttributes[a.Name.Local] {
			return "", false, fmt.Errorf("unsupported attribute %s on ac:link", a.Name.Local)
		}
	}
	var accountID, label string
	depth := 1
	inLabel := false
	for depth > 0 {
		token, err := d.Token()
		if err != nil {
			return "", false, fmt.Errorf("invalid storage markup: %w", err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			switch {
			case t.Name.Space == "ri" && t.Name.Local == "user":
				for _, a := range t.Attr {
					if !mentionAttributes[a.Name.Local] {
						return "", false, fmt.Errorf("unsupported attribute %s on ri:user", a.Name.Local)
					}
					if a.Name.Local == "account-id" {
						accountID = a.Value
					}
				}
			case t.Name.Space == "ac" && t.Name.Local == "plain-text-link-body":
				inLabel = true
			default:
				return "", false, fmt.Errorf("a link may only mention a user")
			}
		case xml.EndElement:
			depth--
			if t.Name.Space == "ac" && t.Name.Local == "plain-text-link-body" {
				inLabel = false
			}
		case xml.CharData:
			if inLabel {
				label += string(t)
			} else if strings.TrimSpace(string(t)) != "" {
				return "", false, fmt.Errorf("a mention's text belongs in its link body")
			}
		default:
			return "", false, fmt.Errorf("storage directives are not supported")
		}
	}
	if accountID == "" || strings.ContainsAny(accountID, "/?#\\ ") {
		return "", false, fmt.Errorf("a mention names an account")
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "user"
	}
	return `<a href=` + quotedAttribute("/people/"+url.PathEscape(accountID)) + `>@` + html.EscapeString(label) + `</a>`, true, nil
}

// quotedAttribute renders a value as a double-quoted HTML attribute. The value
// is HTML-escaped, and a double quote, which would end the attribute, is
// replaced explicitly.
func quotedAttribute(value string) string {
	return `"` + strings.ReplaceAll(html.EscapeString(value), `"`, "&#34;") + `"`
}

// MentionedAccounts lists the accounts a storage body mentions, each once, in
// the order they first appear.
func MentionedAccounts(storage string) []string {
	decoder := xml.NewDecoder(strings.NewReader("<root>" + storage + "</root>"))
	seen := map[string]bool{}
	accounts := []string{}
	for {
		token, err := decoder.Token()
		if err != nil {
			return accounts
		}
		if start, ok := token.(xml.StartElement); ok && start.Name.Space == "ri" && start.Name.Local == "user" {
			for _, a := range start.Attr {
				if a.Name.Local == "account-id" && a.Value != "" && !seen[a.Value] {
					seen[a.Value] = true
					accounts = append(accounts, a.Value)
				}
			}
		}
	}
}

var unlabelledMention = regexp.MustCompile(`<ac:link>\s*<ri:user\s+ri:account-id="([^"]+)"\s*/>\s*</ac:link>`)

// LabelMentions names the people a storage body mentions without a link body,
// so a reader sees who was mentioned rather than a bare "@user".
func LabelMentions(storage string, names map[string]string) string {
	if len(names) == 0 || !strings.Contains(storage, "ri:user") {
		return storage
	}
	return unlabelledMention.ReplaceAllStringFunc(storage, func(link string) string {
		name := names[html.UnescapeString(unlabelledMention.FindStringSubmatch(link)[1])]
		if name == "" {
			return link
		}
		return strings.TrimSuffix(strings.TrimSpace(link), "</ac:link>") + `<ac:plain-text-link-body>` + html.EscapeString(name) + `</ac:plain-text-link-body></ac:link>`
	})
}
