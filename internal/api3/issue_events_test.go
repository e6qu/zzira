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
)

// TestCustomIssueEvents covers events administrators add beyond Jira's
// built-in ones: they are listed with the built-ins, notification schemes map
// them, workflow transitions fire them through customIssueEventId, and an
// event in use cannot be deleted.
func TestCustomIssueEvents(t *testing.T) {
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
	projectKey := fmt.Sprintf("EV%06d", time.Now().UnixNano()%1000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Issue events')`, workspaceID)
	for _, identity := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Events "+identity.id)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflows WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	send := func(user, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(user+"@example.test", user)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		return response
	}
	call := func(user, method, path, body string, want int) string {
		t.Helper()
		response := send(user, method, path, body)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}

	event, err := st.CreateIssueEvent(ctx, workspaceID, adminID, "Release approved", "A release was approved.")
	if err != nil || !store.IsCustomIssueEvent(event.ID) {
		t.Fatalf("event = %+v err=%v", event, err)
	}
	if _, err = st.CreateIssueEvent(ctx, workspaceID, adminID, "release APPROVED", ""); !errors.Is(err, store.ErrIssueEventConflict) {
		t.Fatalf("duplicate event err = %v", err)
	}
	if _, err = st.CreateIssueEvent(ctx, workspaceID, adminID, "Issue created", ""); !errors.Is(err, store.ErrIssueEventConflict) {
		t.Fatalf("built-in name clash err = %v", err)
	}
	eventID := fmt.Sprint(event.ID)
	if events := call(adminID, http.MethodGet, "/rest/api/3/events", "", http.StatusOK); !strings.Contains(events, `{"id":`+eventID+`,"name":"Release approved"}`) || !strings.Contains(events, `"name":"Issue created"`) {
		t.Fatalf("events = %s", events)
	}
	call(memberID, http.MethodGet, "/rest/api/3/events", "", http.StatusForbidden)

	// A notification scheme maps the custom event.
	var schemes struct {
		Values []struct {
			ID int64 `json:"id"`
		} `json:"values"`
	}
	if err = json.Unmarshal([]byte(call(adminID, http.MethodGet, "/rest/api/3/notificationscheme", "", http.StatusOK)), &schemes); err != nil || len(schemes.Values) == 0 {
		t.Fatalf("schemes = %+v err=%v", schemes, err)
	}
	schemePath := fmt.Sprintf("/rest/api/3/notificationscheme/%d", schemes.Values[0].ID)
	if added := send(adminID, http.MethodPut, schemePath+"/notification", `{"notificationSchemeEvents":[{"event":{"id":"`+eventID+`"},"notifications":[{"notificationType":"Reporter"}]}]}`); added.Code >= 300 {
		t.Fatalf("mapping the custom event: %d %s", added.Code, added.Body.String())
	}
	if scheme := call(adminID, http.MethodGet, schemePath+"?expand=all", "", http.StatusOK); !strings.Contains(scheme, `"name":"Release approved"`) {
		t.Fatalf("scheme events = %s", scheme)
	}
	if added := send(adminID, http.MethodPut, schemePath+"/notification", `{"notificationSchemeEvents":[{"event":{"id":"99999"},"notifications":[{"notificationType":"Reporter"}]}]}`); added.Code != http.StatusBadRequest {
		t.Fatalf("mapping an unknown event: %d %s", added.Code, added.Body.String())
	}
	if err = st.DeleteIssueEvent(ctx, workspaceID, adminID, event.ID); !errors.Is(err, store.ErrIssueEventConflict) {
		t.Fatalf("deleting a mapped event err = %v", err)
	}
	if err = st.DeleteIssueEvent(ctx, workspaceID, adminID, 1); !errors.Is(err, store.ErrIssueEventValidation) {
		t.Fatalf("deleting a built-in event err = %v", err)
	}

	// A workflow transition fires the custom event.
	workflowName := "Events " + projectKey
	createBody := func(event string) string {
		return `{"scope":{"type":"GLOBAL"},"statuses":[{"id":"st_todo","name":"To Do","statusCategory":"TODO","statusReference":"todo"},{"id":"st_done","name":"Done","statusCategory":"DONE","statusReference":"done"}],
			"workflows":[{"name":"` + workflowName + `","description":"","statuses":[{"statusReference":"todo","properties":{}},{"statusReference":"done","properties":{}}],
			"transitions":[{"id":"11","name":"Approve release","type":"DIRECTED","toStatusReference":"done","links":[{"fromStatusReference":"todo"}],"customIssueEventId":"` + event + `"}]}]}`
	}
	if invalid := send(adminID, http.MethodPost, "/rest/api/3/workflows/create", createBody("99999")); invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "custom issue event does not exist") {
		t.Fatalf("unknown transition event: %d %s", invalid.Code, invalid.Body.String())
	}
	if validation := call(adminID, http.MethodPost, "/rest/api/3/workflows/create/validation", `{"payload":`+createBody("99999")+`,"validationOptions":{"levels":["ERROR"]}}`, http.StatusOK); !strings.Contains(validation, "TRANSITION_EVENT_NOT_FOUND") {
		t.Fatalf("create validation = %s", validation)
	}
	call(adminID, http.MethodPost, "/rest/api/3/workflows/create", createBody(eventID), http.StatusOK)
	if search := call(adminID, http.MethodGet, "/rest/api/3/workflows/search?queryString="+projectKey+"&expand=values.transitions", "", http.StatusOK); !strings.Contains(search, `"customIssueEventId":"`+eventID+`"`) {
		t.Fatalf("workflow search = %s", search)
	}

	project := map[string]any{}
	if err = json.Unmarshal([]byte(call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Events `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)), &project); err != nil {
		t.Fatal(err)
	}
	scheme := map[string]any{}
	if err = json.Unmarshal([]byte(call(adminID, http.MethodPost, "/rest/api/3/workflowscheme", `{"name":"Events scheme `+projectKey+`","defaultWorkflow":"`+workflowName+`"}`, http.StatusCreated)), &scheme); err != nil {
		t.Fatal(err)
	}
	call(adminID, http.MethodPut, "/rest/api/3/workflowscheme/project", fmt.Sprintf(`{"projectId":"%v","workflowSchemeId":"%v"}`, project["id"], scheme["id"]), http.StatusNoContent)
	issue := map[string]any{}
	if err = json.Unmarshal([]byte(call(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Approve the release","issuetype":{"name":"Task"}}}`, http.StatusCreated)), &issue); err != nil {
		t.Fatal(err)
	}
	issueKey := issue["key"].(string)
	var transitions struct {
		Transitions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"transitions"`
	}
	if err = json.Unmarshal([]byte(call(adminID, http.MethodGet, "/rest/api/3/issue/"+issueKey+"/transitions", "", http.StatusOK)), &transitions); err != nil {
		t.Fatal(err)
	}
	transitionID := ""
	for _, transition := range transitions.Transitions {
		if transition.Name == "Approve release" {
			transitionID = transition.ID
		}
	}
	if transitionID == "" {
		t.Fatalf("transitions = %+v", transitions)
	}
	call(adminID, http.MethodPost, "/rest/api/3/issue/"+issueKey+"/transitions", `{"transition":{"id":"`+transitionID+`"}}`, http.StatusNoContent)
	var fired bool
	if err = st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM notification_event_deliveries d JOIN issues i ON i.id=d.issue_id WHERE i.workspace_id=$1 AND i.key=$2 AND d.event_id=$3)`, workspaceID, issueKey, event.ID).Scan(&fired); err != nil || !fired {
		t.Fatalf("custom event delivery fired=%v err=%v", fired, err)
	}
}
