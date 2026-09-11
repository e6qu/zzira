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

// TestWikiGroups pins Confluence's group surface: listing, creating, reading,
// searching and deleting groups, and moving people in and out of them.
func TestWikiGroups(t *testing.T) {
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
	groupName := "wiki-editors-" + strings.ToLower(store.NewID("g"))
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Group test')`, ws)
	for _, value := range []struct{ id, role string }{{admin, "admin"}, {member, "member"}, {outsider, ""}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Group user')`, value.id, value.id+"@example.test")
		if value.role != "" {
			exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, value.id, value.role)
		}
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, value.id, store.HashToken(value.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM group_members WHERE group_id IN (SELECT id FROM groups WHERE name LIKE $1)`, groupName+"%")
		exec(`DELETE FROM groups WHERE name LIKE $1`, groupName+"%")
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
	call := func(user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, "/wiki/rest/api"+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		var handler http.Handler = v1
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, response.Code, want, response.Body.String())
		}
		return response
	}
	object := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, response.Body.String())
		}
		return out
	}

	// Creating a group is administration.
	call(member, "POST", "/group", map[string]any{"name": groupName}, 403)
	created := object(call(admin, "POST", "/group", map[string]any{"name": groupName}, 201))
	groupID, _ := created["id"].(string)
	if groupID == "" || created["name"] != groupName {
		t.Fatalf("created group: %v", created)
	}
	call(admin, "POST", "/group", map[string]any{"name": groupName}, 400)
	call(admin, "POST", "/group", map[string]any{"name": "   "}, 400)

	// Reading is open to any member; someone outside the workspace is refused.
	read := object(call(member, "GET", "/group/by-id?id="+groupID, nil, 200))
	if read["name"] != groupName {
		t.Fatalf("group by id: %v", read)
	}
	call(member, "GET", "/group/by-id", nil, 400)
	call(member, "GET", "/group/by-id?id=00000000-0000-0000-0000-000000000000", nil, 404)
	call(outsider, "GET", "/group", nil, 403)

	listed := object(call(member, "GET", "/group", nil, 200))
	results, _ := listed["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("group list: %v", listed)
	}
	// The list does not count unless asked; the picker does when asked.
	if _, counted := listed["totalSize"]; counted {
		t.Fatalf("the list counted without being asked: %v", listed)
	}
	picked := object(call(member, "GET", "/group/picker?query="+groupName+"&shouldReturnTotalSize=true", nil, 200))
	if picked["size"].(float64) != 1 || picked["totalSize"].(float64) != 1 {
		t.Fatalf("group picker: %v", picked)
	}
	if quiet := object(call(member, "GET", "/group/picker?query="+groupName, nil, 200)); quiet["totalSize"] != nil {
		t.Fatalf("the picker counted without being asked: %v", quiet)
	}
	if none := object(call(member, "GET", "/group/picker?query=nothingmatchesthis", nil, 200)); none["size"].(float64) != 0 {
		t.Fatalf("group picker: %v", none)
	}
	call(member, "GET", "/group/picker", nil, 400)

	// Membership, and the user surface agreeing about it.
	if empty := object(call(member, "GET", "/group/"+groupID+"/membersByGroupId", nil, 200)); empty["size"].(float64) != 0 {
		t.Fatalf("a new group had members: %v", empty)
	}
	call(member, "POST", "/group/userByGroupId?groupId="+groupID, map[string]any{"accountId": member}, 403)
	call(admin, "POST", "/group/userByGroupId?groupId="+groupID, map[string]any{"accountId": member}, 201)
	members := object(call(member, "GET", "/group/"+groupID+"/membersByGroupId", nil, 200))
	memberResults, _ := members["results"].([]any)
	if len(memberResults) != 1 {
		t.Fatalf("group members: %v", members)
	}
	if entry, _ := memberResults[0].(map[string]any); entry["accountId"] != member {
		t.Fatalf("group members: %v", memberResults[0])
	}
	// The two surfaces describe one membership, not two.
	memberOf := object(call(member, "GET", "/user/memberof?accountId="+member, nil, 200))
	groupsOfUser, _ := memberOf["results"].([]any)
	if len(groupsOfUser) != 1 {
		t.Fatalf("memberof disagrees with the group's members: %v", memberOf)
	}
	if entry, _ := groupsOfUser[0].(map[string]any); entry["id"] != groupID {
		t.Fatalf("memberof named a different group: %v", groupsOfUser[0])
	}
	// Adding twice is not an error; the person is already in the group.
	call(admin, "POST", "/group/userByGroupId?groupId="+groupID, map[string]any{"accountId": member}, 201)
	call(admin, "POST", "/group/userByGroupId?groupId="+groupID, map[string]any{"accountId": outsider}, 404)
	call(admin, "POST", "/group/userByGroupId?groupId="+groupID, map[string]any{}, 400)
	call(admin, "POST", "/group/userByGroupId", map[string]any{"accountId": member}, 400)

	call(member, "DELETE", "/group/userByGroupId?groupId="+groupID+"&accountId="+member, nil, 403)
	call(admin, "DELETE", "/group/userByGroupId?groupId="+groupID+"&accountId="+member, nil, 204)
	call(admin, "DELETE", "/group/userByGroupId?groupId="+groupID+"&accountId="+member, nil, 404)
	if gone := object(call(member, "GET", "/user/memberof?accountId="+member, nil, 200)); gone["size"].(float64) != 0 {
		t.Fatalf("the person is still reported in the group: %v", gone)
	}

	// Deleting is administration, and takes the group with its memberships.
	call(admin, "POST", "/group/userByGroupId?groupId="+groupID, map[string]any{"accountId": admin}, 201)
	call(member, "DELETE", "/group/by-id?id="+groupID, nil, 403)
	call(admin, "DELETE", "/group/by-id?id="+groupID, nil, 204)
	call(admin, "DELETE", "/group/by-id?id="+groupID, nil, 404)
	var remaining int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM group_members WHERE group_id::text=$1`, groupID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("memberships outlived the group: %d", remaining)
	}
}
