package adf

import (
	"encoding/json"
	"html"
	"strings"
)

// FromHTML builds an Atlassian Document Format document from the HTML subset
// Confluence's storage format uses. It is the inverse of ToHTML for the
// structures this product produces, and degrades the same way: an element it
// does not model contributes its text rather than failing the conversion.
func FromHTML(source string) json.RawMessage {
	parser := &htmlParser{source: source}
	doc := Node{Type: "doc", Attrs: map[string]any{"version": 1}, Content: parser.blocks()}
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
		if block, produced := blockNode(name, inner); produced {
			flush()
			nodes = append(nodes, block)
		} else {
			loose.WriteString(inner)
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
	if strings.HasSuffix(tag, "/") || strings.HasPrefix(tag, "!") {
		return "", "", close + 1, true
	}
	name = tagName(tag)
	if name == "" {
		return "", "", close + 1, true
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
	depth := 0
	at := from
	for {
		next := strings.Index(strings.ToLower(source[at:]), "<"+name)
		closeAt := strings.Index(strings.ToLower(source[at:]), "</"+name+">")
		if closeAt < 0 {
			return -1
		}
		closeAt += at
		if next >= 0 && next+at < closeAt {
			depth++
			at = next + at + len(name) + 1
			continue
		}
		if depth == 0 {
			return closeAt
		}
		depth--
		at = closeAt + len(name) + 3
	}
}

func blockNode(name, inner string) (Node, bool) {
	switch name {
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
	}
	return Node{}, false
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
			href := attributeValue(source, "href")
			for _, child := range inlineNodes(inner) {
				child.Mark = append(child.Mark, Mark{Type: "link", Attrs: map[string]any{"href": href}})
				nodes = append(nodes, child)
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
