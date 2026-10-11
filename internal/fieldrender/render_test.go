package fieldrender

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFieldRendererUsesFormattingWithoutExecutingMarkup(t *testing.T) {
	source := `*Bold* <script>alert(1)</script> [unsafe|javascript:alert(1)]`
	text := HTML(source, "jira-text-renderer")
	wiki := HTML(source, "atlassian-wiki-renderer")
	if !strings.Contains(text, "*Bold*") || strings.Contains(text, "<script>") || !strings.Contains(wiki, "<strong>Bold</strong>") || strings.Contains(wiki, "<script>") || strings.Contains(wiki, `href="javascript:`) {
		t.Fatalf("unsafe or wrong renderer: text=%s wiki=%s", text, wiki)
	}
	doc := json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Bold <script>","marks":[{"type":"strong"}]}]}]}`)
	if rich := HTML(doc, "atlassian-wiki-renderer"); !strings.Contains(rich, "<strong>") {
		t.Fatalf("rich ADF lost formatting: %s", rich)
	}
	if plain := HTML(doc, "jira-text-renderer"); strings.Contains(plain, "<strong>") || strings.Contains(plain, "<script>") || !strings.Contains(plain, "Bold &lt;script&gt;") {
		t.Fatalf("plain ADF unsafe or formatted: %s", plain)
	}
	if HTML(nil, "") != "" || HTML(map[string]string{"id": "picker"}, "atlassian-wiki-renderer") != "" {
		t.Fatal("nontext field was interpreted as a document")
	}
}
