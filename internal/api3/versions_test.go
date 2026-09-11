package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestVersionLifecycleMembershipAndVisibility(t *testing.T) {
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
	ws, actor, member := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Version test')`, ws)
	for _, user := range []string{actor, member} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Version user')`, user, user+"@example.test")
		role := "member"
		if user == actor {
			role = "admin"
		}
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, user, role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, user, store.HashToken(user))
	}
	t.Cleanup(func() {
		for _, sql := range []string{`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, `DELETE FROM projects WHERE workspace_id=$1`, `DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`} {
			exec(sql, ws)
		}
		for _, user := range []string{actor, member} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path string, body any, want int) map[string]any {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.SetBasicAuth(user+"@example.test", user)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		out := map[string]any{}
		if want != 204 && len(rec.Body.Bytes()) > 0 && rec.Body.Bytes()[0] == '{' {
			if err = json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	callList := func(user, method, path string, body any, want int) []any {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.SetBasicAuth(user+"@example.test", user)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
		}
		out := []any{}
		if err = json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, rec.Body.String())
		}
		return out
	}
	for _, key := range []string{"VR", "OT"} {
		call(actor, "POST", "/rest/api/3/project", map[string]any{"key": key, "name": key, "projectTypeKey": "software", "leadAccountId": actor}, 201)
	}
	project, err := st.ProjectByIDOrKey(ctx, ws, "VR")
	if err != nil {
		t.Fatal(err)
	}
	create := func(name, project string) string {
		t.Helper()
		v := call(actor, "POST", "/rest/api/3/version", map[string]any{"project": project, "name": name, "startDate": "2026-09-01", "releaseDate": "2026-09-30"}, 201)
		return v["id"].(string)
	}
	first, second, other := create("1.0", "VR"), create("2.0", "VR"), create("Other", "OT")
	endpoint := "/rest/api/3/version/" + first
	call(member, "POST", "/rest/api/3/version", map[string]any{"project": "VR", "name": "Denied"}, 403)
	call(member, "PUT", endpoint, map[string]any{"name": "Denied"}, 403)
	call(actor, "POST", "/rest/api/3/version", map[string]any{"project": "VR", "name": "1.0"}, 400)
	call(actor, "PUT", endpoint, map[string]any{"releaseDate": "2026-02-30"}, 400)
	call(actor, "PUT", endpoint, map[string]any{"driver": "unsupported"}, 400)
	page := call(member, "GET", "/rest/api/3/project/VR/version?maxResults=1&orderBy=-name", nil, 200)
	if page["total"] != float64(2) || page["isLast"] != false || page["nextPage"] == nil {
		t.Fatal(page)
	}
	next := call(member, "GET", page["nextPage"].(string), nil, 200)
	if next["isLast"] != true {
		t.Fatal(next)
	}
	call(member, "GET", "/rest/api/3/project/VR/version?status=unknown", nil, 400)
	call(member, "GET", "/rest/api/3/project/VR/version?expand=operations", nil, 400)
	call(member, "GET", "/rest/api/3/project/VR/versions", nil, 200)
	issue := call(member, "POST", "/rest/api/3/issue", map[string]any{"fields": map[string]any{"project": map[string]string{"key": "VR"}, "summary": "Ship it", "issuetype": map[string]string{"name": "Task"}, "fixVersions": []map[string]string{{"name": "1.0"}}, "versions": []map[string]string{{"id": first}}}}, 201)
	issueURL := "/rest/api/3/issue/" + issue["key"].(string)
	versions := func(field string) []any {
		return call(member, "GET", issueURL, nil, 200)["fields"].(map[string]any)[field].([]any)
	}
	if versions("fixVersions")[0].(map[string]any)["id"] != first {
		t.Fatal("name resolution lost")
	}
	for _, search := range []struct {
		user string
		jql  string
	}{
		{member, `fixVersion IN unreleasedVersions(VR)`},
		{member, `affectedVersion = earliestUnreleasedVersion(VR)`},
		{member, `issue IN updatedBy(currentUser())`},
		{actor, `project IN projectsLeadByUser()`},
	} {
		result := call(search.user, "GET", "/rest/api/3/search?jql="+url.QueryEscape(search.jql), nil, 200)
		if result["total"] != float64(1) {
			t.Fatalf("JQL %q total = %v", search.jql, result["total"])
		}
	}
	call(member, "PUT", issueURL, map[string]any{"fields": map[string]any{"summary": "Should roll back", "fixVersions": []map[string]string{{"id": other}}}}, 400)
	if call(member, "GET", issueURL, nil, 200)["fields"].(map[string]any)["summary"] != "Ship it" {
		t.Fatal("invalid membership partially changed issue")
	}
	call(member, "PUT", issueURL, map[string]any{"fields": map[string]any{"fixVersions": nil}}, 400)
	call(member, "PUT", issueURL, map[string]any{"update": map[string]any{"fixVersions": []any{map[string]any{"add": map[string]string{"id": second}}}}}, 204)
	if len(versions("fixVersions")) != 2 {
		t.Fatal("add lost existing membership")
	}
	call(member, "PUT", issueURL, map[string]any{"update": map[string]any{"fixVersions": []any{map[string]any{"remove": map[string]string{"id": second}}}}}, 204)
	call(member, "PUT", issueURL, map[string]any{"fields": map[string]any{"fixVersions": []any{}}, "update": map[string]any{"fixVersions": []any{map[string]any{"set": []any{}}}}}, 400)
	for _, jql := range []string{"fixVersion = " + first, "fixVersion = \"1.0\"", "affectedVersion IN (" + first + ")"} {
		result := call(member, "GET", "/rest/api/3/search/jql?jql="+url.QueryEscape(jql), nil, 200)
		if len(result["issues"].([]any)) != 1 {
			t.Fatal(result)
		}
	}
	hidden := call(actor, "POST", "/rest/api/3/issue", map[string]any{"fields": map[string]any{"project": map[string]string{"key": "VR"}, "summary": "Private work", "issuetype": map[string]string{"name": "Task"}, "fixVersions": []map[string]string{{"id": first}}}}, 201)
	hiddenIssue, err := st.IssueByIDOrKey(ctx, ws, hidden["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE issues SET security_level_id='private-test' WHERE id=$1`, hiddenIssue.ID)
	counts := call(member, "GET", endpoint+"/relatedIssueCounts", nil, 200)
	if counts["issuesFixedCount"] != float64(1) || counts["issuesAffectedCount"] != float64(1) {
		t.Fatal(counts)
	}
	unresolved := call(member, "GET", endpoint+"/unresolvedIssueCount", nil, 200)
	if unresolved["issuesCount"] != float64(1) || unresolved["issuesUnresolvedCount"] != float64(1) {
		t.Fatal(unresolved)
	}
	meta := call(member, "GET", issueURL+"/editmeta", nil, 200)
	if meta["fields"].(map[string]any)["fixVersions"].(map[string]any)["schema"].(map[string]any)["items"] != "version" {
		t.Fatal(meta)
	}
	expanded := call(member, "GET", endpoint+"?expand=issuesstatus", nil, 200)
	if expanded["issuesStatusForFixVersion"].(map[string]any)["toDo"] != float64(1) {
		t.Fatal(expanded)
	}
	// ---- ordering and related work ----

	secondID := second
	order := func() []string {
		t.Helper()
		listed := callList(actor, "GET", "/rest/api/3/project/VR/versions", nil, 200)
		names := []string{}
		for _, entry := range listed {
			names = append(names, fmt.Sprint(entry.(map[string]any)["name"]))
		}
		return names
	}
	if strings.Join(order(), ",") != "1.0,2.0" {
		t.Fatalf("order=%v", order())
	}
	call(actor, "POST", "/rest/api/3/version/"+secondID+"/move", map[string]any{"position": "First"}, 200)
	if strings.Join(order(), ",") != "2.0,1.0" {
		t.Fatalf("order=%v", order())
	}
	call(actor, "POST", "/rest/api/3/version/"+secondID+"/move", map[string]any{"position": "Later"}, 200)
	if strings.Join(order(), ",") != "1.0,2.0" {
		t.Fatalf("order=%v", order())
	}
	call(actor, "POST", "/rest/api/3/version/"+secondID+"/move", map[string]any{"after": "https://zzira.test/rest/api/3/version/" + first}, 200)
	if strings.Join(order(), ",") != "1.0,2.0" {
		t.Fatalf("order=%v", order())
	}
	call(actor, "POST", "/rest/api/3/version/"+secondID+"/move", map[string]any{}, 400)
	call(actor, "POST", "/rest/api/3/version/"+secondID+"/move", map[string]any{"after": "9999"}, 400)

	relatedPath := endpoint + "/relatedwork"
	call(actor, "POST", relatedPath, map[string]any{"category": "  "}, 400)
	call(actor, "POST", relatedPath, map[string]any{"category": "Design", "url": "not-a-url"}, 400)
	work := call(actor, "POST", relatedPath, map[string]any{"category": "Design", "title": "Release brief", "url": "https://design.example/brief"}, 201)
	workID := fmt.Sprint(work["relatedWorkId"])
	if len(callList(actor, "GET", relatedPath, nil, 200)) != 1 {
		t.Fatal("related work should be listed")
	}
	call(actor, "PUT", relatedPath, map[string]any{"relatedWorkId": workID, "category": "Design", "title": "Updated brief", "url": "https://design.example/brief"}, 200)
	updated := callList(actor, "GET", relatedPath, nil, 200)
	if fmt.Sprint(updated[0].(map[string]any)["title"]) != "Updated brief" {
		t.Fatalf("related work=%v", updated)
	}
	call(actor, "PUT", relatedPath, map[string]any{"relatedWorkId": "9999", "category": "Design"}, 404)
	call(actor, "DELETE", relatedPath+"/"+workID, nil, 204)
	call(actor, "DELETE", relatedPath+"/"+workID, nil, 404)
	if len(callList(actor, "GET", relatedPath, nil, 200)) != 0 {
		t.Fatal("related work should be gone")
	}
	call(actor, "PUT", endpoint, map[string]any{"name": "1.0 final", "released": true, "releaseDate": "2026-09-05"}, 200)
	if versions("fixVersions")[0].(map[string]any)["name"] != "1.0 final" || versions("fixVersions")[0].(map[string]any)["released"] != true {
		t.Fatal("issue snapshot was not refreshed")
	}
	actions, err := st.ActionsSince(ctx, ws, member, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range actions {
		if action.EntityID == hiddenIssue.ID {
			t.Fatal("private issue leaked through version refresh action")
		}
	}
	call(member, "DELETE", endpoint, nil, 403)
	call(actor, "DELETE", endpoint+"?moveFixIssuesTo="+other, nil, 400)
	call(actor, "DELETE", endpoint+"?moveFixIssuesTo="+first, nil, 400)
	call(actor, "GET", endpoint, nil, 200)
	call(actor, "POST", endpoint+"/removeAndSwap", map[string]any{"moveFixIssuesTo": json.Number(second)}, 204)
	if len(versions("fixVersions")) != 1 || versions("fixVersions")[0].(map[string]any)["id"] != second || len(versions("versions")) != 0 {
		t.Fatal("replacement/clearing failed")
	}
	call(actor, "GET", endpoint, nil, 404)
	// Two independent adds must both survive rather than replacing a stale array.
	third, fourth := create("3.0", "VR"), create("4.0", "VR")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []string{third, fourth} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			raw, _ := json.Marshal(map[string]string{"id": id})
			_, _, err := h.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{ActorID: member, WorkspaceID: ws, IssueIDOrKey: issue["id"].(string), VersionOperations: map[string][]map[string]json.RawMessage{"fixVersions": {{"add": raw}}}})
			errs <- err
		}(id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(versions("fixVersions")) != 3 {
		t.Fatal("concurrent add lost membership")
	}
	call(actor, "PUT", "/rest/api/3/version/"+third+"/mergeto/"+fourth, nil, 204)
	if len(versions("fixVersions")) != 2 {
		t.Fatal("merge did not deduplicate")
	}
	// A foreign workspace cannot read or mutate a globally addressable version ID.
	foreign := store.NewID("ws")
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Foreign')`, foreign)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, foreign, actor)
	h.WorkspaceSlug = foreign
	call(actor, "GET", "/rest/api/3/version/"+second, nil, 404)
	call(actor, "PUT", "/rest/api/3/version/"+second, map[string]string{"name": "No"}, 404)
	h.WorkspaceSlug = ws
	exec(`DELETE FROM memberships WHERE workspace_id=$1`, foreign)
	exec(`DELETE FROM workspaces WHERE id=$1`, foreign)
	call(actor, "DELETE", "/rest/api/3/version/"+second, nil, 204)
	if len(versions("fixVersions")) != 1 {
		t.Fatal("delete cleared unrelated version")
	}
	got := call(actor, "GET", "/rest/api/3/version/"+fourth, nil, 200)
	if fmt.Sprint(got["projectId"]) != project.ID {
		t.Fatalf("numeric project id: %#v", got)
	}
}
