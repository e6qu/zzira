package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
		for _, sql := range []string{`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`, `DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`, `DELETE FROM wiki_spaces WHERE workspace_id=$1`, `DELETE FROM wiki_labels WHERE workspace_id=$1`, `DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`} {
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
	if !strings.Contains(pageComments.Body.String(), "Ready for review") || strings.Contains(pageComments.Body.String(), "Approval recorded") {
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
	callV1(actor, "PUT", "/content/"+restricted.ID+"/restriction", actorOnly, 200)
	call(member, "GET", "/pages/"+restricted.ID, nil, 404)
	call(member, "GET", "/attachments/"+restrictedAttachmentID, nil, 404)
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
	for _, a := range actions {
		if a.EntityType == "wiki_footer_comment_like" && strings.HasPrefix(a.EntityID, secretCommentBean.ID+":") {
			t.Fatalf("private wiki like action leaked: %s", a.Payload)
		}
		if strings.Contains(string(a.Payload), "Private draft") || strings.Contains(string(a.Payload), "Secret guide") || strings.Contains(string(a.Payload), "PRIVATE") || strings.Contains(string(a.Payload), "Private launch phrase") || strings.Contains(string(a.Payload), "secret-label") || strings.Contains(string(a.Payload), "Restricted launch plan") || strings.Contains(string(a.Payload), "Managers approved the launch") || strings.Contains(string(a.Payload), "classified-release") || strings.Contains(string(a.Payload), "classified-metadata") || strings.Contains(string(a.Payload), "classified-file") {
			t.Fatalf("private wiki action leaked: %s", a.Payload)
		}
	}
}
