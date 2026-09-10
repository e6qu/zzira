package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestIssueSecuritySchemeContract(t *testing.T) {
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
	projectKey := fmt.Sprintf("S%08d", time.Now().UnixNano()%100000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Issue security contract')`, workspaceID)
	for _, identity := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Security "+identity.id)
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
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if user != "" {
			request.SetBasicAuth(user+"@example.test", user)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	projectResponse := call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Security","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)
	var projectWire struct {
		ID int64 `json:"id"`
	}
	if err = json.Unmarshal(projectResponse.Body.Bytes(), &projectWire); err != nil || projectWire.ID == 0 {
		t.Fatalf("project=%+v err=%v body=%s", projectWire, err, projectResponse.Body.String())
	}
	projectID := fmt.Sprint(projectWire.ID)

	call(memberID, http.MethodGet, "/rest/api/3/issuesecurityschemes", "", http.StatusForbidden)
	created := call(adminID, http.MethodPost, "/rest/api/3/issuesecurityschemes", `{"name":"Confidential work","description":"Restrict sensitive work","levels":[{"name":"Leadership","description":"Leads and reporter","isDefault":true,"members":[{"type":"user","parameter":"`+adminID+`"},{"type":"reporter"}]}]}`, http.StatusCreated)
	var scheme struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &scheme); err != nil || scheme.ID == "" {
		t.Fatalf("scheme=%+v err=%v body=%s", scheme, err, created.Body.String())
	}
	schemePath := "/rest/api/3/issuesecurityschemes/" + scheme.ID
	detail := call(adminID, http.MethodGet, schemePath, "", http.StatusOK)
	var decoded struct {
		Levels []struct {
			ID string `json:"id"`
		} `json:"levels"`
	}
	if err = json.Unmarshal(detail.Body.Bytes(), &decoded); err != nil || len(decoded.Levels) != 1 {
		t.Fatalf("detail=%+v err=%v body=%s", decoded, err, detail.Body.String())
	}
	levelID := decoded.Levels[0].ID
	call(adminID, http.MethodGet, "/rest/api/3/issuesecurityschemes", "", http.StatusOK)
	call(adminID, http.MethodGet, "/rest/api/3/issuesecurityschemes/search?id="+scheme.ID+"&maxResults=1", "", http.StatusOK)
	call(adminID, http.MethodGet, "/rest/api/3/issuesecurityschemes/level?id="+levelID+"&onlyDefault=true", "", http.StatusOK)
	call(adminID, http.MethodGet, "/rest/api/3/securitylevel/"+levelID, "", http.StatusOK)
	call(adminID, http.MethodPut, schemePath, `{"name":"Confidential delivery","description":"Sensitive delivery work"}`, http.StatusNoContent)
	call(adminID, http.MethodPut, schemePath+"/level/"+levelID, `{"name":"Release leads","description":"Release leadership"}`, http.StatusNoContent)
	call(adminID, http.MethodPut, "/rest/api/3/issuesecurityschemes/level/default", `{"defaultValues":[{"issueSecuritySchemeId":"`+scheme.ID+`","defaultLevelId":"`+levelID+`"}]}`, http.StatusNoContent)
	call(adminID, http.MethodPut, schemePath+"/level", `{"levels":[{"name":"Engineering","members":[{"type":"projectRole","parameter":"10001"}]}]}`, http.StatusNoContent)

	detail = call(adminID, http.MethodGet, schemePath, "", http.StatusOK)
	decoded.Levels = nil
	if err = json.Unmarshal(detail.Body.Bytes(), &decoded); err != nil || len(decoded.Levels) != 2 {
		t.Fatalf("detail=%+v err=%v body=%s", decoded, err, detail.Body.String())
	}
	secondLevelID := decoded.Levels[1].ID
	call(adminID, http.MethodPut, schemePath+"/level/"+secondLevelID+"/member", `{"members":[{"type":"assignee"}]}`, http.StatusNoContent)
	members := call(adminID, http.MethodGet, "/rest/api/3/issuesecurityschemes/level/member?schemeId="+scheme.ID, "", http.StatusOK)
	call(adminID, http.MethodGet, schemePath+"/members?issueSecurityLevelId="+secondLevelID, "", http.StatusOK)
	var memberPage struct {
		Values []struct {
			ID                   string `json:"id"`
			IssueSecurityLevelID string `json:"issueSecurityLevelId"`
			Holder               struct {
				Type string `json:"type"`
			} `json:"holder"`
		} `json:"values"`
	}
	if err = json.Unmarshal(members.Body.Bytes(), &memberPage); err != nil {
		t.Fatal(err)
	}
	removeMemberID := ""
	for _, value := range memberPage.Values {
		if value.IssueSecurityLevelID == secondLevelID && value.Holder.Type == "assignee" {
			removeMemberID = value.ID
		}
	}
	if removeMemberID == "" {
		t.Fatal(members.Body.String())
	}
	call(adminID, http.MethodDelete, schemePath+"/level/"+secondLevelID+"/member/"+removeMemberID, "", http.StatusNoContent)

	assignment := call(adminID, http.MethodPut, "/rest/api/3/issuesecurityschemes/project", `{"projectId":"`+projectID+`","schemeId":"`+scheme.ID+`","oldToNewSecurityLevelMappings":[]}`, http.StatusSeeOther)
	var task struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(assignment.Body.Bytes(), &task); err != nil || task.ID == "" {
		t.Fatalf("task=%+v err=%v body=%s", task, err, assignment.Body.String())
	}
	// A second association for the same project must not race the first.
	call(adminID, http.MethodPut, "/rest/api/3/issuesecurityschemes/project", `{"projectId":"`+projectID+`","schemeId":"`+scheme.ID+`","oldToNewSecurityLevelMappings":[]}`, http.StatusConflict)
	runner := store.APITaskRunner{Store: st}
	if err = runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	call(adminID, http.MethodGet, "/rest/api/3/task/"+task.ID, "", http.StatusOK)
	call(adminID, http.MethodGet, "/rest/api/3/issuesecurityschemes/project?projectId="+projectID, "", http.StatusOK)
	call(adminID, http.MethodGet, "/rest/api/3/issuesecurityschemes/search?projectId="+projectID, "", http.StatusOK)
	call(adminID, http.MethodGet, "/rest/api/3/project/"+projectKey+"/issuesecuritylevelscheme", "", http.StatusOK)
	call(memberID, http.MethodGet, "/rest/api/3/project/"+projectKey+"/securitylevel", "", http.StatusOK)

	issue := call(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Restricted release","issuetype":{"name":"Task"},"security":{"id":"`+levelID+`"}}}`, http.StatusCreated)
	var createdIssue struct {
		Key string `json:"key"`
	}
	if err = json.Unmarshal(issue.Body.Bytes(), &createdIssue); err != nil {
		t.Fatal(err)
	}
	call(memberID, http.MethodGet, "/rest/api/3/issue/"+createdIssue.Key, "", http.StatusNotFound)

	removeLevel := call(adminID, http.MethodDelete, schemePath+"/level/"+levelID+"?replaceWith="+secondLevelID, "", http.StatusSeeOther)
	task.ID = ""
	if err = json.Unmarshal(removeLevel.Body.Bytes(), &task); err != nil || task.ID == "" {
		t.Fatalf("task=%+v err=%v body=%s", task, err, removeLevel.Body.String())
	}
	// The same level cannot be queued for removal twice.
	call(adminID, http.MethodDelete, schemePath+"/level/"+levelID+"?replaceWith="+secondLevelID, "", http.StatusConflict)
	if err = runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	call(memberID, http.MethodGet, "/rest/api/3/issue/"+createdIssue.Key, "", http.StatusOK)

	clear := call(adminID, http.MethodPut, "/rest/api/3/issuesecurityschemes/project", `{"projectId":"`+projectID+`","schemeId":null,"oldToNewSecurityLevelMappings":[{"oldLevelId":"`+secondLevelID+`","newLevelId":""}]}`, http.StatusSeeOther)
	task.ID = ""
	if err = json.Unmarshal(clear.Body.Bytes(), &task); err != nil || task.ID == "" {
		t.Fatalf("task=%+v err=%v body=%s", task, err, clear.Body.String())
	}
	if err = runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	call(adminID, http.MethodDelete, schemePath, "", http.StatusNoContent)
	call(adminID, http.MethodGet, schemePath, "", http.StatusNotFound)
	var actions int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type LIKE '%security%'`, workspaceID).Scan(&actions); err != nil || actions < 8 {
		t.Fatalf("actions=%d err=%v", actions, err)
	}
}
