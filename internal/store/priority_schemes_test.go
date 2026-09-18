package store_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

// A project offers the priorities of the scheme it uses, and takes that
// scheme's default when a work item names no priority. Removing the project
// from the scheme hands it back to the site's default scheme.
func TestProjectFollowsItsPriorityScheme(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	ws, admin, projectID := store.NewID("ws"), store.NewID("usr"), store.NewID("prj")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Priorities')`, ws)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Priority admin')`, admin, admin+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, ws, admin)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,$3,'Priority scheme','wf_default',$4)`,
		projectID, ws, "PS"+strings.ToUpper(projectID[len(projectID)-4:]), admin)
	t.Cleanup(func() {
		for _, statement := range []string{
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM project_priority_schemes WHERE workspace_id=$1`,
			`DELETE FROM projects WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(statement, ws)
		}
		exec(`DELETE FROM users WHERE id=$1`, admin)
	})

	site, err := st.PrioritiesForWorkspace(ctx, ws)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, priority := range site {
		byName[priority.Name] = priority.ID
	}
	// Before a scheme of its own, the project follows the site's default one.
	defaultPriority, err := st.ProjectDefaultPriority(ctx, ws, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if defaultPriority != byName["Medium"] {
		t.Fatalf("the project's default priority is %q", defaultPriority)
	}
	name, description, defaultID := "Support", "What support work may be", byName["Low"]
	priorities := []string{byName["High"], byName["Low"]}
	projects := []string{projectID}
	schemeID, err := st.CreatePriorityScheme(ctx, ws, store.PrioritySchemeInput{
		Name: &name, Description: &description, DefaultPriority: &defaultID,
		PriorityIDs: &priorities, ProjectIDs: &projects,
	})
	if err != nil {
		t.Fatal(err)
	}

	offered, err := st.PrioritiesForProject(ctx, ws, projectID)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(offered))
	for _, priority := range offered {
		names = append(names, priority.Name)
	}
	if strings.Join(names, ",") != "High,Low" {
		t.Fatalf("the project offers %v", names)
	}
	for priority, want := range map[string]bool{"High": true, "Low": true, "Medium": false} {
		got, err := st.ProjectOffersPriority(ctx, ws, projectID, byName[priority])
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("the project offers %s: %v, want %v", priority, got, want)
		}
	}
	if defaultPriority, err = st.ProjectDefaultPriority(ctx, ws, projectID); err != nil || defaultPriority != byName["Low"] {
		t.Fatalf("the scheme's default is %q (%v)", defaultPriority, err)
	}

	// A scheme a project uses stays; the project leaves it first.
	if err := st.DeletePriorityScheme(ctx, ws, schemeID); err == nil {
		t.Fatal("a scheme with a project was deleted")
	}
	if _, err := st.UpdatePriorityScheme(ctx, ws, schemeID, store.PrioritySchemeInput{
		AddRemove: &store.PrioritySchemeAddRemove{RemoveProjects: []string{projectID}},
	}); err != nil {
		t.Fatal(err)
	}
	if defaultPriority, err = st.ProjectDefaultPriority(ctx, ws, projectID); err != nil || defaultPriority != byName["Medium"] {
		t.Fatalf("back on the site's scheme, the default is %q (%v)", defaultPriority, err)
	}
	if err := st.DeletePriorityScheme(ctx, ws, schemeID); err != nil {
		t.Fatal(err)
	}
}
