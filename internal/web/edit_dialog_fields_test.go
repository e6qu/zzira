package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestEditDialogCanChangeFieldsAbsentFromTheViewScreen(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "EDF", Name: "Editing fields", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
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
	parent, _, err := f.store.CreateIssue(ctx, f.ownerID, project.ID, "Parent epic", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_epic", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	issue, _, err := f.store.CreateIssue(ctx, f.ownerID, project.ID, "Editable work", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	screen := func(name string, fields ...string) string {
		t.Helper()
		s, err := f.store.CreateScreen(ctx, ws, f.ownerID, name, "")
		if err != nil {
			t.Fatal(err)
		}
		tabs, err := f.store.ScreenTabs(ctx, ws, s.ID)
		if err != nil || len(tabs) != 1 {
			t.Fatalf("tabs: %+v %v", tabs, err)
		}
		for _, field := range append([]string{"summary"}, fields...) {
			if _, err := f.store.AddScreenTabField(ctx, ws, f.ownerID, s.ID, tabs[0].ID, field); err != nil {
				t.Fatal(err)
			}
		}
		return s.ID
	}
	editScreen := screen("Editing all fields", "priority", "labels", "duedate", "parent", "resolution", "timetracking")
	viewScreen := screen("Reading only summary")
	screens, err := f.store.CreateScreenScheme(ctx, ws, f.ownerID, "Editing field screens", "", map[string]string{"default": editScreen, "edit": editScreen, "view": viewScreen})
	if err != nil {
		t.Fatal(err)
	}
	types, err := f.store.CreateIssueTypeScreenScheme(ctx, ws, f.ownerID, "Editing field types", "", []models.IssueTypeScreenSchemeItem{{IssueTypeID: store.DefaultIssueTypeMapping, ScreenSchemeID: screens.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AssignIssueTypeScreenScheme(ctx, ws, f.ownerID, project.ID, types.ID); err != nil {
		t.Fatal(err)
	}
	request := func(method string, values url.Values) *http.Request {
		r := httptest.NewRequest(method, "/issues/"+issue.Key+"/edit", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
		return r
	}
	edit, err := f.handler.buildEditDialogView(ctx, ws, f.ownerID, issue, map[string]bool{"RESOLVE_ISSUES": true, "SCHEDULE_ISSUES": true})
	if err != nil || len(edit.Priorities) == 0 || len(edit.Resolutions) == 0 || len(edit.ParentOptions) != 1 || edit.ParentOptions[0].Key != parent.Key {
		t.Fatalf("edit metadata: %+v %v", edit, err)
	}
	values := url.Values{"summary": {issue.Summary}, "priority": {"pr_high"}, "labels": {"release, verified"}, "duedate": {"2026-12-01"}, "parent": {parent.Key}, "resolution": {"Done"}, "originalEstimate": {"3h"}, "remainingEstimate": {"1h"}}
	w := httptest.NewRecorder()
	f.handler.EditIssue(w, request(http.MethodPost, values), issue.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("edit %d: %s", w.Code, w.Body.String())
	}
	current, err := f.store.IssueByIDOrKey(ctx, ws, issue.Key)
	if err != nil || current.Priority == nil || current.Priority.ID != "pr_high" || len(current.Labels) != 2 || current.DueDate != "2026-12-01" || current.Parent == nil || current.Parent.ID != parent.ID || current.Resolution == nil || current.Resolution.Name != "Done" || current.OriginalEstimateSeconds == nil || *current.OriginalEstimateSeconds != 10800 || current.RemainingEstimateSeconds == nil || *current.RemainingEstimateSeconds != 3600 {
		t.Fatalf("edit did not persist all fields: %+v %v", current, err)
	}
	// Invalid durations refuse the entire edit before a summary or parent write.
	values.Set("summary", "Refused summary")
	values.Set("originalEstimate", "not a duration")
	w = httptest.NewRecorder()
	f.handler.EditIssue(w, request(http.MethodPost, values), issue.Key)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid estimate %d", w.Code)
	}
	unchanged, err := f.store.IssueByIDOrKey(ctx, ws, issue.Key)
	if err != nil || unchanged.UpdatedSeq != current.UpdatedSeq || unchanged.Summary != issue.Summary {
		t.Fatalf("invalid edit changed work: %+v %v", unchanged, err)
	}
	// A default parent choice preserves the relationship, including when a
	// reader cannot name the existing parent in the picker.
	w = httptest.NewRecorder()
	f.handler.EditIssue(w, request(http.MethodPost, url.Values{"summary": {issue.Summary}, "parent": {"__keep_parent__"}, "originalEstimate": {""}}), issue.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("partial edit %d: %s", w.Code, w.Body.String())
	}
	current, err = f.store.IssueByIDOrKey(ctx, ws, issue.Key)
	if err != nil || current.Parent == nil || current.Parent.ID != parent.ID || current.OriginalEstimateSeconds != nil || current.RemainingEstimateSeconds == nil || *current.RemainingEstimateSeconds != 3600 || current.Priority.ID != "pr_high" || len(current.Labels) != 2 {
		t.Fatalf("omitted fields changed: %+v %v", current, err)
	}
	var configuration string
	if err := f.store.Pool.QueryRow(ctx, `SELECT mapping.configuration_id::text FROM project_field_configuration_schemes assigned JOIN field_configuration_scheme_items mapping ON mapping.scheme_id=assigned.scheme_id WHERE assigned.project_id=$1 AND mapping.issue_type_id=$2`, project.ID, store.DefaultIssueTypeMapping).Scan(&configuration); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetFieldConfigurationItems(ctx, ws, f.ownerID, configuration, []models.FieldConfigurationItem{{FieldID: "timetracking", IsRequired: true}}); err != nil {
		t.Fatal(err)
	}
	clear := store.ClearEstimate
	original := int64(7200)
	if _, _, err := f.handler.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{ActorID: f.ownerID, WorkspaceID: ws, IssueIDOrKey: issue.Key, OriginalEstimate: &original}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.handler.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{ActorID: f.ownerID, WorkspaceID: ws, IssueIDOrKey: issue.Key, OriginalEstimate: &clear}); err != nil {
		t.Fatalf("clearing one required estimate lost its retained counterpart: %v", err)
	}
	if _, _, err := f.handler.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{ActorID: f.ownerID, WorkspaceID: ws, IssueIDOrKey: issue.Key, RemainingEstimate: &clear}); err == nil {
		t.Fatal("cleared the last required estimate")
	}
	if err := f.store.SetFieldConfigurationItems(ctx, ws, f.ownerID, configuration, []models.FieldConfigurationItem{{FieldID: "timetracking", IsHidden: true}}); err != nil {
		t.Fatal(err)
	}
	seconds := int64(7200)
	if _, _, err := f.handler.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{ActorID: f.ownerID, WorkspaceID: ws, IssueIDOrKey: issue.Key, OriginalEstimate: &seconds}); err == nil {
		t.Fatal("set a hidden estimate")
	}
}
