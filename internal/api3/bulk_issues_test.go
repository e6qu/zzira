package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestBulkWatchOperationsUseDurableTaskQueue(t *testing.T) {
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
	workspaceID, adminID, memberID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	securitySchemeID := store.NewID("sec")
	customFieldIDs := []string{}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Bulk issue operations')`, workspaceID)
	for _, value := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Bulk user')`, value.id, value.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, value.id, value.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, value.id, store.HashToken(value.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM api_tasks WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE id=ANY($1)`, customFieldIDs)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM security_schemes WHERE id=$1`, securitySchemeID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(userID, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(userID+"@example.test", userID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	project := call(adminID, "POST", "/rest/api/3/project", `{"key":"BULK","name":"Bulk work","projectTypeKey":"software","leadAccountId":"`+adminID+`"}`, 201)
	if !strings.Contains(project.Body.String(), `"key":"BULK"`) {
		t.Fatal(project.Body.String())
	}
	issueKeys := make([]string, 0, 2)
	for _, summary := range []string{"First bulk item", "Second bulk item"} {
		created := call(adminID, "POST", "/rest/api/3/issue", `{"fields":{"project":{"key":"BULK"},"summary":"`+summary+`","issuetype":{"name":"Task"}}}`, 201)
		var issue struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(created.Body.Bytes(), &issue); err != nil || issue.Key == "" {
			t.Fatalf("created issue: %v %s", err, created.Body.String())
		}
		issueKeys = append(issueKeys, issue.Key)
	}
	bulkTextFieldID := ""
	var projectID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND key='BULK'`, workspaceID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	for i := range 55 {
		fieldNumber, err := st.NextCustomFieldNumber(ctx)
		if err != nil {
			t.Fatal(err)
		}
		fieldID := fmt.Sprintf("customfield_%d", fieldNumber)
		customFieldIDs = append(customFieldIDs, fieldID)
		if i == 0 {
			bulkTextFieldID = fieldID
		}
		// Create the field in the workspace, as the product does: a
		// workspace-less custom field reaches no workspace's default screen.
		exec(`INSERT INTO custom_fields(id,name,type,description,workspace_id) VALUES($1,$2,'text','Bulk-edit text',$3)`,
			fieldID, fmt.Sprintf("Bulk custom %02d", i), workspaceID)
		exec(`UPDATE custom_field_contexts SET all_projects=FALSE WHERE field_id=$1`, fieldID)
		exec(`INSERT INTO custom_field_context_projects(context_id,project_id)
			SELECT id,$2 FROM custom_field_contexts WHERE field_id=$1`, fieldID, projectID)
	}
	fieldPath := "/rest/api/3/bulk/issues/fields?issueIdsOrKeys=" + strings.Join(issueKeys, "%2C")
	// Every site member holds Jira's global Bulk change permission.
	call(memberID, "GET", fieldPath, "", 200)
	firstFields := call(adminID, "GET", fieldPath, "", 200)
	var firstPage struct {
		Fields        []map[string]any `json:"fields"`
		StartingAfter string           `json:"startingAfter"`
		EndingBefore  string           `json:"endingBefore"`
	}
	if err := json.Unmarshal(firstFields.Body.Bytes(), &firstPage); err != nil || len(firstPage.Fields) != 50 || firstPage.StartingAfter == "" || firstPage.EndingBefore != "" {
		t.Fatalf("first bulk field page: %v %s", err, firstFields.Body.String())
	}
	secondFields := call(adminID, "GET", fieldPath+"&startingAfter="+firstPage.StartingAfter, "", 200)
	var secondPage struct {
		Fields        []map[string]any `json:"fields"`
		StartingAfter string           `json:"startingAfter"`
		EndingBefore  string           `json:"endingBefore"`
	}
	if err := json.Unmarshal(secondFields.Body.Bytes(), &secondPage); err != nil || len(secondPage.Fields) == 0 || len(secondPage.Fields) > 50 || secondPage.EndingBefore == "" {
		t.Fatalf("second bulk field page: %v %s", err, secondFields.Body.String())
	}
	backFields := call(adminID, "GET", fieldPath+"&endingBefore="+secondPage.EndingBefore, "", 200)
	var backPage struct {
		Fields []map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(backFields.Body.Bytes(), &backPage); err != nil || len(backPage.Fields) != 50 || backPage.Fields[0]["id"] != firstPage.Fields[0]["id"] {
		t.Fatalf("backward bulk field page: %v %s", err, backFields.Body.String())
	}
	priorityFields := call(adminID, "GET", fieldPath+"&searchText=priority", "", 200)
	if !strings.Contains(priorityFields.Body.String(), `"id":"priority"`) || !strings.Contains(priorityFields.Body.String(), `"priority":"Medium"`) {
		t.Fatal(priorityFields.Body.String())
	}
	call(adminID, "GET", fieldPath+"&startingAfter=invalid", "", 400)
	call(adminID, "GET", "/rest/api/3/bulk/issues/fields?issueIdsOrKeys=DOES-NOT-EXIST", "", 400)
	payload := `{"selectedIssueIdsOrKeys":["` + strings.Join(issueKeys, `","`) + `"]}`
	call(adminID, "POST", "/rest/api/3/bulk/issues/fields", `{"selectedIssueIdsOrKeys":["`+strings.Join(issueKeys, `","`)+`"],"selectedActions":["summary"],"editedFieldsInput":{"labelsFields":[{"fieldId":"labels","bulkEditMultiSelectFieldOption":"ADD","labels":[{"name":"bulk-edited"}]}]}}`, 400)
	editPayload := fmt.Sprintf(`{"selectedIssueIdsOrKeys":["%s"],"selectedActions":["summary","labels","%s"],"editedFieldsInput":{"singleLineTextFields":[{"fieldId":"summary","text":"Bulk changed"},{"fieldId":"%s","text":"shared value"}],"labelsFields":[{"fieldId":"labels","bulkEditMultiSelectFieldOption":"ADD","labels":[{"name":"bulk-edited"}]}]},"sendBulkNotification":false}`, strings.Join(issueKeys, `","`), bulkTextFieldID, bulkTextFieldID)
	edited := call(adminID, "POST", "/rest/api/3/bulk/issues/fields", editPayload, 201)
	var editSubmission struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(edited.Body.Bytes(), &editSubmission); err != nil || editSubmission.TaskID == "" {
		t.Fatalf("bulk edit submission: %v %s", err, edited.Body.String())
	}
	runner := &store.APITaskRunner{Store: st, BulkIssueExecutor: handler.Commands}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	editProgress := call(adminID, "GET", "/rest/api/3/bulk/queue/"+editSubmission.TaskID, "", 200)
	if !strings.Contains(editProgress.Body.String(), `"status":"COMPLETE"`) || !strings.Contains(editProgress.Body.String(), `"processedAccessibleIssues"`) || !strings.Contains(editProgress.Body.String(), `"totalIssueCount":2`) || strings.Contains(editProgress.Body.String(), `"failedAccessibleIssues"`) {
		t.Fatal(editProgress.Body.String())
	}
	for _, key := range issueKeys {
		updated := call(adminID, "GET", "/rest/api/3/issue/"+key, "", 200)
		if !strings.Contains(updated.Body.String(), `"summary":"Bulk changed"`) || !strings.Contains(updated.Body.String(), `"bulk-edited"`) || !strings.Contains(updated.Body.String(), `"`+bulkTextFieldID+`":"shared value"`) {
			t.Fatal(updated.Body.String())
		}
	}
	var actionsAfterEdit int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type='issue'`, workspaceID).Scan(&actionsAfterEdit); err != nil {
		t.Fatal(err)
	}
	repeatedEdit := call(adminID, "POST", "/rest/api/3/bulk/issues/fields", editPayload, 201)
	if err := json.Unmarshal(repeatedEdit.Body.Bytes(), &editSubmission); err != nil {
		t.Fatal(err)
	}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	var actionsAfterRepeat int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type='issue'`, workspaceID).Scan(&actionsAfterRepeat); err != nil || actionsAfterRepeat != actionsAfterEdit {
		t.Fatalf("repeated bulk edit actions=%d want %d err=%v", actionsAfterRepeat, actionsAfterEdit, err)
	}
	failedEdit := call(adminID, "POST", "/rest/api/3/bulk/issues/fields", `{"selectedIssueIdsOrKeys":["`+strings.Join(issueKeys, `","`)+`"],"selectedActions":["priority"],"editedFieldsInput":{"priority":{"priorityId":"missing-priority"}}}`, 201)
	if err := json.Unmarshal(failedEdit.Body.Bytes(), &editSubmission); err != nil {
		t.Fatal(err)
	}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	failedProgress := call(adminID, "GET", "/rest/api/3/bulk/queue/"+editSubmission.TaskID, "", 200)
	if !strings.Contains(failedProgress.Body.String(), `"failedAccessibleIssues"`) || !strings.Contains(failedProgress.Body.String(), `"invalidOrInaccessibleIssueCount":0`) {
		t.Fatal(failedProgress.Body.String())
	}
	call(adminID, "POST", "/rest/api/3/bulk/issues/watch", `{"selectedIssueIdsOrKeys":["`+issueKeys[0]+`","`+issueKeys[0]+`"]}`, 400)
	submitted := call(adminID, "POST", "/rest/api/3/bulk/issues/watch", payload, 201)
	var submission struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(submitted.Body.Bytes(), &submission); err != nil || submission.TaskID == "" {
		t.Fatalf("submission: %v %s", err, submitted.Body.String())
	}
	queued := call(adminID, "GET", "/rest/api/3/bulk/queue/"+submission.TaskID, "", 200)
	if !strings.Contains(queued.Body.String(), `"status":"ENQUEUED"`) || !strings.Contains(queued.Body.String(), `"submittedBy":{"accountId":"`+adminID+`"}`) {
		t.Fatal(queued.Body.String())
	}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	complete := call(adminID, "GET", "/rest/api/3/bulk/queue/"+submission.TaskID, "", 200)
	if !strings.Contains(complete.Body.String(), `"status":"COMPLETE"`) || !strings.Contains(complete.Body.String(), `"progressPercent":100`) || !strings.Contains(complete.Body.String(), `"totalIssueCount":2`) || !strings.Contains(complete.Body.String(), `"invalidOrInaccessibleIssueCount":0`) {
		t.Fatal(complete.Body.String())
	}
	for _, key := range issueKeys {
		watchers := call(adminID, "GET", "/rest/api/3/issue/"+key+"/watchers", "", 200)
		if !strings.Contains(watchers.Body.String(), `"isWatching":true`) {
			t.Fatal(watchers.Body.String())
		}
	}
	unwatch := call(adminID, "POST", "/rest/api/3/bulk/issues/unwatch", payload, 201)
	if err := json.Unmarshal(unwatch.Body.Bytes(), &submission); err != nil {
		t.Fatal(err)
	}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	for _, key := range issueKeys {
		watchers := call(adminID, "GET", "/rest/api/3/issue/"+key+"/watchers", "", 200)
		if !strings.Contains(watchers.Body.String(), `"isWatching":false`) {
			t.Fatal(watchers.Body.String())
		}
	}
	call(adminID, "GET", "/rest/api/3/bulk/queue/not-a-task", "", 400)
	var watcherActions int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type='watcher'`, workspaceID).Scan(&watcherActions); err != nil || watcherActions != 4 {
		t.Fatalf("watcher actions = %d, %v", watcherActions, err)
	}
	// URL, date picker and estimate families edit through the ordinary issue
	// update, and their values land on every selected work item.
	familyField := func(kind string) string {
		t.Helper()
		number, err := st.NextCustomFieldNumber(ctx)
		if err != nil {
			t.Fatal(err)
		}
		fieldID := fmt.Sprintf("customfield_%d", number)
		customFieldIDs = append(customFieldIDs, fieldID)
		exec(`INSERT INTO custom_fields(id,name,type,description,workspace_id) VALUES($1,$2,$3,'Bulk family',$4)`, fieldID, "Bulk "+kind, kind, workspaceID)
		exec(`UPDATE custom_field_contexts SET all_projects=FALSE WHERE field_id=$1`, fieldID)
		exec(`INSERT INTO custom_field_context_projects(context_id,project_id)
			SELECT id,$2 FROM custom_field_contexts WHERE field_id=$1`, fieldID, projectID)
		return fieldID
	}
	urlField, dateField := familyField("url"), familyField("date")
	familiesPayload := fmt.Sprintf(`{"selectedIssueIdsOrKeys":["%s"],"selectedActions":["%s","%s","timeoriginalestimate"],"editedFieldsInput":{"urlFields":[{"fieldId":"%s","url":"https://example.test/runbook"}],"datePickerFields":[{"fieldId":"%s","date":{"formattedDate":"2026-09-20"}}],"originalEstimateField":{"originalEstimateField":"2h"}},"sendBulkNotification":false}`,
		strings.Join(issueKeys, `","`), urlField, dateField, urlField, dateField)
	families := call(adminID, "POST", "/rest/api/3/bulk/issues/fields", familiesPayload, 201)
	if err := json.Unmarshal(families.Body.Bytes(), &submission); err != nil {
		t.Fatal(err)
	}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if progress := call(adminID, "GET", "/rest/api/3/bulk/queue/"+submission.TaskID, "", 200); !strings.Contains(progress.Body.String(), `"status":"COMPLETE"`) || strings.Contains(progress.Body.String(), `"failedAccessibleIssues"`) {
		t.Fatal(progress.Body.String())
	}
	for _, key := range issueKeys {
		updated := call(adminID, "GET", "/rest/api/3/issue/"+key, "", 200).Body.String()
		if !strings.Contains(updated, `"`+urlField+`":"https://example.test/runbook"`) || !strings.Contains(updated, `"`+dateField+`":"2026-09-20"`) || !strings.Contains(updated, `"timeoriginalestimate":7200`) {
			t.Fatalf("bulk edited families on %s = %s", key, updated)
		}
	}
	revoked := call(adminID, "POST", "/rest/api/3/bulk/issues/watch", payload, 201)
	if err := json.Unmarshal(revoked.Body.Bytes(), &submission); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO security_schemes(id,name,levels) VALUES($1,'Restricted during bulk work',$2)`, securitySchemeID, `[{
		"id":"revoked","name":"Revoked","members":[]
	}]`)
	exec(`UPDATE projects SET security_scheme_id=$2 WHERE workspace_id=$1 AND key='BULK'`, workspaceID, securitySchemeID)
	exec(`UPDATE issues SET security_level_id='revoked' WHERE workspace_id=$1 AND key=$2`, workspaceID, issueKeys[0])
	exec(`UPDATE memberships SET role='member' WHERE workspace_id=$1 AND user_id=$2`, workspaceID, adminID)
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	task, err := st.APITaskByID(ctx, workspaceID, submission.TaskID)
	var revokedResult struct {
		Invalid int `json:"invalidOrInaccessibleIssueCount"`
	}
	decodeErr := json.Unmarshal(task.Result, &revokedResult)
	if err != nil || decodeErr != nil || task.Status != "COMPLETE" || revokedResult.Invalid != 1 {
		t.Fatalf("revoked visibility task: status=%q result=%s err=%v", task.Status, task.Result, err)
	}
	var restrictedWatcher, visibleWatcher bool
	if err := st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM watchers w JOIN issues i ON i.id=w.issue_id WHERE i.workspace_id=$1 AND i.key=$2 AND w.user_id=$3)`, workspaceID, issueKeys[0], adminID).Scan(&restrictedWatcher); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM watchers w JOIN issues i ON i.id=w.issue_id WHERE i.workspace_id=$1 AND i.key=$2 AND w.user_id=$3)`, workspaceID, issueKeys[1], adminID).Scan(&visibleWatcher); err != nil {
		t.Fatal(err)
	}
	if restrictedWatcher || !visibleWatcher {
		t.Fatalf("execution-time visibility: restricted=%v visible=%v", restrictedWatcher, visibleWatcher)
	}
	exec(`UPDATE memberships SET role='admin' WHERE workspace_id=$1 AND user_id=$2`, workspaceID, adminID)
	exec(`UPDATE issues SET security_level_id=NULL WHERE workspace_id=$1`, workspaceID)
	exec(`UPDATE projects SET security_scheme_id=NULL WHERE workspace_id=$1`, workspaceID)
	exec(`DELETE FROM security_schemes WHERE id=$1`, securitySchemeID)
	for range 5 {
		call(adminID, "POST", "/rest/api/3/bulk/issues/watch", payload, 201)
	}
	limit := call(adminID, "POST", "/rest/api/3/bulk/issues/watch", payload, 400)
	if !strings.Contains(limit.Body.String(), "five bulk operations are already queued or running") {
		t.Fatal(limit.Body.String())
	}
}

// Jira's bulk edit names the work type and the status among the fields it
// edits. The type re-homes the work item the way a move within its project
// does, and the status runs the transition that leads there; a status no
// transition reaches fails that work item and says so.
func TestBulkEditChangesWorkTypeAndStatus(t *testing.T) {
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
	workspaceID, adminID := store.NewID("ws"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Bulk type and status')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Bulk editor')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM api_tasks WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, adminID)
		exec(`DELETE FROM users WHERE id=$1`, adminID)
	})
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(adminID+"@example.test", adminID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	call("POST", "/rest/api/3/project", `{"key":"TYPE","name":"Bulk types","projectTypeKey":"software","leadAccountId":"`+adminID+`"}`, 201)
	issueKeys := make([]string, 0, 2)
	for _, summary := range []string{"Type change one", "Type change two"} {
		created := call("POST", "/rest/api/3/issue", `{"fields":{"project":{"key":"TYPE"},"summary":"`+summary+`","issuetype":{"name":"Task"}}}`, 201)
		var issue struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(created.Body.Bytes(), &issue); err != nil || issue.Key == "" {
			t.Fatalf("created issue: %v %s", err, created.Body.String())
		}
		issueKeys = append(issueKeys, issue.Key)
	}
	var siteTypes []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(call("GET", "/rest/api/3/issuetype", "", 200).Body.Bytes(), &siteTypes); err != nil {
		t.Fatal(err)
	}
	bugTypeID := ""
	for _, issueType := range siteTypes {
		if issueType.Name == "Bug" {
			bugTypeID = issueType.ID
		}
	}
	if bugTypeID == "" {
		t.Fatalf("no Bug work type among %+v", siteTypes)
	}
	// A status the work item's workflow can actually reach from where it is.
	var transitionList struct {
		Transitions []struct {
			To struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"to"`
		} `json:"transitions"`
	}
	available := call("GET", "/rest/api/3/issue/"+issueKeys[0]+"/transitions", "", 200)
	if err := json.Unmarshal(available.Body.Bytes(), &transitionList); err != nil || len(transitionList.Transitions) == 0 {
		t.Fatalf("transitions: %v %s", err, available.Body.String())
	}
	targetStatusID := transitionList.Transitions[0].To.ID

	runner := &store.APITaskRunner{Store: st, BulkIssueExecutor: handler.Commands}
	drain := func(body string) string {
		t.Helper()
		submitted := call("POST", "/rest/api/3/bulk/issues/fields", body, 201)
		var submission struct {
			TaskID string `json:"taskId"`
		}
		if err := json.Unmarshal(submitted.Body.Bytes(), &submission); err != nil || submission.TaskID == "" {
			t.Fatalf("submission: %v %s", err, submitted.Body.String())
		}
		if err := runner.DrainOnce(ctx, workspaceID); err != nil {
			t.Fatal(err)
		}
		return call("GET", "/rest/api/3/bulk/queue/"+submission.TaskID, "", 200).Body.String()
	}

	progress := drain(fmt.Sprintf(`{"selectedIssueIdsOrKeys":["%s"],"selectedActions":["issuetype","status"],"editedFieldsInput":{"issueType":{"issueTypeId":"%s"},"status":{"statusId":"%s"}},"sendBulkNotification":false}`,
		strings.Join(issueKeys, `","`), bugTypeID, targetStatusID))
	if !strings.Contains(progress, `"status":"COMPLETE"`) || strings.Contains(progress, `"failedAccessibleIssues"`) {
		t.Fatal(progress)
	}
	for _, key := range issueKeys {
		updated := call("GET", "/rest/api/3/issue/"+key, "", 200).Body.String()
		if !strings.Contains(updated, `"name":"Bug"`) || !strings.Contains(updated, `"status":{"id":"`+targetStatusID+`"`) {
			t.Fatalf("%s did not change type and status: %s", key, updated)
		}
	}

	// A status of the project that no transition reaches is the work item's
	// failure, not the task's: the task completes and names what could not be
	// done. A status that is not the project's at all is refused outright.
	var projectID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND key='TYPE'`, workspaceID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	var strandedStatuses []struct {
		ID string `json:"id"`
	}
	stranded := call("POST", "/rest/api/3/statuses", `{"scope":{"type":"PROJECT","project":{"id":"`+projectID+`"}},"statuses":[{"name":"Off the workflow","statusCategory":"IN_PROGRESS"}]}`, 200)
	if err := json.Unmarshal(stranded.Body.Bytes(), &strandedStatuses); err != nil || len(strandedStatuses) != 1 {
		t.Fatalf("stranded status: %v %s", err, stranded.Body.String())
	}
	failed := drain(fmt.Sprintf(`{"selectedIssueIdsOrKeys":["%s"],"selectedActions":["status"],"editedFieldsInput":{"status":{"statusId":"%s"}},"sendBulkNotification":false}`,
		strings.Join(issueKeys, `","`), strandedStatuses[0].ID))
	if !strings.Contains(failed, `"failedAccessibleIssues"`) || !strings.Contains(failed, "no transition from") {
		t.Fatal(failed)
	}
	call("POST", "/rest/api/3/bulk/issues/fields", fmt.Sprintf(`{"selectedIssueIdsOrKeys":["%s"],"selectedActions":["status"],"editedFieldsInput":{"status":{"statusId":"sts_not_in_this_site"}}}`,
		strings.Join(issueKeys, `","`)), 400)

	// The two are still fields of the edit: naming one and sending the other
	// is refused before anything is queued.
	call("POST", "/rest/api/3/bulk/issues/fields", `{"selectedIssueIdsOrKeys":["`+issueKeys[0]+`"],"selectedActions":["status"],"editedFieldsInput":{"issueType":{"issueTypeId":"`+bugTypeID+`"}}}`, 400)
}
