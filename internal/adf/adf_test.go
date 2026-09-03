package adf

import (
	"encoding/json"
	"testing"
)

func TestToHTMLTables(t *testing.T) {
	doc := Doc(
		ParagraphText("before"),
		Node{Type: "table", Content: []Node{
			{Type: "tableRow", Content: []Node{
				{Type: "tableHeader", Content: []Node{Text("Name")}},
				{Type: "tableHeader", Content: []Node{Text("Qty")}},
			}},
			{Type: "tableRow", Content: []Node{
				{Type: "tableCell", Content: []Node{Text("bolt")}},
				{Type: "tableCell", Content: []Node{Text("12")}},
			}},
		}},
	)
	got := ToHTML(doc)
	want := `<p>before</p><table class="adf-table"><tr><th>Name</th><th>Qty</th></tr><tr><td>bolt</td><td>12</td></tr></table>`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestToHTMLMentionEmojiHardBreak(t *testing.T) {
	doc := Doc(Node{Type: "paragraph", Content: []Node{
		Text("hi "),
		{Type: "mention", Attrs: map[string]any{"id": "usr_1", "text": "Demo User"}},
		{Type: "emoji", Attrs: map[string]any{"shortcode": "grin", "text": "\U0001F604"}},
		HardBreak(),
		Text("next"),
	}})
	got := ToHTML(doc)
	want := `<p>hi <span class="mention" data-account-id="usr_1">@Demo User</span>` + "\U0001F604" + `<br>next</p>`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestToHTMLUnknownNodeDegrades(t *testing.T) {
	doc := Doc(Node{Type: "panel", Content: []Node{
		Node{Type: "paragraph", Content: []Node{Text("inside unknown")}},
	}})
	got := ToHTML(doc)
	if got != "<p>inside unknown</p>" {
		t.Fatalf("got %s", got)
	}
}

func TestNormalizeStripsUnsupportedMarks(t *testing.T) {
	raw, _ := json.Marshal(Doc(
		Paragraph(
			Node{Type: "text", Text: "kept", Mark: []Mark{{Type: "strong"}, {Type: "color", Attrs: map[string]any{"color": "#ff0000"}}}},
			Node{Type: "status", Attrs: map[string]any{"text": "new"}},
		),
	))
	norm := Normalize(raw)
	var doc Node
	if err := json.Unmarshal(norm, &doc); err != nil {
		t.Fatal(err)
	}
	p := doc.Content[0]
	if len(p.Content) != 1 {
		t.Fatalf("unsupported status node should collapse to text: %+v", p)
	}
	marks := p.Content[0].Mark
	if len(marks) != 1 || marks[0].Type != "strong" {
		t.Fatalf("marks = %+v", marks)
	}
}

func TestNormalizePassthroughOnSupportedDoc(t *testing.T) {
	raw := ParagraphDoc("plain")
	if string(Normalize(raw)) != string(raw) {
		t.Fatal("supported docs must round-trip byte-identical")
	}
}

func TestXSSEscape(t *testing.T) {
	doc := Doc(Paragraph(Link("click", `javascript:alert("x")`)))
	got := ToHTML(doc)
	if contains(got, "href=") || contains(got, "javascript:") {
		t.Fatalf("unsafe link rendered: %s", got)
	}
}

func TestSafeLinkSchemes(t *testing.T) {
	for _, href := range []string{"https://example.com/a?q=1", "http://example.com", "mailto:team@example.com", "/browse/ZZ-1", "#details"} {
		got := ToHTML(Doc(Paragraph(Link("click", href))))
		if !contains(got, `href="`) {
			t.Errorf("safe href %q was removed: %s", href, got)
		}
	}
	for _, href := range []string{"data:text/html,<script>alert(1)</script>", "vbscript:msgbox(1)", " javascript:alert(1)"} {
		got := ToHTML(Doc(Paragraph(Link("click", href))))
		if contains(got, "href=") {
			t.Errorf("unsafe href %q rendered: %s", href, got)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
