package api3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestSearchValidationRefusesDeletedWorkspaceCatalogValues(t *testing.T) {
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
	ws, actor := store.NewID("ws"), store.NewID("usr")
	email := actor + "@example.invalid"
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Deleted JQL values')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Catalogue actor')`, actor, email)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, ws, actor)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actor, store.HashToken(actor))
	t.Cleanup(func() {
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM workspaces WHERE id=$1`, ws)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, actor)
		exec(`DELETE FROM users WHERE id=$1`, actor)
	})
	var priorityID, jiraID string
	if err := st.Pool.QueryRow(ctx, `SELECT id,jira_id::text FROM priorities WHERE workspace_id IS NULL ORDER BY jira_id LIMIT 1`).Scan(&priorityID, &jiraID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO issue_metadata_overrides(workspace_id,entity_type,entity_id,name,deleted) VALUES($1,'priority',$2,'Retired urgency',TRUE)`, ws, priorityID)
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	for _, value := range []string{"Retired urgency", jiraID} {
		for _, mode := range []string{"strict", "warn", "none"} {
			query := url.Values{"jql": {`priority = "` + value + `"`}, "validateQuery": {mode}}
			request := httptest.NewRequest(http.MethodGet, "/rest/api/3/search?"+query.Encode(), nil)
			request.SetBasicAuth(email, actor)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			want := http.StatusOK
			if mode == "strict" {
				want = http.StatusBadRequest
			}
			if response.Code != want {
				t.Fatalf("%s %s: %d %s", mode, value, response.Code, response.Body.String())
			}
			if mode == "strict" && !strings.Contains(response.Body.String(), "does not exist") {
				t.Fatalf("strict validation lost value error: %s", response.Body.String())
			}
			if mode != "strict" {
				var body struct {
					Warnings []string `json:"warningMessages"`
				}
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if mode == "warn" && len(body.Warnings) != 1 {
					t.Fatalf("warn validation lost warning: %s", response.Body.String())
				}
				if mode == "none" && len(body.Warnings) != 0 {
					t.Fatalf("disabled validation emitted a warning: %s", response.Body.String())
				}
			}
		}
	}
}
