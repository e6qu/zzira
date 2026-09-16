package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestProjectsWhereUserHasPermissionSearch(t *testing.T) {
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
	ws, admin, member := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, sql, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Permission function')`, ws)
	for _, user := range []string{admin, member} {
		role := "member"
		if user == admin {
			role = "admin"
		}
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Permission user')`, user, user+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, user, role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, user, store.HashToken(user))
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM issues WHERE workspace_id=$1`,
			`DELETE FROM projects WHERE workspace_id=$1`,
			`DELETE FROM permission_schemes WHERE workspace_id=$1`,
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

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) string {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(user+"@example.test", user)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec.Body.String()
	}
	total := func(user, jql string) float64 {
		t.Helper()
		var page map[string]any
		if err := json.Unmarshal([]byte(call(user, http.MethodGet, "/rest/api/3/search?jql="+url.QueryEscape(jql), "", http.StatusOK)), &page); err != nil {
			t.Fatal(err)
		}
		value, _ := page["total"].(float64)
		return value
	}

	stamp := fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
	openKey, closedKey := "PFO"+stamp, "PFC"+stamp
	for _, project := range []struct{ key, name string }{{openKey, "Open project"}, {closedKey, "Closed project"}} {
		call(admin, http.MethodPost, "/rest/api/3/project",
			`{"key":"`+project.key+`","name":"`+project.name+`","projectTypeKey":"software","leadAccountId":"`+admin+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)
		call(admin, http.MethodPost, "/rest/api/3/issue",
			`{"fields":{"project":{"key":"`+project.key+`"},"summary":"Work in `+project.key+`","issuetype":{"name":"Task"}}}`, http.StatusCreated)
	}

	// Everyone browses one project; only the member's own grant reaches the
	// other, which is left to the site default.
	var scheme map[string]any
	if err := json.Unmarshal([]byte(call(admin, http.MethodPost, "/rest/api/3/permissionscheme",
		`{"name":"Open browse `+openKey+`","permissions":[{"permission":"BROWSE_PROJECTS","holder":{"type":"anyone"}}]}`, http.StatusCreated)), &scheme); err != nil {
		t.Fatal(err)
	}
	call(admin, http.MethodPut, "/rest/api/3/project/"+openKey+"/permissionscheme", fmt.Sprintf(`{"id":%v}`, scheme["id"]), http.StatusOK)

	var closed map[string]any
	if err := json.Unmarshal([]byte(call(admin, http.MethodPost, "/rest/api/3/permissionscheme",
		`{"name":"Admin only `+closedKey+`","permissions":[{"permission":"BROWSE_PROJECTS","holder":{"type":"user","value":"`+admin+`"}}]}`, http.StatusCreated)), &closed); err != nil {
		t.Fatal(err)
	}
	call(admin, http.MethodPut, "/rest/api/3/project/"+closedKey+"/permissionscheme", fmt.Sprintf(`{"id":%v}`, closed["id"]), http.StatusOK)

	// The function answers for the searching user, so each sees their own
	// projects rather than everyone's.
	browse := `project IN projectsWhereUserHasPermission("BROWSE_PROJECTS")`
	if got := total(member, browse); got != 1 {
		t.Fatalf("member browses %v projects' work, want 1", got)
	}
	if got := total(admin, browse); got != 2 {
		t.Fatalf("admin browses %v projects' work, want 2", got)
	}
	// A permission nobody holds in either project matches nothing, which shows
	// the permission argument is read rather than ignored.
	if got := total(admin, `project IN projectsWhereUserHasPermission("ADMINISTER_PROJECTS")`); got != 2 {
		t.Fatalf("admin administers %v projects' work, want 2", got)
	}
	if got := total(member, `project IN projectsWhereUserHasPermission("ADMINISTER_PROJECTS")`); got != 0 {
		t.Fatalf("member administers %v projects' work, want 0", got)
	}
	// Confluence's spelling compiles the same way.
	if got := total(member, `project IN spacesWhereUserHasPermission("BROWSE_PROJECTS")`); got != 1 {
		t.Fatalf("member's spaces = %v, want 1", got)
	}
	// The function needs exactly one permission.
	call(member, http.MethodGet, "/rest/api/3/search?jql="+url.QueryEscape(`project IN projectsWhereUserHasPermission()`), "", http.StatusBadRequest)
}
