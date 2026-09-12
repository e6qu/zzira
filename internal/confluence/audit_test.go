package confluence

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestAuditLog pins Confluence's audit log: reading it, adding to it, the
// period back from now, the export, and the retention that decides how long a
// record is kept.
func TestAuditLog(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Audit test')`, ws)
	for _, value := range []struct{ id, role string }{{admin, "admin"}, {member, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$3)`,
			value.id, value.id+"@example.test", "Audit "+value.role)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, value.id, value.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, value.id, store.HashToken(value.id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM wiki_audit_records WHERE workspace_id=$1`,
			`DELETE FROM wiki_site_settings WHERE workspace_id=$1`,
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
	call := func(user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, "/wiki/rest/api"+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		var handler http.Handler = v1
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, response.Code, want, response.Body.String())
		}
		return response
	}
	object := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, response.Body.String())
		}
		return out
	}
	size := func(response *httptest.ResponseRecorder) int {
		t.Helper()
		return int(object(response)["size"].(float64))
	}

	// The whole log is administration.
	call(member, "GET", "/audit", nil, 403)
	call(member, "POST", "/audit", map[string]any{"summary": "Sneaky"}, 403)
	call(member, "GET", "/audit/retention", nil, 403)

	if retention := object(call(admin, "GET", "/audit/retention", nil, 200)); retention["number"].(float64) != 3 || retention["units"] != "MONTHS" {
		t.Fatalf("default retention: %v", retention)
	}

	// A record carries what the administrator said about it.
	recorded := object(call(admin, "POST", "/audit", map[string]any{
		"summary": "Exported a space", "description": "Space TPL exported",
		"category": "Space administration", "sysAdmin": true,
		"affectedObject": map[string]any{"name": "TPL", "objectType": "Space"},
		"changedValues":  []any{map[string]any{"name": "status", "oldValue": "current", "newValue": "exported"}},
	}, 200))
	if recorded["summary"] != "Exported a space" || recorded["sysAdmin"] != true {
		t.Fatalf("recorded: %v", recorded)
	}
	author, _ := recorded["author"].(map[string]any)
	if author["accountId"] != admin {
		t.Fatalf("a record with no author named is the caller's: %v", recorded)
	}
	affected, _ := recorded["affectedObject"].(map[string]any)
	if affected["name"] != "TPL" {
		t.Fatalf("affected object: %v", recorded)
	}
	changed, _ := recorded["changedValues"].([]any)
	if len(changed) != 1 {
		t.Fatalf("changed values: %v", recorded)
	}
	if _, ok := recorded["creationDate"].(float64); !ok {
		t.Fatalf("a record carries a millisecond creation date: %v", recorded)
	}
	call(admin, "POST", "/audit", map[string]any{"summary": "   "}, 400)

	call(admin, "POST", "/audit", map[string]any{"summary": "Changed a permission", "category": "Permissions"}, 200)
	if got := size(call(admin, "GET", "/audit", nil, 200)); got != 2 {
		t.Fatalf("audit listing: %d", got)
	}
	// The search looks at the summary, description and category.
	if got := size(call(admin, "GET", "/audit?searchString=Exported", nil, 200)); got != 1 {
		t.Fatalf("search by summary: %d", got)
	}
	if got := size(call(admin, "GET", "/audit?searchString=Permissions", nil, 200)); got != 1 {
		t.Fatalf("search by category: %d", got)
	}
	if got := size(call(admin, "GET", "/audit?searchString=nothingmatchesthis", nil, 200)); got != 0 {
		t.Fatalf("search miss: %d", got)
	}
	call(admin, "GET", "/audit?startDate=yesterday", nil, 400)

	// A date range that ends before the records begin finds none.
	longAgo := time.Now().UTC().Add(-48 * time.Hour).UnixMilli()
	if got := size(call(admin, "GET", "/audit?endDate="+itoa64(longAgo), nil, 200)); got != 0 {
		t.Fatalf("a range ending before the records should be empty: %d", got)
	}

	// The period back from now is the same read with the start worked out.
	if got := size(call(admin, "GET", "/audit/since?number=1&units=DAYS", nil, 200)); got != 2 {
		t.Fatalf("since: %d", got)
	}
	call(admin, "GET", "/audit/since?units=FORTNIGHTS", nil, 400)
	call(admin, "GET", "/audit/since?number=0", nil, 400)

	// The export carries the same records, as CSV and as the same CSV zipped.
	csvExport := call(admin, "GET", "/audit/export", nil, 200)
	if got := csvExport.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Fatalf("csv content type: %q", got)
	}
	lines := strings.Split(strings.TrimSpace(csvExport.Body.String()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "Date,Author") {
		t.Fatalf("csv export: %q", csvExport.Body.String())
	}
	zipExport := call(admin, "GET", "/audit/export?format=zip", nil, 200)
	if got := zipExport.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("zip content type: %q", got)
	}
	archive, err := zip.NewReader(bytes.NewReader(zipExport.Body.Bytes()), int64(zipExport.Body.Len()))
	if err != nil {
		t.Fatalf("zip export: %v", err)
	}
	if len(archive.File) != 1 || archive.File[0].Name != "audit.csv" {
		t.Fatalf("zip entries: %v", archive.File)
	}
	entry, err := archive.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	var zipped bytes.Buffer
	if _, err := zipped.ReadFrom(entry); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(zipped.String()) != strings.TrimSpace(csvExport.Body.String()) {
		t.Fatalf("the zip should carry the same csv:\n%q\n%q", zipped.String(), csvExport.Body.String())
	}
	call(admin, "GET", "/audit/export?format=pdf", nil, 400)

	// Confluence caps the retention at a year.
	call(admin, "PUT", "/audit/retention", map[string]any{"number": 2, "units": "YEARS"}, 400)
	call(admin, "PUT", "/audit/retention", map[string]any{"number": 400, "units": "DAYS"}, 400)
	call(admin, "PUT", "/audit/retention", map[string]any{"number": 0, "units": "DAYS"}, 400)
	call(admin, "PUT", "/audit/retention", map[string]any{"number": 1, "units": "FORTNIGHTS"}, 400)
	call(member, "PUT", "/audit/retention", map[string]any{"number": 1, "units": "DAYS"}, 403)
	if set := object(call(admin, "PUT", "/audit/retention", map[string]any{"number": 1, "units": "DAYS"}, 200)); set["units"] != "DAYS" {
		t.Fatalf("set retention: %v", set)
	}

	// A record older than the retention is not in the log, because the site
	// says it keeps records only that long.
	exec(`UPDATE wiki_audit_records SET created_at = now() - interval '3 days' WHERE workspace_id=$1`, ws)
	if got := size(call(admin, "GET", "/audit", nil, 200)); got != 0 {
		t.Fatalf("records past the retention are still in the log: %d", got)
	}
	// Shortening the retention removes them rather than hiding them.
	var remaining int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM wiki_audit_records WHERE workspace_id=$1`, ws).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	call(admin, "PUT", "/audit/retention", map[string]any{"number": 2, "units": "DAYS"}, 200)
	var afterShortening int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM wiki_audit_records WHERE workspace_id=$1`, ws).Scan(&afterShortening); err != nil {
		t.Fatal(err)
	}
	if remaining == 0 || afterShortening != 0 {
		t.Fatalf("setting the retention should delete what falls outside it: %d then %d", remaining, afterShortening)
	}
}

func itoa64(value int64) string {
	return strconv.FormatInt(value, 10)
}
