package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestSecurityAssignmentRequiresCompleteMappingsBeforeQueuing(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx := context.Background()
	ws := f.handler.WorkspaceSlug
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, query, ws); err != nil {
				t.Error(err)
			}
		}
	})
	project, err := f.handler.Commands.CreateProject(ctx, f.ownerID, ws, commands.CreateProjectInput{Key: "MAP", Name: "Mapped work", ProjectTypeKey: "software", LeadAccountID: f.ownerID})
	if err != nil {
		t.Fatal(err)
	}
	old, err := f.store.CreateIssueSecurityScheme(ctx, ws, f.ownerID, "Old boundary", "", []store.SecurityLevelInput{{Name: "Active work"}, {Name: "Archived work"}, {Name: "Unused level"}})
	if err != nil {
		t.Fatal(err)
	}
	target, err := f.store.CreateIssueSecurityScheme(ctx, ws, f.ownerID, "New boundary", "", []store.SecurityLevelInput{{Name: "Delivery leads"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(ctx, `UPDATE projects SET security_scheme_id=$2 WHERE id=$1`, project.ID, old.ID); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 2; index++ {
		issue, _, err := f.handler.Commands.CreateIssue(ctx, commands.CreateIssueInput{ActorID: f.ownerID, WorkspaceID: ws, ProjectIDOrKey: project.ID, Summary: "Mapped work", IssueTypeID: "it_task", SecurityLevelID: old.Levels[index].ID})
		if err != nil {
			t.Fatal(err)
		}
		if index == 1 {
			if _, err := f.store.Pool.Exec(ctx, `UPDATE issues SET archived_at=now() WHERE id=$1`, issue.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	usage, err := f.store.ProjectIssueSecurityLevelUsage(ctx, ws, project.ID)
	if err != nil || len(usage) != 2 {
		t.Fatalf("usage omitted active or archived work: %+v, %v", usage, err)
	}
	if usage, err := f.store.ProjectIssueSecurityLevelUsage(ctx, "other-workspace", project.ID); err != nil || len(usage) != 0 {
		t.Fatalf("usage crossed workspace boundaries: %+v, %v", usage, err)
	}
	post := func(user string, form url.Values, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := f.post(t, func(w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("id", target.ID)
			f.handler.IssueSecuritySchemeMutation(w, r)
		}, user, "/settings/issue-security-schemes/"+target.ID, "", form)
		if w.Code != want {
			t.Fatalf("assign scheme: got %d, want %d: %s", w.Code, want, w.Body.String())
		}
		return w
	}
	form := url.Values{"action": {"assign-project"}, "project": {project.ID}}
	post(f.otherID, form, http.StatusForbidden)
	w := post(f.ownerID, form, http.StatusOK)
	if !strings.Contains(w.Body.String(), "Map security levels") || !strings.Contains(w.Body.String(), "Unused level") || !strings.Contains(w.Body.String(), "Archived work (1 work item)") {
		t.Fatal("mapping preview omitted current or unused levels")
	}
	form.Set("mapped", "true")
	form.Set("level."+old.Levels[0].ID, target.Levels[0].ID)
	w = post(f.ownerID, form, http.StatusBadRequest)
	if !strings.Contains(w.Body.String(), "Choose a replacement for every security level.") || !strings.Contains(w.Body.String(), `value="`+target.Levels[0].ID+`" selected`) {
		t.Fatal("incomplete mapping did not retain its valid selection")
	}
	form.Set("level."+old.Levels[1].ID, old.Levels[0].ID)
	form.Set("level."+old.Levels[2].ID, target.Levels[0].ID)
	post(f.ownerID, form, http.StatusBadRequest)
	var tasks int
	if err := f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM api_tasks WHERE workspace_id=$1`, ws).Scan(&tasks); err != nil || tasks != 0 {
		t.Fatalf("a preview/refused mapping queued a migration: %d, %v", tasks, err)
	}
	form.Set("level."+old.Levels[1].ID, target.Levels[0].ID)
	post(f.ownerID, form, http.StatusSeeOther)
	if err := (&store.APITaskRunner{Store: f.store}).DrainOnce(ctx, ws); err != nil {
		t.Fatal(err)
	}
	assigned, _, err := f.store.AssignedIssueSecurityScheme(ctx, ws, project.ID, true)
	if err != nil || assigned.ID != target.ID {
		t.Fatalf("scheme was not assigned: %+v, %v", assigned, err)
	}
	var moved int
	if err := f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE project_id=$1 AND security_level_id=$2`, project.ID, target.Levels[0].ID).Scan(&moved); err != nil || moved != 2 {
		t.Fatalf("active/archived work not migrated: %d, %v", moved, err)
	}
}
