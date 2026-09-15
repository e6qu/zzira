package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestV1AttachmentsOnBlogPostsAndMoves pins the v1 attachment routes on blog
// posts — upload, new versions by name and by id, and download — and changing
// an attachment's name, media type and comment or moving it to a page.
func TestV1AttachmentsOnBlogPostsAndMoves(t *testing.T) {
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
	ws, admin := store.NewID("ws"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Attachments test')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$1)`, admin, admin+"@example.test")
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, admin, store.HashToken(admin))
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin')`, ws, admin)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_attachment_versions WHERE attachment_id IN (SELECT a.id FROM wiki_attachments a LEFT JOIN wiki_pages p ON p.id=a.page_id LEFT JOIN wiki_blog_posts b ON b.id=a.blog_post_id JOIN wiki_spaces s ON s.id=COALESCE(p.space_id,b.space_id) WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_attachments WHERE id IN (SELECT a.id FROM wiki_attachments a LEFT JOIN wiki_pages p ON p.id=a.page_id LEFT JOIN wiki_blog_posts b ON b.id=a.blog_post_id JOIN wiki_spaces s ON s.id=COALESCE(p.space_id,b.space_id) WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_post_versions WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, admin)
		exec(`DELETE FROM users WHERE id=$1`, admin)
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	v1 := &V1Handler{Handler: h}
	send := func(method, path, contentType string, body *bytes.Buffer, want int) (map[string]any, *httptest.ResponseRecorder) {
		t.Helper()
		request := httptest.NewRequest(method, path, body)
		request.SetBasicAuth(admin+"@example.test", admin)
		request.Header.Set("X-Atlassian-Token", "no-check")
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		response := httptest.NewRecorder()
		if strings.HasPrefix(path, "/wiki/api/v2") {
			h.ServeHTTP(response, request)
		} else {
			v1.ServeHTTP(response, request)
		}
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return out, response
	}
	jsonBody := func(value any) *bytes.Buffer {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return bytes.NewBuffer(raw)
	}
	upload := func(method, path, filename, content string, want int) map[string]any {
		t.Helper()
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("comment", "Figures"); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		out, _ := send(method, path, writer.FormDataContentType(), &buffer, want)
		return out
	}
	storage := map[string]any{"representation": "storage", "value": "<p>Report</p>"}
	spaceBean, _ := send("POST", "/wiki/api/v2/spaces", "application/json", jsonBody(map[string]any{"key": "ATT", "name": "Attachments"}), 201)
	space := spaceBean["id"].(string)
	postBean, _ := send("POST", "/wiki/api/v2/blogposts", "application/json", jsonBody(map[string]any{"spaceId": space, "title": "Weekly", "status": "current", "body": storage}), 200)
	post := postBean["id"].(string)
	pageBean, _ := send("POST", "/wiki/api/v2/pages", "application/json", jsonBody(map[string]any{"spaceId": space, "title": "Archive", "status": "current", "body": storage}), 200)
	page := pageBean["id"].(string)

	// Blog posts take attachments through the v1 routes.
	created := upload("POST", "/wiki/rest/api/content/"+post+"/child/attachment", "figures.csv", "a,b", 200)["results"].([]any)[0].(map[string]any)
	attachment := created["id"].(string)
	if created["container"].(map[string]any)["type"] != "blogpost" || created["extensions"].(map[string]any)["collectionName"] != "contentId-"+post {
		t.Fatalf("blog attachment bean: %v", created)
	}
	updated := upload("PUT", "/wiki/rest/api/content/"+post+"/child/attachment", "figures.csv", "a,b,c", 200)["results"].([]any)[0].(map[string]any)
	if updated["id"] != attachment || updated["version"].(map[string]any)["number"] != float64(2) {
		t.Fatalf("new version by name: %v", updated)
	}
	byID := upload("POST", "/wiki/rest/api/content/"+post+"/child/attachment/"+attachment+"/data", "figures.csv", "a,b,c,d", 200)
	if byID["version"].(map[string]any)["number"] != float64(3) {
		t.Fatalf("new version by id: %v", byID)
	}
	if _, response := send("GET", "/wiki/rest/api/content/"+post+"/child/attachment/"+attachment+"/download", "", &bytes.Buffer{}, 302); !strings.Contains(response.Header().Get("Location"), "/wiki/download/attachments/"+post+"/"+attachment+"/figures.csv") {
		t.Fatalf("download: %v", response.Header())
	}

	// Metadata changes as a new version, and the attachment moves to a page.
	renamed, _ := send("PUT", "/wiki/rest/api/content/"+post+"/child/attachment/"+attachment, "application/json", jsonBody(map[string]any{
		"id": attachment, "type": "attachment", "title": "q3-figures.csv", "version": map[string]any{"number": 4},
		"metadata": map[string]any{"mediaType": "text/csv", "comment": "Quarterly figures"},
	}), 200)
	if renamed["title"] != "q3-figures.csv" || renamed["metadata"].(map[string]any)["comment"] != "Quarterly figures" {
		t.Fatalf("renamed: %v", renamed)
	}
	send("PUT", "/wiki/rest/api/content/"+post+"/child/attachment/"+attachment, "application/json", jsonBody(map[string]any{"version": map[string]any{"number": 4}}), 409)
	moved, _ := send("PUT", "/wiki/rest/api/content/"+post+"/child/attachment/"+attachment, "application/json", jsonBody(map[string]any{
		"version": map[string]any{"number": 5}, "container": map[string]any{"id": page, "type": "page"},
	}), 200)
	if moved["container"].(map[string]any)["id"] != page || moved["container"].(map[string]any)["type"] != "page" {
		t.Fatalf("moved: %v", moved)
	}
	pageAttachments, _ := send("GET", "/wiki/api/v2/pages/"+page+"/attachments", "", &bytes.Buffer{}, 200)
	blogAttachments, _ := send("GET", "/wiki/api/v2/blogposts/"+post+"/attachments", "", &bytes.Buffer{}, 200)
	if len(pageAttachments["results"].([]any)) != 1 || len(blogAttachments["results"].([]any)) != 0 {
		t.Fatalf("after the move: page %v blog %v", pageAttachments, blogAttachments)
	}
}
