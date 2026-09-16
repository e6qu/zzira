// Package adf is the pure Go Atlassian Document Format subset: parse → safe
// HTML, plus builders. It compiles into both server and wasm targets and is
// the only rich-text code path, so the API's renderedFields, the SSR page,
// and the offline replica all agree byte-for-byte.
package adf

import (
	"encoding/json"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Node is a generic ADF node.
type Node struct {
	Type string `json:"type"`
	// Version is set on the document node alone.
	Version int            `json:"version,omitempty"`
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
	return render(raw, false)
}

// ToStorage renders a document as Confluence storage format: the markup
// ToHTML produces where the two agree, and Confluence's own elements for
// mentions, task lists and dates, with no presentation attributes.
func ToStorage(raw json.RawMessage) string {
	return render(raw, true)
}

func render(raw json.RawMessage, storage bool) string {
	if len(raw) == 0 {
		return ""
	}
	var doc Node
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	var b strings.Builder
	renderNodes(&b, doc.Content, map[string]bool{}, storage)
	return b.String()
}

func renderNodes(b *strings.Builder, nodes []Node, marks map[string]bool, storage bool) {
	for _, n := range nodes {
		renderNode(b, n, marks, storage)
	}
}

func renderNode(b *strings.Builder, n Node, marks map[string]bool, storage bool) {
	switch n.Type {
	case "paragraph":
		b.WriteString("<p>")
		renderInline(b, n.Content, marks, storage)
		b.WriteString("</p>")
	case "heading":
		level := 1
		if v, ok := n.Attrs["level"].(float64); ok && v >= 1 && v <= 6 {
			level = int(v)
		}
		tag := "h" + itoa(level)
		b.WriteString("<" + tag + ">")
		renderInline(b, n.Content, marks, storage)
		b.WriteString("</" + tag + ">")
	case "bulletList":
		b.WriteString("<ul>")
		renderNodes(b, n.Content, marks, storage)
		b.WriteString("</ul>")
	case "orderedList":
		b.WriteString("<ol>")
		renderNodes(b, n.Content, marks, storage)
		b.WriteString("</ol>")
	case "listItem":
		b.WriteString("<li>")
		renderNodes(b, n.Content, marks, storage)
		b.WriteString("</li>")
	case "codeBlock":
		b.WriteString("<pre><code>")
		b.WriteString(html.EscapeString(collectText(n)))
		b.WriteString("</code></pre>")
	case "blockquote":
		b.WriteString("<blockquote>")
		renderNodes(b, n.Content, marks, storage)
		b.WriteString("</blockquote>")
	case "hardBreak":
		if storage {
			b.WriteString("<br/>")
		} else {
			b.WriteString("<br>")
		}
	case "rule":
		if storage {
			b.WriteString("<hr/>")
		} else {
			b.WriteString("<hr>")
		}
	case "table":
		if storage {
			b.WriteString("<table>")
		} else {
			b.WriteString(`<table class="adf-table">`)
		}
		renderNodes(b, n.Content, marks, storage)
		b.WriteString("</table>")
	case "tableRow":
		b.WriteString("<tr>")
		renderNodes(b, n.Content, marks, storage)
		b.WriteString("</tr>")
	case "tableHeader":
		b.WriteString("<th>")
		renderNodes(b, n.Content, marks, storage)
		b.WriteString("</th>")
	case "tableCell":
		b.WriteString("<td>")
		renderNodes(b, n.Content, marks, storage)
		b.WriteString("</td>")
	case "mention":
		name, _ := n.Attrs["text"].(string)
		name = strings.TrimPrefix(name, "@")
		id, _ := n.Attrs["id"].(string)
		if storage {
			b.WriteString(`<ac:link><ri:user ri:account-id="` + html.EscapeString(id) + `" />`)
			if name != "" {
				b.WriteString(`<ac:plain-text-link-body>` + html.EscapeString(name) + `</ac:plain-text-link-body>`)
			}
			b.WriteString(`</ac:link>`)
			break
		}
		if name == "" {
			name = id
		}
		b.WriteString(`<span class="mention" data-account-id="` + html.EscapeString(id) + `">@` + html.EscapeString(name) + `</span>`)
	case "taskList":
		open, closing := `<ul class="adf-task-list">`, "</ul>"
		if storage {
			open, closing = "<ac:task-list>", "</ac:task-list>"
		}
		b.WriteString(open)
		for i, item := range n.Content {
			// Storage names every task; an item without a local id is named
			// by its place in the list.
			if id, _ := item.Attrs["localId"].(string); item.Type == "taskItem" && id == "" {
				attrs := map[string]any{"localId": strconv.Itoa(i + 1)}
				for key, value := range item.Attrs {
					if key != "localId" {
						attrs[key] = value
					}
				}
				item.Attrs = attrs
			}
			renderNode(b, item, marks, storage)
		}
		b.WriteString(closing)
	case "taskItem":
		id, _ := n.Attrs["localId"].(string)
		done := n.Attrs["state"] == "DONE"
		if storage {
			status := "incomplete"
			if done {
				status = "complete"
			}
			b.WriteString("<ac:task><ac:task-id>" + html.EscapeString(id) + "</ac:task-id><ac:task-status>" + status + "</ac:task-status><ac:task-body>")
			renderInline(b, n.Content, marks, storage)
			b.WriteString("</ac:task-body></ac:task>")
			break
		}
		box := "☐ "
		if done {
			box = "☑ "
		}
		b.WriteString("<li>" + box)
		renderInline(b, n.Content, marks, storage)
		b.WriteString("</li>")
	case "date":
		var millis int64
		switch value := n.Attrs["timestamp"].(type) {
		case string:
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return
			}
			millis = parsed
		case float64:
			millis = int64(value)
		default:
			return
		}
		day := time.UnixMilli(millis).UTC().Format("2006-01-02")
		if storage {
			b.WriteString(`<time datetime="` + day + `" />`)
		} else {
			b.WriteString(`<time datetime="` + day + `">` + day + `</time>`)
		}
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
		if storage {
			b.WriteString("[attachment]")
		} else {
			b.WriteString(`<span class="adf-media">[attachment]</span>`)
		}
	case "text":
		renderText(b, n, marks, storage)
	default:
		renderInline(b, n.Content, marks, storage)
	}
}

func renderInline(b *strings.Builder, nodes []Node, marks map[string]bool, storage bool) {
	for _, n := range nodes {
		renderNode(b, n, marks, storage)
	}
}

func renderText(b *strings.Builder, n Node, marks map[string]bool, storage bool) {
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
				link = safeHref(href)
			}
		}
	}
	if link != "" && storage {
		text = `<a href="` + html.EscapeString(link) + `">` + text + `</a>`
	} else if link != "" {
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

func safeHref(raw string) string {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch strings.ToLower(parsed.Scheme) {
	case "", "http", "https", "mailto":
		return raw
	default:
		return ""
	}
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
	raw, _ := json.Marshal(Node{Type: "doc", Version: 1, Content: blocks})
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

// MentionedAccounts lists the account ids a document mentions, once each, in
// document order.
func MentionedAccounts(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var doc Node
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var accounts []string
	var walk func(Node)
	walk = func(n Node) {
		if n.Type == "mention" {
			if id, _ := n.Attrs["id"].(string); id != "" && !seen[id] {
				seen[id] = true
				accounts = append(accounts, id)
			}
		}
		for _, child := range n.Content {
			walk(child)
		}
	}
	walk(doc)
	return accounts
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
		"tableHeader", "tableCell", "mention", "emoji", "text", "taskList", "taskItem", "date", "rule":
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
