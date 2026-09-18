package api3

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestIssueMetadataContract pins issue types, priorities, resolutions, their
// schemes and issue type properties as a Jira client sees them: numeric ids,
// Jira's default sets, a site's changes kept to that site, and the resolution
// an issue gets when it is done.
func TestIssueMetadataContract(t *testing.T) {
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
	// Two sites, so a change in one can be shown not to reach the other.
	type site struct {
		id, admin, member string
		h                 *Handler
	}
	newSite := func(label string) site {
		t.Helper()
		s := site{id: store.NewID("ws"), admin: store.NewID("usr"), member: store.NewID("usr")}
		exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,$2)`, s.id, "Metadata "+label)
		for _, identity := range []struct{ id, role string }{{s.admin, "admin"}, {s.member, "member"}} {
			exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Metadata "+identity.id)
			exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, s.id, identity.id, identity.role)
			exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
		}
		s.h = &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: s.id, BaseURL: "https://zzira.test"}
		t.Cleanup(func() {
			for _, q := range []string{
				`DELETE FROM api_tasks WHERE workspace_id=$1`,
				`DELETE FROM universal_avatars WHERE workspace_id=$1`,
				`DELETE FROM issues WHERE workspace_id=$1`,
				`DELETE FROM project_issue_type_schemes WHERE workspace_id=$1`,
				`DELETE FROM project_priority_schemes WHERE workspace_id=$1`,
				`DELETE FROM projects WHERE workspace_id=$1`,
				`DELETE FROM actions WHERE workspace_id=$1`,
				`DELETE FROM memberships WHERE workspace_id=$1`,
				`DELETE FROM workspaces WHERE id=$1`,
			} {
				exec(q, s.id)
			}
			for _, id := range []string{s.admin, s.member} {
				exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
				exec(`DELETE FROM users WHERE id=$1`, id)
			}
		})
		return s
	}
	one, two := newSite("one"), newSite("two")

	call := func(s site, user, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(user+"@example.test", user)
		if method == http.MethodPost && strings.HasSuffix(path, "/avatar2") {
			request.Header.Set("X-Atlassian-Token", "no-check")
			request.Header.Set("Content-Type", "image/png")
		}
		response := httptest.NewRecorder()
		s.h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	decode := func(response *httptest.ResponseRecorder, into any) {
		t.Helper()
		if err := json.Unmarshal(response.Body.Bytes(), into); err != nil {
			t.Fatalf("decode: %v %s", err, response.Body.String())
		}
	}
	type bean struct {
		ID             string `json:"id"`
		Name           string `json:"name"`
		Description    string `json:"description"`
		Subtask        bool   `json:"subtask"`
		HierarchyLevel int    `json:"hierarchyLevel"`
		IsDefault      bool   `json:"isDefault"`
		StatusColor    string `json:"statusColor"`
		IconURL        string `json:"iconUrl"`
	}
	list := func(s site, user, path string) []bean {
		t.Helper()
		var out []bean
		decode(call(s, user, http.MethodGet, path, "", http.StatusOK), &out)
		return out
	}
	names := func(values []bean) []string {
		out := []string{}
		for _, v := range values {
			out = append(out, v.Name)
		}
		return out
	}
	byName := func(values []bean, name string) bean {
		t.Helper()
		for _, v := range values {
			if v.Name == name {
				return v
			}
		}
		t.Fatalf("%q not in %v", name, names(values))
		return bean{}
	}
	drain := func(s site) {
		t.Helper()
		runner := &store.APITaskRunner{Store: st}
		for i := 0; i < 5; i++ {
			if err := runner.DrainOnce(ctx, s.id); err != nil {
				t.Fatalf("drain: %v", err)
			}
		}
	}

	// ---- Jira's defaults, with Jira's numeric ids ----
	types := list(one, one.member, "/rest/api/3/issuetype")
	if got := strings.Join(names(types), ","); got != "Epic,Story,Task,Bug,Sub-task" {
		t.Fatalf("default issue types: %s", got)
	}
	if epic := byName(types, "Epic"); epic.ID != "10000" || epic.HierarchyLevel != 1 {
		t.Fatalf("epic: %+v", epic)
	}
	if sub := byName(types, "Sub-task"); !sub.Subtask || sub.HierarchyLevel != -1 {
		t.Fatalf("sub-task: %+v", sub)
	}
	for _, v := range types {
		if strings.HasPrefix(v.ID, "it_") {
			t.Fatalf("an internal id reached a client: %+v", v)
		}
	}
	priorities := list(one, one.member, "/rest/api/3/priority")
	if got := strings.Join(names(priorities), ","); got != "Highest,High,Medium,Low,Lowest" {
		t.Fatalf("default priorities: %s", got)
	}
	medium := byName(priorities, "Medium")
	if medium.ID != "3" || !medium.IsDefault || medium.StatusColor == "" {
		t.Fatalf("medium: %+v", medium)
	}
	resolutions := list(one, one.member, "/rest/api/3/resolution")
	if got := strings.Join(names(resolutions), ","); got != "Done,Won't Do,Duplicate,Cannot Reproduce" {
		t.Fatalf("default resolutions: %s", got)
	}
	if done := byName(resolutions, "Done"); done.ID != "10000" {
		t.Fatalf("done: %+v", done)
	}

	// ---- A site's change stays in that site ----
	call(one, one.member, http.MethodPut, "/rest/api/3/priority/3", `{"name":"Normal"}`, http.StatusForbidden)
	call(one, one.admin, http.MethodPut, "/rest/api/3/priority/3", `{"name":"Normal","statusColor":"#123456"}`, http.StatusNoContent)
	if renamed := byName(list(one, one.member, "/rest/api/3/priority"), "Normal"); renamed.ID != "3" || renamed.StatusColor != "#123456" {
		t.Fatalf("renamed in site one: %+v", renamed)
	}
	if other := byName(list(two, two.member, "/rest/api/3/priority"), "Medium"); other.ID != "3" {
		t.Fatalf("site two lost Medium: %+v", other)
	}
	call(one, one.admin, http.MethodPut, "/rest/api/3/priority/3", `{"name":"High"}`, http.StatusBadRequest)
	call(one, one.admin, http.MethodPut, "/rest/api/3/priority/3", `{}`, http.StatusBadRequest)
	call(one, one.admin, http.MethodPut, "/rest/api/3/priority/3", `{"statusColor":"red"}`, http.StatusBadRequest)

	// A priority made without an icon still has one to show.
	var iconless bean
	decode(call(one, one.admin, http.MethodPost, "/rest/api/3/priority", `{"name":"Pager","statusColor":"#ff5630"}`, http.StatusCreated), &iconless)
	if created := byName(list(one, one.member, "/rest/api/3/priority"), "Pager"); !strings.HasSuffix(created.IconURL, "/images/icons/priorities/medium.svg") {
		t.Fatalf("a priority created without an icon: %+v", created)
	}
	call(one, one.admin, http.MethodPut, "/rest/api/3/priority/"+iconless.ID, `{"name":"Pager duty"}`, http.StatusNoContent)
	if kept := byName(list(one, one.member, "/rest/api/3/priority"), "Pager duty"); !strings.HasSuffix(kept.IconURL, "/images/icons/priorities/medium.svg") {
		t.Fatalf("an edit lost the icon: %+v", kept)
	}

	// ---- Issue types ----
	var incident bean
	decode(call(one, one.admin, http.MethodPost, "/rest/api/3/issuetype", `{"name":"Incident","description":"Something broke","type":"standard"}`, http.StatusCreated), &incident)
	if incident.ID == "" || strings.HasPrefix(incident.ID, "it_") || incident.HierarchyLevel != 0 {
		t.Fatalf("created issue type: %+v", incident)
	}
	call(one, one.admin, http.MethodPost, "/rest/api/3/issuetype", `{"name":"incident"}`, http.StatusConflict)
	call(one, one.admin, http.MethodPost, "/rest/api/3/issuetype", `{"name":"Bad","type":"weird"}`, http.StatusBadRequest)
	// A type made in one site does not exist in the other.
	call(two, two.member, http.MethodGet, "/rest/api/3/issuetype/"+incident.ID, "", http.StatusNotFound)
	if got := names(list(two, two.member, "/rest/api/3/issuetype")); strings.Contains(strings.Join(got, ","), "Incident") {
		t.Fatalf("site two sees site one's issue type: %v", got)
	}
	var updatedType bean
	decode(call(one, one.admin, http.MethodPut, "/rest/api/3/issuetype/"+incident.ID, `{"description":"Service disruption"}`, http.StatusOK), &updatedType)
	if updatedType.Description != "Service disruption" || updatedType.Name != "Incident" {
		t.Fatalf("updated issue type: %+v", updatedType)
	}
	alternatives := list(one, one.member, "/rest/api/3/issuetype/"+incident.ID+"/alternatives")
	for _, v := range alternatives {
		if v.Subtask || v.ID == incident.ID {
			t.Fatalf("alternatives must be other standard types: %+v", alternatives)
		}
	}

	// Issue type properties: 201 when new, 200 when replaced.
	call(one, one.admin, http.MethodPut, "/rest/api/3/issuetype/"+incident.ID+"/properties/runbook", `{"url":"https://runbooks"}`, http.StatusCreated)
	call(one, one.admin, http.MethodPut, "/rest/api/3/issuetype/"+incident.ID+"/properties/runbook", `{"url":"https://runbooks/v2"}`, http.StatusOK)
	call(one, one.admin, http.MethodPut, "/rest/api/3/issuetype/"+incident.ID+"/properties/empty", ``, http.StatusBadRequest)
	var property struct {
		Key   string         `json:"key"`
		Value map[string]any `json:"value"`
	}
	decode(call(one, one.member, http.MethodGet, "/rest/api/3/issuetype/"+incident.ID+"/properties/runbook", "", http.StatusOK), &property)
	if property.Value["url"] != "https://runbooks/v2" {
		t.Fatalf("property: %+v", property)
	}
	var keys struct {
		Keys []struct {
			Key string `json:"key"`
		} `json:"keys"`
	}
	decode(call(one, one.member, http.MethodGet, "/rest/api/3/issuetype/"+incident.ID+"/properties", "", http.StatusOK), &keys)
	if len(keys.Keys) != 1 || keys.Keys[0].Key != "runbook" {
		t.Fatalf("property keys: %+v", keys)
	}
	call(one, one.member, http.MethodDelete, "/rest/api/3/issuetype/"+incident.ID+"/properties/runbook", "", http.StatusForbidden)
	call(one, one.admin, http.MethodDelete, "/rest/api/3/issuetype/"+incident.ID+"/properties/runbook", "", http.StatusNoContent)
	call(one, one.admin, http.MethodGet, "/rest/api/3/issuetype/"+incident.ID+"/properties/runbook", "", http.StatusNotFound)

	// ---- Projects, issues and the resolution of done work ----
	projectKey := fmt.Sprintf("M%07d", time.Now().UnixNano()%10000000)
	var project struct {
		ID json.RawMessage `json:"id"`
	}
	decode(call(one, one.admin, http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Metadata","projectTypeKey":"software","leadAccountId":"`+one.admin+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated), &project)
	projectID := strings.Trim(string(project.ID), `"`)
	// A project offers the issue types of its scheme, which is the default one.
	projectTypes := list(one, one.member, "/rest/api/3/issuetype/project?projectId="+projectID)
	if !strings.Contains(strings.Join(names(projectTypes), ","), "Incident") {
		t.Fatalf("new types join the default scheme, so the project offers them: %v", names(projectTypes))
	}
	if level := list(one, one.member, "/rest/api/3/issuetype/project?projectId="+projectID+"&level=-1"); len(level) != 1 || level[0].Name != "Sub-task" {
		t.Fatalf("level filter: %v", names(level))
	}

	createIssue := func(typeID, summary string) (key string) {
		t.Helper()
		var created struct {
			Key string `json:"key"`
		}
		decode(call(one, one.admin, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"summary":"`+summary+`","issuetype":{"id":"`+typeID+`"}}}`, http.StatusCreated), &created)
		return created.Key
	}
	type issueFields struct {
		Fields struct {
			IssueType      bean            `json:"issuetype"`
			Priority       *bean           `json:"priority"`
			Resolution     *bean           `json:"resolution"`
			ResolutionDate *string         `json:"resolutiondate"`
			Raw            json.RawMessage `json:"-"`
		} `json:"fields"`
	}
	readIssue := func(key string) issueFields {
		t.Helper()
		var out issueFields
		decode(call(one, one.admin, http.MethodGet, "/rest/api/3/issue/"+key, "", http.StatusOK), &out)
		return out
	}
	taskKey := createIssue(byName(types, "Task").ID, "Close the loop")
	fresh := readIssue(taskKey)
	if fresh.Fields.IssueType.ID != byName(types, "Task").ID {
		t.Fatalf("issue type on the issue is the numeric id: %+v", fresh.Fields.IssueType)
	}
	if fresh.Fields.Resolution != nil || fresh.Fields.ResolutionDate != nil {
		t.Fatalf("a new issue is unresolved: %+v", fresh.Fields)
	}
	search := func(jql string) []string {
		t.Helper()
		var page struct {
			Issues []struct {
				Key string `json:"key"`
			} `json:"issues"`
		}
		decode(call(one, one.admin, http.MethodGet, "/rest/api/3/search/jql?fields=summary&jql="+url.QueryEscape(jql), "", http.StatusOK), &page)
		out := []string{}
		for _, issue := range page.Issues {
			out = append(out, issue.Key)
		}
		return out
	}
	if got := search(`project = ` + projectKey + ` AND resolution = Unresolved`); len(got) != 1 || got[0] != taskKey {
		t.Fatalf("resolution = Unresolved: %v", got)
	}
	// Moving the issue to a done status resolves it with the site's default.
	var transitions struct {
		Transitions []struct {
			ID string `json:"id"`
			To struct {
				StatusCategory struct {
					Key string `json:"key"`
				} `json:"statusCategory"`
			} `json:"to"`
		} `json:"transitions"`
	}
	decode(call(one, one.admin, http.MethodGet, "/rest/api/3/issue/"+taskKey+"/transitions", "", http.StatusOK), &transitions)
	doneTransition, openTransition := "", ""
	for _, tr := range transitions.Transitions {
		if tr.To.StatusCategory.Key == "done" && doneTransition == "" {
			doneTransition = tr.ID
		}
	}
	if doneTransition == "" {
		t.Fatalf("no transition to a done status: %+v", transitions)
	}
	call(one, one.admin, http.MethodPost, "/rest/api/3/issue/"+taskKey+"/transitions", `{"transition":{"id":"`+doneTransition+`"}}`, http.StatusNoContent)
	resolved := readIssue(taskKey)
	if resolved.Fields.Resolution == nil || resolved.Fields.Resolution.Name != "Done" || resolved.Fields.Resolution.ID != "10000" || resolved.Fields.ResolutionDate == nil {
		t.Fatalf("a done issue carries the default resolution and its date: %+v", resolved.Fields)
	}
	if got := search(`project = ` + projectKey + ` AND resolution = Unresolved`); len(got) != 0 {
		t.Fatalf("a resolved issue is not Unresolved: %v", got)
	}
	if got := search(`project = ` + projectKey + ` AND resolution = Done`); len(got) != 1 {
		t.Fatalf("resolution = Done: %v", got)
	}
	if got := search(`project = ` + projectKey + ` AND resolutiondate >= "2000-01-01"`); len(got) != 1 {
		t.Fatalf("resolutiondate: %v", got)
	}
	// Leaving the done status unresolves it again.
	decode(call(one, one.admin, http.MethodGet, "/rest/api/3/issue/"+taskKey+"/transitions", "", http.StatusOK), &transitions)
	for _, tr := range transitions.Transitions {
		if tr.To.StatusCategory.Key != "done" && openTransition == "" {
			openTransition = tr.ID
		}
	}
	if openTransition != "" {
		call(one, one.admin, http.MethodPost, "/rest/api/3/issue/"+taskKey+"/transitions", `{"transition":{"id":"`+openTransition+`"}}`, http.StatusNoContent)
		if reopened := readIssue(taskKey); reopened.Fields.Resolution != nil || reopened.Fields.ResolutionDate != nil {
			t.Fatalf("a reopened issue is unresolved: %+v", reopened.Fields)
		}
	}

	// ---- Resolutions ----
	var fixed struct {
		ID string `json:"id"`
	}
	decode(call(one, one.admin, http.MethodPost, "/rest/api/3/resolution", `{"name":"Fixed","description":"A fix was shipped"}`, http.StatusCreated), &fixed)
	call(one, one.admin, http.MethodPost, "/rest/api/3/resolution", `{"name":"fixed"}`, http.StatusBadRequest)
	call(one, one.admin, http.MethodPut, "/rest/api/3/resolution/"+fixed.ID, `{"name":"Fixed in release"}`, http.StatusNoContent)
	call(one, one.admin, http.MethodPut, "/rest/api/3/resolution/default", `{"id":"`+fixed.ID+`"}`, http.StatusNoContent)
	var resolutionPage struct {
		Total  int `json:"total"`
		Values []struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Default bool   `json:"default"`
		} `json:"values"`
	}
	decode(call(one, one.member, http.MethodGet, "/rest/api/3/resolution/search?onlyDefault=true", "", http.StatusOK), &resolutionPage)
	if resolutionPage.Total != 1 || resolutionPage.Values[0].ID != fixed.ID || !resolutionPage.Values[0].Default {
		t.Fatalf("default resolution search: %+v", resolutionPage)
	}
	call(one, one.admin, http.MethodPut, "/rest/api/3/resolution/move", `{"ids":["`+fixed.ID+`"],"position":"First"}`, http.StatusNoContent)
	if first := list(one, one.member, "/rest/api/3/resolution")[0]; first.ID != fixed.ID {
		t.Fatalf("moved first: %+v", first)
	}
	call(one, one.admin, http.MethodPut, "/rest/api/3/resolution/move", `{"ids":["`+fixed.ID+`"]}`, http.StatusBadRequest)
	// Deleting needs a replacement; the resolved issue moves to it rather than
	// becoming unresolved.
	call(one, one.admin, http.MethodDelete, "/rest/api/3/resolution/10000", "", http.StatusBadRequest)
	call(one, one.admin, http.MethodDelete, "/rest/api/3/resolution/10000?replaceWith=10000", "", http.StatusBadRequest)
	if openTransition != "" {
		call(one, one.admin, http.MethodPost, "/rest/api/3/issue/"+taskKey+"/transitions", `{"transition":{"id":"`+doneTransition+`"}}`, http.StatusNoContent)
	}
	deletion := call(one, one.admin, http.MethodDelete, "/rest/api/3/resolution/10001?replaceWith="+fixed.ID, "", http.StatusSeeOther)
	if !strings.Contains(deletion.Header().Get("Location"), "/rest/api/3/task/") {
		t.Fatalf("resolution delete must point at its task: %v", deletion.Header())
	}
	drain(one)
	if got := names(list(one, one.member, "/rest/api/3/resolution")); strings.Contains(strings.Join(got, ","), "Won't Do") {
		t.Fatalf("Won't Do was deleted: %v", got)
	}
	if got := names(list(two, two.member, "/rest/api/3/resolution")); !strings.Contains(strings.Join(got, ","), "Won't Do") {
		t.Fatalf("deleting in site one removed it from site two: %v", got)
	}

	// ---- Priorities ----
	var urgent struct {
		ID string `json:"id"`
	}
	decode(call(one, one.admin, http.MethodPost, "/rest/api/3/priority", `{"name":"Urgent","statusColor":"#ff0000","description":"Drop everything"}`, http.StatusCreated), &urgent)
	call(one, one.admin, http.MethodPost, "/rest/api/3/priority", `{"name":"No colour"}`, http.StatusBadRequest)
	call(one, one.admin, http.MethodPut, "/rest/api/3/priority/move", `{"ids":["`+urgent.ID+`"],"position":"First"}`, http.StatusNoContent)
	if first := list(one, one.member, "/rest/api/3/priority")[0]; first.ID != urgent.ID {
		t.Fatalf("urgent moved first: %+v", first)
	}
	call(one, one.admin, http.MethodPut, "/rest/api/3/priority/move", `{"ids":["`+urgent.ID+`"],"after":"`+urgent.ID+`"}`, http.StatusBadRequest)
	var prioritySearch struct {
		Total  int    `json:"total"`
		Values []bean `json:"values"`
	}
	decode(call(one, one.member, http.MethodGet, "/rest/api/3/priority/search?priorityName=urg", "", http.StatusOK), &prioritySearch)
	if prioritySearch.Total != 1 || prioritySearch.Values[0].ID != urgent.ID {
		t.Fatalf("priority search by name: %+v", prioritySearch)
	}
	// The default priority cannot be deleted; another can, through its task.
	call(one, one.admin, http.MethodDelete, "/rest/api/3/priority/3", "", http.StatusBadRequest)
	call(one, one.admin, http.MethodDelete, "/rest/api/3/priority/"+urgent.ID, "", http.StatusSeeOther)
	call(one, one.admin, http.MethodDelete, "/rest/api/3/priority/"+urgent.ID, "", http.StatusConflict)
	drain(one)
	call(one, one.member, http.MethodGet, "/rest/api/3/priority/"+urgent.ID, "", http.StatusNotFound)

	// ---- Issue type schemes ----
	var created struct {
		IssueTypeSchemeID string `json:"issueTypeSchemeId"`
	}
	call(one, one.member, http.MethodGet, "/rest/api/3/issuetypescheme", "", http.StatusForbidden)
	decode(call(one, one.admin, http.MethodPost, "/rest/api/3/issuetypescheme",
		`{"name":"Ops","issueTypeIds":["`+incident.ID+`","`+byName(types, "Bug").ID+`"],"defaultIssueTypeId":"`+incident.ID+`"}`, http.StatusCreated), &created)
	call(one, one.admin, http.MethodPost, "/rest/api/3/issuetypescheme", `{"name":"ops","issueTypeIds":["`+incident.ID+`"]}`, http.StatusConflict)
	call(one, one.admin, http.MethodPost, "/rest/api/3/issuetypescheme", `{"name":"Bad default","issueTypeIds":["`+incident.ID+`"],"defaultIssueTypeId":"10000"}`, http.StatusBadRequest)
	// The project has a Task issue, which the Ops scheme does not offer.
	call(one, one.admin, http.MethodPut, "/rest/api/3/issuetypescheme/project", `{"issueTypeSchemeId":"`+created.IssueTypeSchemeID+`","projectId":"`+projectID+`"}`, http.StatusBadRequest)
	call(one, one.admin, http.MethodPut, "/rest/api/3/issuetypescheme/"+created.IssueTypeSchemeID+"/issuetype", `{"issueTypeIds":["`+byName(types, "Task").ID+`"]}`, http.StatusNoContent)
	call(one, one.admin, http.MethodPut, "/rest/api/3/issuetypescheme/"+created.IssueTypeSchemeID+"/issuetype", `{"issueTypeIds":["`+byName(types, "Task").ID+`"]}`, http.StatusBadRequest)
	call(one, one.admin, http.MethodPut, "/rest/api/3/issuetypescheme/project", `{"issueTypeSchemeId":"`+created.IssueTypeSchemeID+`","projectId":"`+projectID+`"}`, http.StatusNoContent)
	if offered := names(list(one, one.member, "/rest/api/3/issuetype/project?projectId="+projectID)); strings.Join(offered, ",") != "Incident,Bug,Task" {
		t.Fatalf("project offers its scheme's types in order: %v", offered)
	}
	call(one, one.admin, http.MethodPut, "/rest/api/3/issuetypescheme/"+created.IssueTypeSchemeID+"/issuetype/move", `{"issueTypeIds":["`+byName(types, "Task").ID+`"],"position":"First"}`, http.StatusNoContent)
	if offered := names(list(one, one.member, "/rest/api/3/issuetype/project?projectId="+projectID)); offered[0] != "Task" {
		t.Fatalf("moved within the scheme: %v", offered)
	}
	// Task is used by an issue in a project on this scheme, so it stays.
	call(one, one.admin, http.MethodDelete, "/rest/api/3/issuetypescheme/"+created.IssueTypeSchemeID+"/issuetype/"+byName(types, "Task").ID, "", http.StatusBadRequest)
	call(one, one.admin, http.MethodDelete, "/rest/api/3/issuetypescheme/"+created.IssueTypeSchemeID+"/issuetype/"+byName(types, "Bug").ID, "", http.StatusNoContent)
	// A scheme a project uses, and the default scheme, cannot be deleted.
	call(one, one.admin, http.MethodDelete, "/rest/api/3/issuetypescheme/"+created.IssueTypeSchemeID, "", http.StatusBadRequest)
	var schemePage struct {
		Total  int `json:"total"`
		Values []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			IsDefault bool   `json:"isDefault"`
		} `json:"values"`
	}
	decode(call(one, one.admin, http.MethodGet, "/rest/api/3/issuetypescheme?orderBy=name", "", http.StatusOK), &schemePage)
	if schemePage.Total != 2 || schemePage.Values[0].Name != "Default Issue Type Scheme" || !schemePage.Values[0].IsDefault {
		t.Fatalf("issue type schemes: %+v", schemePage)
	}
	call(one, one.admin, http.MethodDelete, "/rest/api/3/issuetypescheme/"+schemePage.Values[0].ID, "", http.StatusBadRequest)
	var mappingPage struct {
		Total int `json:"total"`
	}
	decode(call(one, one.admin, http.MethodGet, "/rest/api/3/issuetypescheme/mapping?issueTypeSchemeId="+created.IssueTypeSchemeID, "", http.StatusOK), &mappingPage)
	if mappingPage.Total != 2 {
		t.Fatalf("scheme mappings: %+v", mappingPage)
	}
	var projectSchemes struct {
		Values []struct {
			ProjectIDs []string `json:"projectIds"`
		} `json:"values"`
	}
	decode(call(one, one.admin, http.MethodGet, "/rest/api/3/issuetypescheme/project?projectId="+projectID, "", http.StatusOK), &projectSchemes)
	if len(projectSchemes.Values) != 1 || len(projectSchemes.Values[0].ProjectIDs) != 1 {
		t.Fatalf("project schemes: %+v", projectSchemes)
	}

	// Deleting an issue type in use needs an alternative, which takes its issues.
	incidentKey := createIssue(incident.ID, "Outage")
	call(one, one.admin, http.MethodDelete, "/rest/api/3/issuetype/"+incident.ID, "", http.StatusNotFound)
	call(one, one.admin, http.MethodDelete, "/rest/api/3/issuetype/"+incident.ID+"?alternativeIssueTypeId="+incident.ID, "", http.StatusConflict)
	call(one, one.admin, http.MethodDelete, "/rest/api/3/issuetype/"+incident.ID+"?alternativeIssueTypeId="+byName(types, "Task").ID, "", http.StatusNoContent)
	if moved := readIssue(incidentKey); moved.Fields.IssueType.Name != "Task" {
		t.Fatalf("the outage moved to the alternative: %+v", moved.Fields.IssueType)
	}

	// ---- Priority schemes ----
	priorities = list(one, one.member, "/rest/api/3/priority")
	high, medium, low := byName(priorities, "High"), byName(priorities, "Normal"), byName(priorities, "Low")
	call(one, one.admin, http.MethodPut, "/rest/api/3/issue/"+taskKey, `{"fields":{"priority":{"id":"`+high.ID+`"}}}`, http.StatusNoContent)
	var priorityScheme struct {
		ID json.RawMessage `json:"id"`
	}
	// The project's work items use High and the site default, which the new
	// scheme lacks: each needs a mapping.
	call(one, one.admin, http.MethodPost, "/rest/api/3/priorityscheme",
		`{"name":"Lean","defaultPriorityId":`+low.ID+`,"priorityIds":[`+low.ID+`],"projectIds":[`+projectID+`]}`, http.StatusBadRequest)
	decode(call(one, one.admin, http.MethodPost, "/rest/api/3/priorityscheme",
		`{"name":"Lean","defaultPriorityId":`+low.ID+`,"priorityIds":[`+low.ID+`],"projectIds":[`+projectID+`],"mappings":{"in":{"`+high.ID+`":`+low.ID+`,"`+medium.ID+`":`+low.ID+`}}}`, http.StatusCreated), &priorityScheme)
	schemeID := strings.Trim(string(priorityScheme.ID), `"`)
	if mapped := readIssue(taskKey); mapped.Fields.Priority == nil || mapped.Fields.Priority.ID != low.ID {
		t.Fatalf("the mapping moved the issue to Low: %+v", mapped.Fields.Priority)
	}
	var schemePriorities struct {
		Total int `json:"total"`
	}
	decode(call(one, one.admin, http.MethodGet, "/rest/api/3/priorityscheme/"+schemeID+"/priorities", "", http.StatusOK), &schemePriorities)
	if schemePriorities.Total != 1 {
		t.Fatalf("scheme priorities: %+v", schemePriorities)
	}
	var available struct {
		Total int `json:"total"`
	}
	decode(call(one, one.admin, http.MethodGet, "/rest/api/3/priorityscheme/priorities/available?schemeId="+schemeID, "", http.StatusOK), &available)
	if available.Total != len(priorities)-1 {
		t.Fatalf("available priorities: %d of %d", available.Total, len(priorities))
	}
	var schemeProjects struct {
		Total int `json:"total"`
	}
	decode(call(one, one.admin, http.MethodGet, "/rest/api/3/priorityscheme/"+schemeID+"/projects", "", http.StatusOK), &schemeProjects)
	if schemeProjects.Total != 1 {
		t.Fatalf("scheme projects: %+v", schemeProjects)
	}
	call(one, one.admin, http.MethodPut, "/rest/api/3/priorityscheme/"+schemeID, `{"description":"Fewer choices"}`, http.StatusAccepted)
	call(one, one.admin, http.MethodDelete, "/rest/api/3/priorityscheme/"+schemeID, "", http.StatusBadRequest)
	call(one, one.admin, http.MethodPut, "/rest/api/3/priorityscheme/"+schemeID, `{"projects":{"remove":{"ids":[`+projectID+`]}}}`, http.StatusAccepted)
	call(one, one.admin, http.MethodDelete, "/rest/api/3/priorityscheme/"+schemeID, "", http.StatusNoContent)
	var suggestions struct {
		Total int `json:"total"`
	}
	var prioritySchemes struct {
		Values []struct {
			ID        string `json:"id"`
			IsDefault bool   `json:"isDefault"`
		} `json:"values"`
	}
	decode(call(one, one.admin, http.MethodGet, "/rest/api/3/priorityscheme?onlyDefault=true", "", http.StatusOK), &prioritySchemes)
	if len(prioritySchemes.Values) != 1 || !prioritySchemes.Values[0].IsDefault {
		t.Fatalf("default priority scheme: %+v", prioritySchemes)
	}
	decode(call(one, one.admin, http.MethodPost, "/rest/api/3/priorityscheme/mappings",
		`{"schemeId":`+prioritySchemes.Values[0].ID+`,"priorities":{"remove":[`+low.ID+`]}}`, http.StatusOK), &suggestions)
	if suggestions.Total != 1 {
		t.Fatalf("removing Low from the default scheme strands the Low issue: %+v", suggestions)
	}
}
