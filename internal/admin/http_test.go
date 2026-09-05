package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

func TestOrganizationAndGroupAPIJourney(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}

	workspaceID := store.NewID("ws")
	adminID := store.NewID("usr")
	memberID := store.NewID("usr")
	for _, userID := range []string{adminID, memberID} {
		if _, err := st.Pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$1 || '@example.invalid','unused',$1)`, userID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Admin API test')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(ctx, workspaceID, adminID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(ctx, workspaceID, memberID, "member"); err != nil {
		t.Fatal(err)
	}
	adminToken := store.NewID("secret")
	memberToken := store.NewID("secret")
	if err := st.CreateAPIToken(ctx, store.NewID("tok"), adminID, store.HashToken(adminToken), "admin-api-test"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAPIToken(ctx, store.NewID("tok"), memberID, store.HashToken(memberToken), "admin-api-test"); err != nil {
		t.Fatal(err)
	}
	organization, err := st.OrganizationByWorkspace(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE user_id IN ($1,$2)`, adminID, memberID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `
			DELETE FROM role_bindings rb
			WHERE rb.principal_id IN ($1,$2)
			   OR rb.scope_id=$3
			   OR rb.scope_id IN (SELECT id::text FROM sites WHERE organization_id=$3::uuid)
			   OR rb.scope_id IN (SELECT p.id::text FROM products p JOIN sites s ON s.id=p.site_id WHERE s.organization_id=$3::uuid)
			   OR rb.principal_id IN (SELECT g.id::text FROM groups g JOIN directories d ON d.id=g.directory_id WHERE d.organization_id=$3::uuid)`, adminID, memberID, organization.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organizations WHERE id::text=$1`, organization.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, adminID, memberID)
	}()

	handler := &Handler{Store: st, BaseURL: "https://zzira.example", WorkspaceSlug: workspaceID}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/v1/orgs", handler.Organizations)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}", handler.Organization)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories", handler.Directories)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/groups", handler.Groups)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/directories/{directoryId}/groups", handler.Groups)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/memberships", handler.GroupMembership)
	mux.HandleFunc("DELETE /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/memberships/{accountId}", handler.DeleteGroupMembership)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/workspaces", handler.Workspaces)
	mux.HandleFunc("POST /admin/v1/orgs/{orgId}/users/{userId}/role-assignments/assign", handler.UserRoleMutation)
	mux.HandleFunc("POST /admin/v1/orgs/{orgId}/users/{userId}/role-assignments/revoke", handler.UserRoleMutation)
	mux.HandleFunc("POST /admin/v1/orgs/{orgId}/users/{userId}/roles/assign", handler.UserRoleMutation)
	mux.HandleFunc("POST /admin/v1/orgs/{orgId}/users/{userId}/roles/revoke", handler.UserRoleMutation)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/role-assignments", handler.RoleAssignments)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/role-assignments/assign", handler.GroupRoleMutation)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/role-assignments/revoke", handler.GroupRoleMutation)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/users/{accountId}/role-assignments", handler.RoleAssignments)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/users", handler.ManagedUsers)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/users", handler.DirectoryUsers)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/users/count", handler.DirectoryUserCount)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/users/{userId}", handler.DirectoryUserDetails)
	mux.HandleFunc("DELETE /admin/v2/orgs/{orgId}/directories/{directoryId}/users/{accountId}", handler.DirectoryUserLifecycle)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/directories/{directoryId}/users/{accountId}/suspend", handler.DirectoryUserLifecycle)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/directories/{directoryId}/users/{accountId}/restore", handler.DirectoryUserLifecycle)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/users/invite", handler.InviteUsers)

	call := func(method, path, token string, body any, want int) map[string]any {
		t.Helper()
		var requestBody bytes.Buffer
		if body != nil {
			if err := json.NewEncoder(&requestBody).Encode(body); err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, "https://zzira.example"+path, &requestBody)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("%s %s status=%d, want %d; body=%s", method, path, response.Code, want, response.Body.String())
		}
		if response.Body.Len() == 0 {
			return nil
		}
		var decoded map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode %s %s: %v; body=%s", method, path, err, response.Body.String())
		}
		return decoded
	}

	call(http.MethodGet, "/admin/v1/orgs", "", nil, http.StatusUnauthorized)
	call(http.MethodGet, "/admin/v1/orgs", memberToken, nil, http.StatusForbidden)
	orgs := call(http.MethodGet, "/admin/v1/orgs", adminToken, nil, http.StatusOK)
	data := orgs["data"].([]any)
	if len(data) != 1 || data[0].(map[string]any)["id"] != organization.ID {
		t.Fatalf("unexpected organization page: %#v", orgs)
	}
	orgPath := "/admin/v1/orgs/" + organization.ID
	call(http.MethodGet, orgPath, adminToken, nil, http.StatusOK)

	directoryPage := call(http.MethodGet, "/admin/v2/orgs/"+organization.ID+"/directories", adminToken, nil, http.StatusOK)
	directories := directoryPage["data"].([]any)
	if len(directories) != 1 {
		t.Fatalf("unexpected directory page: %#v", directoryPage)
	}
	directoryID := directories[0].(map[string]any)["directoryId"].(string)
	groupsPath := "/admin/v2/orgs/" + organization.ID + "/directories/" + directoryID + "/groups"
	call(http.MethodPost, groupsPath, adminToken, map[string]string{"name": "invalid", "unknown": "field"}, http.StatusBadRequest)
	call(http.MethodPost, groupsPath, adminToken, map[string]string{"name": "support-leads", "description": "Service owners"}, http.StatusCreated)
	groupsPage := call(http.MethodGet, groupsPath, adminToken, nil, http.StatusOK)
	groups := groupsPage["data"].([]any)
	if len(groups) != 1 {
		t.Fatalf("unexpected group page: %#v", groupsPage)
	}
	groupID := groups[0].(map[string]any)["id"].(string)
	membershipPath := groupsPath + "/" + groupID + "/memberships"
	call(http.MethodPost, membershipPath, adminToken, map[string]string{"accountId": memberID}, http.StatusNoContent)
	call(http.MethodPost, membershipPath, adminToken, map[string]string{"accountId": memberID}, http.StatusConflict)

	workspaces := call(http.MethodPost, "/admin/v2/orgs/"+organization.ID+"/workspaces", adminToken, map[string]any{"limit": 20}, http.StatusOK)
	workspaceData := workspaces["data"].([]any)
	if len(workspaceData) != 3 {
		t.Fatalf("workspace resources=%d, want 3", len(workspaceData))
	}
	resourceID := ""
	for _, item := range workspaceData {
		workspace := item.(map[string]any)
		attributes := workspace["attributes"].(map[string]any)
		if attributes["typeKey"] == "jira-service-management" {
			resourceID = workspace["id"].(string)
		}
	}
	if resourceID == "" {
		t.Fatal("Jira Service Management workspace resource was not returned")
	}
	groupRolesPath := groupsPath + "/" + groupID + "/role-assignments"
	call(http.MethodPost, groupRolesPath+"/assign", adminToken, map[string]string{"resourceId": resourceID, "roleId": "atlassian/user"}, http.StatusOK)
	groupRoles := call(http.MethodGet, groupRolesPath+"?roleIds=atlassian/user", adminToken, nil, http.StatusOK)
	if len(groupRoles["data"].([]any)) != 1 {
		t.Fatalf("unexpected group role assignments: %#v", groupRoles)
	}
	userRolesPath := "/admin/v2/orgs/" + organization.ID + "/directories/" + directoryID + "/users/" + memberID + "/role-assignments"
	userRoles := call(http.MethodGet, userRolesPath+"?resourceIds="+resourceID, adminToken, nil, http.StatusOK)
	if len(userRoles["data"].([]any)) != 1 {
		t.Fatalf("effective user role assignments missing group role: %#v", userRoles)
	}
	assignments := userRoles["data"].([]any)[0].(map[string]any)["roleAssignments"].([]any)
	foundGroupMethod := false
	if len(assignments) == 1 {
		for _, method := range assignments[0].(map[string]any)["roleAssignmentMethods"].([]any) {
			foundGroupMethod = foundGroupMethod || method == "group_direct"
		}
	}
	if !foundGroupMethod {
		t.Fatalf("effective user assignment method is not group_direct: %#v", userRoles)
	}
	call(http.MethodPost, groupRolesPath+"/revoke", adminToken, map[string]string{"resourceId": resourceID, "roleId": "atlassian/user"}, http.StatusNoContent)

	userMutationPath := "/admin/v1/orgs/" + organization.ID + "/users/" + memberID + "/roles"
	call(http.MethodPost, userMutationPath+"/assign", adminToken, map[string]string{"resource": resourceID, "role": "atlassian/customer"}, http.StatusNoContent)
	call(http.MethodPost, userMutationPath+"/revoke", adminToken, map[string]string{"resource": resourceID, "role": "atlassian/customer"}, http.StatusNoContent)
	organizationRolePath := "/admin/v1/orgs/" + organization.ID + "/users/" + memberID + "/role-assignments"
	call(http.MethodPost, organizationRolePath+"/assign", adminToken, map[string]string{"role": "atlassian/org-admin"}, http.StatusNoContent)
	call(http.MethodPost, organizationRolePath+"/revoke", adminToken, map[string]string{"role": "atlassian/org-admin"}, http.StatusNoContent)
	call(http.MethodDelete, membershipPath+"/"+memberID, adminToken, nil, http.StatusNoContent)

	managedUsers := call(http.MethodGet, "/admin/v1/orgs/"+organization.ID+"/users", adminToken, nil, http.StatusOK)
	if managedUsers["meta"].(map[string]any)["total"].(float64) != 2 {
		t.Fatalf("unexpected managed user page: %#v", managedUsers)
	}
	inviteEmail := store.NewID("invite") + "@example.invalid"
	invited := call(http.MethodPost, "/admin/v2/orgs/"+organization.ID+"/users/invite", adminToken, map[string]any{"emails": []string{inviteEmail}}, http.StatusOK)
	invitedID := invited["data"].([]any)[0].(map[string]any)["id"].(string)
	defer func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, invitedID) }()
	usersPath := "/admin/v2/orgs/" + organization.ID + "/directories/" + directoryID + "/users"
	call(http.MethodGet, usersPath+"?searchTerm="+inviteEmail, adminToken, nil, http.StatusOK)
	call(http.MethodGet, usersPath+"/"+invitedID, adminToken, nil, http.StatusOK)
	invitedToken := store.NewID("secret")
	if err := st.CreateAPIToken(ctx, store.NewID("tok"), invitedID, store.HashToken(invitedToken), "suspension-test"); err != nil {
		t.Fatal(err)
	}
	call(http.MethodPost, usersPath+"/"+invitedID+"/suspend", adminToken, nil, http.StatusNoContent)
	if _, err := st.UserByAPIToken(ctx, store.HashToken(invitedToken)); err == nil {
		t.Fatal("suspension did not revoke the invited user's API token")
	}
	call(http.MethodPost, usersPath+"/"+invitedID+"/restore", adminToken, nil, http.StatusNoContent)
	call(http.MethodDelete, usersPath+"/"+invitedID, adminToken, nil, http.StatusNoContent)

	audit, err := st.OrganizationAuditEvents(ctx, organization.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 13 {
		t.Fatalf("audit events=%d, want 13", len(audit))
	}
}
