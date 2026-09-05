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
	for _, input := range []string{`<script>alert(1)</script>`, `<p onclick="alert(1)">x</p>`, `<a href="javascript:alert(1)">x</a>`, `<a href="//evil.test">x</a>`, `<a href="&#106;avascript:alert(1)">x</a>`, `<iframe src="https://evil.test"/>`, `<ac:structured-macro ac:name="x"/>`, `<!DOCTYPE x><p>x</p>`, `<p>broken`, `</root><script>x</script><root>`, strings.Repeat("<p>", 101) + strings.Repeat("</p>", 101)} {
		if _, err := Render(input); err == nil {
			t.Errorf("accepted unsafe or unsupported storage %q", input)
		}
	}
}
