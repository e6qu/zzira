package api3

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestFieldOperations pins Jira's issue field surface beyond the list and the
// create: the paginated searches, the update, the project associations, the
// contexts read, and the trash lifecycle that guards a delete.
func TestFieldOperations(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, adminID, memberID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Field operations')`, workspaceID)
	for _, value := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Field user')`, value.id, value.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, value.id, value.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, value.id, store.HashToken(value.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(userID, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(userID+"@example.test", userID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	call(adminID, "POST", "/rest/api/3/project", `{"key":"FLD","name":"Field work","projectTypeKey":"software","leadAccountId":"`+adminID+`"}`, 201)

	// A client configured against Jira sends the canonical type key.
	created := call(adminID, "POST", "/rest/api/3/field",
		`{"name":"Team cost centre","type":"com.atlassian.jira.plugin.system.customfieldtypes:textfield","description":"Who pays"}`, 201)
	var field struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &field); err != nil || field.ID == "" {
		t.Fatalf("created field: %v %s", err, created.Body.String())
	}
	call(adminID, "POST", "/rest/api/3/field", `{"name":"Nonsense","type":"com.atlassian.jira.plugin.system.customfieldtypes:cascadingselect"}`, 400)

	// Field discovery reports the system fields the search resolves through,
	// not a shorter hand-written list.
	listed := call(adminID, "GET", "/rest/api/3/field", "", 200)
	var all []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &all); err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, entry := range all {
		present[entry.ID] = true
	}
	for _, required := range []string{"summary", "status", "assignee", "priority", "reporter", "components", "parent", "security", field.ID} {
		if !present[required] {
			t.Fatalf("%s missing from field discovery: %s", required, listed.Body.String())
		}
	}

	// The paginated search filters, counts usage and pages.
	page := func(userID, path string, want int) map[string]any {
		t.Helper()
		response := call(userID, "GET", path, "", want)
		var decoded map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s: %v %s", path, err, response.Body.String())
		}
		return decoded
	}
	search := page(adminID, "/rest/api/3/field/search?query=cost", 200)
	values, _ := search["values"].([]any)
	if len(values) != 1 || search["total"].(float64) != 1 {
		t.Fatalf("field search: %v", search)
	}
	first, _ := values[0].(map[string]any)
	if first["id"] != field.ID || first["contextsCount"].(float64) != 1 || first["projectsCount"].(float64) != 1 {
		t.Fatalf("field search usage: %v", first)
	}
	if empty := page(adminID, "/rest/api/3/field/search?query=nothingmatchesthis", 200); empty["total"].(float64) != 0 {
		t.Fatalf("field search: %v", empty)
	}
	if typed := page(adminID, "/rest/api/3/field/search?type=com.atlassian.jira.plugin.system.customfieldtypes:float", 200); typed["total"].(float64) != 0 {
		t.Fatalf("field search by type: %v", typed)
	}
	// The searches are administration, so an ordinary member cannot run them.
	call(memberID, "GET", "/rest/api/3/field/search", "", 403)

	// The update renames and re-describes; an empty name is refused.
	call(adminID, "PUT", "/rest/api/3/field/"+field.ID, `{"name":"Cost centre","description":"Who pays for the work"}`, 204)
	renamed := call(adminID, "GET", "/rest/api/3/field/"+field.ID, "", 200)
	if !strings.Contains(renamed.Body.String(), `"Cost centre"`) || !strings.Contains(renamed.Body.String(), "Who pays for the work") {
		t.Fatal(renamed.Body.String())
	}
	call(adminID, "PUT", "/rest/api/3/field/"+field.ID, `{"name":"   "}`, 400)
	call(adminID, "PUT", "/rest/api/3/field/customfield_404404", `{"name":"Ghost"}`, 404)

	// `/contexts` reports the context's scope, and is a different operation
	// from `/context`, which the router used to swallow it into.
	contexts := page(adminID, "/rest/api/3/field/"+field.ID+"/contexts", 200)
	contextValues, _ := contexts["values"].([]any)
	if len(contextValues) != 1 {
		t.Fatalf("field contexts: %v", contexts)
	}
	scope, _ := contextValues[0].(map[string]any)["scope"].(map[string]any)
	if scope["type"] != "GLOBAL" {
		t.Fatalf("field context scope: %v", contextValues[0])
	}

	// A global context reaches the workspace's projects.
	associations := page(adminID, "/rest/api/3/field/"+field.ID+"/association/project", 200)
	if associations["total"].(float64) != 1 {
		t.Fatalf("field project associations: %v", associations)
	}
	call(adminID, "GET", "/rest/api/3/field/customfield_404404/association/project", "", 404)

	// The project field read resolves through the same context rules.
	projectFields := page(adminID, "/rest/api/3/projects/fields?fieldId="+field.ID, 200)
	rows, _ := projectFields["values"].([]any)
	if len(rows) == 0 {
		t.Fatalf("project fields: %v", projectFields)
	}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["fieldId"] != field.ID || row["isRequired"] != false {
			t.Fatalf("project field row: %v", row)
		}
	}

	// A work item carries a value, which the trash must not destroy.
	issue := call(adminID, "POST", "/rest/api/3/issue",
		`{"fields":{"project":{"key":"FLD"},"summary":"Field work item","issuetype":{"name":"Task"},"`+field.ID+`":"Platform"}}`, 201)
	var work struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(issue.Body.Bytes(), &work); err != nil || work.Key == "" {
		t.Fatalf("created work item: %v %s", err, issue.Body.String())
	}

	// Before the trash, the field reaches the create form.
	liveMeta := call(adminID, "GET", "/rest/api/3/issue/createmeta?projectKeys=FLD&expand=projects.issuetypes.fields", "", 200)
	if !strings.Contains(liveMeta.Body.String(), field.ID) {
		t.Fatalf("field missing from createmeta before the trash: %s", liveMeta.Body.String())
	}

	// A delete is refused until the field is trashed.
	call(adminID, "DELETE", "/rest/api/3/field/"+field.ID, "", 400)
	call(adminID, "POST", "/rest/api/3/field/"+field.ID+"/trash", "", 200)
	call(adminID, "POST", "/rest/api/3/field/"+field.ID+"/trash", "", 400)

	// A trashed field leaves discovery, metadata and the trashed search finds it.
	trashedList := call(adminID, "GET", "/rest/api/3/field", "", 200)
	if strings.Contains(trashedList.Body.String(), field.ID) {
		t.Fatalf("trashed field still discoverable: %s", trashedList.Body.String())
	}
	meta := call(adminID, "GET", "/rest/api/3/issue/createmeta?projectKeys=FLD&expand=projects.issuetypes.fields", "", 200)
	if strings.Contains(meta.Body.String(), field.ID) {
		t.Fatalf("trashed field still in createmeta: %s", meta.Body.String())
	}
	trashed := page(adminID, "/rest/api/3/field/search/trashed", 200)
	if trashed["total"].(float64) != 1 {
		t.Fatalf("trashed search: %v", trashed)
	}
	if live := page(adminID, "/rest/api/3/field/search?query=cost", 200); live["total"].(float64) != 0 {
		t.Fatalf("trashed field still in the live search: %v", live)
	}

	// Restoring brings it back with the recorded value intact.
	call(adminID, "POST", "/rest/api/3/field/"+field.ID+"/restore", "", 200)
	call(adminID, "POST", "/rest/api/3/field/"+field.ID+"/restore", "", 400)
	restored := call(adminID, "GET", "/rest/api/3/issue/"+work.Key+"?fields="+field.ID, "", 200)
	if !strings.Contains(restored.Body.String(), "Platform") {
		t.Fatalf("value lost across the trash: %s", restored.Body.String())
	}

	// Deleting from the trash reports Jira's finished task and removes it.
	call(adminID, "POST", "/rest/api/3/field/"+field.ID+"/trash", "", 200)
	removed := call(adminID, "DELETE", "/rest/api/3/field/"+field.ID, "", 303)
	if !strings.Contains(removed.Body.String(), `"status":"COMPLETE"`) {
		t.Fatal(removed.Body.String())
	}
	call(adminID, "GET", "/rest/api/3/field/"+field.ID, "", 404)
	if gone := page(adminID, "/rest/api/3/field/search/trashed", 200); gone["total"].(float64) != 0 {
		t.Fatalf("deleted field still in the trash: %v", gone)
	}
}
