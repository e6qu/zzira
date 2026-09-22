package store

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestASavedPlanViewKeepsHowThePlanIsRead(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, leadID := NewID("ws"), NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Plan view test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, leadID, leadID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, leadID)
	// A plan needs a source, and a project source names the project by the
	// number its id spells.
	projectID := "99" + strconv.FormatInt(time.Now().UnixNano()%100000, 10)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Plan view project','wf_default')`, projectID, workspaceID, "PV"+strings.ToUpper(workspaceID[len(workspaceID)-5:]))
	t.Cleanup(func() {
		exec(`DELETE FROM plans WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, leadID)
	})
	plan := Plan{Name: "Roadmap", LeadAccountID: leadID, Scheduling: PlanScheduling{Estimation: "StoryPoints"},
		IssueSources: []PlanIssueSource{{Type: "Project", Value: planViewProjectNumber(projectID)}}}
	NormalizePlan(&plan)
	planID, err := st.CreatePlan(ctx, workspaceID, leadID, plan)
	if err != nil {
		t.Fatal(err)
	}

	saved, err := st.SavePlanView(ctx, workspaceID, leadID, planID, PlanView{Name: "By team", GroupBy: "team", Query: "PAY", RollUp: true})
	if err != nil {
		t.Fatal(err)
	}
	// Saving the same name again is the same view, changed, rather than a
	// second one with the same name.
	again, err := st.SavePlanView(ctx, workspaceID, leadID, planID, PlanView{Name: "by team", GroupBy: "sprint"})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != saved.ID {
		t.Fatalf("saving over the view made a second one: %d and %d", saved.ID, again.ID)
	}
	views, err := st.PlanViews(ctx, workspaceID, planID)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].GroupBy != "sprint" || views[0].Query != "" || views[0].RollUp {
		t.Fatalf("views = %+v", views)
	}
	read, err := st.PlanViewByID(ctx, workspaceID, planID, saved.ID)
	if err != nil || read.Name != "By team" || read.RollUp {
		t.Fatalf("read back %+v, %v", read, err)
	}

	for _, refusal := range []struct {
		name string
		view PlanView
		want string
	}{
		{"no name", PlanView{Name: "  "}, "needs a name"},
		{"an unknown grouping", PlanView{Name: "Odd", GroupBy: "colour"}, "not a way to group"},
		{"a filter that is too long", PlanView{Name: "Long", Query: strings.Repeat("x", 201)}, "at most 200"},
	} {
		if _, err := st.SavePlanView(ctx, workspaceID, leadID, planID, refusal.view); err == nil || !strings.Contains(err.Error(), refusal.want) {
			t.Fatalf("%s: error = %v", refusal.name, err)
		}
	}

	if err := st.DeletePlanView(ctx, workspaceID, planID, saved.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeletePlanView(ctx, workspaceID, planID, saved.ID); err == nil {
		t.Fatal("deleting a view twice was allowed")
	}
	if views, err = st.PlanViews(ctx, workspaceID, planID); err != nil || len(views) != 0 {
		t.Fatalf("views after deleting = %+v, %v", views, err)
	}
}

// planViewProjectNumber is the number a numeric project id spells, which is
// how a plan's project source names it.
func planViewProjectNumber(id string) int64 {
	value, _ := strconv.ParseInt(id, 10, 64)
	return value
}
