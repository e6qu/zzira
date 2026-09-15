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

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestAppDeclaredPermissions covers the project and global permissions an app
// declares: they join Jira's catalog under the app and module keys, schemes
// grant the project ones, and global ones follow their default grants.
func TestAppDeclaredPermissions(t *testing.T) {
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
	stamp := time.Now().UnixNano() % 1000000
	projectKey, appKey := fmt.Sprintf("AP%06d", stamp), fmt.Sprintf("insights-%d", stamp)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'App permissions')`, workspaceID)
	for _, identity := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Permissions "+identity.id)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	principals := []string{}
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM app_installations WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range append([]string{adminID, memberID}, principals...) {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})

	raw := []byte(`{"key":"` + appKey + `","name":"Insights","baseUrl":"https://insights.example.test","authentication":{"type":"jwt"},"scopes":["READ"],
		"modules":{
		  "jiraProjectPermissions":[{"key":"approve-release","name":{"value":"Approve releases"},"description":{"value":"Approve a release."},"category":"projects"}],
		  "jiraGlobalPermissions":[{"key":"view-insights","name":{"value":"View insights"},"description":{"value":"See insights."},"defaultGrants":["ALL"]},
		    {"key":"manage-insights","name":{"value":"Manage insights"},"description":{"value":"Manage insights."},"defaultGrants":["JIRA-ADMINISTRATORS"]}]}}`)
	descriptor, err := apps.ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := st.InstallApp(ctx, workspaceID, adminID, descriptor, raw, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	principals = append(principals, installation.PrincipalID)
	if len(installation.Permissions) != 3 {
		t.Fatalf("installed permissions = %+v", installation.Permissions)
	}
	projectPermission, viewPermission, managePermission := appKey+"__approve-release", appKey+"__view-insights", appKey+"__manage-insights"

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(user+"@example.test", user)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	permissionsOf := func(body string) map[string]struct {
		Type           string `json:"type"`
		Name           string `json:"name"`
		HavePermission bool   `json:"havePermission"`
	} {
		t.Helper()
		var decoded struct {
			Permissions map[string]struct {
				Type           string `json:"type"`
				Name           string `json:"name"`
				HavePermission bool   `json:"havePermission"`
			} `json:"permissions"`
		}
		if decodeErr := json.Unmarshal([]byte(body), &decoded); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return decoded.Permissions
	}

	catalog := permissionsOf(call(memberID, http.MethodGet, "/rest/api/3/permissions", "", http.StatusOK))
	if catalog[projectPermission].Type != "PROJECT" || catalog[projectPermission].Name != "Approve releases" || catalog[projectPermission].HavePermission {
		t.Fatalf("project app permission = %+v", catalog[projectPermission])
	}
	if !catalog[viewPermission].HavePermission || catalog[viewPermission].Type != "GLOBAL" {
		t.Fatalf("view permission = %+v", catalog[viewPermission])
	}
	if catalog[managePermission].HavePermission {
		t.Fatalf("a member holds an administrators-only app permission: %+v", catalog[managePermission])
	}
	if admin := permissionsOf(call(adminID, http.MethodGet, "/rest/api/3/mypermissions?permissions="+managePermission, "", http.StatusOK)); !admin[managePermission].HavePermission {
		t.Fatalf("administrator app permission = %+v", admin)
	}

	// A scheme grants the project permission; unknown and global keys are refused.
	call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"App permissions `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)
	var schemes struct {
		PermissionSchemes []struct {
			ID int64 `json:"id"`
		} `json:"permissionSchemes"`
	}
	if err = json.Unmarshal([]byte(call(adminID, http.MethodGet, "/rest/api/3/project/"+projectKey+"/permissionscheme", "", http.StatusOK)), &schemes.PermissionSchemes); err != nil {
		var single struct {
			ID int64 `json:"id"`
		}
		if err = json.Unmarshal([]byte(call(adminID, http.MethodGet, "/rest/api/3/project/"+projectKey+"/permissionscheme", "", http.StatusOK)), &single); err != nil {
			t.Fatal(err)
		}
		schemes.PermissionSchemes = append(schemes.PermissionSchemes, single)
	}
	grantPath := fmt.Sprintf("/rest/api/3/permissionscheme/%d/permission", schemes.PermissionSchemes[0].ID)
	call(adminID, http.MethodPost, grantPath, `{"permission":"NOT_A_PERMISSION","holder":{"type":"user","value":"`+memberID+`"}}`, http.StatusBadRequest)
	call(adminID, http.MethodPost, grantPath, `{"permission":"`+viewPermission+`","holder":{"type":"user","value":"`+memberID+`"}}`, http.StatusBadRequest)
	if mine := permissionsOf(call(memberID, http.MethodGet, "/rest/api/3/mypermissions?projectKey="+projectKey+"&permissions="+projectPermission, "", http.StatusOK)); mine[projectPermission].HavePermission {
		t.Fatal("the member holds the app permission before a grant")
	}
	call(adminID, http.MethodPost, grantPath, `{"permission":"`+projectPermission+`","holder":{"type":"user","value":"`+memberID+`"}}`, http.StatusCreated)
	if mine := permissionsOf(call(memberID, http.MethodGet, "/rest/api/3/mypermissions?projectKey="+projectKey+"&permissions="+projectPermission, "", http.StatusOK)); !mine[projectPermission].HavePermission {
		t.Fatalf("granted app permission = %+v", mine)
	}
	if checked := call(memberID, http.MethodPost, "/rest/api/3/permissions/check", `{"globalPermissions":["`+viewPermission+`","`+managePermission+`"]}`, http.StatusOK); !strings.Contains(checked, viewPermission) || strings.Contains(checked, managePermission) {
		t.Fatalf("bulk check = %s", checked)
	}
	// Another user's permissions need Administer Jira, unless an app asks from
	// its server.
	otherUser := `{"accountId":"` + adminID + `","globalPermissions":["` + managePermission + `"]}`
	call(memberID, http.MethodPost, "/rest/api/3/permissions/check", otherUser, http.StatusForbidden)
	appRequest := httptest.NewRequest(http.MethodPost, "/rest/api/3/permissions/check", strings.NewReader(otherUser))
	appRequest = appRequest.WithContext(apps.ContextWithInstallation(ctx, installation))
	appRequest.Header.Set("Content-Type", "application/json")
	appResponse := httptest.NewRecorder()
	h.ServeHTTP(appResponse, appRequest)
	if appResponse.Code != http.StatusOK || !strings.Contains(appResponse.Body.String(), managePermission) {
		t.Fatalf("app bulk check: %d %s", appResponse.Code, appResponse.Body.String())
	}
	call(memberID, http.MethodPost, "/rest/api/3/permissions/check", `{"projectPermissions":[{"permissions":["BROWSE_PROJECTS"],"projects":[`+strings.TrimSuffix(strings.Repeat("1,", 1001), ",")+`]}]}`, http.StatusBadRequest)
}
