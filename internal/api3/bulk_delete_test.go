package api3

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

func TestBulkDeleteUsesDurableTaskAndAttachmentCleanup(t *testing.T) {
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
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Bulk delete')`, workspaceID)
	for _, value := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Bulk delete user')`, value.id, value.id+"@example.test")
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
	service := &commands.Service{Store: st, Blobs: blobs}
	handler := &Handler{Store: st, Commands: service, Blobs: blobs, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
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
	call(adminID, "POST", "/rest/api/3/project", `{"key":"BDEL","name":"Bulk delete","projectTypeKey":"software","leadAccountId":"`+adminID+`"}`, 201)
	type issueBean struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	issues := make([]issueBean, 0, 2)
	for _, summary := range []string{"Delete with blob", "Access revoked"} {
		created := call(adminID, "POST", "/rest/api/3/issue", `{"fields":{"project":{"key":"BDEL"},"summary":"`+summary+`","issuetype":{"name":"Task"}}}`, 201)
		var issue issueBean
		if err := json.Unmarshal(created.Body.Bytes(), &issue); err != nil || issue.ID == "" || issue.Key == "" {
			t.Fatalf("created issue: %v %s", err, created.Body.String())
		}
		issues = append(issues, issue)
	}
	first, err := st.IssueByIDOrKey(ctx, workspaceID, issues[0].Key)
	if err != nil {
		t.Fatal(err)
	}
	attachment, _, err := service.AddAttachment(ctx, adminID, workspaceID, first.ID, "delete.txt", "text/plain", strings.NewReader("delete me"))
	if err != nil {
		t.Fatal(err)
	}
	blobRef, _, _, err := st.AttachmentBlobRef(ctx, workspaceID, attachment.ID)
	if err != nil {
		t.Fatal(err)
	}
	call(memberID, "POST", "/rest/api/3/bulk/issues/delete", `{"selectedIssueIdsOrKeys":["`+issues[0].Key+`"]}`, 403)
	call(adminID, "POST", "/rest/api/3/bulk/issues/delete", `{"selectedIssueIdsOrKeys":["`+issues[0].Key+`","`+issues[0].Key+`"]}`, 400)
	submitted := call(adminID, "POST", "/rest/api/3/bulk/issues/delete", `{"selectedIssueIdsOrKeys":["`+issues[0].Key+`","`+issues[1].Key+`"],"sendBulkNotification":false}`, 201)
	var submission struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(submitted.Body.Bytes(), &submission); err != nil || submission.TaskID == "" {
		t.Fatalf("bulk delete submission: %v %s", err, submitted.Body.String())
	}
	exec(`INSERT INTO security_schemes(id,name,levels) VALUES($1,'Bulk delete restriction',$2)`, securitySchemeID, `[{"id":"private","name":"Private","members":[]}]`)
	exec(`UPDATE projects SET security_scheme_id=$2 WHERE workspace_id=$1 AND key='BDEL'`, workspaceID, securitySchemeID)
	exec(`UPDATE issues SET security_level_id='private' WHERE workspace_id=$1 AND key=$2`, workspaceID, issues[1].Key)
	exec(`UPDATE memberships SET role='member' WHERE workspace_id=$1 AND user_id=$2`, workspaceID, adminID)
	runner := &store.APITaskRunner{Store: st, BulkIssueExecutor: service}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.IssueByIDOrKey(ctx, workspaceID, issues[0].Key); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("processed issue lookup error=%v, want not found", err)
	}
	if _, err := st.IssueByIDOrKey(ctx, workspaceID, issues[1].Key); err != nil {
		t.Fatalf("inaccessible issue was deleted: %v", err)
	}
	if _, _, err := blobs.Get(ctx, blobRef); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("attachment blob still exists: %v", err)
	}
	pending, err := st.AttachmentBlobDeletionPending(ctx, blobRef)
	if err != nil || pending {
		t.Fatalf("attachment cleanup pending=%v err=%v", pending, err)
	}
	// Replaying a stale claim reconstructs the first success from its atomic
	// delete action and still treats the access-revoked issue as inaccessible.
	exec(`UPDATE api_tasks SET status='RUNNING',progress=5,finished_at=NULL WHERE id=$1`, submission.TaskID)
	replay, err := st.APITaskByID(ctx, workspaceID, submission.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ExecuteBulkIssueTask(ctx, replay); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE memberships SET role='admin' WHERE workspace_id=$1 AND user_id=$2`, workspaceID, adminID)
	progress := call(adminID, "GET", "/rest/api/3/bulk/queue/"+submission.TaskID, "", 200)
	if !strings.Contains(progress.Body.String(), `"status":"COMPLETE"`) || !strings.Contains(progress.Body.String(), `"totalIssueCount":2`) || !strings.Contains(progress.Body.String(), `"invalidOrInaccessibleIssueCount":1`) || !strings.Contains(progress.Body.String(), `"processedAccessibleIssues":[`+issues[0].ID+`]`) {
		t.Fatal(progress.Body.String())
	}
	var deleteActions int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type='issue' AND op='delete' AND payload->>'reason'=$2`, workspaceID, "bulk delete task "+submission.TaskID).Scan(&deleteActions); err != nil || deleteActions != 1 {
		t.Fatalf("delete actions=%d err=%v", deleteActions, err)
	}
}
