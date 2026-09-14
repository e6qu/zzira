package api3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestProjectAdministrationPermissions covers the permissions Jira documents
// for project configuration and work item changes: a project's administrators
// manage its components, versions, properties, features, details and
// statuses without administering the site; issue properties need Edit issues;
// and attachments are deleted with Delete own or Delete all attachments.
func TestProjectAdministrationPermissions(t *testing.T) {
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
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspaceID, adminID, leadID, memberID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	projectKey := fmt.Sprintf("PA%06d", time.Now().UnixNano()%1000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Project administration')`, workspaceID)
	for _, identity := range []struct{ id, role, name string }{{adminID, "admin", "Site Admin"}, {leadID, "member", "Project Lead"}, {memberID, "member", "Team Member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", identity.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM statuses WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, leadID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})

	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	send := func(user, method, path, contentType, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(user+"@example.test", user)
		request.Header.Set("Content-Type", contentType)
		request.Header.Set("X-Atlassian-Token", "no-check")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	call := func(user, method, path, body string, want int) string {
		t.Helper()
		return send(user, method, path, "application/json", body, want)
	}
	object := func(body string) map[string]any {
		t.Helper()
		decoded := map[string]any{}
		if decodeErr := json.Unmarshal([]byte(body), &decoded); decodeErr != nil {
			t.Fatalf("decode %s: %v", body, decodeErr)
		}
		return decoded
	}

	// The lead holds the project's Administrators role; the member does not.
	project := object(call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Administration `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+leadID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated))
	projectID := fmt.Sprint(project["id"])

	// Components.
	call(memberID, http.MethodPost, "/rest/api/3/component", `{"name":"Member component","project":"`+projectKey+`"}`, http.StatusForbidden)
	component := object(call(leadID, http.MethodPost, "/rest/api/3/component", `{"name":"Lead component","project":"`+projectKey+`"}`, http.StatusCreated))
	componentPath := "/rest/api/3/component/" + fmt.Sprint(component["id"])
	call(memberID, http.MethodPut, componentPath, `{"description":"Member edit"}`, http.StatusForbidden)
	call(leadID, http.MethodPut, componentPath, `{"description":"Lead edit"}`, http.StatusOK)
	call(memberID, http.MethodDelete, componentPath, "", http.StatusForbidden)
	call(leadID, http.MethodDelete, componentPath, "", http.StatusNoContent)

	// Versions and their approvers.
	call(memberID, http.MethodPost, "/rest/api/3/version", `{"name":"Member release","projectId":`+projectID+`}`, http.StatusForbidden)
	version := object(call(leadID, http.MethodPost, "/rest/api/3/version", `{"name":"Lead release","projectId":`+projectID+`}`, http.StatusCreated))
	versionID := version["id"].(string)
	call(leadID, http.MethodPut, "/rest/api/3/version/"+versionID, `{"description":"Planned by the lead"}`, http.StatusOK)
	if err = st.AddVersionApprover(ctx, workspaceID, memberID, versionID, memberID, ""); err == nil {
		t.Fatal("a member asked for release approvals")
	}
	if err = st.AddVersionApprover(ctx, workspaceID, leadID, versionID, memberID, "Check the notes"); err != nil {
		t.Fatal(err)
	}
	call(memberID, http.MethodDelete, "/rest/api/3/version/"+versionID, "", http.StatusForbidden)

	// Project details, properties, features and email.
	call(memberID, http.MethodPut, "/rest/api/3/project/"+projectKey, `{"description":"Member edit"}`, http.StatusForbidden)
	call(leadID, http.MethodPut, "/rest/api/3/project/"+projectKey, `{"description":"Lead edit"}`, http.StatusOK)
	call(memberID, http.MethodPut, "/rest/api/3/project/"+projectKey+"/properties/team", `{"size":3}`, http.StatusForbidden)
	call(leadID, http.MethodPut, "/rest/api/3/project/"+projectKey+"/properties/team", `{"size":3}`, http.StatusCreated)
	call(memberID, http.MethodDelete, "/rest/api/3/project/"+projectKey+"/properties/team", "", http.StatusForbidden)
	call(leadID, http.MethodDelete, "/rest/api/3/project/"+projectKey+"/properties/team", "", http.StatusNoContent)
	features := object(call(leadID, http.MethodGet, "/rest/api/3/project/"+projectKey+"/features", "", http.StatusOK))["features"].([]any)
	if len(features) == 0 {
		t.Fatal("a software project has no features")
	}
	featurePath := "/rest/api/3/project/" + projectKey + "/features/" + features[0].(map[string]any)["feature"].(string)
	call(memberID, http.MethodPut, featurePath, `{"state":"DISABLED"}`, http.StatusForbidden)
	call(leadID, http.MethodPut, featurePath, `{"state":"DISABLED"}`, http.StatusOK)
	call(memberID, http.MethodPut, "/rest/api/3/project/"+projectID+"/email", `{"emailAddress":"member@example.test"}`, http.StatusForbidden)
	call(leadID, http.MethodPut, "/rest/api/3/project/"+projectID+"/email", `{"emailAddress":"releases@example.test"}`, http.StatusNoContent)

	// Statuses: a project's administrators manage its statuses, not global ones.
	projectStatus := `{"scope":{"type":"PROJECT","project":{"id":"` + projectID + `"}},"statuses":[{"name":"Lead review","statusCategory":"IN_PROGRESS"}]}`
	call(memberID, http.MethodPost, "/rest/api/3/statuses", projectStatus, http.StatusForbidden)
	call(leadID, http.MethodPost, "/rest/api/3/statuses", projectStatus, http.StatusOK)
	call(leadID, http.MethodPost, "/rest/api/3/statuses", `{"scope":{"type":"GLOBAL"},"statuses":[{"name":"Lead global","statusCategory":"TODO"}]}`, http.StatusForbidden)

	// Issue properties need Edit issues; attachments need Delete own or Delete
	// all attachments.
	issue := object(call(memberID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Permission work","issuetype":{"name":"Task"}}}`, http.StatusCreated))
	issueKey := issue["key"].(string)
	call(memberID, http.MethodPut, "/rest/api/3/issue/"+issueKey+"/properties/review", `{"ready":true}`, http.StatusCreated)
	exec(`DELETE FROM permission_scheme_grants WHERE workspace_id=$1 AND permission_key='EDIT_ISSUES' AND holder_type='projectRole' AND holder_value='10001'`, workspaceID)
	call(memberID, http.MethodPut, "/rest/api/3/issue/"+issueKey+"/properties/review", `{"ready":false}`, http.StatusForbidden)
	call(memberID, http.MethodDelete, "/rest/api/3/issue/"+issueKey+"/properties/review", "", http.StatusForbidden)

	upload := func(user string) string {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, _ := writer.CreateFormFile("file", "notes.txt")
		_, _ = part.Write([]byte("release notes"))
		_ = writer.Close()
		var files []map[string]any
		if decodeErr := json.Unmarshal([]byte(send(user, http.MethodPost, "/rest/api/3/issue/"+issueKey+"/attachments", writer.FormDataContentType(), body.String(), http.StatusOK)), &files); decodeErr != nil || len(files) != 1 {
			t.Fatalf("attachment upload: %v", decodeErr)
		}
		return fmt.Sprint(files[0]["id"])
	}
	leadAttachment := upload(leadID)
	// Members delete anyone's attachments while they hold Delete all attachments.
	call(memberID, http.MethodDelete, "/rest/api/3/attachment/"+leadAttachment, "", http.StatusNoContent)
	exec(`DELETE FROM permission_scheme_grants WHERE workspace_id=$1 AND permission_key='DELETE_ALL_ATTACHMENTS' AND holder_type='projectRole' AND holder_value='10001'`, workspaceID)
	leadAttachment, memberAttachment := upload(leadID), upload(memberID)
	call(memberID, http.MethodDelete, "/rest/api/3/attachment/"+leadAttachment, "", http.StatusForbidden)
	call(memberID, http.MethodDelete, "/rest/api/3/attachment/"+memberAttachment, "", http.StatusNoContent)
}
