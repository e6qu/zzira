package automation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

func webhookRuleBody(name, actor, state string, issuesFromWebhook bool, actions []map[string]any) map[string]any {
	return map[string]any{"rule": map[string]any{
		"actor": map[string]string{"actor": actor, "type": "ACCOUNT_ID"}, "name": name,
		"description": "Incoming webhook", "state": state, "labels": []string{},
		"ruleScopeARIs": []string{}, "components": actions,
		"trigger": map[string]any{"component": "TRIGGER", "type": WebhookTriggerType, "schemaVersion": 1,
			"value": map[string]any{"jql": "", "issuesFromWebhook": issuesFromWebhook}},
	}, "connections": []any{}}
}

// Jira Automation's incoming webhook gives a rule a secret address; a request
// to it runs the rule, for the work the request names or for none at all.
func TestIncomingWebhookRunsARule(t *testing.T) {
	fx := newAutomationFixture(t)
	base := "/gateway/api/automation/public/jira/" + fx.cloudID + "/rest/v1/rule"
	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'HOOK','Hooks','wf_default',$3)`, projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, "webhook target",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	actions := []map[string]any{{"component": "ACTION", "type": "jira.issue.comment",
		"value": map[string]string{"comment": "Deployed {{webhookData.release.version}} for {{issue.key}}"}}}
	created := fx.call(fx.admin, http.MethodPost, base, webhookRuleBody("Deploy hook", fx.admin, "ENABLED", true, actions), http.StatusCreated)
	uuid, _ := created["ruleUuid"].(string)
	if uuid == "" {
		t.Fatalf("created rule = %v", created)
	}
	rule, err := fx.service.Rule(fx.ctx, fx.ws, uuid)
	if err != nil {
		t.Fatal(err)
	}
	if rule.WebhookToken == "" {
		t.Fatal("an incoming webhook rule was given no address")
	}
	if rule.WebhookSecret != "" {
		t.Fatal("a rule was given a secret nobody is shown; the address is the credential")
	}
	if rule.EventTrigger != "" {
		t.Fatalf("event trigger = %q, want empty so the event loop leaves webhook rules alone", rule.EventTrigger)
	}
	token := rule.WebhookToken

	post := func(token, secret, body string, want int) map[string]any {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/pro/hooks/"+token, strings.NewReader(body))
		if secret != "" {
			request.Header.Set("X-Automation-Webhook-Token", secret)
		}
		response := httptest.NewRecorder()
		fx.handler.IncomingWebhook(response, request, token)
		if response.Code != want {
			t.Fatalf("POST /pro/hooks: got %d want %d: %s", response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		if len(response.Body.Bytes()) > 0 {
			_ = json.Unmarshal(response.Body.Bytes(), &out)
		}
		return out
	}

	// The address is the credential: an address that names no rule is refused,
	// and a rule that carries a secret checks it.
	post("not-a-real-token", "", `{}`, http.StatusNotFound)
	if _, err := fx.store.Pool.Exec(fx.ctx, `UPDATE automation_rules SET webhook_secret='shared' WHERE uuid=$1`, uuid); err != nil {
		t.Fatal(err)
	}
	post(token, "", `{}`, http.StatusForbidden)
	post(token, "wrong-secret", `{}`, http.StatusForbidden)
	if _, err := fx.store.Pool.Exec(fx.ctx, `UPDATE automation_rules SET webhook_secret=NULL WHERE uuid=$1`, uuid); err != nil {
		t.Fatal(err)
	}

	body := `{"release":{"version":"4.2.0"},"issues":["` + issue.Key + `"]}`
	if queued := post(token, "", body, http.StatusOK); queued["queued"] != float64(1) {
		t.Fatalf("queued = %v, want 1", queued["queued"])
	}
	runner := &Runner{Service: fx.service}
	if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
		t.Fatal(err)
	}
	runs, err := fx.service.Runs(fx.ctx, fx.ws, uuid, 10)
	if err != nil || len(runs) != 1 || runs[0].State != "SUCCESS" || runs[0].ChangedCount != 1 {
		t.Fatalf("runs = %+v, err=%v", runs, err)
	}
	comments, err := fx.store.CommentsByIssue(fx.ctx, issue.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) == 0 {
		t.Fatal("the webhook rule left no comment")
	}
	text := string(comments[len(comments)-1].Body)
	if !strings.Contains(text, "4.2.0") || !strings.Contains(text, issue.Key) {
		t.Fatalf("comment did not render the webhook body: %s", text)
	}

	// Saving the rule again keeps its address: a rotating URL would silently
	// break whatever already calls it.
	fx.call(fx.admin, http.MethodPut, base+"/"+uuid, webhookRuleBody("Deploy hook", fx.admin, "ENABLED", true, actions), http.StatusOK)
	again, err := fx.service.Rule(fx.ctx, fx.ws, uuid)
	if err != nil {
		t.Fatal(err)
	}
	if again.WebhookToken != token {
		t.Fatal("saving the rule changed the address it is called at")
	}

	// A disabled rule is not run by its address.
	fx.call(fx.admin, http.MethodPut, base+"/"+uuid, webhookRuleBody("Deploy hook", fx.admin, "DISABLED", true, actions), http.StatusOK)
	post(token, "", `{}`, http.StatusConflict)
}

// Jira's webhook can run a rule with no work items at all; an action that
// needs one then fails, rather than the run being skipped.
func TestIncomingWebhookWithNoWorkItems(t *testing.T) {
	fx := newAutomationFixture(t)
	base := "/gateway/api/automation/public/jira/" + fx.cloudID + "/rest/v1/rule"
	actions := []map[string]any{{"component": "ACTION", "type": "jira.issue.add-label", "value": map[string]string{"label": "hooked"}}}
	created := fx.call(fx.admin, http.MethodPost, base, webhookRuleBody("Bodiless hook", fx.admin, "ENABLED", false, actions), http.StatusCreated)
	uuid, _ := created["ruleUuid"].(string)
	rule, err := fx.service.Rule(fx.ctx, fx.ws, uuid)
	if err != nil {
		t.Fatal(err)
	}

	// A request with no body at all is accepted, as Jira accepts one.
	request := httptest.NewRequest(http.MethodPost, "/pro/hooks/"+rule.WebhookToken, strings.NewReader(""))
	response := httptest.NewRecorder()
	fx.handler.IncomingWebhook(response, request, rule.WebhookToken)
	if response.Code != http.StatusOK {
		t.Fatalf("empty body = %d: %s", response.Code, response.Body.String())
	}

	runner := &Runner{Service: fx.service}
	// The run fails rather than panicking: the action needs a work item.
	_ = runner.DrainOnce(fx.ctx, fx.ws)
	runs, err := fx.service.Runs(fx.ctx, fx.ws, uuid, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %+v, err=%v", runs, err)
	}
	if runs[0].State != "FAILED" || !strings.Contains(runs[0].Detail, "needs a work item") {
		t.Fatalf("run = %+v, want a failure naming the missing work item", runs[0])
	}
}
