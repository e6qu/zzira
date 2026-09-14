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
}

// storageDate is the only form a time element's datetime takes in storage.
var storageDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

var macroAttributes = map[string]bool{
	"name": true, "macro-id": true, "schema-version": true, "local-id": true,
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
				case "parameter", "task-id", "task-uuid":
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
				date := html.EscapeString(t.Attr[0].Value)
				b.WriteString(`<time datetime="` + date + `">` + date)
				suppressed++
				continue
			}
			if t.Name.Space != "" || !tags[tag] {
				return "", fmt.Errorf("unsupported storage element: %s", tag)
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
				case "parameter", "task-id", "task-uuid":
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
			if suppressed == 0 {
				b.WriteString(html.EscapeString(string(t)))
			}
		case xml.Comment:
			return "", fmt.Errorf("storage comments are not supported")
		default:
			return "", fmt.Errorf("storage directives are not supported")
		}
	}
	return b.String(), nil
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
	return `<a href="/people/` + html.EscapeString(url.PathEscape(accountID)) + `">@` + html.EscapeString(label) + `</a>`, true, nil
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
