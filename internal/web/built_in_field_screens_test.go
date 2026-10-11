package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestBuiltInIssueDetailsRespectViewEditScreensAndHiddenRules(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "BIS", Name: "Built-in screens", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
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
	description := json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Keep rich description","marks":[{"type":"strong"}]}]}]}`)
	issue, _, err := f.store.CreateIssue(ctx, f.ownerID, project.ID, "Built-in screen journey", description, "st_todo", "it_task", "pr_medium", f.ownerID, []string{"view-only-label"}, nil, "", "")
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
			t.Fatalf("tabs: %v %v", tabs, err)
		}
		for _, field := range append([]string{"summary"}, fields...) {
			if _, err := f.store.AddScreenTabField(ctx, ws, f.ownerID, s.ID, tabs[0].ID, field); err != nil {
				t.Fatal(err)
			}
		}
		return s.ID
	}
	editID := screen("Built-in editing", "duedate", "priority", "assignee")
	viewID := screen("Built-in reading", "description", "labels", "priority", "assignee")
	scheme, err := f.store.CreateScreenScheme(ctx, ws, f.ownerID, "Built-in screens", "", map[string]string{"default": editID, "edit": editID, "view": viewID})
	if err != nil {
		t.Fatal(err)
	}
	types, err := f.store.CreateIssueTypeScreenScheme(ctx, ws, f.ownerID, "Built-in types", "", []models.IssueTypeScreenSchemeItem{{IssueTypeID: store.DefaultIssueTypeMapping, ScreenSchemeID: scheme.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AssignIssueTypeScreenScheme(ctx, ws, f.ownerID, project.ID, types.ID); err != nil {
		t.Fatal(err)
	}
	var configID string
	if err := f.store.Pool.QueryRow(ctx, `SELECT mapping.configuration_id::text FROM project_field_configuration_schemes assigned JOIN field_configuration_scheme_items mapping ON mapping.scheme_id=assigned.scheme_id WHERE assigned.project_id=$1 AND mapping.issue_type_id=$2`, project.ID, store.DefaultIssueTypeMapping).Scan(&configID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetFieldConfigurationItems(ctx, ws, f.ownerID, configID, []models.FieldConfigurationItem{{FieldID: "priority", IsHidden: true}, {FieldID: "assignee", IsHidden: true}}); err != nil {
		t.Fatal(err)
	}
	user, err := f.store.MemberByID(ctx, ws, f.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method string, values url.Values) *http.Request {
		r := httptest.NewRequest(method, "/browse/"+issue.Key, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
		return r
	}
	r := request(http.MethodGet, nil)
	view, err := f.handler.buildIssueView(r, user, ws, issue.Key)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"priority", "assignee", "reporter", "duedate", "resolution", "security", "timetracking"} {
		if view.FieldOnView(field) {
			t.Fatalf("unexpected visible field %s", field)
		}
	}
	if !view.FieldOnView("description") || !view.FieldOnView("labels") || view.FieldOnEdit("description") || view.FieldOnEdit("labels") || !view.FieldOnEdit("duedate") {
		t.Fatalf("view/edit screen rules: %+v %+v", view.ViewFields, view.EditFields)
	}
	w := httptest.NewRecorder()
	f.handler.BrowseIssue(w, r, issue.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("view %d", w.Code)
	}
	html := w.Body.String()
	if !strings.Contains(html, "Keep rich description") || !strings.Contains(html, "view-only-label") {
		t.Fatal("view-only values missing")
	}
	for _, id := range []string{"field-priority", "field-assignee", "field-reporter", "field-labels", "field-duedate", "field-resolution", "field-security"} {
		if strings.Contains(html, `id="`+id+`"`) {
			t.Fatalf("inappropriate inline control %s", id)
		}
	}
	edit, err := f.handler.buildEditDialogView(ctx, ws, f.ownerID, issue, map[string]bool{"ASSIGN_ISSUES": true, "MODIFY_REPORTER": true})
	if err != nil {
		t.Fatal(err)
	}
	if edit.FieldOnEdit("description") || edit.FieldOnEdit("assignee") || edit.FieldOnEdit("security") {
		t.Fatal("editor ignored screen or hidden rules")
	}
	for _, in := range []commands.UpdateIssueInput{{Description: json.RawMessage(`{"type":"doc","version":1,"content":[]}`)}, {Labels: &[]string{"forged"}}, {PriorityID: builtInScreenString("pr_high")}, {AssigneeID: builtInScreenString(f.ownerID)}} {
		in.ActorID = f.ownerID
		in.WorkspaceID = ws
		in.IssueIDOrKey = issue.Key
		if _, _, err := f.handler.Commands.UpdateIssue(ctx, in); err == nil {
			t.Fatalf("forged write accepted: %+v", in)
		}
	}
	// Omitting a field from an editor must preserve its existing rich value.
	w = httptest.NewRecorder()
	f.handler.EditIssue(w, request(http.MethodPost, url.Values{"summary": {"Changed summary"}}), issue.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("summary edit %d: %s", w.Code, w.Body.String())
	}
	updated, err := f.store.IssueByIDOrKey(ctx, ws, issue.Key)
	var originalDoc, savedDoc any
	if err := json.Unmarshal(description, &originalDoc); err != nil {
		t.Fatal(err)
	}
	if err == nil {
		if err := json.Unmarshal(updated.Description, &savedDoc); err != nil {
			t.Fatal(err)
		}
	}
	if err != nil || updated.Summary != "Changed summary" || !reflect.DeepEqual(originalDoc, savedDoc) || strings.Join(updated.Labels, ",") != "view-only-label" {
		t.Fatalf("unrelated edit changed omitted fields: %+v %v", updated, err)
	}
}

func builtInScreenString(value string) *string { return &value }
