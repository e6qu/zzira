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

func TestBulkMoveChangesKeyPreservesAliasAndReplaysOnce(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Bulk move')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Bulk move admin')`, adminID, adminID+"@example.test")
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
	service := &commands.Service{Store: st}
	handler := &Handler{Store: st, Commands: service, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
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
	for _, key := range []string{"BMVA", "BMVB"} {
		call("POST", "/rest/api/3/project", `{"key":"`+key+`","name":"`+key+`","projectTypeKey":"software","leadAccountId":"`+adminID+`"}`, 201)
	}
	created := call("POST", "/rest/api/3/issue", `{"fields":{"project":{"key":"BMVA"},"summary":"Move me","issuetype":{"name":"Task"}}}`, 201)
	var issue struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &issue); err != nil {
		t.Fatal(err)
	}
	call("POST", "/rest/api/3/bulk/issues/move", `{"targetToSourcesMapping":{"BMVB,it_subtask":{"inferClassificationDefaults":true,"inferFieldDefaults":true,"inferStatusDefaults":true,"inferSubtaskTypeDefault":false,"issueIdsOrKeys":["`+issue.Key+`"]}}}`, 400)
	submitted := call("POST", "/rest/api/3/bulk/issues/move", `{"sendBulkNotification":false,"targetToSourcesMapping":{"BMVB,it_task":{"inferClassificationDefaults":true,"inferFieldDefaults":true,"inferStatusDefaults":true,"inferSubtaskTypeDefault":false,"issueIdsOrKeys":["`+issue.Key+`"]}}}`, 201)
	var submission struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(submitted.Body.Bytes(), &submission); err != nil || submission.TaskID == "" {
		t.Fatalf("submission=%+v err=%v", submission, err)
	}
	if err := (&store.APITaskRunner{Store: st, BulkIssueExecutor: service}).DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	moved, err := st.IssueByIDOrKey(ctx, workspaceID, issue.Key)
	if err != nil || moved.ProjectID == "" || !strings.HasPrefix(moved.Key, "BMVB-") || moved.JiraID == 0 {
		t.Fatalf("moved=%+v err=%v", moved, err)
	}
	newKey := moved.Key
	progress := call("GET", "/rest/api/3/bulk/queue/"+submission.TaskID, "", 200)
	if !strings.Contains(progress.Body.String(), `"processedAccessibleIssues":[`+issue.ID+`]`) {
		t.Fatal(progress.Body.String())
	}
	exec(`UPDATE api_tasks SET status='RUNNING',progress=5,finished_at=NULL WHERE id=$1`, submission.TaskID)
	task, err := st.APITaskByID(ctx, workspaceID, submission.TaskID)
	if err != nil || service.ExecuteBulkIssueTask(ctx, task) != nil {
		t.Fatalf("replay task err=%v", err)
	}
	replayed, err := st.IssueByIDOrKey(ctx, workspaceID, issue.Key)
	if err != nil || replayed.Key != newKey {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	var itemCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM bulk_issue_task_items WHERE task_id=$1`, submission.TaskID).Scan(&itemCount); err != nil || itemCount != 1 {
		t.Fatalf("task item count=%d err=%v", itemCount, err)
	}
}
