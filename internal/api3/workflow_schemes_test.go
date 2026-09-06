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

func TestWorkflowSchemeAPILifecycleAndAssignment(t *testing.T) {
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
	ws, actor, member, projectID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("project")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Workflow scheme API')`, ws)
	for _, user := range []string{actor, member} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Scheme user')`, user, user+"@example.test")
		role := "member"
		if user == actor {
			role = "admin"
		}
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, user, role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, user, store.HashToken(user))
	}
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,'WSA','Scheme API project','wf_default')`, projectID, ws)
	t.Cleanup(func() {
		exec(`DELETE FROM projects WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workspaces WHERE id=$1`, ws)
		for _, user := range []string{actor, member} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.SetBasicAuth(user+"@example.test", user)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec
	}
	body := `{"name":"Delivery scheme","description":"Routes delivery","defaultWorkflow":"Default","issueTypeMappings":{"it_task":"Default"}}`
	call(member, "POST", "/rest/api/3/workflowscheme", body, 403)
	created := call(actor, "POST", "/rest/api/3/workflowscheme", body, 201)
	var scheme map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &scheme); err != nil {
		t.Fatal(err)
	}
	schemeID := scheme["id"].(string)
	call(actor, "GET", "/rest/api/3/workflowscheme", "", 200)
	call(actor, "GET", "/rest/api/3/workflowscheme/"+schemeID, "", 200)
	update := `{"name":"Delivery scheme updated","description":"Draft routing","defaultWorkflow":"Default","issueTypeMappings":{}}`
	draft := call(actor, "PUT", "/rest/api/3/workflowscheme/"+schemeID, update, 200)
	if !strings.Contains(draft.Body.String(), `"draft":true`) {
		t.Fatal(draft.Body.String())
	}
	call(actor, "PUT", "/rest/api/3/workflowscheme/project", `{"projectId":"`+projectID+`","workflowSchemeId":"`+schemeID+`"}`, 204)
	association := call(actor, "GET", "/rest/api/3/workflowscheme/project?projectId="+projectID, "", 200)
	if !strings.Contains(association.Body.String(), schemeID) {
		t.Fatal(association.Body.String())
	}
	call(actor, "DELETE", "/rest/api/3/workflowscheme/"+schemeID, "", 409)
	unused := call(actor, "POST", "/rest/api/3/workflowscheme", `{"name":"Unused scheme","defaultWorkflow":"Default"}`, 201)
	if err := json.Unmarshal(unused.Body.Bytes(), &scheme); err != nil {
		t.Fatal(err)
	}
	call(actor, "DELETE", "/rest/api/3/workflowscheme/"+scheme["id"].(string), "", 204)
}
