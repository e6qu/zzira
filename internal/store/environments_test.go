package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// What is running where, and what has reached an earlier environment and not
// the one after it: the question somebody asks before promoting a release.
func TestProjectEnvironmentsSayWhatIsWaiting(t *testing.T) {
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
		WorkspaceID: workspaceID, Key: "EN" + NewID("t")[len(NewID("t"))-4:], Name: "Environments test",
		LeadAccountID: actorID, AssigneeType: "UNASSIGNED", ProjectTypeKey: "software",
	}, "kanban")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, project.ID) })
	shipped, _, err := st.CreateIssue(ctx, actorID, project.ID, "Shipped everywhere",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	waiting, _, err := st.CreateIssue(ctx, actorID, project.ID, "Only on staging",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	pipeline := "pipeline-" + NewID("p")
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM software_deployments WHERE workspace_id=$1 AND pipeline_id=$2`, workspaceID, pipeline)
	})
	now := time.Now().UTC()
	deployment := func(environment string, sequence int64, at time.Time, keys ...string) models.SoftwareDeployment {
		return models.SoftwareDeployment{
			PipelineID: pipeline, EnvironmentID: environment, DeploymentSequenceNumber: sequence, UpdateSequenceNumber: sequence,
			IssueKeys: keys, State: "successful", EnvironmentType: environment, EnvironmentName: environment,
			LastUpdated: at, Payload: json.RawMessage(`{"state":"successful","environment":{"type":"` + environment + `"}}`),
		}
	}
	if err := st.UpsertSoftwareDeployments(ctx, workspaceID, []models.SoftwareDeployment{
		deployment("staging", 1, now.Add(-3*time.Hour), shipped.Key, waiting.Key),
		deployment("production", 2, now.Add(-2*time.Hour), shipped.Key),
	}); err != nil {
		t.Fatalf("record the deployments: %v", err)
	}

	environments, err := st.ProjectEnvironments(ctx, workspaceID, project.ID, actorID)
	if err != nil {
		t.Fatalf("read the environments: %v", err)
	}
	if len(environments) != 2 || environments[0].Type != "staging" || environments[1].Type != "production" {
		t.Fatalf("environments = %+v, want staging before production", environments)
	}
	if len(environments[0].IssueKeys) != 2 || len(environments[0].Waiting) != 0 {
		t.Fatalf("staging = %+v", environments[0])
	}
	if len(environments[1].IssueKeys) != 1 || environments[1].IssueKeys[0] != shipped.Key {
		t.Fatalf("production holds %v, want %s", environments[1].IssueKeys, shipped.Key)
	}
	if len(environments[1].Waiting) != 1 || environments[1].Waiting[0] != waiting.Key {
		t.Fatalf("production is waiting for %v, want %s", environments[1].Waiting, waiting.Key)
	}
}
