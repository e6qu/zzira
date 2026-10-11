package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

func TestTransitionFormKeepsTypedValuesRichTextAndExplicitClears(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "TVL", Name: "Transition values", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
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
	for _, kind := range []string{models.CustomFieldNumber, models.CustomFieldMultiSelect} {
		number, err := f.store.NextCustomFieldNumber(ctx)
		if err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("customfield_%d", number)
		if _, err := f.store.CreateWorkspaceCustomFieldOfKind(ctx, ws, id, kind, kind, models.CustomFieldTypeKeys[kind], ""); err != nil {
			t.Fatal(err)
		}
		fields[kind] = id
	}
	multi, number := fields[models.CustomFieldMultiSelect], fields[models.CustomFieldNumber]
	contexts, err := f.store.CustomFieldContexts(ctx, ws, multi, nil)
	if err != nil || len(contexts) != 1 {
		t.Fatalf("contexts: %v %v", contexts, err)
	}
	options, err := f.store.CreateCustomFieldOptions(ctx, ws, f.ownerID, multi, contexts[0].ID, []string{"One", "Two"})
	if err != nil {
		t.Fatal(err)
	}
	selected := []string{options[0].ID, options[1].ID}
	selectedJSON, _ := json.Marshal(selected)
	rich := json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Retained rich text","marks":[{"type":"strong"}]}]},{"type":"paragraph","content":[{"type":"text","text":"Second paragraph","marks":[{"type":"em"}]}]}]}`)
	issue, _, err := f.store.CreateIssue(ctx, f.ownerID, project.ID, "Original transition", rich, "st_todo", "it_task", "pr_medium", "", nil, map[string]json.RawMessage{multi: selectedJSON, number: json.RawMessage("2"), "environment": rich}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	wf := workflow.Workflow{ID: store.NewID("wf"), Name: "Typed transitions", Transitions: []workflow.Transition{
		{ID: "1", Name: "Create", Type: workflow.TransitionInitial, To: "st_todo"},
		{ID: "21", Name: "Collect", From: []string{"st_todo"}, To: "st_inprogress", Screen: &workflow.Rule{ID: "screen", RuleKey: workflow.RuleTransitionScreen, Parameters: map[string]string{"fields": "summary,description,environment," + number + "," + multi}}},
		{ID: "11", Name: "Back", From: []string{"st_inprogress"}, To: "st_todo"},
	}}
	if err := f.store.CreateWorkflow(ctx, ws, wf); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AssignWorkflowToProject(ctx, ws, project.ID, wf.ID); err != nil {
		t.Fatal(err)
	}
	post := func(form url.Values, want int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/issues/"+issue.Key+"/transition", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
		w := httptest.NewRecorder()
		f.handler.TransitionIssue(w, r, issue.Key)
		if w.Code != want {
			t.Fatalf("transition: %d want %d: %s", w.Code, want, w.Body.String())
		}
	}
	read := func() *models.Issue {
		t.Helper()
		saved, err := f.store.IssueByIDOrKey(ctx, ws, issue.Key)
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	back := func() {
		t.Helper()
		if _, _, err := f.handler.Commands.TransitionIssue(ctx, f.ownerID, ws, issue.Key, "11"); err != nil {
			t.Fatal(err)
		}
	}
	plain := strings.ReplaceAll(adf.PlainText(rich), "\n", "\r\n")
	form := url.Values{"transition": {"21"}, "field_summary": {"Saved transition"}, "field_description": {plain}, "field_environment": {plain}, "field_" + number: {"invalid"}, "field_" + multi: {"", selected[0], selected[1]}}
	before := read()
	post(form, 400)
	if saved := read(); saved.Summary != before.Summary || saved.Status.ID != before.Status.ID || saved.UpdatedSeq != before.UpdatedSeq || string(saved.Fields[number]) != "2" {
		t.Fatal("invalid typed value partially changed the work item")
	}
	form.Set("field_"+number, "12.5")
	post(form, 200)
	saved := read()
	var got []string
	if err := json.Unmarshal(saved.Fields[multi], &got); err != nil || !slices.Equal(got, selected) {
		t.Fatalf("multi-select values lost: %s %v", saved.Fields[multi], err)
	}
	if string(saved.Fields[number]) != "12.5" || !adf.Equal(saved.Description, rich) || !adf.Equal(saved.Fields["environment"], rich) {
		t.Fatal("transition changed a typed number or unchanged rich document")
	}
	back()
	post(url.Values{"transition": {"21"}, "field_summary": {"Only summary changed"}}, 200)
	if saved := read(); string(saved.Fields[number]) != "12.5" || (json.Unmarshal(saved.Fields[multi], &got) != nil || !slices.Equal(got, selected)) || !adf.Equal(saved.Description, rich) || !adf.Equal(saved.Fields["environment"], rich) {
		t.Fatal("omitted transition fields were changed")
	}
	back()
	post(url.Values{"transition": {"21"}, "field_description": {""}, "field_environment": {""}, "field_" + number: {""}, "field_" + multi: {""}}, 200)
	saved = read()
	if adf.PlainText(saved.Description) != "" {
		t.Fatal("description did not clear")
	}
	for _, id := range []string{"environment", number, multi} {
		if raw := saved.Fields[id]; len(raw) > 0 && string(raw) != "null" {
			t.Fatalf("field %s did not clear: %s", id, raw)
		}
	}
}
