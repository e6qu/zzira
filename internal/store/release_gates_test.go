package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

// A project can ask for its conditions to be met before a version ships: every
// approver has approved, and nothing unresolved is left in it. A project that
// asks for neither ships whenever an administrator says so.
func TestReleaseGatesRefuseAVersionUntilTheirConditionsHold(t *testing.T) {
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
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(ctx, actorID, models.Project{
		WorkspaceID: workspaceID, Key: "RG" + NewID("t")[len(NewID("t"))-4:], Name: "Release gate test",
		LeadAccountID: actorID, AssigneeType: "UNASSIGNED", ProjectTypeKey: "software",
	}, "kanban")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, project.ID) })
	released := true
	version, err := st.SaveVersion(ctx, workspaceID, actorID, project.ID, "", VersionUpdate{Name: strPtr("1.0")})
	if err != nil {
		t.Fatalf("create the version: %v", err)
	}
	issue, _, err := st.CreateIssue(ctx, actorID, project.ID, "Still open",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil,
		map[string]json.RawMessage{"fixVersions": json.RawMessage(`[{"id":"` + version.ID + `"}]`)}, "", "")
	if err != nil {
		t.Fatalf("raise work in the version: %v", err)
	}

	// With no conditions, it ships.
	if _, err := st.SaveVersion(ctx, workspaceID, actorID, project.ID, version.ID, VersionUpdate{Released: &released}); err != nil {
		t.Fatalf("a project with no conditions refused a release: %v", err)
	}
	unreleased := false
	if _, err := st.SaveVersion(ctx, workspaceID, actorID, project.ID, version.ID, VersionUpdate{Released: &unreleased}); err != nil {
		t.Fatal(err)
	}

	// Asking for resolved work refuses it, and says how much is open.
	if err := st.SaveReleaseGates(ctx, workspaceID, actorID, project.ID, ReleaseGates{RequireResolved: true}); err != nil {
		t.Fatalf("save the conditions: %v", err)
	}
	_, err = st.SaveVersion(ctx, workspaceID, actorID, project.ID, version.ID, VersionUpdate{Released: &released})
	if err == nil || !strings.Contains(err.Error(), "1 work items in it are not") {
		t.Fatalf("release with unresolved work = %v", err)
	}
	refusal, err := st.ReleaseRefusalFor(ctx, project.ID, version.ID, "")
	if err != nil || !strings.Contains(refusal, "work is resolved") {
		t.Fatalf("the page would say %q, %v", refusal, err)
	}

	// Resolving it lets the version ship.
	resolution := "res_done"
	if _, _, err := st.UpdateIssue(ctx, actorID, workspaceID, issue.ID, IssueUpdate{ResolutionID: &resolution}); err != nil {
		t.Fatalf("resolve the work: %v", err)
	}
	if _, err := st.SaveVersion(ctx, workspaceID, actorID, project.ID, version.ID, VersionUpdate{Released: &released}); err != nil {
		t.Fatalf("release with the work resolved: %v", err)
	}

	// Asking for approvals refuses the next version while one is waiting.
	if err := st.SaveReleaseGates(ctx, workspaceID, actorID, project.ID, ReleaseGates{RequireApprovals: true}); err != nil {
		t.Fatal(err)
	}
	second, err := st.SaveVersion(ctx, workspaceID, actorID, project.ID, "", VersionUpdate{Name: strPtr("1.1")})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddVersionApprover(ctx, workspaceID, actorID, second.ID, actorID, "Signs it off"); err != nil {
		t.Fatalf("add an approver: %v", err)
	}
	_, err = st.SaveVersion(ctx, workspaceID, actorID, project.ID, second.ID, VersionUpdate{Released: &released})
	if err == nil || !strings.Contains(err.Error(), "1 of them have not") {
		t.Fatalf("release with an approval waiting = %v", err)
	}
	if err := st.DecideVersionApproval(ctx, workspaceID, actorID, second.ID, true, ""); err != nil {
		t.Fatalf("approve it: %v", err)
	}
	if _, err := st.SaveVersion(ctx, workspaceID, actorID, project.ID, second.ID, VersionUpdate{Released: &released}); err != nil {
		t.Fatalf("release once approved: %v", err)
	}
}

func strPtr(value string) *string { return &value }
