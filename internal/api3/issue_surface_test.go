package api3

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestIssueSurfaceContract pins Jira's issue-level surface as a client sees it:
// numeric ids for comments, links, link types and remote links; Jira's link
// directions; comment visibility and properties; watchers and assignment;
// the issue projection; bulk reads and writes; changelogs; the picker;
// notifications; archiving; redaction; and bulk issue properties.
func TestIssueSurfaceContract(t *testing.T) {
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
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	ws := store.NewID("ws")
	admin, auditor, member := store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	projectID := store.NewID("project")
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Issue surface')`, ws)
	for _, identity := range []struct{ id, role, name string }{{admin, "admin", "Ada Admin"}, {auditor, "admin", "Aud Auditor"}, {member, "member", "Mia Member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", identity.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id,project_type_key) VALUES($1,$2,'ISS','Issue surface','wf_default',$3,'software')`, projectID, ws, admin)
	var subtaskType string
	if err = st.Pool.QueryRow(ctx, `SELECT id FROM issue_types WHERE subtask AND workspace_id IS NULL ORDER BY jira_id LIMIT 1`).Scan(&subtaskType); err != nil {
		t.Fatal(err)
	}
	issueIDs := map[string]string{}
	insertIssue := func(key, summary, typeID, parentID string) {
		t.Helper()
		id := store.NewID("iss")
		issueIDs[key] = id
		exec(`INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,reporter_id,parent_id,updated_seq) VALUES($1,$2,$3,$4,$5,'st_todo',$6,$7,NULLIF($8,''),0)`, id, ws, projectID, key, summary, typeID, admin, parentID)
	}
	for _, issue := range []struct{ key, summary string }{
		{"ISS-1", "Alpha launch"}, {"ISS-2", "Alpha blockers"}, {"ISS-3", "Archive candidate"}, {"ISS-4", "Archive by query"},
		{"ISS-5", "Secret holder"}, {"ISS-6", "Parent with subtasks"},
	} {
		insertIssue(issue.key, issue.summary, "it_task", "")
	}
	insertIssue("ISS-7", "A subtask", subtaskType, issueIDs["ISS-6"])
	exec(`UPDATE projects SET issue_seq=7 WHERE id=$1`, projectID)
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM api_tasks WHERE workspace_id=$1`,
			`DELETE FROM email_outbox WHERE workspace_id=$1`,
			`DELETE FROM issues WHERE workspace_id=$1`,
			`DELETE FROM groups WHERE directory_id IN (SELECT d.id FROM directories d JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1)`,
			`DELETE FROM projects WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(q, ws)
		}
		for _, id := range []string{admin, auditor, member} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	runner := &store.APITaskRunner{Store: st}
	drain := func() {
		t.Helper()
		if drainErr := runner.DrainOnce(ctx, ws); drainErr != nil {
			t.Fatal(drainErr)
		}
	}
	call := func(user, method, path, body string, want int) []byte {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(user+"@example.test", user)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response.Body.Bytes()
	}
	decode := func(body []byte, into any) {
		t.Helper()
		if err := json.Unmarshal(body, into); err != nil {
			t.Fatalf("decode: %v %s", err, body)
		}
	}
	jiraID := func(key string) string {
		t.Helper()
		var id int64
		if err := st.Pool.QueryRow(ctx, `SELECT jira_id FROM issues WHERE id=$1`, issueIDs[key]).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return strconv.FormatInt(id, 10)
	}
	adf := func(text string) string {
		return `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"` + text + `"}]}]}`
	}
	type issueBean struct {
		ID     string                     `json:"id"`
		Key    string                     `json:"key"`
		Fields map[string]json.RawMessage `json:"fields"`
	}
	getIssue := func(user, key, query string) issueBean {
		t.Helper()
		var bean issueBean
		decode(call(user, "GET", "/rest/api/3/issue/"+key+query, "", 200), &bean)
		return bean
	}

	// Every site starts with Jira's four link types, by Jira's ids.
	var linkTypes struct {
		IssueLinkTypes []struct{ ID, Name, Inward, Outward, Self string } `json:"issueLinkTypes"`
	}
	decode(call(admin, "GET", "/rest/api/3/issueLinkType", "", 200), &linkTypes)
	gotTypes := []string{}
	for _, lt := range linkTypes.IssueLinkTypes {
		gotTypes = append(gotTypes, lt.ID+":"+lt.Name)
	}
	if strings.Join(gotTypes, ",") != "10000:Blocks,10001:Cloners,10002:Duplicate,10003:Relates" {
		t.Fatalf("default link types = %v", gotTypes)
	}
	call(member, "POST", "/rest/api/3/issueLinkType", `{"name":"Causes","inward":"is caused by","outward":"causes"}`, 404)
	var causes struct{ ID, Name, Self string }
	decode(call(admin, "POST", "/rest/api/3/issueLinkType", `{"name":"Causes","inward":"is caused by","outward":"causes"}`, 201), &causes)
	if n, _ := strconv.Atoi(causes.ID); n < 10004 || causes.Self != "https://zzira.test/rest/api/3/issueLinkType/"+causes.ID {
		t.Fatalf("created link type = %+v", causes)
	}
	call(admin, "POST", "/rest/api/3/issueLinkType", `{"name":"causes","inward":"a","outward":"b"}`, 404)
	call(admin, "POST", "/rest/api/3/issueLinkType", `{"name":"Incomplete"}`, 400)
	call(admin, "GET", "/rest/api/3/issueLinkType/lt_blocks", "", 400)
	call(admin, "PUT", "/rest/api/3/issueLinkType/"+causes.ID, `{"outward":"leads to"}`, 200)

	// Links follow Jira's sides: the inward issue shows the outward description.
	call(admin, "POST", "/rest/api/3/issueLink", `{"type":{"name":"Blocks"},"inwardIssue":{"key":"ISS-1"},"outwardIssue":{"key":"ISS-2"},"comment":{"body":`+adf("Linked for launch")+`}}`, 201)
	type linkField []struct {
		ID           string                `json:"id"`
		Type         struct{ ID string }   `json:"type"`
		InwardIssue  *struct{ Key string } `json:"inwardIssue"`
		OutwardIssue *struct{ Key string } `json:"outwardIssue"`
	}
	var sourceLinks, targetLinks linkField
	decode(getIssue(admin, "ISS-1", "?fields=issuelinks").Fields["issuelinks"], &sourceLinks)
	decode(getIssue(admin, "ISS-2", "?fields=issuelinks").Fields["issuelinks"], &targetLinks)
	if len(sourceLinks) != 1 || sourceLinks[0].OutwardIssue == nil || sourceLinks[0].OutwardIssue.Key != "ISS-2" || sourceLinks[0].Type.ID != "10000" {
		t.Fatalf("source issue links = %+v", sourceLinks)
	}
	if len(targetLinks) != 1 || targetLinks[0].InwardIssue == nil || targetLinks[0].InwardIssue.Key != "ISS-1" {
		t.Fatalf("target issue links = %+v", targetLinks)
	}
	var link struct {
		ID           string               `json:"id"`
		InwardIssue  struct{ Key string } `json:"inwardIssue"`
		OutwardIssue struct{ Key string } `json:"outwardIssue"`
	}
	decode(call(admin, "GET", "/rest/api/3/issueLink/"+sourceLinks[0].ID, "", 200), &link)
	if link.InwardIssue.Key != "ISS-1" || link.OutwardIssue.Key != "ISS-2" {
		t.Fatalf("issue link = %+v", link)
	}
	var linkComments struct {
		Total int `json:"total"`
	}
	decode(call(admin, "GET", "/rest/api/3/issue/ISS-2/comment", "", 200), &linkComments)
	if linkComments.Total != 1 {
		t.Fatalf("link comment on the outward issue: total = %d", linkComments.Total)
	}
	call(admin, "GET", "/rest/api/3/issueLink/lnk_old", "", 400)
	call(admin, "POST", "/rest/api/3/issueLink", `{"type":{"name":"Causes"},"inwardIssue":{"key":"ISS-1"},"outwardIssue":{"key":"ISS-3"}}`, 201)
	call(admin, "DELETE", "/rest/api/3/issueLinkType/"+causes.ID, "", 204)
	decode(getIssue(admin, "ISS-1", "?fields=issuelinks").Fields["issuelinks"], &sourceLinks)
	if len(sourceLinks) != 1 {
		t.Fatalf("links after deleting their type = %+v", sourceLinks)
	}
	call(admin, "DELETE", "/rest/api/3/issueLink/"+link.ID, "", 204)
	call(admin, "GET", "/rest/api/3/issueLink/"+link.ID, "", 404)

	// Remote links upsert by global id and belong to one issue.
	var remote struct {
		ID   int64  `json:"id"`
		Self string `json:"self"`
	}
	decode(call(admin, "POST", "/rest/api/3/issue/ISS-1/remotelink", `{"globalId":"system=ci&id=1","relationship":"built by","object":{"url":"https://ci.example.test/1","title":"Build 1"}}`, 201), &remote)
	if remote.ID < 10000 || remote.Self != "https://zzira.test/rest/api/3/issue/"+jiraID("ISS-1")+"/remotelink/"+strconv.FormatInt(remote.ID, 10) {
		t.Fatalf("created remote link = %+v", remote)
	}
	var updatedRemote struct{ ID int64 }
	decode(call(admin, "POST", "/rest/api/3/issue/ISS-1/remotelink", `{"globalId":"system=ci&id=1","object":{"url":"https://ci.example.test/1","title":"Build 1 (rerun)"}}`, 200), &updatedRemote)
	if updatedRemote.ID != remote.ID {
		t.Fatalf("upsert by global id made a new link: %d vs %d", updatedRemote.ID, remote.ID)
	}
	var remoteLinks []struct {
		ID           int64 `json:"id"`
		Relationship *string
		Object       struct{ Title string } `json:"object"`
	}
	decode(call(admin, "GET", "/rest/api/3/issue/ISS-1/remotelink", "", 200), &remoteLinks)
	if len(remoteLinks) != 1 || remoteLinks[0].Object.Title != "Build 1 (rerun)" || remoteLinks[0].Relationship != nil {
		t.Fatalf("remote links after upsert = %+v", remoteLinks)
	}
	call(admin, "GET", "/rest/api/3/issue/ISS-1/remotelink?globalId="+url.QueryEscape("system=ci&id=1"), "", 200)
	remoteID := strconv.FormatInt(remote.ID, 10)
	call(admin, "PUT", "/rest/api/3/issue/ISS-1/remotelink/"+remoteID, `{"object":{"title":"No url"}}`, 400)
	call(admin, "PUT", "/rest/api/3/issue/ISS-1/remotelink/"+remoteID, `{"globalId":"system=ci&id=1","object":{"url":"https://ci.example.test/1","title":"Build 1 final"}}`, 204)
	call(admin, "GET", "/rest/api/3/issue/ISS-2/remotelink/"+remoteID, "", 400)
	call(admin, "GET", "/rest/api/3/issue/ISS-1/remotelink/999999", "", 404)
	call(admin, "GET", "/rest/api/3/issue/ISS-1/remotelink/abc", "", 400)
	call(member, "GET", "/rest/api/3/issue/ISS-1/remotelink", "", 200)
	call(admin, "DELETE", "/rest/api/3/issue/ISS-1/remotelink", "", 400)
	call(admin, "DELETE", "/rest/api/3/issue/ISS-1/remotelink?globalId="+url.QueryEscape("system=ci&id=1"), "", 204)
	decode(call(admin, "GET", "/rest/api/3/issue/ISS-1/remotelink", "", 200), &remoteLinks)
	if len(remoteLinks) != 0 {
		t.Fatalf("remote links after delete = %+v", remoteLinks)
	}

	// Comments: numeric ids, restricted visibility, edits and properties.
	var group struct{ GroupID string }
	decode(call(admin, "POST", "/rest/api/3/group", `{"name":"issue-surface-leads"}`, 201), &group)
	call(admin, "POST", "/rest/api/3/group/user?groupId="+group.GroupID, `{"accountId":"`+admin+`"}`, 201)
	type commentBean struct {
		ID           string                                    `json:"id"`
		Self         string                                    `json:"self"`
		Author       struct{ AccountID string }                `json:"author"`
		UpdateAuthor struct{ AccountID string }                `json:"updateAuthor"`
		Visibility   *struct{ Type, Value, Identifier string } `json:"visibility"`
		RenderedBody *string                                   `json:"renderedBody"`
		Body         json.RawMessage                           `json:"body"`
	}
	var open, restricted commentBean
	decode(call(admin, "POST", "/rest/api/3/issue/ISS-1/comment", `{"body":`+adf("Open to all")+`}`, 201), &open)
	decode(call(admin, "POST", "/rest/api/3/issue/ISS-1/comment", `{"body":`+adf("Leads only")+`,"visibility":{"type":"group","value":"issue-surface-leads"}}`, 201), &restricted)
	if _, err := strconv.ParseInt(open.ID, 10, 64); err != nil || open.Self != "https://zzira.test/rest/api/3/issue/"+jiraID("ISS-1")+"/comment/"+open.ID || open.Author.AccountID != admin {
		t.Fatalf("comment bean = %+v", open)
	}
	if restricted.Visibility == nil || restricted.Visibility.Identifier != group.GroupID || restricted.Visibility.Value != "issue-surface-leads" {
		t.Fatalf("restricted comment visibility = %+v", restricted.Visibility)
	}
	call(admin, "POST", "/rest/api/3/issue/ISS-1/comment", `{"body":`+adf("x")+`,"visibility":{"type":"group","value":"no-such-group"}}`, 400)
	call(admin, "POST", "/rest/api/3/issue/ISS-1/comment", `{}`, 400)
	var page struct {
		Total    int           `json:"total"`
		Comments []commentBean `json:"comments"`
	}
	decode(call(auditor, "GET", "/rest/api/3/issue/ISS-1/comment", "", 200), &page)
	if page.Total != 1 || page.Comments[0].ID != open.ID {
		t.Fatalf("comments a non-member sees = %+v", page)
	}
	decode(call(admin, "GET", "/rest/api/3/issue/ISS-1/comment?orderBy=-created&expand=renderedBody", "", 200), &page)
	if page.Total != 2 || page.Comments[0].ID != restricted.ID || page.Comments[0].RenderedBody == nil {
		t.Fatalf("newest-first comments = %+v", page)
	}
	call(admin, "GET", "/rest/api/3/issue/ISS-1/comment?orderBy=author", "", 400)
	call(auditor, "GET", "/rest/api/3/issue/ISS-1/comment/"+restricted.ID, "", 404)
	decode(call(member, "GET", "/rest/api/3/issue/ISS-1/comment", "", 200), &page)
	if page.Total != 1 {
		t.Fatalf("comments a member outside the group sees = %+v", page)
	}
	var edited commentBean
	decode(call(auditor, "PUT", "/rest/api/3/issue/ISS-1/comment/"+open.ID, `{"body":`+adf("Open to all, edited")+`}`, 200), &edited)
	if edited.UpdateAuthor.AccountID != auditor || !strings.Contains(string(edited.Body), "edited") {
		t.Fatalf("edited comment = %+v", edited)
	}
	var listed struct {
		Total  int           `json:"total"`
		Values []commentBean `json:"values"`
	}
	decode(call(auditor, "POST", "/rest/api/3/comment/list", `{"ids":[`+open.ID+`,`+restricted.ID+`]}`, 200), &listed)
	if listed.Total != 1 || listed.Values[0].ID != open.ID {
		t.Fatalf("comment list for a non-member = %+v", listed)
	}
	call(admin, "POST", "/rest/api/3/comment/list", `{"ids":[]}`, 400)
	commentProperty := "/rest/api/3/comment/" + open.ID + "/properties/review"
	call(admin, "PUT", commentProperty, `{"state":"pending"}`, 201)
	call(admin, "PUT", commentProperty, `{"state":"done"}`, 200)
	var keys struct {
		Keys []struct{ Key string } `json:"keys"`
	}
	decode(call(admin, "GET", "/rest/api/3/comment/"+open.ID+"/properties", "", 200), &keys)
	if len(keys.Keys) != 1 || keys.Keys[0].Key != "review" {
		t.Fatalf("comment property keys = %+v", keys)
	}
	var property struct {
		Value struct{ State string } `json:"value"`
	}
	decode(call(admin, "GET", commentProperty, "", 200), &property)
	if property.Value.State != "done" {
		t.Fatalf("comment property = %+v", property)
	}
	call(admin, "GET", "/rest/api/3/comment/not-a-number/properties", "", 400)
	call(admin, "DELETE", commentProperty, "", 204)
	call(admin, "GET", commentProperty, "", 404)
	call(admin, "DELETE", "/rest/api/3/issue/ISS-1/comment/"+restricted.ID, "", 204)
	call(admin, "GET", "/rest/api/3/issue/ISS-1/comment/"+restricted.ID, "", 404)

	// Watchers: others need Manage watchers, and removal names the person.
	call(admin, "POST", "/rest/api/3/issue/ISS-1/watchers", "", 204)
	call(admin, "POST", "/rest/api/3/issue/ISS-1/watchers", `"`+auditor+`"`, 204)
	call(admin, "POST", "/rest/api/3/issue/ISS-1/watchers", `"usr_nobody"`, 404)
	var watchers struct {
		WatchCount int                          `json:"watchCount"`
		IsWatching bool                         `json:"isWatching"`
		Watchers   []struct{ AccountID string } `json:"watchers"`
	}
	decode(call(admin, "GET", "/rest/api/3/issue/ISS-1/watchers", "", 200), &watchers)
	if watchers.WatchCount != 2 || !watchers.IsWatching || len(watchers.Watchers) != 2 {
		t.Fatalf("watchers = %+v", watchers)
	}
	call(admin, "DELETE", "/rest/api/3/issue/ISS-1/watchers", "", 400)
	call(admin, "DELETE", "/rest/api/3/issue/ISS-1/watchers?accountId="+auditor, "", 204)
	var watching struct {
		IssuesIsWatching map[string]bool `json:"issuesIsWatching"`
	}
	decode(call(admin, "POST", "/rest/api/3/issue/watching", `{"issueIds":["`+jiraID("ISS-1")+`","`+jiraID("ISS-2")+`","999999"]}`, 200), &watching)
	if !watching.IssuesIsWatching[jiraID("ISS-1")] || watching.IssuesIsWatching[jiraID("ISS-2")] || watching.IssuesIsWatching["999999"] {
		t.Fatalf("bulk watching = %+v", watching.IssuesIsWatching)
	}

	// Assignment follows Jira's rules for accountId, null and -1.
	call(admin, "PUT", "/rest/api/3/issue/ISS-1/assignee", `{"accountId":"`+auditor+`"}`, 204)
	if assignee := getIssue(admin, "ISS-1", "?fields=assignee").Fields["assignee"]; !strings.Contains(string(assignee), auditor) {
		t.Fatalf("assignee = %s", assignee)
	}
	call(admin, "PUT", "/rest/api/3/issue/ISS-1/assignee", `{"accountId":null}`, 204)
	if assignee, present := getIssue(admin, "ISS-1", "?fields=assignee").Fields["assignee"]; present && string(assignee) != "null" {
		t.Fatalf("assignee after unassigning = %s", assignee)
	}
	call(admin, "PUT", "/rest/api/3/issue/ISS-1/assignee", `{"accountId":"`+auditor+`","name":"aud"}`, 400)
	call(admin, "PUT", "/rest/api/3/issue/ISS-1/assignee", `{"accountId":"usr_nobody"}`, 400)
	call(admin, "PUT", "/rest/api/3/issue/ISS-1/assignee", `{"name":"aud"}`, 400)
	call(admin, "PUT", "/rest/api/3/issue/ISS-1/assignee", `{}`, 400)
	call(admin, "PUT", "/rest/api/3/issue/ISS-1/assignee", `{"accountId":"-1"}`, 204)

	// The issue projection: named fields, details, creator, names and properties.
	var created struct{ ID, Key string }
	decode(call(admin, "POST", "/rest/api/3/issue", `{"fields":{"project":{"key":"ISS"},"summary":"Alpha created through the API","issuetype":{"name":"Task"}},"properties":[{"key":"origin","value":{"source":"api"}}]}`, 201), &created)
	projected := getIssue(admin, created.Key, "?fields=summary,creator,created,comment&expand=names&properties=origin&updateHistory=true")
	if _, ok := projected.Fields["creator"]; !ok || !strings.Contains(string(projected.Fields["creator"]), admin) {
		t.Fatalf("creator = %s", projected.Fields["creator"])
	}
	if _, ok := projected.Fields["status"]; ok {
		t.Fatalf("unrequested field returned: %v", projected.Fields)
	}
	var full map[string]json.RawMessage
	decode(call(admin, "GET", "/rest/api/3/issue/"+created.Key+"?fields=summary&expand=names&properties=origin", "", 200), &full)
	if !strings.Contains(string(full["names"]), "summary") || !strings.Contains(string(full["properties"]), `"source":"api"`) {
		t.Fatalf("names and properties = %s %s", full["names"], full["properties"])
	}
	defaults := getIssue(admin, "ISS-1", "")
	for _, field := range []string{"comment", "issuelinks", "watches", "votes", "subtasks", "worklog", "created", "status"} {
		if _, ok := defaults.Fields[field]; !ok {
			t.Fatalf("GET issue omits %s: %v", field, defaults.Fields)
		}
	}

	// Bulk fetch and bulk create.
	var fetched struct {
		Issues []issueBean `json:"issues"`
	}
	decode(call(admin, "POST", "/rest/api/3/issue/bulkfetch", `{"issueIdsOrKeys":["ISS-1","`+jiraID("ISS-2")+`","NOPE-1"],"fields":["summary"]}`, 200), &fetched)
	if len(fetched.Issues) != 2 || fetched.Issues[1].Key != "ISS-2" {
		t.Fatalf("bulk fetch = %+v", fetched)
	}
	call(admin, "POST", "/rest/api/3/issue/bulkfetch", `{"issueIdsOrKeys":[]}`, 400)
	var bulk struct {
		Issues []struct{ Key string } `json:"issues"`
		Errors []struct {
			FailedElementNumber int `json:"failedElementNumber"`
			Status              int `json:"status"`
		} `json:"errors"`
	}
	decode(call(admin, "POST", "/rest/api/3/issue/bulk", `{"issueUpdates":[{"fields":{"project":{"key":"ISS"},"summary":"Bulk one","issuetype":{"name":"Task"}}},{"fields":{"project":{"key":"ISS"},"issuetype":{"name":"Task"}}}]}`, 201), &bulk)
	if len(bulk.Issues) != 1 || len(bulk.Errors) != 1 || bulk.Errors[0].FailedElementNumber != 1 || bulk.Errors[0].Status != 400 {
		t.Fatalf("bulk create = %+v", bulk)
	}
	call(admin, "POST", "/rest/api/3/issue/bulk", `{"issueUpdates":[{"fields":{}}]}`, 400)

	// Changelogs: paged, by id, and across issues filtered by field.
	call(admin, "PUT", "/rest/api/3/issue/ISS-2", `{"fields":{"summary":"Alpha blockers, triaged"}}`, 204)
	call(admin, "PUT", "/rest/api/3/issue/ISS-2", `{"fields":{"summary":"Alpha blockers, resolved"}}`, 204)
	var changelog struct {
		Total  int  `json:"total"`
		IsLast bool `json:"isLast"`
		Values []struct {
			ID    string                     `json:"id"`
			Items []struct{ FieldID string } `json:"items"`
		} `json:"values"`
	}
	decode(call(admin, "GET", "/rest/api/3/issue/ISS-2/changelog?maxResults=1", "", 200), &changelog)
	if changelog.Total != 2 || changelog.IsLast || len(changelog.Values) != 1 || changelog.Values[0].Items[0].FieldID != "summary" {
		t.Fatalf("paged changelog = %+v", changelog)
	}
	var byID struct {
		Histories []struct{ ID string } `json:"histories"`
	}
	decode(call(admin, "POST", "/rest/api/3/issue/ISS-2/changelog/list", `{"changelogIds":[`+changelog.Values[0].ID+`]}`, 200), &byID)
	if len(byID.Histories) != 1 || byID.Histories[0].ID != changelog.Values[0].ID {
		t.Fatalf("changelog by id = %+v", byID)
	}
	var bulkChangelog struct {
		IssueChangeLogs []struct {
			IssueID         string            `json:"issueId"`
			ChangeHistories []json.RawMessage `json:"changeHistories"`
		} `json:"issueChangeLogs"`
		NextPageToken string `json:"nextPageToken"`
	}
	decode(call(admin, "POST", "/rest/api/3/changelog/bulkfetch", `{"issueIdsOrKeys":["ISS-2"],"fieldIds":["summary"],"maxResults":1}`, 200), &bulkChangelog)
	if len(bulkChangelog.IssueChangeLogs) != 1 || bulkChangelog.IssueChangeLogs[0].IssueID != jiraID("ISS-2") || bulkChangelog.NextPageToken == "" {
		t.Fatalf("bulk changelog = %+v", bulkChangelog)
	}
	token := bulkChangelog.NextPageToken
	bulkChangelog.IssueChangeLogs, bulkChangelog.NextPageToken = nil, ""
	decode(call(admin, "POST", "/rest/api/3/changelog/bulkfetch", `{"issueIdsOrKeys":["ISS-2"],"fieldIds":["summary"],"maxResults":1,"nextPageToken":"`+token+`"}`, 200), &bulkChangelog)
	if len(bulkChangelog.IssueChangeLogs) != 1 || bulkChangelog.NextPageToken != "" {
		t.Fatalf("second bulk changelog page = %+v", bulkChangelog)
	}
	decode(call(admin, "POST", "/rest/api/3/changelog/bulkfetch", `{"issueIdsOrKeys":["ISS-2"],"fieldIds":["assignee"]}`, 200), &bulkChangelog)
	if len(bulkChangelog.IssueChangeLogs) != 0 {
		t.Fatalf("changelog filtered to an unchanged field = %+v", bulkChangelog)
	}

	// The picker offers viewed issues first, then the current search.
	var picker struct {
		Sections []struct {
			ID     string `json:"id"`
			Issues []struct {
				Key     string `json:"key"`
				KeyHTML string `json:"keyHtml"`
				Summary string `json:"summary"`
			} `json:"issues"`
		} `json:"sections"`
	}
	decode(call(admin, "GET", "/rest/api/3/issue/picker?query=alpha&currentIssueKey=ISS-2", "", 200), &picker)
	if len(picker.Sections) != 2 || len(picker.Sections[0].Issues) != 1 || picker.Sections[0].Issues[0].Key != created.Key {
		t.Fatalf("picker history = %+v", picker)
	}
	currentKeys := []string{}
	for _, issue := range picker.Sections[1].Issues {
		currentKeys = append(currentKeys, issue.Key)
		if !strings.Contains(issue.Summary, "<b>Alpha</b>") {
			t.Fatalf("picker highlight = %q", issue.Summary)
		}
	}
	if strings.Join(currentKeys, ",") != "ISS-1" {
		t.Fatalf("picker current search = %v", currentKeys)
	}

	// Notifications go to resolved recipients, never only to the sender.
	call(admin, "POST", "/rest/api/3/issue/ISS-1/notify", `{"subject":"Heads up","textBody":"Please review","to":{"users":[{"accountId":"`+auditor+`"}]}}`, 204)
	var queued int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE workspace_id=$1 AND recipient=$2 AND subject='Heads up'`, ws, auditor+"@example.test").Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("queued notification = %d, %v", queued, err)
	}
	call(admin, "POST", "/rest/api/3/issue/ISS-1/notify", `{"to":{"users":[{"accountId":"`+admin+`"}]}}`, 400)
	call(admin, "POST", "/rest/api/3/issue/ISS-3/notify", `{"to":{"assignee":true}}`, 400)
	call(admin, "POST", "/rest/api/3/issue/ISS-1/notify", `{"to":{"groups":[{"name":"no-such-group"}]}}`, 400)

	// Events and limit reports are for administrators.
	var events []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	decode(call(admin, "GET", "/rest/api/3/events", "", 200), &events)
	if len(events) != 17 || events[0].ID != 1 {
		t.Fatalf("events = %+v", events)
	}
	call(member, "GET", "/rest/api/3/events", "", 403)
	var limits struct {
		Limits map[string]int `json:"limits"`
	}
	decode(call(admin, "GET", "/rest/api/3/issue/limit/report", "", 200), &limits)
	if limits.Limits["comment"] != 5000 || limits.Limits["remoteIssueLinks"] != 2000 {
		t.Fatalf("limits = %+v", limits)
	}
	limits.Limits = nil
	decode(call(admin, "GET", "/rest/api/3/issue/limit/adf/report", "", 200), &limits)
	if len(limits.Limits) != 5 {
		t.Fatalf("ADF limits = %+v", limits)
	}
	call(admin, "GET", "/rest/api/3/issue/limit/adf/report?fieldType=summary_adf", "", 400)

	// Archiving: read-only, out of search, and restorable.
	var archival struct {
		NumberOfIssuesUpdated int `json:"numberOfIssuesUpdated"`
		Errors                map[string]struct {
			Count          int      `json:"count"`
			IssueIDsOrKeys []string `json:"issueIdsOrKeys"`
		} `json:"errors"`
	}
	decode(call(member, "PUT", "/rest/api/3/issue/archive", `{"issueIdsOrKeys":["ISS-3"]}`, 400), &archival)
	if archival.Errors["userDoesNotHavePermission"].Count != 1 {
		t.Fatalf("archive without permission = %+v", archival)
	}
	archival.Errors = nil
	decode(call(admin, "PUT", "/rest/api/3/issue/archive", `{"issueIdsOrKeys":["ISS-3","NOPE-9","ISS-7"]}`, 200), &archival)
	if archival.NumberOfIssuesUpdated != 1 || archival.Errors["issuesNotFound"].Count != 1 || archival.Errors["issueIsSubtask"].Count != 1 {
		t.Fatalf("archive = %+v", archival)
	}
	getIssue(admin, "ISS-3", "")
	call(admin, "PUT", "/rest/api/3/issue/ISS-3", `{"fields":{"summary":"Edited while archived"}}`, 400)
	if body := call(admin, "GET", "/rest/api/3/search?jql=project%3DISS&maxResults=100", "", 200); strings.Contains(string(body), `"key":"ISS-3"`) {
		t.Fatalf("archived issue in search: %s", body)
	}
	decode(call(admin, "PUT", "/rest/api/3/issue/unarchive", `{"issueIdsOrKeys":["ISS-3"]}`, 200), &archival)
	if archival.NumberOfIssuesUpdated != 1 {
		t.Fatalf("unarchive = %+v", archival)
	}
	call(admin, "PUT", "/rest/api/3/issue/ISS-3", `{"fields":{"summary":"Archive candidate, restored"}}`, 204)
	var taskURL string
	decode(call(admin, "POST", "/rest/api/3/issue/archive", `{"jql":"key = ISS-4"}`, 202), &taskURL)
	if !strings.HasPrefix(taskURL, "https://zzira.test/rest/api/3/task/") {
		t.Fatalf("archive task URL = %q", taskURL)
	}
	call(admin, "POST", "/rest/api/3/issue/archive", `{"jql":"key = ISS-4"}`, 412)
	drain()
	call(admin, "PUT", "/rest/api/3/issue/ISS-4", `{"fields":{"summary":"Edited while archived"}}`, 400)
	var export struct {
		TaskID string `json:"taskId"`
		Status string `json:"status"`
	}
	decode(call(admin, "PUT", "/rest/api/3/issues/archive/export", `{"projects":["ISS"]}`, 202), &export)
	call(admin, "PUT", "/rest/api/3/issues/archive/export", `{}`, 412)
	call(admin, "PUT", "/rest/api/3/issues/archive/export", `{"archivedDateRange":{"dateAfter":"yesterday"}}`, 400)
	drain()
	download := httptest.NewRequest("GET", "/secure/archived-issues-export/"+export.TaskID+".csv", nil)
	download.SetPathValue("file", export.TaskID+".csv")
	download.SetBasicAuth(admin+"@example.test", admin)
	csvResponse := httptest.NewRecorder()
	h.ArchivedIssuesExportFile(csvResponse, download)
	if csvResponse.Code != 200 || !strings.Contains(csvResponse.Body.String(), "ISS-4,") || strings.Contains(csvResponse.Body.String(), "ISS-3,") {
		t.Fatalf("archived export = %d %s", csvResponse.Code, csvResponse.Body.String())
	}

	// Redaction: verified by digest, positions kept, history scrubbed.
	call(admin, "PUT", "/rest/api/3/issue/ISS-5", `{"fields":{"summary":"Secret token abc123"}}`, 204)
	digest := func(text string) string {
		sum := sha256.Sum256([]byte(text))
		return base64.StdEncoding.EncodeToString(sum[:])
	}
	redaction := func(externalID, expected string) string {
		return `{"externalId":"` + externalID + `","reason":"Leaked credential","contentItem":{"entityType":"issuefieldvalue","entityId":"summary","id":"ISS-5"},"redactionPosition":{"expectedText":"` + expected + `","from":13,"to":19}}`
	}
	call(member, "POST", "/rest/api/3/redact", `{"redactions":[`+redaction("0b6f4c1e-8f35-4f2c-9a53-2a1f0c7d9e11", digest("abc123"))+`]}`, 403)
	call(admin, "POST", "/rest/api/3/redact", `{"redactions":[{"externalId":"not-a-uuid"}]}`, 400)
	var jobID string
	decode(call(admin, "POST", "/rest/api/3/redact", `{"redactions":[`+redaction("0b6f4c1e-8f35-4f2c-9a53-2a1f0c7d9e11", digest("abc123"))+`,`+redaction("6d0b2a7e-3c1f-4a8e-b8a2-5f7c9d3e1a22", digest("wrong!"))+`]}`, 202), &jobID)
	var status struct {
		JobStatus             string `json:"jobStatus"`
		BulkRedactionResponse *struct {
			Results []struct {
				ExternalID string `json:"externalId"`
				Successful bool   `json:"successful"`
			} `json:"results"`
		} `json:"bulkRedactionResponse"`
	}
	decode(call(admin, "GET", "/rest/api/3/redact/status/"+jobID, "", 200), &status)
	if status.JobStatus != "PENDING" {
		t.Fatalf("queued redaction status = %+v", status)
	}
	drain()
	decode(call(admin, "GET", "/rest/api/3/redact/status/"+jobID, "", 200), &status)
	if status.JobStatus != "COMPLETED" || status.BulkRedactionResponse == nil || len(status.BulkRedactionResponse.Results) != 2 ||
		!status.BulkRedactionResponse.Results[0].Successful || status.BulkRedactionResponse.Results[1].Successful {
		t.Fatalf("completed redaction status = %+v", status)
	}
	if summary := getIssue(admin, "ISS-5", "?fields=summary").Fields["summary"]; string(summary) != `"Secret token ██████"` {
		t.Fatalf("redacted summary = %s", summary)
	}
	if history := call(admin, "GET", "/rest/api/3/issue/ISS-5/changelog", "", 200); strings.Contains(string(history), "abc123") {
		t.Fatalf("changelog still holds the redacted text: %s", history)
	}
	call(admin, "GET", "/rest/api/3/redact/status/task_missing", "", 404)

	// Bulk issue properties run as tasks on issues the caller can edit.
	var bulkTask struct{ ID string }
	decode(call(admin, "POST", "/rest/api/3/issue/properties", `{"entitiesIds":[`+jiraID("ISS-1")+`,`+jiraID("ISS-2")+`],"properties":{"triage":{"level":1}}}`, 303), &bulkTask)
	drain()
	var issueProperty struct {
		Value struct{ Level int } `json:"value"`
	}
	decode(call(admin, "GET", "/rest/api/3/issue/ISS-2/properties/triage", "", 200), &issueProperty)
	if issueProperty.Value.Level != 1 {
		t.Fatalf("bulk-set property = %+v", issueProperty)
	}
	call(admin, "PUT", "/rest/api/3/issue/properties/triage", `{"value":{"level":2},"filter":{"entityIds":[`+jiraID("ISS-1")+`],"currentValue":{"level":1}}}`, 303)
	drain()
	decode(call(admin, "GET", "/rest/api/3/issue/ISS-1/properties/triage", "", 200), &issueProperty)
	if issueProperty.Value.Level != 2 {
		t.Fatalf("filtered property update = %+v", issueProperty)
	}
	call(admin, "DELETE", "/rest/api/3/issue/properties/triage", `{"entityIds":[`+jiraID("ISS-2")+`]}`, 303)
	drain()
	call(admin, "GET", "/rest/api/3/issue/ISS-2/properties/triage", "", 404)
	call(admin, "POST", "/rest/api/3/issue/properties/multi", `{"issues":[{"issueID":`+jiraID("ISS-1")+`,"properties":{"reviewed":true}}]}`, 303)
	drain()
	call(admin, "GET", "/rest/api/3/issue/ISS-1/properties/reviewed", "", 200)
	call(admin, "PUT", "/rest/api/3/issue/properties/triage", `{"expression":"issue.summary"}`, 400)
	call(admin, "POST", "/rest/api/3/issue/properties", `{"entitiesIds":[],"properties":{}}`, 400)

	// Issue panels are pinned only by an installed panel's module id.
	call(admin, "POST", "/rest/api/3/forge/panel/action/bulk/async", `{"moduleId":"ari:cloud:ecosystem::extension/app/env/static/panel","projectList":[{"projectIdOrKey":"ISS","action":"PIN"}]}`, 400)
	call(member, "POST", "/rest/api/3/forge/panel/action/bulk/async", `{"moduleId":"x","projectList":[]}`, 403)

	// Subtasks are deleted with their parent only when asked.
	call(admin, "DELETE", "/rest/api/3/issue/ISS-6", "", 400)
	call(admin, "DELETE", "/rest/api/3/issue/ISS-6?deleteSubtasks=true", "", 204)
	call(admin, "GET", "/rest/api/3/issue/ISS-7", "", 404)

	// Transition fields come only with their expansion.
	if body := call(admin, "GET", "/rest/api/3/issue/ISS-1/transitions", "", 200); strings.Contains(string(body), `"fields"`) {
		t.Fatalf("transitions carry fields without expand: %s", body)
	}
	if body := call(admin, "GET", "/rest/api/3/issue/ISS-1/transitions?expand=transitions.fields&transitionId=21", "", 200); !strings.Contains(string(body), `"fields"`) || !strings.Contains(string(body), `"id":"21"`) {
		t.Fatalf("expanded transition = %s", body)
	}

	// Worklogs carry numeric ids.
	var worklog struct{ ID, Self string }
	decode(call(admin, "POST", "/rest/api/3/issue/ISS-1/worklog", `{"timeSpentSeconds":600}`, 201), &worklog)
	if _, err := strconv.ParseInt(worklog.ID, 10, 64); err != nil || !strings.HasSuffix(worklog.Self, "/worklog/"+worklog.ID) {
		t.Fatalf("worklog bean = %+v", worklog)
	}
	call(admin, "GET", "/rest/api/3/issue/ISS-1/worklog/"+worklog.ID, "", 200)
}
