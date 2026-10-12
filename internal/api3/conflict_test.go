package api3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// documentsConflict reads what the pinned specification says an operation's
// conflict is, failing the test if it publishes none.
func documentsConflict(t *testing.T, method, template string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "specs", "jira-v3.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Description string `json:"description"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	operation, ok := document.Paths[template][strings.ToLower(method)]
	if !ok {
		t.Fatalf("%s %s is not in the pinned specification", method, template)
	}
	conflict, ok := operation.Responses["409"]
	if !ok {
		t.Fatalf("%s %s publishes no conflict", method, template)
	}
	return strings.TrimSpace(conflict.Description)
}

// wireID reads an id a bean reports either as text or as a JSON number.
func wireID(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	}
	return ""
}

// TestConflictsAnswerTheirDocumentedConditions pins the conflicts a client can
// provoke on purpose. Jira publishes 409 for two collisions a caller can reach
// deliberately: a name already taken, and a resource still in use by
// something a delete would leave dangling. Each condition is built here and
// the answer checked against the status the operation publishes for it. The
// conflicts that need a background task to still be running are not here,
// because this product finishes that work before it answers.
func TestConflictsAnswerTheirDocumentedConditions(t *testing.T) {
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
	workspaceID, adminID := store.NewID("ws"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Conflicts')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Conflict admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM project_field_configuration_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM field_configuration_scheme_items WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM field_configuration_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM field_configuration_items WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM field_configurations WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issue_types WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM api_tasks WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM priorities WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, adminID)
		exec(`DELETE FROM users WHERE id=$1`, adminID)
	})
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	send := func(method, path, body string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(adminID+"@example.test", adminID)
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code, response.Body.String()
	}
	object := func(method, path, body string, want int) map[string]any {
		t.Helper()
		status, answer := send(method, path, body)
		if status != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, status, want, answer)
		}
		out := map[string]any{}
		if answer != "" && answer[0] == '{' {
			if err := json.Unmarshal([]byte(answer), &out); err != nil {
				t.Fatalf("%s %s: decode: %v", method, path, err)
			}
		}
		return out
	}

	// A name already taken is a conflict, not a validation failure.
	for _, c := range []struct{ method, template, path, body string }{
		{http.MethodPost, "/rest/api/3/issuetype", "/rest/api/3/issuetype", `{"name":"Escalation","type":"standard"}`},
		{http.MethodPost, "/rest/api/3/projectCategory", "/rest/api/3/projectCategory", `{"name":"Escalations","description":"Escalated work"}`},
		{http.MethodPost, "/rest/api/3/role", "/rest/api/3/role", `{"name":"Escalation owners","description":"Owns escalations"}`},
	} {
		conflict := documentsConflict(t, c.method, c.template)
		first, body := send(c.method, c.path, c.body)
		if first != http.StatusCreated && first != http.StatusOK {
			t.Errorf("%s %s: first answer %d: %s", c.method, c.path, first, body)
			continue
		}
		again, answer := send(c.method, c.path, c.body)
		if again != http.StatusConflict {
			t.Errorf("%s %s: repeating the name answered %d, want 409 (%s): %s",
				c.method, c.path, again, conflict, answer)
		}
	}

	// A work type scheme names the work types it holds, so its own conflict
	// needs a name repeated with a valid body.
	status, listed := send(http.MethodGet, "/rest/api/3/issuetype", "")
	if status != http.StatusOK {
		t.Fatalf("read the work types: %d: %s", status, listed)
	}
	var types []map[string]any
	if err := json.Unmarshal([]byte(listed), &types); err != nil || len(types) == 0 {
		t.Fatalf("the site offers no work types: %v %s", err, listed)
	}
	typeID := wireID(types[0]["id"])
	schemeBody := `{"name":"Escalation types","issueTypeIds":["` + typeID + `"]}`
	object(http.MethodPost, "/rest/api/3/issuetypescheme", schemeBody, http.StatusCreated)
	schemeConflict := documentsConflict(t, http.MethodPost, "/rest/api/3/issuetypescheme")
	if status, answer := send(http.MethodPost, "/rest/api/3/issuetypescheme", schemeBody); status != http.StatusConflict {
		t.Errorf("repeating a work type scheme name answered %d, want 409 (%s): %s", status, schemeConflict, answer)
	}

	project := object(http.MethodPost, "/rest/api/3/project",
		`{"key":"CFL","name":"Conflict work","projectTypeKey":"software","leadAccountId":"`+adminID+`"}`, http.StatusCreated)
	projectID := wireID(project["id"])

	// A field association scheme a project follows cannot be deleted.
	scheme := object(http.MethodPost, "/rest/api/3/fieldconfigurationscheme", `{"name":"Conflict scheme"}`, http.StatusCreated)
	schemeID := wireID(scheme["id"])
	configuration := object(http.MethodPost, "/rest/api/3/fieldconfiguration", `{"name":"Conflict configuration"}`, http.StatusCreated)
	if status, answer := send(http.MethodPut, "/rest/api/3/fieldconfigurationscheme/"+schemeID+"/mapping",
		`{"mappings":[{"issueTypeId":"default","fieldConfigurationId":"`+wireID(configuration["id"])+`"}]}`); status != http.StatusNoContent {
		t.Fatalf("map the configuration: %d: %s", status, answer)
	}
	if status, answer := send(http.MethodPut, "/rest/api/3/fieldconfigurationscheme/project",
		`{"fieldConfigurationSchemeId":"`+schemeID+`","projectId":"`+projectID+`"}`); status != http.StatusNoContent {
		t.Fatalf("assign the scheme: %d: %s", status, answer)
	}
	inUse := documentsConflict(t, http.MethodDelete, "/rest/api/3/config/fieldschemes/{id}")
	if status, answer := send(http.MethodDelete, "/rest/api/3/config/fieldschemes/"+schemeID, ""); status != http.StatusConflict {
		t.Errorf("deleting a scheme a project follows answered %d, want 409 (%s): %s", status, inUse, answer)
	}

	// Renaming a work type onto a name already taken is a conflict too.
	renamed := object(http.MethodPost, "/rest/api/3/issuetype", `{"name":"Escalation review","type":"standard"}`, http.StatusCreated)
	renameConflict := documentsConflict(t, http.MethodPut, "/rest/api/3/issuetype/{id}")
	if status, answer := send(http.MethodPut, "/rest/api/3/issuetype/"+wireID(renamed["id"]), `{"name":"Escalation"}`); status != http.StatusConflict {
		t.Errorf("renaming a work type onto a taken name answered %d, want 409 (%s): %s", status, renameConflict, answer)
	}

	// A work type in use goes only to another type: naming itself as the
	// replacement is a conflict.
	inUseType := object(http.MethodPost, "/rest/api/3/issuetype", `{"name":"Escalation task","type":"standard"}`, http.StatusCreated)
	inUseTypeID := wireID(inUseType["id"])
	object(http.MethodPost, "/rest/api/3/issue",
		`{"fields":{"project":{"key":"CFL"},"summary":"Escalated","issuetype":{"id":"`+inUseTypeID+`"}}}`, http.StatusCreated)
	typeConflict := documentsConflict(t, http.MethodDelete, "/rest/api/3/issuetype/{id}")
	if status, answer := send(http.MethodDelete, "/rest/api/3/issuetype/"+inUseTypeID+"?alternativeIssueTypeId="+inUseTypeID, ""); status != http.StatusConflict {
		t.Errorf("replacing a work type with itself answered %d, want 409 (%s): %s", status, typeConflict, answer)
	}

	// An app field's option that a work item holds answers the same conflict,
	// which app_field_options_test.go sets up with an app installed.

	// A deletion that Jira runs in the background refuses a second one while
	// the first is still queued. This product finishes that work before it
	// answers, so the condition is built here the way a real collision would
	// leave it: a task of the same kind, still enqueued, naming the same item.
	priority := object(http.MethodPost, "/rest/api/3/priority", `{"name":"Escalated","description":"Escalated work","statusColor":"#ff5630"}`, http.StatusCreated)
	priorityID := wireID(priority["id"])
	var storePriorityID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM priorities WHERE workspace_id=$1 AND jira_id=$2`, workspaceID, priorityID).Scan(&storePriorityID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO api_tasks(id,workspace_id,submitted_by,status,progress,submitted_at,last_update_at,kind,payload)
		VALUES($1,$2,$3,'ENQUEUED',0,now(),now(),'priority-delete',$4::jsonb)`,
		store.NewID("task"), workspaceID, adminID, `{"priorityId":"`+storePriorityID+`"}`)
	queued := documentsConflict(t, http.MethodDelete, "/rest/api/3/priority/{id}")
	if status, answer := send(http.MethodDelete, "/rest/api/3/priority/"+priorityID, ""); status != http.StatusConflict {
		t.Errorf("deleting a priority whose deletion is queued answered %d, want 409 (%s): %s", status, queued, answer)
	}
	exec(`DELETE FROM api_tasks WHERE workspace_id=$1`, workspaceID)

	// A project role somebody holds needs a replacement before it goes.
	// Creating a project role answers 200, which is the status that
	// operation publishes for a success.
	role := object(http.MethodPost, "/rest/api/3/role", `{"name":"Escalation reviewers","description":"Reviews escalations"}`, http.StatusOK)
	roleID := wireID(role["id"])
	if status, answer := send(http.MethodPost, "/rest/api/3/project/CFL/role/"+roleID, `{"user":["`+adminID+`"]}`); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("grant the role: %d: %s", status, answer)
	}
	roleConflict := documentsConflict(t, http.MethodDelete, "/rest/api/3/role/{id}")
	if status, answer := send(http.MethodDelete, "/rest/api/3/role/"+roleID, ""); status != http.StatusConflict {
		t.Errorf("deleting a role in use without a replacement answered %d, want 409 (%s): %s", status, roleConflict, answer)
	}
	replacement := object(http.MethodPost, "/rest/api/3/role", `{"name":"Escalation readers","description":"Reads escalations"}`, http.StatusOK)
	if status, answer := send(http.MethodDelete, "/rest/api/3/role/"+roleID+"?swap="+wireID(replacement["id"]), ""); status != http.StatusNoContent && status != http.StatusOK {
		t.Errorf("deleting a role with a replacement named answered %d: %s", status, answer)
	}
}
