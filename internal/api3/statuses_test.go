package api3

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestStatusAPILifecycleAndWorkspaceScope(t *testing.T) {
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
	ws, actor, member := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Status API test')`, ws)
	for _, user := range []string{actor, member} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Status user')`, user, user+"@example.test")
		role := "member"
		if user == actor {
			role = "admin"
		}
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, user, role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, user, store.HashToken(user))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM statuses WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workspaces WHERE id=$1`, ws)
		for _, user := range []string{actor, member} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.SetBasicAuth(user+"@example.test", user)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec
	}
	body := `{"scope":{"type":"GLOBAL"},"statuses":[{"name":"Peer review","description":"Review pending","statusCategory":"IN_PROGRESS"}]}`
	call(member, "POST", "/rest/api/3/statuses", body, 403)
	created := call(actor, "POST", "/rest/api/3/statuses", body, 200)
	var createdStatuses []map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &createdStatuses); err != nil || len(createdStatuses) != 1 {
		t.Fatalf("created response: %s (%v)", created.Body.String(), err)
	}
	id := createdStatuses[0]["id"].(string)
	if createdStatuses[0]["statusCategory"] != "IN_PROGRESS" || createdStatuses[0]["description"] != "Review pending" {
		t.Fatalf("created status = %#v", createdStatuses[0])
	}
	call(member, "GET", "/rest/api/3/statuses?id="+id, "", 200)
	call(member, "GET", "/rest/api/3/status/Peer%20review", "", 200)
	search := call(member, "GET", "/rest/api/3/statuses/search?searchString=peer&statusCategory=IN_PROGRESS&maxResults=1", "", 200)
	if !strings.Contains(search.Body.String(), `"total":1`) || !strings.Contains(search.Body.String(), id) {
		t.Fatal(search.Body.String())
	}
	update := `{"statuses":[{"id":"` + id + `","name":"Review complete","description":"Reviewed","statusCategory":"DONE"}]}`
	call(actor, "PUT", "/rest/api/3/statuses", update, 204)
	call(member, "GET", "/rest/api/3/statuses/byNames?name=Review%20complete", "", 200)
	call(member, "PUT", "/rest/api/3/statuses", update, 403)
	call(actor, "DELETE", "/rest/api/3/statuses?id=st_todo", "", 409)
	call(actor, "DELETE", "/rest/api/3/statuses?id="+id, "", 204)
	call(member, "GET", "/rest/api/3/status/"+id, "", 404)
	call(member, "GET", "/rest/api/3/statuscategory/4", "", 200)
}
