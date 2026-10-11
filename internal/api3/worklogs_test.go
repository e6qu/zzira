package api3

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestWorklogOperations pins Jira's worklog surface beyond create, read and
// delete: the update, the move between work items, the bulk delete, the
// updated and deleted feeds, and the bulk fetch by id.
func TestWorklogOperations(t *testing.T) {
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
	workspaceID, adminID, outsiderID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Worklog operations')`, workspaceID)
	for _, value := range []struct{ id, role string }{{adminID, "admin"}, {outsiderID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Worklog user')`, value.id, value.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, value.id, value.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, value.id, store.HashToken(value.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM deleted_worklogs WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM worklogs WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, outsiderID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
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
	call(adminID, "POST", "/rest/api/3/project", `{"key":"WORK","name":"Worklog work","projectTypeKey":"software","leadAccountId":"`+adminID+`"}`, 201)
	// Every member lands in the default project role, so the outsider has to be
	// taken out of it to stand for someone who cannot browse the project.
	var projectID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND key='WORK'`, workspaceID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	exec(`DELETE FROM role_bindings WHERE scope_type='project' AND scope_id=$1 AND principal_id=$2`, projectID, outsiderID)
	t.Cleanup(func() { exec(`DELETE FROM role_bindings WHERE scope_type='project' AND scope_id=$1`, projectID) })
	newIssue := func(summary string) string {
		t.Helper()
		created := call(adminID, "POST", "/rest/api/3/issue", `{"fields":{"project":{"key":"WORK"},"summary":"`+summary+`","issuetype":{"name":"Task"}}}`, 201)
		var issue struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(created.Body.Bytes(), &issue); err != nil || issue.Key == "" {
			t.Fatalf("created issue: %v %s", err, created.Body.String())
		}
		return issue.Key
	}
	source, target := newIssue("Worklog source"), newIssue("Worklog target")
	newWorklog := func(issueKey string, seconds int) string {
		t.Helper()
		created := call(adminID, "POST", "/rest/api/3/issue/"+issueKey+"/worklog", `{"timeSpentSeconds":`+strconv.Itoa(seconds)+`,"started":"2026-10-01T11:34:56.123+0300"}`, 201)
		var worklog struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(created.Body.Bytes(), &worklog); err != nil || worklog.ID == "" {
			t.Fatalf("created worklog: %v %s", err, created.Body.String())
		}
		return worklog.ID
	}
	first := newWorklog(source, 3600)
	path := "/rest/api/3/issue/" + source + "/worklog"
	decode := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	original := decode(call(adminID, "GET", path+"/"+first, "", 200))
	if original["started"] != "2026-10-01T08:34:56.123+0000" || original["startsAt"] != nil || original["issueId"] == nil || original["created"] != original["updated"] {
		t.Fatalf("incorrect worklog metadata: %v", original)
	}
	self := original["self"].(string)
	if self != "https://zzira.test/rest/api/3/issue/"+original["issueId"].(string)+"/worklog/"+first {
		t.Fatal(self)
	}
	call(adminID, "GET", strings.TrimPrefix(self, "https://zzira.test"), "", 200)
	for _, raw := range []string{`null`, `""`, `"2026-02-30T09:00:00Z"`, `"2026-10-01T09:00:00"`, `{}`} {
		call(adminID, "POST", path, `{"timeSpentSeconds":60,"started":`+raw+`}`, 400)
		call(adminID, "PUT", path+"/"+first, `{"started":`+raw+`}`, 400)
	}
	call(adminID, "POST", path, `{"timeSpentSeconds":60,"visibility":{"type":"group","value":"private"}}`, 400)
	call(adminID, "PUT", path+"/"+first, `{"visibility":{"type":"role","value":"Administrators"}}`, 400)
	// An edit by another administrator must preserve authorship and creation time.
	exec(`UPDATE memberships SET role='admin' WHERE workspace_id=$1 AND user_id=$2`, workspaceID, outsiderID)
	exec(`UPDATE worklogs SET updated_at='2020-01-01' WHERE workspace_id=$1 AND jira_id::text=$2`, workspaceID, first)
	edited := decode(call(outsiderID, "PUT", path+"/"+first, `{"started":"2026-09-30T07:00:00Z"}`, 200))
	if edited["author"].(map[string]any)["accountId"] != adminID || edited["updateAuthor"].(map[string]any)["accountId"] != outsiderID || edited["created"] != original["created"] || edited["updated"] == "2020-01-01T00:00:00.000+0000" || edited["started"] != "2026-09-30T07:00:00.000+0000" {
		t.Fatalf("edit metadata: %v", edited)
	}
	snapshot, err := st.BootstrapSnapshot(ctx, workspaceID, outsiderID)
	if err != nil || len(snapshot.Worklogs) != 1 || snapshot.Worklogs[0].UpdateAuthorID != outsiderID || snapshot.Worklogs[0].Started != "2026-09-30T07:00:00.000Z" {
		t.Fatalf("replica snapshot metadata: %+v %v", snapshot, err)
	}
	var payload []byte
	if err := st.Pool.QueryRow(ctx, `SELECT payload FROM actions WHERE workspace_id=$1 AND entity_type='worklog' ORDER BY seq DESC LIMIT 1`, workspaceID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var upsert models.WorklogUpsertPayload
	if err := json.Unmarshal(payload, &upsert); err != nil || upsert.Worklog.UpdateAuthorID != outsiderID || upsert.Worklog.Started != "2026-09-30T07:00:00.000Z" {
		t.Fatalf("sync action lost worklog metadata: %s (%v)", payload, err)
	}

	exec(`UPDATE memberships SET role='member' WHERE workspace_id=$1 AND user_id=$2`, workspaceID, outsiderID)
	exec(`DELETE FROM role_bindings WHERE scope_type='project' AND scope_id=$1 AND principal_id=$2`, projectID, outsiderID)

	// The single read and delete answer for a worklog on this work item only.
	read := call(adminID, "GET", "/rest/api/3/issue/"+source+"/worklog/"+first, "", 200)
	if !strings.Contains(read.Body.String(), `"timeSpentSeconds":3600`) {
		t.Fatal(read.Body.String())
	}
	call(adminID, "GET", "/rest/api/3/issue/"+target+"/worklog/"+first, "", 404)
	spare := newWorklog(source, 60)
	call(adminID, "DELETE", "/rest/api/3/issue/"+target+"/worklog/"+spare, "", 404)
	call(adminID, "DELETE", "/rest/api/3/issue/"+source+"/worklog/"+spare, "", 204)

	// The update replaces time and comment, and refuses a non-positive time.
	updated := call(adminID, "PUT", "/rest/api/3/issue/"+source+"/worklog/"+first,
		`{"timeSpentSeconds":7200,"comment":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"revised"}]}]}}`, 200)
	var updatedBean struct {
		TimeSpentSeconds int `json:"timeSpentSeconds"`
	}
	if err := json.Unmarshal(updated.Body.Bytes(), &updatedBean); err != nil || updatedBean.TimeSpentSeconds != 7200 {
		t.Fatalf("updated worklog: %v %s", err, updated.Body.String())
	}
	if !strings.Contains(updated.Body.String(), `"revised"`) {
		t.Fatal(updated.Body.String())
	}
	call(adminID, "PUT", "/rest/api/3/issue/"+source+"/worklog/"+first, `{"timeSpentSeconds":0}`, 400)
	call(adminID, "PUT", "/rest/api/3/issue/"+target+"/worklog/"+first, `{"timeSpentSeconds":60}`, 404)

	// Omitting the comment keeps the one already stored.
	kept := call(adminID, "PUT", "/rest/api/3/issue/"+source+"/worklog/"+first, `{"timeSpentSeconds":1800}`, 200)
	if decode(kept)["started"] != "2026-09-30T07:00:00.000+0000" {
		t.Fatal("omitted started changed the work-start time")
	}
	if !strings.Contains(kept.Body.String(), `"revised"`) {
		t.Fatal(kept.Body.String())
	}

	// The bulk fetch returns the worklogs asked for, across work items.
	second := newWorklog(target, 900)
	// Paging keeps the filtered total, including empty pages. Boundary dates
	// are strict, and properties are expanded only when requested.
	call(adminID, "POST", "/rest/api/3/issue/"+target+"/worklog/move", `{"ids":["`+second+`"],"issueIdOrKey":"`+source+`"}`, 204)
	for _, item := range []struct {
		query                    string
		total, count, start, max int
	}{
		{"?startAt=0&maxResults=1", 2, 1, 0, 1}, {"?startAt=1&maxResults=1", 2, 1, 1, 1}, {"?startAt=2&maxResults=1", 2, 0, 2, 1}, {"?maxResults=0", 2, 0, 0, 0},
		{"?startedAfter=" + strconv.FormatInt(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), 10), 1, 1, 0, 5000},
		{"?startedBefore=" + strconv.FormatInt(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), 10), 1, 1, 0, 5000},
		{"?startedAfter=" + strconv.FormatInt(time.Date(2026, 10, 1, 8, 34, 56, 123000000, time.UTC).UnixMilli(), 10), 0, 0, 0, 5000},
	} {
		page := decode(call(adminID, "GET", path+item.query, "", 200))
		if int(page["total"].(float64)) != item.total || len(page["worklogs"].([]any)) != item.count || int(page["startAt"].(float64)) != item.start || int(page["maxResults"].(float64)) != item.max {
			t.Fatalf("%s: %v", item.query, page)
		}
	}
	for _, query := range []string{"?startAt=-1", "?maxResults=no", "?startedAfter=later", "?startedBefore=-1", "?startedAfter=9223372036854775807"} {
		call(adminID, "GET", path+query, "", 400)
	}
	call(adminID, "POST", path+"/move", `{"ids":["`+second+`"],"issueIdOrKey":"`+target+`"}`, 204)

	listed := call(adminID, "POST", "/rest/api/3/worklog/list", `{"ids":["`+first+`","`+second+`"]}`, 200)
	var beans []struct {
		ID      string `json:"id"`
		IssueID string `json:"issueId"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &beans); err != nil || len(beans) != 2 {
		t.Fatalf("worklog list: %v %s", err, listed.Body.String())
	}
	call(adminID, "POST", "/rest/api/3/worklog/list", `{"ids":[]}`, 400)

	// A user who cannot browse the project gets no worklog content.
	hidden := call(outsiderID, "POST", "/rest/api/3/worklog/list", `{"ids":["`+first+`"]}`, 200)
	if strings.TrimSpace(hidden.Body.String()) != "[]" {
		t.Fatalf("outsider saw worklogs: %s", hidden.Body.String())
	}

	// The updated feed reports both, and since excludes what it has seen.
	feed := call(adminID, "GET", "/rest/api/3/worklog/updated?since=0", "", 200)
	var updatedFeed struct {
		Values []struct {
			WorklogID   any   `json:"worklogId"`
			UpdatedTime int64 `json:"updatedTime"`
		} `json:"values"`
		Until    int64 `json:"until"`
		LastPage bool  `json:"lastPage"`
	}
	if err := json.Unmarshal(feed.Body.Bytes(), &updatedFeed); err != nil || len(updatedFeed.Values) != 2 || !updatedFeed.LastPage {
		t.Fatalf("updated feed: %v %s", err, feed.Body.String())
	}
	caughtUp := call(adminID, "GET", "/rest/api/3/worklog/updated?since="+strconv.FormatInt(updatedFeed.Until, 10), "", 200)
	if !strings.Contains(caughtUp.Body.String(), `"values":[]`) {
		t.Fatalf("caught-up feed: %s", caughtUp.Body.String())
	}
	call(adminID, "GET", "/rest/api/3/worklog/updated?since=later", "", 400)

	// The move relocates the worklog and refuses an unknown destination.
	call(adminID, "POST", "/rest/api/3/issue/"+source+"/worklog/move",
		`{"ids":["`+first+`"],"issueIdOrKey":"`+target+`"}`, 204)
	moved := call(adminID, "GET", "/rest/api/3/issue/"+target+"/worklog", "", 200)
	var targetWorklogs struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(moved.Body.Bytes(), &targetWorklogs); err != nil || targetWorklogs.Total != 2 {
		t.Fatalf("moved worklogs: %v %s", err, moved.Body.String())
	}
	call(adminID, "POST", "/rest/api/3/issue/"+target+"/worklog/move",
		`{"ids":["`+first+`"],"issueIdOrKey":"WORK-9999"}`, 400)
	call(adminID, "POST", "/rest/api/3/issue/"+target+"/worklog/move", `{"ids":[],"issueIdOrKey":"`+source+`"}`, 400)
	// A worklog that is not on the source work item is not moved.
	call(adminID, "POST", "/rest/api/3/issue/"+source+"/worklog/move",
		`{"ids":["`+first+`"],"issueIdOrKey":"`+target+`"}`, 404)

	// Properties hang off a worklog, 201 on a new key and 200 on a replacement.
	propertyPath := "/rest/api/3/issue/" + target + "/worklog/" + second + "/properties/billing"
	call(adminID, "PUT", propertyPath, `{"code":"ACME"}`, 201)
	call(adminID, "PUT", propertyPath, `{"code":"ACME-2"}`, 200)
	property := call(adminID, "GET", propertyPath, "", 200)
	if !strings.Contains(property.Body.String(), `"ACME-2"`) {
		t.Fatal(property.Body.String())
	}
	keys := call(adminID, "GET", "/rest/api/3/issue/"+target+"/worklog/"+second+"/properties", "", 200)
	if !strings.Contains(keys.Body.String(), `"key":"billing"`) {
		t.Fatal(keys.Body.String())
	}
	call(adminID, "GET", "/rest/api/3/issue/"+target+"/worklog/"+second+"/properties/missing", "", 404)

	expanded := decode(call(adminID, "GET", "/rest/api/3/issue/"+target+"/worklog?expand=properties", "", 200))
	for _, value := range expanded["worklogs"].([]any) {
		bean := value.(map[string]any)
		properties, ok := bean["properties"].([]any)
		if !ok {
			t.Fatal("properties expansion missing")
		}
		if bean["id"] == second && (len(properties) != 1 || properties[0].(map[string]any)["key"] != "billing") {
			t.Fatalf("expanded properties: %v", bean)
		}
	}
	singleExpanded := decode(call(adminID, "GET", "/rest/api/3/issue/"+target+"/worklog/"+second+"?expand=properties", "", 200))
	if len(singleExpanded["properties"].([]any)) != 1 {
		t.Fatal(singleExpanded)
	}
	plain := decode(call(adminID, "GET", "/rest/api/3/issue/"+target+"/worklog/"+second, "", 200))
	if plain["properties"] != nil {
		t.Fatal("unrequested properties included")
	}
	call(adminID, "DELETE", propertyPath, "", 204)
	call(adminID, "DELETE", propertyPath, "", 404)
	// A property on a worklog that is not on this work item is not reachable.
	call(adminID, "GET", "/rest/api/3/issue/"+source+"/worklog/"+second+"/properties", "", 404)
	call(adminID, "PUT", propertyPath, `{"code":"ACME"}`, 201)

	// The bulk delete empties the work item and every removal is tombstoned.
	call(adminID, "DELETE", "/rest/api/3/issue/"+target+"/worklog", "", 204)
	emptied := call(adminID, "GET", "/rest/api/3/issue/"+target+"/worklog", "", 200)
	if !strings.Contains(emptied.Body.String(), `"total":0`) {
		t.Fatal(emptied.Body.String())
	}
	deletedFeed := call(adminID, "GET", "/rest/api/3/worklog/deleted?since=0", "", 200)
	var deleted struct {
		Values []struct {
			WorklogID any `json:"worklogId"`
		} `json:"values"`
	}
	if err := json.Unmarshal(deletedFeed.Body.Bytes(), &deleted); err != nil || len(deleted.Values) != 3 {
		t.Fatalf("deleted feed: %v %s", err, deletedFeed.Body.String())
	}
	// Deleting the worklog takes its properties with it.
	var remaining int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM worklog_properties WHERE worklog_id=$1`, second).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("worklog properties outlived the worklog: %d", remaining)
	}

	// A deleted worklog is gone from the bulk fetch.
	gone := call(adminID, "POST", "/rest/api/3/worklog/list", `{"ids":["`+first+`"]}`, 200)
	if strings.TrimSpace(gone.Body.String()) != "[]" {
		t.Fatalf("deleted worklog still listed: %s", gone.Body.String())
	}
}
