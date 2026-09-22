package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// A deployment carries what its sender chose to send. A name, an address and
// an environment name are all optional, and the delivery report reads them out
// of the payload -- so one sent without them used to fail the whole report,
// which is what the demo company's deployments did to the DORA gadget.
func TestDORAReportReadsADeploymentSentWithoutAnAddress(t *testing.T) {
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
		WorkspaceID: workspaceID, Key: "DPL" + NewID("t")[len(NewID("t"))-4:], Name: "Delivery payload test",
		LeadAccountID: actorID, AssigneeType: "UNASSIGNED", ProjectTypeKey: "software",
	}, "kanban")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, project.ID) })
	issue, _, err := st.CreateIssue(ctx, actorID, project.ID, "Shipped work",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	pipeline := "pipeline-" + NewID("p")
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM software_deployments WHERE workspace_id=$1 AND pipeline_id=$2`, workspaceID, pipeline)
	})
	// Everything optional left out: no displayName, no url, no environment
	// name in the payload.
	if err := st.UpsertSoftwareDeployments(ctx, workspaceID, []models.SoftwareDeployment{{
		PipelineID: pipeline, EnvironmentID: "prod", DeploymentSequenceNumber: 1, UpdateSequenceNumber: 1,
		IssueKeys: []string{issue.Key}, State: "successful", EnvironmentType: "production",
		LastUpdated: time.Now().Add(-24 * time.Hour),
		Payload:     json.RawMessage(`{"state":"successful","environment":{"type":"production"}}`),
	}}); err != nil {
		t.Fatalf("record the deployment: %v", err)
	}
	report, err := st.DORAReport(ctx, workspaceID, project.ID, actorID, 30, time.Now())
	if err != nil {
		t.Fatalf("a deployment sent without an address failed the report: %v", err)
	}
	if report.DeploymentFrequency != 1 {
		t.Fatalf("deployments counted = %d, want 1", report.DeploymentFrequency)
	}
	if len(report.Recent) != 1 || report.Recent[0].URL != "" || report.Recent[0].DisplayName != "" {
		t.Fatalf("recent deliveries = %+v", report.Recent)
	}
}
