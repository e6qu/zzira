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
	call(actor, "POST", "/rest/api/3/dashboard?extendAdminPermissions=true", details("No", empty, empty), 400)
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
	call(actor, "POST", "/rest/api/3/project", map[string]any{"key": "DG", "name": "Dashboard project", "projectTypeKey": "software", "leadAccountId": actor}, 201)
	for i := 0; i < 3; i++ {
		issue := call(actor, "POST", "/rest/api/3/issue", map[string]any{"fields": map[string]any{"project": map[string]string{"key": "DG"}, "summary": fmt.Sprintf("Chart work %d", i), "issuetype": map[string]string{"name": "Task"}, "assignee": map[string]string{"accountId": member}}}, 201)
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
	for _, q := range []string{"maxResults=-1", "startAt=-1", "orderBy=bogus", "groupId=unsupported", "status=archived"} {
		call(member, "GET", "/rest/api/3/dashboard/search?"+q, nil, 400)
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
