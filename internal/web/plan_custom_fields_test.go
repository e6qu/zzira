package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestPlanCustomFieldsPersistDisplayFilterAndRespectScope(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "PCF", Name: "Custom field sources", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := strconv.ParseInt(project.ID, 10, 64)
	fields := map[string]string{}
	for _, kind := range []string{models.CustomFieldLabels, models.CustomFieldText, models.CustomFieldNumber} {
		number, err := f.store.NextCustomFieldNumber(ctx)
		if err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("customfield_%d", number)
		if _, err = f.store.CreateWorkspaceCustomFieldOfKind(ctx, ws, id, "Plan "+kind, kind, models.CustomFieldTypeKeys[kind], ""); err != nil {
			t.Fatal(err)
		}
		fields[kind] = id
	}
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM plans WHERE workspace_id=$1`, `DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`, `DELETE FROM custom_fields WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, query, ws); err != nil {
				t.Error(err)
			}
		}
	})
	for _, input := range []struct{ summary, tags string }{{"Blue work", `["blue","green"]`}, {"Red work", `["red"]`}} {
		if _, _, err = f.store.CreateIssue(ctx, f.ownerID, project.ID, input.summary, json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, map[string]json.RawMessage{fields[models.CustomFieldLabels]: json.RawMessage(input.tags), fields[models.CustomFieldText]: json.RawMessage(`"Read me"`), fields[models.CustomFieldNumber]: json.RawMessage(`0`)}, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	planID, err := f.store.CreatePlan(ctx, ws, f.ownerID, store.Plan{Name: "Custom field plan", LeadAccountID: f.ownerID, Scheduling: store.PlanScheduling{Estimation: "StoryPoints"}, IssueSources: []store.PlanIssueSource{{Type: "Project", Value: projectID}}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/plans/" + strconv.FormatInt(planID, 10)
	get := func(suffix string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path+suffix, nil)
		r.SetPathValue("id", strconv.FormatInt(planID, 10))
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
		w := httptest.NewRecorder()
		if strings.HasPrefix(suffix, "/settings") {
			f.handler.PlanSettingsPage(w, r)
		} else {
			f.handler.PlanPage(w, r)
		}
		if w.Code != want {
			t.Fatalf("GET got %d want %d: %s", w.Code, want, w.Body.String())
		}
		return w
	}
	post := func(user string, form url.Values, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, path+"/settings", strings.NewReader(form.Encode()))
		r.SetPathValue("id", strconv.FormatInt(planID, 10))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[user]})
		w := httptest.NewRecorder()
		f.handler.PlanSettingsSave(w, r)
		if w.Code != want {
			t.Fatalf("POST got %d want %d: %s", w.Code, want, w.Body.String())
		}
		return w
	}
	labelID := strings.TrimPrefix(fields[models.CustomFieldLabels], "customfield_")
	textID := strings.TrimPrefix(fields[models.CustomFieldText], "customfield_")
	numberID := strings.TrimPrefix(fields[models.CustomFieldNumber], "customfield_")
	form := url.Values{"action": {"custom-fields"}, "customField": {labelID, textID, numberID}, "customFieldFilter": {labelID}}
	post(f.otherID, form, 404)
	post(f.ownerID, form, 303)
	page := get("", 200).Body.String()
	for _, wanted := range []string{"Plan labels", "Plan text", "Plan number", "blue, green", "Read me", ">0</td>"} {
		if !strings.Contains(page, wanted) {
			t.Fatalf("plan omitted %q", wanted)
		}
	}
	blue := get("?"+url.Values{"cf_" + labelID: {`"blue"`}}.Encode(), 200).Body.String()
	if !strings.Contains(blue, "Blue work") || strings.Contains(blue, "Red work") {
		t.Fatal("label filter did not select matching work")
	}
	viewForm := url.Values{"name": {"   "}, "cf_" + labelID: {`"blue"`}}
	request := httptest.NewRequest(http.MethodPost, path+"/views", strings.NewReader(viewForm.Encode()))
	request.SetPathValue("id", strconv.FormatInt(planID, 10))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
	refusedView := httptest.NewRecorder()
	f.handler.PlanViewSave(refusedView, request)
	if refusedView.Code != 400 || !strings.Contains(refusedView.Body.String(), `name="name" value="   "`) || !strings.Contains(refusedView.Body.String(), "Blue work") || strings.Contains(refusedView.Body.String(), "Red work") {
		t.Fatal("refused view lost its name or selected filter")
	}
	savedView, err := f.store.SavePlanView(ctx, ws, f.ownerID, planID, store.PlanView{Name: "Blue view", CustomFilters: map[string]string{labelID: `"blue"`}, Columns: []string{"customfield_" + labelID}, Zoom: "detailed"})
	if err != nil {
		t.Fatal(err)
	}
	viewPath := "?view=" + strconv.FormatInt(savedView.ID, 10)
	viewPage := get(viewPath, 200).Body.String()
	if strings.Contains(viewPage, `<th scope="col">Start</th>`) || !strings.Contains(viewPage, `<th scope="col">Plan labels</th>`) || !strings.Contains(viewPage, `value="detailed" selected`) {
		t.Fatal("saved view lost column selection or zoom")
	}
	emptyColumns, err := f.store.SavePlanView(ctx, ws, f.ownerID, planID, store.PlanView{Name: "Work only", Columns: []string{}, Zoom: "compact"})
	if err != nil {
		t.Fatal(err)
	}
	emptyLayout := get("?view="+strconv.FormatInt(emptyColumns.ID, 10), 200).Body.String()
	if strings.Contains(emptyLayout, `<th scope="col">Start</th>`) || strings.Contains(emptyLayout, `<th scope="col">Plan labels</th>`) || !strings.Contains(emptyLayout, "Blue work") {
		t.Fatal("explicitly empty columns became defaults")
	}
	get("?zoom=unknown", 400)
	get("?columnsProvided=1&column=unknown", 400)
	if !strings.Contains(viewPage, "Blue work") || strings.Contains(viewPage, "Red work") {
		t.Fatal("saved view did not retain custom filter")
	}
	if _, err = f.store.SavePlanView(ctx, ws, f.otherID, planID, store.PlanView{Name: "Refused editor"}); !errors.Is(err, store.ErrProjectPermission) {
		t.Fatalf("unprivileged view write: %v", err)
	}
	get("?"+url.Values{"cf_" + labelID: {`"forged"`}}.Encode(), 400)
	if !strings.Contains(get("/settings", 200).Body.String(), `name="customFieldFilter" value="`+labelID+`" checked`) {
		t.Fatal("enabled filter lost on reload")
	}
	form.Set("customFieldFilter", textID)
	refused := post(f.ownerID, form, 400).Body.String()
	if !strings.Contains(refused, `name="customField" value="`+textID+`" checked`) {
		t.Fatal("refused save lost selections")
	}
	saved, err := f.store.Plan(ctx, ws, planID)
	if err != nil || len(saved.CustomFields) != 3 || !saved.CustomFields[0].Filter {
		t.Fatalf("refused save changed stored fields: %+v %v", saved.CustomFields, err)
	}
	// Hide the label field in the project's effective configuration. Neither
	// its cell nor its filter options may expose the stored labels afterwards.
	var configurationID int64
	if err = f.store.Pool.QueryRow(ctx, `SELECT i.configuration_id FROM project_field_configuration_schemes a JOIN field_configuration_scheme_items i ON i.scheme_id=a.scheme_id WHERE a.project_id=$1 ORDER BY (i.issue_type_id='it_task') DESC LIMIT 1`, project.ID).Scan(&configurationID); err != nil {
		t.Fatal(err)
	}
	if err = f.store.SetFieldConfigurationItems(ctx, ws, f.ownerID, strconv.FormatInt(configurationID, 10), []models.FieldConfigurationItem{{FieldID: fields[models.CustomFieldLabels], IsHidden: true}}); err != nil {
		t.Fatal(err)
	}
	hidden := get("", 200).Body.String()
	_, filterBody, _ := strings.Cut(hidden, `<select id="plan-custom-filter-`+labelID+`"`)
	filterBody, _, _ = strings.Cut(filterBody, `</select>`)
	if strings.Contains(filterBody, "blue") || strings.Contains(filterBody, "green") || strings.Contains(filterBody, ">red<") || !strings.Contains(hidden, "Not available") {
		t.Fatal("hidden custom field leaked into the plan")
	}
	if err = f.store.SetFieldConfigurationItems(ctx, ws, f.ownerID, strconv.FormatInt(configurationID, 10), []models.FieldConfigurationItem{{FieldID: fields[models.CustomFieldLabels]}}); err != nil {
		t.Fatal(err)
	}
	contexts, err := f.store.CustomFieldContexts(ctx, ws, fields[models.CustomFieldLabels], nil)
	if err != nil || len(contexts) != 1 {
		t.Fatalf("contexts: %+v %v", contexts, err)
	}
	if err = f.store.ChangeCustomFieldContextScope(ctx, ws, f.ownerID, fields[models.CustomFieldLabels], contexts[0].ID, nil, []string{"it_story"}, false); err != nil {
		t.Fatal(err)
	}
	outOfScope := get("", 200).Body.String()
	_, filterBody, _ = strings.Cut(outOfScope, `<select id="plan-custom-filter-`+labelID+`"`)
	filterBody, _, _ = strings.Cut(filterBody, `</select>`)
	if strings.Contains(filterBody, "blue") || !strings.Contains(outOfScope, "Not available") {
		t.Fatal("unassociated values leaked into plan filters")
	}
	privateView := get(viewPath, 200).Body.String()
	if strings.Contains(privateView, "Blue work") || strings.Contains(privateView, "Red work") || strings.Contains(privateView, `value="&#34;blue&#34;"`) || !strings.Contains(privateView, "A saved filter is no longer available") {
		t.Fatal("unavailable saved filter exposed its value or widened the view")
	}
	// Removing the configured fields removes their columns and filter controls.
	post(f.ownerID, url.Values{"action": {"custom-fields"}}, 303)
	cleared := get("", 200).Body.String()
	if strings.Contains(cleared, "Plan labels") || strings.Contains(cleared, "Plan text") {
		t.Fatal("removed plan fields remain visible")
	}
	var count int
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_id=$2 AND entity_type='jira_configuration'`, ws, "plan:"+strconv.FormatInt(planID, 10)).Scan(&count); err != nil || count != 2 {
		t.Fatalf("configuration audit got %d want 2: %v", count, err)
	}
	// A revoked editor cannot reuse an earlier plan snapshot to reinstate the
	// grant or overwrite its settings; authorization uses the stored row.
	current, err := f.store.Plan(ctx, ws, planID)
	if err != nil {
		t.Fatal(err)
	}
	current.Permissions = []store.PlanPermission{{Type: "Edit", HolderType: "AccountId", Holder: f.otherID}}
	if err = f.store.UpdatePlan(ctx, ws, f.ownerID, current); err != nil {
		t.Fatal(err)
	}
	stale := current
	current.Permissions = []store.PlanPermission{}
	if err = f.store.UpdatePlan(ctx, ws, f.ownerID, current); err != nil {
		t.Fatal(err)
	}
	stale.Name = "Refused stale plan"
	if err = f.store.UpdatePlan(ctx, ws, f.otherID, stale); !errors.Is(err, store.ErrProjectPermission) {
		t.Fatalf("revoked editor updated plan: %v", err)
	}
	current, err = f.store.Plan(ctx, ws, planID)
	if err != nil || current.Name != "Custom field plan" || len(current.Permissions) != 0 {
		t.Fatalf("refused save changed plan: %+v %v", current, err)
	}

}

func TestPlanCustomFieldOptionsUseContextNames(t *testing.T) {
	field := planCustomFieldChoice{Kind: models.CustomFieldMultiSelect}
	got := planCustomFieldCellFor(field, json.RawMessage(`["101","102"]`), []models.CreateFieldOption{{ID: "101", Name: "First"}, {ID: "102", Name: "Second"}})
	if got.Display != "First, Second" || len(got.Tokens) != 2 {
		t.Fatalf("select names: %+v", got)
	}
	unavailable := planCustomFieldCellFor(field, json.RawMessage(`["999"]`), []models.CreateFieldOption{{ID: "101", Name: "First"}})
	if unavailable.Display != "Unavailable option" || len(unavailable.Tokens) != 0 {
		t.Fatalf("option outside governing context exposed: %+v", unavailable)
	}
}
