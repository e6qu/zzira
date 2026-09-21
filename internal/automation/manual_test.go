package automation

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestManualRulesAndTemplatesGateway covers the Automation gateway's manually
// triggered rules and its template catalog as site members and administrators
// use them.
func TestManualRulesAndTemplatesGateway(t *testing.T) {
	fx := newAutomationFixture(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := fx.store.Pool.Exec(fx.ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var projectID string
	if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT nextval('jira_project_id')::text`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	projectKey := fmt.Sprintf("M%06d", time.Now().UnixNano()%1000000)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id,project_type_key) VALUES($1,$2,$3,'Manual rules','wf_default',$4,'software')`, projectID, fx.ws, projectKey, fx.admin)
	var issueJiraID int64
	if err := fx.store.Pool.QueryRow(fx.ctx, `INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,reporter_id,labels,updated_seq)
		VALUES('iss_'||md5(random()::text),$1,$2,$3,'Run me','st_todo','it_task',$4,'{}',0) RETURNING jira_id`, fx.ws, projectID, projectKey+"-1", fx.admin).Scan(&issueJiraID); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE projects SET issue_seq=1 WHERE id=$1`, projectID)
	issue := fmt.Sprintf("ari:cloud:jira:%s:issue/%d", fx.cloudID, issueJiraID)
	base := "/gateway/api/automation/public/jira/" + fx.cloudID + "/rest/v1"

	manualRule := func(name string, scope []string) string {
		t.Helper()
		body := map[string]any{"rule": map[string]any{
			"actor": map[string]string{"actor": fx.admin, "type": "ACCOUNT_ID"}, "name": name, "state": "ENABLED",
			"ruleScopeARIs": scope,
			"trigger": map[string]any{"component": "TRIGGER", "type": ManualTriggerType, "schemaVersion": 1, "value": map[string]any{
				"inputPrompts": []map[string]any{{"displayName": "Reason", "inputType": "TEXT", "required": true, "variableName": "reason"}},
			}},
			"components": []map[string]any{{"component": "ACTION", "type": "jira.issue.add-label", "value": map[string]string{"label": "manual"}}},
		}, "connections": []any{}}
		created := fx.call(fx.admin, http.MethodPost, base+"/rule", body, http.StatusCreated)
		return created["ruleUuid"].(string)
	}
	projectRule := manualRule("Label from the issue", []string{fmt.Sprintf("ari:cloud:jira:%s:project/%s", fx.cloudID, projectID)})
	elsewhere := manualRule("Only elsewhere", []string{fmt.Sprintf("ari:cloud:jira:%s:project/999999", fx.cloudID)})

	// Search manual rules for an issue.
	fx.call("", http.MethodPost, base+"/rule/manual/search", map[string]any{"objects": []string{issue}}, http.StatusForbidden)
	fx.call(fx.member, http.MethodPost, base+"/rule/manual/search", map[string]any{}, http.StatusBadRequest)
	fx.call(fx.member, http.MethodPost, base+"/rule/manual/search", map[string]any{"objects": []string{issue}, "cursor": "x"}, http.StatusBadRequest)
	fx.call(fx.member, http.MethodPost, base+"/rule/manual/search", map[string]any{"objects": []string{"not-an-ari"}}, http.StatusBadRequest)
	found := fx.call(fx.member, http.MethodPost, base+"/rule/manual/search", map[string]any{"objects": []string{issue}, "limit": 10}, http.StatusOK)
	data := found["data"].([]any)
	if len(data) != 1 || data[0].(map[string]any)["id"] != projectRule || data[0].(map[string]any)["userInputs"].([]any)[0].(map[string]any)["variableName"] != "reason" {
		t.Fatal(found)
	}
	self := found["links"].(map[string]any)["self"].(string)
	values, err := url.ParseQuery(self)
	if err != nil {
		t.Fatal(err)
	}
	if again := fx.call(fx.member, http.MethodGet, base+"/rule/manual/search?cursor="+url.QueryEscape(values.Get("cursor")), nil, http.StatusOK); len(again["data"].([]any)) != 1 {
		t.Fatal(again)
	}
	fx.call(fx.member, http.MethodGet, base+"/rule/manual/search?objects="+url.QueryEscape(issue), nil, http.StatusBadRequest)

	// Invoke it.
	invoke := base + "/rule/manual/" + projectRule + "/invocation"
	fx.call(fx.member, http.MethodPost, invoke, map[string]any{"objects": []string{issue}}, http.StatusBadRequest)
	missingIssue := fmt.Sprintf("ari:cloud:jira:%s:issue/999999999", fx.cloudID)
	results := fx.call(fx.member, http.MethodPost, invoke, map[string]any{
		"objects": []string{issue, missingIssue}, "userInputs": map[string]any{"reason": map[string]any{"inputType": "TEXT", "value": "because"}},
	}, http.StatusOK)
	if results[issue] != "SUCCESS" || results[missingIssue] != "INVALID_TARGET_OBJECT" {
		t.Fatal(results)
	}
	var labels []string
	if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT labels FROM issues WHERE jira_id=$1`, issueJiraID).Scan(&labels); err != nil || strings.Join(labels, ",") != "manual" {
		t.Fatalf("labels=%v err=%v", labels, err)
	}
	var runs int
	if err := fx.store.Pool.QueryRow(fx.ctx, `SELECT count(*) FROM automation_runs WHERE rule_uuid=$1 AND state='SUCCESS'`, projectRule).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("runs=%d err=%v", runs, err)
	}
	scoped := fx.call(fx.member, http.MethodPost, base+"/rule/manual/"+elsewhere+"/invocation", map[string]any{
		"objects": []string{issue}, "userInputs": map[string]any{"reason": map[string]any{"inputType": "TEXT", "value": "x"}},
	}, http.StatusOK)
	if scoped[issue] != "INVALID_TARGET_SCOPE" {
		t.Fatal(scoped)
	}
	fx.call(fx.member, http.MethodPost, base+"/rule/manual/0190a000-0000-7000-8000-000000000000/invocation", map[string]any{"objects": []string{issue}}, http.StatusNotFound)

	// Templates.
	if all := fx.call(fx.member, http.MethodGet, base+"/template/search", nil, http.StatusOK); len(all["data"].([]any)) != len(ruleTemplates) {
		t.Fatal(all)
	}
	if manual := fx.call(fx.member, http.MethodGet, base+"/template/search?categories=manual", nil, http.StatusOK); len(manual["data"].([]any)) != 2 {
		t.Fatal(manual)
	}
	if scheduled := fx.call(fx.member, http.MethodPost, base+"/template/search", map[string]any{"categories": []string{"scheduled"}, "limit": 1}, http.StatusOK); len(scheduled["data"].([]any)) != 1 || scheduled["links"].(map[string]any)["next"] == nil {
		t.Fatal(scheduled)
	}
	fx.call(fx.member, http.MethodGet, base+"/template/search?categories=manual&cursor=abc", nil, http.StatusBadRequest)
	if template := fx.call(fx.member, http.MethodGet, base+"/template/manual-transition", nil, http.StatusOK); template["id"] != "manual-transition" || len(template["categories"].([]any)) == 0 {
		t.Fatal(template)
	}
	fx.call(fx.member, http.MethodGet, base+"/template/nope", nil, http.StatusNotFound)

	site := "ari:cloud:jira::site/" + fx.cloudID
	create := base + "/template/create"
	fx.call(fx.member, http.MethodPost, create, map[string]any{"templateId": "manual-assign-to-me", "ruleHome": site}, http.StatusForbidden)
	fx.call(fx.admin, http.MethodPost, create, map[string]any{"templateId": "scheduled-assign-unassigned", "ruleHome": site}, http.StatusBadRequest)
	fx.call(fx.admin, http.MethodPost, create, map[string]any{"templateId": "manual-assign-to-me", "ruleHome": "ari:cloud:jira::site/elsewhere"}, http.StatusBadRequest)
	fx.call(fx.admin, http.MethodPost, create, map[string]any{"templateId": "manual-assign-to-me", "ruleHome": site, "parameters": map[string]any{"extra": map[string]any{"type": "TEXT", "value": "x"}}}, http.StatusBadRequest)
	assigned := fx.call(fx.admin, http.MethodPost, create, map[string]any{
		"templateId": "scheduled-assign-unassigned", "ruleHome": site,
		"parameters": map[string]any{"assigneeAccountId": map[string]any{"type": "TEXT", "value": fx.admin}},
	}, http.StatusOK)
	rule := fx.call(fx.admin, http.MethodGet, base+"/rule/"+assigned["ruleUuid"].(string), nil, http.StatusOK)["rule"].(map[string]any)
	if rule["name"] != "Assign unassigned work" || rule["state"] != "ENABLED" {
		t.Fatal(rule)
	}
	projectHome := fmt.Sprintf("ari:cloud:jira:%s:project/%s", fx.cloudID, projectID)
	disabled := fx.call(fx.admin, http.MethodPost, create, map[string]any{"templateId": "manual-assign-to-me", "ruleHome": projectHome, "state": "DISABLED"}, http.StatusOK)
	scopedRule := fx.call(fx.admin, http.MethodGet, base+"/rule/"+disabled["ruleUuid"].(string), nil, http.StatusOK)["rule"].(map[string]any)
	if scopedRule["state"] != "DISABLED" || fmt.Sprint(scopedRule["ruleScopeARIs"]) != "["+projectHome+"]" {
		t.Fatal(scopedRule)
	}
}
