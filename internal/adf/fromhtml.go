package adf

import (
	"encoding/json"
	"html"
	"strconv"
	"strings"
	"time"
)

// FromHTML builds an Atlassian Document Format document from the HTML subset
// Confluence's storage format uses. It is the inverse of ToHTML for the
// structures this product produces, and degrades the same way: an element it
// does not model contributes its text rather than failing the conversion.
func FromHTML(source string) json.RawMessage {
	parser := &htmlParser{source: source}
	doc := Node{Type: "doc", Version: 1, Content: parser.blocks()}
	if len(doc.Content) == 0 {
		doc.Content = []Node{{Type: "paragraph"}}
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		return json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph"}]}`)
	}
	return encoded
}

type htmlParser struct {
	source string
	at     int
	// tag is the opening tag of the element nextElement last found, which
	// is where that element's attributes are.
	tag string
}

// blocks reads the block-level elements in order. Text outside any block
// becomes a paragraph, because a document is made of blocks.
func (p *htmlParser) blocks() []Node {
	var nodes []Node
	var loose strings.Builder
	flush := func() {
		text := strings.TrimSpace(loose.String())
		loose.Reset()
		if text == "" {
			return
		}
		nodes = append(nodes, Node{Type: "paragraph", Content: inlineNodes(text)})
	}
	for p.at < len(p.source) {
		name, inner, next, ok := p.nextElement()
		if !ok {
			loose.WriteString(p.source[p.at:])
			p.at = len(p.source)
			break
		}
		if block, produced := blockNode(name, p.tag, inner); produced {
			flush()
			nodes = append(nodes, block)
		} else {
			// Inline markup outside a block keeps its element, so a mention
			// or a date in loose text is still read as one.
			loose.WriteString(p.source[p.at:next])
		}
		p.at = next
	}
	flush()
	return nodes
}

// nextElement finds the next top-level element and returns its name and inner
// markup. Text before it is returned as an unnamed element so it is not lost.
func (p *htmlParser) nextElement() (name, inner string, next int, ok bool) {
	open := strings.IndexByte(p.source[p.at:], '<')
	if open < 0 {
		return "", "", 0, false
	}
	open += p.at
	if open > p.at {
		return "", p.source[p.at:open], open, true
	}
	close := strings.IndexByte(p.source[open:], '>')
	if close < 0 {
		return "", "", 0, false
	}
	close += open
	tag := p.source[open+1 : close]
	if strings.HasPrefix(tag, "!") {
		return "", "", close + 1, true
	}
	name = tagName(tag)
	if name == "" {
		return "", "", close + 1, true
	}
	p.tag = tag
	if strings.HasSuffix(tag, "/") {
		return name, "", close + 1, true
	}
	end := findClosing(p.source, name, close+1)
	if end < 0 {
		return name, p.source[close+1:], len(p.source), true
	}
	return name, p.source[close+1 : end], end + len(name) + 3, true
}

func tagName(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" || strings.HasPrefix(tag, "/") {
		return ""
	}
	if space := strings.IndexAny(tag, " \t\n"); space >= 0 {
		tag = tag[:space]
	}
	return strings.ToLower(strings.TrimSuffix(tag, "/"))
}

// findClosing locates the matching close tag, counting nested opens of the
// same name so a list inside a list closes the right one.
func findClosing(source, name string, from int) int {
	lower := strings.ToLower(source)
	depth := 0
	at := from
	for {
		closeAt := strings.Index(lower[at:], "</"+name+">")
		if closeAt < 0 {
			return -1
		}
		closeAt += at
		next := strings.Index(lower[at:], "<"+name)
		if next >= 0 && next+at < closeAt {
			next += at
			after := next + 1 + len(name)
			// Only the same element nests: <p does not open a <pre, and a
			// self-closing element closes itself.
			if after < len(lower) && strings.IndexByte(" \t\r\n>/", lower[after]) >= 0 {
				if end := strings.IndexByte(lower[next:], '>'); end > 0 && lower[next+end-1] != '/' {
					depth++
				}
			}
			at = after
			continue
		}
		if depth == 0 {
			return closeAt
		}
		depth--
		at = closeAt + len(name) + 3
	}
}

// macroParameter reads one of a structured macro's parameters.
func macroParameter(inner, name string) string {
	open := `<ac:parameter ac:name="` + name + `">`
	at := strings.Index(inner, open)
	if at < 0 {
		return ""
	}
	rest := inner[at+len(open):]
	end := strings.Index(rest, "</ac:parameter>")
	if end < 0 {
		return ""
	}
	return html.UnescapeString(strings.TrimSpace(rest[:end]))
}

// macroBody reads a macro's rich text or plain text body.
func macroBody(inner, element string) string {
	open, closing := "<"+element+">", "</"+element+">"
	at := strings.Index(inner, open)
	if at < 0 {
		return ""
	}
	rest := inner[at+len(open):]
	end := strings.Index(rest, closing)
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// adfPanelKinds maps a Confluence panel macro to the kind of panel a document
// holds.
var adfPanelKinds = map[string]string{"info": "info", "note": "note", "warning": "warning", "tip": "tip", "panel": "custom"}

func blockNode(name, tag, inner string) (Node, bool) {
	switch name {
	case "ac:structured-macro":
		macro := strings.ToLower(attributeValue(tag, "ac:name"))
		if kind, ok := adfPanelKinds[macro]; ok {
			body := (&htmlParser{source: macroBody(inner, "ac:rich-text-body")}).blocks()
			// The title a panel was given is not something a document holds,
			// so it is kept as the bold line Confluence shows it as.
			if title := macroParameter(inner, "title"); title != "" {
				body = append([]Node{{Type: "paragraph", Content: []Node{{Type: "text", Text: title, Mark: []Mark{{Type: "strong"}}}}}}, body...)
			}
			return Node{Type: "panel", Attrs: map[string]any{"panelType": kind}, Content: body}, true
		}
		if macro == "code" {
			return Node{Type: "codeBlock", Content: textNodes(stripTags(macroBody(inner, "ac:plain-text-body")))}, true
		}
		// A macro the document format has no node for is read as the body it
		// wraps, which is what the rest of this parser does with markup it
		// does not model.
		return Node{}, false
	case "p":
		return Node{Type: "paragraph", Content: inlineNodes(inner)}, true
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(name[1] - '0')
		return Node{Type: "heading", Attrs: map[string]any{"level": level}, Content: inlineNodes(inner)}, true
	case "ul", "ol":
		listType := "bulletList"
		if name == "ol" {
			listType = "orderedList"
		}
		return Node{Type: listType, Content: listItems(inner)}, true
	case "blockquote":
		return Node{Type: "blockquote", Content: (&htmlParser{source: inner}).blocks()}, true
	case "pre":
		return Node{Type: "codeBlock", Content: textNodes(stripTags(inner))}, true
	case "hr":
		return Node{Type: "rule"}, true
	case "ac:task-list":
		return Node{Type: "taskList", Attrs: map[string]any{"localId": ""}, Content: taskItems(inner)}, true
	}
	return Node{}, false
}

// taskItems reads the tasks of a Confluence task list: each one's id, whether
// it is done, and the inline content of its body.
func taskItems(inner string) []Node {
	parser := &htmlParser{source: inner}
	var items []Node
	for parser.at < len(parser.source) {
		name, body, next, ok := parser.nextElement()
		if !ok {
			break
		}
		parser.at = next
		if name != "ac:task" {
			continue
		}
		item := Node{Type: "taskItem", Attrs: map[string]any{"localId": "", "state": "TODO"}}
		fields := &htmlParser{source: body}
		for fields.at < len(fields.source) {
			field, value, after, ok := fields.nextElement()
			if !ok {
				break
			}
			fields.at = after
			switch field {
			case "ac:task-id":
				item.Attrs["localId"] = html.UnescapeString(strings.TrimSpace(value))
			case "ac:task-status":
				if strings.TrimSpace(value) == "complete" {
					item.Attrs["state"] = "DONE"
				}
			case "ac:task-body":
				item.Content = inlineNodes(value)
			}
		}
		items = append(items, item)
	}
	return items
}

func listItems(inner string) []Node {
	parser := &htmlParser{source: inner}
	var items []Node
	for parser.at < len(parser.source) {
		name, body, next, ok := parser.nextElement()
		if !ok {
			break
		}
		parser.at = next
		if name != "li" {
			continue
		}
		content := (&htmlParser{source: body}).blocks()
		if len(content) == 0 {
			content = []Node{{Type: "paragraph"}}
		}
		items = append(items, Node{Type: "listItem", Content: content})
	}
	return items
}

// inlineNodes reads the inline markup inside a block: links and the marks this
// product renders.
func inlineNodes(source string) []Node {
	var nodes []Node
	parser := &htmlParser{source: source}
	for parser.at < len(parser.source) {
		name, inner, next, ok := parser.nextElement()
		if !ok {
			nodes = append(nodes, textNodes(parser.source[parser.at:])...)
			break
		}
		parser.at = next
		switch name {
		case "":
			nodes = append(nodes, textNodes(inner)...)
		case "br":
			nodes = append(nodes, Node{Type: "hardBreak"})
		case "strong", "b", "em", "i", "code", "s", "del", "u":
			mark := map[string]string{"strong": "strong", "b": "strong", "em": "em", "i": "em",
				"code": "code", "s": "strike", "del": "strike", "u": "underline"}[name]
			for _, child := range inlineNodes(inner) {
				child.Mark = append(child.Mark, Mark{Type: mark})
				nodes = append(nodes, child)
			}
		case "a":
			href := attributeValue(parser.tag, "href")
			for _, child := range inlineNodes(inner) {
				child.Mark = append(child.Mark, Mark{Type: "link", Attrs: map[string]any{"href": href}})
				nodes = append(nodes, child)
			}
		case "ac:link":
			account := attributeValue(inner, "ri:account-id")
			if account == "" {
				nodes = append(nodes, inlineNodes(inner)...)
				break
			}
			mention := Node{Type: "mention", Attrs: map[string]any{"id": account}}
			const labelOpen, labelClose = "<ac:plain-text-link-body>", "</ac:plain-text-link-body>"
			if at := strings.Index(inner, labelOpen); at >= 0 {
				label := inner[at+len(labelOpen):]
				if end := strings.Index(label, labelClose); end >= 0 {
					label = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(label[:end]), "<![CDATA["), "]]>")
					if label != "" {
						mention.Attrs["text"] = "@" + html.UnescapeString(label)
					}
				}
			}
			nodes = append(nodes, mention)
		case "ac:structured-macro":
			// A status is a word in a colour, which a document holds as its
			// own node; any other macro here is read as what it wraps.
			if strings.ToLower(attributeValue(parser.tag, "ac:name")) != "status" {
				nodes = append(nodes, inlineNodes(inner)...)
				break
			}
			colour := strings.ToLower(macroParameter(inner, "colour"))
			if !adfStatusColours[colour] {
				colour = "grey"
			}
			nodes = append(nodes, Node{Type: "status", Attrs: map[string]any{"text": macroParameter(inner, "title"), "color": colour}})
		case "time":
			if day, err := time.Parse("2006-01-02", attributeValue(parser.tag, "datetime")); err == nil {
				nodes = append(nodes, Node{Type: "date", Attrs: map[string]any{"timestamp": strconv.FormatInt(day.UnixMilli(), 10)}})
			}
		default:
			nodes = append(nodes, inlineNodes(inner)...)
		}
	}
	return nodes
}

func attributeValue(source, name string) string {
	at := strings.Index(strings.ToLower(source), name+"=\"")
	if at < 0 {
		return ""
	}
	rest := source[at+len(name)+2:]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return html.UnescapeString(rest[:end])
}

func textNodes(source string) []Node {
	text := html.UnescapeString(source)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []Node{{Type: "text", Text: text}}
}

// stripTags reduces markup to the text it shows, which is what a plain reading
// of a body is.
func stripTags(source string) string {
	var out strings.Builder
	depth := 0
	for _, r := range source {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			out.WriteRune(r)
		}
	}
	return html.UnescapeString(out.String())
}

// StripTags is the exported reading used when a caller asks for a body without
// markup.
func StripTags(source string) string { return stripTags(source) }
