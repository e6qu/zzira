// Package adf is the pure Go Atlassian Document Format subset: parse → safe
// HTML, plus builders. It compiles into both server and wasm targets and is
// the only rich-text code path, so the API's renderedFields, the SSR page,
// and the offline replica all agree byte-for-byte.
package adf

import (
	"encoding/json"
	"html"
	"strconv"
	"strings"
)

// Node is a generic ADF node.
type Node struct {
	Type    string         `json:"type"`
	Attrs   map[string]any `json:"attrs,omitempty"`
	Content []Node         `json:"content,omitempty"`
	Text    string         `json:"text,omitempty"`
	Mark    []Mark         `json:"marks,omitempty"`
}

type Mark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

// ToHTML renders the supported subset. Unknown nodes degrade to their text
// content — rendering never fails, it only degrades.
func ToHTML(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var doc Node
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	var b strings.Builder
	renderNodes(&b, doc.Content, map[string]bool{})
	return b.String()
}

func renderNodes(b *strings.Builder, nodes []Node, marks map[string]bool) {
	for _, n := range nodes {
		renderNode(b, n, marks)
	}
}

func renderNode(b *strings.Builder, n Node, marks map[string]bool) {
	switch n.Type {
	case "paragraph":
		b.WriteString("<p>")
		renderInline(b, n.Content, marks)
		b.WriteString("</p>")
	case "heading":
		level := 1
		if v, ok := n.Attrs["level"].(float64); ok && v >= 1 && v <= 6 {
			level = int(v)
		}
		tag := "h" + itoa(level)
		b.WriteString("<" + tag + ">")
		renderInline(b, n.Content, marks)
		b.WriteString("</" + tag + ">")
	case "bulletList":
		b.WriteString("<ul>")
		renderNodes(b, n.Content, marks)
		b.WriteString("</ul>")
	case "orderedList":
		b.WriteString("<ol>")
		renderNodes(b, n.Content, marks)
		b.WriteString("</ol>")
	case "listItem":
		b.WriteString("<li>")
		renderNodes(b, n.Content, marks)
		b.WriteString("</li>")
	case "codeBlock":
		b.WriteString("<pre><code>")
		b.WriteString(html.EscapeString(collectText(n)))
		b.WriteString("</code></pre>")
	case "blockquote":
		b.WriteString("<blockquote>")
		renderNodes(b, n.Content, marks)
		b.WriteString("</blockquote>")
	case "hardBreak":
		b.WriteString("<br>")
	case "table":
		b.WriteString(`<table class="adf-table">`)
		renderNodes(b, n.Content, marks)
		b.WriteString("</table>")
	case "tableRow":
		b.WriteString("<tr>")
		renderNodes(b, n.Content, marks)
		b.WriteString("</tr>")
	case "tableHeader":
		b.WriteString("<th>")
		renderNodes(b, n.Content, marks)
		b.WriteString("</th>")
	case "tableCell":
		b.WriteString("<td>")
		renderNodes(b, n.Content, marks)
		b.WriteString("</td>")
	case "mention":
		name, _ := n.Attrs["text"].(string)
		id, _ := n.Attrs["id"].(string)
		if name == "" {
			name = id
		}
		b.WriteString(`<span class="mention" data-account-id="` + html.EscapeString(id) + `">@` + html.EscapeString(name) + `</span>`)
	case "emoji":
		short := ""
		if v, ok := n.Attrs["shortcode"].(string); ok {
			short = v
		}
		if txt, ok := n.Attrs["text"].(string); ok && txt != "" {
			b.WriteString(html.EscapeString(txt))
		} else {
			b.WriteString(":" + html.EscapeString(short) + ":")
		}
	case "mediaSingle", "media":
		b.WriteString(`<span class="adf-media">[attachment]</span>`)
	case "text":
		renderText(b, n, marks)
	default:
		renderInline(b, n.Content, marks)
	}
}

func renderInline(b *strings.Builder, nodes []Node, marks map[string]bool) {
	for _, n := range nodes {
		renderNode(b, n, marks)
	}
}

func renderText(b *strings.Builder, n Node, marks map[string]bool) {
	text := html.EscapeString(n.Text)
	link := ""
	active := map[string]bool{}
	for _, m := range n.Mark {
		switch m.Type {
		case "strong":
			active["strong"] = true
		case "em":
			active["em"] = true
		case "code":
			active["code"] = true
		case "link":
			if href, ok := m.Attrs["href"].(string); ok {
				link = href
			}
		}
	}
	if link != "" {
		text = `<a href="` + html.EscapeString(link) + `" rel="noopener noreferrer">` + text + `</a>`
	}
	if active["code"] {
		text = "<code>" + text + "</code>"
	}
	if active["strong"] {
		text = "<strong>" + text + "</strong>"
	}
	if active["em"] {
		text = "<em>" + text + "</em>"
	}
	b.WriteString(text)
}

func collectText(n Node) string {
	var b strings.Builder
	if n.Text != "" {
		b.WriteString(n.Text)
		b.WriteString("\n")
	}
	for _, c := range n.Content {
		b.WriteString(collectText(c))
	}
	return b.String()
}

func itoa(i int) string {
	return strconv.Itoa(i)
}

// ---- Builders ----

func Text(text string) Node { return Node{Type: "text", Text: text} }

func Strong(text string) Node {
	return Node{Type: "text", Text: text, Mark: []Mark{{Type: "strong"}}}
}

func Em(text string) Node {
	return Node{Type: "text", Text: text, Mark: []Mark{{Type: "em"}}}
}

func Link(text, href string) Node {
	return Node{Type: "text", Text: text, Mark: []Mark{{Type: "link", Attrs: map[string]any{"href": href}}}}
}

func Paragraph(content ...Node) Node {
	return Node{Type: "paragraph", Content: content}
}

func ParagraphText(text string) Node {
	if text == "" {
		return Node{Type: "paragraph"}
	}
	return Paragraph(Text(text))
}

// HardBreak is a line break inside a paragraph.
type hardBreakNode = Node

func HardBreak() Node { return Node{Type: "hardBreak"} }

// Doc builds a document from blocks.
func Doc(blocks ...Node) json.RawMessage {
	raw, _ := json.Marshal(Node{Type: "doc", Attrs: map[string]any{"version": 1}, Content: blocks})
	return raw
}

// ParagraphDoc is the plain-text convenience used by forms in V1.
func ParagraphDoc(text string) json.RawMessage {
	return Doc(ParagraphText(text))
}

// PlainText extracts raw text (used for search snippets and fallbacks).
func PlainText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var doc Node
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	return strings.TrimRight(collectText(doc), "\n")
}

// Equal compares two ADF documents semantically (by re-encoding canonical form).
func Equal(a, b json.RawMessage) bool {
	var na, nb Node
	if json.Unmarshal(a, &na) != nil || json.Unmarshal(b, &nb) != nil {
		return string(a) == string(b)
	}
	ea, _ := json.Marshal(na)
	eb, _ := json.Marshal(nb)
	return string(ea) == string(eb)
}

// supportedNode reports whether a node type survives Normalize.
func supportedNode(t string) bool {
	switch t {
	case "doc", "paragraph", "heading", "bulletList", "orderedList", "listItem",
		"codeBlock", "blockquote", "hardBreak", "table", "tableRow",
		"tableHeader", "tableCell", "mention", "emoji", "text":
		return true
	}
	return false
}

func supportedMark(t string) bool {
	switch t {
	case "strong", "em", "code", "link", "underline", "strike":
		return true
	}
	return false
}

// Normalize strips nodes and marks outside the supported subset, replacing
// unknown blocks with their plain text (lossy but safe). Stored documents keep
// their original fidelity elsewhere; this is the ingest guarantee.
func Normalize(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var doc Node
	if err := json.Unmarshal(raw, &doc); err != nil {
		return raw
	}
	out, changed := normalizeNode(doc)
	if !changed {
		return raw
	}
	b, err := json.Marshal(out)
	if err != nil {
		return raw
	}
	return b
}

func normalizeNode(n Node) (Node, bool) {
	changed := false
	if !supportedNode(n.Type) && n.Type != "doc" {
		texts := collectTextNodes(n)
		if len(texts) == 0 {
			return Node{}, true // unsupported leaf with no text: drop
		}
		return Node{Type: "paragraph", Content: texts}, true
	}
	var marks []Mark
	for _, m := range n.Mark {
		if supportedMark(m.Type) {
			marks = append(marks, m)
		} else {
			changed = true
		}
	}
	n.Mark = marks
	if len(n.Content) > 0 {
		var kept []Node
		for _, c := range n.Content {
			nc, ch := normalizeNode(c)
			changed = changed || ch
			if nc.Type != "" {
				kept = append(kept, nc)
			}
		}
		n.Content = kept
	}
	return n, changed
}

// collectTextNodes flattens an unsupported node into plain text nodes.
func collectTextNodes(n Node) []Node {
	var out []Node
	if n.Text != "" {
		out = append(out, Text(n.Text))
	}
	for _, c := range n.Content {
		out = append(out, collectTextNodes(c)...)
	}
	return out
}
