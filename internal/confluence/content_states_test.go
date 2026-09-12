package confluence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestContentStates pins Confluence's content state surface: the label a page
// carries beyond its text, chosen from what the space suggests or made by the
// writer as they work.
func TestContentStates(t *testing.T) {
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
	ws, actor, other := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Content state test')`, ws)
	for _, id := range []string{actor, other} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','State user')`, id, id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin')`, ws, id)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, id, store.HashToken(id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM wiki_content_states WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, id := range []string{actor, other} {
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
	send := func(handler interface {
		ServeHTTP(w http.ResponseWriter, r *http.Request)
	}, prefix, user, method, path string, body any, want int) *httptest.ResponseRecorder {
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
	array := func(response *httptest.ResponseRecorder) []any {
		t.Helper()
		var out []any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, response.Body.String())
		}
		return out
	}
	space := object(callV2(actor, "POST", "/spaces", map[string]any{"key": "CST", "name": "Content states"}, 201))
	spaceID, _ := space["id"].(string)
	page := object(callV2(actor, "POST", "/pages", map[string]any{"spaceId": spaceID, "title": "Runbook",
		"status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>steps</p>"}}, 200))
	pageID, _ := page["id"].(string)

	// The space suggests a set of states, and reports what it allows.
	suggested := array(callV1(actor, "GET", "/space/CST/state", nil, 200))
	if len(suggested) == 0 {
		t.Fatalf("space content states: %v", suggested)
	}
	firstSuggested, _ := suggested[0].(map[string]any)
	suggestedID := strconv.FormatInt(int64(firstSuggested["id"].(float64)), 10)
	settings := object(callV1(actor, "GET", "/space/CST/state/settings", nil, 200))
	for _, key := range []string{"contentStatesAllowed", "customContentStatesAllowed", "spaceContentStatesAllowed"} {
		if settings[key] != true {
			t.Fatalf("%s: %v", key, settings)
		}
	}
	if states, _ := settings["spaceContentStates"].([]any); len(states) != len(suggested) {
		t.Fatalf("settings states disagree with the space's: %v", settings)
	}
	callV1(actor, "GET", "/space/NOPE/state", nil, 404)

	// A page starts with no state.
	initial := object(callV1(actor, "GET", "/content/"+pageID+"/state", nil, 200))
	if initial["contentState"] != nil {
		t.Fatalf("a new page already had a state: %v", initial)
	}

	// Setting one from the space's suggestions publishes a new version.
	set := object(callV1(actor, "PUT", "/content/"+pageID+"/state?status=current",
		map[string]any{"id": firstSuggested["id"]}, 200))
	state, _ := set["contentState"].(map[string]any)
	if state["name"] != firstSuggested["name"] {
		t.Fatalf("set state: %v", set)
	}
	read := object(callV1(actor, "GET", "/content/"+pageID+"/state", nil, 200))
	if readState, _ := read["contentState"].(map[string]any); readState["name"] != firstSuggested["name"] {
		t.Fatalf("state did not persist: %v", read)
	}

	// Describing a state creates one belonging to the writer.
	custom := object(callV1(actor, "PUT", "/content/"+pageID+"/state?status=current",
		map[string]any{"name": "Needs legal", "color": "#FF5630"}, 200))
	customState, _ := custom["contentState"].(map[string]any)
	customID := strconv.FormatInt(int64(customState["id"].(float64)), 10)
	if customState["name"] != "Needs legal" || customState["color"] != "#FF5630" {
		t.Fatalf("custom state: %v", custom)
	}
	mine := array(callV1(actor, "GET", "/content-states", nil, 200))
	if len(mine) != 1 {
		t.Fatalf("my custom states: %v", mine)
	}
	// A custom state belongs to the writer who made it.
	if theirs := array(callV1(other, "GET", "/content-states", nil, 200)); len(theirs) != 0 {
		t.Fatalf("another writer saw someone else's custom states: %v", theirs)
	}
	// Re-using a name keeps one state rather than accumulating a new one.
	callV1(actor, "PUT", "/content/"+pageID+"/state?status=current",
		map[string]any{"name": "Needs legal", "color": "#FF5630"}, 200)
	if again := array(callV1(actor, "GET", "/content-states", nil, 200)); len(again) != 1 {
		t.Fatalf("re-using a name made a second state: %v", again)
	}

	// What the page can be set to: the space's states, and the writer's recent ones.
	available := object(callV1(actor, "GET", "/content/"+pageID+"/state/available", nil, 200))
	spaceStates, _ := available["spaceContentStates"].([]any)
	customStates, _ := available["customContentStates"].([]any)
	if len(spaceStates) != len(suggested) || len(customStates) != 1 {
		t.Fatalf("available states: %v", available)
	}

	// Every refusal.
	callV1(actor, "PUT", "/content/"+pageID+"/state", map[string]any{"id": firstSuggested["id"]}, 400)
	callV1(actor, "PUT", "/content/"+pageID+"/state?status=sideways", map[string]any{"id": firstSuggested["id"]}, 400)
	callV1(actor, "PUT", "/content/"+pageID+"/state?status=current",
		map[string]any{"id": firstSuggested["id"], "name": "Both", "color": "#FF5630"}, 400)
	callV1(actor, "PUT", "/content/"+pageID+"/state?status=current", map[string]any{}, 400)
	callV1(actor, "PUT", "/content/"+pageID+"/state?status=current",
		map[string]any{"name": "Not a colour", "color": "red"}, 400)
	callV1(actor, "PUT", "/content/"+pageID+"/state?status=current",
		map[string]any{"name": "This name is far too long to be a state", "color": "#FF5630"}, 400)
	callV1(actor, "PUT", "/content/"+pageID+"/state?status=current", map[string]any{"id": 999999}, 404)
	callV1(actor, "GET", "/content/9999999/state", nil, 404)

	// The space reports the content in a state, and only that content.
	inCustom := object(callV1(actor, "GET", "/space/CST/state/content?state-id="+customID, nil, 200))
	results, _ := inCustom["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("content in the custom state: %v", inCustom)
	}
	if entry, _ := results[0].(map[string]any); entry["id"] != pageID || entry["title"] != "Runbook" {
		t.Fatalf("content in the custom state: %v", results[0])
	}
	if inSuggested := object(callV1(actor, "GET", "/space/CST/state/content?state-id="+suggestedID, nil, 200)); len(inSuggested["results"].([]any)) != 0 {
		t.Fatalf("the page is still reported in the state it left: %v", inSuggested)
	}
	callV1(actor, "GET", "/space/CST/state/content", nil, 400)
	callV1(actor, "GET", "/space/CST/state/content?state-id="+customID+"&limit=0", nil, 400)

	// Each change is a version, so the history shows when the state moved.
	versions := object(callV2(actor, "GET", "/pages/"+pageID+"/versions", nil, 200))
	versionResults, _ := versions["results"].([]any)
	if len(versionResults) < 4 {
		t.Fatalf("state changes did not publish versions: %v", versions)
	}
	body := object(callV2(actor, "GET", "/pages/"+pageID+"?body-format=storage", nil, 200))
	pageBody, _ := body["body"].(map[string]any)
	storage, _ := pageBody["storage"].(map[string]any)
	if storage["value"] != "<p>steps</p>" {
		t.Fatalf("setting a state changed the body: %v", body)
	}

	// Removing the state is also a version, so the history shows that too.
	cleared := object(callV1(actor, "DELETE", "/content/"+pageID+"/state?status=current", nil, 200))
	if cleared["contentState"] != nil {
		t.Fatalf("clear: %v", cleared)
	}
	if after := object(callV1(actor, "GET", "/content/"+pageID+"/state", nil, 200)); after["contentState"] != nil {
		t.Fatalf("the state came back: %v", after)
	}
	if gone := object(callV1(actor, "GET", "/space/CST/state/content?state-id="+customID, nil, 200)); len(gone["results"].([]any)) != 0 {
		t.Fatalf("content still reported in a state it no longer has: %v", gone)
	}
}
