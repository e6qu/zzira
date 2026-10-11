package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

func TestCustomFieldViewsRespectScreensHiddenRulesAndWorkTypeContexts(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "CFS", Name: "Field screens", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`, `DELETE FROM workflow_schemes WHERE workspace_id=$1`, `DELETE FROM workflows WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, q, ws); err != nil {
				t.Error(err)
			}
		}
	})
	fields := map[string]string{}
	values := map[string]json.RawMessage{}
	for _, name := range []string{"Shared", "View only", "Edit only", "Hidden", "Story only"} {
		number, err := f.store.NextCustomFieldNumber(ctx)
		if err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("customfield_%d", number)
		if _, err := f.store.CreateWorkspaceCustomFieldOfKind(ctx, ws, id, name, models.CustomFieldText, models.CustomFieldTypeKeys[models.CustomFieldText], ""); err != nil {
			t.Fatal(err)
		}
		fields[name] = id
		values[id] = json.RawMessage(fmt.Sprintf("%q", name+" answer"))
	}
	// A type-scoped context must not appear for a Task, even if named on its screen.
	var contextID string
	if err := f.store.Pool.QueryRow(ctx, `SELECT id::text FROM custom_field_contexts WHERE field_id=$1`, fields["Story only"]).Scan(&contextID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(ctx, `UPDATE custom_field_contexts SET all_issue_types=false WHERE id=$1::bigint`, contextID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(ctx, `INSERT INTO custom_field_context_issue_types(context_id,issue_type_id) VALUES($1::bigint,'it_story')`, contextID); err != nil {
		t.Fatal(err)
	}
	issue, _, err := f.store.CreateIssue(ctx, f.ownerID, project.ID, "Field screen journey", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, values, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var configID string
	if err := f.store.Pool.QueryRow(ctx, `SELECT mapping.configuration_id::text FROM project_field_configuration_schemes assigned JOIN field_configuration_scheme_items mapping ON mapping.scheme_id=assigned.scheme_id WHERE assigned.project_id=$1 AND mapping.issue_type_id=$2`, project.ID, store.DefaultIssueTypeMapping).Scan(&configID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetFieldConfigurationItems(ctx, ws, f.ownerID, configID, []models.FieldConfigurationItem{{FieldID: fields["Hidden"], IsHidden: true}}); err != nil {
		t.Fatal(err)
	}
	screen := func(name string, names ...string) string {
		t.Helper()
		s, err := f.store.CreateScreen(ctx, ws, f.ownerID, name, "")
		if err != nil {
			t.Fatal(err)
		}
		tabs, err := f.store.ScreenTabs(ctx, ws, s.ID)
		if err != nil || len(tabs) != 1 {
			t.Fatalf("tabs: %v %v", tabs, err)
		}
		for _, id := range append([]string{"summary"}, names...) {
			if _, err := f.store.AddScreenTabField(ctx, ws, f.ownerID, s.ID, tabs[0].ID, id); err != nil {
				t.Fatal(err)
			}
		}
		return s.ID
	}
	editScreen := screen("Editing", fields["Shared"], fields["Edit only"], fields["Hidden"], fields["Story only"])
	viewScreen := screen("Reading", fields["Shared"], fields["View only"], fields["Hidden"], fields["Story only"])
	scheme, err := f.store.CreateScreenScheme(ctx, ws, f.ownerID, "Separate screens", "", map[string]string{"default": editScreen, "edit": editScreen, "view": viewScreen})
	if err != nil {
		t.Fatal(err)
	}
	types, err := f.store.CreateIssueTypeScreenScheme(ctx, ws, f.ownerID, "Field type screens", "", []models.IssueTypeScreenSchemeItem{{IssueTypeID: store.DefaultIssueTypeMapping, ScreenSchemeID: scheme.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AssignIssueTypeScreenScheme(ctx, ws, f.ownerID, project.ID, types.ID); err != nil {
		t.Fatal(err)
	}
	wf := workflow.Workflow{ID: store.NewID("wf"), Name: "Field transition", Transitions: []workflow.Transition{{ID: "1", Name: "Create", Type: workflow.TransitionInitial, To: "st_todo"}, {ID: "21", Name: "Start with view field", From: []string{"st_todo"}, To: "st_inprogress", Screen: &workflow.Rule{ID: "custom-screen", RuleKey: workflow.RuleTransitionScreen, Parameters: map[string]string{"fields": fields["View only"] + "," + fields["Hidden"] + "," + fields["Story only"]}}}}}
	if err := f.store.CreateWorkflow(ctx, ws, wf); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AssignWorkflowToProject(ctx, ws, project.ID, wf.ID); err != nil {
		t.Fatal(err)
	}
	user, err := f.store.MemberByID(ctx, ws, f.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "/browse/"+issue.Key, nil)
	r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
	view, err := f.handler.buildIssueView(r, user, ws, issue.Key)
	if err != nil {
		t.Fatal(err)
	}
	names := func(fields []models.CustomFieldView) string {
		out := []string{}
		for _, field := range fields {
			if _, owned := values[field.ID]; owned {
				out = append(out, field.Name)
			}
		}
		return strings.Join(out, ",")
	}
	if got := names(view.CustomFields); got != "Shared,View only" {
		t.Fatalf("view fields=%s", got)
	}
	if !view.CustomFields[1].ReadOnly || view.CustomFields[0].ReadOnly {
		t.Fatalf("incorrect view editing affordances: %+v", view.CustomFields)
	}
	if len(view.Transitions) != 1 || names(view.Transitions[0].ScreenCustomFields) != "View only" {
		t.Fatalf("transition screen fields: %+v", view.Transitions)
	}
	edit, err := f.handler.buildEditDialogView(ctx, ws, f.ownerID, issue, map[string]bool{"ASSIGN_ISSUES": true, "MODIFY_REPORTER": true})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(edit.CustomFields); got != "Shared,Edit only" {
		t.Fatalf("edit fields=%s", got)
	}
	if got := names(edit.ConfiguredCustomFields); got != "Shared,View only,Edit only" {
		t.Fatalf("transition catalog=%s", got)
	}
	w := httptest.NewRecorder()
	f.handler.BrowseIssue(w, r, issue.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("view: %d %s", w.Code, w.Body.String())
	}
	html := w.Body.String()
	for _, name := range []string{"Hidden answer", "Story only answer", "Edit only answer"} {
		if strings.Contains(html, name) {
			t.Fatalf("view leaked %s", name)
		}
	}
	if !strings.Contains(html, "View only answer") || strings.Contains(html, `name="`+fields["View only"]+`"`) {
		t.Fatal("view-only field missing or editable")
	}
	for _, name := range []string{"View only", "Hidden", "Story only"} {
		request := httptest.NewRequest(http.MethodPost, "/browse/"+issue.Key+"/field", strings.NewReader("field=custom:"+fields[name]+"&value=changed"))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
		response := httptest.NewRecorder()
		f.handler.UpdateIssueField(response, request, issue.Key)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("off-screen %s write: %d", name, response.Code)
		}
	}
	stored, err := f.store.IssueByIDOrKey(ctx, ws, issue.Key)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"View only", "Hidden", "Story only"} {
		if string(stored.Fields[fields[name]]) != string(values[fields[name]]) {
			t.Fatalf("refused write changed %s", name)
		}
	}
	// Authorized app overrides may still update a field outside the edit screen.
	overrideContext := commands.WithOverrides(ctx, commands.Overrides{ScreenSecurity: true})
	if _, _, err := f.handler.Commands.UpdateIssue(overrideContext, commands.UpdateIssueInput{ActorID: f.ownerID, WorkspaceID: ws, IssueIDOrKey: issue.Key, Fields: map[string]json.RawMessage{fields["View only"]: json.RawMessage(`"Authorized answer"`)}}); err != nil {
		t.Fatalf("authorized override: %v", err)
	}
	// A transition uses its own screen rather than the ordinary edit screen.
	changed, _, err := f.handler.Commands.TransitionIssueWithUpdate(ctx, f.ownerID, ws, issue.Key, "21", store.IssueUpdate{Fields: map[string]json.RawMessage{fields["View only"]: json.RawMessage(`"Transition answer"`)}})
	if err != nil || changed.Status.ID != "st_inprogress" || string(changed.Fields[fields["View only"]]) != `"Transition answer"` {
		t.Fatalf("transition write: %#v, %v", changed, err)
	}

}
