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

// TestJiraExpansionsAndListFilters covers the documented expansions and list
// filters: release drivers, approvers and operations; dashboards shared with
// groups, projects and project roles; screen, scheme, field configuration,
// context and option filters; field screen tabs and last use; filter shared
// users; and the user, group, role and field details of scheme holders.
func TestJiraExpansionsAndListFilters(t *testing.T) {
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
	workspaceID, adminID, memberID, outsiderID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	stamp := time.Now().UnixNano() % 1000000
	projectKey := fmt.Sprintf("EX%06d", stamp)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Expansions and filters')`, workspaceID)
	for _, identity := range []struct{ id, role, name string }{{adminID, "admin", "Ada Admin"}, {memberID, "member", "Max Member"}, {outsiderID, "member", "Olga Outsider"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", identity.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	var directoryID, groupID string
	if err = st.Pool.QueryRow(ctx, `SELECT d.id::text FROM directories d JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1 LIMIT 1`, workspaceID).Scan(&directoryID); err != nil {
		t.Fatal(err)
	}
	groupName := fmt.Sprintf("Release crew %d", stamp)
	if err = st.Pool.QueryRow(ctx, `INSERT INTO groups(directory_id,name) VALUES($1,$2) RETURNING id::text`, directoryID, groupName).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO group_members(group_id,user_id) VALUES($1,$2)`, groupID, memberID)
	t.Cleanup(func() {
		exec(`DELETE FROM dashboards WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM filters WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM group_members WHERE group_id::text=$1`, groupID)
		exec(`DELETE FROM groups WHERE id::text=$1`, groupID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID, outsiderID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(user+"@example.test", user)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	object := func(body string) map[string]any {
		t.Helper()
		decoded := map[string]any{}
		if decodeErr := json.Unmarshal([]byte(body), &decoded); decodeErr != nil {
			t.Fatalf("decode %s: %v", body, decodeErr)
		}
		return decoded
	}
	values := func(body string) []any {
		t.Helper()
		list, _ := object(body)["values"].([]any)
		return list
	}

	project := object(call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Expansion `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated))
	projectID := fmt.Sprint(project["id"])
	issue := object(call(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Expansion work","issuetype":{"name":"Task"}}}`, http.StatusCreated))

	// Releases: driver, approvers and operations.
	call(adminID, http.MethodPost, "/rest/api/3/version", `{"name":"Bad driver","projectId":`+projectID+`,"driver":"usr_nobody"}`, http.StatusBadRequest)
	version := object(call(adminID, http.MethodPost, "/rest/api/3/version", `{"name":"2.0","projectId":`+projectID+`,"driver":"`+memberID+`"}`, http.StatusCreated))
	versionID := version["id"].(string)
	if _, leaked := version["driver"]; leaked {
		t.Fatalf("an unexpanded version carries its driver: %v", version)
	}
	if err = st.AddVersionApprover(ctx, workspaceID, adminID, versionID, memberID, "Sign off the release notes"); err != nil {
		t.Fatal(err)
	}
	if err = st.DecideVersionApproval(ctx, workspaceID, outsiderID, versionID, true, ""); err == nil {
		t.Fatal("someone who was not asked approved the release")
	}
	if err = st.DecideVersionApproval(ctx, workspaceID, memberID, versionID, false, "Notes are missing the migration"); err != nil {
		t.Fatal(err)
	}
	expanded := object(call(adminID, http.MethodGet, "/rest/api/3/version/"+versionID+"?expand=driver,approvers,operations", "", http.StatusOK))
	approvers := expanded["approvers"].([]any)
	if expanded["driver"] != memberID || len(approvers) != 1 || approvers[0].(map[string]any)["status"] != "DECLINED" || approvers[0].(map[string]any)["declineReason"] != "Notes are missing the migration" {
		t.Fatalf("expanded version = %v", expanded)
	}
	if operations := expanded["operations"].([]any); len(operations) != 4 || operations[0].(map[string]any)["id"] != "edit_version" {
		t.Fatalf("administrator operations = %v", expanded["operations"])
	}
	if operations := object(call(memberID, http.MethodGet, "/rest/api/3/version/"+versionID+"?expand=operations", "", http.StatusOK))["operations"].([]any); len(operations) != 0 {
		t.Fatalf("a member may manage the release: %v", operations)
	}

	// Dashboards shared with a group, a project and a project role.
	groupDashboard := object(call(adminID, http.MethodPost, "/rest/api/3/dashboard", `{"name":"Crew board","sharePermissions":[{"type":"group","group":{"groupId":"`+groupID+`"}}],"editPermissions":[]}`, http.StatusOK))["id"].(string)
	projectDashboard := object(call(adminID, http.MethodPost, "/rest/api/3/dashboard", `{"name":"Project board","sharePermissions":[{"type":"project","project":{"id":"`+projectID+`"}}],"editPermissions":[]}`, http.StatusOK))["id"].(string)
	adminRoleDashboard := object(call(adminID, http.MethodPost, "/rest/api/3/dashboard", `{"name":"Administrators board","sharePermissions":[{"type":"project","project":{"id":"`+projectKey+`"},"role":{"id":10000}}],"editPermissions":[]}`, http.StatusOK))
	if share := adminRoleDashboard["sharePermissions"].([]any)[0].(map[string]any); share["type"] != "projectRole" || share["role"].(map[string]any)["name"] != "Administrators" || share["project"].(map[string]any)["id"] != projectID {
		t.Fatalf("project role share = %v", share)
	}
	call(memberID, http.MethodGet, "/rest/api/3/dashboard/"+groupDashboard, "", http.StatusOK)
	call(outsiderID, http.MethodGet, "/rest/api/3/dashboard/"+groupDashboard, "", http.StatusNotFound)
	call(outsiderID, http.MethodGet, "/rest/api/3/dashboard/"+projectDashboard, "", http.StatusOK)
	call(memberID, http.MethodGet, "/rest/api/3/dashboard/"+adminRoleDashboard["id"].(string), "", http.StatusNotFound)
	if found := values(call(memberID, http.MethodGet, "/rest/api/3/dashboard/search?groupId="+groupID, "", http.StatusOK)); len(found) != 1 {
		t.Fatalf("dashboards shared with the group = %v", found)
	}
	if found := values(call(memberID, http.MethodGet, "/rest/api/3/dashboard/search?groupname="+strings.ReplaceAll(groupName, " ", "%20"), "", http.StatusOK)); len(found) != 1 {
		t.Fatalf("dashboards shared with the group name = %v", found)
	}
	if found := values(call(outsiderID, http.MethodGet, "/rest/api/3/dashboard/search?projectId="+projectID, "", http.StatusOK)); len(found) != 1 {
		t.Fatalf("dashboards shared with the project = %v", found)
	}
	call(adminID, http.MethodPost, "/rest/api/3/dashboard", `{"name":"Public","sharePermissions":[{"type":"global"}],"editPermissions":[]}`, http.StatusBadRequest)
	call(adminID, http.MethodPost, "/rest/api/3/dashboard", `{"name":"Missing group","sharePermissions":[{"type":"group","group":{"groupId":"0"}}],"editPermissions":[]}`, http.StatusBadRequest)

	// Screens, screen schemes and work type screen schemes.
	if found := values(call(adminID, http.MethodGet, "/rest/api/3/screens?scope=PROJECT", "", http.StatusOK)); len(found) != 0 {
		t.Fatalf("project-scoped screens = %v", found)
	}
	screens := values(call(adminID, http.MethodGet, "/rest/api/3/screens?scope=GLOBAL&orderBy=-name", "", http.StatusOK))
	for i := 1; i < len(screens); i++ {
		if strings.ToLower(screens[i-1].(map[string]any)["name"].(string)) < strings.ToLower(screens[i].(map[string]any)["name"].(string)) {
			t.Fatalf("screens are not in descending name order: %v", screens)
		}
	}
	call(adminID, http.MethodGet, "/rest/api/3/screens?orderBy=position", "", http.StatusBadRequest)
	for _, scheme := range values(call(adminID, http.MethodGet, "/rest/api/3/screenscheme?expand=issueTypeScreenSchemes", "", http.StatusOK)) {
		if _, ok := scheme.(map[string]any)["issueTypeScreenSchemes"].(map[string]any); !ok {
			t.Fatalf("screen scheme without its work type screen schemes: %v", scheme)
		}
	}
	if found := values(call(adminID, http.MethodGet, "/rest/api/3/screenscheme?queryString=no-such-scheme", "", http.StatusOK)); len(found) != 0 {
		t.Fatalf("screen scheme query = %v", found)
	}
	for _, scheme := range values(call(adminID, http.MethodGet, "/rest/api/3/issuetypescreenscheme?expand=projects&orderBy=name", "", http.StatusOK)) {
		if _, ok := scheme.(map[string]any)["projects"].(map[string]any); !ok {
			t.Fatalf("work type screen scheme without projects: %v", scheme)
		}
	}

	// Field configurations, contexts, options, screens and last use.
	for _, configuration := range values(call(adminID, http.MethodGet, "/rest/api/3/fieldconfiguration?isDefault=true", "", http.StatusOK)) {
		if configuration.(map[string]any)["isDefault"] != true {
			t.Fatalf("a non-default configuration matched isDefault: %v", configuration)
		}
	}
	if found := values(call(adminID, http.MethodGet, "/rest/api/3/fieldconfiguration?query=no-such-configuration", "", http.StatusOK)); len(found) != 0 {
		t.Fatalf("field configuration query = %v", found)
	}
	call(adminID, http.MethodGet, "/rest/api/3/fieldconfiguration?isDefault=maybe", "", http.StatusBadRequest)
	fieldID := object(call(adminID, http.MethodPost, "/rest/api/3/field", `{"name":"Rollout ring `+fmt.Sprint(stamp)+`","type":"select"}`, http.StatusCreated))["id"].(string)
	if found := values(call(adminID, http.MethodGet, "/rest/api/3/field/"+fieldID+"/context?isGlobalContext=true&isAnyIssueType=true", "", http.StatusOK)); len(found) != 1 {
		t.Fatalf("global contexts = %v", found)
	}
	if found := values(call(adminID, http.MethodGet, "/rest/api/3/field/"+fieldID+"/context?isGlobalContext=false", "", http.StatusOK)); len(found) != 0 {
		t.Fatalf("project contexts = %v", found)
	}
	call(adminID, http.MethodGet, "/rest/api/3/field/"+fieldID+"/context?isAnyIssueType=sometimes", "", http.StatusBadRequest)
	contextID := fmt.Sprint(values(call(adminID, http.MethodGet, "/rest/api/3/field/"+fieldID+"/context", "", http.StatusOK))[0].(map[string]any)["id"])
	optionPath := "/rest/api/3/field/" + fieldID + "/context/" + contextID + "/option"
	created := object(call(adminID, http.MethodPost, optionPath, `{"options":[{"value":"Canary"},{"value":"Broad"}]}`, http.StatusOK))["options"].([]any)
	canaryID := fmt.Sprint(created[0].(map[string]any)["id"])
	if found := values(call(adminID, http.MethodGet, optionPath+"?optionId="+canaryID+"&onlyOptions=true", "", http.StatusOK)); len(found) != 1 || found[0].(map[string]any)["value"] != "Canary" {
		t.Fatalf("option by id = %v", found)
	}
	// A new field is placed on the default screen, so its screens list the tab
	// each shows it on.
	fieldScreens := values(call(adminID, http.MethodGet, "/rest/api/3/field/"+fieldID+"/screens?expand=tab", "", http.StatusOK))
	if len(fieldScreens) == 0 {
		t.Fatalf("a new field is on no screen")
	}
	for _, screen := range fieldScreens {
		screenID := fmt.Sprint(screen.(map[string]any)["id"])
		var tabs []map[string]any
		if err = json.Unmarshal([]byte(call(adminID, http.MethodGet, "/rest/api/3/screens/"+screenID+"/tabs", "", http.StatusOK)), &tabs); err != nil || len(tabs) == 0 {
			t.Fatalf("tabs = %v err=%v", tabs, err)
		}
		tab, ok := screen.(map[string]any)["tab"].(map[string]any)
		if !ok || tab["name"] == nil {
			t.Fatalf("field screen without its tab: %v", screen)
		}
	}
	if unexpanded := values(call(adminID, http.MethodGet, "/rest/api/3/field/"+fieldID+"/screens", "", http.StatusOK)); unexpanded[0].(map[string]any)["tab"] != nil {
		t.Fatalf("unexpanded field screens carry tabs: %v", unexpanded)
	}
	search := func(query string) map[string]any {
		t.Helper()
		for _, value := range values(call(adminID, http.MethodGet, "/rest/api/3/field/search?id="+fieldID+"&"+query, "", http.StatusOK)) {
			return value.(map[string]any)
		}
		t.Fatalf("field %s not found by search %s", fieldID, query)
		return nil
	}
	if unused := search("expand=key,stableId,lastUsed"); unused["key"] != fieldID || unused["stableId"] == nil || unused["lastUsed"].(map[string]any)["type"] != "NO_INFORMATION" {
		t.Fatalf("unused field = %v", unused)
	}
	if bare := search(""); bare["lastUsed"] != nil || bare["stableId"] != nil {
		t.Fatalf("unexpanded field search = %v", bare)
	}
	call(adminID, http.MethodPut, "/rest/api/3/issue/"+issue["key"].(string), `{"fields":{"`+fieldID+`":{"id":"`+canaryID+`"}}}`, http.StatusNoContent)
	if used := search("expand=lastUsed&orderBy=-lastUsed"); used["lastUsed"].(map[string]any)["type"] != "TRACKED" || used["lastUsed"].(map[string]any)["value"] == nil {
		t.Fatalf("used field = %v", used)
	}

	// Filters list the users they are shared with.
	filter := object(call(adminID, http.MethodPost, "/rest/api/3/filter", `{"name":"Crew filter `+fmt.Sprint(stamp)+`","jql":"project = `+projectKey+`","sharePermissions":[{"type":"group","groupId":"`+groupID+`"}]}`, http.StatusOK))
	shared := object(call(adminID, http.MethodGet, "/rest/api/3/filter/"+filter["id"].(string)+"?expand=sharedUsers", "", http.StatusOK))["sharedUsers"].(map[string]any)
	if shared["size"] != float64(1) || shared["items"].([]any)[0].(map[string]any)["accountId"] != memberID {
		t.Fatalf("shared users = %v", shared)
	}
	if unexpanded := object(call(adminID, http.MethodGet, "/rest/api/3/filter/"+filter["id"].(string), "", http.StatusOK))["sharedUsers"].(map[string]any); len(unexpanded["items"].([]any)) != 0 {
		t.Fatalf("unexpanded shared users = %v", unexpanded)
	}

	// Scheme holders expand to the users, groups, roles and fields they name.
	scheme := object(call(adminID, http.MethodPost, "/rest/api/3/permissionscheme", `{"name":"Crew access `+fmt.Sprint(stamp)+`","permissions":[{"permission":"BROWSE_PROJECTS","holder":{"type":"group","value":"`+groupID+`"}},{"permission":"EDIT_ISSUES","holder":{"type":"user","value":"`+memberID+`"}},{"permission":"ADD_COMMENTS","holder":{"type":"projectRole","value":"10000"}}]}`, http.StatusCreated))
	grants := object(call(adminID, http.MethodGet, fmt.Sprintf("/rest/api/3/permissionscheme/%v?expand=group,user", scheme["id"]), "", http.StatusOK))["permissions"].([]any)
	seen := map[string]bool{}
	for _, grant := range grants {
		holder := grant.(map[string]any)["holder"].(map[string]any)
		switch holder["type"] {
		case "group":
			seen["group"] = holder["group"].(map[string]any)["name"] == groupName
		case "user":
			seen["user"] = holder["user"].(map[string]any)["accountId"] == memberID
		case "projectRole":
			_, expandedRole := holder["projectRole"]
			seen["role unexpanded"] = !expandedRole && holder["expand"] == "projectRole"
		}
	}
	if !seen["group"] || !seen["user"] || !seen["role unexpanded"] {
		t.Fatalf("permission holders = %v", grants)
	}
	notification := object(call(adminID, http.MethodPost, "/rest/api/3/notificationscheme", `{"name":"Crew notices `+fmt.Sprint(stamp)+`","notificationSchemeEvents":[{"event":{"id":"1"},"notifications":[{"notificationType":"Group","parameter":"`+groupID+`"},{"notificationType":"ProjectRole","parameter":"10000"}]}]}`, http.StatusCreated))
	events := object(call(adminID, http.MethodGet, fmt.Sprintf("/rest/api/3/notificationscheme/%v?expand=all", notification["id"]), "", http.StatusOK))["notificationSchemeEvents"].([]any)
	recipients := events[0].(map[string]any)["notifications"].([]any)
	if recipients[0].(map[string]any)["group"].(map[string]any)["name"] != groupName || recipients[1].(map[string]any)["projectRole"].(map[string]any)["name"] != "Administrators" {
		t.Fatalf("notification recipients = %v", recipients)
	}
	security := object(call(adminID, http.MethodPost, "/rest/api/3/issuesecurityschemes", `{"name":"Crew only `+fmt.Sprint(stamp)+`","levels":[{"name":"Crew","members":[{"type":"group","parameter":"`+groupName+`"}]}]}`, http.StatusCreated))
	members := values(call(adminID, http.MethodGet, fmt.Sprintf("/rest/api/3/issuesecurityschemes/%v/members?expand=group", security["id"]), "", http.StatusOK))
	if len(members) != 1 || members[0].(map[string]any)["holder"].(map[string]any)["group"].(map[string]any)["name"] != groupName {
		t.Fatalf("security level members = %v", members)
	}
}
