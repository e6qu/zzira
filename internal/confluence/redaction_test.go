package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestRedactionLifecycle pins redacting by stored-text ranges and by pointers
// into the document format, text in code blocks that cannot be restored,
// redacting one earlier version alone, and space administrators restoring what
// a redaction removed.
func TestRedactionLifecycle(t *testing.T) {
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
	ws, admin, editor := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Redaction test')`, ws)
	for _, user := range []string{admin, editor} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$1)`, user, user+"@example.test")
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, user, store.HashToken(user))
	}
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin'),($1,$3,'member')`, ws, admin, editor)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM organization_audit_events WHERE organization_id=(SELECT organization_id FROM sites WHERE workspace_id=$1)`,
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
		for _, user := range []string{admin, editor} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path string, body any, want int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, "/wiki/api/v2"+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		_ = json.Unmarshal(response.Body.Bytes(), &out)
		return out
	}
	runeRange := func(value, text string) (int, int) {
		t.Helper()
		at := strings.Index(value, text)
		if at < 0 {
			t.Fatalf("%q not in %q", text, value)
		}
		from := utf8.RuneCountInString(value[:at])
		return from, from + utf8.RuneCountInString(text)
	}
	storageOf := func(path string) (string, string, map[string]any) {
		t.Helper()
		bean := call(admin, "GET", path+"?body-format=storage", nil, 200)
		body := bean["body"].(map[string]any)["storage"].(map[string]any)["value"].(string)
		version := bean["version"].(map[string]any)
		return body, bean["title"].(string), version
	}
	space := idOfMap(t, call(admin, "POST", "/spaces", map[string]any{"key": "RED", "name": "Redactions"}, 201))
	body := `<p>Card 4111 1111 and <strong>secret-token</strong> &amp; more</p><pre>password=hunter2</pre>`
	page := idOfMap(t, call(admin, "POST", "/pages", map[string]any{"spaceId": space, "title": "Payroll 2026", "status": "current", "body": map[string]any{"representation": "storage", "value": body}}, 200))
	_, _, version := storageOf("/pages/" + page)

	// Stored-text ranges, a pointer into the document format, and code.
	cardFrom, cardTo := runeRange(body, "4111 1111")
	codeFrom, codeTo := runeRange(body, "hunter2")
	titleFrom, titleTo := 8, 12
	reason := "PCI"
	response := call(editor, "POST", "/pages/"+page+"/redact", map[string]any{
		"createdAt": version["createdAt"],
		"title":     map[string]any{"redactions": []map[string]any{{"pointer": "/title", "from": titleFrom, "to": titleTo}}},
		"body": map[string]any{"redactions": []map[string]any{
			{"pointer": "/body/storage/value", "from": cardFrom, "to": cardTo, "reason": reason},
			{"pointer": "/content/0/content/1/text", "from": 0, "to": 6},
			{"pointer": "/body/storage/value", "from": codeFrom, "to": codeTo},
		}},
	}, 202)
	bodyResults := response["body"].(map[string]any)["redactions"].([]any)
	cardID, _ := bodyResults[0].(map[string]any)["redactionId"].(string)
	tokenID, _ := bodyResults[1].(map[string]any)["redactionId"].(string)
	if cardID == "" || tokenID == "" || bodyResults[1].(map[string]any)["pointer"] != "/content/0/content/1/text" || bodyResults[2].(map[string]any)["redactionId"] != nil {
		t.Fatalf("redaction results: %v", bodyResults)
	}
	titleID := response["title"].(map[string]any)["redactions"].([]any)[0].(map[string]any)["redactionId"].(string)
	redacted, redactedTitle, _ := storageOf("/pages/" + page)
	for _, gone := range []string{"4111 1111", "secret", "hunter2"} {
		if strings.Contains(redacted, gone) {
			t.Fatalf("%q survived redaction: %s", gone, redacted)
		}
	}
	if !strings.Contains(redacted, `ac:macro-id="`+cardID+`"`) || !strings.Contains(redacted, `<strong>`) || !strings.Contains(redacted, "-token</strong>") || !strings.Contains(redacted, "<pre>password=[REDACTED]</pre>") || redactedTitle != "Payroll [REDACTED]" {
		t.Fatalf("redacted page: %q %s", redactedTitle, redacted)
	}
	call(editor, "POST", "/pages/"+page+"/redact", map[string]any{"createdAt": version["createdAt"], "body": map[string]any{"redactions": []map[string]any{{"pointer": "/content/9/text"}}}}, 400)

	// Only a space administrator restores, and only what can be restored.
	if err := st.RestoreWikiRedaction(ctx, ws, editor, "page", page, cardID); !errors.Is(err, store.ErrProjectPermission) {
		t.Fatalf("editor restored: %v", err)
	}
	if err := st.RestoreWikiRedaction(ctx, ws, admin, "page", page, cardID); err != nil {
		t.Fatal(err)
	}
	if err := st.RestoreWikiRedaction(ctx, ws, admin, "page", page, titleID); err != nil {
		t.Fatal(err)
	}
	restored, restoredTitle, restoredVersion := storageOf("/pages/" + page)
	if !strings.Contains(restored, "Card 4111 1111 and") || strings.Contains(restored, cardID) || restoredTitle != "Payroll 2026" || restoredVersion["number"] != float64(4) {
		t.Fatalf("restored page: %q %v %s", restoredTitle, restoredVersion, restored)
	}
	if err := st.RestoreWikiRedaction(ctx, ws, admin, "page", page, cardID); !errors.Is(err, store.ErrWikiValidation) {
		t.Fatalf("restored twice: %v", err)
	}
	redactions, err := st.WikiRedactions(ctx, ws, admin, "page", page)
	if err != nil || len(redactions) != 4 {
		t.Fatalf("redactions: %+v %v", redactions, err)
	}
	unrestorable := 0
	for _, r := range redactions {
		if !r.Restorable {
			unrestorable++
			if err := st.RestoreWikiRedaction(ctx, ws, admin, "page", page, r.ID); !errors.Is(err, store.ErrWikiValidation) {
				t.Fatalf("code redaction restored: %v", err)
			}
		}
	}
	if unrestorable != 1 {
		t.Fatalf("code redactions: %+v", redactions)
	}

	// An earlier version can be redacted on its own.
	first := call(admin, "GET", "/pages/"+page+"/versions/1", nil, 200)
	firstCreated := first["createdAt"]
	if firstCreated == nil {
		firstCreated = first["version"].(map[string]any)["createdAt"]
	}
	call(editor, "POST", "/pages/"+page+"/redact", map[string]any{"createdAt": firstCreated, "versionNumber": 1, "body": map[string]any{"redactions": []map[string]any{{"pointer": "/body/storage/value", "from": cardFrom, "to": cardTo}}}}, 202)
	current, _, currentVersion := storageOf("/pages/" + page)
	if !strings.Contains(current, "4111 1111") || currentVersion["number"] != float64(4) {
		t.Fatalf("current version changed by a historical redaction: %v %s", currentVersion, current)
	}
	historical := call(admin, "GET", "/pages/"+page+"?version=1&body-format=storage", nil, 200)
	if value := historical["body"].(map[string]any)["storage"].(map[string]any)["value"].(string); strings.Contains(value, "4111 1111") || !strings.Contains(value, "[REDACTED]") {
		t.Fatalf("historical version: %s", value)
	}
	call(editor, "POST", "/pages/"+page+"/redact", map[string]any{"createdAt": firstCreated, "versionNumber": 9, "body": map[string]any{"redactions": []map[string]any{{"pointer": "/body/storage/value", "from": 0, "to": 1}}}}, 400)

	// Blog posts redact and restore the same way.
	post := idOfMap(t, call(admin, "POST", "/blogposts", map[string]any{"spaceId": space, "title": "Weekly", "status": "current", "body": map[string]any{"representation": "storage", "value": "<p>Door code 1234</p>"}}, 200))
	_, _, postVersion := storageOf("/blogposts/" + post)
	postResult := call(editor, "POST", "/blogposts/"+post+"/redact", map[string]any{"createdAt": postVersion["createdAt"], "body": map[string]any{"redactions": []map[string]any{{"pointer": "/content/0/content/0/text", "from": 10, "to": 14}}}}, 202)
	postID := postResult["body"].(map[string]any)["redactions"].([]any)[0].(map[string]any)["redactionId"].(string)
	if value, _, _ := storageOf("/blogposts/" + post); strings.Contains(value, "1234") {
		t.Fatalf("blog redaction: %s", value)
	}
	if err := st.RestoreWikiRedaction(ctx, ws, admin, "blogpost", post, postID); err != nil {
		t.Fatal(err)
	}
	if value, _, _ := storageOf("/blogposts/" + post); !strings.Contains(value, "Door code 1234") {
		t.Fatalf("blog restoration: %s", value)
	}
}

func idOfMap(t *testing.T, body map[string]any) string {
	t.Helper()
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("no id in %v", body)
	}
	return id
}
