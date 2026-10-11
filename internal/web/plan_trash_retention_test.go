package web

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/e6qu/zzira/internal/models"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

func retentionPlan(t *testing.T, f *apiTokenFixture, name, status string) int64 {
	t.Helper()
	var id int64
	err := f.store.Pool.QueryRow(context.Background(), `INSERT INTO plans(workspace_id,name,status,created_by,scheduling,exclusion_rules) VALUES($1,$2,$3,$4,'{"estimation":"StoryPoints"}','{}') RETURNING id`, f.handler.WorkspaceSlug, name, status, f.ownerID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPlanTrashRetentionDeadlineAndRestore(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	t.Cleanup(func() {
		if _, err := f.store.Pool.Exec(ctx, `DELETE FROM plans WHERE workspace_id=$1`, ws); err != nil {
			t.Error(err)
		}
	})
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "PTR", Name: "Retained work", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, query, ws); err != nil {
				t.Error(err)
			}
		}
	})
	issue, _, err := f.store.CreateIssue(ctx, f.ownerID, project.ID, "Committed work", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	id := retentionPlan(t, f, "Retention deadline", "Active")
	if err := f.store.SetPlanStatus(ctx, ws, f.ownerID, id, "Trashed"); err != nil {
		t.Fatal(err)
	}
	plan, err := f.store.Plan(ctx, ws, id)
	if err != nil || plan.TrashedAt == nil {
		t.Fatalf("trash timestamp absent: %+v %v", plan, err)
	}
	if _, err = f.store.Pool.Exec(ctx, `UPDATE plans SET trashed_at=now()-interval '59 days',updated_at=now() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if n, err := f.store.PurgeExpiredPlans(ctx, ws); err != nil || n != 0 {
		t.Fatalf("early purge: %d %v", n, err)
	}
	if err = f.store.ChangePlanStatus(ctx, ws, f.ownerID, id, "Trashed", "Active"); err != nil {
		t.Fatal(err)
	}
	plan, err = f.store.Plan(ctx, ws, id)
	if err != nil || plan.TrashedAt != nil {
		t.Fatalf("restore retained trash timer: %+v %v", plan, err)
	}
	if err = f.store.SetPlanStatus(ctx, ws, f.ownerID, id, "Trashed"); err != nil {
		t.Fatal(err)
	}
	plan, err = f.store.Plan(ctx, ws, id)
	if err != nil || plan.TrashedAt == nil {
		t.Fatalf("retrash timer absent: %+v %v", plan, err)
	}
	var recent bool
	if err = f.store.Pool.QueryRow(ctx, `SELECT trashed_at>now()-interval '1 minute' FROM plans WHERE id=$1`, id).Scan(&recent); err != nil || !recent {
		t.Fatalf("retrash did not reset deadline: %v %v", recent, err)
	}
	if _, err = f.store.Pool.Exec(ctx, `UPDATE plans SET trashed_at=now()-interval '60 days',updated_at=now() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = f.store.ChangePlanStatus(ctx, ws, f.ownerID, id, "Trashed", "Active"); !errors.Is(err, store.ErrPlanTrashExpired) {
		t.Fatalf("expired restore: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/plans/"+strconv.FormatInt(id, 10)+"/lifecycle", strings.NewReader(url.Values{"action": {"restore"}, "status": {"Trashed"}}.Encode()))
	r.SetPathValue("id", strconv.FormatInt(id, 10))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
	w := httptest.NewRecorder()
	f.handler.PlanLifecycle(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "60-day") {
		t.Fatalf("expired restore response %d: %s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest(http.MethodGet, "/plans?status=Trashed", nil)
	r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
	w = httptest.NewRecorder()
	f.handler.PlansPage(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Recovery period expired") || strings.Contains(w.Body.String(), ">Restore</button>") {
		t.Fatalf("expired recovery controls %d: %s", w.Code, w.Body.String())
	}
	var scenarioID, teamID int64
	if err = f.store.Pool.QueryRow(ctx, `INSERT INTO plan_scenarios(id,plan_id,name,color,created_by) VALUES(nextval('jira_plan_scenario_id'),$1,'Expired draft','blue',$2) RETURNING id`, id, f.ownerID).Scan(&scenarioID); err != nil {
		t.Fatal(err)
	}
	if err = f.store.Pool.QueryRow(ctx, `INSERT INTO plan_teams(plan_id,name,planning_style) VALUES($1,'Expired team','Kanban') RETURNING id`, id).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	for _, setup := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO plan_scenario_changes(scenario_id,issue_id,field,value,changed_by) VALUES($1,$2,'summary','"Uncommitted draft"',$3)`, []any{scenarioID, issue.ID, f.ownerID}},
		{`INSERT INTO plan_scenario_iteration_capacity(scenario_id,team_id,iteration,capacity,changed_by) VALUES($1,$2,'2026-10-12',17.5,$3)`, []any{scenarioID, teamID, f.ownerID}},
		{`INSERT INTO plan_views(workspace_id,plan_id,name,created_by) VALUES($1,$2,'Expired view',$3)`, []any{ws, id, f.ownerID}},
		{`INSERT INTO plan_issue_sources(plan_id,position,type,value) VALUES($1,0,'Project',$2::bigint)`, []any{id, project.ID}},
	} {
		if _, err = f.store.Pool.Exec(ctx, setup.query, setup.args...); err != nil {
			t.Fatal(err)
		}
	}
	n, err := f.store.PurgeExpiredPlans(ctx, ws)
	if err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if _, err = f.store.Plan(ctx, ws, id); !errors.Is(err, store.ErrPlanNotFound) {
		t.Fatalf("expired plan retained: %v", err)
	}
	var children, audits int
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM plan_scenarios WHERE plan_id=$1`, id).Scan(&children); err != nil || children != 0 {
		t.Fatalf("orphaned scenarios: %d %v", children, err)
	}
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE target_id=$1 AND action='plan.trash_expired' AND actor_id IS NULL`, "plan:"+strconv.FormatInt(id, 10)).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("system deletion audit: %d %v", audits, err)
	}
	for _, table := range []string{"plan_scenario_changes", "plan_scenario_iteration_capacity"} {
		if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE scenario_id=$1`, scenarioID).Scan(&children); err != nil || children != 0 {
			t.Fatalf("orphaned %s: %d %v", table, children, err)
		}
	}
	for _, table := range []string{"plan_teams", "plan_views", "plan_issue_sources"} {
		if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE plan_id=$1`, id).Scan(&children); err != nil || children != 0 {
			t.Fatalf("orphaned %s: %d %v", table, children, err)
		}
	}
	retained, err := f.store.IssueByIDOrKey(ctx, ws, issue.ID)
	if err != nil || retained.Summary != "Committed work" {
		t.Fatalf("purge changed committed work: %+v %v", retained, err)
	}
	if n, err = f.store.PurgeExpiredPlans(ctx, ws); err != nil || n != 0 {
		t.Fatalf("repeat purge: %d %v", n, err)
	}
}

func TestPlanTrashRetentionBatchesAndTenantIsolation(t *testing.T) {
	f, g := newAPITokenFixture(t), newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	for _, fixture := range []*apiTokenFixture{f, g} {
		t.Cleanup(func() {
			if _, err := fixture.store.Pool.Exec(ctx, `DELETE FROM plans WHERE workspace_id=$1`, fixture.handler.WorkspaceSlug); err != nil {
				t.Error(err)
			}
		})
	}
	other := retentionPlan(t, g, "Other tenant expired", "Trashed")
	archived := retentionPlan(t, f, "Archived indefinitely", "Archived")
	recent := retentionPlan(t, f, "Recent trash", "Trashed")
	if _, err := f.store.Pool.Exec(ctx, `INSERT INTO plans(workspace_id,name,status,created_by,scheduling,exclusion_rules) SELECT $1,'Expired batch '||n,'Trashed',$2,'{"estimation":"StoryPoints"}','{}' FROM generate_series(1,101) n`, ws, f.ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(ctx, `UPDATE plans SET trashed_at=now()-interval '61 days' WHERE (workspace_id=$1 AND name LIKE 'Expired batch %') OR id=$2`, ws, other); err != nil {
		t.Fatal(err)
	}
	runner := &store.PlanTrashRunner{Store: f.store, WorkspaceID: ws}
	if err := runner.DrainOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var remaining, audits int
	if err := f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM plans WHERE workspace_id=$1`, ws).Scan(&remaining); err != nil || remaining != 2 {
		t.Fatalf("bounded batches left %d: %v", remaining, err)
	}
	for _, id := range []int64{archived, recent} {
		if _, err := f.store.Plan(ctx, ws, id); err != nil {
			t.Fatalf("unexpired plan removed: %v", err)
		}
	}
	if _, err := g.store.Plan(ctx, g.handler.WorkspaceSlug, other); err != nil {
		t.Fatalf("scoped purge crossed tenant: %v", err)
	}
	if err := f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events a JOIN sites s ON s.organization_id=a.organization_id WHERE s.workspace_id=$1 AND a.action='plan.trash_expired'`, ws).Scan(&audits); err != nil || audits != 101 {
		t.Fatalf("batch audit count %d: %v", audits, err)
	}
	var wg sync.WaitGroup
	counts, errs := make([]int, 2), make([]error, 2)
	for i := range counts {
		wg.Add(1)
		go func() { defer wg.Done(); counts[i], errs[i] = g.store.PurgeExpiredPlans(ctx, g.handler.WorkspaceSlug) }()
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || counts[0]+counts[1] != 1 {
		t.Fatalf("replica purge counts %v errors %v", counts, errs)
	}
}
