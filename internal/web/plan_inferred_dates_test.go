package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestPlanInferredDatesStayProjectedAndFollowScenarioAssignments(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "PID", Name: "Inferred dates", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "scrum")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM plans WHERE workspace_id=$1`, `DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, query, ws); err != nil {
				t.Error(err)
			}
		}
	})
	var startID string
	if err = f.store.Pool.QueryRow(ctx, `SELECT id FROM custom_fields WHERE workspace_id=$1 AND name='Target start'`, ws).Scan(&startID); err != nil {
		t.Fatal(err)
	}
	versionName, start, end := "Assigned release", "2026-11-02", "2026-11-20"
	version, err := f.store.SaveVersion(ctx, ws, f.ownerID, project.ID, "", store.VersionUpdate{Name: &versionName, StartDate: &start, ReleaseDate: &end})
	if err != nil {
		t.Fatal(err)
	}
	versions, _ := json.Marshal([]map[string]string{{"id": version.ID}})
	issue, _, err := f.store.CreateIssue(ctx, f.ownerID, project.ID, "Projected dates", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_story", "pr_medium", "", nil, map[string]json.RawMessage{"fixVersions": versions}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	board, err := f.store.CreateBoard(ctx, f.ownerID, ws, store.BoardCreate{Name: "Inferred sprint", Type: "scrum", ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	sprint, _, err := f.store.CreateSprint(ctx, f.ownerID, ws, board.ID, "Assigned sprint", "")
	if err != nil {
		t.Fatal(err)
	}
	alternate, _, err := f.store.CreateSprint(ctx, f.ownerID, ws, board.ID, "Scenario sprint", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Pool.Exec(ctx, `UPDATE sprints SET start_date='2026-12-01',end_date='2026-12-11' WHERE id=$1`, sprint.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Pool.Exec(ctx, `UPDATE sprints SET start_date='2026-12-14',end_date='2026-12-24' WHERE id=$1`, alternate.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.AddIssueToSprint(ctx, f.ownerID, ws, sprint.ID, issue.ID, "0"); err != nil {
		t.Fatal(err)
	}
	projectNumber, _ := strconv.ParseInt(project.ID, 10, 64)
	planID, err := f.store.CreatePlan(ctx, ws, f.ownerID, store.Plan{Name: "Projected dates plan", LeadAccountID: f.ownerID, IssueSources: []store.PlanIssueSource{{Type: "Project", Value: projectNumber}}, Scheduling: store.PlanScheduling{Estimation: "StoryPoints", StartDate: store.PlanDateField{Type: "TargetStartDate"}, EndDate: store.PlanDateField{Type: "DueDate"}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.store.Plan(ctx, ws, planID)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, suffix string, form url.Values, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/plans/"+strconv.FormatInt(planID, 10)+suffix, strings.NewReader(form.Encode()))
		r.SetPathValue("id", strconv.FormatInt(planID, 10))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
		w := httptest.NewRecorder()
		if method == http.MethodPost && suffix == "/settings" {
			f.handler.PlanSettingsSave(w, r)
		} else if method == http.MethodPost {
			r.SetPathValue("key", issue.Key)
			f.handler.PlanWorkChange(w, r)
		} else {
			f.handler.PlanPage(w, r)
		}
		if w.Code != want {
			t.Fatalf("%s %s got %d want %d: %s", method, suffix, w.Code, want, w.Body.String())
		}
		return w
	}
	read := func(scenario int64, wantStart, wantEnd, source string) models.TimelineItem {
		t.Helper()
		plan, err = f.store.Plan(ctx, ws, planID)
		if err != nil {
			t.Fatal(err)
		}
		planning, err := f.store.PlanPlanning(ctx, ws, f.ownerID, plan, scenario, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		item := planning.Work.Items[0]
		if item.StartDate != wantStart || item.DueDate != wantEnd || item.InferredStart != source {
			t.Fatalf("projection %+v", item)
		}
		return item
	}
	read(plan.ScenarioID, "", "", "")
	call(http.MethodPost, "/settings", url.Values{"action": {"scheduling"}, "inferredDates": {"ReleaseDates"}}, 303)
	read(plan.ScenarioID, start, end, "Release dates")
	page := call(http.MethodGet, "", nil, 200).Body.String()
	if !strings.Contains(page, "Release dates") || !strings.Contains(page, `id="plan-start-`+issue.Key+`" name="startDate" type="date" value=""`) {
		t.Fatal("inferred dates hidden or prefilled into committed date form")
	}
	// Submitting an unrelated summary must not turn projected dates into drafts.
	call(http.MethodPost, "/work/"+issue.Key, url.Values{"issue": {issue.ID}, "summary": {"A planning draft"}, "startDate": {""}, "endDate": {""}}, 303)
	changes, err := f.store.PlanChanges(ctx, ws, planID, plan.ScenarioID)
	if err != nil || len(changes) != 1 || changes[0].Field != "summary" {
		t.Fatalf("projection created extra drafts: %+v %v", changes, err)
	}
	call(http.MethodPost, "/settings", url.Values{"action": {"scheduling"}, "inferredDates": {"SprintDates"}}, 303)
	read(plan.ScenarioID, "2026-12-01", "2026-12-11", "Sprint dates")
	scenario, err := f.store.CreatePlanScenario(ctx, ws, f.ownerID, planID, "Other dates", "blue", 0)
	if err != nil {
		t.Fatal(err)
	}
	sprintValue, _ := json.Marshal(alternate.ID)
	baseSprint, _ := json.Marshal(sprint.ID)
	if err = f.store.SetPlanChange(ctx, ws, f.ownerID, planID, scenario, issue.ID, "sprint", sprintValue, baseSprint); err != nil {
		t.Fatal(err)
	}
	read(scenario, "2026-12-14", "2026-12-24", "Sprint dates")
	if err = f.store.SetPlanChange(ctx, ws, f.ownerID, planID, scenario, issue.ID, "startDate", json.RawMessage(`"2026-12-16"`), json.RawMessage(`null`)); err != nil {
		t.Fatal(err)
	}
	item := read(scenario, "2026-12-16", "2026-12-24", "")
	if item.InferredEnd != "Sprint dates" {
		t.Fatalf("explicit start blocked inferred end: %+v", item)
	}
	if err = f.store.SetPlanChange(ctx, ws, f.ownerID, planID, scenario, issue.ID, "sprint", json.RawMessage(`null`), baseSprint); err != nil {
		t.Fatal(err)
	}
	read(scenario, "2026-12-16", "", "")
	committed, err := f.store.IssueByIDOrKey(ctx, ws, issue.ID)
	if err != nil || committed.Summary != "Projected dates" || committed.DueDate != "" || len(committed.Fields[startID]) > 0 && string(committed.Fields[startID]) != "null" {
		t.Fatalf("inference changed committed work: %+v %v", committed, err)
	}
	// A valid choice survives a refused date-field selection; invalid inference
	// does not alter the persisted configuration.
	refused := call(http.MethodPost, "/settings", url.Values{"action": {"scheduling"}, "inferredDates": {"ReleaseDates"}, "startField": {"Invalid"}}, 400)
	if !strings.Contains(refused.Body.String(), `value="ReleaseDates" selected`) {
		t.Fatal("refused settings lost inferred choice")
	}
	call(http.MethodPost, "/settings", url.Values{"action": {"scheduling"}, "inferredDates": {"Invalid"}}, 400)
	plan, err = f.store.Plan(ctx, ws, planID)
	if err != nil || plan.Scheduling.InferredDates != "SprintDates" {
		t.Fatalf("refusal changed inference: %+v %v", plan, err)
	}
	teamID, err := f.store.SavePlanTeam(ctx, ws, planID, store.PlanTeam{Name: "Kanban dates", PlanningStyle: "Kanban"}, true)
	if err != nil {
		t.Fatal(err)
	}
	kanbanScenario, err := f.store.CreatePlanScenario(ctx, ws, f.ownerID, planID, "Kanban dates", "blue", 0)
	if err != nil {
		t.Fatal(err)
	}
	teamValue, _ := json.Marshal(teamID)
	if err = f.store.SetPlanChange(ctx, ws, f.ownerID, planID, kanbanScenario, issue.ID, "team", teamValue, json.RawMessage(`null`)); err != nil {
		t.Fatal(err)
	}
	read(kanbanScenario, "", "", "")
	// Live release changes are visible without editing the issue's snapshot.
	end = "2026-11-27"
	if _, err = f.store.SaveVersion(ctx, ws, f.ownerID, project.ID, version.ID, store.VersionUpdate{ReleaseDate: &end}); err != nil {
		t.Fatal(err)
	}
	call(http.MethodPost, "/settings", url.Values{"action": {"scheduling"}, "inferredDates": {"ReleaseDates"}}, 303)
	read(plan.ScenarioID, start, end, "Release dates")
	archived := true
	if _, err = f.store.SaveVersion(ctx, ws, f.ownerID, project.ID, version.ID, store.VersionUpdate{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	read(plan.ScenarioID, "", "", "")
	call(http.MethodPost, "/settings", url.Values{"action": {"scheduling"}, "inferredDates": {"None"}}, 303)
	read(plan.ScenarioID, "", "", "")
}

func TestPlanAutoScheduleUsesLiveReleasesAndProtectsActiveSprints(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "RAS", Name: "Release scheduling", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "scrum")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM plans WHERE workspace_id=$1`, `DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`} {
			if _, e := f.store.Pool.Exec(ctx, q, ws); e != nil {
				t.Error(e)
			}
		}
	})
	name, start, end := "Release fallback", "2026-11-02", "2026-11-20"
	version, err := f.store.SaveVersion(ctx, ws, f.ownerID, project.ID, "", store.VersionUpdate{Name: &name, StartDate: &start, ReleaseDate: &end})
	if err != nil {
		t.Fatal(err)
	}
	versions, _ := json.Marshal([]map[string]string{{"id": version.ID}})
	create := func(summary string) *models.Issue {
		t.Helper()
		issue, _, e := f.store.CreateIssue(ctx, f.ownerID, project.ID, summary, json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_story", "pr_medium", "", nil, map[string]json.RawMessage{"fixVersions": versions}, "", "")
		if e != nil {
			t.Fatal(e)
		}
		return issue
	}
	releaseWork, activeWork := create("Release-only work"), create("Active sprint work")
	board, err := f.store.CreateBoard(ctx, f.ownerID, ws, store.BoardCreate{Name: "Active sprint board", Type: "scrum", ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	sprint, _, err := f.store.CreateSprint(ctx, f.ownerID, ws, board.ID, "In progress", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Pool.Exec(ctx, `UPDATE sprints SET state='active',start_date='2026-10-05',end_date='2026-10-16' WHERE id=$1`, sprint.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.AddIssueToSprint(ctx, f.ownerID, ws, sprint.ID, activeWork.ID, "0"); err != nil {
		t.Fatal(err)
	}
	projectNumber, _ := strconv.ParseInt(project.ID, 10, 64)
	planID, err := f.store.CreatePlan(ctx, ws, f.ownerID, store.Plan{Name: "Release scheduling", LeadAccountID: f.ownerID, IssueSources: []store.PlanIssueSource{{Type: "Project", Value: projectNumber}}, Scheduling: store.PlanScheduling{Estimation: "StoryPoints", StartDate: store.PlanDateField{Type: "TargetStartDate"}, EndDate: store.PlanDateField{Type: "DueDate"}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.store.Plan(ctx, ws, planID)
	if err != nil {
		t.Fatal(err)
	}
	// No inferred-date setting or team is needed for the release fallback.
	if plan.Scheduling.InferredDates != "None" && plan.Scheduling.InferredDates != "" {
		t.Fatalf("unexpected inference %s", plan.Scheduling.InferredDates)
	}
	r := httptest.NewRequest(http.MethodPost, "/plans/"+strconv.FormatInt(planID, 10)+"/auto-schedule", strings.NewReader(url.Values{"scope": {"all"}, "from": {"2026-10-01"}}.Encode()))
	r.SetPathValue("id", strconv.FormatInt(planID, 10))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
	w := httptest.NewRecorder()
	f.handler.PlanAutoSchedule(w, r)
	if w.Code != 303 {
		t.Fatalf("scheduler refused %d: %s", w.Code, w.Body.String())
	}
	location, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	notice := location.Query().Get("notice")
	if !strings.Contains(notice, "1 work items") || !strings.Contains(notice, "active sprint") {
		t.Fatalf("missing scheduling feedback: %s", notice)
	}
	changes, err := f.store.PlanChanges(ctx, ws, planID, plan.ScenarioID)
	if err != nil || len(changes) != 2 {
		t.Fatalf("scheduler drafts %+v: %v", changes, err)
	}
	for _, c := range changes {
		if c.IssueID != releaseWork.ID {
			t.Fatal("active sprint was changed")
		}
		if c.Field == "startDate" && string(c.Value) != `"2026-11-02"` || c.Field == "endDate" && string(c.Value) != `"2026-11-20"` {
			t.Fatalf("unexpected release dates %+v", c)
		}
	}
	committed, err := f.store.IssueByIDOrKey(ctx, ws, releaseWork.ID)
	if err != nil || committed.DueDate != "" {
		t.Fatalf("draft changed committed issue %+v: %v", committed, err)
	}
	// A fresh scenario uses current version dates, not an issue snapshot.
	end = "2026-11-27"
	if _, err = f.store.SaveVersion(ctx, ws, f.ownerID, project.ID, version.ID, store.VersionUpdate{ReleaseDate: &end}); err != nil {
		t.Fatal(err)
	}
	scenario, err := f.store.CreatePlanScenario(ctx, ws, f.ownerID, planID, "Live release dates", "blue", 0)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.store.AutoSchedulePlan(ctx, ws, f.ownerID, plan, scenario, store.PlanScheduleRequest{All: true, From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, time.Now())
	if err != nil || result.Scheduled != 1 {
		t.Fatalf("fresh release run %+v: %v", result, err)
	}
	found := false
	for _, c := range result.Changes {
		if c.Field == "endDate" && string(c.Value) == `"2026-11-27"` {
			found = true
		}
	}
	if !found {
		t.Fatalf("stale version dates %+v", result.Changes)
	}
}
