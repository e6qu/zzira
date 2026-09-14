package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestOrganizationClassificationLevels pins classification levels as the
// organization defines them: drafts that cannot be chosen, published levels
// that can, archived levels that content keeps but nobody can choose again,
// their order, and content reading its space's default when it has no level of
// its own.
func TestOrganizationClassificationLevels(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Classification test')`, ws)
	for _, user := range []string{admin, member} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test',$1)`, user, user+"@example.test")
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, user, store.HashToken(user))
	}
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,'admin'),($1,$3,'member')`, ws, admin, member)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM data_classification_levels WHERE organization_id=(SELECT organization_id::text FROM sites WHERE workspace_id=$1)`,
			`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT p.id FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1)`,
			`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, user := range []string{admin, member} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path string, body any, want int) string {
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
		return response.Body.String()
	}
	idOf := func(body string) string {
		t.Helper()
		var bean struct{ ID string }
		if err := json.Unmarshal([]byte(body), &bean); err != nil || bean.ID == "" {
			t.Fatalf("no id in %s", body)
		}
		return bean.ID
	}
	storage := map[string]any{"representation": "storage", "value": "<p>Plan</p>"}

	// Every organization starts with the four levels.
	if levels := call(member, "GET", "/classification-levels", nil, 200); !strings.Contains(levels, `"id":"public"`) || !strings.Contains(levels, `"id":"restricted"`) || strings.Count(levels, `"status":"PUBLISHED"`) != 4 {
		t.Fatalf("starting levels: %s", levels)
	}
	space := idOf(call(admin, "POST", "/spaces", map[string]any{"key": "CLS", "name": "Classified"}, 201))
	page := idOf(call(admin, "POST", "/pages", map[string]any{"spaceId": space, "title": "Plan", "status": "current", "body": storage}, 200))
	other := idOf(call(admin, "POST", "/pages", map[string]any{"spaceId": space, "title": "Other", "status": "current", "body": storage}, 200))

	// Only administrators define levels, and a new level is a draft nobody can
	// choose.
	if _, err := st.CreateDataClassificationLevel(ctx, ws, member, store.DataClassificationLevelInput{Name: "Secret", Color: "PURPLE"}); !errors.Is(err, store.ErrProjectPermission) {
		t.Fatalf("member created a level: %v", err)
	}
	if _, err := st.CreateDataClassificationLevel(ctx, ws, admin, store.DataClassificationLevelInput{Name: "public", Color: "PURPLE"}); !errors.Is(err, store.ErrClassificationValidation) {
		t.Fatalf("duplicate level name: %v", err)
	}
	secret, err := st.CreateDataClassificationLevel(ctx, ws, admin, store.DataClassificationLevelInput{Name: "Secret", Description: "Board only", Guideline: "Never share.", Color: "PURPLE"})
	if err != nil || secret.Status != "DRAFT" || secret.Rank != 4 {
		t.Fatalf("draft level: %+v %v", secret, err)
	}
	if levels := call(member, "GET", "/classification-levels", nil, 200); strings.Contains(levels, "Secret") {
		t.Fatalf("draft level listed: %s", levels)
	}
	call(admin, "PUT", "/pages/"+page+"/classification-level", map[string]string{"id": secret.ID, "status": "current"}, 400)

	// A published level is listed and can be chosen.
	if _, err := st.SetDataClassificationLevelStatus(ctx, ws, admin, secret.ID, "PUBLISHED"); err != nil {
		t.Fatal(err)
	}
	call(admin, "PUT", "/pages/"+page+"/classification-level", map[string]string{"id": secret.ID, "status": "current"}, 204)
	if level := call(member, "GET", "/pages/"+page+"/classification-level", nil, 200); !strings.Contains(level, `"name":"Secret"`) || !strings.Contains(level, `"guideline":"Never share."`) {
		t.Fatalf("page level: %s", level)
	}

	// Content without a level of its own reads its space's default.
	call(admin, "PUT", "/spaces/"+space+"/classification-level/default", map[string]string{"id": "confidential"}, 204)
	call(admin, "POST", "/pages/"+other+"/classification-level/reset", map[string]string{"status": "current"}, 204)
	if level := call(member, "GET", "/pages/"+other+"/classification-level", nil, 200); !strings.Contains(level, `"id":"confidential"`) {
		t.Fatalf("inherited level: %s", level)
	}
	call(admin, "PUT", "/spaces/"+space+"/classification-level/default", map[string]string{"id": secret.ID + "-missing"}, 400)

	// An archived level stays on the content that has it but cannot be chosen
	// again or edited, and it can be restored.
	if _, err := st.SetDataClassificationLevelStatus(ctx, ws, admin, secret.ID, "ARCHIVED"); err != nil {
		t.Fatal(err)
	}
	if level := call(member, "GET", "/pages/"+page+"/classification-level", nil, 200); !strings.Contains(level, `"status":"ARCHIVED"`) {
		t.Fatalf("archived level on content: %s", level)
	}
	call(admin, "PUT", "/pages/"+other+"/classification-level", map[string]string{"id": secret.ID, "status": "current"}, 400)
	if _, err := st.UpdateDataClassificationLevel(ctx, ws, admin, secret.ID, store.DataClassificationLevelInput{Name: "Secret", Color: "RED"}); !errors.Is(err, store.ErrClassificationValidation) {
		t.Fatalf("archived level edited: %v", err)
	}
	if _, err := st.SetDataClassificationLevelStatus(ctx, ws, admin, secret.ID, "DRAFT"); !errors.Is(err, store.ErrClassificationValidation) {
		t.Fatalf("level returned to draft: %v", err)
	}
	if restored, err := st.SetDataClassificationLevelStatus(ctx, ws, admin, secret.ID, "PUBLISHED"); err != nil || restored.Status != "PUBLISHED" {
		t.Fatalf("restored level: %+v %v", restored, err)
	}

	// Levels keep an order administrators change.
	if err := st.MoveDataClassificationLevel(ctx, ws, admin, secret.ID, true); err != nil {
		t.Fatal(err)
	}
	levels, err := st.DataClassificationLevels(ctx, ws)
	if err != nil || len(levels) != 5 || levels[3].ID != secret.ID || levels[4].ID != "restricted" {
		t.Fatalf("reordered levels: %+v %v", levels, err)
	}
}
