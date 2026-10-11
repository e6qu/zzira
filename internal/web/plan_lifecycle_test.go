package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestPlanLifecyclePreservesScenariosAndChecksCurrentState(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "PLC", Name: "Lifecycle sources", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := strconv.ParseInt(project.ID, 10, 64)
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM plans WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, query, ws); err != nil {
				t.Error(err)
			}
		}
	})
	planID, err := f.store.CreatePlan(ctx, ws, f.ownerID, store.Plan{Name: "Lifecycle plan", LeadAccountID: f.ownerID, Scheduling: store.PlanScheduling{Estimation: "StoryPoints"}, IssueSources: []store.PlanIssueSource{{Type: "Project", Value: projectID}}})
	if err != nil {
		t.Fatal(err)
	}
	scenarioID, err := f.store.CreatePlanScenario(ctx, ws, f.ownerID, planID, "Retained scenario", "purple", 0)
	if err != nil {
		t.Fatal(err)
	}
	teamID, err := f.store.SavePlanTeam(ctx, ws, planID, store.PlanTeam{Name: "Retained team", PlanningStyle: "Kanban"}, true)
	if err != nil {
		t.Fatal(err)
	}
	capacity := 17.5
	if err = f.store.SetPlanIterationCapacity(ctx, ws, f.ownerID, planID, scenarioID, teamID, "2026-11-02", &capacity); err != nil {
		t.Fatal(err)
	}
	path := "/plans/" + strconv.FormatInt(planID, 10)
	get := func(user, path string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[user]})
		w := httptest.NewRecorder()
		f.handler.PlansPage(w, r)
		if w.Code != want {
			t.Fatalf("list got %d want %d: %s", w.Code, want, w.Body.String())
		}
		return w
	}
	post := func(user, action, status string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, path+"/lifecycle", strings.NewReader(url.Values{"action": {action}, "status": {status}}.Encode()))
		r.SetPathValue("id", strconv.FormatInt(planID, 10))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[user]})
		w := httptest.NewRecorder()
		f.handler.PlanLifecycle(w, r)
		if w.Code != want {
			t.Fatalf("%s got %d want %d: %s", action, w.Code, want, w.Body.String())
		}
		return w
	}
	get(f.otherID, "/plans?status=Archived", 403)
	get(f.ownerID, "/plans?status=Unknown", 400)
	get(f.ownerID, "/plans?after=-1", 400)
	post(f.otherID, "archive", "", 403)
	if err = f.store.ChangePlanStatus(ctx, ws, f.otherID, planID, "Active", "Archived"); !errors.Is(err, store.ErrProjectPermission) {
		t.Fatalf("store allowed nonadmin: %v", err)
	}
	post(f.ownerID, "restore", "Active", 400)
	post(f.ownerID, "archive", "", 303)
	archived := get(f.ownerID, "/plans?status=Archived", 200)
	if !strings.Contains(archived.Body.String(), "Restore") || strings.Contains(archived.Body.String(), `href="`+path+`"`) {
		t.Fatal("archived plan offered editable link or no restore")
	}
	if strings.Contains(get(f.ownerID, "/plans", 200).Body.String(), "Lifecycle plan") {
		t.Fatal("archived plan still active")
	}
	// Inactive plan pages and their mutations remain unavailable.
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.SetPathValue("id", strconv.FormatInt(planID, 10))
	r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
	w := httptest.NewRecorder()
	f.handler.PlanPage(w, r)
	if w.Code != 404 {
		t.Fatalf("archived plan opened: %d", w.Code)
	}
	post(f.ownerID, "archive", "", 409)
	post(f.ownerID, "restore", "Trashed", 409)
	post(f.ownerID, "restore", "Archived", 303)
	post(f.ownerID, "restore", "Archived", 409)
	post(f.ownerID, "trash", "", 303)
	if !strings.Contains(get(f.ownerID, "/plans?status=Trashed", 200).Body.String(), "Lifecycle plan") {
		t.Fatal("trashed plan missing")
	}
	post(f.ownerID, "restore", "Trashed", 303)
	scenarios, err := f.store.PlanScenarios(ctx, ws, planID)
	if err != nil || len(scenarios) != 2 {
		t.Fatalf("restore lost scenarios: %+v %v", scenarios, err)
	}
	capacities, err := f.store.PlanIterationCapacities(ctx, scenarioID)
	if err != nil || len(capacities) != 1 || capacities[0].Capacity != capacity || capacities[0].TeamID != teamID {
		t.Fatalf("restore lost capacity: %+v %v", capacities, err)
	}
	var actions int
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type='jira_configuration' AND entity_id=$2 AND payload->>'name'='Lifecycle plan'`, ws, "plan:"+strconv.FormatInt(planID, 10)).Scan(&actions); err != nil || actions != 4 {
		t.Fatalf("lifecycle audit got %d want 4: %v", actions, err)
	}
}

func TestPlanLifecycleListsBeyondFirstHundred(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	t.Cleanup(func() {
		if _, err := f.store.Pool.Exec(ctx, `DELETE FROM plans WHERE workspace_id=$1`, ws); err != nil {
			t.Error(err)
		}
	})
	if _, err := f.store.Pool.Exec(ctx, `INSERT INTO plans(workspace_id,name,status,created_by,scheduling,exclusion_rules) SELECT $1,'Archived page '||n,'Archived',$2,'{"estimation":"StoryPoints"}'::jsonb,'{}'::jsonb FROM generate_series(1,101) n`, ws, f.ownerID); err != nil {
		t.Fatal(err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
		w := httptest.NewRecorder()
		f.handler.PlansPage(w, r)
		if w.Code != 200 {
			t.Fatalf("page got %d: %s", w.Code, w.Body.String())
		}
		return w
	}
	first := get("/plans?status=Archived")
	_, rest, found := strings.Cut(first.Body.String(), `href="/plans?after=`)
	if !found {
		t.Fatal("pagination missing")
	}
	query, _, _ := strings.Cut(rest, `"`)
	next := get("/plans?after=" + strings.ReplaceAll(query, "&amp;", "&"))
	if !strings.Contains(next.Body.String(), "Archived page 101") || strings.Contains(next.Body.String(), ">Next plans<") {
		t.Fatal("last archived plan lost or extra next page")
	}
}
