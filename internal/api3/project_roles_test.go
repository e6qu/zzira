package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestProjectRoleLifecycleAndAssignments(t *testing.T) {
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
	workspaceID, adminID, memberID, inactiveID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	suffix := time.Now().UnixNano() % 100000000
	projectKey, inheritedKey := fmt.Sprintf("R%08d", suffix), fmt.Sprintf("I%08d", suffix)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Project role test')`, workspaceID)
	for _, identity := range []struct {
		id, role string
		active   bool
	}{{adminID, "admin", true}, {memberID, "member", true}, {inactiveID, "member", true}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name,active) VALUES($1,$2,'test',$3,$4)`, identity.id, identity.id+"@example.test", "Role "+identity.id, identity.active)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	var directoryID string
	if err = st.Pool.QueryRow(ctx, `SELECT d.id::text FROM directories d JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1 LIMIT 1`, workspaceID).Scan(&directoryID); err != nil {
		t.Fatal(err)
	}
	var groupID string
	if err = st.Pool.QueryRow(ctx, `INSERT INTO groups(directory_id,name,description) VALUES($1,'Delivery leads','Release coordinators') RETURNING id::text`, directoryID).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO group_members(group_id,user_id) VALUES($1,$2)`, groupID, memberID)
	t.Cleanup(func() {
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID, inactiveID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if user != "" {
			r.SetBasicAuth(user+"@example.test", user)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	createProject := func(key string) string {
		t.Helper()
		response := call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+key+`","name":"Role project `+key+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)
		var project struct {
			ID int64 `json:"id"`
		}
		if decodeErr := json.Unmarshal(response.Body.Bytes(), &project); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return strconv.FormatInt(project.ID, 10)
	}
	projectID := createProject(projectKey)

	call(memberID, http.MethodGet, "/rest/api/3/role", "", http.StatusForbidden)
	roles := call(adminID, http.MethodGet, "/rest/api/3/role", "", http.StatusOK)
	if !strings.Contains(roles.Body.String(), `"id":10000`) || !strings.Contains(roles.Body.String(), `"id":10001`) {
		t.Fatal(roles.Body.String())
	}
	call(memberID, http.MethodGet, "/rest/api/3/project/"+projectKey+"/role", "", http.StatusNotFound)

	created := call(adminID, http.MethodPost, "/rest/api/3/role", `{"name":"Developers","description":"Build and operate the product"}`, http.StatusOK)
	var role struct {
		ID int64 `json:"id"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &role); err != nil || role.ID <= 10001 {
		t.Fatalf("created role=%+v err=%v body=%s", role, err, created.Body.String())
	}
	rolePath := "/rest/api/3/role/" + strconv.FormatInt(role.ID, 10)
	call(adminID, http.MethodPost, "/rest/api/3/role", `{"name":"developers"}`, http.StatusConflict)
	call(adminID, http.MethodPost, "/rest/api/3/role", `{"name":" Developers"}`, http.StatusBadRequest)
	call(adminID, http.MethodGet, rolePath, "", http.StatusOK)
	partial := call(adminID, http.MethodPost, rolePath, `{"description":"Own product delivery"}`, http.StatusOK)
	if !strings.Contains(partial.Body.String(), `"description":"Own product delivery"`) {
		t.Fatal(partial.Body.String())
	}
	nameWins := call(adminID, http.MethodPost, rolePath, `{"name":"Engineers","description":"Ignored by Jira partial updates"}`, http.StatusOK)
	if !strings.Contains(nameWins.Body.String(), `"name":"Engineers"`) || !strings.Contains(nameWins.Body.String(), `"description":"Own product delivery"`) {
		t.Fatal(nameWins.Body.String())
	}
	call(adminID, http.MethodPut, rolePath, `{"name":"Engineers"}`, http.StatusBadRequest)
	call(adminID, http.MethodPut, rolePath, `{"name":"Engineers","description":"Design, build, and operate"}`, http.StatusOK)

	defaultActorsPath := rolePath + "/actors"
	call(adminID, http.MethodPost, defaultActorsPath, `{"user":["`+memberID+`"],"groupId":["`+groupID+`"]}`, http.StatusBadRequest)
	call(adminID, http.MethodPost, defaultActorsPath, `{"user":["`+memberID+`"]}`, http.StatusOK)
	call(adminID, http.MethodPost, defaultActorsPath, `{"groupId":["`+groupID+`"]}`, http.StatusOK)
	defaultActors := call(adminID, http.MethodGet, defaultActorsPath, "", http.StatusOK)
	if !strings.Contains(defaultActors.Body.String(), memberID) || !strings.Contains(defaultActors.Body.String(), groupID) {
		t.Fatal(defaultActors.Body.String())
	}
	call(adminID, http.MethodDelete, defaultActorsPath+"?group="+url.QueryEscape("Delivery leads"), "", http.StatusOK)
	call(adminID, http.MethodPost, defaultActorsPath, `{"group":["Delivery leads"]}`, http.StatusOK)

	inheritedID := createProject(inheritedKey)
	projectRolePath := "/rest/api/3/project/" + inheritedKey + "/role/" + strconv.FormatInt(role.ID, 10)
	inherited := call(adminID, http.MethodGet, projectRolePath, "", http.StatusOK)
	if !strings.Contains(inherited.Body.String(), memberID) || !strings.Contains(inherited.Body.String(), groupID) {
		t.Fatal(inherited.Body.String())
	}
	filterID := store.NewID("flt")
	if _, err = st.CreateFilter(ctx, filterID, workspaceID, "Role-scoped delivery", `project = `+inheritedKey, "", adminID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { exec(`DELETE FROM filters WHERE id=$1`, filterID) })
	if _, err = st.AddFilterPermission(ctx, workspaceID, adminID, filterID, store.FilterPermissionInput{Type: "projectRole", ProjectID: inheritedID, ProjectRoleID: strconv.FormatInt(role.ID, 10)}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.FilterByID(ctx, workspaceID, memberID, filterID); err != nil {
		t.Fatalf("custom project role did not grant filter access: %v", err)
	}
	roleMap := call(adminID, http.MethodGet, "/rest/api/3/project/"+inheritedKey+"/role", "", http.StatusOK)
	if !strings.Contains(roleMap.Body.String(), `"Engineers":"https://zzira.test/rest/api/3/project/`+inheritedKey+`/role/`) {
		t.Fatal(roleMap.Body.String())
	}
	details := call(adminID, http.MethodGet, "/rest/api/3/project/"+inheritedKey+"/roledetails?currentMember=true&excludeConnectAddons=true", "", http.StatusOK)
	if !strings.Contains(details.Body.String(), `"name":"Administrators"`) || strings.Contains(details.Body.String(), `"name":"Engineers"`) {
		t.Fatal(details.Body.String())
	}
	call(adminID, http.MethodGet, "/rest/api/3/project/"+inheritedKey+"/roledetails?excludeOtherServiceRoles=invalid", "", http.StatusBadRequest)

	assertProjectAdmin := func(userID string, want bool) {
		t.Helper()
		allowed, lookupErr := st.CanAdministerProject(ctx, workspaceID, userID, inheritedID)
		if lookupErr != nil || allowed != want {
			t.Fatalf("project admin user=%s got=%t want=%t err=%v", userID, allowed, want, lookupErr)
		}
	}
	assertProjectAdmin(inactiveID, false)
	if err = st.AddMember(ctx, workspaceID, inactiveID, "admin"); err != nil {
		t.Fatal(err)
	}
	assertProjectAdmin(inactiveID, true)
	if err = st.AddMember(ctx, workspaceID, inactiveID, "member"); err != nil {
		t.Fatal(err)
	}
	assertProjectAdmin(inactiveID, false)
	exec(`UPDATE projects SET lead_account_id=$2 WHERE id=$1`, inheritedID, inactiveID)
	assertProjectAdmin(inactiveID, true)
	exec(`UPDATE projects SET lead_account_id=$2 WHERE id=$1`, inheritedID, adminID)
	assertProjectAdmin(inactiveID, false)

	administratorPath := "/rest/api/3/project/" + inheritedKey + "/role/10000"
	call(adminID, http.MethodPost, administratorPath, `{"user":["`+memberID+`"]}`, http.StatusOK)
	call(memberID, http.MethodGet, "/rest/api/3/project/"+inheritedKey+"/role", "", http.StatusOK)
	call(memberID, http.MethodPost, projectRolePath, `{"group":["Delivery leads"]}`, http.StatusOK)
	call(memberID, http.MethodPut, projectRolePath, `{"categorisedActors":{"atlassian-user-role-actor":["`+inactiveID+`"],"atlassian-group-role-actor-id":["`+groupID+`"]}}`, http.StatusOK)
	exec(`UPDATE users SET active=FALSE WHERE id=$1`, inactiveID)
	withInactive := call(memberID, http.MethodGet, projectRolePath, "", http.StatusOK)
	withoutInactive := call(memberID, http.MethodGet, projectRolePath+"?excludeInactiveUsers=true", "", http.StatusOK)
	if !strings.Contains(withInactive.Body.String(), inactiveID) || strings.Contains(withoutInactive.Body.String(), inactiveID) {
		t.Fatalf("with=%s without=%s", withInactive.Body.String(), withoutInactive.Body.String())
	}
	call(memberID, http.MethodDelete, projectRolePath+"?groupId="+url.QueryEscape(groupID), "", http.StatusNoContent)
	call(adminID, http.MethodPost, projectRolePath, `{"user":["`+memberID+`"]}`, http.StatusOK)

	call(adminID, http.MethodDelete, rolePath, "", http.StatusConflict)
	replacementResponse := call(adminID, http.MethodPost, "/rest/api/3/role", `{"name":"Delivery partners","description":"Replacement delivery role"}`, http.StatusOK)
	var replacement struct {
		ID int64 `json:"id"`
	}
	if err = json.Unmarshal(replacementResponse.Body.Bytes(), &replacement); err != nil {
		t.Fatal(err)
	}
	call(adminID, http.MethodDelete, rolePath+"?swap="+strconv.FormatInt(replacement.ID, 10), "", http.StatusNoContent)
	call(adminID, http.MethodGet, rolePath, "", http.StatusNotFound)
	moved := call(adminID, http.MethodGet, "/rest/api/3/project/"+inheritedID+"/role/"+strconv.FormatInt(replacement.ID, 10), "", http.StatusOK)
	if !strings.Contains(moved.Body.String(), inactiveID) {
		t.Fatal(moved.Body.String())
	}
	shared, err := st.FilterByID(ctx, workspaceID, memberID, filterID)
	if err != nil || len(shared.SharePermissions) != 1 || shared.SharePermissions[0].ProjectRoleID != strconv.FormatInt(replacement.ID, 10) {
		t.Fatalf("swapped role filter share=%+v err=%v", shared, err)
	}
	replacementPath := "/rest/api/3/role/" + strconv.FormatInt(replacement.ID, 10)
	call(adminID, http.MethodDelete, replacementPath+"/actors?user="+url.QueryEscape(memberID), "", http.StatusOK)
	call(adminID, http.MethodDelete, replacementPath+"?swap=10001", "", http.StatusNoContent)

	ownersResponse := call(adminID, http.MethodPost, "/rest/api/3/role", `{"name":"Project owners"}`, http.StatusOK)
	contributorsResponse := call(adminID, http.MethodPost, "/rest/api/3/role", `{"name":"Contributors"}`, http.StatusOK)
	var owners, contributors struct {
		ID int64 `json:"id"`
	}
	if err = json.Unmarshal(ownersResponse.Body.Bytes(), &owners); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(contributorsResponse.Body.Bytes(), &contributors); err != nil {
		t.Fatal(err)
	}
	call(adminID, http.MethodDelete, "/rest/api/3/role/10000?swap="+strconv.FormatInt(owners.ID, 10), "", http.StatusNoContent)
	call(memberID, http.MethodGet, "/rest/api/3/project/"+inheritedKey+"/role", "", http.StatusOK)
	call(adminID, http.MethodDelete, "/rest/api/3/role/10001?swap="+strconv.FormatInt(contributors.ID, 10), "", http.StatusNoContent)
	postSwapKey := fmt.Sprintf("S%08d", suffix)
	createProject(postSwapKey)
	postSwapMember := call(adminID, http.MethodGet, "/rest/api/3/project/"+postSwapKey+"/role/"+strconv.FormatInt(contributors.ID, 10), "", http.StatusOK)
	postSwapAdmin := call(adminID, http.MethodGet, "/rest/api/3/project/"+postSwapKey+"/role/"+strconv.FormatInt(owners.ID, 10), "", http.StatusOK)
	if !strings.Contains(postSwapMember.Body.String(), memberID) || !strings.Contains(postSwapMember.Body.String(), `"default":true`) {
		t.Fatal(postSwapMember.Body.String())
	}
	if !strings.Contains(postSwapAdmin.Body.String(), adminID) || !strings.Contains(postSwapAdmin.Body.String(), `"admin":true`) {
		t.Fatal(postSwapAdmin.Body.String())
	}

	var actions int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type IN ('project_role','project_role_actors','project_role_default_actors')`, workspaceID).Scan(&actions); err != nil || actions < 8 {
		t.Fatalf("role actions=%d err=%v", actions, err)
	}
	_ = projectID
}
