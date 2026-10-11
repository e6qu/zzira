package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/api3"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

func TestReporterEditTransitionAndRevocation(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx := context.Background()
	ws := f.handler.WorkspaceSlug
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.store.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`, `DELETE FROM workflow_schemes WHERE workspace_id=$1`, `DELETE FROM workflows WHERE workspace_id=$1`} {
			exec(query, ws)
		}
	})
	project, err := f.handler.Commands.CreateProject(ctx, f.ownerID, ws, commands.CreateProjectInput{Key: "RED", Name: "Reporter edits", ProjectTypeKey: "software", LeadAccountID: f.ownerID})
	if err != nil {
		t.Fatal(err)
	}
	grants := []store.PermissionGrantInput{{Permission: "BROWSE_PROJECTS", HolderType: "anyone"}, {Permission: "EDIT_ISSUES", HolderType: "anyone"}, {Permission: "TRANSITION_ISSUES", HolderType: "anyone"}}
	scheme, err := f.store.CreatePermissionScheme(ctx, ws, f.ownerID, "Reporter edit permissions", "", grants)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.store.AssignPermissionScheme(ctx, ws, f.ownerID, project.ID, scheme.ID); err != nil {
		t.Fatal(err)
	}
	wf := workflow.Workflow{ID: store.NewID("wf"), Name: "Reporter transition", Transitions: []workflow.Transition{{ID: "1", Name: "Create", Type: workflow.TransitionInitial, To: "st_todo"}, {ID: "21", Name: "Start and report", From: []string{"st_todo"}, To: "st_inprogress", Screen: &workflow.Rule{ID: "reporter-screen", RuleKey: workflow.RuleTransitionScreen, Parameters: map[string]string{"fields": "reporter"}}}, {ID: "11", Name: "Back", From: []string{"st_inprogress"}, To: "st_todo"}}}
	if err = f.store.CreateWorkflow(ctx, ws, wf); err != nil {
		t.Fatal(err)
	}
	if err = f.store.AssignWorkflowToProject(ctx, ws, project.ID, wf.ID); err != nil {
		t.Fatal(err)
	}
	issue, _, err := f.handler.Commands.CreateIssue(ctx, commands.CreateIssueInput{ActorID: f.ownerID, WorkspaceID: ws, ProjectIDOrKey: project.ID, Summary: "Reporter changes", IssueTypeID: "it_task"})
	if err != nil {
		t.Fatal(err)
	}
	api := &api3.Handler{Store: f.store, Commands: f.handler.Commands, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	for _, id := range []string{f.ownerID, f.otherID} {
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, id, store.HashToken(id))
	}
	call := func(user, method, path, body string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(user+"@example.invalid", user)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s status=%d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		out := map[string]any{}
		if w.Body.Len() > 0 {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	path := "/rest/api/3/issue/" + issue.Key
	metadata := call(f.ownerID, "GET", path+"/editmeta", "", 200)["fields"].(map[string]any)
	reporter := metadata["reporter"].(map[string]any)
	if reporter["schema"].(map[string]any)["type"] != "user" || len(reporter["allowedValues"].([]any)) != 2 {
		t.Fatal(reporter)
	}
	if call(f.otherID, "GET", path+"/editmeta", "", 200)["fields"].(map[string]any)["reporter"] != nil {
		t.Fatal("unprivileged editor offered Reporter")
	}
	for _, user := range []string{f.ownerID, f.otherID} {
		view, err := f.handler.buildEditDialogView(ctx, ws, user, issue, map[string]bool{"ASSIGN_ISSUES": true, "MODIFY_REPORTER": user == f.ownerID})
		if err != nil {
			t.Fatal(err)
		}
		if view.CanModifyReporter != (user == f.ownerID) {
			t.Fatalf("edit Reporter permission for %s", user)
		}
	}
	call(f.otherID, "PUT", path, `{"fields":{"reporter":{"accountId":"`+f.otherID+`"}}}`, 400)
	for _, bad := range []string{`{}`, `{"accountId":""}`, `{"accountId":"usr_missing"}`, `"name"`} {
		call(f.ownerID, "PUT", path, `{"fields":{"reporter":`+bad+`}}`, 400)
	}
	call(f.ownerID, "PUT", path, `{"fields":{"reporter":{"accountId":"`+f.otherID+`"}}}`, 204)
	read := call(f.ownerID, "GET", path+"?fields=reporter,creator", "", 200)["fields"].(map[string]any)
	if read["reporter"].(map[string]any)["accountId"] != f.otherID || read["creator"].(map[string]any)["accountId"] != f.ownerID {
		t.Fatal(read)
	}
	expression := call(f.ownerID, "POST", "/rest/api/3/expression/eval", `{"expression":"{reporter: issue.reporter.accountId, creator: issue.creator.accountId}","context":{"issue":{"key":"`+issue.Key+`"}}}`, 200)["value"].(map[string]any)
	if expression["reporter"] != f.otherID || expression["creator"] != f.ownerID {
		t.Fatal("expression confused Reporter with creator", expression)
	}
	var payload []byte
	if err = f.store.Pool.QueryRow(ctx, `SELECT payload FROM actions WHERE workspace_id=$1 AND entity_type='issue' AND entity_id=$2 ORDER BY seq DESC LIMIT 1`, ws, issue.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var changed models.IssueUpdatePayload
	if err = json.Unmarshal(payload, &changed); err != nil || changed.Diff["reporter"].To != f.otherID {
		t.Fatalf("reporter audit: %s %v", payload, err)
	}
	call(f.ownerID, "PUT", path, `{"update":{"reporter":[{"set":{"accountId":"`+f.ownerID+`"}}]}}`, 204)
	call(f.ownerID, "PUT", path, `{"update":{"reporter":[{"add":{"accountId":"`+f.otherID+`"}}]}}`, 400)
	call(f.ownerID, "PUT", path, `{"fields":{"reporter":null},"update":{"reporter":[{"set":null}]}}`, 400)
	// Optional Reporter may be cleared, but a required configuration refuses it.
	call(f.ownerID, "PUT", path, `{"fields":{"reporter":null}}`, 204)
	cleared := call(f.ownerID, "GET", path+"?fields=reporter", "", 200)["fields"].(map[string]any)
	if value, ok := cleared["reporter"]; !ok || value != nil {
		t.Fatalf("cleared Reporter projection: %v", cleared)
	}
	expression = call(f.ownerID, "POST", "/rest/api/3/expression/eval", `{"expression":"{reporter: issue.reporter, creator: issue.creator.accountId}","context":{"issue":{"key":"`+issue.Key+`"}}}`, 200)["value"].(map[string]any)
	if expression["reporter"] != nil || expression["creator"] != f.ownerID {
		t.Fatal("cleared Reporter changed expression creator", expression)
	}
	call(f.ownerID, "PUT", path, `{"fields":{"reporter":{"accountId":"`+f.ownerID+`"}}}`, 204)
	configurations, err := f.store.FieldConfigurations(ctx, ws, nil)
	if err != nil || len(configurations) == 0 {
		t.Fatal(err)
	}
	configuration := configurations[0].ID
	configure := func(item models.FieldConfigurationItem) {
		t.Helper()
		if err := f.store.SetFieldConfigurationItems(ctx, ws, f.ownerID, configuration, []models.FieldConfigurationItem{item}); err != nil {
			t.Fatal(err)
		}
	}
	configure(models.FieldConfigurationItem{FieldID: "reporter", IsRequired: true})
	call(f.ownerID, "PUT", path, `{"fields":{"reporter":null}}`, 400)
	configure(models.FieldConfigurationItem{FieldID: "reporter", IsHidden: true})
	call(f.ownerID, "PUT", path, `{"fields":{"reporter":{"accountId":"`+f.otherID+`"}}}`, 400)
	if call(f.ownerID, "GET", path+"/editmeta", "", 200)["fields"].(map[string]any)["reporter"] != nil {
		t.Fatal("hidden Reporter in metadata")
	}
	configure(models.FieldConfigurationItem{FieldID: "reporter"})
	// Reporter writes follow the edit screen unless an authorized app overrides it.
	var screenID, tabID string
	var position int
	if err = f.store.Pool.QueryRow(ctx, `SELECT screen_id,tab_id,position FROM screen_tab_fields WHERE workspace_id=$1 AND field_id='reporter' LIMIT 1`, ws).Scan(&screenID, &tabID, &position); err != nil {
		t.Fatal(err)
	}
	exec(`DELETE FROM screen_tab_fields WHERE workspace_id=$1 AND field_id='reporter'`, ws)
	call(f.ownerID, "PUT", path, `{"fields":{"reporter":{"accountId":"`+f.otherID+`"}}}`, 400)
	if call(f.ownerID, "GET", path+"/editmeta", "", 200)["fields"].(map[string]any)["reporter"] != nil {
		t.Fatal("Reporter absent from screen still editable")
	}
	exec(`INSERT INTO screen_tab_fields(workspace_id,screen_id,tab_id,field_id,position) VALUES($1,$2,$3,'reporter',$4)`, ws, screenID, tabID, position)
	// A person may transition without modifying Reporter, and may not forge a change.
	call(f.otherID, "POST", path+"/transitions", `{"transition":{"id":"21"},"fields":{"reporter":{"accountId":"`+f.otherID+`"}}}`, 400)
	call(f.otherID, "POST", path+"/transitions", `{"transition":{"id":"21"}}`, 204)
	call(f.ownerID, "POST", path+"/transitions", `{"transition":{"id":"11"}}`, 204)
	call(f.ownerID, "POST", path+"/transitions", `{"transition":{"id":"21"},"fields":{"reporter":{"accountId":"`+f.otherID+`"}}}`, 204)
	call(f.ownerID, "POST", path+"/transitions", `{"transition":{"id":"11"},"fields":{"reporter":null}}`, 400)
	call(f.ownerID, "POST", path+"/transitions", `{"transition":{"id":"11"}}`, 204)
	// The native edit edge calls the same command, preserving permission scope.
	edit := func(w http.ResponseWriter, r *http.Request) { f.handler.EditIssue(w, r, issue.Key) }
	response := f.post(t, edit, f.otherID, "/issues/"+issue.Key+"/edit", "", url.Values{"summary": {"Reporter changes"}, "reporter": {f.ownerID}})
	if response.Code != 403 {
		t.Fatalf("forged web edit=%d %s", response.Code, response.Body.String())
	}
	if _, err = f.store.CreatePermissionGrant(ctx, ws, f.ownerID, scheme.ID, store.PermissionGrantInput{Permission: "MODIFY_REPORTER", HolderType: "anyone"}); err != nil {
		t.Fatal(err)
	}
	response = f.post(t, edit, f.otherID, "/issues/"+issue.Key+"/edit", "", url.Values{"summary": {"Reporter changes"}, "reporter": {f.ownerID}})
	if response.Code != 200 {
		t.Fatalf("granted web edit=%d %s", response.Code, response.Body.String())
	}
	// Changing a reporter-bound security level revokes the old reporter atomically.
	security, err := f.store.CreateIssueSecurityScheme(ctx, ws, f.ownerID, "Reporter private", "", []store.SecurityLevelInput{{Name: "Reporter only", Members: []store.SecurityLevelMemberInput{{Type: "reporter"}, {Type: "user", Parameter: f.ownerID}}}})
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE projects SET security_scheme_id=$2 WHERE id=$1`, project.ID, security.ID)
	exec(`UPDATE issues SET reporter_id=$2,security_level_id=$3 WHERE id=$1`, issue.ID, f.otherID, security.Levels[0].ID)
	call(f.otherID, "GET", path, "", 200)
	head, err := f.store.Head(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	call(f.ownerID, "PUT", path, `{"fields":{"reporter":{"accountId":"`+f.ownerID+`"}}}`, 204)
	call(f.otherID, "GET", path, "", 404)
	actions, _, err := f.store.ActionPageSince(ctx, ws, f.otherID, head, 100)
	if err != nil {
		t.Fatal(err)
	}
	revoked := false
	for _, action := range actions {
		if action.EntityType == models.EntityIssue && action.EntityID == issue.ID {
			t.Fatal("former reporter received restricted issue")
		}
		if action.EntityType == models.EntityTombstone && action.EntityID == issue.ID {
			revoked = true
		}
	}
	if !revoked {
		t.Fatal("former reporter's replica was not revoked")
	}
}
