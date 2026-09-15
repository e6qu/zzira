package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestDashboardLifecyclePrivacyAndGadgets(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Dashboard test')`, ws)
	for _, user := range []string{actor, member} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Dashboard user')`, user, user+"@example.test")
		role := "member"
		if user == actor {
			role = "admin"
		}
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, user, role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, user, store.HashToken(user))
	}
	t.Cleanup(func() {
		for _, sql := range []string{`DELETE FROM dashboards WHERE workspace_id=$1`, `DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, `DELETE FROM projects WHERE workspace_id=$1`, `DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`} {
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
	details := func(name string, view, edit []any) map[string]any {
		return map[string]any{"name": name, "description": "Delivery insights", "sharePermissions": view, "editPermissions": edit}
	}
	empty := []any{}
	loggedin := []any{map[string]string{"type": "loggedin"}}
	d := call(actor, "POST", "/rest/api/3/dashboard", details("Private", empty, empty), 200)
	id := d["id"].(string)
	path := "/rest/api/3/dashboard/" + id
	if d["isFavourite"] != true || d["isWritable"] != true {
		t.Fatal(d)
	}
	call(member, "GET", path, nil, 404)
	if got := call(member, "GET", "/rest/api/3/dashboard", nil, 200); got["total"] != float64(0) {
		t.Fatal(got)
	}
	actions, err := st.ActionsSince(ctx, ws, member, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range actions {
		if a.EntityType == "dashboard" && a.EntityID == id {
			t.Fatal("private dashboard action leaked")
		}
	}
	call(actor, "POST", "/rest/api/3/dashboard", map[string]any{"name": "Missing permissions"}, 400)
	call(member, "POST", "/rest/api/3/dashboard?extendAdminPermissions=true", details("No", empty, empty), 403)
	call(actor, "POST", "/rest/api/3/dashboard?extendAdminPermissions=maybe", details("No", empty, empty), 400)
	call(actor, "PUT", path, details("Shared", loggedin, empty), 200)
	if got := call(member, "GET", path, nil, 200); got["isWritable"] != false || got["isFavourite"] != false {
		t.Fatal(got)
	}
	call(member, "PUT", path, details("Denied", empty, empty), 403)
	call(member, "DELETE", path, nil, 403)
	call(member, "POST", path+"/gadget", map[string]any{"moduleKey": "com.zzira:pie-chart"}, 403)
	call(actor, "PUT", path, details("Shared", loggedin, []any{map[string]any{"type": "user", "user": map[string]string{"accountId": member}}}), 200)
	call(member, "PUT", path, details("Still owner only", empty, empty), 403)
	g := call(member, "POST", path+"/gadget", map[string]any{"moduleKey": "com.zzira:pie-chart", "position": map[string]int{"column": 0, "row": 0}}, 200)
	gid := int64(g["id"].(float64))
	gp := fmt.Sprintf("%s/gadget/%d", path, gid)
	prop := fmt.Sprintf("%s/items/%d/properties", path, gid)
	call(actor, "POST", path+"/gadget", map[string]any{"uri": "https://example.test/gadget.xml"}, 400)
	call(actor, "POST", path+"/gadget", map[string]any{"moduleKey": "com.zzira:pie-chart", "position": map[string]int{"column": 3, "row": 0}}, 400)
	call(member, "PUT", prop+"/custom", map[string]any{"preferences": []string{"a", "b"}}, 201)
	call(member, "PUT", prop+"/custom", map[string]any{"updated": true}, 200)
	if got := call(member, "GET", prop+"/custom", nil, 200); got["value"].(map[string]any)["updated"] != true {
		t.Fatal(got)
	}
	if got := call(member, "GET", prop, nil, 200); len(got["keys"].([]any)) != 1 {
		t.Fatal(got)
	}
	call(member, "PUT", prop+"/too-large", strings.Repeat("x", 32769), 400)
	call(member, "PUT", prop+"/zzira.config", map[string]any{"limit": 51}, 400)
	call(member, "PUT", prop+"/zzira.config", nil, 400)
	call(member, "PUT", prop+"/zzira.config", map[string]any{"jql": "project = DG", "groupBy": "status", "limit": 1}, 201)
	dgProject := call(actor, "POST", "/rest/api/3/project", map[string]any{"key": "DG", "name": "Dashboard project", "projectTypeKey": "software", "leadAccountId": actor}, 201)
	chartIssues := []string{}
	for i := 0; i < 3; i++ {
		issue := call(actor, "POST", "/rest/api/3/issue", map[string]any{"fields": map[string]any{"project": map[string]string{"key": "DG"}, "summary": fmt.Sprintf("Chart work %d", i), "issuetype": map[string]string{"name": "Task"}, "assignee": map[string]string{"accountId": member}}}, 201)
		chartIssues = append(chartIssues, fmt.Sprint(issue["id"]))
		if i == 2 {
			exec(`UPDATE issues SET security_level_id='private-test' WHERE jira_id::text=$1`, issue["id"])
		}
	}
	for _, viewer := range []string{actor, member} {
		result, err := st.DashboardGadgetResults(ctx, ws, viewer, id, models.DashboardGadget{ID: gid, ModuleKey: "com.zzira:pie-chart"})
		if err != nil {
			t.Fatal(err)
		}
		want := 2
		if viewer == actor {
			want = 3
		}
		if result.Total != want || len(result.Counts) != 1 || result.Counts[0].Count != want {
			t.Fatal(result)
		}
	}
	// The list limit never caps chart totals; currentUser is evaluated for the viewer.
	result, err := st.DashboardGadgetResults(ctx, ws, member, id, models.DashboardGadget{ID: gid, ModuleKey: "com.zzira:assigned-to-me"})
	if err != nil || result.Total != 2 || len(result.Issues) != 1 {
		t.Fatalf("assigned results: %+v, %v", result, err)
	}
	result, err = st.DashboardGadgetResults(ctx, ws, actor, id, models.DashboardGadget{ID: gid, ModuleKey: "com.zzira:assigned-to-me"})
	if err != nil || result.Total != 0 {
		t.Fatalf("owner assignment leaked into viewer: %+v, %v", result, err)
	}
	// Labels count once under each label, while totals count each work item once.
	for index, labels := range []string{"{alpha,beta}", "{alpha}", "{beta}"} {
		exec(`UPDATE issues SET labels=$1::text[] WHERE jira_id::text=$2`, labels, chartIssues[index])
	}
	call(member, "PUT", prop+"/zzira.config", map[string]any{"jql": "project = DG", "groupBy": "labels", "yGroupBy": "fixVersion", "limit": 1}, 400)
	call(member, "PUT", prop+"/zzira.config", map[string]any{"jql": "project = DG", "groupBy": "labels", "limit": 1}, 200)
	counts := func(viewer string) map[string]int {
		t.Helper()
		result, err := st.DashboardGadgetResults(ctx, ws, viewer, id, models.DashboardGadget{ID: gid, ModuleKey: "com.zzira:heat-map"})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int{"total": result.Total}
		for _, count := range result.Counts {
			out[count.Name] = count.Count
		}
		return out
	}
	if got := counts(member); fmt.Sprint(got) != fmt.Sprint(map[string]int{"alpha": 2, "beta": 1, "total": 2}) {
		t.Fatalf("member label counts = %v", got)
	}
	if got := counts(actor); fmt.Sprint(got) != fmt.Sprint(map[string]int{"alpha": 2, "beta": 2, "total": 3}) {
		t.Fatalf("owner label counts = %v", got)
	}
	call(member, "PUT", prop+"/zzira.config", map[string]any{"jql": "project = DG", "groupBy": "status", "yGroupBy": "labels", "limit": 1}, 200)
	result, err = st.DashboardGadgetResults(ctx, ws, actor, id, models.DashboardGadget{ID: gid, ModuleKey: "com.zzira:two-dimensional-statistics"})
	if err != nil || result.Grid == nil || len(result.Grid.Columns) != 1 || len(result.Grid.Rows) != 1 || result.Grid.Rows[0].Name != "alpha" || fmt.Sprint(result.Grid.Rows[0].Counts) != "[2]" || result.Grid.HiddenRows != 1 || fmt.Sprint(result.Grid.ColumnTotals) != "[4]" || result.Grid.Total != 4 || result.Total != 3 {
		t.Fatalf("two dimensional statistics = %+v %+v, %v", result, result.Grid, err)
	}
	result, err = st.DashboardGadgetResults(ctx, ws, member, id, models.DashboardGadget{ID: gid, ModuleKey: "com.zzira:two-dimensional-statistics"})
	if err != nil || result.Grid == nil || len(result.Grid.Rows) != 1 || result.Grid.Rows[0].Name != "alpha" || result.Grid.HiddenRows != 1 || result.Grid.Total != 3 {
		t.Fatalf("member two dimensional statistics = %+v %+v, %v", result, result.Grid, err)
	}
	// Watched, voted and in-progress lists are evaluated for each viewer.
	call(member, "PUT", prop+"/zzira.config", map[string]any{"jql": "project = DG", "limit": 5}, 200)
	exec(`DELETE FROM watchers WHERE issue_id IN (SELECT id FROM issues WHERE project_id=(SELECT id FROM projects WHERE workspace_id=$1 AND key='DG'))`, ws)
	exec(`INSERT INTO watchers(issue_id,user_id) SELECT id,$1 FROM issues WHERE jira_id::text=$2`, member, chartIssues[0])
	exec(`INSERT INTO issue_votes(issue_id,user_id) SELECT id,$1 FROM issues WHERE jira_id::text=$2`, member, chartIssues[1])
	exec(`UPDATE issues SET status_id=(SELECT id FROM statuses WHERE category='indeterminate' ORDER BY id LIMIT 1) WHERE jira_id::text=$1`, chartIssues[1])
	for moduleKey, want := range map[string]map[string]int{
		"com.zzira:watched-issues": {member: 1, actor: 0},
		"com.zzira:voted-issues":   {member: 1, actor: 0},
		"com.zzira:in-progress":    {member: 1, actor: 0},
	} {
		for viewer, total := range want {
			result, err := st.DashboardGadgetResults(ctx, ws, viewer, id, models.DashboardGadget{ID: gid, ModuleKey: moduleKey})
			if err != nil || result.Total != total || len(result.Issues) != total {
				t.Fatalf("%s for %s = %+v, %v", moduleKey, viewer, result, err)
			}
		}
	}
	// The activity stream shows creations, changes and comments the viewer may
	// see, newest first.
	call(actor, "PUT", "/rest/api/3/issue/"+chartIssues[0], map[string]any{"fields": map[string]any{"summary": "Chart work renamed"}}, 204)
	commentBody := func(text string) string {
		return `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"` + text + `"}]}]}`
	}
	exec(`INSERT INTO comments(id,issue_id,workspace_id,author_id,body,created_at) SELECT 'cmt_stream_open_'||$1::text, id, $1, $2, $3::jsonb, now() + interval '1 second' FROM issues WHERE jira_id::text=$4`, ws, actor, commentBody("Open note"), chartIssues[0])
	exec(`INSERT INTO comments(id,issue_id,workspace_id,author_id,body,created_at,visibility_type,visibility_value) SELECT 'cmt_stream_hidden_'||$1::text, id, $1, $2, $3::jsonb, now() + interval '2 seconds', 'group', '00000000-0000-0000-0000-000000000000' FROM issues WHERE jira_id::text=$4`, ws, actor, commentBody("Hidden note"), chartIssues[1])
	call(member, "PUT", prop+"/zzira.config", map[string]any{"jql": "project = DG", "limit": 50}, 200)
	stream := func(viewer string) (kinds map[string]int, text string) {
		t.Helper()
		result, err := st.DashboardGadgetResults(ctx, ws, viewer, id, models.DashboardGadget{ID: gid, ModuleKey: "com.zzira:activity-stream"})
		if err != nil {
			t.Fatal(err)
		}
		kinds = map[string]int{}
		for index, entry := range result.Activity {
			if index > 0 && entry.At.After(result.Activity[index-1].At) {
				t.Fatalf("activity is not newest first: %+v", result.Activity)
			}
			kinds[entry.Kind]++
			text += string(entry.Comment)
			for _, change := range entry.Changes {
				text += change.Field + ":" + change.ToString + ";"
			}
		}
		return kinds, text
	}
	memberKinds, memberText := stream(member)
	if memberKinds["created"] != 2 || memberKinds["commented"] != 1 || !strings.Contains(memberText, "Open note") || strings.Contains(memberText, "Hidden note") || !strings.Contains(memberText, "summary:Chart work renamed;") {
		t.Fatalf("member activity = %v %q", memberKinds, memberText)
	}
	if ownerKinds, ownerText := stream(actor); ownerKinds["created"] != 3 || ownerKinds["commented"] != 1 || strings.Contains(ownerText, "Hidden note") {
		t.Fatalf("owner activity = %v %q", ownerKinds, ownerText)
	}
	call(member, "PUT", prop+"/zzira.config", map[string]any{"jql": "project = DG", "limit": 1}, 200)
	if result, err := st.DashboardGadgetResults(ctx, ws, member, id, models.DashboardGadget{ID: gid, ModuleKey: "com.zzira:activity-stream"}); err != nil || len(result.Activity) != 1 || result.Activity[0].Kind != "commented" {
		t.Fatalf("limited activity = %+v, %v", result.Activity, err)
	}
	// The calendar shows this month's visible due work and the release dates of
	// versions in its projects; the road map shows unreleased versions due soon.
	today := time.Now().UTC()
	for _, issueID := range []string{chartIssues[0], chartIssues[2]} {
		exec(`UPDATE issues SET due_date=$1::date WHERE jira_id::text=$2`, today.Format("2006-01-02"), issueID)
	}
	due := call(actor, "POST", "/rest/api/3/version", map[string]any{"name": "Calendar release", "projectId": dgProject["id"], "releaseDate": today.Format("2006-01-02")}, 201)
	late := call(actor, "POST", "/rest/api/3/version", map[string]any{"name": "Late release", "projectId": dgProject["id"], "releaseDate": today.AddDate(0, 0, -1).Format("2006-01-02")}, 201)
	shipped := call(actor, "POST", "/rest/api/3/version", map[string]any{"name": "Shipped release", "projectId": dgProject["id"], "releaseDate": today.Format("2006-01-02")}, 201)
	call(actor, "PUT", fmt.Sprintf("/rest/api/3/version/%v", shipped["id"]), map[string]any{"released": true}, 200)
	call(actor, "POST", "/rest/api/3/version", map[string]any{"name": "Distant release", "projectId": dgProject["id"], "releaseDate": today.AddDate(0, 0, 60).Format("2006-01-02")}, 201)
	for _, issueID := range []string{chartIssues[0], chartIssues[2]} {
		call(actor, "PUT", "/rest/api/3/issue/"+issueID, map[string]any{"fields": map[string]any{"fixVersions": []map[string]any{{"id": due["id"]}}}}, 204)
	}
	call(member, "PUT", prop+"/zzira.config", map[string]any{"jql": "project = DG", "limit": 5}, 200)
	for viewer, want := range map[string]int{member: 1, actor: 2} {
		result, err := st.DashboardGadgetResults(ctx, ws, viewer, id, models.DashboardGadget{ID: gid, ModuleKey: "com.zzira:calendar"})
		if err != nil || result.Calendar == nil || len(result.Calendar.Issues) != want || result.Calendar.More != 0 || result.Calendar.Issues[0].Date != today.Format("2006-01-02") {
			t.Fatalf("%s calendar = %+v, %v", viewer, result.Calendar, err)
		}
		names := map[string]bool{}
		for _, version := range result.Calendar.Versions {
			names[version.VersionName] = true
		}
		// Yesterday's release shows only while it falls in this month.
		lateThisMonth := today.AddDate(0, 0, -1).Month() == today.Month()
		if !names["Calendar release"] || !names["Shipped release"] || names["Distant release"] || names["Late release"] != lateThisMonth {
			t.Fatalf("%s calendar versions = %v", viewer, names)
		}
	}
	// The bubble chart counts the reporter, assignee and commenters as
	// participants, and votes, for the work each viewer can see.
	call(member, "PUT", prop+"/zzira.config", map[string]any{"jql": "project = DG", "limit": 10, "bubbleAxis": "sideways"}, 400)
	call(member, "PUT", prop+"/zzira.config", map[string]any{"jql": "project = DG", "limit": 10, "bubbleAxis": "votes"}, 200)
	for viewer, want := range map[string]int{member: 2, actor: 3} {
		result, err := st.DashboardGadgetResults(ctx, ws, viewer, id, models.DashboardGadget{ID: gid, ModuleKey: "com.zzira:bubble-chart"})
		if err != nil || len(result.Bubbles) != want || result.Config.BubbleAxis != "votes" {
			t.Fatalf("%s bubbles = %+v, %v", viewer, result, err)
		}
		votes := 0
		for _, bubble := range result.Bubbles {
			votes += bubble.Votes
			if bubble.Participants != 2 || bubble.UpdatedDays != 0 {
				t.Fatalf("%s bubble = %+v", viewer, bubble)
			}
		}
		if votes != 1 {
			t.Fatalf("%s bubble votes = %+v", viewer, result.Bubbles)
		}
	}
	var dgProjectID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND key='DG'`, ws).Scan(&dgProjectID); err != nil {
		t.Fatal(err)
	}
	for viewer, want := range map[string]int{member: 1, actor: 2} {
		versions, err := st.RoadMap(ctx, ws, viewer, dgProjectID, 30, time.Now())
		if err != nil || len(versions) != 2 || versions[0].Version.ID != fmt.Sprint(late["id"]) || !versions[0].Overdue || versions[0].Total != 0 ||
			versions[1].Version.ID != fmt.Sprint(due["id"]) || versions[1].Overdue || versions[1].Total != want || versions[1].Progress.ToDo != want || versions[1].Progress.Done != 0 {
			t.Fatalf("%s road map = %+v, %v", viewer, versions, err)
		}
	}
	call(member, "PUT", gp, map[string]any{"color": "purple", "title": "Team delivery", "position": map[string]int{"column": 1, "row": 999}}, 204)
	// Concurrent inserts at the same location must produce unique contiguous rows.
	var wg sync.WaitGroup
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.SaveDashboardGadget(ctx, ws, member, id, 0, store.GadgetUpdate{ModuleKey: "com.zzira:filter-results"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = st.DashboardPresentation(ctx, ws, member, id, "A", 60000); err != nil {
		t.Fatal(err)
	}
	gadgets, err := st.DashboardGadgets(ctx, ws, member, id)
	if err != nil {
		t.Fatal(err)
	}
	for i, g := range gadgets {
		if g.Position.Column != 0 || g.Position.Row != i {
			t.Fatal(gadgets)
		}
	}
	copy := call(member, "POST", path+"/copy", details("My copy", empty, empty), 200)
	copyID := copy["id"].(string)
	cp := "/rest/api/3/dashboard/" + copyID
	if copy["automaticRefreshMs"] != float64(60000) {
		t.Fatal(copy)
	}
	call(actor, "GET", cp, nil, 404)
	copied, err := st.DashboardGadgets(ctx, ws, member, copyID)
	if err != nil || len(copied) != 4 {
		t.Fatal(copied, err)
	}
	for _, g := range copied {
		if g.ID == gid {
			t.Fatal("copy reused gadget ID")
		}
		if g.ModuleKey == "com.zzira:pie-chart" {
			props, err := st.DashboardProperties(ctx, ws, member, copyID, g.ID)
			if err != nil || props["zzira.config"] == nil || props["custom"] == nil {
				t.Fatal(props, err)
			}
		}
	}
	if err = st.SetDashboardFavourite(ctx, ws, member, id, true); err != nil {
		t.Fatal(err)
	}
	page := call(member, "GET", "/rest/api/3/dashboard?filter=favourite&maxResults=1", nil, 200)
	if page["total"] != float64(2) || page["next"] == nil {
		t.Fatal(page)
	}
	page = call(member, "GET", "/rest/api/3/dashboard/search?dashboardName=Shared&orderBy=-favorite_count&expand=owner,description", nil, 200)
	if page["total"] != float64(1) || page["isLast"] != true {
		t.Fatal(page)
	}
	for _, q := range []string{"maxResults=-1", "startAt=-1", "orderBy=bogus", "groupId=1&groupname=admins", "status=archived", "expand=bogus"} {
		call(member, "GET", "/rest/api/3/dashboard/search?"+q, nil, 400)
	}
	if unshared := call(member, "GET", "/rest/api/3/dashboard/search?groupId=unknown&expand=viewUrl,favourite,favouritedCount,isWritable", nil, 200); unshared["total"] != float64(0) {
		t.Fatal(unshared)
	}
	call(member, "DELETE", prop+"/custom", nil, 204)
	call(member, "GET", prop+"/custom", nil, 404)
	call(member, "DELETE", gp, nil, 204)
	call(member, "GET", prop, nil, 404)
	call(actor, "PUT", path, details("Revoked", empty, empty), 200)
	call(member, "GET", path, nil, 404)
	call(member, "GET", path+"/gadget", nil, 404)
	call(member, "POST", path+"/copy", details("Denied", empty, empty), 404)
	actions, err = st.ActionsSince(ctx, ws, member, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range actions {
		if a.EntityType == "dashboard" && a.EntityID == id {
			t.Fatal("revoked dashboard leaked through sync")
		}
	}
	// Same owner, different workspace: globally addressable IDs remain scoped.
	foreign := store.NewID("ws")
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Foreign')`, foreign)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, foreign, actor)
	h.WorkspaceSlug = foreign
	call(actor, "GET", path, nil, 404)
	call(actor, "PUT", path, details("Denied", empty, empty), 404)
	h.WorkspaceSlug = ws
	exec(`DELETE FROM memberships WHERE workspace_id=$1`, foreign)
	exec(`DELETE FROM workspaces WHERE id=$1`, foreign)
	call(actor, "DELETE", path, nil, 204)
	call(actor, "GET", path, nil, 404)
	call(member, "DELETE", cp, nil, 204)

	// ---- bulk edit ----

	// Jira answers 200 with a per-dashboard error map rather than failing the
	// whole request, so a caller learns exactly which ones it could not change.
	first := call(actor, "POST", "/rest/api/3/dashboard", details("Bulk one", empty, empty), 200)
	second := call(actor, "POST", "/rest/api/3/dashboard", details("Bulk two", empty, empty), 200)
	firstID, secondID := fmt.Sprint(first["id"]), fmt.Sprint(second["id"])
	bulk := "/rest/api/3/dashboard/bulk/edit"

	call(actor, "PUT", bulk, map[string]any{"entityIds": []string{}, "action": "delete"}, 400)
	call(actor, "PUT", bulk, map[string]any{"entityIds": []string{firstID}, "action": "nope"}, 400)
	call(actor, "PUT", bulk, map[string]any{"entityIds": []string{firstID}, "action": "changeOwner"}, 400)
	call(actor, "PUT", bulk, map[string]any{"entityIds": []string{firstID}, "action": "changePermission"}, 400)

	shared := call(actor, "PUT", bulk, map[string]any{
		"entityIds": []string{firstID, secondID, "9999"}, "action": "changePermission",
		"permissionDetails": map[string]any{"sharePermissions": []any{map[string]any{"type": "loggedin"}}, "editPermissions": empty},
	}, 200)
	entityErrors, _ := shared["entityErrors"].(map[string]any)
	if len(entityErrors) != 1 {
		t.Fatalf("only the unknown dashboard should fail: %v", shared)
	}
	if _, unknown := entityErrors["9999"]; !unknown {
		t.Fatalf("entityErrors=%v", entityErrors)
	}
	if reread := call(actor, "GET", "/rest/api/3/dashboard/"+firstID, nil, 200); len(reread["sharePermissions"].([]any)) != 1 {
		t.Fatalf("permissions were not applied: %v", reread)
	}

	// Only the owner may hand a dashboard on, and only to a workspace member.
	call(member, "PUT", bulk, map[string]any{
		"entityIds": []string{firstID}, "action": "changeOwner",
		"changeOwnerDetails": map[string]any{"newOwner": member},
	}, 200)
	handed := call(actor, "PUT", bulk, map[string]any{
		"entityIds": []string{firstID}, "action": "changeOwner",
		"changeOwnerDetails": map[string]any{"newOwner": member},
	}, 200)
	if errs, _ := handed["entityErrors"].(map[string]any); len(errs) != 0 {
		t.Fatalf("the owner should be able to hand the dashboard on: %v", handed)
	}
	if reread := call(member, "GET", "/rest/api/3/dashboard/"+firstID, nil, 200); fmt.Sprint(reread["owner"].(map[string]any)["accountId"]) != member {
		t.Fatalf("owner was not transferred: %v", reread)
	}

	removed := call(actor, "PUT", bulk, map[string]any{"entityIds": []string{secondID}, "action": "delete"}, 200)
	if errs, _ := removed["entityErrors"].(map[string]any); len(errs) != 0 {
		t.Fatalf("delete should succeed: %v", removed)
	}
	call(actor, "GET", "/rest/api/3/dashboard/"+secondID, nil, 404)
}
