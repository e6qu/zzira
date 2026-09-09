package api3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestIssueVoteLifecycle(t *testing.T) {
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
	if err = store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, userID, projectID := store.NewID("ws"), store.NewID("usr"), store.NewID("prj")
	projectKey := "VT" + projectID[len(projectID)-5:]
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Vote test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Vote user')`, userID, userID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, userID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, userID, store.HashToken(userID))
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,$3,'Vote project','wf_default',$4)`, projectID, workspaceID, projectKey, userID)
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, userID)
		exec(`DELETE FROM users WHERE id=$1`, userID)
	})
	service := &commands.Service{Store: st}
	issue, _, err := service.CreateIssue(ctx, commands.CreateIssueInput{
		ActorID: userID, WorkspaceID: workspaceID, ProjectIDOrKey: projectID, Summary: "Vote for this", IssueTypeID: "it_task",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{Store: st, Commands: service, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(method string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "/rest/api/3/issue/"+issue.Key+"/votes", nil)
		request.SetBasicAuth(userID+"@example.test", userID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s votes: %d want %d: %s", method, response.Code, want, response.Body.String())
		}
		return response
	}
	call(http.MethodPost, http.StatusNoContent)
	call(http.MethodPost, http.StatusNoContent)
	searchRequest := httptest.NewRequest(http.MethodGet, "/rest/api/3/search?jql="+url.QueryEscape("issue IN votedIssues()"), nil)
	searchRequest.SetBasicAuth(userID+"@example.test", userID)
	searchResponse := httptest.NewRecorder()
	handler.ServeHTTP(searchResponse, searchRequest)
	if searchResponse.Code != http.StatusOK || !json.Valid(searchResponse.Body.Bytes()) {
		t.Fatalf("vote JQL search: %d %s", searchResponse.Code, searchResponse.Body.String())
	}
	var searchPage struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(searchResponse.Body.Bytes(), &searchPage); err != nil || searchPage.Total != 1 {
		t.Fatalf("vote JQL search = %+v, %v", searchPage, err)
	}
	response := call(http.MethodGet, http.StatusOK)
	var votes struct {
		Votes    int              `json:"votes"`
		HasVoted bool             `json:"hasVoted"`
		Voters   []map[string]any `json:"voters"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &votes); err != nil {
		t.Fatal(err)
	}
	if votes.Votes != 1 || !votes.HasVoted || len(votes.Voters) != 1 || votes.Voters[0]["accountId"] != userID {
		t.Fatalf("votes = %+v", votes)
	}
	call(http.MethodDelete, http.StatusNoContent)
	response = call(http.MethodGet, http.StatusOK)
	if err := json.Unmarshal(response.Body.Bytes(), &votes); err != nil {
		t.Fatal(err)
	}
	if votes.Votes != 0 || votes.HasVoted || len(votes.Voters) != 0 {
		t.Fatalf("votes after delete = %+v", votes)
	}
}
