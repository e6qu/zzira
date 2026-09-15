package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestDeploymentGatingThroughChangeRequests covers Jira deployment gating: a
// service desk connects a deployment provider and gates environment types,
// each gated deployment opens one change request there, and the request's
// approval decides the deployment's gating status.
func TestDeploymentGatingThroughChangeRequests(t *testing.T) {
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
	if err = store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, adminID, memberID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	deskKey := fmt.Sprintf("DG%05d", time.Now().UnixNano()%100000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Deployment gating')`, workspaceID)
	people := []struct{ id, role, name string }{{adminID, "admin", "Release manager"}, {memberID, "member", "Developer"}}
	for _, person := range people {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, person.id, person.id+"@example.test", person.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, person.id, person.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, person.id, store.HashToken(person.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM notifications WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM service_desks WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM role_bindings WHERE scope_type='project' AND scope_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, person := range people {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, person.id)
			exec(`DELETE FROM users WHERE id=$1`, person.id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	callAs := func(accountID, method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(accountID+"@example.test", accountID)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, accountID, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	callAs(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+deskKey+`","name":"Gating `+deskKey+`","projectTypeKey":"service_desk","projectTemplateKey":"com.atlassian.servicedesk:simplified-it-service-management","leadAccountId":"`+adminID+`"}`, http.StatusCreated)
	var serviceDeskID, requestTypeID string
	if err = st.Pool.QueryRow(ctx, `SELECT sd.id FROM service_desks sd JOIN projects p ON p.id=sd.project_id WHERE p.workspace_id=$1 AND p.key=$2`, workspaceID, deskKey).Scan(&serviceDeskID); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 ORDER BY id::bigint LIMIT 1`, serviceDeskID).Scan(&requestTypeID); err != nil {
		t.Fatal(err)
	}
	created := map[string]any{}
	if err = json.Unmarshal([]byte(callAs(memberID, http.MethodPost, "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","requestFieldValues":{"summary":"Ship the release"}}`, http.StatusCreated)), &created); err != nil {
		t.Fatal(err)
	}
	workItem := fmt.Sprint(created["issueKey"])

	// Only the desk's administrators configure gating, for installed providers.
	if err = h.Commands.SetServiceDeploymentGate(ctx, memberID, workspaceID, serviceDeskID, "*", []string{"production"}); err == nil {
		t.Fatal("a member configured deployment gating")
	}
	if err = h.Commands.SetServiceDeploymentGate(ctx, adminID, workspaceID, serviceDeskID, "not-installed", []string{"production"}); err == nil {
		t.Fatal("gating was connected to an app that is not installed")
	}
	if err = h.Commands.SetServiceDeploymentGate(ctx, adminID, workspaceID, serviceDeskID, "*", []string{"production", "everywhere"}); err == nil {
		t.Fatal("gating accepted an unknown environment type")
	}
	if err = h.Commands.SetServiceDeploymentGate(ctx, adminID, workspaceID, serviceDeskID, "*", []string{"production"}); err != nil {
		t.Fatal(err)
	}

	deploy := func(sequence int, environmentType string) {
		t.Helper()
		body := `{"deployments":[{"deploymentSequenceNumber":` + strconv.Itoa(sequence) + `,"updateSequenceNumber":1,"displayName":"Release ` + strconv.Itoa(sequence) + `","url":"https://deploy.example/` + strconv.Itoa(sequence) + `","description":"Release","lastUpdated":"2026-09-15T10:00:00Z","state":"pending","pipeline":{"id":"pipeline-1","displayName":"Main pipeline","url":"https://ci.example/pipeline-1"},"environment":{"id":"` + environmentType + `-1","displayName":"` + environmentType + `","type":"` + environmentType + `"},"issueKeys":["` + workItem + `"]}]}`
		callAs(adminID, http.MethodPost, "/rest/deployments/0.1/bulk", body, http.StatusAccepted)
	}
	type gating struct {
		GatingStatus string `json:"gatingStatus"`
		Details      []struct {
			Type      string `json:"type"`
			IssueKey  string `json:"issueKey"`
			IssueLink string `json:"issueLink"`
		} `json:"details"`
	}
	status := func(sequence int, environmentType string) gating {
		t.Helper()
		var result gating
		if err := json.Unmarshal([]byte(callAs(adminID, http.MethodGet, "/rest/deployments/0.1/pipelines/pipeline-1/environments/"+environmentType+"-1/deployments/"+strconv.Itoa(sequence)+"/gating-status", "", http.StatusOK)), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	decide := func(changeKey, decision string) {
		t.Helper()
		approval, err := h.Commands.CreateServiceApproval(ctx, adminID, workspaceID, changeKey, "Change advisory board", []string{adminID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.Commands.AnswerServiceApproval(ctx, adminID, workspaceID, changeKey, approval.ID, decision); err != nil {
			t.Fatal(err)
		}
	}

	// A production deployment waits for its change request's approval.
	deploy(1, "production")
	first := status(1, "production")
	if first.GatingStatus != "awaiting" || len(first.Details) != 1 || first.Details[0].Type != "issue" || !strings.HasPrefix(first.Details[0].IssueKey, deskKey+"-") || first.Details[0].IssueLink != "https://zzira.test/service/requests/"+first.Details[0].IssueKey {
		t.Fatalf("gated deployment = %+v", first)
	}
	decide(first.Details[0].IssueKey, "approve")
	if approved := status(1, "production"); approved.GatingStatus != "allowed" || approved.Details[0].IssueKey != first.Details[0].IssueKey {
		t.Fatalf("approved deployment = %+v", approved)
	}
	// Resubmitting the deployment keeps its one change request.
	deploy(1, "production")
	var changeRequests int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM software_deployment_gatings WHERE workspace_id=$1`, workspaceID).Scan(&changeRequests); err != nil || changeRequests != 1 {
		t.Fatalf("gatings after resubmitting = %d err=%v", changeRequests, err)
	}

	// A declined change prevents the deployment.
	deploy(2, "production")
	second := status(2, "production")
	if second.GatingStatus != "awaiting" || second.Details[0].IssueKey == first.Details[0].IssueKey {
		t.Fatalf("second gated deployment = %+v", second)
	}
	decide(second.Details[0].IssueKey, "decline")
	if prevented := status(2, "production"); prevented.GatingStatus != "prevented" {
		t.Fatalf("declined deployment = %+v", prevented)
	}

	// Ungated environments, and deployments after gating is turned off, pass.
	deploy(3, "staging")
	if staging := status(3, "staging"); staging.GatingStatus != "allowed" || len(staging.Details) != 0 {
		t.Fatalf("ungated deployment = %+v", staging)
	}
	if err = h.Commands.SetServiceDeploymentGate(ctx, adminID, workspaceID, serviceDeskID, "*", nil); err != nil {
		t.Fatal(err)
	}
	deploy(4, "production")
	if ungated := status(4, "production"); ungated.GatingStatus != "allowed" || len(ungated.Details) != 0 {
		t.Fatalf("deployment after gating was turned off = %+v", ungated)
	}
	callAs(adminID, http.MethodGet, "/rest/deployments/0.1/pipelines/pipeline-1/environments/production-1/deployments/99/gating-status", "", http.StatusNotFound)
}
