package adf

import (
	"encoding/json"
	"strings"
	"testing"
)

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

// Storage mentions, task lists and dates survive a trip through the document
// format, and the storage written back has no presentation markup.
func TestStorageTasksMentionsAndDates(t *testing.T) {
	storage := `<p>Owner <ac:link><ri:user ri:account-id="u1" /><ac:plain-text-link-body>Ana</ac:plain-text-link-body></ac:link><br/>next</p><ac:task-list><ac:task><ac:task-id>3</ac:task-id><ac:task-status>complete</ac:task-status><ac:task-body>Ship by <time datetime="2030-01-02" /></ac:task-body></ac:task></ac:task-list><p><a href="https://example.com">site</a> and <a href="https://example.org">more</a></p><table><tr><td>cell</td></tr></table>`
	doc := string(FromHTML(storage))
	for _, want := range []string{`"type":"mention"`, `"id":"u1"`, `"text":"@Ana"`, `"type":"taskList"`, `"localId":"3"`, `"state":"DONE"`, `"type":"date"`, `"timestamp":"1893542400000"`, `"type":"hardBreak"`, `"href":"https://example.org"`} {
		if !strings.Contains(doc, want) {
			t.Fatalf("document lacks %s: %s", want, doc)
		}
	}
	back := ToStorage(json.RawMessage(doc))
	want := `<p>Owner <ac:link><ri:user ri:account-id="u1" /><ac:plain-text-link-body>Ana</ac:plain-text-link-body></ac:link><br/>next</p><ac:task-list><ac:task><ac:task-id>3</ac:task-id><ac:task-status>complete</ac:task-status><ac:task-body>Ship by <time datetime="2030-01-02" /></ac:task-body></ac:task></ac:task-list><p><a href="https://example.com">site</a> and <a href="https://example.org">more</a></p>`
	if !strings.HasPrefix(back, want) {
		t.Fatalf("storage\n got %s\nwant %s", back, want)
	}
	if html := ToHTML(json.RawMessage(doc)); !strings.Contains(html, `<li>☑ Ship by <time datetime="2030-01-02">2030-01-02</time></li>`) || !strings.Contains(html, `>@Ana</span>`) {
		t.Fatalf("html %s", html)
	}
	unnamed := ToStorage(json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"taskList","attrs":{"localId":""},"content":[{"type":"taskItem","attrs":{"state":"TODO"},"content":[{"type":"text","text":"a"}]},{"type":"taskItem","attrs":{"state":"TODO"},"content":[{"type":"text","text":"b"}]}]}]}`))
	if !strings.Contains(unnamed, "<ac:task-id>1</ac:task-id>") || !strings.Contains(unnamed, "<ac:task-id>2</ac:task-id>") {
		t.Fatal(unnamed)
	}
}
