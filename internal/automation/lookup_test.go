package automation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

// A rule asks a question of the site, says how many it found, and branches
// over what it found to act on each one.
func TestLookupKeepsWhatItFoundForTheRestOfTheRule(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx,
		`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'LKP','Lookups','wf_default',$3)`,
		projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	create := func(summary string, labels []string) string {
		t.Helper()
		issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, summary,
			json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", labels, nil, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return issue.Key
	}
	trigger := create("Release train", nil)
	first := create("Late one", []string{"late"})
	second := create("Late two", []string{"late"})

	components := []map[string]any{
		{"component": "ACTION", "type": LookupActionType, "value": map[string]any{"jql": "labels = late ORDER BY created ASC"}},
		{"component": "ACTION", "type": "jira.issue.comment", "value": map[string]string{"comment": "Found {{lookupIssues.size}} late"}},
		{"component": "ACTION", "type": "jira.issue.comment", "value": map[string]string{"comment": "They are {{lookupIssues}}"}},
		{"component": "BRANCH", "type": "jira.issue.related",
			"value": map[string]any{"relatedType": listBranchType, "smartValue": "{{lookupIssues}}", "variableName": "late"},
			"children": []map[string]any{
				{"component": "ACTION", "type": "jira.issue.comment", "value": map[string]string{
					"comment": "Chasing {{late.key}}: {{late.summary}}"}},
			}},
	}
	raw, _ := json.Marshal(ruleBody("Chase the late work", fx.admin, "ENABLED", "key = "+trigger, components))
	uuid, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.service.EnqueueNow(fx.ctx, fx.ws, uuid); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Service: fx.service}
	for range 3 {
		if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
			t.Fatal(err)
		}
	}
	comments := func(key string) string {
		t.Helper()
		rows, err := fx.store.Pool.Query(fx.ctx,
			`SELECT c.body::text FROM comments c JOIN issues i ON i.id=c.issue_id WHERE i.key=$1 AND i.workspace_id=$2`, key, fx.ws)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		written := []string{}
		for rows.Next() {
			var body string
			if err := rows.Scan(&body); err != nil {
				t.Fatal(err)
			}
			written = append(written, body)
		}
		return strings.Join(written, " | ")
	}
	// The count is on the work item the rule ran for.
	if got := comments(trigger); !strings.Contains(got, "Found 2 late") {
		t.Fatalf("the rule did not say how many it found: %s", got)
	}
	// Read as text, the lookup is the work it found.
	if got := comments(trigger); !strings.Contains(got, "They are "+first+", "+second) {
		t.Fatalf("the lookup did not read as the work it found: %s", got)
	}
	// Each comment the branch wrote is on the work item it ran for, because
	// an item that names a work item becomes the work item of that pass.
	if got := comments(first); !strings.Contains(got, "Chasing "+first+": Late one") {
		t.Fatalf("the branch did not act on the first work item it found: %s", got)
	}
	if got := comments(second); !strings.Contains(got, "Chasing "+second+": Late two") {
		t.Fatalf("the branch did not act on the second work item it found: %s", got)
	}
	if got := comments(trigger); strings.Contains(got, "Chasing ") {
		t.Fatalf("the branch wrote on the work item the rule ran for: %s", got)
	}
}

// A lookup that cannot work says so when the rule is written.
func TestLookupActionsAreValidated(t *testing.T) {
	for _, value := range []string{
		`{}`,
		`{"jql":"   "}`,
		`{"jql":"labels = late","limit":-1}`,
		`{"jql":"labels = late","limit":101}`,
	} {
		if _, err := validateLookupAction(json.RawMessage(value)); err == nil {
			t.Fatalf("%s was accepted", value)
		}
	}
	if _, err := validateLookupAction(json.RawMessage(`{"jql":"labels = late","limit":10}`)); err != nil {
		t.Fatalf("a lookup of ten was refused: %v", err)
	}
	payload := `{"rule":{"components":[{"component":"ACTION","type":"jira.issue.lookup","value":{"jql":""}}]}}`
	if _, err := ruleComponents(json.RawMessage(payload)); err == nil {
		t.Fatal("a lookup with no query was accepted")
	}
}
