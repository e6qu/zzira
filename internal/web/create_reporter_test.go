package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestCreateReporterChoicesPersistenceAndPermissions(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, query, ws); err != nil {
				t.Error(err)
			}
		}
	})
	project, err := f.handler.Commands.CreateProject(ctx, f.ownerID, ws, commands.CreateProjectInput{Key: "REP", Name: "Reporter forms", ProjectTypeKey: "software", LeadAccountID: f.ownerID})
	if err != nil {
		t.Fatal(err)
	}
	scheme, err := f.store.CreatePermissionScheme(ctx, ws, f.ownerID, "Reporter scope", "", []store.PermissionGrantInput{{Permission: "BROWSE_PROJECTS", HolderType: "anyone"}, {Permission: "CREATE_ISSUES", HolderType: "anyone"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.store.AssignPermissionScheme(ctx, ws, f.ownerID, project.ID, scheme.ID); err != nil {
		t.Fatal(err)
	}
	assertChoices := func(user string, count int) {
		t.Helper()
		data, err := f.handler.buildCreateDialogData(ctx, ws, user, map[string]string{"project": "REP"})
		if err != nil {
			t.Fatal(err)
		}
		if data.Values["reporter"] != user {
			t.Fatalf("default reporter=%q want %q", data.Values["reporter"], user)
		}
		found := false
		for _, field := range data.Selected.Fields {
			if field.ID == "reporter" {
				found = true
				if len(field.Options) != count {
					t.Fatalf("reporter options=%+v", field.Options)
				}
			}
		}
		if !found {
			t.Fatal("reporter missing from default screen")
		}
	}
	outsider := store.NewID("usr")
	if _, err := f.store.Pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Outside reporter')`, outsider, outsider+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.store.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, outsider); err != nil {
			t.Error(err)
		}
	})
	outside := f.post(t, f.handler.CreateIssue, f.ownerID, "/issues", "", url.Values{"project": {"REP"}, "issuetype": {"it_task"}, "summary": {"Outside reporter refused"}, "reporter": {outsider}})
	if outside.Code != http.StatusOK || !strings.Contains(outside.Body.String(), "reporter is not an active workspace member") {
		t.Fatalf("outside Reporter accepted: %d %s", outside.Code, outside.Body.String())
	}
	if _, _, err := f.store.CreateIssueForReporter(ctx, f.ownerID, outsider, project.ID, "Outside reporter refused", nil, "st_todo", "it_task", "", "", nil, nil, "", ""); err == nil {
		t.Fatal("transaction accepted an outside Reporter")
	}
	var outsideCount int
	if err := f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE workspace_id=$1 AND summary='Outside reporter refused'`, ws).Scan(&outsideCount); err != nil || outsideCount != 0 {
		t.Fatalf("outside Reporter persisted: %d %v", outsideCount, err)
	}
	assertChoices(f.ownerID, 2)
	assertChoices(f.otherID, 1)
	submit := func(user, reporter, summary string) string {
		t.Helper()
		response := f.post(t, f.handler.CreateIssue, user, "/issues", "", url.Values{"project": {"REP"}, "issuetype": {"it_task"}, "summary": {summary}, "reporter": {reporter}})
		if response.Code != http.StatusNoContent {
			t.Fatalf("create=%d %s", response.Code, response.Body.String())
		}
		return strings.TrimPrefix(response.Header().Get("HX-Redirect"), "/browse/")
	}
	refusedSummary := f.post(t, f.handler.CreateIssue, f.ownerID, "/issues", "", url.Values{"project": {"REP"}, "issuetype": {"it_task"}, "summary": {""}, "reporter": {f.otherID}})
	if refusedSummary.Code != http.StatusOK || !strings.Contains(refusedSummary.Body.String(), `value="`+f.otherID+`" selected`) {
		t.Fatal("refused create discarded the selected reporter")
	}
	key := submit(f.ownerID, f.otherID, "Reported for a colleague")
	issue, err := f.store.IssueByIDOrKey(ctx, ws, key)
	if err != nil || issue.Reporter == nil || issue.Reporter.ID != f.otherID {
		t.Fatalf("stored reporter=%+v err=%v", issue, err)
	}
	refused := f.post(t, f.handler.CreateIssue, f.otherID, "/issues", "", url.Values{"project": {"REP"}, "issuetype": {"it_task"}, "summary": {"Forged reporter"}, "reporter": {f.ownerID}})
	if refused.Code != http.StatusOK || !strings.Contains(refused.Body.String(), "permission to change the reporter") {
		t.Fatalf("forged create status=%d; expected reporter permission refusal", refused.Code)
	}
	var refusedCount int
	if err := f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE workspace_id=$1 AND summary='Forged reporter'`, ws).Scan(&refusedCount); err != nil || refusedCount != 0 {
		t.Fatalf("refused create persisted=%d err=%v", refusedCount, err)
	}
	key = submit(f.otherID, "", "Self-reported work")
	issue, err = f.store.IssueByIDOrKey(ctx, ws, key)
	if err != nil || issue.Reporter == nil || issue.Reporter.ID != f.otherID {
		t.Fatalf("implicit reporter=%+v err=%v", issue, err)
	}
	if _, err := f.store.CreatePermissionGrant(ctx, ws, f.ownerID, scheme.ID, store.PermissionGrantInput{Permission: "MODIFY_REPORTER", HolderType: "user", HolderValue: f.otherID}); err != nil {
		t.Fatal(err)
	}
	assertChoices(f.otherID, 2)
	key = submit(f.otherID, f.ownerID, "Authorized report for a colleague")
	issue, err = f.store.IssueByIDOrKey(ctx, ws, key)
	if err != nil || issue.Reporter == nil || issue.Reporter.ID != f.ownerID {
		t.Fatalf("granted reporter=%+v err=%v", issue, err)
	}
	configurations, err := f.store.FieldConfigurations(ctx, ws, nil)
	if err != nil || len(configurations) == 0 {
		t.Fatalf("default field configuration missing: %v", err)
	}
	configurationID := configurations[0].ID
	if err := f.store.SetFieldConfigurationItems(ctx, ws, f.ownerID, configurationID, []models.FieldConfigurationItem{{FieldID: "reporter", IsRequired: true}}); err != nil {
		t.Fatal(err)
	}
	submit(f.otherID, "", "Required reporter defaults to the creator")
	if err := f.store.SetFieldConfigurationItems(ctx, ws, f.ownerID, configurationID, []models.FieldConfigurationItem{{FieldID: "reporter", IsHidden: true}}); err != nil {
		t.Fatal(err)
	}
	data, err := f.handler.buildCreateDialogData(ctx, ws, f.otherID, map[string]string{"project": "REP"})
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range data.Selected.Fields {
		if field.ID == "reporter" {
			t.Fatal("hidden reporter rendered")
		}
	}
	submit(f.otherID, "", "Hidden reporter defaults to the creator")
	refusedHidden := f.post(t, f.handler.CreateIssue, f.ownerID, "/issues", "", url.Values{"project": {"REP"}, "issuetype": {"it_task"}, "summary": {"Hidden reporter override"}, "reporter": {f.otherID}})
	if !strings.Contains(refusedHidden.Body.String(), "reporter is hidden") {
		t.Fatal("hidden reporter override accepted")
	}

}
