package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestBulkTransitionNeedsTransitionPermission covers Jira's bulk transition
// permission: a caller who can browse a project's work items but lacks the
// Transition issues permission there is refused with 403.
func TestBulkTransitionNeedsTransitionPermission(t *testing.T) {
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
	if err = store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, adminID, memberID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	projectKey := fmt.Sprintf("BT%05d", time.Now().UnixNano()%100000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Bulk transition permissions')`, workspaceID)
	people := []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}}
	for _, person := range people {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, person.id, person.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, person.id, person.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, person.id, store.HashToken(person.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM api_tasks WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM role_bindings WHERE scope_type='project' AND scope_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM permission_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, person := range people {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, person.id)
			exec(`DELETE FROM users WHERE id=$1`, person.id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	callAs := func(accountID, method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(accountID+"@example.test", accountID)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, accountID, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	callAs(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Bulk `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"UNASSIGNED"}`, http.StatusCreated)
	created := map[string]any{}
	if err = json.Unmarshal([]byte(callAs(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"issuetype":{"id":"10001"},"summary":"Needs a transition"}}`, http.StatusCreated)), &created); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprint(created["key"])
	transition := `{"bulkTransitionInputs":[{"selectedIssueIdsOrKeys":["` + key + `"],"transitionId":"21"}],"sendBulkNotification":false}`

	// Everyone may browse the project, but no one may transition its work.
	scheme := map[string]any{}
	if err = json.Unmarshal([]byte(callAs(adminID, http.MethodPost, "/rest/api/3/permissionscheme", `{"name":"Browse only `+projectKey+`","permissions":[{"permission":"BROWSE_PROJECTS","holder":{"type":"anyone"}}]}`, http.StatusCreated)), &scheme); err != nil {
		t.Fatal(err)
	}
	callAs(adminID, http.MethodPut, "/rest/api/3/project/"+projectKey+"/permissionscheme", fmt.Sprintf(`{"id":%v}`, scheme["id"]), http.StatusOK)
	callAs(memberID, http.MethodGet, "/rest/api/3/issue/"+key, "", http.StatusOK)
	if refused := callAs(memberID, http.MethodPost, "/rest/api/3/bulk/issues/transition", transition, http.StatusForbidden); !strings.Contains(refused, key) {
		t.Fatalf("bulk transition refusal = %s", refused)
	}
}
