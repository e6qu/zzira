package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestEmptyAssignedScreensKeepOnlyRequiredContextAndRefuseEdits(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "EMP", Name: "Empty forms", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, q, ws); err != nil {
				t.Error(err)
			}
		}
	})
	issue, _, err := f.store.CreateIssue(ctx, f.ownerID, project.ID, "Empty screen journey", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	screen, err := f.store.CreateScreen(ctx, ws, f.ownerID, "No optional fields", "")
	if err != nil {
		t.Fatal(err)
	}
	schemes, err := f.store.CreateScreenScheme(ctx, ws, f.ownerID, "Empty screen forms", "", map[string]string{"default": screen.ID})
	if err != nil {
		t.Fatal(err)
	}
	types, err := f.store.CreateIssueTypeScreenScheme(ctx, ws, f.ownerID, "Empty work type forms", "", []models.IssueTypeScreenSchemeItem{{IssueTypeID: store.DefaultIssueTypeMapping, ScreenSchemeID: schemes.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AssignIssueTypeScreenScheme(ctx, ws, f.ownerID, project.ID, types.ID); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"create", "edit", "view"} {
		fields, err := f.store.ResolveScreenFields(ctx, ws, project.ID, issue.IssueType.ID, operation)
		if err != nil || fields == nil || len(fields) != 0 {
			t.Fatalf("single %s screen lost empty assignment: %v %v", operation, fields, err)
		}
		all, err := f.store.ResolveScreenFieldsByProject(ctx, ws, operation)
		if err != nil || all[project.ID][issue.IssueType.ID] == nil || len(all[project.ID][issue.IssueType.ID]) != 0 {
			t.Fatalf("batch %s screen lost empty assignment: %+v %v", operation, all, err)
		}
		tabs, err := f.store.ResolveScreenTabsByProject(ctx, ws, operation)
		if err != nil || tabs[project.ID][issue.IssueType.ID] == nil || len(tabs[project.ID][issue.IssueType.ID]) != 0 {
			t.Fatalf("batch %s tabs lost empty assignment: %+v %v", operation, tabs, err)
		}
	}
	metadata, err := f.store.IssueCreateMetadata(ctx, ws, f.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range metadata.Projects {
		if entry.Project.ID != project.ID {
			continue
		}
		found = true
		fields := entry.FieldsForIssueType(issue.IssueType.ID)
		if len(fields) != 3 {
			t.Fatalf("empty create screen offered optional fields: %+v", fields)
		}
		for _, field := range fields {
			if field.ID != "summary" && field.ID != "project" && field.ID != "issuetype" {
				t.Fatalf("unexpected create field: %s", field.ID)
			}
		}
	}
	if !found {
		t.Fatal("project missing from create metadata")
	}
	edit, err := f.handler.buildEditDialogView(ctx, ws, f.ownerID, issue, map[string]bool{"MODIFY_REPORTER": true, "RESOLVE_ISSUES": true, "SCHEDULE_ISSUES": true})
	if err != nil || edit.CanModifyReporter || edit.EnvironmentField != nil || len(edit.ChoiceFields) != 0 || len(edit.CustomFields) != 0 {
		t.Fatalf("empty edit screen exposed fields: %+v %v", edit, err)
	}
	for field, shown := range edit.EditFields {
		if shown {
			t.Fatalf("empty editor exposes %s", field)
		}
	}
	bulk, err := f.handler.Commands.BulkReporterField(ctx, ws, f.ownerID, []*models.Issue{issue})
	if err != nil || bulk != nil {
		t.Fatalf("empty edit screen exposes bulk Reporter: %+v %v", bulk, err)
	}
	summary, labels, reporter := "Should not persist", []string{"forged"}, f.ownerID
	for _, in := range []commands.UpdateIssueInput{{Summary: &summary, Labels: &labels}, {ReporterID: &reporter}, {Fields: map[string]json.RawMessage{"environment": json.RawMessage(`{"type":"doc","version":1,"content":[]}`)}}, {VersionOperations: map[string][]map[string]json.RawMessage{"versions": {{"set": json.RawMessage("[]")}}}}} {
		in.ActorID, in.WorkspaceID, in.IssueIDOrKey = f.ownerID, ws, issue.Key
		if _, _, err := f.handler.Commands.UpdateIssue(ctx, in); err == nil {
			t.Fatal("forged edit passed an empty screen")
		}
	}
	if _, _, err := f.handler.Commands.CreateIssue(ctx, commands.CreateIssueInput{ActorID: f.ownerID, WorkspaceID: ws, ProjectIDOrKey: project.ID, IssueTypeID: issue.IssueType.ID, Summary: "Refused environment create", Fields: map[string]json.RawMessage{"environment": json.RawMessage(`{"type":"doc","version":1,"content":[]}`)}}); err == nil {
		t.Fatal("forged environment create passed an empty screen")
	}
	unchanged, err := f.store.IssueByIDOrKey(ctx, ws, issue.Key)
	if err != nil || unchanged.UpdatedSeq != issue.UpdatedSeq || unchanged.Summary != issue.Summary {
		t.Fatalf("refused edit changed issue: %+v %v", unchanged, err)
	}
	r := httptest.NewRequest(http.MethodGet, "/browse/"+issue.Key, nil)
	r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
	w := httptest.NewRecorder()
	f.handler.BrowseIssue(w, r, issue.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("browse empty screen: %d", w.Code)
	}
	for _, selector := range []string{`id="field-labels"`, `id="field-priority"`, `id="field-reporter"`, `id="field-components"`, `id="field-description"`} {
		if strings.Contains(w.Body.String(), selector) {
			t.Fatalf("empty view screen rendered %s", selector)
		}
	}
	// A missing assignment remains distinct from an intentionally empty one.
	if _, err := f.store.Pool.Exec(ctx, `DELETE FROM project_issue_type_screen_schemes WHERE project_id=$1`, project.ID); err != nil {
		t.Fatal(err)
	}
	fields, err := f.store.ResolveScreenFields(ctx, ws, project.ID, issue.IssueType.ID, "edit")
	if err != nil || fields != nil {
		t.Fatalf("ungoverned form is not nil: %v %v", fields, err)
	}
	edit, err = f.handler.buildEditDialogView(ctx, ws, f.ownerID, issue, map[string]bool{"MODIFY_REPORTER": true})
	if err != nil || !edit.EditFields["labels"] || !edit.CanModifyReporter {
		t.Fatalf("ungoverned form lost legacy fallback: %+v %v", edit, err)
	}
}
