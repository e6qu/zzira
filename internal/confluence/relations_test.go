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
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestContentRelations pins Confluence's relation surface: a named, one-way
// link between two entities. 'favourite' is the one the product names itself;
// a client may name any other and have it kept.
func TestContentRelations(t *testing.T) {
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
	ws, admin, member := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Relation test')`, ws)
	for _, person := range []struct{ id, role string }{{admin, "admin"}, {member, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Relation user')`, person.id, person.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, person.id, person.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, person.id, store.HashToken(person.id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_relations WHERE workspace_id=$1`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, id := range []string{admin, member} {
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
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	callV1 := func(user, method, path string, want int) *httptest.ResponseRecorder {
		t.Helper()
		return send(v1, "/wiki/rest/api", user, method, path, nil, want)
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
	// keysOf reports which relations a listing returned, by the far end each
	// one points at. A listing is judged by what a reader sees, not by its
	// status code.
	keysOf := func(response *httptest.ResponseRecorder, side string) []string {
		t.Helper()
		body := object(response)
		results, _ := body["results"].([]any)
		out := []string{}
		for _, raw := range results {
			relation, _ := raw.(map[string]any)
			entity, _ := relation[side].(map[string]any)
			if entity == nil {
				t.Fatalf("%s was not expanded: %s", side, response.Body.String())
			}
			key, _ := entity["id"].(string)
			if key == "" {
				key, _ = entity["key"].(string)
			}
			if key == "" {
				key, _ = entity["accountId"].(string)
			}
			out = append(out, key)
		}
		return out
	}

	space := object(callV2(admin, "POST", "/spaces", map[string]any{"key": "REL", "name": "Relations"}, 201))
	spaceID, _ := space["id"].(string)
	newPage := func(title string) string {
		t.Helper()
		page := object(callV2(admin, "POST", "/pages", map[string]any{"spaceId": spaceID, "title": title,
			"status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>" + title + "</p>"}}, 200))
		id, _ := page["id"].(string)
		return id
	}
	runbook, sibling, cousin := newPage("Runbook"), newPage("Sibling"), newPage("Cousin")

	// A favourite is something a person saves for later.
	favourite := "/relation/favourite/from/user/current/to/content/" + runbook
	callV1(admin, "GET", favourite, 404)
	created := object(callV1(admin, "PUT", favourite, 200))
	if created["name"] != "favourite" {
		t.Fatalf("created relation: %v", created)
	}
	// Creating takes no expand parameter, so the parts are named as expandable.
	expandable, _ := created["_expandable"].(map[string]any)
	for _, part := range []string{"relationData", "source", "target"} {
		if _, ok := expandable[part]; !ok {
			t.Fatalf("%s should be expandable: %v", part, created)
		}
	}
	read := object(callV1(admin, "GET", favourite+"?expand=relationData,source,target", 200))
	source, _ := read["source"].(map[string]any)
	target, _ := read["target"].(map[string]any)
	if source["accountId"] != admin || target["id"] != runbook {
		t.Fatalf("relation ends: %s", read)
	}
	data, _ := read["relationData"].(map[string]any)
	createdBy, _ := data["createdBy"].(map[string]any)
	if createdBy["accountId"] != admin || data["createdDate"] == nil {
		t.Fatalf("relationData: %s", read)
	}
	// Naming a relation that is already there asks for the same state, and
	// leaves it in that state rather than failing.
	callV1(admin, "PUT", favourite, 200)
	// The favourite belongs to whoever saved it; another reader has not saved it.
	callV1(member, "GET", "/relation/favourite/from/user/current/to/content/"+runbook, 404)
	// A favourite is a person's own act, so it cannot be made for someone else
	// by a reader who does not administer the site.
	callV1(member, "PUT", "/relation/favourite/from/user/"+admin+"/to/content/"+runbook, 403)
	// A favourite runs from a person to a space or to content, never the other way.
	callV1(admin, "PUT", "/relation/favourite/from/content/"+runbook+"/to/user/"+admin, 400)
	callV1(admin, "PUT", "/relation/favourite/from/user/current/to/user/"+member, 400)
	// A space can be saved for later too.
	callV1(admin, "PUT", "/relation/favourite/from/user/current/to/space/REL", 200)
	savedSpace := object(callV1(admin, "GET", "/relation/favourite/from/user/current/to/space/REL?expand=target", 200))
	savedTarget, _ := savedSpace["target"].(map[string]any)
	if savedTarget["key"] != "REL" {
		t.Fatalf("saved space: %s", savedSpace)
	}

	// Any other name makes a relation of that name between two entities.
	callV1(admin, "PUT", "/relation/sibling/from/content/"+runbook+"/to/content/"+sibling, 200)
	callV1(admin, "PUT", "/relation/sibling/from/content/"+runbook+"/to/content/"+cousin, 200)
	siblings := keysOf(callV1(admin, "GET", "/relation/sibling/from/content/"+runbook+"/to/content?expand=target", 200), "target")
	if len(siblings) != 2 || siblings[0] != sibling || siblings[1] != cousin {
		t.Fatalf("siblings of the runbook: %v", siblings)
	}
	// Relations are one way: the runbook names its siblings, and they do not
	// name it back until somebody says so.
	if back := keysOf(callV1(admin, "GET", "/relation/sibling/from/content/"+sibling+"/to/content?expand=target", 200), "target"); len(back) != 0 {
		t.Fatalf("a sibling should not point back: %v", back)
	}
	// Read from the far end, the same relation is found by its target.
	if sources := keysOf(callV1(admin, "GET", "/relation/sibling/to/content/"+sibling+"/from/content?expand=source", 200), "source"); len(sources) != 1 || sources[0] != runbook {
		t.Fatalf("sources for the sibling: %v", sources)
	}
	// A listing of one type sees only relations to that type.
	if users := keysOf(callV1(admin, "GET", "/relation/sibling/from/content/"+runbook+"/to/user?expand=target", 200), "target"); len(users) != 0 {
		t.Fatalf("siblings are content, not people: %v", users)
	}
	// Paging walks the listing rather than repeating it.
	if first := keysOf(callV1(admin, "GET", "/relation/sibling/from/content/"+runbook+"/to/content?expand=target&limit=1", 200), "target"); len(first) != 1 || first[0] != sibling {
		t.Fatalf("first page: %v", first)
	}
	if second := keysOf(callV1(admin, "GET", "/relation/sibling/from/content/"+runbook+"/to/content?expand=target&limit=1&start=1", 200), "target"); len(second) != 1 || second[0] != cousin {
		t.Fatalf("second page: %v", second)
	}
	// Favourites are read through their own endpoints, so the listings say so
	// rather than answering with an empty page.
	callV1(admin, "GET", "/relation/favourite/from/user/current/to/content", 400)
	callV1(admin, "GET", "/relation/like/to/content/"+runbook+"/from/user", 400)

	// An entity is a user, a space or content, and it has to exist.
	callV1(admin, "PUT", "/relation/sibling/from/page/"+runbook+"/to/content/"+sibling, 400)
	callV1(admin, "PUT", "/relation/sibling/from/content/"+runbook+"/to/content/9999999", 404)
	callV1(admin, "GET", "/relation/sibling/from/content/9999999/to/content", 404)
	// Only content carries a status and a version.
	callV1(admin, "PUT", "/relation/sibling/from/user/current/to/content/"+sibling+"?sourceStatus=draft", 400)
	callV1(admin, "GET", favourite+"?expand=nonsense", 400)

	// A relation to a historical revision names that revision, and is not the
	// same relation as one to the content as it stands.
	callV2(admin, "PUT", "/pages/"+runbook, map[string]any{"id": runbook, "status": "current", "title": "Runbook",
		"body": models.WikiBody{Representation: "storage", Value: "<p>revised</p>"}, "version": map[string]any{"number": 2}}, 200)
	historical := "/relation/sibling/from/content/" + runbook + "/to/content/" + sibling + "?targetStatus=historical&targetVersion=1"
	callV1(admin, "GET", historical, 404)
	callV1(admin, "PUT", historical, 200)
	callV1(admin, "GET", historical, 200)
	// The relation to the current content is untouched by the historical one.
	callV1(admin, "GET", "/relation/sibling/from/content/"+runbook+"/to/content/"+sibling, 200)
	// A version names a historical revision, so it does not go with a current one.
	callV1(admin, "PUT", "/relation/sibling/from/content/"+runbook+"/to/content/"+sibling+"?targetVersion=1", 400)

	// Removing a relation removes it, and removing it again leaves it removed.
	callV1(admin, "DELETE", "/relation/sibling/from/content/"+runbook+"/to/content/"+cousin, 204)
	callV1(admin, "DELETE", "/relation/sibling/from/content/"+runbook+"/to/content/"+cousin, 204)
	callV1(admin, "GET", "/relation/sibling/from/content/"+runbook+"/to/content/"+cousin, 404)
	if remaining := keysOf(callV1(admin, "GET", "/relation/sibling/from/content/"+runbook+"/to/content?expand=target", 200), "target"); len(remaining) != 1 || remaining[0] != sibling {
		t.Fatalf("after removing the cousin: %v", remaining)
	}
	// The listing of the content as it stands does not reach the relation to a
	// past revision of it; asking for that revision does.
	if past := keysOf(callV1(admin, "GET", "/relation/sibling/from/content/"+runbook+"/to/content?expand=target&targetStatus=historical&targetVersion=1", 200), "target"); len(past) != 1 || past[0] != sibling {
		t.Fatalf("relations to a past revision: %v", past)
	}
	// Removing somebody else's favourite is theirs to do, not a reader's.
	callV1(member, "DELETE", "/relation/favourite/from/user/"+admin+"/to/content/"+runbook, 403)
	callV1(admin, "DELETE", favourite, 204)
	callV1(admin, "GET", favourite, 404)
}
