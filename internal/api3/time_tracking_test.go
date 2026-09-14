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
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestTimeTracking covers Jira time tracking: estimates set on create and edit
// as durations, the issue bean's time tracking fields and sub-task aggregates,
// worklogs moving the remaining estimate with every adjustEstimate mode,
// worklog permissions, and JQL over estimates and time spent.
func TestTimeTracking(t *testing.T) {
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
	projectKey := fmt.Sprintf("TT%06d", time.Now().UnixNano()%1000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Time tracking')`, workspaceID)
	for _, identity := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, identity.id, identity.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	service := &commands.Service{Store: st}
	if err = service.UpdateGlobalJiraConfiguration(ctx, workspaceID, adminID, models.JiraSiteConfiguration{AttachmentsEnabled: true, IssueLinkingEnabled: true, SubTasksEnabled: true, TimeTrackingEnabled: true, UnassignedIssuesAllowed: true, VotingEnabled: true, WatchingEnabled: true}); err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: service, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) map[string]any {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(user+"@example.test", user)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, response.Code, want, response.Body.String())
		}
		decoded := map[string]any{}
		_ = json.Unmarshal(response.Body.Bytes(), &decoded)
		return decoded
	}
	fields := func(key string) map[string]any {
		t.Helper()
		return call(adminID, http.MethodGet, "/rest/api/3/issue/"+key, "", http.StatusOK)["fields"].(map[string]any)
	}

	call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Time `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)

	// An original estimate alone starts the remaining estimate too.
	call(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Bad","issuetype":{"name":"Task"},"timetracking":{"originalEstimate":"soon"}}}`, http.StatusBadRequest)
	key := call(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Estimated work","issuetype":{"name":"Task"},"timetracking":{"originalEstimate":"1d 4h"}}}`, http.StatusCreated)["key"].(string)
	created := fields(key)
	tracking := created["timetracking"].(map[string]any)
	if tracking["originalEstimate"] != "1d 4h" || tracking["remainingEstimateSeconds"] != float64(12*3600) || created["timeoriginalestimate"] != float64(12*3600) || created["workratio"] != float64(0) {
		t.Fatalf("created time tracking = %v", created)
	}

	// Create metadata offers the time tracking field while time tracking is on.
	createMeta := call(adminID, http.MethodGet, "/rest/api/3/issue/createmeta?projectKeys="+projectKey+"&expand=projects.issuetypes.fields", "", http.StatusOK)
	if encoded, _ := json.Marshal(createMeta); !strings.Contains(string(encoded), `"timetracking":{`) {
		t.Fatalf("create metadata lacks timetracking: %s", encoded)
	}

	// Logging work moves the remaining estimate as adjustEstimate says.
	path := "/rest/api/3/issue/" + key + "/worklog"
	deliveries := func() int {
		t.Helper()
		var count int
		if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notification_event_deliveries d JOIN issues i ON i.id=d.issue_id WHERE i.key=$1 AND d.event_id=11`, key).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	call(adminID, http.MethodPost, path, `{"timeSpent":"2h"}`, http.StatusCreated)
	// Logging work notifies unless notifyUsers is false.
	if deliveries() != 1 {
		t.Fatalf("work logged deliveries = %d", deliveries())
	}
	if got := fields(key)["timeestimate"]; got != float64(10*3600) {
		t.Fatalf("auto adjustment left %v", got)
	}
	call(adminID, http.MethodPost, path+"?adjustEstimate=leave&notifyUsers=false", `{"timeSpentSeconds":3600}`, http.StatusCreated)
	if deliveries() != 1 {
		t.Fatalf("notifyUsers=false still delivered: %d", deliveries())
	}
	call(adminID, http.MethodPost, path+"?adjustEstimate=manual&reduceBy=30m", `{"timeSpent":"1h"}`, http.StatusCreated)
	if got := fields(key)["timeestimate"]; got != float64(9*3600+1800) {
		t.Fatalf("leave then manual left %v", got)
	}
	worklog := call(adminID, http.MethodPost, path+"?adjustEstimate=new&newEstimate=5h", `{"timeSpent":"1h"}`, http.StatusCreated)
	call(adminID, http.MethodPost, path+"?adjustEstimate=new", `{"timeSpent":"1h"}`, http.StatusBadRequest)
	call(adminID, http.MethodPost, path+"?adjustEstimate=sideways", `{"timeSpent":"1h"}`, http.StatusBadRequest)
	current := fields(key)
	if current["timeestimate"] != float64(5*3600) || current["timespent"] != float64(5*3600) {
		t.Fatalf("after new estimate = %v", current)
	}
	if progress := current["progress"].(map[string]any); progress["progress"] != float64(5*3600) || progress["total"] != float64(10*3600) || progress["percent"] != float64(50) {
		t.Fatalf("progress = %v", progress)
	}
	if current["workratio"] != float64(41) {
		t.Fatalf("work ratio = %v", current["workratio"])
	}
	worklogPath := path + "/" + worklog["id"].(string)
	call(adminID, http.MethodPut, worklogPath, `{"timeSpent":"3h"}`, http.StatusOK)
	if got := fields(key)["timeestimate"]; got != float64(3*3600) {
		t.Fatalf("auto update left %v", got)
	}
	call(adminID, http.MethodDelete, worklogPath+"?adjustEstimate=manual&increaseBy=1h", "", http.StatusNoContent)
	if got := fields(key)["timeestimate"]; got != float64(4*3600) {
		t.Fatalf("manual delete left %v", got)
	}

	// Estimates are edited as a field or with the update operation, and cleared.
	call(adminID, http.MethodPut, "/rest/api/3/issue/"+key, `{"fields":{"timetracking":{"originalEstimate":"2d","remainingEstimate":"1d"}}}`, http.StatusNoContent)
	if tracking = fields(key)["timetracking"].(map[string]any); tracking["originalEstimate"] != "2d" || tracking["remainingEstimate"] != "1d" {
		t.Fatalf("edited estimates = %v", tracking)
	}
	call(adminID, http.MethodPut, "/rest/api/3/issue/"+key, `{"update":{"timetracking":[{"edit":{"remainingEstimate":""}}]}}`, http.StatusNoContent)
	if edited := fields(key); edited["timeestimate"] != nil || edited["timeoriginalestimate"] != float64(16*3600) {
		t.Fatalf("cleared remaining estimate = %v", edited)
	}
	changelog := call(adminID, http.MethodGet, "/rest/api/3/issue/"+key+"/changelog", "", http.StatusOK)
	if encoded, _ := json.Marshal(changelog); !strings.Contains(string(encoded), `"field":"timeestimate"`) || !strings.Contains(string(encoded), `"field":"timespent"`) {
		t.Fatalf("changelog lacks time tracking items: %s", encoded)
	}

	// Sub-tasks add to the parent's aggregates.
	var subtaskType string
	if err = st.Pool.QueryRow(ctx, `SELECT name FROM issue_types WHERE subtask AND (workspace_id IS NULL OR workspace_id=$1) ORDER BY workspace_id NULLS FIRST LIMIT 1`, workspaceID).Scan(&subtaskType); err != nil {
		t.Fatal(err)
	}
	subtask := call(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"parent":{"key":"`+key+`"},"summary":"Part","issuetype":{"name":"`+subtaskType+`"},"timetracking":{"originalEstimate":"4h","remainingEstimate":"3h"}}}`, http.StatusCreated)["key"].(string)
	call(adminID, http.MethodPost, "/rest/api/3/issue/"+subtask+"/worklog", `{"timeSpent":"1h"}`, http.StatusCreated)
	if parent := fields(key); parent["aggregatetimeoriginalestimate"] != float64(20*3600) || parent["aggregatetimespent"] != float64(5*3600) {
		t.Fatalf("aggregates = %v", parent)
	}

	// Members edit only their own worklogs once Edit all worklogs is removed.
	own := call(memberID, http.MethodPost, path, `{"timeSpent":"30m"}`, http.StatusCreated)["id"].(string)
	adminWorklog := call(adminID, http.MethodPost, path+"?adjustEstimate=leave", `{"timeSpent":"30m"}`, http.StatusCreated)["id"].(string)
	exec(`DELETE FROM permission_scheme_grants WHERE workspace_id=$1 AND permission_key IN ('EDIT_ALL_WORKLOGS','DELETE_ALL_WORKLOGS') AND holder_type='projectRole' AND holder_value='10001'`, workspaceID)
	call(memberID, http.MethodPut, path+"/"+adminWorklog, `{"timeSpent":"1h"}`, http.StatusForbidden)
	call(memberID, http.MethodDelete, path+"/"+adminWorklog, "", http.StatusForbidden)
	call(memberID, http.MethodPut, path+"/"+own, `{"timeSpent":"45m"}`, http.StatusOK)
	call(memberID, http.MethodDelete, path+"/"+own, "", http.StatusNoContent)
	exec(`DELETE FROM permission_scheme_grants WHERE workspace_id=$1 AND permission_key='WORK_ON_ISSUES' AND holder_type='projectRole' AND holder_value='10001'`, workspaceID)
	call(memberID, http.MethodPost, path, `{"timeSpent":"30m"}`, http.StatusForbidden)

	// JQL compares estimates and time spent as durations.
	search := func(jql string) []any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jql": jql, "fields": []string{"summary"}})
		return call(adminID, http.MethodPost, "/rest/api/3/search/jql", string(body), http.StatusOK)["issues"].([]any)
	}
	if found := search(`project = ` + projectKey + ` AND originalEstimate >= 1d AND timeSpent > 4h`); len(found) != 1 {
		t.Fatalf("estimate search = %v", found)
	}
	if found := search(`project = ` + projectKey + ` AND remainingEstimate is EMPTY`); len(found) != 1 {
		t.Fatalf("empty remaining estimate search = %v", found)
	}
	// The parent has logged 4h 30m of 16h (28%); the sub-task 1h of 4h (25%).
	if found := search(`project = ` + projectKey + ` AND workRatio > 26 ORDER BY timeSpent DESC`); len(found) != 1 {
		t.Fatalf("work ratio search = %v", found)
	}
	call(adminID, http.MethodPost, "/rest/api/3/search/jql", `{"jql":"project = `+projectKey+` AND timeSpent > later"}`, http.StatusBadRequest)
}
