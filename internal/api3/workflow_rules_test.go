package api3

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestWorkflowRulesPersistAndExecuteAcrossAPIJourney(t *testing.T) {
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
	workspaceID, adminID, reporterID, otherID, projectID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr"), store.NewID("project")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Workflow rules')`, workspaceID)
	for _, userID := range []string{adminID, reporterID, otherID} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, userID, userID+"@example.test", "Rules "+userID)
		role := "member"
		if userID == adminID {
			role = "admin"
		}
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, userID, role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, userID, store.HashToken(userID))
	}
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,'WRK','Rule project','wf_default')`, projectID, workspaceID)
	for fieldID, name := range map[string]string{"customfield_99101": "Start date", "customfield_99102": "Due date"} {
		if _, err := st.CreateCustomField(ctx, fieldID, name, models.CustomFieldDatetime, "Workflow date comparison"); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		exec(`DELETE FROM webhook_deliveries WHERE webhook_id IN (SELECT id FROM webhooks WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM webhooks WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE id=ANY($1)`, []string{"customfield_99101", "customfield_99102"})
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflows WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM organization_audit_events WHERE actor_id=ANY($1)`, []string{adminID, reporterID, otherID})
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, userID := range []string{adminID, reporterID, otherID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, userID)
			exec(`DELETE FROM users WHERE id=$1`, userID)
		}
	})

	memberPermissions, err := authz.JiraPermissions(ctx, st, workspaceID, reporterID)
	if err != nil || !memberPermissions["EDIT_ISSUES"] || memberPermissions["ADMINISTER_PROJECTS"] {
		t.Fatalf("member permissions = %v, %v", memberPermissions, err)
	}
	adminPermissions, err := authz.JiraPermissions(ctx, st, workspaceID, adminID)
	if err != nil || !adminPermissions["ADMINISTER_PROJECTS"] {
		t.Fatalf("admin permissions = %v, %v", adminPermissions, err)
	}

	service := &commands.Service{Store: st}
	handler := &Handler{Store: st, Commands: service, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(userID, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.SetBasicAuth(userID+"@example.test", userID)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		return rec
	}
	createIssue := func(body string, want int) map[string]any {
		t.Helper()
		rec := call(reporterID, "POST", "/rest/api/3/issue", body, want)
		result := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &result)
		return result
	}
	workflowWebhook, err := st.CreateWebhook(ctx, workspaceID, "https://example.invalid/workflow", []string{"jira:issue_created"}, `summary = "does not match"`)
	if err != nil {
		t.Fatal(err)
	}
	firstParent := createIssue(`{"fields":{"project":{"key":"WRK"},"summary":"First hierarchy parent","issuetype":{"id":"it_task"}}}`, 201)
	createIssue(`{"fields":{"project":{"key":"WRK"},"summary":"Missing parent","issuetype":{"id":"it_subtask"}}}`, 400)
	child := createIssue(`{"fields":{"project":{"key":"WRK"},"summary":"REST hierarchy child","issuetype":{"id":"it_subtask"},"parent":{"key":"`+firstParent["key"].(string)+`"}}}`, 201)
	childKey := child["key"].(string)
	childResponse := call(reporterID, "GET", "/rest/api/3/issue/"+childKey, "", 200)
	if !strings.Contains(childResponse.Body.String(), `"parent":{"fields":{"summary":"First hierarchy parent"}`) || !strings.Contains(childResponse.Body.String(), `"subtask":true`) {
		t.Fatalf("child response omits Jira hierarchy fields: %s", childResponse.Body.String())
	}
	secondParent := createIssue(`{"fields":{"project":{"key":"WRK"},"summary":"Second hierarchy parent","issuetype":{"id":"it_task"}}}`, 201)
	call(reporterID, "PUT", "/rest/api/3/issue/"+childKey, `{"fields":{"parent":{"id":"`+secondParent["id"].(string)+`"}}}`, 204)
	reparented := call(reporterID, "GET", "/rest/api/3/issue/"+childKey, "", 200)
	if !strings.Contains(reparented.Body.String(), `"parent":{"fields":{"summary":"Second hierarchy parent"}`) {
		t.Fatalf("updated parent is missing: %s", reparented.Body.String())
	}

	capabilities := call(adminID, "GET", "/rest/api/3/workflows/capabilities?workflowId=wf_default", "", 200)
	for _, ruleKey := range []string{"system:restrict-issue-transition", "system:restrict-from-all-users", "system:check-field-value", "system:previous-status-condition", "system:separation-of-duties", "system:parent-or-child-blocking-condition", "system:validate-field-value", "system:previous-status-validator", "system:check-permission-validator", "system:parent-or-child-blocking-validator", "system:change-assignee", "system:update-field", "system:copy-value-from-other-field", "system:trigger-webhook", "system:transition-screen"} {
		if !strings.Contains(capabilities.Body.String(), `"ruleKey":"`+ruleKey+`"`) {
			t.Fatalf("capabilities omit %s: %s", ruleKey, capabilities.Body.String())
		}
	}

	createBody := `{"scope":{"type":"GLOBAL"},"statuses":[{"id":"st_todo","name":"To Do","statusCategory":"TODO","statusReference":"todo"},{"id":"st_done","name":"Done","statusCategory":"DONE","statusReference":"done"}],"workflows":[{"name":"Executable rule workflow","description":"A governed release path","startPointLayout":{"x":-100,"y":-80},"loopedTransitionContainerLayout":{"x":700,"y":40},"statuses":[{"statusReference":"todo","layout":{"x":40,"y":60},"properties":{"phase":"intake"}},{"statusReference":"done","layout":{"x":520,"y":60},"properties":{}}],"transitions":[{"id":"complete","name":"Complete","type":"DIRECTED","toStatusReference":"done","links":[{"fromStatusReference":"todo"}],"conditions":{"operation":"ALL","conditions":[{"ruleKey":"system:restrict-issue-transition","parameters":{"accountIds":"allow-reporter"}},{"ruleKey":"system:restrict-from-all-users","parameters":{"restrictMode":"users"}},{"ruleKey":"system:check-field-value","parameters":{"fieldId":"summary","fieldValue":"[\"Rule journey\"]","comparator":"=","comparisonType":"STRING"}},{"ruleKey":"system:previous-status-condition","parameters":{"previousStatusIds":"st_inprogress","mostRecentStatusOnly":"true","includeCurrentStatus":"false","not":"false","ignoreLoopTransitions":"true"}},{"ruleKey":"system:separation-of-duties","parameters":{"fromStatusId":"st_todo","toStatusId":"st_inprogress"}},{"ruleKey":"system:parent-or-child-blocking-condition","parameters":{"blocker":"CHILD","statusIds":"st_todo"}}],"conditionGroups":[]},"validators":[{"ruleKey":"system:validate-field-value","parameters":{"ruleType":"fieldRequired","fieldsRequired":"description","errorMessage":"Add completion notes"}},{"ruleKey":"system:previous-status-validator","parameters":{"previousStatusIds":"st_inprogress","mostRecentStatusOnly":"true"}},{"ruleKey":"system:check-permission-validator","parameters":{"permissionKey":"EDIT_ISSUES"}},{"ruleKey":"system:validate-field-value","parameters":{"ruleType":"fieldChanged","fieldKey":"labels","errorMessage":"Update labels during transition"}},{"ruleKey":"system:validate-field-value","parameters":{"ruleType":"fieldMatchesRegularExpression","fieldKey":"labels","regexp":"^verified$","errorMessage":"Add the verified label"}},{"ruleKey":"system:validate-field-value","parameters":{"ruleType":"fieldHasSingleValue","fieldKey":"summary","excludeSubtasks":"false"}},{"ruleKey":"system:validate-field-value","parameters":{"ruleType":"dateFieldComparison","date1FieldKey":"customfield_99101","date2FieldKey":"customfield_99102","includeTime":"true","conditionSelected":"<"}},{"ruleKey":"system:validate-field-value","parameters":{"ruleType":"windowDateComparison","date1FieldKey":"customfield_99102","date2FieldKey":"customfield_99101","numberOfDays":"3"}},{"ruleKey":"system:parent-or-child-blocking-validator","parameters":{"blocker":"PARENT","statusIds":"st_todo"}}],"actions":[{"ruleKey":"system:change-assignee","parameters":{"type":"to-current-user"}},{"ruleKey":"system:update-field","parameters":{"field":"labels","value":"workflow-updated","mode":"append"}},{"ruleKey":"system:update-field","parameters":{"field":"summary","value":" [released]","mode":"append"}},{"ruleKey":"system:copy-value-from-other-field","parameters":{"sourceFieldKey":"summary","targetFieldKey":"description","issueSource":"SAME"}},{"ruleKey":"system:trigger-webhook","parameters":{"webhookId":"` + workflowWebhook.ID + `"}}],"transitionScreen":{"ruleKey":"system:transition-screen","parameters":{"fields":"labels"}}}]}]}`
	created := call(adminID, "POST", "/rest/api/3/workflows/create", createBody, 200)
	var result struct {
		Workflows []struct {
			ID string `json:"id"`
		} `json:"workflows"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil || len(result.Workflows) != 1 {
		t.Fatalf("created workflow: %v %s", err, created.Body.String())
	}
	workflowID := result.Workflows[0].ID
	updateBody := `{"workflows":[{"id":"` + workflowID + `","version":{"id":"` + workflowID + `","versionNumber":1},"statuses":[{"statusReference":"st_todo","layout":{"x":40,"y":60},"properties":{"phase":"intake"}},{"statusReference":"st_done","layout":{"x":560,"y":72},"properties":{}}],"transitions":[{"id":"complete","name":"Complete","type":"DIRECTED","toStatusReference":"st_done","links":[{"fromStatusReference":"st_todo"}],"conditions":{"operation":"ALL","conditions":[{"ruleKey":"system:restrict-issue-transition","parameters":{"accountIds":"allow-reporter"}},{"ruleKey":"system:restrict-from-all-users","parameters":{"restrictMode":"users"}},{"ruleKey":"system:check-field-value","parameters":{"fieldId":"summary","fieldValue":"[\"Rule journey\"]","comparator":"=","comparisonType":"STRING"}},{"ruleKey":"system:previous-status-condition","parameters":{"previousStatusIds":"st_inprogress","mostRecentStatusOnly":"true","includeCurrentStatus":"false","not":"false","ignoreLoopTransitions":"true"}},{"ruleKey":"system:separation-of-duties","parameters":{"fromStatusId":"st_todo","toStatusId":"st_inprogress"}},{"ruleKey":"system:parent-or-child-blocking-condition","parameters":{"blocker":"CHILD","statusIds":"st_todo"}}],"conditionGroups":[]},"validators":[{"ruleKey":"system:validate-field-value","parameters":{"ruleType":"fieldRequired","fieldsRequired":"description","errorMessage":"Add completion notes"}},{"ruleKey":"system:previous-status-validator","parameters":{"previousStatusIds":"st_inprogress","mostRecentStatusOnly":"true"}},{"ruleKey":"system:check-permission-validator","parameters":{"permissionKey":"EDIT_ISSUES"}},{"ruleKey":"system:validate-field-value","parameters":{"ruleType":"fieldChanged","fieldKey":"labels","errorMessage":"Update labels during transition"}},{"ruleKey":"system:validate-field-value","parameters":{"ruleType":"fieldMatchesRegularExpression","fieldKey":"labels","regexp":"^verified$","errorMessage":"Add the verified label"}},{"ruleKey":"system:validate-field-value","parameters":{"ruleType":"fieldHasSingleValue","fieldKey":"summary","excludeSubtasks":"false"}},{"ruleKey":"system:validate-field-value","parameters":{"ruleType":"dateFieldComparison","date1FieldKey":"customfield_99101","date2FieldKey":"customfield_99102","includeTime":"true","conditionSelected":"<"}},{"ruleKey":"system:validate-field-value","parameters":{"ruleType":"windowDateComparison","date1FieldKey":"customfield_99102","date2FieldKey":"customfield_99101","numberOfDays":"3"}},{"ruleKey":"system:parent-or-child-blocking-validator","parameters":{"blocker":"PARENT","statusIds":"st_todo"}}],"actions":[{"ruleKey":"system:change-assignee","parameters":{"type":"to-current-user"}},{"ruleKey":"system:update-field","parameters":{"field":"labels","value":"workflow-updated","mode":"append"}},{"ruleKey":"system:update-field","parameters":{"field":"summary","value":" [released]","mode":"append"}},{"ruleKey":"system:copy-value-from-other-field","parameters":{"sourceFieldKey":"summary","targetFieldKey":"description","issueSource":"SAME"}},{"ruleKey":"system:trigger-webhook","parameters":{"webhookId":"` + workflowWebhook.ID + `"}}],"transitionScreen":{"ruleKey":"system:transition-screen","parameters":{"fields":"labels"}}}]}]}`
	updatedWorkflow := call(adminID, "POST", "/rest/api/3/workflows/update", updateBody, 200)
	if !strings.Contains(updatedWorkflow.Body.String(), `"versionNumber":2`) || !strings.Contains(updatedWorkflow.Body.String(), `"ruleKey":"system:change-assignee"`) {
		t.Fatal(updatedWorkflow.Body.String())
	}
	if err := st.AssignWorkflowToProject(ctx, workspaceID, projectID, workflowID); err != nil {
		t.Fatal(err)
	}
	issue, _, err := service.CreateIssue(ctx, commands.CreateIssueInput{ActorID: reporterID, WorkspaceID: workspaceID, ProjectIDOrKey: "WRK", Summary: "Rule journey", IssueTypeID: "it_task"})
	if err != nil {
		t.Fatal(err)
	}
	inProgress := "st_inprogress"
	issue, _, err = st.UpdateIssue(ctx, otherID, workspaceID, issue.ID, store.IssueUpdate{StatusID: &inProgress, ExpectedUpdatedSeq: &issue.UpdatedSeq})
	if err != nil {
		t.Fatal(err)
	}
	toDo := "st_todo"
	issue, _, err = st.UpdateIssue(ctx, otherID, workspaceID, issue.ID, store.IssueUpdate{StatusID: &toDo, ExpectedUpdatedSeq: &issue.UpdatedSeq, Fields: map[string]json.RawMessage{"customfield_99101": json.RawMessage(`"2026-09-06T09:00:00Z"`), "customfield_99102": json.RawMessage(`"2026-09-07T09:00:00Z"`)}})
	if err != nil {
		t.Fatal(err)
	}
	history, err := st.IssueStatusHistory(ctx, workspaceID, issue.ID)
	if err != nil || len(history) != 2 || history[0] != "st_todo" || history[1] != "st_inprogress" {
		t.Fatalf("status history = %v, %v", history, err)
	}
	transitionHistory, err := st.IssueTransitionHistory(ctx, workspaceID, issue.ID)
	if err != nil || len(transitionHistory) != 2 || transitionHistory[0].ActorID != otherID || transitionHistory[0].FromStatusID != "st_todo" || transitionHistory[0].ToStatusID != "st_inprogress" {
		t.Fatalf("transition history = %+v, %v", transitionHistory, err)
	}
	wrongValueIssue, _, err := service.CreateIssue(ctx, commands.CreateIssueInput{ActorID: reporterID, WorkspaceID: workspaceID, ProjectIDOrKey: "WRK", Summary: "Wrong field value", IssueTypeID: "it_task"})
	if err != nil {
		t.Fatal(err)
	}
	wrongValueTransitions := call(reporterID, "GET", "/rest/api/3/issue/"+wrongValueIssue.Key+"/transitions", "", 200)
	if !strings.Contains(wrongValueTransitions.Body.String(), `"transitions":[]`) {
		t.Fatalf("field condition exposed transition: %s", wrongValueTransitions.Body.String())
	}

	hidden := call(otherID, "GET", "/rest/api/3/issue/"+issue.Key+"/transitions", "", 200)
	if !strings.Contains(hidden.Body.String(), `"transitions":[]`) {
		t.Fatal(hidden.Body.String())
	}
	if _, _, err := service.TransitionIssue(ctx, reporterID, workspaceID, issue.Key, "complete"); err == nil {
		t.Fatal("API-only transition was available through the user-facing command path")
	}
	call(otherID, "POST", "/rest/api/3/issue/"+issue.Key+"/transitions", `{"transition":{"id":"complete"}}`, 400)
	visible := call(reporterID, "GET", "/rest/api/3/issue/"+issue.Key+"/transitions", "", 200)
	if !strings.Contains(visible.Body.String(), `"id":"complete"`) || !strings.Contains(visible.Body.String(), `"isConditional":true`) || !strings.Contains(visible.Body.String(), `"hasScreen":true`) || !strings.Contains(visible.Body.String(), `"labels":{"name":"Labels"`) {
		t.Fatal(visible.Body.String())
	}
	outsideScreen := call(reporterID, "POST", "/rest/api/3/issue/"+issue.Key+"/transitions", `{"transition":{"id":"complete"},"fields":{"summary":"Hidden edit"}}`, 400)
	if !strings.Contains(outsideScreen.Body.String(), "not available") {
		t.Fatal(outsideScreen.Body.String())
	}
	failed := call(reporterID, "POST", "/rest/api/3/issue/"+issue.Key+"/transitions", `{"transition":{"id":"complete"}}`, 400)
	if !strings.Contains(failed.Body.String(), "Add completion notes") {
		t.Fatal(failed.Body.String())
	}
	if _, _, err := service.UpdateIssue(ctx, commands.UpdateIssueInput{ActorID: reporterID, WorkspaceID: workspaceID, IssueIDOrKey: issue.Key, Description: adf.ParagraphDoc("Released after review")}); err != nil {
		t.Fatal(err)
	}
	reversed, _, err := st.UpdateIssue(ctx, reporterID, workspaceID, issue.ID, store.IssueUpdate{Fields: map[string]json.RawMessage{"customfield_99102": json.RawMessage(`"2026-09-05T09:00:00Z"`)}})
	if err != nil {
		t.Fatal(err)
	}
	dateFailed := call(reporterID, "POST", "/rest/api/3/issue/"+issue.Key+"/transitions", `{"transition":{"id":"complete"},"fields":{"labels":["released","verified"]}}`, 400)
	if !strings.Contains(dateFailed.Body.String(), "workflow date fields do not satisfy") {
		t.Fatal(dateFailed.Body.String())
	}
	if _, _, err := st.UpdateIssue(ctx, reporterID, workspaceID, issue.ID, store.IssueUpdate{ExpectedUpdatedSeq: &reversed.UpdatedSeq, Fields: map[string]json.RawMessage{"customfield_99102": json.RawMessage(`"2026-09-07T09:00:00Z"`)}}); err != nil {
		t.Fatal(err)
	}
	sameValue := call(reporterID, "POST", "/rest/api/3/issue/"+issue.Key+"/transitions", `{"transition":{"id":"complete"},"fields":{"labels":[]}}`, 400)
	if !strings.Contains(sameValue.Body.String(), "Update labels during transition") {
		t.Fatal(sameValue.Body.String())
	}
	nonMatching := call(reporterID, "POST", "/rest/api/3/issue/"+issue.Key+"/transitions", `{"transition":{"id":"complete"},"fields":{"labels":["released"]}}`, 400)
	if !strings.Contains(nonMatching.Body.String(), "Add the verified label") {
		t.Fatal(nonMatching.Body.String())
	}
	staleStatus := "st_done"
	if _, _, err := st.UpdateIssue(ctx, reporterID, workspaceID, issue.ID, store.IssueUpdate{StatusID: &staleStatus, ExpectedUpdatedSeq: &issue.UpdatedSeq}); err == nil {
		t.Fatal("a transition based on a stale rule snapshot must fail")
	}
	call(reporterID, "POST", "/rest/api/3/issue/"+issue.Key+"/transitions", `{"transition":{"id":"complete"},"fields":{"labels":["released","verified"]}}`, 204)
	updated, err := st.IssueByIDOrKey(ctx, workspaceID, issue.ID)
	if err != nil || updated.Status.ID != "st_done" || updated.Summary != "Rule journey [released]" || adf.PlainText(updated.Description) != "Rule journey [released]" || updated.Assignee == nil || updated.Assignee.ID != reporterID || len(updated.Labels) != 3 || !slices.Contains(updated.Labels, "workflow-updated") {
		t.Fatalf("updated issue = %+v, %v", updated, err)
	}
	transitionAction, err := st.ActionBySeq(ctx, workspaceID, updated.UpdatedSeq)
	var transitionPayload models.IssueUpdatePayload
	if err != nil || json.Unmarshal(transitionAction.Payload, &transitionPayload) != nil || len(transitionPayload.TriggeredWebhookIDs) != 1 || transitionPayload.TriggeredWebhookIDs[0] != workflowWebhook.ID {
		t.Fatalf("transition webhook payload = %+v, %v", transitionPayload, err)
	}
	changes, err := st.IssueChangelog(ctx, workspaceID, issue.ID)
	if err != nil || len(changes) < 2 {
		t.Fatalf("changelog = %+v, %v", changes, err)
	}
	latest := changes[len(changes)-1]
	fields := map[string]bool{}
	for _, item := range latest.Items {
		fields[item.Field] = true
	}
	if !fields["status"] || !fields["assignee"] || !fields["labels"] {
		t.Fatalf("transition changelog = %+v", latest.Items)
	}

	search := call(adminID, "GET", "/rest/api/3/workflows/search?queryString=Executable&expand=values.transitions", "", 200)
	for _, fragment := range []string{`"description":"A governed release path"`, `"layout":{"x":40,"y":60}`, `"startPointLayout":{"x":-100,"y":-80}`, `"conditions":{"operation":"ALL"`, `"ruleKey":"system:restrict-from-all-users"`, `"ruleKey":"system:check-field-value"`, `"ruleKey":"system:previous-status-condition"`, `"ruleKey":"system:separation-of-duties"`, `"ruleKey":"system:parent-or-child-blocking-condition"`, `"ruleKey":"system:validate-field-value"`, `"ruleKey":"system:previous-status-validator"`, `"ruleKey":"system:check-permission-validator"`, `"ruleKey":"system:parent-or-child-blocking-validator"`, `"ruleKey":"system:change-assignee"`, `"ruleKey":"system:update-field"`, `"ruleKey":"system:copy-value-from-other-field"`, `"ruleKey":"system:trigger-webhook"`} {
		if !strings.Contains(search.Body.String(), fragment) {
			t.Fatalf("search omits %s: %s", fragment, search.Body.String())
		}
	}
	preview := call(adminID, "POST", "/rest/api/3/workflows/preview", `{"projectId":"`+projectID+`","workflowIds":["`+workflowID+`"]}`, 200)
	if !strings.Contains(preview.Body.String(), `"ruleKey":"system:restrict-issue-transition"`) || !strings.Contains(preview.Body.String(), `"layout":{"x":560,"y":72}`) {
		t.Fatal(preview.Body.String())
	}
}
