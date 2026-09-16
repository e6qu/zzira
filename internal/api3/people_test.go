package api3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestPeopleContract pins people and identity as a Jira client sees them:
// users and their search, the structured user query, groups and their swap on
// delete, preferences, properties, columns, application roles and avatars.
func TestUserMigrationResolvesKeysAndUsernames(t *testing.T) {
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
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	ws, caller, named := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Migration')`, ws)
	for _, id := range []string{caller, named} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Migration person')`, id, id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, ws, id)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, id, store.HashToken(id))
	}
	exec(`UPDATE users SET username='migrated.handle' WHERE id=$1`, named)
	t.Cleanup(func() {
		exec(`DELETE FROM api_tokens WHERE user_id=ANY($1)`, []string{caller, named})
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workspaces WHERE id=$1`, ws)
		exec(`DELETE FROM users WHERE id=ANY($1)`, []string{caller, named})
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	get := func(query string, want int) []any {
		t.Helper()
		r := httptest.NewRequest("GET", "/rest/api/3/user/bulk/migration"+query, nil)
		r.SetBasicAuth(caller+"@example.test", caller)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Fatalf("GET %s: %d want %d: %s", query, rec.Code, want, rec.Body.String())
		}
		out := []any{}
		if want == 200 {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("GET %s: %v: %s", query, err, rec.Body.String())
			}
		}
		return out
	}

	get("", 400)
	// A key answers with the key asked about; an account id names itself.
	byKey := get("?key="+named, 200)
	if len(byKey) != 1 || byKey[0].(map[string]any)["accountId"] != named || byKey[0].(map[string]any)["key"] != named {
		t.Fatalf("by key = %+v", byKey)
	}
	// A username resolves the handle an identity provider supplied, and the
	// answer names the username asked about rather than a key.
	byUsername := get("?username=migrated.handle", 200)
	if len(byUsername) != 1 || byUsername[0].(map[string]any)["accountId"] != named || byUsername[0].(map[string]any)["username"] != "migrated.handle" {
		t.Fatalf("by username = %+v", byUsername)
	}
	// While username is unset, the local part of the email answers instead.
	byLocalPart := get("?username="+caller, 200)
	if len(byLocalPart) != 1 || byLocalPart[0].(map[string]any)["accountId"] != caller {
		t.Fatalf("by email local part = %+v", byLocalPart)
	}
	if nobody := get("?username=nobody.at.all", 200); len(nobody) != 0 {
		t.Fatalf("unknown handle = %+v", nobody)
	}
	// The resource pages, as the specification says it does.
	both := get("?key="+named+"&key="+caller, 200)
	if len(both) != 2 {
		t.Fatalf("two keys = %+v", both)
	}
	if page := get("?key="+named+"&key="+caller+"&maxResults=1", 200); len(page) != 1 {
		t.Fatalf("first page = %+v", page)
	}
	if page := get("?key="+named+"&key="+caller+"&startAt=1&maxResults=1", 200); len(page) != 1 || page[0].(map[string]any)["accountId"] != both[1].(map[string]any)["accountId"] {
		t.Fatalf("second page = %+v", page)
	}
	if page := get("?key="+named+"&startAt=5", 200); len(page) != 0 {
		t.Fatalf("past the end = %+v", page)
	}
}

func TestPeopleContract(t *testing.T) {
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
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	ws, other := store.NewID("ws"), store.NewID("ws")
	admin, member, outsider := store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	projectID := store.NewID("project")
	for _, id := range []string{ws, other} {
		exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,$2)`, id, "People "+id)
	}
	for _, identity := range []struct{ id, ws, role, name string }{
		{admin, ws, "admin", "Ada Admin"}, {member, ws, "member", "Mia Member"}, {outsider, other, "admin", "Oscar Outsider"},
	} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", identity.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, identity.ws, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'PPL','People','wf_default',$3)`, projectID, ws, admin)
	exec(`INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,reporter_id,assignee_id,updated_seq) VALUES($1,$2,$3,'PPL-1','Assigned','st_todo','it_task',$4,$5,0)`, store.NewID("iss"), ws, projectID, admin, member)
	t.Cleanup(func() {
		for _, id := range []string{ws, other} {
			for _, q := range []string{
				`DELETE FROM jira_user_properties WHERE workspace_id=$1`,
				`DELETE FROM jira_user_preferences WHERE workspace_id=$1`,
				`DELETE FROM jira_user_columns WHERE workspace_id=$1`,
				`DELETE FROM universal_avatars WHERE workspace_id=$1`,
				`DELETE FROM role_bindings WHERE scope_type='product' AND scope_id IN (SELECT p.id::text FROM products p JOIN sites si ON si.id=p.site_id WHERE si.workspace_id=$1)`,
				`DELETE FROM groups WHERE directory_id IN (SELECT d.id FROM directories d JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1)`,
				`DELETE FROM issues WHERE workspace_id=$1`,
				`DELETE FROM projects WHERE workspace_id=$1`,
				`DELETE FROM actions WHERE workspace_id=$1`,
				`DELETE FROM memberships WHERE workspace_id=$1`,
				`DELETE FROM workspaces WHERE id=$1`,
			} {
				exec(q, id)
			}
		}
		for _, id := range []string{admin, member, outsider} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	otherSite := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: other, BaseURL: "https://zzira.test"}

	send := func(handler *Handler, user string, request *http.Request, want int) []byte {
		t.Helper()
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", request.Method, request.URL, response.Code, want, response.Body.String())
		}
		return response.Body.Bytes()
	}
	call := func(user, method, path, body string, want int) []byte {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		return send(h, user, request, want)
	}
	decode := func(body []byte, into any) {
		t.Helper()
		if err := json.Unmarshal(body, into); err != nil {
			t.Fatalf("decode: %v %s", err, body)
		}
	}
	type user struct {
		AccountID   string `json:"accountId"`
		Email       string `json:"emailAddress"`
		DisplayName string `json:"displayName"`
		Self        string `json:"self"`
		Locale      string `json:"locale"`
		AccountType string `json:"accountType"`
		Groups      *struct {
			Size  int `json:"size"`
			Items []struct {
				Name string `json:"name"`
			} `json:"items"`
		} `json:"groups"`
		ApplicationRoles *struct {
			Items []struct {
				Key string `json:"key"`
			} `json:"items"`
		} `json:"applicationRoles"`
	}
	ids := func(users []user) []string {
		out := []string{}
		for _, u := range users {
			out = append(out, u.AccountID)
		}
		sort.Strings(out)
		return out
	}
	sorted := func(values ...string) []string {
		sort.Strings(values)
		return values
	}
	same := func(label string, got, want []string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}

	// Myself carries self, a locale and the expansions Jira offers.
	var me user
	decode(call(member, "GET", "/rest/api/3/myself?expand=groups,applicationRoles", "", 200), &me)
	if me.AccountID != member || me.Self != "https://zzira.test/rest/api/3/user?accountId="+member || me.Locale != "en_US" || me.Groups == nil || me.ApplicationRoles == nil {
		t.Fatalf("myself = %+v", me)
	}
	call(member, "PUT", "/rest/api/3/mypreferences/locale", `{"locale":"xx_XX"}`, 400)
	call(member, "PUT", "/rest/api/3/mypreferences/locale", `{"locale":"de_DE"}`, 204)
	decode(call(member, "GET", "/rest/api/3/myself", "", 200), &me)
	if me.Locale != "de_DE" {
		t.Fatalf("locale after update = %q", me.Locale)
	}
	var locale map[string]string
	decode(call(member, "GET", "/rest/api/3/mypreferences/locale", "", 200), &locale)
	if locale["locale"] != "de_DE" {
		t.Fatalf("mypreferences/locale = %v", locale)
	}

	// Preferences are plain values under a key; a missing key is 404.
	call(member, "GET", "/rest/api/3/mypreferences?key=jira.user.theme", "", 404)
	call(member, "PUT", "/rest/api/3/mypreferences?key=jira.user.theme", "dark", 204)
	var theme string
	decode(call(member, "GET", "/rest/api/3/mypreferences?key=jira.user.theme", "", 200), &theme)
	if theme != "dark" {
		t.Fatalf("preference = %q", theme)
	}
	call(member, "DELETE", "/rest/api/3/mypreferences?key=jira.user.theme", "", 204)
	call(member, "GET", "/rest/api/3/mypreferences?key=jira.user.theme", "", 404)

	// A person reads another's record without their email; they read their own with it.
	var seen user
	decode(call(member, "GET", "/rest/api/3/user?accountId="+admin, "", 200), &seen)
	if seen.Email != "" || seen.DisplayName != "Ada Admin" {
		t.Fatalf("another person's record = %+v", seen)
	}
	decode(call(admin, "GET", "/rest/api/3/user?accountId="+member, "", 200), &seen)
	if seen.Email != member+"@example.test" {
		t.Fatalf("an administrator sees email: %+v", seen)
	}
	call(member, "GET", "/rest/api/3/user?accountId="+outsider, "", 404)
	call(member, "GET", "/rest/api/3/user", "", 400)

	// Properties: 201 on create, 200 on replace, only the person or an administrator.
	userProperty := "/rest/api/3/user/properties/prefs?accountId=" + member
	call(member, "PUT", userProperty, `{"team":"core"}`, 201)
	call(member, "PUT", userProperty, `{"team":"core","level":2}`, 200)
	call(admin, "GET", userProperty, "", 200)
	call(member, "GET", "/rest/api/3/user/properties/prefs?accountId="+admin, "", 403)
	var keys struct {
		Keys []struct {
			Key string `json:"key"`
		} `json:"keys"`
	}
	decode(call(member, "GET", "/rest/api/3/user/properties?accountId="+member, "", 200), &keys)
	if len(keys.Keys) != 1 || keys.Keys[0].Key != "prefs" {
		t.Fatalf("property keys = %+v", keys)
	}
	var found []user
	decode(call(admin, "GET", "/rest/api/3/user/search?property="+url.QueryEscape("prefs.team=core"), "", 200), &found)
	same("property search", ids(found), []string{member})
	decode(call(admin, "GET", "/rest/api/3/user/search?query=mia", "", 200), &found)
	same("query search", ids(found), []string{member})
	call(admin, "GET", "/rest/api/3/user/search", "", 400)

	// Everyone in the site, and no one from another site.
	decode(call(admin, "GET", "/rest/api/3/users/search", "", 200), &found)
	same("users/search", ids(found), sorted(admin, member))
	var picked struct {
		Users []struct {
			AccountID string `json:"accountId"`
			HTML      string `json:"html"`
		} `json:"users"`
		Total int `json:"total"`
	}
	decode(call(member, "GET", "/rest/api/3/user/picker?query=Ada", "", 200), &picked)
	if picked.Total != 1 || picked.Users[0].HTML != "<strong>Ada</strong> Admin" {
		t.Fatalf("user picker = %+v", picked)
	}

	// The structured query ties people to issues, and properties, with AND and OR.
	var page struct {
		Total  int    `json:"total"`
		Values []user `json:"values"`
	}
	query := func(q string, want ...string) {
		t.Helper()
		decode(call(admin, "GET", "/rest/api/3/user/search/query?query="+url.QueryEscape(q), "", 200), &page)
		same(q, ids(page.Values), sorted(want...))
	}
	query("is assignee of PPL", member)
	query("is reporter of (PPL-1)", admin)
	query("is assignee of PPL OR is reporter of PPL", admin, member)
	query("is assignee of PPL AND is reporter of PPL")
	query(`[prefs].team is "core" AND is assignee of PPL`, member)
	call(admin, "GET", "/rest/api/3/user/search/query?query="+url.QueryEscape("is owner of PPL"), "", 400)
	call(admin, "GET", "/rest/api/3/user/search/query?query="+url.QueryEscape("is assignee of NOPE"), "", 400)
	var keyed struct {
		Values []map[string]string `json:"values"`
	}
	decode(call(admin, "GET", "/rest/api/3/user/search/query/key?query="+url.QueryEscape("is assignee of PPL"), "", 200), &keyed)
	if len(keyed.Values) != 1 || keyed.Values[0]["accountId"] != member {
		t.Fatalf("query/key = %+v", keyed)
	}

	// Assignable and browse searches answer for a project.
	decode(call(admin, "GET", "/rest/api/3/user/assignable/search?project=PPL&query=ada", "", 200), &found)
	same("assignable", ids(found), []string{admin})
	call(admin, "GET", "/rest/api/3/user/assignable/search", "", 400)
	decode(call(admin, "GET", "/rest/api/3/user/assignable/multiProjectSearch?projectKeys=PPL&query=ada", "", 200), &found)
	same("multi-project assignable", ids(found), []string{admin})
	decode(call(admin, "GET", "/rest/api/3/user/viewissue/search?issueKey=PPL-1&query=ada", "", 200), &found)
	same("browse", ids(found), []string{admin})

	// Groups: only administrators create them, and a group on one site is not on another.
	call(member, "POST", "/rest/api/3/group", `{"name":"jira-developers"}`, 403)
	var group struct {
		GroupID string `json:"groupId"`
		Name    string `json:"name"`
	}
	decode(call(admin, "POST", "/rest/api/3/group", `{"name":"jira-developers"}`, 201), &group)
	developers := group.GroupID
	decode(call(admin, "POST", "/rest/api/3/group", `{"name":"jira-operators"}`, 201), &group)
	operators := group.GroupID
	send(otherSite, outsider, httptest.NewRequest("GET", "/rest/api/3/group?groupId="+developers, nil), 404)
	call(admin, "POST", "/rest/api/3/group/user?groupId="+developers, `{"accountId":"`+member+`"}`, 201)
	call(admin, "POST", "/rest/api/3/group/user?groupId="+developers, `{"accountId":"`+outsider+`"}`, 404)
	decode(call(admin, "GET", "/rest/api/3/group/member?groupname=jira-developers", "", 200), &page)
	same("group members", ids(page.Values), []string{member})
	var memberGroups []struct {
		Name string `json:"name"`
	}
	decode(call(member, "GET", "/rest/api/3/user/groups?accountId="+member, "", 200), &memberGroups)
	if len(memberGroups) != 1 || memberGroups[0].Name != "jira-developers" {
		t.Fatalf("user groups = %+v", memberGroups)
	}
	var groupPicker struct {
		Total  int `json:"total"`
		Groups []struct {
			Name string `json:"name"`
		} `json:"groups"`
	}
	decode(call(member, "GET", "/rest/api/3/groups/picker?query=operat", "", 200), &groupPicker)
	if groupPicker.Total != 1 || groupPicker.Groups[0].Name != "jira-operators" {
		t.Fatalf("group picker = %+v", groupPicker)
	}
	var both struct {
		Users  struct{ Total int } `json:"users"`
		Groups struct{ Total int } `json:"groups"`
	}
	decode(call(member, "GET", "/rest/api/3/groupuserpicker?query=jira", "", 200), &both)
	if both.Groups.Total != 2 {
		t.Fatalf("groupuserpicker = %+v", both)
	}
	decode(call(admin, "GET", "/rest/api/3/group/bulk?groupName=jira-operators", "", 200), &page)
	if page.Total != 1 {
		t.Fatalf("group bulk total = %d", page.Total)
	}

	// Application roles count access granted through a group, for administrators only.
	exec(`INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source)
		SELECT 'product',p.id::text,'atlassian/user','group',$2,'manual' FROM products p JOIN sites si ON si.id=p.site_id
		WHERE si.workspace_id=$1 AND p.product_key='jira-software'`, ws, developers)
	call(member, "GET", "/rest/api/3/applicationrole", "", 403)
	var role struct {
		Key       string   `json:"key"`
		Groups    []string `json:"groups"`
		UserCount int      `json:"userCount"`
	}
	decode(call(admin, "GET", "/rest/api/3/applicationrole/jira-software", "", 200), &role)
	if strings.Join(role.Groups, ",") != "jira-developers" || role.UserCount < 1 {
		t.Fatalf("application role = %+v", role)
	}
	call(admin, "GET", "/rest/api/3/applicationrole/jira-nope", "", 404)
	decode(call(member, "GET", "/rest/api/3/myself?expand=applicationRoles,groups", "", 200), &me)
	if me.ApplicationRoles == nil || len(me.ApplicationRoles.Items) == 0 || me.ApplicationRoles.Items[0].Key != "jira-software" {
		t.Fatalf("myself application roles = %+v", me.ApplicationRoles)
	}

	// Deleting a group with a swap moves its access and members to the swap group.
	call(admin, "DELETE", "/rest/api/3/group?groupId="+developers+"&swapGroupId="+developers, "", 400)
	call(member, "DELETE", "/rest/api/3/group?groupId="+developers+"&swapGroupId="+operators, "", 403)
	call(admin, "DELETE", "/rest/api/3/group?groupId="+developers+"&swapGroupId="+operators, "", 200)
	call(admin, "GET", "/rest/api/3/group?groupId="+developers, "", 404)
	decode(call(admin, "GET", "/rest/api/3/applicationrole/jira-software", "", 200), &role)
	if strings.Join(role.Groups, ",") != "jira-operators" {
		t.Fatalf("application role after swap = %+v", role)
	}
	decode(call(admin, "GET", "/rest/api/3/group/member?groupId="+operators, "", 200), &page)
	same("swap group members", ids(page.Values), []string{member})
	call(admin, "DELETE", "/rest/api/3/group/user?groupId="+operators+"&accountId="+member, "", 200)
	decode(call(admin, "GET", "/rest/api/3/group/member?groupId="+operators, "", 200), &page)
	same("members after removal", ids(page.Values), []string{})

	// Columns are sent as form data and fall back to the site's when reset.
	request := httptest.NewRequest("PUT", "/rest/api/3/user/columns", strings.NewReader("columns=summary&columns=status"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	send(h, member, request, 200)
	var columns []map[string]string
	decode(call(member, "GET", "/rest/api/3/user/columns", "", 200), &columns)
	if len(columns) != 2 || columns[0]["value"] != "summary" || columns[1]["label"] != "Status" {
		t.Fatalf("columns = %+v", columns)
	}
	call(member, "GET", "/rest/api/3/user/columns?accountId="+admin, "", 403)
	call(member, "DELETE", "/rest/api/3/user/columns", "", 204)
	decode(call(member, "GET", "/rest/api/3/user/columns", "", 200), &columns)
	if len(columns) == 2 && columns[0]["value"] == "summary" {
		t.Fatal("columns were not reset")
	}

	// Email lookups are for apps only.
	call(admin, "GET", "/rest/api/3/user/email?accountId="+member, "", 400)

	// Avatars: the system set, an uploaded one selected for a project, served back.
	var system struct {
		System []struct {
			ID       string `json:"id"`
			IsSystem bool   `json:"isSystemAvatar"`
		} `json:"system"`
	}
	decode(call(member, "GET", "/rest/api/3/avatar/project/system", "", 200), &system)
	if len(system.System) == 0 || !system.System[0].IsSystem {
		t.Fatalf("system avatars = %+v", system)
	}
	call(member, "GET", "/rest/api/3/avatar/nope/system", "", 404)
	png := "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82"
	upload := func(user, path string, token bool, want int) []byte {
		t.Helper()
		request := httptest.NewRequest("POST", path, strings.NewReader(png))
		request.Header.Set("Content-Type", "image/png")
		if token {
			request.Header.Set("X-Atlassian-Token", "no-check")
		}
		return send(h, user, request, want)
	}
	upload(admin, "/rest/api/3/project/PPL/avatar2", false, 403)
	upload(member, "/rest/api/3/project/PPL/avatar2", true, 403)
	var avatar struct {
		ID          string `json:"id"`
		Owner       string `json:"owner"`
		IsDeletable bool   `json:"isDeletable"`
	}
	decode(upload(admin, "/rest/api/3/project/PPL/avatar2", true, 201), &avatar)
	if avatar.ID == "" || !avatar.IsDeletable {
		t.Fatalf("uploaded avatar = %+v", avatar)
	}
	call(admin, "PUT", "/rest/api/3/project/PPL/avatar", `{"id":"`+avatar.ID+`"}`, 204)
	var owned struct {
		System []map[string]any `json:"system"`
		Custom []struct {
			ID         string `json:"id"`
			IsSelected bool   `json:"isSelected"`
		} `json:"custom"`
	}
	decode(call(member, "GET", "/rest/api/3/project/PPL/avatars", "", 200), &owned)
	if len(owned.Custom) != 1 || !owned.Custom[0].IsSelected || owned.Custom[0].ID != avatar.ID {
		t.Fatalf("project avatars = %+v", owned)
	}
	request = httptest.NewRequest("GET", "/rest/api/3/universal_avatar/view/type/project/owner/"+projectID, nil)
	request.SetBasicAuth(member+"@example.test", member)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != 200 || response.Header().Get("Content-Type") != "image/png" || response.Body.String() != png {
		t.Fatalf("selected avatar view = %d %s", response.Code, response.Header().Get("Content-Type"))
	}
	call(member, "GET", "/rest/api/3/universal_avatar/view/type/project/avatar/"+system.System[0].ID+"?size=gigantic", "", 404)
	call(admin, "DELETE", "/rest/api/3/universal_avatar/type/project/owner/"+projectID+"/avatar/"+system.System[0].ID, "", 403)
	call(admin, "DELETE", "/rest/api/3/project/PPL/avatar/"+avatar.ID, "", 204)
	decode(call(member, "GET", "/rest/api/3/universal_avatar/type/project/owner/"+projectID, "", 200), &owned)
	if len(owned.Custom) != 0 {
		t.Fatalf("custom avatars after delete = %+v", owned.Custom)
	}

	// Removing a person takes them off the site.
	call(member, "DELETE", "/rest/api/3/user?accountId="+admin, "", 403)
	call(admin, "DELETE", "/rest/api/3/user?accountId="+member, "", 204)
	call(admin, "GET", "/rest/api/3/user?accountId="+member, "", 404)
}
