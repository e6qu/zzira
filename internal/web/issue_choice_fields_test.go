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

func TestIssueComponentAndVersionControlsPersistClearAndRespectScreens(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "CHO", Name: "Issue choices", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
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
	component, err := f.store.CreateComponent(ctx, ws, f.ownerID, store.ComponentInput{ProjectIDOrKey: project.ID, Name: "Payments", AssigneeType: "PROJECT_DEFAULT"})
	if err != nil {
		t.Fatal(err)
	}
	versionName := "October release"
	version, err := f.store.SaveVersion(ctx, ws, f.ownerID, project.ID, "", store.VersionUpdate{Name: &versionName})
	if err != nil {
		t.Fatal(err)
	}
	issue, _, err := f.store.CreateIssue(ctx, f.ownerID, project.ID, "Choice journey", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	request := func(method string, values url.Values) *http.Request {
		r := httptest.NewRequest(method, "/issues/"+issue.Key+"/edit", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
		return r
	}
	values := url.Values{"summary": {issue.Summary}, "components": {"", component.ID}, "fixVersions": {"", version.ID}, "versions": {"", version.ID}}
	w := httptest.NewRecorder()
	f.handler.EditIssue(w, request(http.MethodPost, values), issue.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("save choices %d: %s", w.Code, w.Body.String())
	}
	current, err := f.store.IssueByIDOrKey(ctx, ws, issue.Key)
	if err != nil || len(current.VersionRefs("fixVersions")) != 1 || len(current.VersionRefs("versions")) != 1 || !strings.Contains(string(current.Fields["components"]), component.ID) {
		t.Fatalf("choices not persisted: %+v %v", current, err)
	}
	foreignProject, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "OUT", Name: "Other choices", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
	if err != nil {
		t.Fatal(err)
	}
	foreignComponent, err := f.store.CreateComponent(ctx, ws, f.ownerID, store.ComponentInput{ProjectIDOrKey: foreignProject.ID, Name: "Other payments", AssigneeType: "PROJECT_DEFAULT"})
	if err != nil {
		t.Fatal(err)
	}
	foreignVersion, err := f.store.SaveVersion(ctx, ws, f.ownerID, foreignProject.ID, "", store.VersionUpdate{Name: &versionName})
	if err != nil {
		t.Fatal(err)
	}
	for field, id := range map[string]string{"components": foreignComponent.ID, "fixVersions": foreignVersion.ID, "versions": foreignVersion.ID} {
		if _, _, err := f.handler.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{ActorID: f.ownerID, WorkspaceID: ws, IssueIDOrKey: issue.Key, Fields: map[string]json.RawMessage{field: issueChoiceRefs([]string{id})}}); err == nil {
			t.Fatalf("accepted %s from another project", field)
		}
	}
	unchanged, err := f.store.IssueByIDOrKey(ctx, ws, issue.Key)
	if err != nil || unchanged.UpdatedSeq != current.UpdatedSeq {
		t.Fatalf("refused references changed issue checkpoint: %+v %v", unchanged, err)
	}
	edit, err := f.handler.buildEditDialogView(ctx, ws, f.ownerID, current, map[string]bool{"RESOLVE_ISSUES": true})
	if err != nil || len(edit.ChoiceFields) != 3 {
		t.Fatalf("choice editor: %+v %v", edit, err)
	}
	for _, field := range edit.ChoiceFields {
		if field.Value == "" || len(field.Field.Options) != 1 {
			t.Fatalf("picker omitted its selected value or project options: %+v", field)
		}
	}
	withoutResolve, err := f.handler.buildEditDialogView(ctx, ws, f.ownerID, current, map[string]bool{})
	if err != nil || len(withoutResolve.ChoiceFields) != 2 {
		t.Fatalf("resolve permission did not restrict fix-version editor: %+v %v", withoutResolve, err)
	}
	// Clearing an inline multiple select posts only its explicit empty value.
	w = httptest.NewRecorder()
	f.handler.UpdateIssueField(w, request(http.MethodPost, url.Values{"field": {"components"}, "value": {""}}), issue.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("clear components %d: %s", w.Code, w.Body.String())
	}
	current, err = f.store.IssueByIDOrKey(ctx, ws, issue.Key)
	if err != nil || string(current.Fields["components"]) != "[]" || len(current.VersionRefs("fixVersions")) != 1 || len(current.VersionRefs("versions")) != 1 {
		t.Fatalf("clear changed unrelated choices: %+v %v", current, err)
	}
	// A project-specific pair of screens makes fix versions edit-only and
	// affects versions view-only. Components are hidden by their field rule.
	screen := func(name string, fields ...string) string {
		t.Helper()
		s, err := f.store.CreateScreen(ctx, ws, f.ownerID, name, "")
		if err != nil {
			t.Fatal(err)
		}
		tabs, err := f.store.ScreenTabs(ctx, ws, s.ID)
		if err != nil || len(tabs) != 1 {
			t.Fatalf("screen tabs: %+v %v", tabs, err)
		}
		for _, field := range append([]string{"summary"}, fields...) {
			if _, err := f.store.AddScreenTabField(ctx, ws, f.ownerID, s.ID, tabs[0].ID, field); err != nil {
				t.Fatal(err)
			}
		}
		return s.ID
	}
	editScreen, viewScreen := screen("Choice editing", "components", "fixVersions"), screen("Choice reading", "components", "versions")
	screens, err := f.store.CreateScreenScheme(ctx, ws, f.ownerID, "Choice screens", "", map[string]string{"default": editScreen, "edit": editScreen, "view": viewScreen})
	if err != nil {
		t.Fatal(err)
	}
	types, err := f.store.CreateIssueTypeScreenScheme(ctx, ws, f.ownerID, "Choice types", "", []models.IssueTypeScreenSchemeItem{{IssueTypeID: store.DefaultIssueTypeMapping, ScreenSchemeID: screens.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AssignIssueTypeScreenScheme(ctx, ws, f.ownerID, project.ID, types.ID); err != nil {
		t.Fatal(err)
	}
	var config string
	if err := f.store.Pool.QueryRow(ctx, `SELECT mapping.configuration_id::text FROM project_field_configuration_schemes assigned JOIN field_configuration_scheme_items mapping ON mapping.scheme_id=assigned.scheme_id WHERE assigned.project_id=$1 AND mapping.issue_type_id=$2`, project.ID, store.DefaultIssueTypeMapping).Scan(&config); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SetFieldConfigurationItems(ctx, ws, f.ownerID, config, []models.FieldConfigurationItem{{FieldID: "components", IsHidden: true}}); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	f.handler.BrowseIssue(w, request(http.MethodGet, nil), issue.Key)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "Fix version:") || !strings.Contains(w.Body.String(), "Affects version: October release") || strings.Contains(w.Body.String(), `id="field-components"`) || strings.Contains(w.Body.String(), `id="field-versions"`) {
		t.Fatalf("view ignored screens or hidden rules: %d", w.Code)
	}
	edit, err = f.handler.buildEditDialogView(ctx, ws, f.ownerID, current, map[string]bool{"RESOLVE_ISSUES": true})
	if err != nil || len(edit.ChoiceFields) != 1 || edit.ChoiceFields[0].Field.ID != "fixVersions" {
		t.Fatalf("editor ignored screens or hidden rules: %+v %v", edit, err)
	}
	for _, in := range []commands.UpdateIssueInput{
		{Fields: map[string]json.RawMessage{"versions": issueChoiceRefs([]string{version.ID})}},
		{Fields: map[string]json.RawMessage{"components": issueChoiceRefs([]string{component.ID})}},
		{VersionOperations: map[string][]map[string]json.RawMessage{"versions": {{"set": json.RawMessage("[]")}}}},
	} {
		in.ActorID, in.WorkspaceID, in.IssueIDOrKey = f.ownerID, ws, issue.Key
		if _, _, err := f.handler.Commands.UpdateIssue(ctx, in); err == nil {
			t.Fatal("forged off-screen/hidden choice edit was accepted")
		}
	}
	w = httptest.NewRecorder()
	f.handler.EditIssue(w, request(http.MethodPost, url.Values{"summary": {issue.Summary}, "fixVersions": {""}}), issue.Key)
	if w.Code != http.StatusOK {
		t.Fatalf("clear fix versions %d: %s", w.Code, w.Body.String())
	}
	current, err = f.store.IssueByIDOrKey(ctx, ws, issue.Key)
	if err != nil || len(current.VersionRefs("fixVersions")) != 0 || len(current.VersionRefs("versions")) != 1 {
		t.Fatalf("omitted view-only choice changed: %+v %v", current, err)
	}
}
