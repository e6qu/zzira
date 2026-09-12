package wikimarkup

import (
	"strings"
	"testing"
)

func TestStorageRenderingPreservesFormattingAndRejectsExecutableMarkup(t *testing.T) {
	valid := `<h2>Decision &amp; rationale</h2><p><strong>Ship</strong> <a href="/browse/ZZ-1">ZZ-1</a></p><table><tbody><tr><td>Ready</td></tr></tbody></table><br/>`
	got, err := Render(valid)
	if err != nil || !strings.Contains(got, `<strong>Ship</strong>`) || !strings.Contains(got, `Decision &amp; rationale`) {
		t.Fatalf("%s: %v", got, err)
	}
	for _, input := range []string{`<script>alert(1)</script>`, `<p onclick="alert(1)">x</p>`, `<a href="javascript:alert(1)">x</a>`, `<a href="//evil.test">x</a>`, `<a href="&#106;avascript:alert(1)">x</a>`, `<iframe src="https://evil.test"/>`, `<ac:unknown-thing ac:name="x"/>`, `<ac:structured-macro ac:onclick="x"/>`, `<!DOCTYPE x><p>x</p>`, `<p>broken`, `</root><script>x</script><root>`, strings.Repeat("<p>", 101) + strings.Repeat("</p>", 101)} {
		if _, err := Render(input); err == nil {
			t.Errorf("accepted unsafe or unsupported storage %q", input)
		}
	}
}

// TestStorageRendersMacrosAsTheContentTheyShow covers Confluence's structured
// macros: the macro and its parameters are structure rather than markup, so a
// reader sees the body the macro wraps and none of the ac elements reach the
// HTML.
func TestStorageRendersMacrosAsTheContentTheyShow(t *testing.T) {
	storage := `<p>Intro</p><ac:structured-macro ac:name="info" ac:macro-id="m-1">` +
		`<ac:parameter ac:name="title">Heads up</ac:parameter>` +
		`<ac:rich-text-body><p>Careful <strong>here</strong></p></ac:rich-text-body>` +
		`</ac:structured-macro>`
	got, err := Render(storage)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "ac:") || strings.Contains(got, "structured-macro") {
		t.Fatalf("a macro element reached the rendering: %s", got)
	}
	if !strings.Contains(got, "<p>Careful <strong>here</strong></p>") {
		t.Fatalf("the macro body was not rendered: %s", got)
	}
	// A parameter configures the macro; it is not something a reader sees.
	if strings.Contains(got, "Heads up") {
		t.Fatalf("a macro parameter was shown to the reader: %s", got)
	}
}

func TestTextExtractsReadableContentFromValidatedStorage(t *testing.T) {
	got, err := Text(`<h2>Decision &amp; rationale</h2><p><strong>Ship</strong><br/>after verification</p>`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Decision & rationale Ship after verification" {
		t.Fatalf("Text() = %q", got)
	}
	if _, err := Text(`<script>alert(1)</script>`); err == nil {
		t.Fatal("Text accepted unsupported storage markup")
	}
}
