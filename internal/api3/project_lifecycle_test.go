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
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

func TestProjectLifecycleAndRecentProjects(t *testing.T) {
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
	workspaceID, adminID, memberID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	suffix := time.Now().UnixNano() % 100000000
	projectKey := fmt.Sprintf("L%08d", suffix)
	deleteKey := fmt.Sprintf("D%08d", suffix)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Project lifecycle test')`, workspaceID)
	for _, identity := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Lifecycle User')`, identity.id, identity.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if user != "" {
			r.SetBasicAuth(user+"@example.test", user)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	createProject := func(key string) string {
		t.Helper()
		call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+key+`","name":"Lifecycle `+key+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)
		var projectID string
		if queryErr := st.Pool.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND key=$2`, workspaceID, key).Scan(&projectID); queryErr != nil {
			t.Fatal(queryErr)
		}
		return projectID
	}

	projectID := createProject(projectKey)
	call(adminID, http.MethodPut, "/rest/api/3/project/"+projectKey+"/properties/app.lifecycle", `{"retention":"60d"}`, http.StatusCreated)
	call(memberID, http.MethodGet, "/rest/api/3/project/"+projectKey, "", http.StatusOK)
	recent := call(memberID, http.MethodGet, "/rest/api/3/project/recent?expand=projectKeys,permissions,insight&properties=app.lifecycle", "", http.StatusOK)
	if !strings.Contains(recent.Body.String(), `"projectKeys":["`+projectKey+`"]`) || !strings.Contains(recent.Body.String(), `"app.lifecycle":{"retention":"60d"}`) || !strings.Contains(recent.Body.String(), `"ADMINISTER_PROJECTS":{"havePermission":false`) {
		t.Fatal(recent.Body.String())
	}
	call(memberID, http.MethodPost, "/rest/api/3/project/"+projectKey+"/archive", "", http.StatusForbidden)
	call(adminID, http.MethodPost, "/rest/api/3/project/"+projectKey+"/archive", "", http.StatusNoContent)
	var projectAction string
	if err = st.Pool.QueryRow(ctx, `SELECT op FROM actions WHERE workspace_id=$1 AND entity_type='project' AND entity_id=$2 ORDER BY seq DESC LIMIT 1`, workspaceID, projectID).Scan(&projectAction); err != nil || projectAction != "delete" {
		t.Fatalf("archive project action=%q err=%v", projectAction, err)
	}
	call(memberID, http.MethodGet, "/rest/api/3/project/"+projectKey, "", http.StatusNotFound)
	search := call(memberID, http.MethodGet, "/rest/api/3/project/search", "", http.StatusOK)
	if strings.Contains(search.Body.String(), `"key":"`+projectKey+`"`) {
		t.Fatal(search.Body.String())
	}
	call(adminID, http.MethodDelete, "/rest/api/3/project/"+projectKey, "", http.StatusBadRequest)
	restored := call(adminID, http.MethodPost, "/rest/api/3/project/"+projectKey+"/restore", "", http.StatusOK)
	if !strings.Contains(restored.Body.String(), `"key":"`+projectKey+`"`) {
		t.Fatal(restored.Body.String())
	}
	if err = st.Pool.QueryRow(ctx, `SELECT op FROM actions WHERE workspace_id=$1 AND entity_type='project' AND entity_id=$2 ORDER BY seq DESC LIMIT 1`, workspaceID, projectID).Scan(&projectAction); err != nil || projectAction != "upsert" {
		t.Fatalf("restore project action=%q err=%v", projectAction, err)
	}

	createdIssue := call(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Retained lifecycle work","issuetype":{"name":"Task"}}}`, http.StatusCreated)
	var issueBean map[string]any
	if err = json.Unmarshal(createdIssue.Body.Bytes(), &issueBean); err != nil {
		t.Fatal(err)
	}
	var issueID string
	if err = st.Pool.QueryRow(ctx, `SELECT id FROM issues WHERE workspace_id=$1 AND key=$2`, workspaceID, issueBean["key"].(string)).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	blobRef := store.NewID("blob")
	exec(`INSERT INTO attachments(id,issue_id,workspace_id,filename,mime_type,size,blob_ref,author_id) VALUES($1,$2,$3,'evidence.txt','text/plain',8,$4,$5)`, store.NewID("att"), issueID, workspaceID, blobRef, adminID)

	call(adminID, http.MethodDelete, "/rest/api/3/project/"+projectKey, "", http.StatusNoContent)
	trashed, err := st.ProjectByIDOrKeyAnyState(ctx, workspaceID, projectID)
	if err != nil || trashed.LifecycleState != store.ProjectLifecycleTrashed || trashed.TrashedAt == "" {
		t.Fatalf("trashed project = %#v, err=%v", trashed, err)
	}
	call(adminID, http.MethodPost, "/rest/api/3/project/"+projectKey+"/restore", "", http.StatusOK)
	var attachmentRestoreAction string
	if err = st.Pool.QueryRow(ctx, `SELECT op FROM actions WHERE workspace_id=$1 AND entity_type='attachment' AND entity_id IN (SELECT id FROM attachments WHERE blob_ref=$2) ORDER BY seq DESC LIMIT 1`, workspaceID, blobRef).Scan(&attachmentRestoreAction); err != nil || attachmentRestoreAction != "upsert" {
		t.Fatalf("attachment restore action=%q err=%v", attachmentRestoreAction, err)
	}

	deleteID := createProject(deleteKey)
	async := call(adminID, http.MethodPost, "/rest/api/3/project/"+deleteKey+"/delete", "", http.StatusSeeOther)
	if async.Header().Get("Location") == "" || !strings.Contains(async.Body.String(), `"status":"ENQUEUED"`) {
		t.Fatalf("async delete = %s, location=%q", async.Body.String(), async.Header().Get("Location"))
	}
	var task map[string]any
	if err = json.Unmarshal(async.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if err = (&store.APITaskRunner{Store: st}).DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	completed := call(adminID, http.MethodGet, "/rest/api/3/task/"+task["id"].(string), "", http.StatusOK)
	if !strings.Contains(completed.Body.String(), `"status":"COMPLETE"`) || !strings.Contains(completed.Body.String(), deleteID) {
		t.Fatal(completed.Body.String())
	}
	if _, err = st.ProjectByIDOrKeyAnyState(ctx, workspaceID, deleteID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("async-deleted project lookup err=%v", err)
	}
	expiredKey := fmt.Sprintf("E%08d", suffix)
	expiredID := createProject(expiredKey)
	call(adminID, http.MethodDelete, "/rest/api/3/project/"+expiredKey, "", http.StatusNoContent)
	exec(`UPDATE projects SET trashed_at=now()-interval '61 days' WHERE id=$1`, expiredID)
	if err = (&store.ProjectTrashRunner{Store: st}).DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.ProjectByIDOrKeyAnyState(ctx, workspaceID, expiredID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expired project lookup err=%v", err)
	}

	call(adminID, http.MethodDelete, "/rest/api/3/project/"+projectKey+"?enableUndo=false", "", http.StatusNoContent)
	if _, err = st.ProjectByIDOrKeyAnyState(ctx, workspaceID, projectID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("permanently deleted project lookup err=%v", err)
	}
	var pending int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM attachment_blob_deletions WHERE blob_ref=$1`, blobRef).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("attachment deletion pending=%d err=%v", pending, err)
	}
}
