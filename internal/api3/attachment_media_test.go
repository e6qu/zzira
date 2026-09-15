package api3

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestAttachmentMediaFollowsJira covers attachment downloads as Jira serves
// them: content and thumbnails redirect to their download, thumbnails scale
// within the requested bounds or fall back to the default thumbnail, archives
// other than ZIP conflict, the site's attachment size limit and switch apply,
// and metadata carries a numeric id.
func TestAttachmentMediaFollowsJira(t *testing.T) {
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
	if err = store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspaceID, adminID := store.NewID("ws"), store.NewID("usr")
	projectKey := fmt.Sprintf("AM%06d", time.Now().UnixNano()%1000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Attachment media')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Media Admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, adminID)
		exec(`DELETE FROM users WHERE id=$1`, adminID)
	})
	service := &commands.Service{Store: st, Blobs: blobs}
	h := &Handler{Store: st, Commands: service, Blobs: blobs, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	send := func(method, path string, header http.Header, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		request.SetBasicAuth(adminID+"@example.test", adminID)
		for name, values := range header {
			request.Header[name] = values
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		return response
	}
	call := func(method, path string, want int) *httptest.ResponseRecorder {
		t.Helper()
		response := send(method, path, nil, nil)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}

	var project map[string]any
	if err = json.Unmarshal(send(http.MethodPost, "/rest/api/3/project", http.Header{"Content-Type": {"application/json"}}, []byte(`{"key":"`+projectKey+`","name":"Media `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`)).Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	var issue map[string]any
	if err = json.Unmarshal(send(http.MethodPost, "/rest/api/3/issue", http.Header{"Content-Type": {"application/json"}}, []byte(`{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Media","issuetype":{"name":"Task"}}}`)).Body.Bytes(), &issue); err != nil || issue["key"] == nil {
		t.Fatalf("issue = %v err=%v", issue, err)
	}
	issueKey := issue["key"].(string)
	attach := func(name, mimeType string, contents []byte) *models.Attachment {
		t.Helper()
		attachment, _, attachErr := service.AddAttachment(ctx, adminID, workspaceID, issueKey, name, mimeType, bytes.NewReader(contents))
		if attachErr != nil {
			t.Fatal(attachErr)
		}
		return attachment
	}

	// Content redirects to its download, which honours ranges.
	notes := attach("release notes.txt", "text/plain", []byte("release notes body"))
	redirect := call(http.MethodGet, "/rest/api/3/attachment/content/"+notes.ID, http.StatusSeeOther)
	location := redirect.Header().Get("Location")
	if location != fmt.Sprintf("https://zzira.test/secure/attachment/%d/release%%20notes.txt", notes.JiraID) {
		t.Fatalf("content location = %q", location)
	}
	download := strings.TrimPrefix(location, "https://zzira.test")
	if body := call(http.MethodGet, download, http.StatusOK).Body.String(); body != "release notes body" {
		t.Fatalf("download = %q", body)
	}
	if ranged := send(http.MethodGet, download, http.Header{"Range": {"bytes=8-12"}}, nil); ranged.Code != http.StatusPartialContent || ranged.Body.String() != "notes" {
		t.Fatalf("ranged download = %d %q", ranged.Code, ranged.Body.String())
	}
	if malformed := send(http.MethodGet, "/rest/api/3/attachment/content/"+notes.ID+"?redirect=false", http.Header{"Range": {"lines=1-2"}}, nil); malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed range = %d", malformed.Code)
	}
	call(http.MethodGet, "/rest/api/3/attachment/content/"+notes.ID+"?redirect=maybe", http.StatusBadRequest)

	// Metadata has Jira's numeric id and the author's user bean.
	var metadata map[string]any
	if err = json.Unmarshal(call(http.MethodGet, "/rest/api/3/attachment/"+notes.ID, http.StatusOK).Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["id"] != float64(notes.JiraID) || metadata["author"].(map[string]any)["displayName"] != "Media Admin" || metadata["properties"] == nil {
		t.Fatalf("metadata = %v", metadata)
	}

	// Thumbnails scale within the bounds, never enlarging.
	picture := image.NewNRGBA(image.Rect(0, 0, 400, 100))
	for x := 0; x < 400; x++ {
		for y := 0; y < 100; y++ {
			picture.Set(x, y, color.NRGBA{R: uint8(x), G: 90, B: 200, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	banner := attach("banner.png", "image/png", encoded.Bytes())
	thumbnailRedirect := call(http.MethodGet, "/rest/api/3/attachment/thumbnail/"+banner.ID+"?width=100&height=100", http.StatusSeeOther)
	if location := thumbnailRedirect.Header().Get("Location"); location != fmt.Sprintf("https://zzira.test/secure/thumbnail/%d/banner.png?height=100&width=100", banner.JiraID) {
		t.Fatalf("thumbnail location = %q", location)
	}
	size := func(path string) (int, int) {
		t.Helper()
		response := call(http.MethodGet, path, http.StatusOK)
		config, _, decodeErr := image.DecodeConfig(response.Body)
		if decodeErr != nil {
			t.Fatalf("%s: %v", path, decodeErr)
		}
		return config.Width, config.Height
	}
	if width, height := size(strings.TrimPrefix(thumbnailRedirect.Header().Get("Location"), "https://zzira.test")); width != 100 || height != 25 {
		t.Fatalf("scaled thumbnail = %dx%d", width, height)
	}
	if width, height := size("/rest/api/3/attachment/thumbnail/" + banner.ID + "?redirect=false"); width != 200 || height != 50 {
		t.Fatalf("default-bound thumbnail = %dx%d", width, height)
	}
	if width, height := size("/rest/api/3/attachment/thumbnail/" + banner.ID + "?redirect=false&width=1000&height=1000"); width != 400 || height != 100 {
		t.Fatalf("unenlarged thumbnail = %dx%d", width, height)
	}
	if width, height := size("/rest/api/3/attachment/thumbnail/" + notes.ID + "?redirect=false&width=48&height=64"); width != 48 || height != 48 {
		t.Fatalf("default thumbnail = %dx%d", width, height)
	}
	call(http.MethodGet, "/rest/api/3/attachment/thumbnail/"+notes.ID+"?redirect=false&fallbackToDefault=false", http.StatusNotFound)
	call(http.MethodGet, "/rest/api/3/attachment/thumbnail/"+banner.ID+"?width=0", http.StatusBadRequest)

	// Only ZIP archives expand; corrupt ones have no entries.
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	longName := "evidence/" + strings.Repeat("quarterly-", 6) + "report.txt"
	entry, err := writer.Create(longName)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = entry.Write([]byte("archive evidence"))
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	zipped := attach("evidence.zip", "application/zip", archive.Bytes())
	raw := call(http.MethodGet, "/rest/api/3/attachment/"+zipped.ID+"/expand/raw", http.StatusOK).Body.String()
	if !strings.Contains(raw, `"abbreviatedName":"`+abbreviateArchiveName(longName)+`"`) || len([]rune(abbreviateArchiveName(longName))) != 40 {
		t.Fatalf("raw expansion = %s", raw)
	}
	if human := call(http.MethodGet, "/rest/api/3/attachment/"+zipped.ID+"/expand/human", http.StatusOK).Body.String(); !strings.Contains(human, fmt.Sprintf(`"id":%d`, zipped.JiraID)) {
		t.Fatalf("human expansion = %s", human)
	}
	corrupt := attach("broken.zip", "application/zip", []byte("PK\x03\x04 not really"))
	if empty := call(http.MethodGet, "/rest/api/3/attachment/"+corrupt.ID+"/expand/raw", http.StatusOK).Body.String(); !strings.Contains(empty, `"totalEntryCount":0`) {
		t.Fatalf("corrupt expansion = %s", empty)
	}
	var gzipped bytes.Buffer
	compressor := gzip.NewWriter(&gzipped)
	_, _ = compressor.Write([]byte("logs"))
	_ = compressor.Close()
	tarball := attach("logs.tar.gz", "application/gzip", gzipped.Bytes())
	call(http.MethodGet, "/rest/api/3/attachment/"+tarball.ID+"/expand/human", http.StatusConflict)

	// The site's maximum attachment size and attachment switch.
	configure := func(enabled bool, limit int64) {
		t.Helper()
		if configureErr := service.UpdateGlobalJiraConfiguration(ctx, workspaceID, adminID, models.JiraSiteConfiguration{AttachmentsEnabled: enabled, AttachmentUploadLimit: limit, IssueLinkingEnabled: true, SubTasksEnabled: true, TimeTrackingEnabled: true, UnassignedIssuesAllowed: true, VotingEnabled: true, WatchingEnabled: true}); configureErr != nil {
			t.Fatal(configureErr)
		}
	}
	upload := func(contents string) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		part, _ := form.CreateFormFile("file", "upload.txt")
		_, _ = part.Write([]byte(contents))
		_ = form.Close()
		return send(http.MethodPost, "/rest/api/3/issue/"+issueKey+"/attachments", http.Header{"Content-Type": {form.FormDataContentType()}, "X-Atlassian-Token": {"no-check"}}, body.Bytes())
	}
	configure(true, 10)
	if settings := call(http.MethodGet, "/rest/api/3/attachment/meta", http.StatusOK).Body.String(); !strings.Contains(settings, `"uploadLimit":10`) {
		t.Fatalf("settings = %s", settings)
	}
	if tooLarge := upload("more than ten bytes"); tooLarge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload = %d %s", tooLarge.Code, tooLarge.Body.String())
	}
	if _, _, err = service.AddAttachment(ctx, adminID, workspaceID, issueKey, "big.txt", "text/plain", strings.NewReader("more than ten bytes")); err != commands.ErrAttachmentTooLarge {
		t.Fatalf("oversized command err = %v", err)
	}
	if small := upload("tiny"); small.Code != http.StatusOK {
		t.Fatalf("small upload = %d %s", small.Code, small.Body.String())
	}
	configure(false, 0)
	call(http.MethodGet, "/rest/api/3/attachment/"+notes.ID, http.StatusNotFound)
	call(http.MethodGet, download, http.StatusNotFound)
	if disabled := upload("tiny"); disabled.Code != http.StatusForbidden {
		t.Fatalf("upload while disabled = %d", disabled.Code)
	}
	configure(true, 0)
	if settings := call(http.MethodGet, "/rest/api/3/attachment/meta", http.StatusOK).Body.String(); !strings.Contains(settings, `"uploadLimit":10`) {
		t.Fatalf("a zero limit replaced the saved one: %s", settings)
	}
}
