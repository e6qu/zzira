package wikimarkup

import (
	"strings"
	"testing"
)

func TestFromNotation(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"heading and paragraph", "h2. Release plan\nShip it *today*.", "<h2>Release plan</h2><p>Ship it <strong>today</strong>.</p>"},
		{"line breaks join a paragraph", "first line\nsecond line", "<p>first line<br />second line</p>"},
		{"emphasis, strike, underline and mono", "_calm_ -old- +new+ {{go test}}", "<p><em>calm</em> <del>old</del> <u>new</u> <code>go test</code></p>"},
		{"hyphenated words stay words", "a well-known long-running job", "<p>a well-known long-running job</p>"},
		{"bullet and nested numbered lists", "* one\n** one a\n* two\n# first", "<ul><li>one<ul><li>one a</li></ul></li><li>two</li></ul><ol><li>first</li></ol>"},
		{"links", "[Runbook|https://example.test/run?a=1&b=2] and [https://example.test]", `<p><a href="https://example.test/run?a=1&amp;b=2">Runbook</a> and <a href="https://example.test">https://example.test</a></p>`},
		{"unsafe links stay text", "[click|javascript:alert(1)]", "<p>[click|javascript:alert(1)]</p>"},
		{"markup is escaped", "<script>alert(1)</script>", "<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>"},
		{"code block keeps its text", "{code:go}\nif a < b {\n}\n{code}", "<pre>if a &lt; b {\n}</pre>"},
		{"quote and rule", "bq. Quoted\n----", "<blockquote><p>Quoted</p></blockquote><hr />"},
		{"table", "||Name||Owner||\n|API|Platform|", "<table><tbody><tr><th>Name</th><th>Owner</th></tr><tr><td>API</td><td>Platform</td></tr></tbody></table>"},
		{"bold sentence is not a list", "*Note* read this", "<p><strong>Note</strong> read this</p>"},
	} {
		if got := FromNotation(tc.in); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
	if _, err := Render(FromNotation("h1. Title\n* item\n[link|https://example.test]\n{code}x{code}\n||a||\n|b|")); err != nil {
		t.Fatalf("converted markup is not valid storage: %v", err)
	}
}

func TestMentions(t *testing.T) {
	storage := `<p>Thanks <ac:link><ri:user ri:account-id="usr_ana" /><ac:plain-text-link-body><![CDATA[Ana <Soursop>]]></ac:plain-text-link-body></ac:link> and <ac:link><ri:user ri:account-id="usr_bo"/></ac:link>, and again <ac:link><ri:user ri:account-id="usr_ana"/></ac:link>.</p>`
	rendered, err := Render(storage)
	if err != nil {
		t.Fatal(err)
	}
	want := `<p>Thanks <a href="/people/usr_ana">@Ana &lt;Soursop&gt;</a> and <a href="/people/usr_bo">@user</a>, and again <a href="/people/usr_ana">@user</a>.</p>`
	if rendered != want {
		t.Fatalf("rendered mention:\n got %q\nwant %q", rendered, want)
	}
	if got := MentionedAccounts(storage); len(got) != 2 || got[0] != "usr_ana" || got[1] != "usr_bo" {
		t.Fatalf("mentioned accounts: %v", got)
	}
	if text, err := Text(storage); err != nil || !strings.Contains(text, "Ana <Soursop>") {
		t.Fatalf("mention text: %q %v", text, err)
	}
	for _, bad := range []string{
		`<p><ac:link><ri:page ri:content-title="Other"/></ac:link></p>`,
		`<p><ac:link><ri:user ri:account-id="../x"/></ac:link></p>`,
		`<p><ac:link><ri:user ri:account-id="usr_a" ri:onclick="x"/></ac:link></p>`,
		`<p><ac:link><ri:user ri:account-id="usr_a"/>loose text</ac:link></p>`,
		`<p><ri:user ri:account-id="usr_a"/></p>`,
	} {
		if _, err := Render(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestLabelMentions(t *testing.T) {
	body := `<p>Ask <ac:link><ri:user ri:account-id="u1" /></ac:link> and <ac:link><ri:user ri:account-id="u2"/></ac:link> or <ac:link><ri:user ri:account-id="u1" /><ac:plain-text-link-body>Kept</ac:plain-text-link-body></ac:link></p>`
	labelled := LabelMentions(body, map[string]string{"u1": "Ana <Ops>"})
	rendered, err := Render(labelled)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<p>Ask <a href="/people/u1">@Ana &lt;Ops&gt;</a> and <a href="/people/u2">@user</a> or <a href="/people/u1">@Kept</a></p>`; rendered != want {
		t.Fatalf("got %s", rendered)
	}
	if got := MentionedAccounts(labelled); len(got) != 2 {
		t.Fatalf("labelling changed who is mentioned: %v", got)
	}
}

func TestTasks(t *testing.T) {
	body := `<p>Plan</p><ac:task-list><ac:task><ac:task-id>1</ac:task-id><ac:task-status>incomplete</ac:task-status><ac:task-body>Ship <ac:link><ri:user ri:account-id="u1" /></ac:link> by <time datetime="2030-01-02" /></ac:task-body></ac:task><ac:task><ac:task-id>7</ac:task-id><ac:task-body><strong>Check</strong></ac:task-body></ac:task></ac:task-list>`
	rendered, err := Render(body)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<p>Plan</p><ul><li>☐ Ship <a href="/people/u1">@user</a> by <time datetime="2030-01-02">2030-01-02</time></li><li>☐ <strong>Check</strong></li></ul>`; rendered != want {
		t.Fatalf("rendered %s", rendered)
	}
	if text, err := Text(body); err != nil || text != "Plan Ship by Check" {
		t.Fatalf("text %q %v", text, err)
	}
	tasks, err := Tasks(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != "1" || tasks[0].Assignee != "u1" || tasks[0].Due != "2030-01-02" || tasks[0].Status != "incomplete" || !strings.HasPrefix(tasks[0].Body, "Ship <ac:link>") || tasks[1].Body != "<strong>Check</strong>" || tasks[1].Status != "incomplete" {
		t.Fatalf("tasks %+v", tasks)
	}
	if NextTaskID(tasks) != "8" {
		t.Fatal(NextTaskID(tasks))
	}
	done, err := SetTaskStatus(body, "1", "complete")
	if err != nil || !strings.Contains(done, "<ac:task-status>complete</ac:task-status>") {
		t.Fatalf("status %s %v", done, err)
	}
	done, err = SetTaskStatus(done, "7", "complete")
	if err != nil {
		t.Fatal(err)
	}
	if tasks, err = Tasks(done); err != nil || tasks[0].Status != "complete" || tasks[1].Status != "complete" {
		t.Fatalf("after status changes %+v %v", tasks, err)
	}
	if rendered, _ := Render(done); !strings.Contains(rendered, "<li>☑ <strong>Check</strong></li>") {
		t.Fatal(rendered)
	}
	for _, bad := range []string{
		`<ac:task-list><ac:task><ac:task-body>x</ac:task-body></ac:task></ac:task-list>`,
		`<ac:task-list><ac:task><ac:task-id>1</ac:task-id></ac:task><ac:task><ac:task-id>1</ac:task-id></ac:task></ac:task-list>`,
		`<ac:task-list><ac:task><ac:task-id>1</ac:task-id><ac:task-status>done</ac:task-status></ac:task></ac:task-list>`,
	} {
		if _, err := Tasks(bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	if _, err := Render(`<time datetime="tomorrow" />`); err == nil {
		t.Fatal("accepted a time that is not a date")
	}
}
