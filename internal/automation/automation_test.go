package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

type automationFixture struct {
	t       *testing.T
	ctx     context.Context
	store   *store.Store
	service *Service
	handler *Handler
	ws      string
	admin   string
	member  string
	cloudID string
}

func newAutomationFixture(t *testing.T) *automationFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, st.Pool); err != nil {
		st.Close()
		t.Fatal(err)
	}
	fx := &automationFixture{t: t, ctx: ctx, store: st, ws: store.NewID("ws"), admin: store.NewID("usr"), member: store.NewID("usr")}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Automation test')`, fx.ws)
	for _, user := range []string{fx.admin, fx.member} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, user, user+"@example.test", user)
		role := "member"
		if user == fx.admin {
			role = "admin"
		}
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, fx.ws, user, role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, user, store.HashToken(user))
	}
	cmd := &commands.Service{Store: st}
	fx.service = &Service{Store: st, Commands: cmd}
	fx.handler = &Handler{Service: fx.service, WorkspaceSlug: fx.ws}
	fx.cloudID, err = fx.service.WorkspaceCloudID(ctx, fx.ws)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM issues WHERE workspace_id=$1`,
			`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`,
			`DELETE FROM projects WHERE workspace_id=$1`,
			`DELETE FROM automation_rules WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			_, _ = st.Pool.Exec(ctx, query, fx.ws)
		}
		for _, user := range []string{fx.admin, fx.member} {
			_, _ = st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE user_id=$1`, user)
			_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user)
		}
		st.Close()
	})
	return fx
}

func (fx *automationFixture) call(user, method, path string, body any, want int) map[string]any {
	fx.t.Helper()
	var content string
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			fx.t.Fatal(err)
		}
		content = string(raw)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(content))
	if user != "" {
		req.SetBasicAuth(user+"@example.test", user)
	}
	rec := httptest.NewRecorder()
	fx.handler.ServeHTTP(rec, req)
	if rec.Code != want {
		fx.t.Fatalf("%s %s: got %d, want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	result := map[string]any{}
	if len(rec.Body.Bytes()) > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			fx.t.Fatal(err)
		}
	}
	return result
}

func ruleBody(name, actor, state, query string, actions []map[string]any) map[string]any {
	return map[string]any{"rule": map[string]any{
		"actor": map[string]string{"actor": actor, "type": "ACCOUNT_ID"}, "name": name,
		"description": "Scheduled maintenance", "state": state, "labels": []string{"ops"},
		"ruleScopeARIs": []string{}, "components": actions,
		"trigger": map[string]any{"component": "TRIGGER", "type": "jira.jql.scheduled", "schemaVersion": 1,
			"value": map[string]any{"intervalMinutes": 60, "timezone": "UTC", "jql": query}},
	}, "connections": []any{}}
}

func TestRuleManagementGatewayLifecycle(t *testing.T) {
	fx := newAutomationFixture(t)
	base := "/gateway/api/automation/public/jira/" + fx.cloudID + "/rest/v1/rule"
	actions := []map[string]any{{"component": "ACTION", "type": "jira.issue.add-label", "value": map[string]string{"label": "reviewed"}}}
	fx.call("", http.MethodPost, base, ruleBody("Nightly", fx.admin, "ENABLED", "", actions), http.StatusUnauthorized)
	fx.call(fx.member, http.MethodPost, base, ruleBody("Nightly", fx.admin, "ENABLED", "", actions), http.StatusForbidden)
	created := fx.call(fx.admin, http.MethodPost, base, ruleBody("Nightly", fx.admin, "ENABLED", "", actions), http.StatusCreated)
	uuid, _ := created["ruleUuid"].(string)
	if !isUUIDv7(uuid) {
		t.Fatalf("generated uuid = %q, want UUIDv7", uuid)
	}
	got := fx.call(fx.admin, http.MethodGet, base+"/"+uuid, nil, http.StatusOK)
	readRule := got["rule"].(map[string]any)
	if readRule["name"] != "Nightly" || readRule["created"] == nil || readRule["updated"] == nil {
		t.Fatal(got)
	}
	if readRule["trigger"].(map[string]any)["id"] == "" || readRule["components"].([]any)[0].(map[string]any)["id"] == "" {
		t.Fatal("component IDs were not generated", got)
	}
	list := fx.call(fx.admin, http.MethodGet, base+"/summary?limit=1", nil, http.StatusOK)
	if len(list["data"].([]any)) != 1 {
		t.Fatal(list)
	}
	fx.call(fx.admin, http.MethodGet, base+"/summary?limit=0", nil, http.StatusBadRequest)
	fx.call(fx.admin, http.MethodPost, base+"/summary", map[string]any{}, http.StatusBadRequest)
	fx.call(fx.admin, http.MethodPost, base+"/summary", map[string]any{"limit": 1}, http.StatusOK)
	fx.call(fx.admin, http.MethodPost, base+"/summary", map[string]any{"limit": 0}, http.StatusBadRequest)
	filtered := fx.call(fx.admin, http.MethodPost, base+"/summary", map[string]any{"state": "ENABLED"}, http.StatusOK)
	if len(filtered["data"].([]any)) != 1 {
		t.Fatal(filtered)
	}
	scope := fmt.Sprintf("ari:cloud:jira:%s:project/10000", fx.cloudID)
	fx.call(fx.admin, http.MethodPut, base+"/"+uuid+"/rule-scope", map[string]any{"ruleScopeARIs": []string{scope}}, http.StatusOK)
	fx.call(fx.admin, http.MethodPut, base+"/"+uuid+"/state", map[string]string{"value": "DRAFT"}, http.StatusBadRequest)
	fx.call(fx.admin, http.MethodDelete, base+"/"+uuid, nil, http.StatusBadRequest)
	fx.call(fx.admin, http.MethodPut, base+"/"+uuid+"/state", map[string]string{"value": "DISABLED"}, http.StatusOK)
	updated := ruleBody("Nightly renamed", fx.admin, "DISABLED", "", actions)
	fx.call(fx.admin, http.MethodPut, strings.ReplaceAll(base, "/rest/v1/", "/rest/latest/")+"/"+uuid, updated, http.StatusOK)
	fx.call(fx.admin, http.MethodDelete, base+"/"+uuid, nil, http.StatusOK)
	fx.call(fx.admin, http.MethodGet, base+"/"+uuid, nil, http.StatusNotFound)
	smart := ruleBody("Event actor", fx.admin, "DISABLED", "", actions)
	smart["rule"].(map[string]any)["actor"] = map[string]string{"actor": "{{initiator.accountId}}", "type": "SMART_VALUE"}
	smartCreated := fx.call(fx.admin, http.MethodPost, base, smart, http.StatusCreated)
	smartUUID := smartCreated["ruleUuid"].(string)
	smartRead := fx.call(fx.admin, http.MethodGet, base+"/"+smartUUID, nil, http.StatusOK)
	if smartRead["rule"].(map[string]any)["actor"].(map[string]any)["type"] != "SMART_VALUE" {
		t.Fatal(smartRead)
	}
	smartList := fx.call(fx.admin, http.MethodPost, base+"/summary", map[string]any{"author": fx.admin}, http.StatusOK)
	if smartList["data"].([]any)[0].(map[string]any)["actorAccountId"] != nil {
		t.Fatal(smartList)
	}
	fx.call(fx.admin, http.MethodDelete, base+"/"+smartUUID, nil, http.StatusOK)
}

func TestTenantInfo(t *testing.T) {
	fx := newAutomationFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/_edge/tenant_info", nil)
	rec := httptest.NewRecorder()
	fx.handler.TenantInfo(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), fx.cloudID) {
		t.Fatalf("tenant info: %d %s", rec.Code, rec.Body.String())
	}
}

func TestScheduledRunnerAppliesActionsOnceAcrossConcurrentWorkers(t *testing.T) {
	fx := newAutomationFixture(t)
	projectID := store.NewID("prj")
	issueID := store.NewID("iss")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,'AUTO','Automation','wf_default')`, projectID, fx.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,updated_seq) VALUES($1,$2,$3,'AUTO-1','Due work','st_todo','it_task',0)`, issueID, fx.ws, projectID); err != nil {
		t.Fatal(err)
	}
	actions := []map[string]any{
		{"component": "ACTION", "type": "jira.issue.add-label", "value": map[string]string{"label": "scheduled"}},
		{"component": "ACTION", "type": "jira.issue.assign", "value": map[string]string{"accountId": fx.member}},
		{"component": "ACTION", "type": "jira.issue.transition", "value": map[string]string{"statusId": "st_inprogress"}},
	}
	body, _ := json.Marshal(ruleBody("Concurrent schedule", fx.admin, "ENABLED", "project = AUTO", actions))
	uuid, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, body)
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.service.EnqueueNow(fx.ctx, fx.ws, uuid); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Service: fx.service}
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errs <- runner.DrainOnce(fx.ctx, fx.ws)
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	issue, err := fx.store.IssueByIDOrKey(fx.ctx, fx.ws, issueID)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Status.ID != "st_inprogress" || issue.Assignee == nil || issue.Assignee.ID != fx.member || len(issue.Labels) != 1 || issue.Labels[0] != "scheduled" {
		t.Fatalf("unexpected issue after run: %#v", issue)
	}
	runs, err := fx.service.Runs(fx.ctx, fx.ws, uuid, 25)
	if err != nil || len(runs) != 1 || runs[0].State != "SUCCESS" || runs[0].Attempts != 1 || runs[0].MatchedCount != 1 || runs[0].ChangedCount != 1 {
		t.Fatalf("runs = %#v, err = %v", runs, err)
	}
}

func TestScheduledRunnerDisablesAfterTenFailures(t *testing.T) {
	fx := newAutomationFixture(t)
	actions := []map[string]any{{"component": "ACTION", "type": "jira.issue.unsupported", "value": map[string]string{}}}
	body, _ := json.Marshal(ruleBody("Broken rule", fx.admin, "ENABLED", "", actions))
	uuid, err := fx.service.CreateRule(fx.ctx, fx.ws, fx.admin, body)
	if err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Service: fx.service}
	for attempt := 0; attempt < 10; attempt++ {
		if err := fx.service.EnqueueNow(fx.ctx, fx.ws, uuid); err != nil {
			t.Fatal(err)
		}
		if err := runner.DrainOnce(fx.ctx, fx.ws); err == nil {
			t.Fatal("unsupported action run unexpectedly succeeded")
		}
	}
	rule, err := fx.service.Rule(fx.ctx, fx.ws, uuid)
	if err != nil {
		t.Fatal(err)
	}
	if rule.State != "DISABLED" || rule.ConsecutiveFailures != 10 {
		t.Fatalf("rule state=%s failures=%d", rule.State, rule.ConsecutiveFailures)
	}
}
