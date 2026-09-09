package api3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestLoginDateJQLContract(t *testing.T) {
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
	projectKey := "LG" + strings.ToUpper(projectID[len(projectID)-5:])
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Login JQL')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Login user')`, userID, userID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, userID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, userID, store.HashToken(userID))
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,$3,'Login project','wf_default',$4)`, projectID, workspaceID, projectKey, userID)
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE id=$1`, projectID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM sessions WHERE user_id=$1`, userID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, userID)
		exec(`DELETE FROM users WHERE id=$1`, userID)
	})

	passwordSession := store.HashToken(store.NewID("login"))
	providerSession := store.HashToken(store.NewID("login"))
	registeredProviderSession := store.HashToken(store.NewID("login"))
	if err = st.CreateSession(ctx, passwordSession, userID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err = st.CreateOIDCSession(ctx, providerSession, userID, "id-token", "https://accounts.example.test", "subject", "sid", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err = st.CreateIdentityProviderSession(ctx, registeredProviderSession, userID, "id-token", "https://login.example.test", "subject", "sid", "google", time.Hour); err != nil {
		t.Fatal(err)
	}
	var recordedCurrent time.Time
	var recordedPrevious *time.Time
	if err = st.Pool.QueryRow(ctx, `SELECT current_started_at,previous_started_at FROM user_login_state WHERE user_id=$1`, userID).Scan(&recordedCurrent, &recordedPrevious); err != nil {
		t.Fatal(err)
	}
	if recordedPrevious == nil || recordedCurrent.Before(*recordedPrevious) {
		t.Fatalf("recorded login boundaries: current=%s previous=%v", recordedCurrent, recordedPrevious)
	}
	current := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	previous := current.Add(-time.Hour)
	exec(`UPDATE user_login_state SET current_started_at=$2,previous_started_at=$3 WHERE user_id=$1`, userID, current, previous)
	if err = st.DeleteSession(ctx, registeredProviderSession); err != nil {
		t.Fatal(err)
	}

	service := &commands.Service{Store: st}
	older, _, err := service.CreateIssue(ctx, commands.CreateIssueInput{ActorID: userID, WorkspaceID: workspaceID, ProjectIDOrKey: projectID, Summary: "Previous login", IssueTypeID: "it_task"})
	if err != nil {
		t.Fatal(err)
	}
	recent, _, err := service.CreateIssue(ctx, commands.CreateIssueInput{ActorID: userID, WorkspaceID: workspaceID, ProjectIDOrKey: projectID, Summary: "Current login", IssueTypeID: "it_task"})
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE issues SET created_at=$2,updated_at=$2 WHERE id=$1`, older.ID, previous.Add(30*time.Minute))
	exec(`UPDATE issues SET created_at=$2,updated_at=$2 WHERE id=$1`, recent.ID, current.Add(30*time.Minute))
	exec(`UPDATE actions SET created_at=$2,payload='{"diff":{"status":{"from":"To Do","to":"In Progress"}}}'::jsonb WHERE workspace_id=$1 AND entity_id=$3`, workspaceID, current.Add(15*time.Minute), recent.ID)

	handler := &Handler{Store: st, Commands: service, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	search := func(jql string, wantStatus, wantTotal int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/rest/api/3/search/jql?jql="+url.QueryEscape(jql), nil)
		request.SetBasicAuth(userID+"@example.test", userID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != wantStatus {
			t.Fatalf("%s: got %d want %d: %s", jql, response.Code, wantStatus, response.Body.String())
		}
		if wantStatus != http.StatusOK {
			return
		}
		var page struct {
			Issues []json.RawMessage `json:"issues"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Issues) != wantTotal {
			t.Fatalf("%s: page=%+v err=%v body=%s", jql, page, err, response.Body.String())
		}
	}
	search(`created > currentLogin()`, http.StatusOK, 1)
	search(`created > lastLogin()`, http.StatusOK, 2)
	search(`created > lastLogin() AND created < currentLogin()`, http.StatusOK, 1)
	search(`status CHANGED AFTER currentLogin()`, http.StatusOK, 1)
	search(`created IN (currentLogin())`, http.StatusBadRequest, 0)
}
