package api3

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestBuildAndDeploymentContractJourney(t *testing.T) {
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
	workspaceID, actorID, projectID := store.NewID("ws"), store.NewID("usr"), store.NewID("project")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Delivery test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Delivery user')`, actorID, actorID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actorID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actorID, store.HashToken(actorID))
	exec(`INSERT INTO projects(id,workspace_id,key,name) VALUES($1,$2,'SHIP','Delivery project')`, projectID, workspaceID)
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM organization_audit_events WHERE actor_id=$1`, actorID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, actorID)
		exec(`DELETE FROM users WHERE id=$1`, actorID)
	})
	service := &commands.Service{Store: st}
	issue, _, err := service.CreateIssue(ctx, commands.CreateIssueInput{ActorID: actorID, WorkspaceID: workspaceID, ProjectIDOrKey: projectID, Summary: "Ship safely", IssueTypeID: "it_task"})
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{Store: st, Commands: service, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(actorID+"@example.test", actorID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	build := `{"properties":{"accountId":"delivery-provider"},"builds":[{"schemaVersion":"1.0","pipelineId":"pipeline-1","buildNumber":42,"updateSequenceNumber":2,"displayName":"Build 42","url":"https://ci.example/build/42","state":"successful","lastUpdated":"2026-09-06T12:00:00Z","associations":[{"associationType":"issueIdOrKeys","values":["` + issue.ID + `","UNKNOWN-99"]}],"testInfo":{"totalNumber":10,"numberPassed":10,"numberFailed":0}}]}`
	response := call("POST", "/rest/builds/0.1/bulk", build, 202)
	if !strings.Contains(response.Body.String(), `"acceptedBuilds":[{"buildNumber":42,"pipelineId":"pipeline-1"}]`) || !strings.Contains(response.Body.String(), `"unknownIssueKeys":["UNKNOWN-99"]`) {
		t.Fatal(response.Body.String())
	}
	deployment := `{"properties":{"accountId":"delivery-provider"},"deployments":[{"deploymentSequenceNumber":7,"updateSequenceNumber":3,"displayName":"Production 7","url":"https://deploy.example/7","description":"Release","lastUpdated":"2026-09-06T13:00:00Z","state":"successful","pipeline":{"id":"pipeline-1","displayName":"Main pipeline","url":"https://ci.example/pipeline-1"},"environment":{"id":"production","displayName":"Production","type":"production"},"issueKeys":["` + issue.Key + `"]}]}`
	response = call("POST", "/rest/deployments/0.1/bulk", deployment, 202)
	if !strings.Contains(response.Body.String(), `"acceptedDeployments":[{"deploymentSequenceNumber":7,"environmentId":"production","pipelineId":"pipeline-1"}]`) {
		t.Fatal(response.Body.String())
	}
	items, err := st.DeliveryItemsForIssues(ctx, workspaceID, []string{issue.Key})
	if err != nil || len(items) != 2 || items[0].Kind != "deployment" || items[1].Kind != "build" {
		t.Fatalf("delivery items = %+v, %v", items, err)
	}
	stale := strings.Replace(strings.Replace(build, `"updateSequenceNumber":2`, `"updateSequenceNumber":1`, 1), `"displayName":"Build 42"`, `"displayName":"Stale"`, 1)
	call("POST", "/rest/builds/0.1/bulk", stale, 202)
	if got := call("GET", "/rest/builds/0.1/pipelines/pipeline-1/builds/42", "", 200); !strings.Contains(got.Body.String(), `"displayName": "Build 42"`) {
		t.Fatal(got.Body.String())
	}
	var cloudID string
	if err := st.Pool.QueryRow(ctx, `SELECT cloud_id::text FROM workspaces WHERE id=$1`, workspaceID).Scan(&cloudID); err != nil {
		t.Fatal(err)
	}
	call("GET", "/jira/builds/0.1/cloud/"+cloudID+"/pipelines/pipeline-1/builds/42", "", 200)
	gating := call("GET", "/jira/deployments/0.1/cloud/"+cloudID+"/pipelines/pipeline-1/environments/production/deployments/7/gating-status", "", 200)
	if !strings.Contains(gating.Body.String(), `"gatingStatus":"allowed"`) {
		t.Fatal(gating.Body.String())
	}
	call("DELETE", "/rest/deployments/0.1/pipelines/pipeline-1/environments/production/deployments/7?_updateSequenceNumber=2", "", 202)
	call("GET", "/rest/deployments/0.1/pipelines/pipeline-1/environments/production/deployments/7", "", 200)
	call("DELETE", "/rest/deployments/0.1/pipelines/pipeline-1/environments/production/deployments/7?_updateSequenceNumber=3", "", 202)
	call("GET", "/rest/deployments/0.1/pipelines/pipeline-1/environments/production/deployments/7", "", 404)
	call("DELETE", "/rest/builds/0.1/bulkByProperties?accountId=delivery-provider&_updateSequenceNumber=2", "", 202)
	call("GET", "/rest/builds/0.1/pipelines/pipeline-1/builds/42", "", 404)
	invalid := `{"builds":[{"pipelineId":"bad","buildNumber":1,"updateSequenceNumber":1,"displayName":"Bad","url":"https://ci.example/bad","state":"not-a-state","lastUpdated":"2026-09-06T12:00:00Z"}]}`
	if rejected := call("POST", "/rest/builds/0.1/bulk", invalid, 202); !strings.Contains(rejected.Body.String(), `"rejectedBuilds":[`) {
		t.Fatal(rejected.Body.String())
	}
}
