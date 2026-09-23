package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// A lead time is how long a change took to reach production. A work item that
// is deployed again later -- a rollback, a re-release, a deployment that
// carries it along with newer work -- must not make the change that shipped it
// months ago read as a change that took months.
func TestDORALeadTimeMeasuresTheDeliveryThatCarriedTheChange(t *testing.T) {
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
		WorkspaceID: workspaceID, Key: "LED" + NewID("t")[len(NewID("t"))-4:], Name: "Lead time test",
		LeadAccountID: actorID, AssigneeType: "UNASSIGNED", ProjectTypeKey: "software",
	}, "kanban")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, project.ID) })
	issue, _, err := st.CreateIssue(ctx, actorID, project.ID, "Long lived work",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	pipeline := "pipeline-" + NewID("p")
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM software_deployments WHERE workspace_id=$1 AND pipeline_id=$2`, workspaceID, pipeline)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM development_repositories WHERE workspace_id=$1 AND id=$2`, workspaceID, "lead-repo-"+project.ID)
	})
	deployment := func(sequence int64, at time.Time) models.SoftwareDeployment {
		return models.SoftwareDeployment{
			PipelineID: pipeline, EnvironmentID: "prod", DeploymentSequenceNumber: sequence, UpdateSequenceNumber: sequence,
			IssueKeys: []string{issue.Key}, State: "successful", EnvironmentType: "production", LastUpdated: at,
			Payload: json.RawMessage(`{"state":"successful","environment":{"type":"production"}}`),
		}
	}
	// The change shipped a hundred days ago, two hours after it was written,
	// and the same work item went out again yesterday.
	shipped := now.AddDate(0, 0, -100)
	if err := st.UpsertSoftwareDeployments(ctx, workspaceID, []models.SoftwareDeployment{
		deployment(1, shipped), deployment(2, now.AddDate(0, 0, -1)),
	}); err != nil {
		t.Fatalf("record the deployments: %v", err)
	}
	written := shipped.Add(-2 * time.Hour)
	commitPayload, err := json.Marshal(map[string]any{
		"id": "lead-commit", "displayId": "lead", "message": "Ship " + issue.Key,
		"updateSequenceId": 1, "authorTimestamp": written.Format(time.RFC3339), "issueKeys": []string{issue.Key},
	})
	if err != nil {
		t.Fatal(err)
	}
	repositoryPayload, err := json.Marshal(map[string]any{"id": "lead-repo-" + project.ID, "name": "Lead repo", "updateSequenceId": 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertDevelopmentRepositories(ctx, workspaceID, []models.DevelopmentRepository{{
		ID: "lead-repo-" + project.ID, UpdateSequenceID: 1, Name: "Lead repo", Payload: repositoryPayload,
		Properties: json.RawMessage(`{}`),
		Entities: []models.DevelopmentEntity{{
			RepositoryID: "lead-repo-" + project.ID, Type: "commit", ID: "lead-commit", UpdateSequenceID: 1,
			IssueKeys: []string{issue.Key}, Name: "Ship " + issue.Key, OccurredAt: &written, Payload: commitPayload,
		}},
	}}); err != nil {
		t.Fatalf("record the commit: %v", err)
	}

	// Last month: yesterday's deployment is in the window, but the change it
	// carries reached production a hundred days ago, so it is not this
	// month's lead time.
	month, err := st.DORAReport(ctx, workspaceID, project.ID, actorID, 30, now)
	if err != nil {
		t.Fatalf("read the report: %v", err)
	}
	if month.LeadTimeSamples != 0 {
		t.Fatalf("a change that shipped 100 days ago counted as %d of this month's lead times (%s)",
			month.LeadTimeSamples, month.LeadTimeDisplay)
	}

	// Reading the window the change actually shipped in gives the two hours
	// between writing it and shipping it.
	shippedWindow, err := st.DORAReport(ctx, workspaceID, project.ID, actorID, 7, shipped.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("read the report: %v", err)
	}
	if shippedWindow.LeadTimeSamples != 1 || shippedWindow.LeadTimeSeconds != 7200 {
		t.Fatalf("lead time = %d samples, %d seconds, want 1 and 7200", shippedWindow.LeadTimeSamples, shippedWindow.LeadTimeSeconds)
	}
}
