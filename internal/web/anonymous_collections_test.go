package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestAnonymousCollectionsPageOnlyVisibleWorkWithoutHistory(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	f.handler.AnonymousAccess = true
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := f.store.Pool.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	project := func(key string) *models.Project {
		t.Helper()
		p, e := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: key, Name: key + " collection", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	public, private := project("PUBLIC"), project("PRIVATE")
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`} {
			if _, e := f.store.Pool.Exec(ctx, q, ws); e != nil {
				t.Error(e)
			}
		}
	})
	scheme, e := f.store.CreatePermissionScheme(ctx, ws, f.ownerID, "Public collections", "", []store.PermissionGrantInput{{Permission: "BROWSE_PROJECTS", HolderType: "anyone"}})
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = f.store.AssignPermissionScheme(ctx, ws, f.ownerID, public.ID, scheme.ID); e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 51; n++ {
		id, key := store.NewID("prj"), fmt.Sprintf("A%02d", n)
		exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,$3,'wf_default')`, id, ws, key)
		exec(`INSERT INTO project_permission_schemes(project_id,workspace_id,scheme_id) VALUES($1,$2,$3) ON CONFLICT(project_id) DO UPDATE SET scheme_id=EXCLUDED.scheme_id`, id, ws, scheme.ID)
	}
	exec(`UPDATE projects SET lifecycle_state='ARCHIVED' WHERE workspace_id=$1 AND key='A50'`, ws)
	create := func(p *models.Project, summary string) *models.Issue {
		t.Helper()
		issue, _, e := f.store.CreateIssue(ctx, f.ownerID, p.ID, summary, json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
		if e != nil {
			t.Fatal(e)
		}
		return issue
	}
	for n := 0; n < 52; n++ {
		create(public, fmt.Sprintf("Public row %02d", n))
	}
	secret := create(private, "Private collection secret")
	secured := create(public, "Secured collection secret")
	archived := create(public, "Archived collection secret")
	exec(`UPDATE issues SET archived_at=now() WHERE id=$1`, archived.ID)
	security, e := f.store.CreateIssueSecurityScheme(ctx, ws, f.ownerID, "Restricted collections", "", []store.SecurityLevelInput{{Name: "Private", Members: []store.SecurityLevelMemberInput{{Type: "user", Parameter: f.ownerID}}}})
	if e != nil {
		t.Fatal(e)
	}
	exec(`UPDATE projects SET security_scheme_id=$2 WHERE id=$1`, public.ID, security.ID)
	exec(`UPDATE issues SET security_level_id=$2 WHERE id=$1`, secured.ID, security.Levels[0].ID)
	call := func(path, cookie string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "zzira_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		if strings.HasPrefix(r.URL.Path, "/issues/") {
			f.handler.ProjectIssues(w, r, strings.TrimPrefix(r.URL.Path, "/issues/"))
		} else {
			f.handler.ProjectsPage(w, r)
		}
		if w.Code != want {
			t.Fatalf("%s got %d want %d: %s", path, w.Code, want, w.Body.String())
		}
		return w
	}
	page := call("/projects", "", 200)
	for _, path := range []string{"/projects", "/issues/PUBLIC"} {
		w := call(path, "", 200)
		body := w.Body.String()
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("public collection can be cached")
		}
		for _, hidden := range []string{"PRIVATE collection", secret.Key, secured.Key, archived.Key, "Secured collection secret", "Archived collection secret", "app.js", "worker.js", "zzira-worker", "<form", f.ownerID + "@example.invalid"} {
			if strings.Contains(body, hidden) {
				t.Fatalf("%s exposed %q", path, hidden)
			}
		}
	}
	if strings.Contains(page.Body.String(), "A50") || strings.Contains(page.Body.String(), "PUBLIC collection") || !strings.Contains(page.Body.String(), "after=A49") {
		t.Fatal("projects did not page or exposed archived project")
	}
	second := call("/projects?after=A49", "", 200).Body.String()
	if !strings.Contains(second, "PUBLIC collection") || !strings.Contains(second, ">First page</a>") || strings.Contains(second, ">Next page</a>") {
		t.Fatal("last project page lacks visible project or paging recovery")
	}
	issues, next, e := f.store.PublicIssuesAfter(ctx, ws, public.ID, "")
	if e != nil || len(issues) != 50 || next == "" {
		t.Fatalf("first issue page %d next %q: %v", len(issues), next, e)
	}
	rest, last, e := f.store.PublicIssuesAfter(ctx, ws, public.ID, next)
	if e != nil || len(rest) != 2 || last != "" {
		t.Fatalf("last issue page %d next %q: %v", len(rest), last, e)
	}
	seen := map[string]bool{}
	for _, issue := range append(issues, rest...) {
		if seen[issue.ID] {
			t.Fatal("duplicate issue across pages")
		}
		seen[issue.ID] = true
	}
	call("/issues/PUBLIC?after="+url.QueryEscape(next), "", 200)
	call("/issues/PRIVATE", "", 404)
	call("/projects?status=archived", "", 404)
	call("/projects?status=trash", "", 404)
	call("/issues/PUBLIC?jql=summary~secret", "", 400)
	call("/projects?after="+strings.Repeat("A", 256), "", 400)
	for _, path := range []string{"/projects", "/issues/PUBLIC"} {
		call(path, "invalid", 303)
		call(path, f.tokens[f.ownerID], 200)
	}
	var views int
	if e = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM issue_views WHERE workspace_id=$1`, ws).Scan(&views); e != nil || views != 0 {
		t.Fatalf("anonymous issue history %d: %v", views, e)
	}
	if e = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM project_views WHERE workspace_id=$1`, ws).Scan(&views); e != nil || views != 0 {
		t.Fatalf("anonymous project history %d: %v", views, e)
	}
	exec(`DELETE FROM permission_scheme_grants WHERE workspace_id=$1 AND scheme_id=$2 AND holder_type='anyone'`, ws, scheme.ID)
	call("/issues/PUBLIC", "", 404)
	empty := call("/projects", "", 200).Body.String()
	if !strings.Contains(empty, "No public projects") || strings.Contains(empty, "PUBLIC collection") {
		t.Fatal("revoked public project remains visible")
	}
	f.handler.AnonymousAccess = false
	call("/projects", "", 303)
	call("/issues/PUBLIC", "", 303)
}
