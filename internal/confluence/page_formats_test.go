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

func TestMediaTypeDescription(t *testing.T) {
	for _, tc := range []struct{ mediaType, filename, want string }{
		{"image/png", "a.png", "PNG Image"},
		{"application/pdf; charset=binary", "plan.pdf", "PDF Document"},
		{"application/octet-stream", "budget.xlsx", "Excel Spreadsheet"},
		{"video/mp4", "demo.mp4", "Video"},
		{"application/octet-stream", "blob.bin", "File"},
	} {
		if got := mediaTypeDescription(tc.mediaType, tc.filename); got != tc.want {
			t.Errorf("%s %s: got %q want %q", tc.mediaType, tc.filename, got, tc.want)
		}
	}
}

// TestPageFormatsOwnersAndLiveDocs pins how a page is written and read beyond
// its storage body: where a page without a parent goes, the nested and
// converted body forms, the reading formats, live docs, private pages,
// ownership, collaborators and stars, and the v1 content expansions and
// descendant reads.
func TestPageFormatsOwnersAndLiveDocs(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Page formats test')`, ws)
	for _, user := range []struct{ id, role string }{{actor, "admin"}, {other, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$1)`, user.id, user.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, user.id, user.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, user.id, store.HashToken(user.id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_attachment_versions WHERE attachment_id IN (SELECT a.id FROM wiki_attachments a JOIN wiki_pages p ON p.id=a.page_id JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_attachments WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_footer_comment_versions WHERE comment_id IN (SELECT c.id FROM wiki_footer_comments c JOIN wiki_pages p ON p.id=c.page_id JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`UPDATE wiki_footer_comments SET parent_id=NULL WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_footer_comments WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_favourites WHERE workspace_id=$1`,
			`UPDATE wiki_pages SET parent_content_id=NULL WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_content WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`UPDATE wiki_spaces SET homepage_id=NULL WHERE workspace_id=$1`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`UPDATE wiki_pages SET parent_id=NULL WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM notifications WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, user := range []string{actor, other} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := &commands.Service{Store: st, Blobs: blobs}
	h := &Handler{Store: st, Commands: service, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	v1 := &V1Handler{Handler: h}
	send := func(handler http.Handler, user, prefix, method, path string, body any, want int) map[string]any {
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
		out := map[string]any{}
		if response.Body.Len() > 0 {
			if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
		}
		return out
	}
	callV2 := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		return send(h, actor, "/wiki/api/v2", method, path, body, want)
	}
	callV1 := func(method, path string, body any, want int) map[string]any {
		t.Helper()
		return send(v1, actor, "/wiki/rest/api", method, path, body, want)
	}
	idOf := func(body map[string]any) string {
		t.Helper()
		id, _ := body["id"].(string)
		if id == "" {
			t.Fatalf("no id in %v", body)
		}
		return id
	}
	dig := func(value any, path ...string) any {
		for _, key := range path {
			object, _ := value.(map[string]any)
			value = object[key]
		}
		return value
	}
	storage := func(value string) map[string]any { return map[string]any{"representation": "storage", "value": value} }
	space := idOf(callV2("POST", "/spaces", map[string]any{"key": "PFS", "name": "Formats"}, 201))

	// A new page without a parent goes beneath the space homepage, unless it
	// is asked to sit at the root.
	home := idOf(callV2("POST", "/pages?root-level=true", map[string]any{"spaceId": space, "title": "Home", "status": "current", "body": map[string]any{"storage": storage("<p>Home</p>")}}, 200))
	exec(`UPDATE wiki_spaces SET homepage_id=$2::bigint WHERE id::text=$1`, space, home)
	beneath := callV2("POST", "/pages", map[string]any{"spaceId": space, "title": "Beneath home", "status": "current", "body": storage("<p>Child</p>")}, 200)
	child := idOf(beneath)
	if beneath["parentId"] != home {
		t.Fatalf("a page without a parent was not put beneath the homepage: %v", beneath)
	}
	if atRoot := callV2("POST", "/pages?root-level=true", map[string]any{"spaceId": space, "title": "At the root", "status": "current", "body": storage("<p>Root</p>")}, 200); atRoot["parentId"] != nil {
		t.Fatalf("a root-level page has a parent: %v", atRoot)
	}
	callV2("POST", "/pages?root-level=true", map[string]any{"spaceId": space, "title": "Both", "parentId": home, "body": storage("<p>x</p>")}, 400)

	// Wiki markup and the document format are kept as storage.
	wikiPage := idOf(callV2("POST", "/pages", map[string]any{"spaceId": space, "title": "From wiki markup", "status": "current",
		"body": map[string]any{"wiki": map[string]any{"representation": "wiki", "value": "h2. Plan\n* ship *today*"}}}, 200))
	if got := dig(callV2("GET", "/pages/"+wikiPage+"?body-format=storage", nil, 200), "body", "storage", "value"); got != "<h2>Plan</h2><ul><li>ship <strong>today</strong></li></ul>" {
		t.Fatalf("wiki markup body: %v", got)
	}
	adfPage := idOf(callV2("POST", "/pages", map[string]any{"spaceId": space, "title": "From ADF", "status": "current",
		"body": map[string]any{"representation": "atlas_doc_format", "value": `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Written as ADF"}]}]}`}}, 200))
	if got, _ := dig(callV2("GET", "/pages/"+adfPage+"?body-format=storage", nil, 200), "body", "storage", "value").(string); !strings.Contains(got, "Written as ADF") {
		t.Fatalf("document format body: %q", got)
	}
	callV2("POST", "/pages", map[string]any{"spaceId": space, "title": "Two formats", "body": map[string]any{"storage": storage("<p>a</p>"), "wiki": map[string]any{"value": "b"}}}, 400)
	callV2("POST", "/pages", map[string]any{"spaceId": space, "title": "Markdown", "body": map[string]any{"representation": "markdown", "value": "# x"}}, 400)
	// Any primary format reads back.
	if got, _ := dig(callV2("GET", "/pages/"+wikiPage+"?body-format=atlas_doc_format", nil, 200), "body", "atlas_doc_format", "value").(string); !strings.Contains(got, "Plan") {
		t.Fatalf("document format read: %q", got)
	}
	if got, _ := dig(callV2("GET", "/pages/"+wikiPage+"?body-format=view", nil, 200), "body", "view", "value").(string); !strings.Contains(got, "<strong>today</strong>") {
		t.Fatalf("view read: %q", got)
	}
	callV2("GET", "/pages/"+wikiPage+"?body-format=bogus", nil, 400)
	callV2("GET", "/pages?space-id="+space+"&body-format=view", nil, 400)
	if listed := callV2("GET", "/pages?space-id="+space+"&body-format=atlas_doc_format", nil, 200); dig(listed["results"].([]any)[0], "body", "atlas_doc_format") == nil {
		t.Fatalf("list in the document format: %v", listed)
	}

	// A live doc is always published.
	live := idOf(callV2("POST", "/pages", map[string]any{"spaceId": space, "title": "Live notes", "status": "current", "subtype": "live", "body": storage("<p>Live</p>")}, 200))
	callV2("POST", "/pages", map[string]any{"spaceId": space, "title": "Live draft", "status": "draft", "subtype": "live", "body": storage("<p>x</p>")}, 400)
	callV2("POST", "/pages", map[string]any{"spaceId": space, "title": "Other subtype", "status": "current", "subtype": "blog", "body": storage("<p>x</p>")}, 400)
	titles := func(path string) string {
		results, _ := callV2("GET", path, nil, 200)["results"].([]any)
		out := []string{}
		for _, raw := range results {
			out = append(out, raw.(map[string]any)["title"].(string))
		}
		return strings.Join(out, ",")
	}
	if got := titles("/pages?space-id=" + space + "&subtype=live"); got != "Live notes" {
		t.Fatalf("live docs: %s", got)
	}
	if got := titles("/pages?space-id=" + space + "&subtype=page"); strings.Contains(got, "Live notes") {
		t.Fatalf("pages listed a live doc: %s", got)
	}
	callV2("PUT", "/pages/"+live, map[string]any{"id": live, "status": "current", "title": "Live notes", "subtype": "live", "body": storage("<p>x</p>"), "version": map[string]any{"number": 2}}, 400)

	// A private page is the creator's alone.
	private := idOf(callV2("POST", "/pages?private=true", map[string]any{"spaceId": space, "title": "Private plan", "status": "current", "body": storage("<p>Mine</p>")}, 200))
	send(h, other, "/wiki/api/v2", "GET", "/pages/"+private, nil, 404)
	send(h, other, "/wiki/api/v2", "GET", "/pages/"+child, nil, 200)

	// Ownership moves to another member, and the page remembers the last owner.
	transferred := callV2("PUT", "/pages/"+child, map[string]any{"id": child, "status": "current", "title": "Beneath home", "body": storage("<p>Child</p>"), "version": map[string]any{"number": 2}, "ownerId": other}, 200)
	if transferred["ownerId"] != other || transferred["lastOwnerId"] != actor || transferred["authorId"] != actor {
		t.Fatalf("ownership transfer: %v", transferred)
	}
	callV2("PUT", "/pages/"+child, map[string]any{"id": child, "status": "current", "title": "Beneath home", "body": storage("<p>Child</p>"), "version": map[string]any{"number": 3}, "ownerId": "usr_nobody"}, 400)
	send(h, other, "/wiki/api/v2", "PUT", "/pages/"+child, map[string]any{"id": child, "status": "current", "title": "Beneath home", "body": storage("<p>Edited by the owner</p>"), "version": map[string]any{"number": 3}}, 200)

	// Collaborators, stars and web resources.
	included := callV2("GET", "/pages/"+child+"?include-collaborators=true&include-favorited-by-current-user-status=true&include-webresources=true", nil, 200)
	collaborators, _ := dig(included, "collaborators", "results").([]any)
	if len(collaborators) != 2 || dig(collaborators[0], "accountId") != actor || dig(collaborators[1], "accountId") != other {
		t.Fatalf("collaborators: %v", included["collaborators"])
	}
	if included["isFavoritedByCurrentUser"] != false {
		t.Fatalf("an unstarred page reads as starred: %v", included)
	}
	if css, _ := dig(included, "webresources", "tags", "css").(string); !strings.Contains(css, "workspace.css") {
		t.Fatalf("web resources: %v", included["webresources"])
	}
	if err := st.SetWikiPageFavourite(ctx, ws, actor, child, true); err != nil {
		t.Fatal(err)
	}
	if starred := callV2("GET", "/pages/"+child+"?include-favorited-by-current-user-status=true", nil, 200); starred["isFavoritedByCurrentUser"] != true {
		t.Fatalf("a starred page: %v", starred)
	}
	if favourites, err := st.WikiFavouritePages(ctx, ws, actor); err != nil || len(favourites) != 1 || favourites[0].ID != child {
		t.Fatalf("starred pages: %v %v", favourites, err)
	}
	if err := st.SetWikiPageFavourite(ctx, ws, actor, private, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetWikiPageFavourite(ctx, ws, other, private, true); err == nil {
		t.Fatal("a reader starred a page they cannot see")
	}

	// The v1 copy answers the content with the expansions asked for.
	copied := callV1("POST", "/content/"+child+"/copy?expand=space,version,history,body.storage,ancestors,metadata.labels,children.page", map[string]any{
		"pageTitle": "Copied child", "destination": map[string]any{"type": "parent_page", "value": home}}, 200)
	if dig(copied, "space", "key") != "PFS" || dig(copied, "version", "number") != float64(1) || dig(copied, "history", "createdBy", "accountId") != actor {
		t.Fatalf("copy expansions: %v", copied)
	}
	if got, _ := dig(copied, "body", "storage", "value").(string); !strings.Contains(got, "Edited by the owner") {
		t.Fatalf("copied body: %v", copied["body"])
	}
	if ancestors, _ := copied["ancestors"].([]any); len(ancestors) != 1 || dig(ancestors[0], "id") != home {
		t.Fatalf("copy ancestors: %v", copied["ancestors"])
	}
	if dig(copied, "metadata", "labels", "size") != float64(0) || dig(copied, "children", "page", "size") != float64(0) {
		t.Fatalf("copy metadata and children: %v %v", copied["metadata"], copied["children"])
	}
	if _, listed := dig(copied, "_expandable").(map[string]any)["operations"]; !listed {
		t.Fatalf("unexpanded parts are not listed: %v", copied["_expandable"])
	}
	callV1("POST", "/content/"+child+"/copy?expand=a,b,c,d,e,f,g,h,i", map[string]any{"destination": map[string]any{"type": "parent_page", "value": home}}, 400)

	// Descendants of every kind, by expansion and by type.
	folder := idOf(callV2("POST", "/folders", map[string]any{"spaceId": space, "title": "Home folder", "parentId": home}, 200))
	top, err := service.CreateWikiFooterComment(ctx, ws, actor, models.WikiFooterComment{PageID: home, Body: models.WikiBody{Representation: "storage", Value: "<p>Looks good</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateWikiFooterComment(ctx, ws, other, models.WikiFooterComment{ParentCommentID: top.ID, Body: models.WikiBody{Representation: "storage", Value: "<p>Agreed</p>"}}); err != nil {
		t.Fatal(err)
	}
	var attachmentID string
	if err := st.Pool.QueryRow(ctx, `INSERT INTO wiki_attachments(page_id,file_id,filename,media_type,comment,size,version,status,author_id)
		VALUES ($1::bigint,'file-plan','plan.pdf','application/pdf','',10,1,'current',$2) RETURNING id::text`, home, actor).Scan(&attachmentID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO wiki_attachment_versions(attachment_id,version,filename,media_type,comment,size,blob_ref,author_id,message)
		VALUES ($1::bigint,1,'plan.pdf','application/pdf','',10,'blob-plan',$2,'')`, attachmentID, actor)
	attachment := callV2("GET", "/attachments/"+attachmentID+"?include-collaborators=true", nil, 200)
	if attachment["mediaTypeDescription"] != "PDF Document" || dig(dig(attachment, "collaborators", "results").([]any)[0], "accountId") != actor {
		t.Fatalf("attachment description and collaborators: %v", attachment)
	}
	summary := callV1("GET", "/content/"+home+"/descendant", nil, 200)
	if _, expanded := summary["page"]; expanded || dig(summary, "_expandable", "page") != "/rest/api/content/"+home+"/descendant/page" {
		t.Fatalf("unexpanded descendants: %v", summary)
	}
	expanded := callV1("GET", "/content/"+home+"/descendant?expand=page,comment,attachment,folder", nil, 200)
	if dig(expanded, "comment", "size") != float64(2) || dig(expanded, "attachment", "size") != float64(1) || dig(expanded, "folder", "size") != float64(1) {
		t.Fatalf("expanded descendants: %v", expanded)
	}
	if pages, _ := dig(expanded, "page", "results").([]any); len(pages) < 3 {
		t.Fatalf("page descendants: %v", expanded["page"])
	}
	callV1("GET", "/content/"+home+"/descendant?expand=bogus", nil, 400)
	if roots := callV1("GET", "/content/"+home+"/descendant/comment?depth=root", nil, 200); roots["size"] != float64(1) {
		t.Fatalf("top-level comments: %v", roots)
	}
	if all := callV1("GET", "/content/"+home+"/descendant/comment", nil, 200); all["size"] != float64(2) {
		t.Fatalf("all comments: %v", all)
	}
	if files := callV1("GET", "/content/"+home+"/descendant/attachment?depth=root", nil, 200); files["size"] != float64(1) || dig(files["results"].([]any)[0], "extensions", "mediaTypeDescription") != "PDF Document" {
		t.Fatalf("attachment descendants: %v", files)
	}
	versions := callV1("GET", "/content/"+home+"/descendant/page?depth=root&expand=version", nil, 200)
	if results, _ := versions["results"].([]any); len(results) == 0 || dig(results[0], "version", "number") == nil {
		t.Fatalf("expanded page descendants: %v", versions)
	}
	_ = folder
}
