package automation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// A page written in the wiki starts a rule, which raises the work the page
// asks for; a page in a space the rule actor cannot read starts nothing.
func TestWikiEventsStartRules(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx,
		`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'WIK','Wiki rules','wf_default',$3)`,
		projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	spaceKey := "AUT" + strings.ToUpper(store.NewID("s")[len(store.NewID("s"))-4:])
	open, err := fx.service.Commands.CreateWikiSpace(fx.ctx, fx.ws, fx.admin, spaceKey, "Automation pages", "Read by rules", false)
	if err != nil {
		t.Skip("the wiki space could not be created: " + err.Error())
	}
	// A private space only the admin is in, to check that a rule whose actor
	// is not in it never reads what it holds.
	privateKey := "PRV" + strings.ToUpper(store.NewID("s")[len(store.NewID("s"))-4:])
	shut, err := fx.service.Commands.CreateWikiSpace(fx.ctx, fx.ws, fx.admin, privateKey, "Admins only", "Not for every rule", true)
	if err != nil {
		t.Skip("the private wiki space could not be created: " + err.Error())
	}

	mustRuleAs := func(actor, name, triggerType, summary string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"rule": map[string]any{
			"actor": map[string]string{"actor": actor, "type": "ACCOUNT_ID"}, "name": name, "state": "ENABLED",
			"canOtherRuleTrigger": false,
			"components": []map[string]any{{"component": "ACTION", "type": "jira.issue.create",
				"value": map[string]string{"issueTypeId": "it_task", "projectId": projectID, "summary": summary}}},
			"trigger": map[string]any{"component": "TRIGGER", "type": triggerType, "schemaVersion": 1, "value": map[string]any{}},
		}, "connections": []any{}})
		if _, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	mustRule := func(name, triggerType, summary string) { mustRuleAs(fx.admin, name, triggerType, summary) }
	mustRule("Page written", "confluence.page.created", "Review {{page.title}}")
	mustRule("Page changed", "confluence.page.updated", "Re-read {{page.title}} v{{page.version.number}}")
	mustRule("Page commented", "confluence.page.commented", "Answer the comment on the wiki")
	mustRule("Page labelled", "confluence.page.labelled", "Sort the {{label.name}} page")
	// The same event, for a rule that runs as somebody who is not in the
	// private space.
	mustRuleAs(fx.member, "Page written, as a member", "confluence.page.created", "Member read {{page.title}}")

	runner := &Runner{Service: fx.service}
	drain := func() {
		t.Helper()
		for range 25 {
			if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The runner reads events from where it last looked, and starts looking
	// at the head: a rule never runs for what happened before it existed.
	drain()

	page, err := fx.store.SaveWikiPage(fx.ctx, fx.ws, fx.admin, models.WikiPage{
		SpaceID: open.ID, Title: "Release checklist", Status: "current", Published: true,
		Body: models.WikiBody{Value: "<p>Steps</p>", Representation: "storage"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.store.SaveWikiPage(fx.ctx, fx.ws, fx.admin, models.WikiPage{
		SpaceID: shut.ID, Title: "Secret plans", Status: "current", Published: true,
		Body: models.WikiBody{Value: "<p>Shh</p>", Representation: "storage"},
	}); err != nil {
		t.Fatal(err)
	}
	summaries := func() string {
		t.Helper()
		rows, err := fx.store.Pool.Query(fx.ctx, `SELECT summary FROM issues WHERE workspace_id=$1 ORDER BY key`, fx.ws)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		found := []string{}
		for rows.Next() {
			var summary string
			if err := rows.Scan(&summary); err != nil {
				t.Fatal(err)
			}
			found = append(found, summary)
		}
		return strings.Join(found, " | ")
	}
	drain()
	if got := summaries(); !strings.Contains(got, "Review Release checklist") {
		t.Fatalf("a page written in the wiki started nothing: %s", got)
	}
	// The member's rule read the open page, so it can raise work at all --
	// and did not read the page in the space it is not in.
	if got := summaries(); !strings.Contains(got, "Member read Release checklist") {
		t.Fatalf("a rule running as a member started nothing for a page it can see: %s", got)
	}
	if got := summaries(); strings.Contains(got, "Member read Secret plans") {
		t.Fatalf("a rule read a page its actor cannot see: %s", got)
	}
	// The admin is in the private space, so its own rule did read that page.
	if got := summaries(); !strings.Contains(got, "Review Secret plans") {
		t.Fatalf("a rule whose actor is in the space did not read its page: %s", got)
	}

	// Changing the page is an update, not another creation. A save is
	// against the version that was read, so read it back first.
	fresh, err := fx.store.WikiPage(fx.ctx, fx.ws, fx.admin, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	fresh.Version.Number++
	fresh.Title = "Release checklist v2"
	fresh.Body = models.WikiBody{Value: "<p>More steps</p>", Representation: "storage"}
	if _, err := fx.store.SaveWikiPage(fx.ctx, fx.ws, fx.admin, *fresh); err != nil {
		t.Fatal(err)
	}
	drain()
	got := summaries()
	if !strings.Contains(got, "Re-read Release checklist v2 v2") {
		t.Fatalf("a changed page did not start the update rule: %s", got)
	}
	if strings.Count(got, "Review Release checklist") != 1 {
		t.Fatalf("a changed page was read as a new one: %s", got)
	}

	// A comment on the page, and a label, each start their own rule.
	if _, err := fx.store.CreateWikiFooterComment(fx.ctx, fx.ws, fx.admin, models.WikiFooterComment{
		PageID: page.ID, Body: models.WikiBody{Value: "<p>Looks right</p>", Representation: "storage"},
	}); err != nil {
		t.Fatal(err)
	}
	drain()
	if got := summaries(); !strings.Contains(got, "Answer the comment on the wiki") {
		t.Fatalf("a comment on a page started nothing: %s", got)
	}

	// Labelling the page starts the rule that watches for labels, and the
	// rule reads back which label it was.
	if _, err := fx.store.AddWikiPageLabels(fx.ctx, fx.ws, fx.admin, page.ID, []models.WikiLabel{{Name: "urgent", Prefix: "global"}}); err != nil {
		t.Fatal(err)
	}
	drain()
	if got := summaries(); !strings.Contains(got, "Sort the urgent page") {
		t.Fatalf("labelling a page started nothing: %s", got)
	}
}

// The log says what happened; these are the readings a rule triggers on.
func TestWikiActionEvents(t *testing.T) {
	page := func(published bool, status string) []byte {
		raw, _ := json.Marshal(map[string]any{"wikiSpaceId": "1", "wiki_page": map[string]any{
			"id": "9", "title": "A page", "published": published, "status": status,
		}})
		return raw
	}
	cases := []struct {
		name   string
		action loggedAction
		want   string
	}{
		{"a page written", loggedAction{EntityType: entityWikiPage, Op: "upsert", First: true, Payload: page(true, "current")}, "page_created"},
		{"a page changed", loggedAction{EntityType: entityWikiPage, Op: "upsert", Payload: page(true, "current")}, "page_updated"},
		{"a draft", loggedAction{EntityType: entityWikiPage, Op: "upsert", First: true, Payload: page(false, "draft")}, ""},
		{"a trashed page", loggedAction{EntityType: entityWikiPage, Op: "upsert", Payload: page(true, "trashed")}, ""},
		{"a label attached", loggedAction{EntityType: entityWikiLabel, Op: "upsert", First: true,
			Payload: []byte(`{"wikiSpaceId":"1","wiki_label":{"id":"1","name":"urgent","pageId":"9","attached":true}}`)}, "page_labelled"},
		{"a label removed", loggedAction{EntityType: entityWikiLabel, Op: "upsert",
			Payload: []byte(`{"wikiSpaceId":"1","wiki_label":{"id":"1","name":"urgent","pageId":"9","attached":false}}`)}, ""},
		{"a label on an attachment", loggedAction{EntityType: entityWikiLabel, Op: "upsert", First: true,
			Payload: []byte(`{"wikiSpaceId":"1","wiki_label":{"id":"1","name":"urgent","attachmentId":"3","attached":true}}`)}, ""},
		{"a comment on a page", loggedAction{EntityType: entityWikiFooterComment, Op: "upsert", First: true,
			Payload: []byte(`{"wikiSpaceId":"1","wiki_footer_comment":{"id":"2","pageId":"9"}}`)}, "page_commented"},
		{"a comment on a blog post", loggedAction{EntityType: entityWikiFooterComment, Op: "upsert", First: true,
			Payload: []byte(`{"wikiSpaceId":"1","wiki_footer_comment":{"id":"2","blogPostId":"4"}}`)}, ""},
		{"a comment edited", loggedAction{EntityType: entityWikiFooterComment, Op: "upsert",
			Payload: []byte(`{"wikiSpaceId":"1","wiki_footer_comment":{"id":"2","pageId":"9"}}`)}, ""},
		{"a blog post published", loggedAction{EntityType: entityWikiBlogPost, Op: "upsert", First: true,
			Payload: []byte(`{"wikiSpaceId":"1","wiki_blogpost":{"id":"4","title":"News","published":true,"status":"current"}}`)}, "blogpost_created"},
	}
	for _, test := range cases {
		if got := wikiActionEvent(test.action); got != test.want {
			t.Fatalf("%s = %q, want %q", test.name, got, test.want)
		}
	}
	// What a run carries is what its actions read back.
	data := wikiTriggerData("page_created", loggedAction{EntityType: entityWikiPage, First: true, Payload: page(true, "current")})
	if wikiPageID(data) != "9" {
		t.Fatalf("the run does not name the page it is about: %s", data)
	}
	label := wikiTriggerData("page_labelled", loggedAction{EntityType: entityWikiLabel,
		Payload: []byte(`{"wiki_label":{"id":"1","name":"urgent","pageId":"9","attached":true}}`)})
	if wikiPageID(label) != "9" || !strings.Contains(string(label), "urgent") {
		t.Fatalf("a labelled run carries %s", label)
	}
}
