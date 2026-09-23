package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestAWorkItemReadsInTheLanguageItsReaderChose holds the point of translating
// a site: the person who chose a language sees the site's work types,
// priorities and statuses in it, and everybody else sees the site's own words.
func TestAWorkItemReadsInTheLanguageItsReaderChose(t *testing.T) {
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
	workspaceID, readerID, otherID, projectID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("prj")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Translation test')`, workspaceID)
	for _, id := range []string{readerID, otherID} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, id, id+"@example.invalid")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, id)
	}
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Translated project','wf_default')`,
		projectID, workspaceID, "TR"+strings.ToUpper(projectID[len(projectID)-5:]))
	t.Cleanup(func() {
		exec(`DELETE FROM sessions WHERE user_id IN ($1,$2)`, readerID, otherID)
		exec(`DELETE FROM jira_user_preferences WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issue_metadata_translations WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id IN ($1,$2)`, readerID, otherID)
	})
	service := &commands.Service{Store: st}
	issue, _, err := service.CreateIssue(ctx, commands.CreateIssueInput{
		ActorID: readerID, WorkspaceID: workspaceID, ProjectIDOrKey: projectID,
		Summary: "Le paiement a échoué", IssueTypeID: "it_task",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, translation := range []store.MetadataTranslation{
		{EntityType: "issuetype", EntityID: issue.IssueType.ID, Locale: "fr", Name: "Tâche"},
		{EntityType: "status", EntityID: issue.Status.ID, Locale: "fr", Name: "À faire"},
	} {
		if err := st.SaveIssueMetadataTranslation(ctx, workspaceID, readerID, translation); err != nil {
			t.Fatal(err)
		}
	}

	h := &Handler{Store: st, Commands: service, WorkspaceSlug: workspaceID}
	read := func(userID string) string {
		t.Helper()
		token, _, err := authn.LoginOIDC(ctx, st, userID, "id-token", "https://issuer.example.invalid", userID+"-subject", "")
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodGet, "/browse/"+issue.Key, nil)
		request.AddCookie(&http.Cookie{Name: "zzira_session", Value: token})
		user := h.currentUser(request)
		if user == nil {
			t.Fatal("the session did not identify anybody")
		}
		view, err := h.buildIssueView(request, user, workspaceID, issue.Key)
		if err != nil {
			t.Fatal(err)
		}
		return view.Issue.IssueType.Name + " · " + view.Issue.Status.Name
	}

	if read(otherID) != "Task · To Do" {
		t.Fatalf("a reader who chose no language reads %q", read(otherID))
	}
	if err := st.SetUserPreference(ctx, workspaceID, readerID, store.UserPreferenceLocaleKey, "fr"); err != nil {
		t.Fatal(err)
	}
	if got := read(readerID); got != "Tâche · À faire" {
		t.Fatalf("the reader who chose French reads %q", got)
	}
	if got := read(otherID); got != "Task · To Do" {
		t.Fatalf("the other reader now reads %q", got)
	}
	// A list of work reads in the same language, and so do the statuses a
	// filter offers.
	listed := []*models.Issue{{ID: issue.ID, IssueType: issue.IssueType, Status: issue.Status}}
	french := h.readerMetadataNames(ctx, workspaceID, readerID)
	translateIssues(french, listed)
	if listed[0].Status.Name != "À faire" || listed[0].IssueType.Name != "Tâche" {
		t.Fatalf("the list reads %q · %q", listed[0].IssueType.Name, listed[0].Status.Name)
	}
	offered := []models.Status{{ID: issue.Status.ID, Name: issue.Status.Name}}
	translateStatuses(french, offered)
	if offered[0].Name != "À faire" {
		t.Fatalf("the filter offers %q", offered[0].Name)
	}
	// Nobody else's page changes: a reader with no language reads the site's.
	plain := []*models.Issue{{ID: issue.ID, IssueType: issue.IssueType, Status: issue.Status}}
	translateIssues(h.readerMetadataNames(ctx, workspaceID, otherID), plain)
	if plain[0].Status.Name != "To Do" {
		t.Fatalf("the other reader's list reads %q", plain[0].Status.Name)
	}

	// The site's own words are what a search takes, so they are unchanged.
	stored, err := st.IssueByIDOrKey(ctx, workspaceID, issue.Key)
	if err != nil || stored.Status.Name != "To Do" {
		t.Fatalf("the stored status is %+v, %v", stored.Status, err)
	}
}
