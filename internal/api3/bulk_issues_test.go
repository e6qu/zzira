package api3

import (
	"context"
	"encoding/json"
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
	payload := `{"selectedIssueIdsOrKeys":["` + strings.Join(issueKeys, `","`) + `"]}`
	call(memberID, "POST", "/rest/api/3/bulk/issues/watch", payload, 403)
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
	runner := &store.APITaskRunner{Store: st}
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
