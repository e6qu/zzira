package web

import (
	"context"
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

func TestPlanSchedulingOffersCustomDatesAndPreservesRefusedChoices(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	fields := map[string]string{}
	for _, kind := range []string{models.CustomFieldDate, models.CustomFieldDatetime, models.CustomFieldText} {
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
		if _, err := f.store.Pool.Exec(ctx, `DELETE FROM custom_fields WHERE workspace_id=$1`, ws); err != nil {
			t.Error(err)
		}
	})
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "PDT" + strings.TrimPrefix(fields[models.CustomFieldDate], "customfield_"), Name: "Plan date project", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := strconv.ParseInt(project.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM plans WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, query, ws); err != nil {
				t.Error(err)
			}
		}
	})
	planID, err := f.store.CreatePlan(ctx, ws, f.ownerID, store.Plan{Name: "Date sources", IssueSources: []store.PlanIssueSource{{Type: "Project", Value: projectID}}, Scheduling: store.PlanScheduling{Estimation: "StoryPoints"}, LeadAccountID: f.ownerID, Permissions: []store.PlanPermission{{Type: "View", HolderType: "AccountId", Holder: f.otherID}}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/plans/" + strconv.FormatInt(planID, 10) + "/settings"
	call := func(user, method string, form url.Values, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.SetPathValue("id", strconv.FormatInt(planID, 10))
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[user]})
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		if method == http.MethodGet {
			f.handler.PlanSettingsPage(w, r)
		} else {
			f.handler.PlanSettingsSave(w, r)
		}
		if w.Code != want {
			t.Fatalf("plan settings got %d want %d: %s", w.Code, want, w.Body.String())
		}
		return w
	}
	value := func(kind string) string { return "DateCustomField:" + strings.TrimPrefix(fields[kind], "customfield_") }
	page := call(f.ownerID, http.MethodGet, nil, http.StatusOK)
	for _, kind := range []string{models.CustomFieldDate, models.CustomFieldDatetime} {
		if !strings.Contains(page.Body.String(), `value="`+value(kind)+`"`) {
			t.Fatalf("missing %s date choice", kind)
		}
	}
	if strings.Contains(page.Body.String(), `value="`+value(models.CustomFieldText)+`"`) {
		t.Fatal("a text field was offered as a date source")
	}
	form := url.Values{"action": {"scheduling"}, "estimation": {"Hours"}, "startField": {value(models.CustomFieldDate)}, "endField": {value(models.CustomFieldDatetime)}}
	call(f.otherID, http.MethodPost, form, http.StatusForbidden)
	call(f.ownerID, http.MethodPost, form, http.StatusSeeOther)
	saved, err := f.store.Plan(ctx, ws, planID)
	if err != nil || planDateFieldValue(saved.Scheduling.StartDate) != value(models.CustomFieldDate) || planDateFieldValue(saved.Scheduling.EndDate) != value(models.CustomFieldDatetime) {
		t.Fatalf("saved dates: %+v err=%v", saved.Scheduling, err)
	}
	page = call(f.ownerID, http.MethodGet, nil, http.StatusOK)
	for _, kind := range []string{models.CustomFieldDate, models.CustomFieldDatetime} {
		if !strings.Contains(page.Body.String(), `value="`+value(kind)+`" selected`) {
			t.Fatalf("%s selection did not survive reload", kind)
		}
	}
	form.Set("startField", value(models.CustomFieldText))
	refused := call(f.ownerID, http.MethodPost, form, http.StatusBadRequest)
	if !strings.Contains(refused.Body.String(), `value="`+value(models.CustomFieldDatetime)+`" selected`) || !strings.Contains(refused.Body.String(), "not a date field") {
		t.Fatal("refused scheduling lost the other date choice or its explanation")
	}
	unchanged, err := f.store.Plan(ctx, ws, planID)
	if err != nil || planDateFieldValue(unchanged.Scheduling.StartDate) != value(models.CustomFieldDate) {
		t.Fatalf("refused scheduling changed persisted dates: %+v err=%v", unchanged.Scheduling, err)
	}
	if _, err = f.store.Pool.Exec(ctx, `UPDATE custom_fields SET trashed_at=now() WHERE id=$1`, fields[models.CustomFieldDate]); err != nil {
		t.Fatal(err)
	}
	page = call(f.ownerID, http.MethodGet, nil, http.StatusOK)
	if !strings.Contains(page.Body.String(), `value="`+value(models.CustomFieldDate)+`" selected`) || !strings.Contains(page.Body.String(), "Unavailable date custom field") {
		t.Fatal("removing a configured field silently changed the displayed date source")
	}
}
