package api3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestConcurrentUpdateRefusesAStaleEdit pins the conflict Jira publishes on
// editing and transitioning a work item. An edit is composed from the work
// item as the caller read it; if somebody else changes the work item in
// between, applying the edit would overwrite a change nobody involved has
// seen, so it is refused with 409 rather than silently winning.
func TestConcurrentUpdateRefusesAStaleEdit(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Concurrency')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Concurrent editor')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
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
	send := func(method, path, body string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(adminID+"@example.test", adminID)
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code, response.Body.String()
	}
	created := func(method, path, body string, want int) map[string]any {
		t.Helper()
		status, answer := send(method, path, body)
		if status != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, status, want, answer)
		}
		out := map[string]any{}
		if answer != "" && answer[0] == '{' {
			if err := json.Unmarshal([]byte(answer), &out); err != nil {
				t.Fatalf("%s %s: decode: %v", method, path, err)
			}
		}
		return out
	}

	created(http.MethodPost, "/rest/api/3/project",
		`{"key":"CNC","name":"Concurrent work","projectTypeKey":"software","leadAccountId":"`+adminID+`"}`, http.StatusCreated)
	issue := created(http.MethodPost, "/rest/api/3/issue",
		`{"fields":{"project":{"key":"CNC"},"summary":"Original summary","issuetype":{"name":"Task"}}}`, http.StatusCreated)
	issueKey, _ := issue["key"].(string)

	// An ordinary edit still goes through: the precondition only refuses a
	// work item that moved.
	if status, answer := send(http.MethodPut, "/rest/api/3/issue/"+issueKey, `{"fields":{"summary":"Edited once"}}`); status != http.StatusNoContent {
		t.Fatalf("a lone edit answered %d: %s", status, answer)
	}

	// Now stage the collision the contract describes. The edit is computed
	// from the work item as it is read here, and the work item moves before
	// the write lands; the store refuses the write it was handed.
	current, err := st.IssueByIDOrKey(ctx, workspaceID, issueKey)
	if err != nil {
		t.Fatal(err)
	}
	stale := current.UpdatedSeq
	if status, answer := send(http.MethodPut, "/rest/api/3/issue/"+issueKey, `{"fields":{"summary":"Somebody else was here"}}`); status != http.StatusNoContent {
		t.Fatalf("the other edit answered %d: %s", status, answer)
	}
	summary := "Edited from a stale read"
	_, _, err = service.UpdateIssue(ctx, commands.UpdateIssueInput{
		ActorID: adminID, WorkspaceID: workspaceID, IssueIDOrKey: issueKey,
		Summary: &summary, ExpectedUpdatedSeq: &stale,
	})
	if !errors.Is(err, store.ErrIssueConcurrentUpdate) {
		t.Fatalf("an edit from a stale read answered %v, want a concurrent update refusal", err)
	}
	// The other edit stands: the refused edit changed nothing.
	after, err := st.IssueByIDOrKey(ctx, workspaceID, issueKey)
	if err != nil {
		t.Fatal(err)
	}
	if after.Summary != "Somebody else was here" {
		t.Errorf("the work item reads %q, want the edit that was not refused", after.Summary)
	}

	// The refusal is a conflict over the API, on both operations that publish
	// one, because both report it through the same mapping.
	for _, wrapped := range []error{
		store.ErrIssueConcurrentUpdate,
		fmt.Errorf("updating %s: %w", issueKey, store.ErrIssueConcurrentUpdate),
	} {
		reported := httptest.NewRecorder()
		if !issueWriteConflict(reported, wrapped) {
			t.Errorf("%v was not reported as a conflict", wrapped)
			continue
		}
		if reported.Code != http.StatusConflict {
			t.Errorf("%v answered %d, want 409", wrapped, reported.Code)
		}
		var body struct {
			ErrorMessages []string `json:"errorMessages"`
		}
		if err := json.Unmarshal(reported.Body.Bytes(), &body); err != nil || len(body.ErrorMessages) != 1 {
			t.Errorf("the conflict answered %s", reported.Body.String())
		}
	}
	// Anything else keeps its own answer.
	other := httptest.NewRecorder()
	if issueWriteConflict(other, fmt.Errorf("summary is required")) {
		t.Error("an unrelated failure was reported as a conflict")
	}
}
