package api3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// pagedCollections names every collection the pinned specification pages with
// startAt and maxResults and that takes no path parameter, so the sweep below
// reaches all of them rather than the ones somebody remembered.
func pagedCollections(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "specs", "jira-v3.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name string `json:"name"`
				In   string `json:"in"`
			} `json:"parameters"`
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	for path, item := range document.Paths {
		get, ok := item["get"]
		if !ok || strings.Contains(path, "{") || !strings.HasPrefix(path, "/rest/api/3/") {
			continue
		}
		pages, offsets := false, false
		for _, parameter := range get.Parameters {
			if parameter.In != "query" {
				continue
			}
			switch parameter.Name {
			case "maxResults":
				pages = true
			case "startAt":
				offsets = true
			}
		}
		if pages && offsets {
			paths = append(paths, path)
		}
	}
	return paths
}

// TestAnOversizedPageIsServedNotRefused pins what a collection does when a
// client asks for more rows than it serves. The contract documents no failure
// for asking too much, and where it says anything it says the page size "will
// default to 100", so every paged collection answers with the largest page it
// serves and reports that size. A page nobody could serve -- a negative one,
// or one that is not a number -- is still refused.
func TestAnOversizedPageIsServedNotRefused(t *testing.T) {
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
	workspaceID, adminID := store.NewID("ws"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Page sizes')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Page admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, adminID)
		exec(`DELETE FROM users WHERE id=$1`, adminID)
	})
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	ask := func(path string) (int, map[string]any) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.SetBasicAuth(adminID+"@example.test", adminID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		out := map[string]any{}
		if response.Body.Len() > 0 && response.Body.Bytes()[0] == '{' {
			_ = json.Unmarshal(response.Body.Bytes(), &out)
		}
		return response.Code, out
	}

	collections := pagedCollections(t)
	if len(collections) < 40 {
		t.Fatalf("the contract pages %d collections without a path parameter, which is too few to be right", len(collections))
	}
	reached := 0
	for _, path := range collections {
		// Only the collections this site can answer are checked; the rest
		// refuse for their own reasons, which other tests cover.
		if status, _ := ask(path); status != http.StatusOK {
			continue
		}
		reached++
		status, answer := ask(path + "?maxResults=10000")
		if status != http.StatusOK {
			t.Errorf("GET %s refused a page of 10000 with %d, where the contract documents no failure for asking too much", path, status)
			continue
		}
		served, ok := answer["maxResults"].(float64)
		if !ok {
			continue
		}
		if served >= 10000 {
			t.Errorf("GET %s served a page of %v, which is the whole collection rather than the largest page it serves", path, served)
		}
	}
	if reached < 20 {
		t.Fatalf("only %d paged collections answered, too few to call this swept", reached)
	}

	// A page nobody could serve is still refused.
	for _, path := range []string{
		"/rest/api/3/screens?maxResults=-1",
		"/rest/api/3/screens?startAt=-1",
		"/rest/api/3/search?maxResults=-1",
		"/rest/api/3/search?maxResults=notanumber",
	} {
		if status, _ := ask(path); status != http.StatusBadRequest {
			t.Errorf("GET %s answered %d, want 400", path, status)
		}
	}
}
