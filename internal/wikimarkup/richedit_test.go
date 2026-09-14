package wikimarkup

import "testing"

func TestRichEditable(t *testing.T) {
	for body, want := range map[string]bool{
		`<p>Plain <strong>text</strong> <a href="https://example.com">link</a></p>`:                                          true,
		`<p>Ask <ac:link><ri:user ri:account-id="u1" /><ac:plain-text-link-body>Ana</ac:plain-text-link-body></ac:link></p>`: true,
		`<ac:task-list><ac:task><ac:task-id>1</ac:task-id><ac:task-body>x</ac:task-body></ac:task></ac:task-list>`:           false,
		`<ac:structured-macro ac:name="info"><ac:rich-text-body><p>x</p></ac:rich-text-body></ac:structured-macro>`:          false,
		`<p>Due <time datetime="2030-01-02" /></p>`:                                                                          false,
		`<p>unclosed`: false,
	} {
		if got := RichEditable(body); got != want {
			t.Errorf("RichEditable(%s) = %v, want %v", body, got, want)
		}
	}
}
