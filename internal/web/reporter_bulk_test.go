package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/api3"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestBulkReporterUsesGlobalAndPerItemPermissions(t *testing.T) {
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
		for _, query := range []string{`DELETE FROM api_tasks WHERE workspace_id=$1`, `DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`} {
			exec(query, ws)
		}
	})
	project, err := f.handler.Commands.CreateProject(ctx, f.ownerID, ws, commands.CreateProjectInput{Key: "RBU", Name: "Bulk reporters", ProjectTypeKey: "software", LeadAccountID: f.ownerID})
	if err != nil {
		t.Fatal(err)
	}
	scheme, err := f.store.CreatePermissionScheme(ctx, ws, f.ownerID, "Bulk Reporter permissions", "", []store.PermissionGrantInput{{Permission: "BROWSE_PROJECTS", HolderType: "anyone"}, {Permission: "EDIT_ISSUES", HolderType: "anyone"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.store.AssignPermissionScheme(ctx, ws, f.ownerID, project.ID, scheme.ID); err != nil {
		t.Fatal(err)
	}
	issues := []*models.Issue{}
	for _, summary := range []string{"First reporter", "Second reporter"} {
		issue, _, err := f.handler.Commands.CreateIssue(ctx, commands.CreateIssueInput{ActorID: f.ownerID, WorkspaceID: ws, ProjectIDOrKey: project.ID, Summary: summary, IssueTypeID: "it_task"})
		if err != nil {
			t.Fatal(err)
		}
		issues = append(issues, issue)
	}
	api := &api3.Handler{Store: f.store, Commands: f.handler.Commands, WorkspaceSlug: ws}
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, f.otherID, store.HashToken(f.otherID))
	call := func(method, path, body string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(f.otherID+"@example.invalid", f.otherID)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s status=%d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		out := map[string]any{}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	selection := issues[0].Key + "," + issues[1].Key
	query := "/rest/api/3/bulk/issues/fields?issueIdsOrKeys=" + selection + "&searchText=Reporter"
	payload := func(value string) string {
		return `{"selectedIssueIdsOrKeys":["` + issues[0].Key + `","` + issues[1].Key + `"],"selectedActions":["reporter"],"editedFieldsInput":{"singleSelectClearableUserPickerFields":[{"fieldId":"reporter","user":` + value + `}]},"sendBulkNotification":false}`
	}
	body := payload(`{"accountId":"` + f.otherID + `"}`)
	exec(`DELETE FROM global_permission_grants WHERE workspace_id=$1 AND permission_key='BULK_CHANGE'`, ws)
	call("GET", query, "", 403)
	call("POST", "/rest/api/3/bulk/issues/fields", body, 403)
	if _, err := f.store.AddGlobalPermissionGrant(ctx, ws, f.ownerID, "BULK_CHANGE", "", "jira-software"); err != nil {
		t.Fatal(err)
	}
	call("GET", query, "", 404)
	call("POST", "/rest/api/3/bulk/issues/fields", body, 400)
	grant, err := f.store.CreatePermissionGrant(ctx, ws, f.ownerID, scheme.ID, store.PermissionGrantInput{Permission: "MODIFY_REPORTER", HolderType: "reporter"})
	if err != nil {
		t.Fatal(err)
	}
	// The permission is item-dependent: the member reports only the first.
	if _, _, err = f.store.UpdateIssue(ctx, f.ownerID, ws, issues[0].ID, store.IssueUpdate{ReporterID: &f.otherID}); err != nil {
		t.Fatal(err)
	}
	call("GET", query, "", 404)
	single := "/rest/api/3/bulk/issues/fields?issueIdsOrKeys=" + issues[0].Key + "&searchText=Reporter"
	if len(call("GET", single, "", 200)["fields"].([]any)) != 1 {
		t.Fatal("per-item Reporter permission not reflected")
	}
	if err := f.store.DeletePermissionGrant(ctx, ws, f.ownerID, scheme.ID, grant.ID); err != nil {
		t.Fatal(err)
	}
	grant, err = f.store.CreatePermissionGrant(ctx, ws, f.ownerID, scheme.ID, store.PermissionGrantInput{Permission: "MODIFY_REPORTER", HolderType: "anyone"})
	if err != nil {
		t.Fatal(err)
	}
	field := call("GET", query, "", 200)["fields"].([]any)[0].(map[string]any)
	if field["type"] != "reporter" || field["isRequired"] != false || len(field["fieldOptions"].([]any)) != 2 || field["fieldOptions"].([]any)[0].(map[string]any)["accountId"] == nil {
		t.Fatal(field)
	}
	// Global Bulk change admits this non-admin's REST edit. The worker must
	// still refuse when a field permission is revoked after submission.
	submitted := call("POST", "/rest/api/3/bulk/issues/fields", body, 201)
	if err := f.store.DeletePermissionGrant(ctx, ws, f.ownerID, scheme.ID, grant.ID); err != nil {
		t.Fatal(err)
	}
	runner := &store.APITaskRunner{Store: f.store, BulkIssueExecutor: f.handler.Commands}
	if err := runner.DrainOnce(ctx, ws); err != nil {
		t.Fatal(err)
	}
	progress := call("GET", "/rest/api/3/bulk/queue/"+submitted["taskId"].(string), "", 200)
	if !strings.Contains(string(mustBulkJSON(t, progress)), "failedAccessibleIssues") {
		t.Fatal(progress)
	}
	if _, err = f.store.CreatePermissionGrant(ctx, ws, f.ownerID, scheme.ID, store.PermissionGrantInput{Permission: "MODIFY_REPORTER", HolderType: "anyone"}); err != nil {
		t.Fatal(err)
	}
	submitted = call("POST", "/rest/api/3/bulk/issues/fields", body, 201)
	if err := runner.DrainOnce(ctx, ws); err != nil {
		t.Fatal(err)
	}
	progress = call("GET", "/rest/api/3/bulk/queue/"+submitted["taskId"].(string), "", 200)
	if strings.Contains(string(mustBulkJSON(t, progress)), "failedAccessibleIssues") {
		t.Fatal(progress)
	}
	for _, issue := range issues {
		updated, err := f.store.IssueByIDOrKey(ctx, ws, issue.ID)
		if err != nil || updated.Reporter == nil || updated.Reporter.ID != f.otherID {
			t.Fatalf("updated Reporter/creator: %+v %v", updated, err)
		}
		fields := call("GET", "/rest/api/3/issue/"+issue.Key+"?fields=reporter,creator", "", 200)["fields"].(map[string]any)
		if fields["creator"].(map[string]any)["accountId"] != f.ownerID {
			t.Fatal("Reporter edit changed the creator", fields)
		}
	}
	configurations, err := f.store.FieldConfigurations(ctx, ws, nil)
	if err != nil || len(configurations) == 0 {
		t.Fatal(err)
	}
	if err := f.store.SetFieldConfigurationItems(ctx, ws, f.ownerID, configurations[0].ID, []models.FieldConfigurationItem{{FieldID: "reporter", IsRequired: true}}); err != nil {
		t.Fatal(err)
	}
	if call("GET", query, "", 200)["fields"].([]any)[0].(map[string]any)["isRequired"] != true {
		t.Fatal("required Reporter omitted from bulk metadata")
	}
	submitted = call("POST", "/rest/api/3/bulk/issues/fields", payload("null"), 201)
	if err := runner.DrainOnce(ctx, ws); err != nil {
		t.Fatal(err)
	}
	progress = call("GET", "/rest/api/3/bulk/queue/"+submitted["taskId"].(string), "", 200)
	if !strings.Contains(string(mustBulkJSON(t, progress)), "reporter is required") {
		t.Fatal(progress)
	}
	if err := f.store.SetFieldConfigurationItems(ctx, ws, f.ownerID, configurations[0].ID, []models.FieldConfigurationItem{{FieldID: "reporter", IsHidden: true}}); err != nil {
		t.Fatal(err)
	}
	call("GET", query, "", 404)
	call("POST", "/rest/api/3/bulk/issues/fields", body, 400)
	if err := f.store.SetFieldConfigurationItems(ctx, ws, f.ownerID, configurations[0].ID, []models.FieldConfigurationItem{{FieldID: "reporter"}}); err != nil {
		t.Fatal(err)
	}
	var screenID, tabID string
	var position int
	if err = f.store.Pool.QueryRow(ctx, `SELECT screen_id,tab_id,position FROM screen_tab_fields WHERE workspace_id=$1 AND field_id='reporter' LIMIT 1`, ws).Scan(&screenID, &tabID, &position); err != nil {
		t.Fatal(err)
	}
	exec(`DELETE FROM screen_tab_fields WHERE workspace_id=$1 AND field_id='reporter'`, ws)
	call("GET", query, "", 404)
	call("POST", "/rest/api/3/bulk/issues/fields", body, 400)
	exec(`INSERT INTO screen_tab_fields(workspace_id,screen_id,tab_id,field_id,position) VALUES($1,$2,$3,'reporter',$4)`, ws, screenID, tabID, position)
	submitted = call("POST", "/rest/api/3/bulk/issues/fields", payload("null"), 201)
	if err := runner.DrainOnce(ctx, ws); err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		updated, err := f.store.IssueByIDOrKey(ctx, ws, issue.ID)
		if err != nil || updated.Reporter != nil {
			t.Fatalf("optional Reporter not cleared: %+v %v", updated, err)
		}
	}
	// Browser submissions encode the same account ID, rather than inventing
	// a custom field named reporter.
	operations, err := bulkEditOperations(bulkEditRequest(url.Values{"field": {"reporter"}, "valueReporter": {f.ownerID}}), nil)
	if err != nil || len(operations) != 1 || operations[0].FieldID != "reporter" {
		t.Fatalf("browser Reporter operation: %+v %v", operations, err)
	}
}

func mustBulkJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
