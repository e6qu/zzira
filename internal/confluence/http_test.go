package confluence

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"log"
	"mime/multipart"
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

func TestWriteErrorEscapesLogInput(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	rec := httptest.NewRecorder()
	writeError(rec, errors.New("database value\r\nforged entry\x1b[31m"))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "database value") {
		t.Fatalf("unexpected error response: %d %s", rec.Code, rec.Body.String())
	}
	logged := output.String()
	if strings.Count(logged, "\n") != 1 || strings.ContainsAny(logged, "\r\x1b") || !strings.Contains(logged, `database value\r\nforged entry\x1b[31m`) {
		t.Fatalf("unsafe log entry: %q", logged)
	}
}

func TestWikiAPIPrivacyAndVersionedLifecycle(t *testing.T) {
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
	ws, actor, admin, member, outsider := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Wiki test')`, ws)
	for _, id := range []string{actor, admin, member, outsider} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Wiki User')`, id, id+"@example.test")
		role := "member"
		if id == actor || id == admin {
			role = "admin"
		}
		if id != outsider {
			exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, id, role)
		}
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, id, store.HashToken(id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{`DELETE FROM wiki_content_property_versions WHERE property_id IN (SELECT cp.id FROM wiki_content_properties cp JOIN wiki_content c ON c.id=cp.content_id JOIN wiki_spaces s ON s.id=c.space_id WHERE s.workspace_id=$1)`, `DELETE FROM wiki_content_properties WHERE content_id IN (SELECT c.id FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id WHERE s.workspace_id=$1)`, `DELETE FROM wiki_content_versions WHERE content_id IN (SELECT c.id FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id WHERE s.workspace_id=$1)`, `DELETE FROM wiki_content WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`, `DELETE FROM wiki_blog_post_property_versions WHERE property_id IN (SELECT bp.id FROM wiki_blog_post_properties bp JOIN wiki_blog_posts b ON b.id=bp.blog_post_id JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`, `DELETE FROM wiki_blog_post_properties WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`, `DELETE FROM wiki_blog_post_labels WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`, `DELETE FROM wiki_blog_post_likes WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`, `DELETE FROM wiki_blog_post_versions WHERE blog_post_id IN (SELECT b.id FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1)`, `DELETE FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`, `DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`, `DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`, `DELETE FROM wiki_spaces WHERE workspace_id=$1`, `DELETE FROM wiki_labels WHERE workspace_id=$1`, `DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`} {
			exec(sql, ws)
		}
		for _, id := range []string{actor, admin, member, outsider} {
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
		r := httptest.NewRequest(method, "/wiki/api/v2"+path, strings.NewReader(string(raw)))
		if user != "" {
			r.SetBasicAuth(user+"@example.test", user)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	callV1 := func(user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, "/wiki/rest/api"+path, strings.NewReader(string(raw)))
		if user != "" {
			r.SetBasicAuth(user+"@example.test", user)
		}
		w := httptest.NewRecorder()
		v1.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("v1 %s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	callV1NoCheck := func(user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, "/wiki/rest/api"+path, strings.NewReader(string(raw)))
		r.SetBasicAuth(user+"@example.test", user)
		r.Header.Set("X-Atlassian-Token", "no-check")
		w := httptest.NewRecorder()
		v1.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("v1 no-check %s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	callV1Multipart := func(user, method, path, filename, content, comment string, want int) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("comment", comment); err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteField("minorEdit", "false"); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, "/wiki/rest/api"+path, &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.SetBasicAuth(user+"@example.test", user)
		w := httptest.NewRecorder()
		v1.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("v1 multipart %s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	space := func(key string, private bool) string {
		t.Helper()
		w := call(actor, "POST", "/spaces", map[string]any{"key": key, "name": key, "createPrivateSpace": private}, 201)
		var s struct{ ID string }
		if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	public, private := space("PUBLIC", false), space("PRIVATE", true)
	call("", "GET", "/spaces", nil, 401)
	call(outsider, "GET", "/spaces", nil, 403)
	call(member, "POST", "/spaces", map[string]string{"key": "DENIED", "name": "Denied"}, 403)
	hidden := call(member, "GET", "/spaces", nil, 200)
	if strings.Contains(hidden.Body.String(), "PRIVATE") {
		t.Fatal("private space leaked")
	}
	call(member, "GET", "/spaces/"+private, nil, 404)
	create := func(space, title, status string) models.WikiPage {
		t.Helper()
		w := call(actor, "POST", "/pages", map[string]any{"spaceId": space, "title": title, "status": status, "body": models.WikiBody{Representation: "storage", Value: "<p><strong>Release</strong> notes</p>"}}, 200)
		var page models.WikiPage
		// Wire body is a representation map, unlike the materialized model.
		var bean struct {
			ID, SpaceID, Title, Status string
			Version                    models.WikiVersion
			Body                       map[string]models.WikiBody
		}
		if err := json.Unmarshal(w.Body.Bytes(), &bean); err != nil {
			t.Fatal(err)
		}
		page = models.WikiPage{ID: bean.ID, SpaceID: bean.SpaceID, Title: bean.Title, Status: bean.Status, Version: bean.Version, Body: bean.Body["storage"]}
		return page
	}
	page := create(public, "Release guide", "current")
	draft := create(public, "Private draft", "draft")
	secret := create(private, "Secret guide", "current")
	call(actor, "POST", "/blogposts?private=maybe", map[string]any{"spaceId": public, "title": "Invalid blog", "status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>Invalid</p>"}}, 400)
	blogResponse := call(actor, "POST", "/blogposts", map[string]any{"spaceId": public, "title": "Release update", "status": "current", "createdAt": "2026-09-07T09:30:00Z", "body": models.WikiBody{Representation: "storage", Value: "<p>Release candidate is ready.</p>"}}, 200)
	var blog struct {
		ID, SpaceID, Title, Status string
		Version                    models.WikiVersion
		Body                       map[string]models.WikiBody
	}
	if err := json.Unmarshal(blogResponse.Body.Bytes(), &blog); err != nil || blog.Version.Number != 1 || blog.Body["storage"].Value == "" {
		t.Fatalf("unexpected blog post: %+v %v", blog, err)
	}
	call(actor, "POST", "/blogposts", map[string]any{"spaceId": public, "title": "Release update", "status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>Duplicate</p>"}}, 400)
	if listed := call(member, "GET", "/blogposts?space-id="+public+"&title=Release%20update&body-format=storage", nil, 200); !strings.Contains(listed.Body.String(), "Release candidate is ready") {
		t.Fatal(listed.Body.String())
	}
	call(member, "GET", "/spaces/"+public+"/blogposts?sort=-modified-date", nil, 200)
	call(member, "GET", "/blogposts/"+blog.ID+"?body-format=storage", nil, 200)
	updatedBlog := call(actor, "PUT", "/blogposts/"+blog.ID, map[string]any{"id": blog.ID, "spaceId": public, "title": "Release update", "status": "current", "body": map[string]any{"storage": models.WikiBody{Representation: "storage", Value: "<p>Release candidate reached production.</p>"}}, "version": map[string]any{"number": 2, "message": "Published rollout"}}, 200)
	if !strings.Contains(updatedBlog.Body.String(), "Published rollout") {
		t.Fatal(updatedBlog.Body.String())
	}
	call(actor, "PUT", "/blogposts/"+blog.ID, map[string]any{"id": blog.ID, "spaceId": public, "title": "Stale update", "status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>Stale</p>"}, "version": map[string]any{"number": 2}}, 409)
	if versions := call(member, "GET", "/blogposts/"+blog.ID+"/versions?sort=-modified-date", nil, 200); !strings.Contains(versions.Body.String(), "Published rollout") {
		t.Fatal(versions.Body.String())
	}
	detail := call(member, "GET", "/blogposts/"+blog.ID+"/versions/2", nil, 200)
	if !strings.Contains(detail.Body.String(), `"prevVersion":1`) {
		t.Fatal(detail.Body.String())
	}
	blogAttachment, err := h.Commands.SaveWikiBlogAttachment(ctx, ws, actor, blog.ID, "", "release-evidence.txt", "text/plain", "Production evidence", "", false, strings.NewReader("release evidence v1"))
	if err != nil || blogAttachment.BlogPostID != blog.ID {
		t.Fatalf("save blog attachment: %+v %v", blogAttachment, err)
	}
	if listed := call(member, "GET", "/blogposts/"+blog.ID+"/attachments?filename=release-evidence.txt", nil, 200); !strings.Contains(listed.Body.String(), "release-evidence.txt") || !strings.Contains(listed.Body.String(), `"blogPostId":"`+blog.ID+`"`) {
		t.Fatal(listed.Body.String())
	}
	if attachment := call(member, "GET", "/attachments/"+blogAttachment.ID+"?include-versions=true&include-operations=true", nil, 200); !strings.Contains(attachment.Body.String(), "Production evidence") || !strings.Contains(attachment.Body.String(), `"operation":"update"`) {
		t.Fatal(attachment.Body.String())
	}
	privateBlogResponse := call(actor, "POST", "/blogposts?private=true", map[string]any{"spaceId": public, "title": "Private launch journal", "status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>Private blog phrase</p>"}}, 200)
	var privateBlog struct{ ID string }
	if err := json.Unmarshal(privateBlogResponse.Body.Bytes(), &privateBlog); err != nil {
		t.Fatal(err)
	}
	call(member, "GET", "/blogposts/"+privateBlog.ID, nil, 404)
	if _, err := h.Commands.SaveWikiBlogAttachment(ctx, ws, actor, privateBlog.ID, "", "private-evidence.txt", "text/plain", "", "", false, strings.NewReader("private evidence")); err != nil {
		t.Fatal(err)
	}
	call(member, "GET", "/blogposts/"+privateBlog.ID+"/attachments", nil, 404)
	blogLabels, err := h.Commands.AddWikiBlogPostLabels(ctx, ws, actor, blog.ID, []models.WikiLabel{{Prefix: "global", Name: "release-update"}})
	if err != nil || len(blogLabels) != 1 {
		t.Fatalf("add blog label: %+v %v", blogLabels, err)
	}
	if labels := call(member, "GET", "/blogposts/"+blog.ID+"/labels?sort=name", nil, 200); !strings.Contains(labels.Body.String(), "release-update") {
		t.Fatal(labels.Body.String())
	}
	if posts := call(member, "GET", "/labels/"+blogLabels[0].ID+"/blogposts?body-format=storage", nil, 200); !strings.Contains(posts.Body.String(), "Release candidate reached production") {
		t.Fatal(posts.Body.String())
	}
	if operations := call(member, "GET", "/blogposts/"+blog.ID+"/operations", nil, 200); !strings.Contains(operations.Body.String(), `"operation":"update"`) {
		t.Fatal(operations.Body.String())
	}
	call(member, "GET", "/blogposts/"+blog.ID+"/likes/count", nil, 200)
	if err := st.SetWikiBlogPostLike(ctx, ws, member, blog.ID, true); err != nil {
		t.Fatal(err)
	}
	if count := call(actor, "GET", "/blogposts/"+blog.ID+"/likes/count", nil, 200); !strings.Contains(count.Body.String(), `"count":1`) {
		t.Fatal(count.Body.String())
	}
	if users := call(actor, "GET", "/blogposts/"+blog.ID+"/likes/users", nil, 200); !strings.Contains(users.Body.String(), member) {
		t.Fatal(users.Body.String())
	}
	blogPropertyResponse := call(actor, "POST", "/blogposts/"+blog.ID+"/properties", map[string]any{"key": "release-metadata", "value": map[string]any{"ring": "canary"}}, 200)
	var blogProperty models.WikiContentProperty
	if err := json.Unmarshal(blogPropertyResponse.Body.Bytes(), &blogProperty); err != nil || blogProperty.Version.Number != 1 {
		t.Fatalf("unexpected blog property: %+v %v", blogProperty, err)
	}
	call(actor, "POST", "/blogposts/"+blog.ID+"/properties", map[string]any{"key": "release-metadata", "value": true}, 400)
	if properties := call(member, "GET", "/blogposts/"+blog.ID+"/properties?key=release-metadata", nil, 200); !strings.Contains(properties.Body.String(), "canary") {
		t.Fatal(properties.Body.String())
	}
	call(member, "GET", "/blogposts/"+blog.ID+"/properties/"+blogProperty.ID, nil, 200)
	updatedBlogProperty := call(actor, "PUT", "/blogposts/"+blog.ID+"/properties/"+blogProperty.ID, map[string]any{"key": "release-metadata", "value": map[string]any{"ring": "global"}, "version": map[string]any{"number": 2, "message": "Expanded rollout"}}, 200)
	if !strings.Contains(updatedBlogProperty.Body.String(), "global") {
		t.Fatal(updatedBlogProperty.Body.String())
	}
	call(actor, "PUT", "/blogposts/"+blog.ID+"/properties/"+blogProperty.ID, map[string]any{"key": "release-metadata", "value": false, "version": map[string]any{"number": 2}}, 409)
	call(actor, "DELETE", "/blogposts/"+blog.ID+"/properties/"+blogProperty.ID, nil, 204)
	call(actor, "GET", "/blogposts/"+blog.ID+"/classification-level", nil, 404)
	call(actor, "PUT", "/blogposts/"+blog.ID+"/classification-level", map[string]string{"id": "internal", "status": "current"}, 204)
	if classification := call(member, "GET", "/blogposts/"+blog.ID+"/classification-level?status=current", nil, 200); !strings.Contains(classification.Body.String(), "Internal") {
		t.Fatal(classification.Body.String())
	}
	call(actor, "POST", "/blogposts/"+blog.ID+"/classification-level/reset", map[string]string{"status": "current"}, 204)
	call(actor, "GET", "/blogposts/"+blog.ID+"/custom-content", nil, 400)
	call(actor, "GET", "/blogposts/"+blog.ID+"/custom-content?type=com.example:unknown", nil, 404)
	exec(`INSERT INTO wiki_blog_custom_content(blog_post_id,type,title,body,author_id) VALUES($1::bigint,'com.zzira:diagram','Release topology','<p>Service graph</p>',$2)`, blog.ID, actor)
	if custom := call(member, "GET", "/blogposts/"+blog.ID+"/custom-content?type=com.zzira:diagram&body-format=storage", nil, 200); !strings.Contains(custom.Body.String(), "Release topology") || !strings.Contains(custom.Body.String(), "Service graph") {
		t.Fatal(custom.Body.String())
	}
	call(member, "GET", "/blogposts/"+privateBlog.ID+"/custom-content?type=com.zzira:diagram", nil, 404)
	currentBlogResponse := call(actor, "GET", "/blogposts/"+blog.ID+"?body-format=storage", nil, 200)
	var currentBlog struct {
		Version models.WikiVersion
		Body    map[string]models.WikiBody
	}
	if err := json.Unmarshal(currentBlogResponse.Body.Bytes(), &currentBlog); err != nil {
		t.Fatal(err)
	}
	redactionText := currentBlog.Body["storage"].Value
	redactionFrom := strings.Index(redactionText, "production")
	redactionTo := redactionFrom + len("production")
	call(actor, "POST", "/blogposts/"+blog.ID+"/redact", map[string]any{"createdAt": "2020-01-01T00:00:00Z", "body": map[string]any{"redactions": []any{map[string]any{"pointer": "/body/storage/value", "from": redactionFrom, "to": redactionTo}}}}, 400)
	redacted := call(actor, "POST", "/blogposts/"+blog.ID+"/redact", map[string]any{"createdAt": currentBlog.Version.CreatedAt, "versionNumber": currentBlog.Version.Number, "cleanHistory": true, "body": map[string]any{"redactions": []any{map[string]any{"pointer": "/body/storage/value", "from": redactionFrom, "to": redactionTo, "reason": "Customer data"}}}}, 202)
	if !strings.Contains(redacted.Body.String(), "redactionId") {
		t.Fatal(redacted.Body.String())
	}
	if post := call(member, "GET", "/blogposts/"+blog.ID+"?body-format=storage", nil, 200); strings.Contains(post.Body.String(), "production") || !strings.Contains(post.Body.String(), "[REDACTED]") {
		t.Fatal(post.Body.String())
	}
	var historicalSensitive int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM wiki_blog_post_versions WHERE blog_post_id::text=$1 AND body LIKE '%production%'`, blog.ID).Scan(&historicalSensitive); err != nil || historicalSensitive != 0 {
		t.Fatalf("historical sensitive versions=%d: %v", historicalSensitive, err)
	}
	var redactionAudit int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE target_type='wiki_blogpost' AND target_id=$1 AND action='wiki.blogpost.redacted'`, blog.ID).Scan(&redactionAudit); err != nil || redactionAudit != 1 {
		t.Fatalf("redaction audit=%d: %v", redactionAudit, err)
	}
	if _, err := h.Commands.CreateWikiBlogPostProperty(ctx, ws, actor, privateBlog.ID, "private-blog-metadata", json.RawMessage(`{"secret":true}`)); err != nil {
		t.Fatal(err)
	}
	privateBlogLabels, err := h.Commands.AddWikiBlogPostLabels(ctx, ws, actor, privateBlog.ID, []models.WikiLabel{{Prefix: "global", Name: "secret-blog-label"}})
	if err != nil || len(privateBlogLabels) != 1 {
		t.Fatal(err)
	}
	if err := st.SetWikiBlogPostLike(ctx, ws, actor, privateBlog.ID, true); err != nil {
		t.Fatal(err)
	}
	call(member, "GET", "/blogposts/"+privateBlog.ID+"/properties", nil, 404)
	call(member, "GET", "/blogposts/"+privateBlog.ID+"/labels", nil, 404)
	call(member, "GET", "/blogposts/"+privateBlog.ID+"/likes/users", nil, 404)
	call(member, "GET", "/labels/"+privateBlogLabels[0].ID+"/blogposts", nil, 404)
	if labels := call(member, "GET", "/labels", nil, 200); strings.Contains(labels.Body.String(), "secret-blog-label") {
		t.Fatal(labels.Body.String())
	}
	call(actor, "DELETE", "/blogposts/"+blog.ID, nil, 204)
	call(actor, "GET", "/blogposts/"+blog.ID, nil, 404)
	call(actor, "GET", "/blogposts/"+blog.ID+"?status=trashed", nil, 200)
	call(actor, "PUT", "/blogposts/"+blog.ID, map[string]any{"id": blog.ID, "spaceId": public, "title": "Ignored during restore", "status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>Ignored</p>"}, "version": map[string]any{"number": 5, "message": "Restored"}}, 200)
	call(actor, "DELETE", "/blogposts/"+blog.ID, nil, 204)
	call(actor, "DELETE", "/blogposts/"+blog.ID+"?purge=true", nil, 204)
	call(actor, "GET", "/blogposts/"+blog.ID+"?status=trashed", nil, 404)
	rootFolderResponse := call(member, "POST", "/folders", map[string]any{"spaceId": public, "title": "Runbooks", "parentId": page.ID}, 200)
	var rootFolder models.WikiContent
	if err := json.Unmarshal(rootFolderResponse.Body.Bytes(), &rootFolder); err != nil || rootFolder.Type != "folder" || rootFolder.ParentType != "page" {
		t.Fatalf("unexpected root folder: %+v %v", rootFolder, err)
	}
	childFolderResponse := call(member, "POST", "/folders", map[string]any{"spaceId": public, "title": "Database runbooks", "parentId": rootFolder.ID}, 200)
	var childFolder models.WikiContent
	if err := json.Unmarshal(childFolderResponse.Body.Bytes(), &childFolder); err != nil || childFolder.ParentType != "folder" {
		t.Fatalf("unexpected child folder: %+v %v", childFolder, err)
	}
	call(member, "POST", "/embeds", map[string]any{"spaceId": public, "title": "Unsafe link", "embedUrl": "javascript:alert(1)"}, 400)
	smartLinkResponse := call(member, "POST", "/embeds", map[string]any{"spaceId": public, "title": "Incident dashboard", "parentId": childFolder.ID, "embedUrl": "https://status.example.test/incidents"}, 200)
	var smartLink models.WikiContent
	if err := json.Unmarshal(smartLinkResponse.Body.Bytes(), &smartLink); err != nil || smartLink.Type != "embed" || smartLink.EmbedURL != "https://status.example.test/incidents" {
		t.Fatalf("unexpected Smart Link: %+v %v", smartLink, err)
	}
	linkChildResponse := call(member, "POST", "/folders", map[string]any{"spaceId": public, "title": "Incident exports", "parentId": smartLink.ID}, 200)
	var linkChild models.WikiContent
	if err := json.Unmarshal(linkChildResponse.Body.Bytes(), &linkChild); err != nil || linkChild.ParentType != "embed" {
		t.Fatalf("unexpected Smart Link child: %+v %v", linkChild, err)
	}
	if expanded := call(member, "GET", "/embeds/"+smartLink.ID+"?include-direct-children=true&include-operations=true&include-properties=true&include-collaborators=true", nil, 200); !strings.Contains(expanded.Body.String(), "Incident exports") || !strings.Contains(expanded.Body.String(), `"embedUrl":"https://status.example.test/incidents"`) {
		t.Fatal(expanded.Body.String())
	}
	if ancestors := call(member, "GET", "/embeds/"+smartLink.ID+"/ancestors", nil, 200); !strings.Contains(ancestors.Body.String(), `"id":"`+rootFolder.ID+`","type":"folder"`) || !strings.Contains(ancestors.Body.String(), `"id":"`+childFolder.ID+`","type":"folder"`) {
		t.Fatal(ancestors.Body.String())
	}
	call(member, "GET", "/embeds/"+smartLink.ID+"/direct-children?sort=title", nil, 200)
	call(member, "GET", "/embeds/"+smartLink.ID+"/descendants?depth=2", nil, 200)
	call(member, "GET", "/embeds/"+smartLink.ID+"/operations", nil, 200)
	linkPropertyResponse := call(member, "POST", "/embeds/"+smartLink.ID+"/properties", map[string]any{"key": "display", "value": "card"}, 200)
	var linkProperty models.WikiContentProperty
	if err := json.Unmarshal(linkPropertyResponse.Body.Bytes(), &linkProperty); err != nil || linkProperty.Version.Number != 1 {
		t.Fatalf("unexpected Smart Link property: %+v %v", linkProperty, err)
	}
	call(member, "GET", "/embeds/"+smartLink.ID+"/properties?key=display", nil, 200)
	call(member, "GET", "/embeds/"+smartLink.ID+"/properties/"+linkProperty.ID, nil, 200)
	call(member, "PUT", "/embeds/"+smartLink.ID+"/properties/"+linkProperty.ID, map[string]any{"key": "display", "value": "embed", "version": map[string]any{"number": 2, "message": "Show inline"}}, 200)
	call(member, "DELETE", "/embeds/"+smartLink.ID, nil, 400)
	call(member, "DELETE", "/embeds/"+smartLink.ID+"/properties/"+linkProperty.ID, nil, 204)
	call(member, "DELETE", "/folders/"+linkChild.ID, nil, 204)
	call(member, "DELETE", "/embeds/"+smartLink.ID, nil, 204)
	call(member, "GET", "/embeds/"+smartLink.ID, nil, 404)
	call(member, "POST", "/databases?private=maybe", map[string]any{"spaceId": public, "title": "Invalid database"}, 400)
	databaseResponse := call(member, "POST", "/databases", map[string]any{"spaceId": public, "title": "Service catalog", "parentId": childFolder.ID}, 200)
	var database models.WikiContent
	if err := json.Unmarshal(databaseResponse.Body.Bytes(), &database); err != nil || database.Type != "database" || database.ParentType != "folder" {
		t.Fatalf("unexpected database: %+v %v", database, err)
	}
	databaseChildResponse := call(member, "POST", "/folders", map[string]any{"spaceId": public, "title": "Catalog guidance", "parentId": database.ID}, 200)
	var databaseChild models.WikiContent
	if err := json.Unmarshal(databaseChildResponse.Body.Bytes(), &databaseChild); err != nil || databaseChild.ParentType != "database" {
		t.Fatalf("unexpected database child: %+v %v", databaseChild, err)
	}
	if expanded := call(member, "GET", "/databases/"+database.ID+"?include-direct-children=true&include-operations=true&include-properties=true&include-collaborators=true", nil, 200); !strings.Contains(expanded.Body.String(), "Catalog guidance") || !strings.Contains(expanded.Body.String(), `"targetType":"database"`) {
		t.Fatal(expanded.Body.String())
	}
	if ancestors := call(member, "GET", "/databases/"+database.ID+"/ancestors", nil, 200); !strings.Contains(ancestors.Body.String(), `"id":"`+childFolder.ID+`","type":"folder"`) {
		t.Fatal(ancestors.Body.String())
	}
	call(member, "GET", "/databases/"+database.ID+"/direct-children?sort=title", nil, 200)
	call(member, "GET", "/databases/"+database.ID+"/descendants?depth=2", nil, 200)
	call(member, "GET", "/databases/"+database.ID+"/operations", nil, 200)
	call(member, "GET", "/databases/"+database.ID+"/classification-level", nil, 404)
	call(member, "PUT", "/databases/"+database.ID+"/classification-level", map[string]any{"id": "unknown", "status": "current"}, 400)
	call(member, "PUT", "/databases/"+database.ID+"/classification-level", map[string]any{"id": "confidential", "status": "current"}, 204)
	if level := call(member, "GET", "/databases/"+database.ID+"/classification-level", nil, 200); !strings.Contains(level.Body.String(), `"name":"Confidential"`) || !strings.Contains(level.Body.String(), `"color":"ORANGE"`) {
		t.Fatal(level.Body.String())
	}
	call(member, "POST", "/databases/"+database.ID+"/classification-level/reset", map[string]any{"status": "current"}, 204)
	call(member, "GET", "/databases/"+database.ID+"/classification-level", nil, 404)
	databasePropertyResponse := call(member, "POST", "/databases/"+database.ID+"/properties", map[string]any{"key": "schema", "value": map[string]any{"version": 1}}, 200)
	var databaseProperty models.WikiContentProperty
	if err := json.Unmarshal(databasePropertyResponse.Body.Bytes(), &databaseProperty); err != nil {
		t.Fatal(err)
	}
	call(member, "GET", "/databases/"+database.ID+"/properties?key=schema", nil, 200)
	call(member, "GET", "/databases/"+database.ID+"/properties/"+databaseProperty.ID, nil, 200)
	call(member, "PUT", "/databases/"+database.ID+"/properties/"+databaseProperty.ID, map[string]any{"key": "schema", "value": map[string]any{"version": 2}, "version": map[string]any{"number": 2, "message": "Expanded fields"}}, 200)
	call(member, "DELETE", "/databases/"+database.ID, nil, 400)
	call(member, "DELETE", "/databases/"+database.ID+"/properties/"+databaseProperty.ID, nil, 204)
	call(member, "DELETE", "/folders/"+databaseChild.ID, nil, 204)
	call(member, "DELETE", "/databases/"+database.ID, nil, 204)
	call(member, "GET", "/databases/"+database.ID, nil, 404)
	privateDatabaseResponse := call(actor, "POST", "/databases?private=true", map[string]any{"spaceId": public, "title": "Private database", "parentId": page.ID}, 200)
	var privateDatabase models.WikiContent
	if err := json.Unmarshal(privateDatabaseResponse.Body.Bytes(), &privateDatabase); err != nil || !privateDatabase.Private {
		t.Fatalf("unexpected private database: %+v %v", privateDatabase, err)
	}
	call(actor, "POST", "/databases/"+privateDatabase.ID+"/properties", map[string]any{"key": "private-database-property", "value": true}, 200)
	call(member, "GET", "/databases/"+privateDatabase.ID, nil, 404)
	privateDatabaseChildResponse := call(actor, "POST", "/folders", map[string]any{"spaceId": public, "title": "Private database child", "parentId": privateDatabase.ID}, 200)
	var privateDatabaseChild models.WikiContent
	if err := json.Unmarshal(privateDatabaseChildResponse.Body.Bytes(), &privateDatabaseChild); err != nil {
		t.Fatal(err)
	}
	call(member, "GET", "/folders/"+privateDatabaseChild.ID, nil, 404)
	call(member, "POST", "/whiteboards?private=sometimes", map[string]any{"spaceId": public, "title": "Invalid private"}, 400)
	call(member, "POST", "/whiteboards", map[string]any{"spaceId": public, "title": "Invalid template", "templateKey": "unknown"}, 400)
	call(member, "POST", "/whiteboards", map[string]any{"spaceId": public, "title": "Invalid locale", "locale": "en-US"}, 400)
	whiteboardResponse := call(member, "POST", "/whiteboards", map[string]any{"spaceId": public, "title": "Incident review canvas", "parentId": childFolder.ID, "templateKey": "incident-postmortem", "locale": "en-US"}, 200)
	var whiteboard models.WikiContent
	if err := json.Unmarshal(whiteboardResponse.Body.Bytes(), &whiteboard); err != nil || whiteboard.Type != "whiteboard" || whiteboard.ParentType != "folder" {
		t.Fatalf("unexpected whiteboard: %+v %v", whiteboard, err)
	}
	storedWhiteboard, err := st.WikiContent(ctx, ws, member, whiteboard.ID, "whiteboard")
	if err != nil || storedWhiteboard.TemplateKey != "incident-postmortem" || storedWhiteboard.Locale != "en-US" {
		t.Fatalf("unexpected stored whiteboard template: %+v %v", storedWhiteboard, err)
	}
	whiteboardChildResponse := call(member, "POST", "/embeds", map[string]any{"spaceId": public, "title": "Canvas evidence", "parentId": whiteboard.ID, "embedUrl": "https://example.test/canvas-evidence"}, 200)
	var whiteboardChild models.WikiContent
	if err := json.Unmarshal(whiteboardChildResponse.Body.Bytes(), &whiteboardChild); err != nil || whiteboardChild.ParentType != "whiteboard" {
		t.Fatalf("unexpected whiteboard child: %+v %v", whiteboardChild, err)
	}
	if expanded := call(member, "GET", "/whiteboards/"+whiteboard.ID+"?include-direct-children=true&include-operations=true&include-properties=true&include-collaborators=true", nil, 200); !strings.Contains(expanded.Body.String(), "Canvas evidence") || !strings.Contains(expanded.Body.String(), `"targetType":"whiteboard"`) || !strings.Contains(expanded.Body.String(), `"editui"`) {
		t.Fatal(expanded.Body.String())
	}
	if ancestors := call(member, "GET", "/whiteboards/"+whiteboard.ID+"/ancestors", nil, 200); !strings.Contains(ancestors.Body.String(), `"id":"`+childFolder.ID+`","type":"folder"`) {
		t.Fatal(ancestors.Body.String())
	}
	call(member, "GET", "/whiteboards/"+whiteboard.ID+"/direct-children?sort=title", nil, 200)
	call(member, "GET", "/whiteboards/"+whiteboard.ID+"/descendants?depth=2", nil, 200)
	call(member, "GET", "/whiteboards/"+whiteboard.ID+"/operations", nil, 200)
	call(member, "GET", "/whiteboards/"+whiteboard.ID+"/classification-level", nil, 404)
	call(member, "PUT", "/whiteboards/"+whiteboard.ID+"/classification-level", map[string]any{"id": "restricted", "status": "current"}, 204)
	if level := call(member, "GET", "/whiteboards/"+whiteboard.ID+"/classification-level", nil, 200); !strings.Contains(level.Body.String(), `"name":"Restricted"`) {
		t.Fatal(level.Body.String())
	}
	call(member, "POST", "/whiteboards/"+whiteboard.ID+"/classification-level/reset", map[string]any{"status": "current"}, 204)
	whiteboardPropertyResponse := call(member, "POST", "/whiteboards/"+whiteboard.ID+"/properties", map[string]any{"key": "canvas", "value": map[string]any{"nodes": 3}}, 200)
	var whiteboardProperty models.WikiContentProperty
	if err := json.Unmarshal(whiteboardPropertyResponse.Body.Bytes(), &whiteboardProperty); err != nil {
		t.Fatal(err)
	}
	call(member, "GET", "/whiteboards/"+whiteboard.ID+"/properties?key=canvas", nil, 200)
	call(member, "GET", "/whiteboards/"+whiteboard.ID+"/properties/"+whiteboardProperty.ID, nil, 200)
	call(member, "PUT", "/whiteboards/"+whiteboard.ID+"/properties/"+whiteboardProperty.ID, map[string]any{"key": "canvas", "value": map[string]any{"nodes": 4}, "version": map[string]any{"number": 2}}, 200)
	call(member, "DELETE", "/whiteboards/"+whiteboard.ID, nil, 400)
	call(member, "DELETE", "/whiteboards/"+whiteboard.ID+"/properties/"+whiteboardProperty.ID, nil, 204)
	call(member, "DELETE", "/embeds/"+whiteboardChild.ID, nil, 204)
	call(member, "DELETE", "/whiteboards/"+whiteboard.ID, nil, 204)
	call(member, "GET", "/whiteboards/"+whiteboard.ID, nil, 404)
	privateWhiteboardResponse := call(actor, "POST", "/whiteboards?private=true", map[string]any{"spaceId": public, "title": "Private whiteboard", "parentId": page.ID, "templateKey": "workflow", "locale": "en-GB"}, 200)
	var privateWhiteboard models.WikiContent
	if err := json.Unmarshal(privateWhiteboardResponse.Body.Bytes(), &privateWhiteboard); err != nil {
		t.Fatal(err)
	}
	call(member, "GET", "/whiteboards/"+privateWhiteboard.ID, nil, 404)
	if expanded := call(member, "GET", "/folders/"+rootFolder.ID+"?include-direct-children=true&include-operations=true&include-properties=true&include-collaborators=true", nil, 200); !strings.Contains(expanded.Body.String(), "Database runbooks") || !strings.Contains(expanded.Body.String(), `"operation":"delete"`) {
		t.Fatal(expanded.Body.String())
	}
	if ancestors := call(member, "GET", "/folders/"+childFolder.ID+"/ancestors?limit=25", nil, 200); !strings.Contains(ancestors.Body.String(), `"id":"`+page.ID+`","type":"page"`) || !strings.Contains(ancestors.Body.String(), `"id":"`+rootFolder.ID+`","type":"folder"`) {
		t.Fatal(ancestors.Body.String())
	}
	if descendants := call(member, "GET", "/folders/"+rootFolder.ID+"/descendants?depth=2&limit=25", nil, 200); !strings.Contains(descendants.Body.String(), `"depth":1`) || !strings.Contains(descendants.Body.String(), "Database runbooks") {
		t.Fatal(descendants.Body.String())
	}
	folderPropertyResponse := call(member, "POST", "/folders/"+rootFolder.ID+"/properties", map[string]any{"key": "audience", "value": map[string]any{"team": "platform"}}, 200)
	var folderProperty models.WikiContentProperty
	if err := json.Unmarshal(folderPropertyResponse.Body.Bytes(), &folderProperty); err != nil || folderProperty.Version.Number != 1 {
		t.Fatalf("unexpected folder property: %+v %v", folderProperty, err)
	}
	call(member, "POST", "/folders/"+rootFolder.ID+"/properties", map[string]any{"key": "audience", "value": true}, 400)
	call(member, "GET", "/folders/"+rootFolder.ID+"/properties?key=audience&sort=-key", nil, 200)
	call(member, "GET", "/folders/"+rootFolder.ID+"/properties/"+folderProperty.ID, nil, 200)
	call(member, "PUT", "/folders/"+rootFolder.ID+"/properties/"+folderProperty.ID, map[string]any{"key": "audience", "value": map[string]any{"team": "operations"}, "version": map[string]any{"number": 3}}, 409)
	call(member, "PUT", "/folders/"+rootFolder.ID+"/properties/"+folderProperty.ID, map[string]any{"key": "audience", "value": map[string]any{"team": "operations"}, "version": map[string]any{"number": 2, "message": "Broadened ownership"}}, 200)
	call(member, "DELETE", "/folders/"+rootFolder.ID, nil, 400)
	call(member, "DELETE", "/folders/"+rootFolder.ID+"/properties/"+folderProperty.ID, nil, 204)
	call(member, "DELETE", "/folders/"+childFolder.ID, nil, 204)
	call(member, "DELETE", "/folders/"+rootFolder.ID, nil, 204)
	call(member, "GET", "/folders/"+rootFolder.ID, nil, 404)
	call(member, "GET", "/folders/"+rootFolder.ID+"/properties", nil, 404)
	secretFolderResponse := call(actor, "POST", "/folders", map[string]any{"spaceId": private, "title": "Private folder", "parentId": secret.ID}, 200)
	var secretFolder models.WikiContent
	if err := json.Unmarshal(secretFolderResponse.Body.Bytes(), &secretFolder); err != nil {
		t.Fatal(err)
	}
	call(actor, "POST", "/folders/"+secretFolder.ID+"/properties", map[string]any{"key": "secret-folder-property", "value": true}, 200)
	call(member, "GET", "/folders/"+secretFolder.ID, nil, 404)
	secretLinkResponse := call(actor, "POST", "/embeds", map[string]any{"spaceId": private, "title": "Private Smart Link", "parentId": secretFolder.ID, "embedUrl": "https://private.example.test/secret-link"}, 200)
	var secretLink models.WikiContent
	if err := json.Unmarshal(secretLinkResponse.Body.Bytes(), &secretLink); err != nil {
		t.Fatal(err)
	}
	call(member, "GET", "/embeds/"+secretLink.ID, nil, 404)
	secretComment := call(actor, "POST", "/footer-comments", map[string]any{"pageId": secret.ID, "body": models.WikiBody{Representation: "storage", Value: "<p>Private launch phrase</p>"}}, 201)
	if secretComment.Header().Get("Location") == "" {
		t.Fatal("created footer comment omitted Location")
	}
	var secretCommentBean struct{ ID string }
	if err := json.Unmarshal(secretComment.Body.Bytes(), &secretCommentBean); err != nil {
		t.Fatal(err)
	}
	if err := st.SetWikiFooterCommentLike(ctx, ws, actor, secretCommentBean.ID, true); err != nil {
		t.Fatal(err)
	}
	call(member, "GET", "/pages/"+draft.ID+"?status=draft", nil, 404)
	call(member, "GET", "/pages/"+secret.ID, nil, 404)
	call(member, "POST", "/pages", map[string]any{"spaceId": private, "title": "Intrusion", "body": models.WikiBody{Representation: "storage", Value: "<p>x</p>"}}, 404)
	call(actor, "POST", "/pages", map[string]any{"spaceId": public, "title": "Unsafe", "body": models.WikiBody{Representation: "storage", Value: "<script>alert(1)</script>"}}, 400)
	call(actor, "GET", "/pages?body-format=atlas_doc_format", nil, 400)
	update := map[string]any{"id": page.ID, "spaceId": public, "title": "Release guide updated", "status": "current", "body": models.WikiBody{Representation: "storage", Value: "<h2>Ready</h2>"}, "version": map[string]any{"number": 2, "message": "Updated release instructions"}}
	call(member, "PUT", "/pages/"+page.ID, update, 200)
	call(actor, "PUT", "/pages/"+page.ID, update, 409)
	labelsResponse := callV1(member, "POST", "/content/"+page.ID+"/label", []map[string]string{{"prefix": "global", "name": "release-ready"}, {"prefix": "global", "name": "handbook"}, {"prefix": "team", "name": "engineering-content"}}, 200)
	var labels struct{ Results []models.WikiLabel }
	if err := json.Unmarshal(labelsResponse.Body.Bytes(), &labels); err != nil {
		t.Fatal(err)
	}
	if len(labels.Results) != 3 {
		t.Fatalf("unexpected v1 labels: %s", labelsResponse.Body.String())
	}
	var releaseLabelID string
	for _, label := range labels.Results {
		if label.Name == "release-ready" {
			releaseLabelID = label.ID
		}
	}
	if releaseLabelID == "" {
		t.Fatal("release label id missing")
	}
	pageLabels := call(member, "GET", "/pages/"+page.ID+"/labels?sort=name", nil, 200)
	if !strings.Contains(pageLabels.Body.String(), "release-ready") || !strings.Contains(pageLabels.Body.String(), "handbook") {
		t.Fatal(pageLabels.Body.String())
	}
	globalLabels := call(member, "GET", "/labels?prefix=global&label-id="+releaseLabelID, nil, 200)
	if !strings.Contains(globalLabels.Body.String(), "release-ready") || strings.Contains(globalLabels.Body.String(), "handbook") {
		t.Fatal(globalLabels.Body.String())
	}
	labeledPages := call(member, "GET", "/labels/"+releaseLabelID+"/pages?space-id="+public+"&body-format=storage&sort=-title", nil, 200)
	if !strings.Contains(labeledPages.Body.String(), "Release guide updated") {
		t.Fatal(labeledPages.Body.String())
	}
	legacyContent := callV1(member, "GET", "/label?name=release-ready&type=page", nil, 200)
	if !strings.Contains(legacyContent.Body.String(), "Release guide updated") {
		t.Fatal(legacyContent.Body.String())
	}
	watchHead, err := st.Head(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	callV1(member, "POST", "/user/watch/content/"+page.ID, nil, 204)
	memberWatchActions, err := st.ActionsSince(ctx, ws, member, watchHead, 20)
	if err != nil || len(memberWatchActions) != 1 || memberWatchActions[0].EntityType != store.EntityWikiWatch {
		t.Fatalf("watcher did not receive private watch action: %+v %v", memberWatchActions, err)
	}
	actorWatchActions, err := st.ActionsSince(ctx, ws, actor, watchHead, 20)
	if err != nil || len(actorWatchActions) != 0 {
		t.Fatalf("private watch action leaked to administrator: %+v %v", actorWatchActions, err)
	}
	if status := callV1(member, "GET", "/user/watch/content/"+page.ID, nil, 200); !strings.Contains(status.Body.String(), `"watching":true`) {
		t.Fatal(status.Body.String())
	}
	callV1(member, "POST", "/user/watch/label/release-ready", nil, 403)
	callV1NoCheck(member, "POST", "/user/watch/label/release-ready", nil, 204)
	callV1NoCheck(member, "POST", "/user/watch/space/PUBLIC", nil, 204)
	if status := callV1(member, "GET", "/user/watch/label/release-ready", nil, 200); !strings.Contains(status.Body.String(), `"watching":true`) {
		t.Fatal(status.Body.String())
	}
	if status := callV1(member, "GET", "/user/watch/space/PUBLIC", nil, 200); !strings.Contains(status.Body.String(), `"watching":true`) {
		t.Fatal(status.Body.String())
	}
	exec(`UPDATE users SET username='ana-watch' WHERE id=$1`, member)
	if status := callV1(actor, "GET", "/user/watch/content/"+page.ID+"?username=ana-watch", nil, 200); !strings.Contains(status.Body.String(), `"watching":true`) {
		t.Fatal(status.Body.String())
	}
	callV1(actor, "POST", "/user/watch/content/"+page.ID+"?accountId="+admin, nil, 204)
	callV1(member, "GET", "/user/watch/content/"+page.ID+"?accountId="+actor, nil, 403)
	pageWatches := callV1(actor, "GET", "/content/"+page.ID+"/notification/child-created?limit=1", nil, 200)
	if !strings.Contains(pageWatches.Body.String(), `"type":"page"`) || !strings.Contains(pageWatches.Body.String(), `"size":1`) || !strings.Contains(pageWatches.Body.String(), `"contentId":`+page.ID) || !strings.Contains(pageWatches.Body.String(), `"profilePicture"`) {
		t.Fatal(pageWatches.Body.String())
	}
	spaceWatches := callV1(actor, "GET", "/content/"+page.ID+"/notification/created", nil, 200)
	if !strings.Contains(spaceWatches.Body.String(), member) {
		t.Fatal(spaceWatches.Body.String())
	}
	spaceWatchList := callV1(actor, "GET", "/space/PUBLIC/watch", nil, 200)
	if !strings.Contains(spaceWatchList.Body.String(), `"spaceKey":"PUBLIC"`) {
		t.Fatal(spaceWatchList.Body.String())
	}
	childResponse := call(actor, "POST", "/pages", map[string]any{"spaceId": public, "parentId": page.ID, "title": "Watched child", "status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>Draft release details</p>"}}, 200)
	var child struct {
		ID      string
		Version models.WikiVersion
	}
	if err := json.Unmarshal(childResponse.Body.Bytes(), &child); err != nil || child.ID == "" {
		t.Fatalf("unexpected watched child: %+v %v", child, err)
	}
	if notifications, err := st.NotificationsByUser(ctx, ws, member, 20); err != nil || len(notifications) != 1 || notifications[0].EntityID != child.ID {
		t.Fatalf("expected one deduplicated child notification: %+v %v", notifications, err)
	}
	callV1(actor, "POST", "/content/"+child.ID+"/label", map[string]string{"prefix": "global", "name": "release-ready"}, 200)
	call(actor, "PUT", "/pages/"+child.ID, map[string]any{"id": child.ID, "spaceId": public, "parentId": page.ID, "title": "Watched child updated", "status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>Ready</p>"}, "version": map[string]any{"number": 2}}, 200)
	if notifications, err := st.NotificationsByUser(ctx, ws, member, 20); err != nil || len(notifications) != 2 {
		t.Fatalf("expected label and space watches to deduplicate: %+v %v", notifications, err)
	}
	childPage, err := st.WikiPage(ctx, ws, actor, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	childPage.Version.Number++
	childPage.Version.MinorEdit = true
	childPage.Body.Value = "<p>Spelling corrected</p>"
	if _, err := h.Commands.SaveWikiPage(ctx, ws, actor, *childPage); err != nil {
		t.Fatal(err)
	}
	if notifications, err := st.NotificationsByUser(ctx, ws, member, 20); err != nil || len(notifications) != 2 {
		t.Fatalf("minor edit generated a notification: %+v %v", notifications, err)
	}
	callV1(member, "DELETE", "/user/watch/content/"+page.ID, nil, 403)
	callV1NoCheck(member, "DELETE", "/user/watch/content/"+page.ID, nil, 204)
	callV1(member, "DELETE", "/user/watch/label/release-ready", nil, 204)
	callV1(member, "DELETE", "/user/watch/space/PUBLIC", nil, 204)
	call(actor, "DELETE", "/pages/"+child.ID, nil, 204)
	hierarchyChildResponse := call(actor, "POST", "/pages", map[string]any{"spaceId": public, "parentId": page.ID, "title": "Hierarchy child", "status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>Child</p>"}}, 200)
	var hierarchyChild models.WikiPage
	if err := json.Unmarshal(hierarchyChildResponse.Body.Bytes(), &hierarchyChild); err != nil {
		t.Fatal(err)
	}
	hierarchyGrandchildResponse := call(actor, "POST", "/pages", map[string]any{"spaceId": public, "parentId": hierarchyChild.ID, "title": "Hierarchy grandchild", "status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>Grandchild</p>"}}, 200)
	var hierarchyGrandchild models.WikiPage
	if err := json.Unmarshal(hierarchyGrandchildResponse.Body.Bytes(), &hierarchyGrandchild); err != nil {
		t.Fatal(err)
	}
	hierarchyChildren := call(member, "GET", "/pages/"+page.ID+"/children?sort=-id&limit=1", nil, 200)
	if !strings.Contains(hierarchyChildren.Body.String(), "Hierarchy child") || strings.Contains(hierarchyChildren.Body.String(), "Hierarchy grandchild") {
		t.Fatal(hierarchyChildren.Body.String())
	}
	directChildren := call(member, "GET", "/pages/"+page.ID+"/direct-children?sort=title", nil, 200)
	if !strings.Contains(directChildren.Body.String(), `"type":"page"`) {
		t.Fatal(directChildren.Body.String())
	}
	descendants := call(member, "GET", "/pages/"+page.ID+"/descendants?depth=2", nil, 200)
	if !strings.Contains(descendants.Body.String(), `"depth":1`) || !strings.Contains(descendants.Body.String(), `"depth":2`) {
		t.Fatal(descendants.Body.String())
	}
	ancestors := call(member, "GET", "/pages/"+hierarchyGrandchild.ID+"/ancestors?limit=2", nil, 200)
	if rootIndex, childIndex := strings.Index(ancestors.Body.String(), `"id":"`+page.ID+`"`), strings.Index(ancestors.Body.String(), `"id":"`+hierarchyChild.ID+`"`); rootIndex < 0 || childIndex < rootIndex {
		t.Fatal(ancestors.Body.String())
	}
	v1Descendants := callV1(member, "GET", "/content/"+page.ID+"/descendant", nil, 200)
	if !strings.Contains(v1Descendants.Body.String(), "Hierarchy grandchild") {
		t.Fatal(v1Descendants.Body.String())
	}
	callV1(member, "GET", "/content/"+page.ID+"/descendant/page?depth=root&start=0&limit=1&expand=page", nil, 200)
	callV1(member, "GET", "/content/"+page.ID+"/descendant/comment?depth=all", nil, 200)
	callV1(member, "GET", "/content/"+page.ID+"/descendant/page?depth=101", nil, 400)
	inlineResponse := call(member, "POST", "/inline-comments", map[string]any{"pageId": page.ID, "body": models.WikiBody{Representation: "storage", Value: "<p>Is this ready?</p>"}, "inlineCommentProperties": map[string]any{"textSelection": "Ready", "textSelectionMatchCount": 1, "textSelectionMatchIndex": 0}}, 201)
	var inline models.WikiFooterComment
	if err := json.Unmarshal(inlineResponse.Body.Bytes(), &inline); err != nil || inline.ID == "" || !strings.Contains(inlineResponse.Body.String(), `"resolutionStatus":"open"`) {
		t.Fatalf("unexpected inline comment: %+v %v %s", inline, err, inlineResponse.Body.String())
	}
	call(member, "POST", "/inline-comments", map[string]any{"pageId": page.ID, "body": models.WikiBody{Representation: "storage", Value: "<p>Bad anchor</p>"}, "inlineCommentProperties": map[string]any{"textSelection": "Missing", "textSelectionMatchCount": 1, "textSelectionMatchIndex": 0}}, 400)
	inlineReplyResponse := call(actor, "POST", "/inline-comments", map[string]any{"parentCommentId": inline.ID, "body": models.WikiBody{Representation: "storage", Value: "<p>Yes.</p>"}}, 201)
	var inlineReply models.WikiFooterComment
	if err := json.Unmarshal(inlineReplyResponse.Body.Bytes(), &inlineReply); err != nil || inlineReply.ID == "" {
		t.Fatal(err)
	}
	call(member, "GET", "/pages/"+page.ID+"/inline-comments?body-format=storage&resolution-status=open", nil, 200)
	call(member, "GET", "/inline-comments?body-format=storage", nil, 200)
	exec(`INSERT INTO wiki_footer_comment_likes(comment_id,user_id) VALUES($1::bigint,$2)`, inline.ID, actor)
	expandedInline := call(member, "GET", "/inline-comments/"+inline.ID+"?body-format=storage&include-operations=true&include-likes=true&include-versions=true", nil, 200)
	if !strings.Contains(expandedInline.Body.String(), actor) || !strings.Contains(expandedInline.Body.String(), "inlineOriginalSelection") {
		t.Fatal(expandedInline.Body.String())
	}
	call(member, "GET", "/inline-comments/"+inline.ID+"/children?body-format=storage", nil, 200)
	call(member, "GET", "/inline-comments/"+inline.ID+"/operations", nil, 200)
	call(member, "GET", "/inline-comments/"+inline.ID+"/likes/count", nil, 200)
	call(member, "GET", "/inline-comments/"+inline.ID+"/likes/users", nil, 200)
	resolvedInline := call(member, "PUT", "/inline-comments/"+inline.ID, map[string]any{"version": map[string]any{"number": 2, "message": "Question answered"}, "resolved": true}, 200)
	if !strings.Contains(resolvedInline.Body.String(), `"resolutionStatus":"resolved"`) {
		t.Fatal(resolvedInline.Body.String())
	}
	call(member, "GET", "/inline-comments/"+inline.ID+"/versions?body-format=storage&sort=-modified-date", nil, 200)
	call(member, "GET", "/inline-comments/"+inline.ID+"/versions/1", nil, 200)
	call(actor, "DELETE", "/inline-comments/"+inlineReply.ID, nil, 204)
	wikiTask, err := h.Commands.CreateWikiTask(ctx, ws, actor, models.WikiTask{PageID: page.ID, AssignedTo: member, DueAt: "2030-01-02T00:00:00Z", Body: models.WikiBody{Representation: "storage", Value: "<p>Publish the release notes</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	blankTask, err := h.Commands.CreateWikiTask(ctx, ws, actor, models.WikiTask{PageID: page.ID, Body: models.WikiBody{Representation: "storage"}})
	if err != nil {
		t.Fatal(err)
	}
	taskFilters := []string{
		"body-format=storage",
		"status=incomplete", "task-id=" + wikiTask.ID, "space-id=" + public,
		"page-id=" + page.ID, "created-by=" + actor, "assigned-to=" + member,
		"created-at-from=0", "due-at-from=1893456000000", "due-at-to=1893628800000", "limit=1",
	}
	for i := range taskFilters {
		taskList := call(member, "GET", "/tasks?"+strings.Join(taskFilters[:i+1], "&"), nil, 200)
		if !strings.Contains(taskList.Body.String(), "Publish the release notes") || !strings.Contains(taskList.Body.String(), `"assignedTo":"`+member+`"`) {
			t.Fatalf("task filter %q returned %s", taskFilters[i], taskList.Body.String())
		}
	}
	nonBlankTasks := call(member, "GET", "/tasks?body-format=storage&include-blank-tasks=false&page-id="+page.ID, nil, 200)
	if strings.Contains(nonBlankTasks.Body.String(), `"id":"`+blankTask.ID+`"`) {
		t.Fatal(nonBlankTasks.Body.String())
	}
	call(member, "GET", "/tasks/"+wikiTask.ID+"?body-format=storage", nil, 200)
	completedTask := call(member, "PUT", "/tasks/"+wikiTask.ID+"?body-format=storage", map[string]string{"status": "complete"}, 200)
	if !strings.Contains(completedTask.Body.String(), `"status":"complete"`) || !strings.Contains(completedTask.Body.String(), `"completedBy":"`+member+`"`) {
		t.Fatal(completedTask.Body.String())
	}
	call(actor, "GET", "/tasks?status=complete&completed-by="+member+"&completed-at-from=0", nil, 200)
	call(actor, "PUT", "/tasks/"+wikiTask.ID, map[string]string{"status": "invalid"}, 400)
	call(actor, "GET", "/tasks?task-id=invalid", nil, 400)
	callV1(actor, "POST", "/content/"+secret.ID+"/label", map[string]string{"prefix": "global", "name": "secret-label"}, 200)
	if leaked := call(member, "GET", "/labels", nil, 200); strings.Contains(leaked.Body.String(), "secret-label") {
		t.Fatal("private page label leaked")
	}
	callV1(member, "POST", "/space/PUBLIC/label", []map[string]string{{"prefix": "team", "name": "engineering"}}, 403)
	callV1(actor, "POST", "/space/PUBLIC/label", []map[string]string{{"prefix": "team", "name": "engineering"}}, 200)
	spaceLabels := call(actor, "GET", "/spaces/"+public+"/labels?prefix=team", nil, 200)
	if !strings.Contains(spaceLabels.Body.String(), "engineering") {
		t.Fatal(spaceLabels.Body.String())
	}
	contentLabels := call(actor, "GET", "/spaces/"+public+"/content/labels?prefix=team", nil, 200)
	if !strings.Contains(contentLabels.Body.String(), "engineering-content") {
		t.Fatal(contentLabels.Body.String())
	}
	callV1(actor, "DELETE", "/space/PUBLIC/label?name=engineering&prefix=team", nil, 204)
	callV1(member, "DELETE", "/content/"+page.ID+"/label/handbook", nil, 204)
	callV1(member, "DELETE", "/content/"+page.ID+"/label?name=release-ready", nil, 204)
	if remaining := call(member, "GET", "/pages/"+page.ID+"/labels", nil, 200); strings.Contains(remaining.Body.String(), "release-ready") || strings.Contains(remaining.Body.String(), "handbook") {
		t.Fatal(remaining.Body.String())
	}
	upload := callV1Multipart(member, "POST", "/content/"+page.ID+"/child/attachment", "release.txt", "release one", "Initial release file", 200)
	var uploaded struct {
		Results []struct {
			ID      string
			Version models.WikiVersion
		}
	}
	if err := json.Unmarshal(upload.Body.Bytes(), &uploaded); err != nil || len(uploaded.Results) != 1 {
		t.Fatalf("unexpected attachment: %v %s", err, upload.Body.String())
	}
	attachmentID := uploaded.Results[0].ID
	call(member, "GET", "/pages/"+page.ID+"/attachments?filename=release.txt", nil, 200)
	call(member, "GET", "/attachments?mediaType=application/octet-stream", nil, 200)
	propertyResponse := call(member, "POST", "/attachments/"+attachmentID+"/properties", map[string]any{"key": "release-state", "value": map[string]any{"approved": false}}, 200)
	var attachmentProperty models.WikiAttachmentProperty
	if err := json.Unmarshal(propertyResponse.Body.Bytes(), &attachmentProperty); err != nil || attachmentProperty.ID == "" || attachmentProperty.Version.Number != 1 {
		t.Fatalf("unexpected attachment property: %+v %v", attachmentProperty, err)
	}
	call(member, "POST", "/attachments/"+attachmentID+"/properties", map[string]any{"key": "release-state", "value": true}, 400)
	propertiesResponse := call(member, "GET", "/attachments/"+attachmentID+"/properties?key=release-state&sort=-key", nil, 200)
	if !strings.Contains(propertiesResponse.Body.String(), `"approved":false`) {
		t.Fatal(propertiesResponse.Body.String())
	}
	call(member, "GET", "/attachments/"+attachmentID+"/properties/"+attachmentProperty.ID, nil, 200)
	call(member, "PUT", "/attachments/"+attachmentID+"/properties/"+attachmentProperty.ID, map[string]any{"key": "release-state", "value": map[string]any{"approved": true}, "version": map[string]any{"number": 3}}, 409)
	updatedProperty := call(member, "PUT", "/attachments/"+attachmentID+"/properties/"+attachmentProperty.ID, map[string]any{"key": "release-state", "value": map[string]any{"approved": true}, "version": map[string]any{"number": 2, "message": "Release approved"}}, 200)
	if !strings.Contains(updatedProperty.Body.String(), `"number":2`) || !strings.Contains(updatedProperty.Body.String(), `"approved":true`) {
		t.Fatal(updatedProperty.Body.String())
	}
	attachmentLabels, err := h.Commands.AddWikiAttachmentLabels(ctx, ws, member, attachmentID, []models.WikiLabel{{Prefix: "global", Name: "release-file"}})
	if err != nil || len(attachmentLabels) != 1 {
		t.Fatalf("unexpected attachment labels: %+v %v", attachmentLabels, err)
	}
	attachmentLabelResponse := call(member, "GET", "/attachments/"+attachmentID+"/labels?prefix=global&sort=-name", nil, 200)
	if !strings.Contains(attachmentLabelResponse.Body.String(), "release-file") {
		t.Fatal(attachmentLabelResponse.Body.String())
	}
	labeledAttachments := call(member, "GET", "/labels/"+attachmentLabels[0].ID+"/attachments?sort=-modified-date", nil, 200)
	if !strings.Contains(labeledAttachments.Body.String(), "release.txt") {
		t.Fatal(labeledAttachments.Body.String())
	}
	expandedAttachment := call(member, "GET", "/attachments/"+attachmentID+"?include-operations=true&include-versions=true&include-labels=true&include-properties=true", nil, 200)
	if !strings.Contains(expandedAttachment.Body.String(), "release-file") || !strings.Contains(expandedAttachment.Body.String(), "release-state") {
		t.Fatal(expandedAttachment.Body.String())
	}
	call(member, "GET", "/attachments/"+attachmentID+"/operations", nil, 200)
	attachmentCommentResponse := call(member, "POST", "/footer-comments", map[string]any{"attachmentId": attachmentID, "body": models.WikiBody{Representation: "storage", Value: "<p>Check the attached release plan.</p>"}}, 201)
	var attachmentComment models.WikiFooterComment
	if err := json.Unmarshal(attachmentCommentResponse.Body.Bytes(), &attachmentComment); err != nil || attachmentComment.ID == "" || attachmentComment.AttachmentID != attachmentID {
		t.Fatalf("unexpected attachment comment: %+v %v", attachmentComment, err)
	}
	attachmentComments := call(member, "GET", "/attachments/"+attachmentID+"/footer-comments?body-format=storage&sort=-modified-date&version=1", nil, 200)
	if !strings.Contains(attachmentComments.Body.String(), "Check the attached release plan") || strings.Contains(attachmentComments.Body.String(), `"pageId"`) {
		t.Fatal(attachmentComments.Body.String())
	}
	attachmentReply := call(actor, "POST", "/footer-comments", map[string]any{"parentCommentId": attachmentComment.ID, "body": models.WikiBody{Representation: "storage", Value: "<p>Attachment approved.</p>"}}, 201)
	if !strings.Contains(attachmentReply.Body.String(), `"attachmentId":"`+attachmentID+`"`) {
		t.Fatal(attachmentReply.Body.String())
	}
	attachmentChildren := call(member, "GET", "/footer-comments/"+attachmentComment.ID+"/children?body-format=storage", nil, 200)
	if !strings.Contains(attachmentChildren.Body.String(), "Attachment approved") {
		t.Fatal(attachmentChildren.Body.String())
	}
	pngBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	imageUpload := callV1Multipart(member, "POST", "/content/"+page.ID+"/child/attachment", "release-dot.png", string(pngBytes), "Release marker", 200)
	var uploadedImage struct{ Results []models.WikiAttachment }
	if err := json.Unmarshal(imageUpload.Body.Bytes(), &uploadedImage); err != nil || len(uploadedImage.Results) != 1 {
		t.Fatalf("unexpected image attachment: %+v %v", uploadedImage, err)
	}
	thumbnailRedirect := call(member, "GET", "/attachments/"+uploadedImage.Results[0].ID+"/thumbnail/download?width=3&height=2&version=1", nil, 302)
	thumbnailRequest := httptest.NewRequest(http.MethodGet, thumbnailRedirect.Header().Get("Location"), nil)
	thumbnailRequest.SetBasicAuth(member+"@example.test", member)
	thumbnailResponse := httptest.NewRecorder()
	(&ThumbnailHandler{Handler: h}).ServeHTTP(thumbnailResponse, thumbnailRequest)
	thumbnailConfig, err := png.DecodeConfig(bytes.NewReader(thumbnailResponse.Body.Bytes()))
	if err != nil || thumbnailResponse.Code != 200 || thumbnailConfig.Width != 3 || thumbnailConfig.Height != 2 {
		t.Fatalf("unexpected thumbnail: status=%d config=%+v err=%v body=%s", thumbnailResponse.Code, thumbnailConfig, err, thumbnailResponse.Body.String())
	}
	callV1Multipart(member, "POST", "/content/"+page.ID+"/child/attachment/"+attachmentID+"/data", "release.txt", "release two", "Updated release file", 200)
	versionsResponse := call(member, "GET", "/attachments/"+attachmentID+"/versions", nil, 200)
	if !strings.Contains(versionsResponse.Body.String(), `"number":2`) {
		t.Fatal(versionsResponse.Body.String())
	}
	call(member, "GET", "/attachments/"+attachmentID+"/versions/1", nil, 200)
	properties := map[string]any{"id": attachmentID, "type": "attachment", "title": "release-notes.txt", "metadata": map[string]string{"mediaType": "text/plain"}, "version": map[string]any{"number": 3, "message": "Renamed file"}}
	callV1(member, "PUT", "/content/"+page.ID+"/child/attachment/"+attachmentID, properties, 200)
	redirect := callV1(member, "GET", "/content/"+page.ID+"/child/attachment/"+attachmentID+"/download?version=2", nil, 302)
	downloadRequest := httptest.NewRequest(http.MethodGet, redirect.Header().Get("Location"), nil)
	downloadRequest.SetBasicAuth(member+"@example.test", member)
	downloadResponse := httptest.NewRecorder()
	(&DownloadHandler{Handler: h}).ServeHTTP(downloadResponse, downloadRequest)
	if downloadResponse.Code != 200 || downloadResponse.Body.String() != "release two" {
		t.Fatalf("unexpected attachment download: %d %s", downloadResponse.Code, downloadResponse.Body.String())
	}
	call(member, "DELETE", "/attachments/"+attachmentID+"/properties/"+attachmentProperty.ID, nil, 204)
	call(member, "GET", "/attachments/"+attachmentID+"/properties/"+attachmentProperty.ID, nil, 404)
	call(member, "DELETE", "/attachments/"+attachmentID, nil, 204)
	call(member, "GET", "/attachments/"+attachmentID, nil, 404)
	call(member, "GET", "/footer-comments/"+attachmentComment.ID, nil, 404)
	topResponse := call(member, "POST", "/footer-comments", map[string]any{"pageId": page.ID, "body": map[string]any{"storage": models.WikiBody{Representation: "storage", Value: "<p>Ready for review</p>"}}}, 201)
	var top struct {
		ID      string
		PageID  string
		Version models.WikiVersion
	}
	if err := json.Unmarshal(topResponse.Body.Bytes(), &top); err != nil {
		t.Fatal(err)
	}
	if top.ID == "" || top.PageID != page.ID || top.Version.Number != 1 {
		t.Fatalf("unexpected footer comment: %+v", top)
	}
	replyResponse := call(actor, "POST", "/footer-comments", map[string]any{"parentCommentId": top.ID, "body": models.WikiBody{Representation: "storage", Value: "<p>Approval recorded</p>"}}, 201)
	var reply struct {
		ID, ParentCommentID string
	}
	if err := json.Unmarshal(replyResponse.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.ID == "" || reply.ParentCommentID != top.ID {
		t.Fatalf("unexpected footer reply: %+v", reply)
	}
	pageComments := call(actor, "GET", "/pages/"+page.ID+"/footer-comments?body-format=storage&sort=-created-date", nil, 200)
	if !strings.Contains(pageComments.Body.String(), "Ready for review") || strings.Contains(pageComments.Body.String(), "Approval recorded") || strings.Contains(pageComments.Body.String(), "Is this ready?") {
		t.Fatalf("page collection should contain top-level comments only: %s", pageComments.Body.String())
	}
	children := call(member, "GET", "/footer-comments/"+top.ID+"/children?body-format=storage", nil, 200)
	if !strings.Contains(children.Body.String(), "Approval recorded") {
		t.Fatal(children.Body.String())
	}
	allVisible := call(member, "GET", "/footer-comments?body-format=storage&sort=modified-date", nil, 200)
	if !strings.Contains(allVisible.Body.String(), "Ready for review") || !strings.Contains(allVisible.Body.String(), "Approval recorded") || strings.Contains(allVisible.Body.String(), "Private launch phrase") {
		t.Fatalf("visible footer comment collection is incorrect: %s", allVisible.Body.String())
	}
	call(member, "PUT", "/footer-comments/"+reply.ID, map[string]any{"version": map[string]int{"number": 2}, "body": models.WikiBody{Representation: "storage", Value: "<p>Changed by another user</p>"}}, 404)
	commentUpdate := map[string]any{"version": map[string]any{"number": 2, "message": "Clarified review state"}, "body": models.WikiBody{Representation: "storage", Value: "<p>Ready for final review</p>"}}
	call(actor, "PUT", "/footer-comments/"+top.ID, commentUpdate, 200)
	call(member, "PUT", "/footer-comments/"+top.ID, commentUpdate, 409)
	call(member, "PUT", "/footer-comments/"+top.ID, map[string]any{"version": map[string]int{"number": 3}, "body": models.WikiBody{Representation: "storage", Value: "<script>unsafe</script>"}}, 400)
	gotComment := call(actor, "GET", "/footer-comments/"+top.ID+"?body-format=storage", nil, 200)
	if !strings.Contains(gotComment.Body.String(), "Ready for final review") || !strings.Contains(gotComment.Body.String(), "Clarified review state") {
		t.Fatal(gotComment.Body.String())
	}
	historical := call(member, "GET", "/footer-comments/"+top.ID+"?body-format=storage&version=1", nil, 200)
	if !strings.Contains(historical.Body.String(), "Ready for review") || strings.Contains(historical.Body.String(), "final review") {
		t.Fatal(historical.Body.String())
	}
	commentVersions := call(actor, "GET", "/footer-comments/"+top.ID+"/versions?body-format=storage&sort=-modified-date", nil, 200)
	if !strings.Contains(commentVersions.Body.String(), "Clarified review state") || !strings.Contains(commentVersions.Body.String(), "Ready for review") {
		t.Fatal(commentVersions.Body.String())
	}
	versionDetails := call(actor, "GET", "/footer-comments/"+top.ID+"/versions/2", nil, 200)
	if !strings.Contains(versionDetails.Body.String(), `"prevVersion":1`) {
		t.Fatal(versionDetails.Body.String())
	}
	operations := call(member, "GET", "/footer-comments/"+top.ID+"/operations", nil, 200)
	if !strings.Contains(operations.Body.String(), `"operation":"update"`) {
		t.Fatal(operations.Body.String())
	}
	readOnly := call(member, "GET", "/footer-comments/"+reply.ID+"/operations", nil, 200)
	if strings.Contains(readOnly.Body.String(), `"operation":"update"`) || !strings.Contains(readOnly.Body.String(), `"operation":"read"`) {
		t.Fatal(readOnly.Body.String())
	}
	if err := st.SetWikiFooterCommentLike(ctx, ws, member, reply.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetWikiFooterCommentLike(ctx, ws, member, reply.ID, true); err != nil {
		t.Fatal(err)
	}
	likeCount := call(actor, "GET", "/footer-comments/"+reply.ID+"/likes/count", nil, 200)
	if !strings.Contains(likeCount.Body.String(), `"count":1`) {
		t.Fatal(likeCount.Body.String())
	}
	likeUsers := call(actor, "GET", "/footer-comments/"+reply.ID+"/likes/users", nil, 200)
	if !strings.Contains(likeUsers.Body.String(), member) {
		t.Fatal(likeUsers.Body.String())
	}
	call(member, "DELETE", "/footer-comments/"+reply.ID, nil, 404)
	call(actor, "DELETE", "/footer-comments/"+reply.ID, nil, 204)
	call(actor, "GET", "/footer-comments/"+reply.ID, nil, 404)
	call(member, "DELETE", "/footer-comments/"+top.ID, nil, 204)
	call(actor, "GET", "/footer-comments/"+top.ID, nil, 404)
	versions := call(actor, "GET", "/pages/"+page.ID+"/versions", nil, 200)
	if !strings.Contains(versions.Body.String(), "Updated release instructions") {
		t.Fatal(versions.Body.String())
	}
	update["parentId"] = page.ID
	update["version"] = map[string]int{"number": 3}
	call(actor, "PUT", "/pages/"+page.ID, update, 400)
	delete(update, "parentId")
	call(actor, "DELETE", "/pages/"+hierarchyGrandchild.ID, nil, 204)
	call(actor, "DELETE", "/pages/"+hierarchyChild.ID, nil, 204)
	call(actor, "DELETE", "/folders/"+privateDatabaseChild.ID, nil, 204)
	call(actor, "DELETE", "/databases/"+privateDatabase.ID, nil, 204)
	call(actor, "DELETE", "/whiteboards/"+privateWhiteboard.ID, nil, 204)
	call(actor, "DELETE", "/pages/"+page.ID, nil, 204)
	call(actor, "GET", "/pages/"+page.ID, nil, 404)
	call(actor, "GET", "/pages/"+page.ID+"?status=trashed&body-format=storage", nil, 200)
	update["version"] = map[string]int{"number": 4}
	call(actor, "PUT", "/pages/"+page.ID, update, 200)
	got := call(actor, "GET", "/pages/"+page.ID+"?body-format=storage", nil, 200)
	if !strings.Contains(got.Body.String(), "<h2>Ready</h2>") && !strings.Contains(got.Body.String(), `\u003ch2\u003eReady`) {
		t.Fatal(got.Body.String())
	}
	create(public, "Another page", "current")
	paged := call(actor, "GET", "/pages?limit=1", nil, 200)
	if paged.Header().Get("Link") == "" {
		t.Fatal("missing cursor Link header")
	}
	call(actor, "GET", "/pages?cursor=broken", nil, 400)
	call(actor, "DELETE", "/pages/"+draft.ID, nil, 204)
	call(member, "GET", "/pages/"+draft.ID+"?status=trashed", nil, 404)
	trash := call(member, "GET", "/pages?status=trashed", nil, 200)
	if strings.Contains(trash.Body.String(), "Private draft") {
		t.Fatal("trashed draft leaked")
	}
	restricted := create(public, "Restricted launch plan", "current")
	var groupID string
	if err := st.Pool.QueryRow(ctx, `INSERT INTO groups(directory_id,name,description)
		SELECT d.id,'release-managers','Release coordination' FROM sites si JOIN directories d ON d.organization_id=si.organization_id
		WHERE si.workspace_id=$1 ORDER BY d.id LIMIT 1 RETURNING id::text`, ws).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO group_members(group_id,user_id) VALUES($1::uuid,$2)`, groupID, member)
	callV1(actor, "GET", "/content/"+restricted.ID+"/restriction", nil, 200)
	callV1(actor, "PUT", "/content/"+restricted.ID+"/restriction", []map[string]any{{"operation": "update", "restrictions": map[string]any{"user": []map[string]string{{"type": "known", "accountId": actor}}}}}, 200)
	callV1(member, "POST", "/content/"+restricted.ID+"/restriction", []map[string]any{{"operation": "read", "restrictions": map[string]any{"user": []map[string]string{{"type": "known", "accountId": member}}}}}, 403)
	actorOnly := []map[string]any{{"operation": "read", "restrictions": map[string]any{"user": []map[string]string{{"type": "known", "accountId": actor}}}}}
	rootRestrictions := callV1(actor, "PUT", "/content/"+restricted.ID+"/restriction", actorOnly, 200)
	if !strings.Contains(rootRestrictions.Body.String(), `"operation":"read"`) || !strings.Contains(rootRestrictions.Body.String(), `"restrictionsHash"`) || !strings.Contains(rootRestrictions.Body.String(), `"base":"https://zzira.test/wiki"`) {
		t.Fatal(rootRestrictions.Body.String())
	}
	call(member, "GET", "/pages/"+restricted.ID, nil, 404)
	call(member, "GET", "/pages/"+restricted.ID+"/children", nil, 404)
	call(member, "GET", "/pages/"+restricted.ID+"/ancestors", nil, 404)
	call(member, "GET", "/pages/"+restricted.ID+"/descendants", nil, 404)
	callV1(member, "GET", "/content/"+restricted.ID+"/descendant/page", nil, 404)
	call(admin, "GET", "/pages/"+restricted.ID, nil, 200)
	callV1(admin, "GET", "/content/"+restricted.ID+"/restriction", nil, 200)
	callV1(actor, "GET", "/content/"+restricted.ID+"/restriction/byOperation", nil, 200)
	callV1(actor, "GET", "/content/"+restricted.ID+"/restriction/byOperation/read", nil, 200)
	groupPath := "/content/" + restricted.ID + "/restriction/byOperation/read/byGroupId/" + groupID
	if status := callV1(actor, "GET", groupPath, nil, 200); strings.TrimSpace(status.Body.String()) != "false" {
		t.Fatal(status.Body.String())
	}
	callV1(actor, "PUT", groupPath, nil, 200)
	if status := callV1(member, "GET", groupPath, nil, 200); strings.TrimSpace(status.Body.String()) != "true" {
		t.Fatal(status.Body.String())
	}
	call(member, "GET", "/pages/"+restricted.ID, nil, 200)
	callV1(actor, "DELETE", groupPath, nil, 200)
	call(member, "GET", "/pages/"+restricted.ID, nil, 404)
	userPath := "/content/" + restricted.ID + "/restriction/byOperation/read/user?accountId=" + member
	callV1(actor, "PUT", userPath, nil, 200)
	if status := callV1(member, "GET", userPath, nil, 200); strings.TrimSpace(status.Body.String()) != "true" {
		t.Fatal(status.Body.String())
	}
	callV1(actor, "GET", "/content/"+restricted.ID+"/restriction/byOperation/read/user?key="+member, nil, 200)
	actorUpdatePath := "/content/" + restricted.ID + "/restriction/byOperation/update/user?accountId=" + actor
	callV1(actor, "PUT", actorUpdatePath, nil, 200)
	restrictedUpdate := map[string]any{"id": restricted.ID, "spaceId": public, "title": "Restricted launch plan", "status": "current", "body": models.WikiBody{Representation: "storage", Value: "<p>Managers approved the launch.</p>"}, "version": map[string]int{"number": 2}}
	call(member, "PUT", "/pages/"+restricted.ID, restrictedUpdate, 404)
	callV1(member, "POST", "/content/"+restricted.ID+"/label", map[string]string{"prefix": "global", "name": "denied-label"}, 404)
	callV1(actor, "DELETE", actorUpdatePath, nil, 200)
	updatePath := "/content/" + restricted.ID + "/restriction/byOperation/update/user?accountId=" + member
	callV1(actor, "PUT", updatePath, nil, 200)
	call(member, "PUT", "/pages/"+restricted.ID, restrictedUpdate, 200)
	callV1(actor, "DELETE", updatePath, nil, 200)
	callV1(actor, "DELETE", userPath, nil, 200)
	call(member, "GET", "/pages/"+restricted.ID, nil, 404)
	callV1(actor, "POST", "/content/"+restricted.ID+"/restriction", map[string]any{"results": []map[string]any{{"operation": "read", "restrictions": map[string]any{"group": []map[string]string{{"type": "group", "id": groupID}}}}}}, 200)
	call(member, "GET", "/pages/"+restricted.ID, nil, 200)
	callV1(actor, "DELETE", "/content/"+restricted.ID+"/restriction", nil, 200)
	call(member, "GET", "/pages/"+restricted.ID, nil, 200)
	restrictedUpload := callV1Multipart(actor, "POST", "/content/"+restricted.ID+"/child/attachment", "classified-release.txt", "confidential", "Restricted file", 200)
	var restrictedAttachments struct {
		Results []models.WikiAttachment
	}
	if err := json.Unmarshal(restrictedUpload.Body.Bytes(), &restrictedAttachments); err != nil || len(restrictedAttachments.Results) != 1 {
		t.Fatalf("unexpected restricted attachment: %+v %v", restrictedAttachments, err)
	}
	restrictedAttachmentID := restrictedAttachments.Results[0].ID
	call(actor, "POST", "/attachments/"+restrictedAttachmentID+"/properties", map[string]any{"key": "classified-metadata", "value": "hidden"}, 200)
	if _, err := h.Commands.AddWikiAttachmentLabels(ctx, ws, actor, restrictedAttachmentID, []models.WikiLabel{{Prefix: "global", Name: "classified-file"}}); err != nil {
		t.Fatal(err)
	}
	restrictedCommentResponse := call(actor, "POST", "/footer-comments", map[string]any{"attachmentId": restrictedAttachmentID, "body": models.WikiBody{Representation: "storage", Value: "<p>Classified attachment discussion</p>"}}, 201)
	var restrictedAttachmentComment models.WikiFooterComment
	if err := json.Unmarshal(restrictedCommentResponse.Body.Bytes(), &restrictedAttachmentComment); err != nil {
		t.Fatal(err)
	}
	restrictedInlineResponse := call(actor, "POST", "/inline-comments", map[string]any{"pageId": restricted.ID, "body": models.WikiBody{Representation: "storage", Value: "<p>Classified inline discussion</p>"}, "inlineCommentProperties": map[string]any{"textSelection": "Managers approved", "textSelectionMatchCount": 1, "textSelectionMatchIndex": 0}}, 201)
	var restrictedInlineComment models.WikiFooterComment
	if err := json.Unmarshal(restrictedInlineResponse.Body.Bytes(), &restrictedInlineComment); err != nil {
		t.Fatal(err)
	}
	restrictedTask, err := h.Commands.CreateWikiTask(ctx, ws, actor, models.WikiTask{PageID: restricted.ID, Body: models.WikiBody{Representation: "storage", Value: "<p>Classified task discussion</p>"}})
	if err != nil {
		t.Fatal(err)
	}
	callV1(actor, "PUT", "/content/"+restricted.ID+"/restriction", actorOnly, 200)
	call(member, "GET", "/pages/"+restricted.ID, nil, 404)
	call(member, "GET", "/attachments/"+restrictedAttachmentID, nil, 404)
	call(member, "GET", "/footer-comments/"+restrictedAttachmentComment.ID, nil, 404)
	call(member, "GET", "/inline-comments/"+restrictedInlineComment.ID, nil, 404)
	call(member, "GET", "/tasks/"+restrictedTask.ID, nil, 404)
	if visibleAttachments := call(member, "GET", "/attachments", nil, 200); strings.Contains(visibleAttachments.Body.String(), "classified-release") {
		t.Fatal("restricted attachment leaked through the global collection")
	}
	if visibleLabels := call(member, "GET", "/labels", nil, 200); strings.Contains(visibleLabels.Body.String(), "classified-file") {
		t.Fatal("restricted attachment label leaked through the global collection")
	}
	if spaceContentLabels := call(member, "GET", "/spaces/"+public+"/content/labels", nil, 200); strings.Contains(spaceContentLabels.Body.String(), "classified-file") {
		t.Fatal("restricted attachment label leaked through the space content collection")
	}
	actions, err := st.ActionsSince(ctx, ws, member, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	publicBlogAttachmentVisible := false
	for _, a := range actions {
		if a.EntityType == "wiki_attachment" && a.EntityID == blogAttachment.ID {
			publicBlogAttachmentVisible = true
		}
		if a.EntityType == "wiki_footer_comment_like" && strings.HasPrefix(a.EntityID, secretCommentBean.ID+":") {
			t.Fatalf("private wiki like action leaked: %s", a.Payload)
		}
		if strings.Contains(string(a.Payload), "Private draft") || strings.Contains(string(a.Payload), "Secret guide") || strings.Contains(string(a.Payload), "PRIVATE") || strings.Contains(string(a.Payload), "Private launch phrase") || strings.Contains(string(a.Payload), "Private blog phrase") || strings.Contains(string(a.Payload), "private-blog-metadata") || strings.Contains(string(a.Payload), "secret-blog-label") || strings.Contains(string(a.Payload), "private-evidence.txt") || strings.Contains(string(a.Payload), "Private folder") || strings.Contains(string(a.Payload), "secret-folder-property") || strings.Contains(string(a.Payload), "Private Smart Link") || strings.Contains(string(a.Payload), "private.example.test") || strings.Contains(string(a.Payload), "Private database") || strings.Contains(string(a.Payload), "private-database-property") || strings.Contains(string(a.Payload), "Private database child") || strings.Contains(string(a.Payload), "Private whiteboard") || strings.Contains(string(a.Payload), "secret-label") || strings.Contains(string(a.Payload), "Restricted launch plan") || strings.Contains(string(a.Payload), "Managers approved the launch") || strings.Contains(string(a.Payload), "classified-release") || strings.Contains(string(a.Payload), "classified-metadata") || strings.Contains(string(a.Payload), "classified-file") || strings.Contains(string(a.Payload), "Classified attachment discussion") || strings.Contains(string(a.Payload), "Classified inline discussion") || strings.Contains(string(a.Payload), "Classified task discussion") {
			t.Fatalf("private wiki action leaked: %s", a.Payload)
		}
	}
	if !publicBlogAttachmentVisible {
		t.Fatal("public blog attachment action was not synchronized")
	}
}
