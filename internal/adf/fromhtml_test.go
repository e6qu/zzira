package adf

import "strings"
import "testing"

// TestFromHTMLBuildsTheDocumentItRenders checks the inverse of ToHTML for the
// structures this product produces: what goes in comes back out.
func TestFromHTMLBuildsTheDocumentItRenders(t *testing.T) {
	source := `<h2>Steps</h2><p>Do <strong>this</strong> then <a href="https://x.test">that</a>.</p><ul><li>one</li><li>two</li></ul>`
	doc := FromHTML(source)
	rendered := ToHTML(doc)
	for _, want := range []string{"<h2>Steps</h2>", "<strong>this</strong>", `href="https://x.test"`, "<li><p>one</p></li>"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("%q missing from the round trip: %s", want, rendered)
		}
	}
}

// TestFromHTMLDegradesUnknownMarkup keeps the text of an element it does not
// model, because losing the words would be worse than losing the formatting.
func TestFromHTMLDegradesUnknownMarkup(t *testing.T) {
	doc := FromHTML(`<p>before <ac:structured-macro ac:name="info">inside</ac:structured-macro> after</p>`)
	rendered := ToHTML(doc)
	for _, want := range []string{"before", "inside", "after"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("%q was lost: %s", want, rendered)
		}
	}
}

// TestFromHTMLAlwaysProducesADocument means a conversion never answers nothing.
func TestFromHTMLAlwaysProducesADocument(t *testing.T) {
	for _, source := range []string{"", "   ", "<p></p>"} {
		doc := FromHTML(source)
		if !strings.HasPrefix(string(doc), `{"type":"doc"`) {
			t.Fatalf("empty source produced %s", doc)
		}
	}
}
