package confluence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestWikiUsers pins Confluence's user surface: looking people up by account id
// or email, the groups they are in, searching for them, and the arbitrary data
// an app keeps against them.
func TestWikiUsers(t *testing.T) {
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
	ws, admin, member, outsider := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	organizationID, directoryID, groupID := "", "", ""
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'User test')`, ws)
	for _, value := range []struct{ id, role, name string }{
		{admin, "admin", "Ada Admin"}, {member, "member", "Mel Member"}, {outsider, "", "Otto Outsider"},
	} {
		exec(`INSERT INTO users(id,email,password_hash,display_name,nickname) VALUES ($1,$2,'test',$3,$4)`,
			value.id, value.id+"@example.test", value.name, strings.Split(value.name, " ")[0])
		if value.role != "" {
			exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, value.id, value.role)
		}
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, value.id, store.HashToken(value.id))
	}
	// A group the member belongs to, so memberof has something true to report.
	if err := st.Pool.QueryRow(ctx, `INSERT INTO organizations(name) VALUES($1) RETURNING id::text`, "User test org "+ws).Scan(&organizationID); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `INSERT INTO directories(organization_id,name) VALUES($1::uuid,$2) RETURNING id::text`,
		organizationID, "User test directory "+ws).Scan(&directoryID); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `INSERT INTO groups(directory_id,name) VALUES($1::uuid,'wiki-editors') RETURNING id::text`, directoryID).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO group_members(group_id,user_id) VALUES($1::uuid,$2)`, groupID, member)
	t.Cleanup(func() {
		exec(`DELETE FROM wiki_user_properties WHERE user_id = ANY($1)`, []string{admin, member, outsider})
		exec(`DELETE FROM group_members WHERE group_id=$1::uuid`, groupID)
		exec(`DELETE FROM groups WHERE id=$1::uuid`, groupID)
		exec(`DELETE FROM directories WHERE id=$1::uuid`, directoryID)
		exec(`DELETE FROM organizations WHERE id=$1::uuid`, organizationID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workspaces WHERE id=$1`, ws)
		for _, id := range []string{admin, member, outsider} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	v1 := &V1Handler{Handler: h}
	send := func(handler http.Handler, prefix, user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, prefix+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, response.Code, want, response.Body.String())
		}
		return response
	}
	callV1 := func(user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		return send(v1, "/wiki/rest/api", user, method, path, body, want)
	}
	callV2 := func(user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		return send(h, "/wiki/api/v2", user, method, path, body, want)
	}
	object := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, response.Body.String())
		}
		return out
	}

	// Who am I, and who is that.
	current := object(callV1(member, "GET", "/user/current", nil, 200))
	if current["accountId"] != member || current["displayName"] != "Mel Member" {
		t.Fatalf("current user: %v", current)
	}
	if _, leaked := current["email"]; leaked {
		t.Fatalf("the user read carried an email address: %v", current)
	}
	byID := object(callV1(member, "GET", "/user?accountId="+admin, nil, 200))
	if byID["accountId"] != admin || byID["publicName"] != "Ada" {
		t.Fatalf("user by id: %v", byID)
	}
	callV1(member, "GET", "/user", nil, 400)
	callV1(member, "GET", "/user?accountId="+outsider, nil, 404)

	// Anonymous has no account, so it carries no id and no link to one.
	anonymous := object(callV1(member, "GET", "/user/anonymous", nil, 200))
	if anonymous["type"] != "anonymous" {
		t.Fatalf("anonymous: %v", anonymous)
	}
	if _, present := anonymous["accountId"]; present {
		t.Fatalf("anonymous carried an account id: %v", anonymous)
	}

	// Bulk reads skip ids that are not this workspace's people.
	bulk := object(callV1(member, "GET", "/user/bulk?accountId="+member+","+admin+","+outsider, nil, 200))
	if bulk["size"].(float64) != 2 {
		t.Fatalf("bulk: %v", bulk)
	}
	callV1(member, "GET", "/user/bulk", nil, 400)

	// An email address is administration, not part of knowing who someone is.
	callV1(member, "GET", "/user/email?accountId="+admin, nil, 403)
	callV1(member, "GET", "/user/email/bulk?accountId="+admin, nil, 403)
	email := object(callV1(admin, "GET", "/user/email?accountId="+member, nil, 200))
	if email["email"] != member+"@example.test" {
		t.Fatalf("email: %v", email)
	}
	emails := object(callV1(admin, "GET", "/user/email/bulk?accountId="+member+","+admin, nil, 200))
	if emails["size"].(float64) != 2 {
		t.Fatalf("bulk emails: %v", emails)
	}

	// The groups a person is in.
	groups := object(callV1(member, "GET", "/user/memberof?accountId="+member, nil, 200))
	groupResults, _ := groups["results"].([]any)
	if len(groupResults) != 1 {
		t.Fatalf("memberof: %v", groups)
	}
	if entry, _ := groupResults[0].(map[string]any); entry["name"] != "wiki-editors" {
		t.Fatalf("memberof: %v", groupResults[0])
	}
	if none := object(callV1(member, "GET", "/user/memberof?accountId="+admin, nil, 200)); none["size"].(float64) != 0 {
		t.Fatalf("memberof for someone in no group: %v", none)
	}

	// Searching for people, and a query naming something this does not filter
	// on being refused rather than quietly matching everyone.
	found := object(callV1(member, "GET", `/search/user?cql=user.fullname~"Mel"`, nil, 200))
	if found["size"].(float64) != 1 {
		t.Fatalf("user search: %v", found)
	}
	if empty := object(callV1(member, "GET", `/search/user?cql=user~"nobody"`, nil, 200)); empty["size"].(float64) != 0 {
		t.Fatalf("user search: %v", empty)
	}
	callV1(member, "GET", "/search/user?cql=type=page", nil, 400)
	callV1(member, "GET", "/search/user", nil, 400)

	// User properties, and who may write them.
	created := object(callV1(member, "POST", "/user/"+member+"/property/editor", map[string]any{"value": map[string]any{"theme": "dark"}}, 201))
	if created["key"] != "editor" {
		t.Fatalf("created property: %v", created)
	}
	callV1(member, "POST", "/user/"+member+"/property/editor", map[string]any{"value": map[string]any{"theme": "dark"}}, 409)
	updated := object(callV1(member, "PUT", "/user/"+member+"/property/editor", map[string]any{"value": map[string]any{"theme": "light"}}, 200))
	version, _ := updated["version"].(map[string]any)
	if version["number"].(float64) != 2 {
		t.Fatalf("updated property: %v", updated)
	}
	listed := object(callV1(member, "GET", "/user/"+member+"/property", nil, 200))
	if listed["size"].(float64) != 1 {
		t.Fatalf("property list: %v", listed)
	}
	callV1(member, "GET", "/user/"+member+"/property/missing", nil, 404)
	callV1(member, "PUT", "/user/"+member+"/property/editor", map[string]any{"key": "different", "value": map[string]any{}}, 400)
	// A person's own data is theirs; someone else's needs administration.
	callV1(member, "PUT", "/user/"+admin+"/property/editor", map[string]any{"value": map[string]any{"theme": "dark"}}, 403)
	callV1(admin, "PUT", "/user/"+member+"/property/editor", map[string]any{"value": map[string]any{"theme": "system"}}, 200)
	callV1(member, "DELETE", "/user/"+member+"/property/editor", nil, 204)
	callV1(member, "DELETE", "/user/"+member+"/property/editor", nil, 404)

	// The v2 bulk read takes its ids in the body.
	bulkV2 := object(callV2(member, "POST", "/users-bulk", map[string]any{"accountIds": []string{member, admin}}, 200))
	results, _ := bulkV2["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("v2 bulk users: %v", bulkV2)
	}
	callV2(member, "POST", "/users-bulk", map[string]any{"accountIds": []string{}}, 400)
	// Someone outside the workspace does not get to enumerate its people.
	callV2(outsider, "POST", "/users-bulk", map[string]any{"accountIds": []string{member}}, 403)
}
