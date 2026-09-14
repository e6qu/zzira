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

// TestCreateProjectWithSchemesAndTemplates covers Jira's create project
// request: the schemes and avatar it names are assigned as the project is
// created, an unknown scheme refuses the whole request, every documented
// template of a project type is accepted, and the deprecated lead works.
func TestCreateProjectWithSchemesAndTemplates(t *testing.T) {
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
	workspaceID, adminID := store.NewID("ws"), store.NewID("usr")
	stamp := time.Now().UnixNano() % 100000
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Project schemes')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Schemes admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM service_desks WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, adminID)
		exec(`DELETE FROM users WHERE id=$1`, adminID)
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(adminID+"@example.test", adminID)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	idOf := func(body string) int64 {
		t.Helper()
		var decoded struct {
			ID json.Number `json:"id"`
		}
		decoder := json.NewDecoder(strings.NewReader(body))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		id, err := decoded.ID.Int64()
		if err != nil {
			var text string
			if json.Unmarshal([]byte(`"`+decoded.ID.String()+`"`), &text) == nil {
				fmt.Sscan(text, &id)
			}
		}
		if id == 0 {
			t.Fatalf("no numeric id in %s", body)
		}
		return id
	}
	permission := idOf(call(http.MethodPost, "/rest/api/3/permissionscheme", `{"name":"Start permissions `+fmt.Sprint(stamp)+`"}`, http.StatusCreated))
	notification := idOf(call(http.MethodPost, "/rest/api/3/notificationscheme", `{"name":"Start notices `+fmt.Sprint(stamp)+`"}`, http.StatusCreated))
	security := idOf(call(http.MethodPost, "/rest/api/3/issuesecurityschemes", `{"name":"Start security `+fmt.Sprint(stamp)+`","levels":[{"name":"Team"}]}`, http.StatusCreated))
	fields := idOf(call(http.MethodPost, "/rest/api/3/fieldconfigurationscheme", `{"name":"Start fields `+fmt.Sprint(stamp)+`"}`, http.StatusCreated))
	workflows := idOf(call(http.MethodPost, "/rest/api/3/workflowscheme", `{"name":"Start workflows `+fmt.Sprint(stamp)+`","defaultWorkflow":"Default"}`, http.StatusCreated))

	key := fmt.Sprintf("PS%05d", stamp)
	body := fmt.Sprintf(`{"key":%q,"name":"Schemes %s","projectTypeKey":"business","projectTemplateKey":"com.atlassian.jira-core-project-templates:jira-core-simplified-document-approval",
		"lead":%q,"assigneeType":"UNASSIGNED","avatarId":10400,"permissionScheme":%d,"notificationScheme":%d,"issueSecurityScheme":%d,"fieldConfigurationScheme":%d,"workflowScheme":%d}`,
		key, key, adminID, permission, notification, security, fields, workflows)
	projectID := idOf(call(http.MethodPost, "/rest/api/3/project", body, http.StatusCreated))

	assigned := func(query string, want any) {
		t.Helper()
		var got string
		if err := st.Pool.QueryRow(ctx, query, workspaceID, fmt.Sprint(projectID)).Scan(&got); err != nil || got != fmt.Sprint(want) {
			t.Fatalf("%s = %q (err %v), want %v", query, got, err, want)
		}
	}
	assigned(`SELECT scheme_id::text FROM project_permission_schemes WHERE workspace_id=$1 AND project_id=$2`, permission)
	assigned(`SELECT scheme_id::text FROM project_notification_schemes WHERE workspace_id=$1 AND project_id=$2`, notification)
	assigned(`SELECT security_scheme_id FROM projects WHERE workspace_id=$1 AND id=$2`, security)
	assigned(`SELECT scheme_id::text FROM project_field_configuration_schemes WHERE workspace_id=$1 AND project_id=$2`, fields)
	assigned(`SELECT ws.jira_id::text FROM projects p JOIN workflow_schemes ws ON ws.id=p.workflow_scheme_id WHERE p.workspace_id=$1 AND p.id=$2`, workflows)
	assigned(`SELECT avatar_id::text FROM projects WHERE workspace_id=$1 AND id=$2`, 10400)
	assigned(`SELECT lead_account_id FROM projects WHERE workspace_id=$1 AND id=$2`, adminID)
	if scheme := call(http.MethodGet, "/rest/api/3/project/"+key+"/permissionscheme", "", http.StatusOK); !strings.Contains(scheme, fmt.Sprint(permission)) {
		t.Fatalf("assigned permission scheme = %s", scheme)
	}

	// An unknown scheme refuses the request and leaves no project behind.
	missingKey := fmt.Sprintf("PM%05d", stamp)
	refused := call(http.MethodPost, "/rest/api/3/project", fmt.Sprintf(`{"key":%q,"name":"Missing %s","projectTypeKey":"software","leadAccountId":%q,"notificationScheme":99999999}`, missingKey, missingKey, adminID), http.StatusBadRequest)
	if !strings.Contains(refused, `"notificationScheme"`) {
		t.Fatalf("unknown scheme refusal = %s", refused)
	}
	call(http.MethodGet, "/rest/api/3/project/"+missingKey, "", http.StatusNotFound)
	call(http.MethodPost, "/rest/api/3/project", fmt.Sprintf(`{"key":"PA%05d","name":"Bad avatar","projectTypeKey":"software","leadAccountId":%q,"avatarId":1}`, stamp, adminID), http.StatusBadRequest)
	call(http.MethodPost, "/rest/api/3/project", fmt.Sprintf(`{"key":"PL%05d","name":"Two leads","projectTypeKey":"software","leadAccountId":%q,"lead":"someone-else"}`, stamp, adminID), http.StatusBadRequest)
	call(http.MethodPost, "/rest/api/3/project", fmt.Sprintf(`{"key":"PF%05d","name":"Two field schemes","projectTypeKey":"software","leadAccountId":%q,"fieldScheme":1,"fieldConfigurationScheme":2}`, stamp, adminID), http.StatusBadRequest)

	// Every documented template of a type is accepted; others are refused.
	board := func(projectKey string) string {
		t.Helper()
		var boardType string
		if err := st.Pool.QueryRow(ctx, `SELECT b.type FROM boards b JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1 AND p.key=$2`, workspaceID, projectKey).Scan(&boardType); err != nil {
			t.Fatal(err)
		}
		return boardType
	}
	create := func(projectKey, projectType, template string, want int) {
		t.Helper()
		call(http.MethodPost, "/rest/api/3/project", fmt.Sprintf(`{"key":%q,"name":"Template %s","projectTypeKey":%q,"projectTemplateKey":%q,"leadAccountId":%q}`, projectKey, projectKey, projectType, template, adminID), want)
	}
	create(fmt.Sprintf("TK%05d", stamp), "software", "com.pyxis.greenhopper.jira:gh-simplified-agility-kanban", http.StatusCreated)
	if got := board(fmt.Sprintf("TK%05d", stamp)); got != "kanban" {
		t.Fatalf("team-managed kanban template board = %s", got)
	}
	create(fmt.Sprintf("TS%05d", stamp), "software", "com.pyxis.greenhopper.jira:gh-simplified-agility-scrum", http.StatusCreated)
	if got := board(fmt.Sprintf("TS%05d", stamp)); got != "scrum" {
		t.Fatalf("team-managed scrum template board = %s", got)
	}
	create(fmt.Sprintf("TH%05d", stamp), "service_desk", "com.atlassian.servicedesk:simplified-hr-service-desk", http.StatusCreated)
	create(fmt.Sprintf("TX%05d", stamp), "software", "com.atlassian.servicedesk:simplified-hr-service-desk", http.StatusBadRequest)
	create(fmt.Sprintf("TC%05d", stamp), "customer_service", "com.atlassian.jcs:customer-service-management", http.StatusBadRequest)
}
