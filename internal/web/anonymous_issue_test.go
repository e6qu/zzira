package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestAnonymousIssueBrowseUsesPublicPermissionsWithoutIdentity(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	f.handler.AnonymousAccess = true
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.store.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	project := func(key string) *models.Project {
		t.Helper()
		p, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: key, Name: key + " project", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	public, private := project("PUB"), project("PRV")
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, ws)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, ws)
	})
	create := func(p *models.Project, summary string) *models.Issue {
		t.Helper()
		issue, _, err := f.store.CreateIssue(ctx, f.ownerID, p.ID, summary, json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Public description","marks":[{"type":"strong"}]}]}]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
		if err != nil {
			t.Fatal(err)
		}
		return issue
	}
	open, secret, restricted := create(public, "Public journey"), create(private, "Private summary"), create(public, "Restricted summary")
	scheme, err := f.store.CreatePermissionScheme(ctx, ws, f.ownerID, "Public browser", "", []store.PermissionGrantInput{{Permission: "BROWSE_PROJECTS", HolderType: "anyone"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.store.AssignPermissionScheme(ctx, ws, f.ownerID, public.ID, scheme.ID); err != nil {
		t.Fatal(err)
	}
	security, err := f.store.CreateIssueSecurityScheme(ctx, ws, f.ownerID, "Private work", "", []store.SecurityLevelInput{{Name: "Private", Members: []store.SecurityLevelMemberInput{{Type: "user", Parameter: f.ownerID}}}})
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE projects SET security_scheme_id=$2 WHERE id=$1`, public.ID, security.ID)
	exec(`UPDATE issues SET security_level_id=$2 WHERE id=$1`, restricted.ID, security.Levels[0].ID)
	exec(`UPDATE issues SET parent_id=$2 WHERE id=$1`, open.ID, secret.ID)
	for _, c := range []struct {
		body       string
		visibility store.CommentVisibility
	}{{"Public comment", store.CommentVisibility{}}, {"Private role comment", store.CommentVisibility{Type: "role", Value: "10002"}}, {"Private group comment", store.CommentVisibility{Type: "group", Value: "missing-group"}}} {
		raw, _ := json.Marshal(c.body)
		if _, _, err = f.store.CreateCommentWithVisibility(ctx, f.ownerID, ws, open.ID, raw, c.visibility); err != nil {
			t.Fatal(err)
		}
	}
	att, _, err := f.store.CreateAttachment(ctx, f.ownerID, ws, open.ID, "public.txt", "text/plain", 4, "fixture-public")
	if err != nil {
		t.Fatal(err)
	}
	internal, _, err := f.store.CreateAttachment(ctx, f.ownerID, ws, open.ID, "internal-secret.txt", "text/plain", 4, "fixture-private")
	if err != nil {
		t.Fatal(err)
	}
	var deskID, requestTypeID string
	if err = f.store.Pool.QueryRow(ctx, `INSERT INTO service_desks(workspace_id,project_id,portal_name) VALUES($1,$2,'Public service') RETURNING id`, ws, public.ID).Scan(&deskID); err != nil {
		t.Fatal(err)
	}
	if err = f.store.Pool.QueryRow(ctx, `INSERT INTO service_request_types(service_desk_id,name,issue_type_id) VALUES($1,'Public request','it_task') RETURNING id`, deskID).Scan(&requestTypeID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO service_requests(issue_id,workspace_id,service_desk_id,request_type_id,customer_id) VALUES($1,$2,$3,$4,$5)`, open.ID, ws, deskID, requestTypeID, f.ownerID)
	exec(`INSERT INTO service_request_comments(comment_id,request_issue_id,public) SELECT id,$1,false FROM comments WHERE issue_id=$1 AND body='"Private role comment"'::jsonb`, open.ID)
	serviceComment, _, err := f.store.CreateComment(ctx, f.ownerID, ws, open.ID, json.RawMessage(`"Internal service note"`))
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO service_request_comments(comment_id,request_issue_id,public) VALUES($1,$2,false)`, serviceComment.ID, open.ID)
	exec(`INSERT INTO service_request_attachments(attachment_id,request_issue_id,comment_id,public) SELECT $1,$2,id,false FROM comments WHERE issue_id=$2 ORDER BY id LIMIT 1`, internal.ID, open.ID)
	browse := func(key, cookie, authorization string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/browse/"+key, nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "zzira_session", Value: cookie})
		}
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		w := httptest.NewRecorder()
		f.handler.BrowseIssue(w, r, key)
		if w.Code != want {
			t.Fatalf("browse %s: %d want %d: %s", key, w.Code, want, w.Body.String())
		}
		return w
	}
	page := browse(open.Key, "", "", 200)
	for _, text := range []string{"Public journey", "Public comment", "public.txt", "<strong>Public description</strong>"} {
		if !strings.Contains(page.Body.String(), text) {
			t.Fatalf("missing %q", text)
		}
	}
	for _, text := range []string{secret.Key, "Private summary", "Private role comment", "Private group comment", "Internal service note", "internal-secret.txt", f.ownerID + "@example.invalid", "app.js", "worker.js", "zzira-worker", "<form"} {
		if strings.Contains(page.Body.String(), text) {
			t.Fatalf("guest exposed %q", text)
		}
	}
	if page.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("guest page can be cached after revocation")
	}
	if !strings.Contains(page.Body.String(), "/rest/api/3/attachment/content/") || att.JiraID == 0 {
		t.Fatal("public attachment download missing")
	}
	browse(open.Key, "", "", 200)
	var views int
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM issue_views WHERE workspace_id=$1`, ws).Scan(&views); err != nil || views != 0 {
		t.Fatalf("anonymous history: %d %v", views, err)
	}
	browse(secret.Key, "", "", 404)
	browse(restricted.Key, "", "", 404)
	browse("PUB-99999", "", "", 404)
	browse(open.Key, "invalid", "", 303)
	browse(open.Key, "", "Bearer invalid", 303)
	f.handler.AnonymousAccess = false
	browse(open.Key, "", "", 303)
	f.handler.AnonymousAccess = true
	browse(open.Key, f.tokens[f.ownerID], "", 200)
	exec(`DELETE FROM permission_scheme_grants WHERE scheme_id=$1`, scheme.ID)
	browse(open.Key, "", "", 404)
}
