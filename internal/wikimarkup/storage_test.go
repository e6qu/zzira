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
// macros: a panel is a box around its body with the title it was given, and a
// macro the site does not draw shows the body it wraps with its parameters
// left out. No ac element reaches the HTML.
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
	// The macro keeps its own identifier, so a body that goes through the
	// editor comes back as the one Confluence stored.
	if !strings.Contains(got, `<div class="wiki-panel wiki-panel-info" data-macro="info" data-macro-id="m-1">`) {
		t.Fatalf("the panel was not drawn: %s", got)
	}
	if !strings.Contains(got, `<p class="wiki-panel-title">Heads up</p>`) {
		t.Fatalf("the panel title was not shown: %s", got)
	}
	if !strings.Contains(got, "<p>Careful <strong>here</strong></p>") {
		t.Fatalf("the macro body was not rendered: %s", got)
	}
	// A macro the site draws no box for shows its body, and the parameters
	// that describe it stay out of the reading.
	other, err := Render(`<ac:structured-macro ac:name="expand"><ac:parameter ac:name="title">More</ac:parameter><ac:rich-text-body><p>Detail</p></ac:rich-text-body></ac:structured-macro>`)
	if err != nil {
		t.Fatal(err)
	}
	if other != "<p>Detail</p>" {
		t.Fatalf("unrecognised macro = %s", other)
	}
}

// TestStorageRendersStatusCodeAndContents covers the macros that are not a box
// around a body: a status word in its colour, a code block in its language,
// and a table of contents built from the headings the page holds.
func TestStorageRendersStatusCodeAndContents(t *testing.T) {
	got, err := Render(`<ac:structured-macro ac:name="status"><ac:parameter ac:name="colour">Green</ac:parameter><ac:parameter ac:name="title">Ready</ac:parameter></ac:structured-macro>`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `<span class="wiki-status wiki-status-green" data-macro="status" data-colour="green">Ready</span>` {
		t.Fatalf("status = %s", got)
	}
	// A colour Confluence does not know is drawn grey rather than refused.
	if got, err = Render(`<ac:structured-macro ac:name="status"><ac:parameter ac:name="colour">chartreuse</ac:parameter><ac:parameter ac:name="title">Ready</ac:parameter></ac:structured-macro>`); err != nil || !strings.Contains(got, "wiki-status-grey") {
		t.Fatalf("unknown status colour = %s, %v", got, err)
	}
	if got, err = Render(`<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">go</ac:parameter><ac:plain-text-body>if a &lt; b {}</ac:plain-text-body></ac:structured-macro>`); err != nil {
		t.Fatal(err)
	} else if got != `<pre class="wiki-code" data-macro="code" data-language="go">if a &lt; b {}</pre>` {
		t.Fatalf("code macro = %s", got)
	}
	got, err = Render(`<ac:structured-macro ac:name="toc"/><h2>First &amp; foremost</h2><p>x</p><h3>Then</h3>`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<nav class="wiki-toc" data-macro="toc" aria-label="Contents">`,
		`<li class="wiki-toc-level-2"><a href="#wiki-heading-1">First &amp; foremost</a></li>`,
		`<li class="wiki-toc-level-3"><a href="#wiki-heading-2">Then</a></li>`,
		`<h2 id="wiki-heading-1">`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("contents = %s, missing %s", got, want)
		}
	}
	if strings.Contains(got, "\x00") {
		t.Fatalf("the contents placeholder survived: %q", got)
	}
	if got, err = Render(`<ac:structured-macro ac:name="toc"/>`); err != nil || !strings.Contains(got, "no headings yet") {
		t.Fatalf("contents without headings = %s, %v", got, err)
	}
}

// TestStorageRendersLayoutSections covers Confluence's page layouts: a section
// of columns, each holding its own content.
func TestStorageRendersLayoutSections(t *testing.T) {
	got, err := Render(`<ac:layout><ac:layout-section ac:type="two_equal"><ac:layout-cell><p>Left</p></ac:layout-cell><ac:layout-cell><p>Right</p></ac:layout-cell></ac:layout-section></ac:layout>`)
	if err != nil {
		t.Fatal(err)
	}
	want := `<div class="wiki-layout" data-macro="layout"><div class="wiki-layout-section wiki-layout-two_equal" data-layout-type="two_equal">` +
		`<div class="wiki-layout-cell"><p>Left</p></div><div class="wiki-layout-cell"><p>Right</p></div></div></div>`
	if got != want {
		t.Fatalf("layout = %s", got)
	}
	if _, err := Render(`<ac:layout><ac:layout-section ac:type="seventeen"><ac:layout-cell><p>x</p></ac:layout-cell></ac:layout-section></ac:layout>`); err == nil {
		t.Fatal("accepted an unsupported layout section type")
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

// A macro identifier is written back onto the element it renders as, so only
// an identifier's own shape is accepted: nothing from a page body reaches an
// attribute where it could end the quoting.
func TestMacroIdentifiersAreOnlyEchoedWhenTheyAreIdentifiers(t *testing.T) {
	got, err := Render(`<ac:structured-macro ac:name="info" ac:macro-id="m-1"><ac:rich-text-body><p>x</p></ac:rich-text-body></ac:structured-macro>`)
	if err != nil || !strings.Contains(got, `data-macro-id="m-1"`) {
		t.Fatalf("a plain identifier = %s, %v", got, err)
	}
	for _, id := range []string{`" onclick="alert(1)`, "with space", strings.Repeat("x", 65), "quote\"inside"} {
		rendered, renderErr := Render(`<ac:structured-macro ac:name="info" ac:macro-id="` + strings.ReplaceAll(id, `"`, "&quot;") + `"><ac:rich-text-body><p>x</p></ac:rich-text-body></ac:structured-macro>`)
		if renderErr != nil {
			t.Fatalf("Render(%q) = %v", id, renderErr)
		}
		if strings.Contains(rendered, "data-macro-id") {
			t.Fatalf("identifier %q was written into an attribute: %s", id, rendered)
		}
		if strings.Contains(rendered, "onclick") {
			t.Fatalf("identifier %q reached the markup: %s", id, rendered)
		}
	}
}
