package automation

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

func webRequestRuleBody(name, actor, address string, extra []map[string]any) map[string]any {
	actions := []map[string]any{{"component": "ACTION", "type": WebRequestActionType,
		"value": map[string]string{"method": "POST", "url": address}}}
	actions = append(actions, extra...)
	return map[string]any{"rule": map[string]any{
		"actor": map[string]string{"actor": actor, "type": "ACCOUNT_ID"}, "name": name,
		"description": "Web request", "state": "ENABLED", "labels": []string{},
		"ruleScopeARIs": []string{}, "components": actions,
		"trigger": map[string]any{"component": "TRIGGER", "type": WebhookTriggerType, "schemaVersion": 1,
			"value": map[string]any{"jql": "", "issuesFromWebhook": true}},
	}, "connections": []any{}}
}

// allowLocalWebRequests lets a test reach its own server, which the guard
// refuses by design, and restores the guard afterwards.
func allowLocalWebRequests(t *testing.T) {
	t.Helper()
	previous := webRequestHostCheck
	webRequestHostCheck = func(*url.URL) error { return nil }
	t.Cleanup(func() { webRequestHostCheck = previous })
}

// Jira Automation sends a web request and later actions read its answer.
func TestSendWebRequestActionCallsOutAndKeepsTheAnswer(t *testing.T) {
	fx := newAutomationFixture(t)
	allowLocalWebRequests(t)

	type received struct {
		method string
		body   string
	}
	got := make(chan received, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- received{method: r.Method, body: string(body)}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	projectID := store.NewID("prj")
	if _, err := fx.store.Pool.Exec(fx.ctx, `INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,'WEBR','Web requests','wf_default',$3)`, projectID, fx.ws, fx.admin); err != nil {
		t.Fatal(err)
	}
	issue, _, err := fx.store.CreateIssue(fx.ctx, fx.admin, projectID, "web request target",
		json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}

	base := "/gateway/api/automation/public/jira/" + fx.cloudID + "/rest/v1/rule"
	comment := []map[string]any{{"component": "ACTION", "type": "jira.issue.comment",
		"value": map[string]string{"comment": "answered {{webResponse.status}} with {{webResponse.body}}"}}}
	created := fx.call(fx.admin, http.MethodPost, base, webRequestRuleBody("Call out", fx.admin, server.URL+"/hook", comment), http.StatusCreated)
	uuid, _ := created["ruleUuid"].(string)
	rule, err := fx.service.Rule(fx.ctx, fx.ws, uuid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.service.TriggerWebhook(fx.ctx, rule.WebhookToken, "", json.RawMessage(`{"issues":["`+issue.Key+`"]}`)); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{Service: fx.service}
	if err := runner.DrainOnce(fx.ctx, fx.ws); err != nil {
		t.Fatal(err)
	}
	runs, err := fx.service.Runs(fx.ctx, fx.ws, uuid, 5)
	if err != nil || len(runs) != 1 || runs[0].State != "SUCCESS" {
		t.Fatalf("runs = %+v, err=%v", runs, err)
	}
	select {
	case call := <-got:
		if call.method != http.MethodPost || !strings.Contains(call.body, issue.Key) {
			t.Fatalf("the site sent %s with %s", call.method, call.body)
		}
	default:
		t.Fatal("the rule sent no web request")
	}
	comments, err := fx.store.CommentsByIssue(fx.ctx, issue.ID)
	if err != nil || len(comments) == 0 {
		t.Fatalf("comments = %d, err=%v", len(comments), err)
	}
	text := string(comments[len(comments)-1].Body)
	if !strings.Contains(text, "answered 200") || !strings.Contains(text, `{\"ok\":true}`) && !strings.Contains(text, `{"ok":true}`) {
		t.Fatalf("the answer did not reach the comment: %s", text)
	}
}

// An answer outside 2xx fails the rule, and a private address is refused.
func TestWebRequestFailuresAreRecorded(t *testing.T) {
	fx := newAutomationFixture(t)
	base := "/gateway/api/automation/public/jira/" + fx.cloudID + "/rest/v1/rule"
	runner := &Runner{Service: fx.service}

	run := func(name, address string) string {
		t.Helper()
		created := fx.call(fx.admin, http.MethodPost, base, webRequestRuleBody(name, fx.admin, address, nil), http.StatusCreated)
		uuid, _ := created["ruleUuid"].(string)
		rule, err := fx.service.Rule(fx.ctx, fx.ws, uuid)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fx.service.TriggerWebhook(fx.ctx, rule.WebhookToken, "", json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		_ = runner.DrainOnce(fx.ctx, fx.ws)
		runs, err := fx.service.Runs(fx.ctx, fx.ws, uuid, 5)
		if err != nil || len(runs) != 1 {
			t.Fatalf("runs = %+v, err=%v", runs, err)
		}
		if runs[0].State != "FAILED" {
			t.Fatalf("run state = %s, want FAILED: %s", runs[0].State, runs[0].Detail)
		}
		return runs[0].Detail
	}

	// A private address is refused before anything is sent.
	if detail := run("Private call", "http://127.0.0.1:9/hook"); !strings.Contains(detail, "private network") {
		t.Fatalf("detail = %q, want the private network refusal", detail)
	}

	allowLocalWebRequests(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	if detail := run("Failing call", server.URL+"/hook"); !strings.Contains(detail, "answered 500") {
		t.Fatalf("detail = %q, want the answered status", detail)
	}
}
