package automation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// A rule the wiki starts answers in the wiki: it comments under the page that
// asked and labels it, and it does both as the rule actor.
func TestWikiActionsWriteOnThePageTheRuleRanFor(t *testing.T) {
	fx := newAutomationFixture(t)
	spaceKey := "ACT" + strings.ToUpper(store.NewID("s")[len(store.NewID("s"))-4:])
	space, err := fx.service.Commands.CreateWikiSpace(fx.ctx, fx.ws, fx.admin, spaceKey, "Answered pages", "Rules write here", false)
	if err != nil {
		t.Skip("the wiki space could not be created: " + err.Error())
	}
	rule := func(name, triggerType string, components ...map[string]any) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"rule": map[string]any{
			"actor": map[string]string{"actor": fx.admin, "type": "ACCOUNT_ID"}, "name": name, "state": "ENABLED",
			"canOtherRuleTrigger": false, "components": components,
			"trigger": map[string]any{"component": "TRIGGER", "type": triggerType, "schemaVersion": 1, "value": map[string]any{}},
		}, "connections": []any{}})
		if _, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	rule("Answer the page", "confluence.page.created",
		map[string]any{"component": "ACTION", "type": WikiCommentActionType,
			"value": map[string]string{"comment": "Read by {{rule.name}}: {{page.title}}"}},
		map[string]any{"component": "ACTION", "type": WikiLabelActionType,
			"value": map[string]string{"label": "read-by-a-rule"}})

	runner := &Runner{Service: fx.service}
	drain := func() {
		t.Helper()
		for range 25 {
			if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The runner starts looking at the head, so nothing before this counts.
	drain()

	page, err := fx.store.SaveWikiPage(fx.ctx, fx.ws, fx.admin, models.WikiPage{
		SpaceID: space.ID, Title: "Release checklist", Status: "current", Published: true,
		Body: models.WikiBody{Value: "<p>Steps</p>", Representation: "storage"},
	})
	if err != nil {
		t.Fatal(err)
	}
	drain()

	comments, err := fx.store.WikiFooterComments(fx.ctx, fx.ws, fx.admin, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	written := ""
	for _, comment := range comments {
		written += comment.Body.Value
	}
	if !strings.Contains(written, "Read by Answer the page: Release checklist") {
		t.Fatalf("the rule did not comment on the page it ran for: %q", written)
	}
	labels, err := fx.store.WikiPageLabels(fx.ctx, fx.ws, fx.admin, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, label := range labels {
		found = found || label.Name == "read-by-a-rule"
	}
	if !found {
		t.Fatalf("the rule did not label the page: %+v", labels)
	}

	// Running again labels nothing new: a label the page already carries is
	// not a change, so the run reports no action rather than a change.
	fresh, err := fx.store.WikiPage(fx.ctx, fx.ws, fx.admin, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	run := &claimedRun{WorkspaceID: fx.ws, ActorID: fx.admin, RuleName: "Answer the page"}
	run.TriggerData = json.RawMessage(`{"page":{"id":"` + fresh.ID + `"}}`)
	changed, err := runner.labelWikiPage(fx.ctx, run, wikiPageActionValue{Label: "read-by-a-rule"},
		func(text string) (string, error) { return text, nil })
	if err != nil || changed {
		t.Fatalf("labelling again reported changed=%v, err=%v", changed, err)
	}
	// An action with no page to write on says so rather than guessing.
	empty := &claimedRun{WorkspaceID: fx.ws, ActorID: fx.admin}
	if _, err := runner.labelWikiPage(fx.ctx, empty, wikiPageActionValue{Label: "x"},
		func(text string) (string, error) { return text, nil }); err == nil {
		t.Fatal("a label action with no page was accepted")
	}

	// Writing at the end of a page keeps what was already on it, in a new
	// version, and archiving it takes it out of the space.
	plain := func(text string) (string, error) { return text, nil }
	changed, err = runner.appendToWikiPage(fx.ctx, run, wikiPageActionValue{Body: "Checked by {{rule.name}}"}, plain)
	if err != nil || !changed {
		t.Fatalf("appending reported changed=%v, err=%v", changed, err)
	}
	after, err := fx.store.WikiPage(fx.ctx, fx.ws, fx.admin, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(after.Body.Value, "Steps") || !strings.Contains(after.Body.Value, "Checked by") {
		t.Fatalf("the page now reads %q", after.Body.Value)
	}
	if after.Version.Number <= fresh.Version.Number {
		t.Fatalf("appending left the page at version %d", after.Version.Number)
	}
	if _, err := runner.appendToWikiPage(fx.ctx, run, wikiPageActionValue{Body: "   "}, plain); err == nil {
		t.Fatal("appending nothing was accepted")
	}
	changed, err = runner.archiveWikiPage(fx.ctx, run, wikiPageActionValue{}, plain)
	if err != nil || !changed {
		t.Fatalf("archiving reported changed=%v, err=%v", changed, err)
	}
	archived, err := fx.store.WikiPage(fx.ctx, fx.ws, fx.admin, page.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != "archived" {
		t.Fatalf("the page is %q after the rule archived it", archived.Status)
	}
	// Archiving an archived page is not a change.
	if changed, err := runner.archiveWikiPage(fx.ctx, run, wikiPageActionValue{}, plain); err != nil || changed {
		t.Fatalf("archiving again reported changed=%v, err=%v", changed, err)
	}
}

// A rule that no work item started can still write a page -- which the docs
// have always said and the runner used to refuse -- and the page it writes
// does not start the rule again.
func TestCreatePageRunsWithoutAWorkItem(t *testing.T) {
	fx := newAutomationFixture(t)
	spaceKey := "NEW" + strings.ToUpper(store.NewID("s")[len(store.NewID("s"))-4:])
	space, err := fx.service.Commands.CreateWikiSpace(fx.ctx, fx.ws, fx.admin, spaceKey, "Raised pages", "Rules write here", false)
	if err != nil {
		t.Skip("the wiki space could not be created: " + err.Error())
	}
	body, _ := json.Marshal(map[string]any{"rule": map[string]any{
		"actor": map[string]string{"actor": fx.admin, "type": "ACCOUNT_ID"}, "name": "Follow the page", "state": "ENABLED",
		"canOtherRuleTrigger": false,
		"components": []map[string]any{{"component": "ACTION", "type": WikiPageActionType,
			"value": map[string]string{"spaceKey": spaceKey, "title": "Notes on {{page.title}}"}}},
		"trigger": map[string]any{"component": "TRIGGER", "type": "confluence.page.created", "schemaVersion": 1, "value": map[string]any{}},
	}, "connections": []any{}})
	if _, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, body); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Service: fx.service}
	drain := func() {
		t.Helper()
		for range 25 {
			if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
				t.Fatal(err)
			}
		}
	}
	drain()
	if _, err := fx.store.SaveWikiPage(fx.ctx, fx.ws, fx.admin, models.WikiPage{
		SpaceID: space.ID, Title: "Design review", Status: "current", Published: true,
		Body: models.WikiBody{Value: "<p>Read this</p>", Representation: "storage"},
	}); err != nil {
		t.Fatal(err)
	}
	drain()
	titles := func() string {
		t.Helper()
		rows, err := fx.store.Pool.Query(fx.ctx, `SELECT p.title FROM wiki_pages p WHERE p.space_id::text=$1 ORDER BY p.id`, space.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		found := []string{}
		for rows.Next() {
			var title string
			if err := rows.Scan(&title); err != nil {
				t.Fatal(err)
			}
			found = append(found, title)
		}
		return strings.Join(found, " | ")
	}
	got := titles()
	if !strings.Contains(got, "Notes on Design review") {
		t.Fatalf("a rule with no work item did not write its page: %s", got)
	}
	// The page the rule wrote is a page created, but the rule caused it: a
	// rule does not start itself.
	if strings.Contains(got, "Notes on Notes on") {
		t.Fatalf("the rule started itself: %s", got)
	}
}

// A failure says what the run was about, so it can be gone and looked at.
func TestRunSubjectNamesWhatTheRunWasAbout(t *testing.T) {
	for data, want := range map[string]string{
		`{"page":{"id":"9","title":"Release checklist"}}`: `the page "Release checklist"`,
		`{"blogPost":{"title":"News"}}`:                   `the blog post "News"`,
		`{"label":{"name":"urgent"},"pageId":"9"}`:        "page 9",
		`{"version":{"name":"2.0"}}`:                      `the version "2.0"`,
		`{"sprint":{"name":"Sprint 4"}}`:                  `the sprint "Sprint 4"`,
		`{"deletedIssue":{"key":"ZZ-7"}}`:                 "ZZ-7",
		``:                                                "the webhook's request",
		`not json`:                                        "the webhook's request",
	} {
		if got := runSubject(&claimedRun{TriggerData: json.RawMessage(data)}); got != want {
			t.Fatalf("%s = %q, want %q", data, got, want)
		}
	}
}

// The catalogue says which actions run without a work item, and the runner
// must agree with it: a page rule has no work item and still writes a page.
func TestActionsThatRunWithoutAWorkItem(t *testing.T) {
	for _, actionType := range []string{"jira.issue.create", WebRequestActionType, VariableActionType,
		WikiPageActionType, WikiCommentActionType, WikiLabelActionType, WikiAppendActionType, WikiArchiveActionType} {
		if !actionsWithoutWork[actionType] {
			t.Fatalf("%s needs a work item, and the docs say it does not", actionType)
		}
	}
	for _, actionType := range []string{"jira.issue.add-label", "jira.issue.comment", "jira.issue.transition", "jira.issue.delete"} {
		if actionsWithoutWork[actionType] {
			t.Fatalf("%s acts on a work item and cannot run without one", actionType)
		}
	}
}
