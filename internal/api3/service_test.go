package api3

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestServiceProjectAndRequestTypeContract(t *testing.T) {
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
	workspaceID, actorID := store.NewID("ws"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Service test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Service admin')`, actorID, actorID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actorID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actorID, store.HashToken(actorID))
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM organization_audit_events WHERE actor_id=$1`, actorID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, actorID)
		exec(`DELETE FROM users WHERE id=$1`, actorID)
	})
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(actorID+"@example.test", actorID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	created := call("POST", "/rest/api/3/project", `{"key":"HELP","name":"Help Center","projectTypeKey":"service_desk","projectTemplateKey":"com.atlassian.servicedesk:simplified-it-service-management","leadAccountId":"`+actorID+`"}`, 201)
	if !strings.Contains(created.Body.String(), `"key":"HELP"`) {
		t.Fatal(created.Body.String())
	}
	project, err := st.ProjectByKey(ctx, workspaceID, "HELP")
	if err != nil || project.ProjectTypeKey != "service_desk" {
		t.Fatalf("service project = %+v, %v", project, err)
	}
	desks := call("GET", "/rest/servicedeskapi/servicedesk", "", 200)
	if !strings.Contains(desks.Body.String(), `"projectKey":"HELP"`) || !strings.Contains(desks.Body.String(), `"projectTypeKey":"service_desk"`) {
		t.Fatal(desks.Body.String())
	}
	var serviceDeskID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_desks WHERE project_id=$1`, project.ID).Scan(&serviceDeskID); err != nil {
		t.Fatal(err)
	}
	call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID, "", 200)
	requestTypes := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype", "", 200)
	if !strings.Contains(requestTypes.Body.String(), `"name":"Get IT help"`) || !strings.Contains(requestTypes.Body.String(), `"name":"Report an incident"`) {
		t.Fatal(requestTypes.Body.String())
	}
	var requestTypeID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 ORDER BY id::bigint LIMIT 1`, serviceDeskID).Scan(&requestTypeID); err != nil {
		t.Fatal(err)
	}
	fields := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/"+requestTypeID+"/field", "", 200)
	if !strings.Contains(fields.Body.String(), `"fieldId":"summary"`) {
		t.Fatal(fields.Body.String())
	}
	createdType := call("POST", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype", `{"name":"Ask a question","description":"General help","helpText":"What do you need?","issueTypeId":"it_task"}`, 200)
	if !strings.Contains(createdType.Body.String(), `"name":"Ask a question"`) {
		t.Fatal(createdType.Body.String())
	}
	var createdTypeID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 AND name='Ask a question'`, serviceDeskID).Scan(&createdTypeID); err != nil {
		t.Fatal(err)
	}
	call("DELETE", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/"+createdTypeID, "", 204)
	call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/"+createdTypeID, "", 404)
	call("GET", "/rest/servicedeskapi/requesttype?searchQuery=incident", "", 200)
	call("GET", "/rest/servicedeskapi/info", "", 200)
}
