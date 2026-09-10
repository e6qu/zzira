package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestPermissionSchemeContractAndEvaluation(t *testing.T) {
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
	workspaceID := store.NewID("ws")
	adminID, leadID, viewerID, outsiderID := store.NewID("usr"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	suffix := time.Now().UnixNano() % 100000000
	projectKey := fmt.Sprintf("P%08d", suffix)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Permission scheme contract')`, workspaceID)
	for _, identity := range []struct{ id, role string }{
		{adminID, "admin"}, {leadID, "member"}, {viewerID, "member"}, {outsiderID, "member"},
	} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Permission "+identity.id)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	var directoryID, groupID string
	if err = st.Pool.QueryRow(ctx, `SELECT d.id::text FROM directories d JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1 LIMIT 1`, workspaceID).Scan(&directoryID); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO groups(directory_id,name) VALUES($1,'Release viewers') RETURNING id::text`, directoryID).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO group_members(group_id,user_id) VALUES($1,$2)`, groupID, viewerID)
	t.Cleanup(func() {
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, leadID, viewerID, outsiderID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if user != "" {
			request.SetBasicAuth(user+"@example.test", user)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	projectResponse := call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Permission project","projectTypeKey":"software","leadAccountId":"`+leadID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)
	var project struct {
		ID int64 `json:"id"`
	}
	if err = json.Unmarshal(projectResponse.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	projectID := strconv.FormatInt(project.ID, 10)
	issueResponse := call(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Permission boundary","issuetype":{"name":"Task"}}}`, http.StatusCreated)
	var issue struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err = json.Unmarshal(issueResponse.Body.Bytes(), &issue); err != nil || issue.ID == "" {
		t.Fatalf("issue=%+v err=%v body=%s", issue, err, issueResponse.Body.String())
	}

	defaultList := call(viewerID, http.MethodGet, "/rest/api/3/permissionscheme?expand=permissions", "", http.StatusOK)
	if !strings.Contains(defaultList.Body.String(), `"id":10000`) || !strings.Contains(defaultList.Body.String(), `"permission":"BROWSE_PROJECTS"`) {
		t.Fatal(defaultList.Body.String())
	}
	call(viewerID, http.MethodPost, "/rest/api/3/permissionscheme", `{"name":"Forbidden"}`, http.StatusForbidden)
	call(adminID, http.MethodPost, "/rest/api/3/permissionscheme", `{"name":"Invalid","permissions":[{"permission":"BROWSE_PROJECTS","holder":{"type":"group","value":"missing"}}]}`, http.StatusBadRequest)

	createBody := `{"name":"Governed delivery","description":"Restricted release project access","permissions":[` +
		`{"permission":"BROWSE_PROJECTS","holder":{"type":"group","value":"` + groupID + `"}},` +
		`{"permission":"ADMINISTER_PROJECTS","holder":{"type":"user","value":"` + leadID + `"}}]}`
	created := call(adminID, http.MethodPost, "/rest/api/3/permissionscheme?expand=permissions", createBody, http.StatusCreated)
	var scheme struct {
		ID          int64 `json:"id"`
		Permissions []struct {
			ID         int64  `json:"id"`
			Permission string `json:"permission"`
		} `json:"permissions"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &scheme); err != nil || scheme.ID <= 10000 || len(scheme.Permissions) != 2 {
		t.Fatalf("scheme=%+v err=%v body=%s", scheme, err, created.Body.String())
	}
	schemePath := "/rest/api/3/permissionscheme/" + strconv.FormatInt(scheme.ID, 10)
	call(viewerID, http.MethodGet, schemePath+"?expand=permissions", "", http.StatusOK)
	grants := call(viewerID, http.MethodGet, schemePath+"/permission", "", http.StatusOK)
	if !strings.Contains(grants.Body.String(), `"parameter":"Release viewers"`) {
		t.Fatal(grants.Body.String())
	}
	call(viewerID, http.MethodGet, schemePath+"/permission/"+strconv.FormatInt(scheme.Permissions[0].ID, 10), "", http.StatusOK)
	call(adminID, http.MethodPost, schemePath+"/permission", `{"permission":"BROWSE_PROJECTS","holder":{"type":"group","value":"`+groupID+`"}}`, http.StatusBadRequest)

	updated := call(adminID, http.MethodPut, schemePath+"?expand=permissions", `{"name":"Governed releases","description":"Approved release access"}`, http.StatusOK)
	if !strings.Contains(updated.Body.String(), `"Approved release access"`) || !strings.Contains(updated.Body.String(), `"permissions"`) {
		t.Fatal(updated.Body.String())
	}
	call(adminID, http.MethodPut, "/rest/api/3/project/"+projectKey+"/permissionscheme", `{"id":`+strconv.FormatInt(scheme.ID, 10)+`}`, http.StatusOK)
	call(adminID, http.MethodDelete, schemePath, "", http.StatusBadRequest)

	if allowed, permissionErr := st.HasProjectPermission(ctx, workspaceID, viewerID, projectID, "", "BROWSE_PROJECTS"); permissionErr != nil || !allowed {
		t.Fatalf("group browse permission=%t err=%v", allowed, permissionErr)
	}
	if allowed, permissionErr := st.HasProjectPermission(ctx, workspaceID, outsiderID, projectID, "", "BROWSE_PROJECTS"); permissionErr != nil || allowed {
		t.Fatalf("outsider browse permission=%t err=%v", allowed, permissionErr)
	}
	if allowed, permissionErr := st.CanAdministerProject(ctx, workspaceID, leadID, projectID); permissionErr != nil || !allowed {
		t.Fatalf("delegated project admin=%t err=%v", allowed, permissionErr)
	}
	call(viewerID, http.MethodGet, "/rest/api/3/project/"+projectKey, "", http.StatusOK)
	call(outsiderID, http.MethodGet, "/rest/api/3/project/"+projectKey, "", http.StatusNotFound)
	call(viewerID, http.MethodGet, "/rest/api/3/issue/"+issue.Key, "", http.StatusOK)
	call(outsiderID, http.MethodGet, "/rest/api/3/issue/"+issue.Key, "", http.StatusNotFound)
	viewerSearch := call(viewerID, http.MethodGet, "/rest/api/3/search/jql?jql=project%20%3D%20"+projectKey, "", http.StatusOK)
	outsiderSearch := call(outsiderID, http.MethodGet, "/rest/api/3/search/jql?jql=project%20%3D%20"+projectKey, "", http.StatusOK)
	if !strings.Contains(viewerSearch.Body.String(), `"id":"`+issue.ID+`"`) || strings.Contains(outsiderSearch.Body.String(), `"id":"`+issue.ID+`"`) {
		t.Fatalf("viewer search=%s outsider search=%s", viewerSearch.Body.String(), outsiderSearch.Body.String())
	}
	call(leadID, http.MethodGet, "/rest/api/3/project/"+projectKey+"/permissionscheme?expand=permissions", "", http.StatusOK)
	call(viewerID, http.MethodGet, "/rest/api/3/project/"+projectKey+"/permissionscheme", "", http.StatusForbidden)

	myPermissions := call(viewerID, http.MethodGet, "/rest/api/3/mypermissions?projectKey="+projectKey+"&permissions=BROWSE_PROJECTS,ADMINISTER_PROJECTS", "", http.StatusOK)
	if !strings.Contains(myPermissions.Body.String(), `"BROWSE_PROJECTS":{"description"`) || !strings.Contains(myPermissions.Body.String(), `"havePermission":true`) {
		t.Fatal(myPermissions.Body.String())
	}
	all := call(viewerID, http.MethodGet, "/rest/api/3/permissions", "", http.StatusOK)
	if !strings.Contains(all.Body.String(), `"BROWSE_PROJECTS"`) || !strings.Contains(all.Body.String(), `"ADMINISTER"`) {
		t.Fatal(all.Body.String())
	}
	permitted := call(viewerID, http.MethodPost, "/rest/api/3/permissions/project", `{"permissions":["BROWSE_PROJECTS"]}`, http.StatusOK)
	if !strings.Contains(permitted.Body.String(), `"id":"`+projectID+`"`) {
		t.Fatal(permitted.Body.String())
	}
	bulk := call(adminID, http.MethodPost, "/rest/api/3/permissions/check", `{"accountId":"`+viewerID+`","globalPermissions":["ADMINISTER","BROWSE_USERS"],"projectPermissions":[{"permissions":["BROWSE_PROJECTS","ADMINISTER_PROJECTS"],"projects":[`+projectID+`]}]}`, http.StatusOK)
	if !strings.Contains(bulk.Body.String(), `"permission":"BROWSE_PROJECTS","projects":[`+projectID+`]`) {
		t.Fatal(bulk.Body.String())
	}
	call(viewerID, http.MethodPost, "/rest/api/3/permissions/check", `{"accountId":"`+leadID+`"}`, http.StatusForbidden)
	users := call(leadID, http.MethodGet, "/rest/api/3/user/permission/search?projectKey="+projectKey+"&permissions=BROWSE_PROJECTS&query=Permission", "", http.StatusOK)
	if !strings.Contains(users.Body.String(), viewerID) || strings.Contains(users.Body.String(), outsiderID) {
		t.Fatal(users.Body.String())
	}

	call(adminID, http.MethodDelete, schemePath+"/permission/"+strconv.FormatInt(scheme.Permissions[0].ID, 10), "", http.StatusNoContent)
	call(adminID, http.MethodPut, "/rest/api/3/project/"+projectKey+"/permissionscheme", `{"id":10000}`, http.StatusOK)
	call(adminID, http.MethodDelete, schemePath, "", http.StatusNoContent)
	call(adminID, http.MethodGet, schemePath, "", http.StatusNotFound)
	var actionCount int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type IN ('permission_scheme','permission_grant','project_permission_scheme')`, workspaceID).Scan(&actionCount); err != nil || actionCount < 5 {
		t.Fatalf("permission actions=%d err=%v", actionCount, err)
	}
}
