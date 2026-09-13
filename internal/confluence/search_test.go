package confluence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestContentSearch pins Confluence's CQL search: the query language a client
// writes, the results it describes, and the content it returns.
func TestContentSearch(t *testing.T) {
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
	ws, author, reader := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Search test')`, ws)
	// The reader is an ordinary member, because an administrator sees past a
	// page restriction and would not show whether the search respects one.
	for _, person := range []struct{ id, name, role string }{{author, "Ada Writer", "admin"}, {reader, "Ben Reader", "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$3)`, person.id, person.id+"@example.test", person.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, person.id, person.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, person.id, store.HashToken(person.id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_relations WHERE workspace_id=$1`,
			`DELETE FROM wiki_watches WHERE workspace_id=$1`,
			`DELETE FROM wiki_page_labels WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_labels WHERE workspace_id=$1`,
			`DELETE FROM wiki_footer_comment_versions WHERE comment_id IN (SELECT c.id FROM wiki_footer_comments c JOIN wiki_pages p ON p.id=c.page_id JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_footer_comments WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_post_versions WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`UPDATE wiki_pages SET parent_id=NULL WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`UPDATE wiki_spaces SET homepage_id=NULL WHERE workspace_id=$1`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, id := range []string{author, reader} {
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
	// search runs a query and reports the titles a reader gets back, in order.
	// A search is judged by what it finds, never by its status code.
	search := func(user, endpoint, cql, extra string, want int) (map[string]any, []string) {
		t.Helper()
		path := endpoint + "?cql=" + url.QueryEscape(cql)
		if extra != "" {
			path += "&" + extra
		}
		response := send(v1, "/wiki/rest/api", user, "GET", path, nil, want)
		if want != 200 {
			return nil, nil
		}
		body := object(response)
		results, _ := body["results"].([]any)
		titles := []string{}
		for _, raw := range results {
			result, _ := raw.(map[string]any)
			title, _ := result["title"].(string)
			titles = append(titles, title)
		}
		return body, titles
	}
	has := func(titles []string, want ...string) bool {
		if len(titles) != len(want) {
			return false
		}
		seen := map[string]bool{}
		for _, title := range titles {
			seen[title] = true
		}
		for _, title := range want {
			if !seen[title] {
				return false
			}
		}
		return true
	}

	space := object(callV2(author, "POST", "/spaces", map[string]any{"key": "SEARCH", "name": "Search space"}, 201))
	spaceID, _ := space["id"].(string)
	other := object(callV2(author, "POST", "/spaces", map[string]any{"key": "OTHER", "name": "Other space"}, 201))
	otherID, _ := other["id"].(string)

	newPage := func(spaceID, title, body, parent string) string {
		t.Helper()
		payload := map[string]any{"spaceId": spaceID, "title": title, "status": "current",
			"body": models.WikiBody{Representation: "storage", Value: body}}
		if parent != "" {
			payload["parentId"] = parent
		}
		page := object(callV2(author, "POST", "/pages", payload, 200))
		id, _ := page["id"].(string)
		return id
	}
	runbook := newPage(spaceID, "Deployment runbook", "<p>How to deploy the service safely.</p>", "")
	onboarding := newPage(spaceID, "Onboarding guide", "<p>Welcome aboard. Read the runbook first.</p>", runbook)
	checklist := newPage(spaceID, "Release checklist", "<p>Steps before a release.</p>", onboarding)
	newPage(otherID, "Elsewhere", "<p>A runbook lives here too.</p>", "")

	// A query names what to find, and finds only that.
	if _, titles := search(author, "/search", "type = page", "", 200); !has(titles, "Deployment runbook", "Onboarding guide", "Release checklist", "Elsewhere") {
		t.Fatalf("all pages: %v", titles)
	}
	if _, titles := search(author, "/search", `space = SEARCH and type = page`, "", 200); !has(titles, "Deployment runbook", "Onboarding guide", "Release checklist") {
		t.Fatalf("one space: %v", titles)
	}
	// The words a reader types are looked for in the title and the body alike.
	if _, titles := search(author, "/search", `text ~ "runbook"`, "", 200); !has(titles, "Deployment runbook", "Onboarding guide", "Elsewhere") {
		t.Fatalf("full text: %v", titles)
	}
	if _, titles := search(author, "/search", `title ~ "runbook"`, "", 200); !has(titles, "Deployment runbook") {
		t.Fatalf("title only: %v", titles)
	}
	// A parent is the page directly above; an ancestor is any page above.
	if _, titles := search(author, "/search", `parent = `+runbook, "", 200); !has(titles, "Onboarding guide") {
		t.Fatalf("parent: %v", titles)
	}
	if _, titles := search(author, "/search", `ancestor = `+runbook, "", 200); !has(titles, "Onboarding guide", "Release checklist") {
		t.Fatalf("ancestor: %v", titles)
	}
	// Boolean structure means what it says, and brackets change it.
	if _, titles := search(author, "/search", `text ~ "runbook" and not title ~ "onboard"`, "", 200); !has(titles, "Deployment runbook", "Elsewhere") {
		t.Fatalf("and not: %v", titles)
	}
	if _, titles := search(author, "/search", `title ~ "runbook" or title ~ "checklist"`, "", 200); !has(titles, "Deployment runbook", "Release checklist") {
		t.Fatalf("or: %v", titles)
	}
	if _, titles := search(author, "/search", `space = SEARCH and (title ~ "runbook" or title ~ "elsewhere")`, "", 200); !has(titles, "Deployment runbook") {
		t.Fatalf("brackets: %v", titles)
	}
	// Without brackets AND binds tighter, so the same words find more.
	if _, titles := search(author, "/search", `space = SEARCH and title ~ "runbook" or title ~ "elsewhere"`, "", 200); !has(titles, "Deployment runbook", "Elsewhere") {
		t.Fatalf("precedence: %v", titles)
	}

	// A space is searchable too, and the search that reads content never
	// returns one however the query is written.
	if _, titles := search(author, "/search", `type = space`, "", 200); !has(titles, "Search space", "Other space") {
		t.Fatalf("spaces: %v", titles)
	}
	if _, titles := search(author, "/content/search", `type = space`, "", 200); len(titles) != 0 {
		t.Fatalf("the content search returned a space: %v", titles)
	}
	// The content search returns the content itself rather than a description
	// of the match.
	body, _ := search(author, "/content/search", `title ~ "runbook"`, "", 200)
	results, _ := body["results"].([]any)
	first, _ := results[0].(map[string]any)
	if first["id"] != runbook || first["type"] != "page" || first["excerpt"] != nil {
		t.Fatalf("content result: %v", first)
	}
	// The search that describes matches carries the content inside the result.
	body, _ = search(author, "/search", `title ~ "runbook"`, "", 200)
	first, _ = body["results"].([]any)[0].(map[string]any)
	content, _ := first["content"].(map[string]any)
	if content["id"] != runbook || first["entityType"] != "content" || first["url"] == "" {
		t.Fatalf("search result: %v", first)
	}
	if body["cqlQuery"] != `title ~ "runbook"` || body["totalSize"] != float64(1) {
		t.Fatalf("search envelope: %v", body)
	}

	// The excerpt shows why a result is there, and says so only when asked.
	body, _ = search(author, "/search", `text ~ "runbook"`, "excerpt=highlight", 200)
	marked := false
	for _, raw := range body["results"].([]any) {
		result, _ := raw.(map[string]any)
		if excerpt, _ := result["excerpt"].(string); strings.Contains(excerpt, "@@@hl@@@runbook@@@endhl@@@") {
			marked = true
		}
	}
	if !marked {
		t.Fatalf("no excerpt marked the word searched for: %v", body["results"])
	}
	body, _ = search(author, "/search", `text ~ "runbook"`, "excerpt=none", 200)
	for _, raw := range body["results"].([]any) {
		if result, _ := raw.(map[string]any); result["excerpt"] != "" {
			t.Fatalf("excerpt=none still carried one: %v", result)
		}
	}

	// A label, a watch and a favourite are all searchable once they exist.
	send(v1, "/wiki/rest/api", author, "POST", "/content/"+checklist+"/label", []map[string]string{{"name": "release"}}, 200)
	if _, titles := search(author, "/search", `label = release`, "", 200); !has(titles, "Release checklist") {
		t.Fatalf("label: %v", titles)
	}
	if _, titles := search(author, "/search", `label = nothing`, "", 200); len(titles) != 0 {
		t.Fatalf("a label nobody used: %v", titles)
	}
	send(v1, "/wiki/rest/api", reader, "POST", "/user/watch/content/"+runbook, nil, 204)
	if _, titles := search(author, "/search", `watcher = `+reader, "", 200); !has(titles, "Deployment runbook") {
		t.Fatalf("watcher: %v", titles)
	}
	send(v1, "/wiki/rest/api", reader, "PUT", "/relation/favourite/from/user/current/to/content/"+onboarding, nil, 200)
	send(v1, "/wiki/rest/api", reader, "PUT", "/relation/favourite/from/user/current/to/space/SEARCH", nil, 200)
	// A favourite belongs to the person who saved it, so each reader's search
	// finds their own.
	if _, titles := search(reader, "/search", `favourite = currentUser()`, "", 200); !has(titles, "Onboarding guide", "Search space") {
		t.Fatalf("the reader's favourites: %v", titles)
	}
	if _, titles := search(author, "/search", `favourite = currentUser()`, "", 200); len(titles) != 0 {
		t.Fatalf("the author has saved nothing: %v", titles)
	}
	// A named set of spaces is read from the favourites, not from the query.
	if _, titles := search(reader, "/search", `space in favouriteSpaces() and type = page`, "", 200); !has(titles, "Deployment runbook", "Onboarding guide", "Release checklist") {
		t.Fatalf("favourite spaces: %v", titles)
	}

	// Who wrote something is searchable, and currentUser() means whoever asked.
	if _, titles := search(author, "/search", `creator = currentUser() and type = page`, "", 200); len(titles) != 4 {
		t.Fatalf("the author wrote every page: %v", titles)
	}
	if _, titles := search(reader, "/search", `creator = currentUser() and type = page`, "", 200); len(titles) != 0 {
		t.Fatalf("the reader wrote none: %v", titles)
	}

	// Ordering is what a reader asked for, and settles ties so paging is stable.
	_, titles := search(author, "/search", `type = page order by title`, "", 200)
	if len(titles) != 4 || titles[0] != "Deployment runbook" || titles[3] != "Release checklist" {
		t.Fatalf("ordered by title: %v", titles)
	}
	_, descending := search(author, "/search", `type = page order by title desc`, "", 200)
	if len(descending) != 4 || descending[0] != "Release checklist" {
		t.Fatalf("descending: %v", descending)
	}

	// Paging walks the results rather than repeating them, and the link handed
	// back is the one that continues the walk.
	body, firstPage := search(author, "/search", `type = page order by title`, "limit=2", 200)
	if len(firstPage) != 2 || firstPage[0] != "Deployment runbook" {
		t.Fatalf("first page: %v", firstPage)
	}
	if body["totalSize"] != float64(4) {
		t.Fatalf("total across pages: %v", body["totalSize"])
	}
	links, _ := body["_links"].(map[string]any)
	next, _ := links["next"].(string)
	if next == "" {
		t.Fatalf("no next link: %v", links)
	}
	following := object(send(v1, "/wiki/rest/api", author, "GET", strings.TrimPrefix(next, "/rest/api"), nil, 200))
	secondPage := []string{}
	for _, raw := range following["results"].([]any) {
		result, _ := raw.(map[string]any)
		title, _ := result["title"].(string)
		secondPage = append(secondPage, title)
	}
	if len(secondPage) != 2 || secondPage[0] != "Onboarding guide" {
		t.Fatalf("second page: %v", secondPage)
	}
	secondLinks, _ := following["_links"].(map[string]any)
	if _, ok := secondLinks["prev"]; !ok {
		t.Fatalf("the second page has no way back: %v", secondLinks)
	}
	if _, ok := secondLinks["next"]; ok {
		t.Fatalf("the last page offered another: %v", secondLinks)
	}
	// A cursor this search did not issue is refused rather than read as a
	// position it happens to decode to.
	search(author, "/search", `type = page`, "cursor=nonsense", 400)
	// Walking past the end returns nothing but still says how many there are,
	// so a client knows it reached the end rather than losing the count.
	past, empty := search(author, "/search", `type = page`, "start=10", 200)
	if len(empty) != 0 || past["totalSize"] != float64(4) {
		t.Fatalf("past the end: %v %v", empty, past["totalSize"])
	}
	// Asking for none of them is how a client asks only how many there are.
	counted, none := search(author, "/search", `type = page`, "limit=0", 200)
	if len(none) != 0 || counted["totalSize"] != float64(4) {
		t.Fatalf("count only: %v %v", none, counted["totalSize"])
	}

	// cqlcontext scopes the search, and a draft is not searched unless asked for.
	draft := object(callV2(author, "POST", "/pages", map[string]any{"spaceId": spaceID, "title": "Unfinished",
		"status": "draft", "body": models.WikiBody{Representation: "storage", Value: "<p>Not ready.</p>"}}, 200))
	if _, titles := search(author, "/search", `title ~ "unfinished"`, "", 200); len(titles) != 0 {
		t.Fatalf("a draft was searched: %v", titles)
	}
	scoped := url.QueryEscape(`{"spaceKey":"SEARCH","contentStatuses":["draft"]}`)
	if _, titles := search(author, "/search", `title ~ "unfinished"`, "cqlcontext="+scoped, 200); !has(titles, "Unfinished") {
		t.Fatalf("drafts when asked for: %v", titles)
	}
	_ = draft
	contextOne := url.QueryEscape(`{"spaceKey":"SEARCH"}`)
	if _, titles := search(author, "/search", `type = page`, "cqlcontext="+contextOne, 200); !has(titles, "Deployment runbook", "Onboarding guide", "Release checklist") {
		t.Fatalf("space context: %v", titles)
	}
	// currentSpace() means the space the search was scoped to, and needs one.
	if _, titles := search(author, "/search", `space = currentSpace() and type = page`, "cqlcontext="+contextOne, 200); !has(titles, "Deployment runbook", "Onboarding guide", "Release checklist") {
		t.Fatalf("currentSpace: %v", titles)
	}
	search(author, "/search", `space = currentSpace()`, "", 400)

	// A query that cannot be read, or that asks for something the search does
	// not have, is refused rather than answered with everything.
	for _, invalid := range []string{`type = page and`, `nosuchfield = 1`, `type = page order by relevance`, `created ~ "yesterday"`} {
		search(author, "/search", invalid, "", 400)
		search(author, "/content/search", invalid, "", 400)
	}
	send(v1, "/wiki/rest/api", author, "GET", "/search", nil, 400)
	send(v1, "/wiki/rest/api", author, "GET", "/content/search", nil, 400)

	// A search never widens what a reader may see. A page restricted to its
	// author is absent from everybody else's results, whatever they ask.
	send(v1, "/wiki/rest/api", author, "PUT", "/content/"+checklist+"/restriction",
		[]map[string]any{{"operation": "read", "restrictions": map[string]any{"user": []map[string]string{{"accountId": author}}}}}, 200)
	if _, titles := search(reader, "/search", `title ~ "checklist"`, "", 200); len(titles) != 0 {
		t.Fatalf("a restricted page reached another reader: %v", titles)
	}
	if _, titles := search(author, "/search", `title ~ "checklist"`, "", 200); !has(titles, "Release checklist") {
		t.Fatalf("its author cannot find it: %v", titles)
	}
	// The total counts only what the reader may see, so a restricted page is
	// not reported as a result they cannot reach.
	restricted, _ := search(reader, "/search", `space = SEARCH and type = page`, "", 200)
	if restricted["totalSize"] != float64(2) {
		t.Fatalf("total for a reader who may not see everything: %v", restricted["totalSize"])
	}
}
