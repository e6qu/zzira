package confluence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestSpacePermissionTransition pins the operations that move a site from
// direct space permission grants to roles: finding the distinct permission sets
// people hold, deciding what each becomes, and applying those decisions.
func TestSpacePermissionTransition(t *testing.T) {
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
	ws, admin, member, other := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Transition test')`, ws)
	for _, value := range []struct{ id, role string }{{admin, "admin"}, {member, "member"}, {other, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Transition user')`, value.id, value.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, value.id, value.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, value.id, store.HashToken(value.id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM api_tasks WHERE workspace_id=$1`,
			`DELETE FROM wiki_space_permission_combinations WHERE workspace_id=$1`,
			`DELETE FROM wiki_space_permission_grants WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_space_role_assignments WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
			`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
			`DELETE FROM memberships WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(sql, ws)
		}
		for _, id := range []string{admin, member, other} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	v1 := &V1Handler{Handler: h}
	send := func(handler http.Handler, prefix, user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, prefix+path, strings.NewReader(string(raw)))
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, response.Code, want, response.Body.String())
		}
		return response
	}
	callV1 := func(user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		return send(v1, "/wiki/rest/api", user, method, path, body, want)
	}
	callV2 := func(user, method, path string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		return send(h, "/wiki/api/v2", user, method, path, body, want)
	}
	object := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, response.Body.String())
		}
		return out
	}
	runner := &store.APITaskRunner{Store: st, BulkIssueExecutor: h.Commands}
	runTask := func(user, taskID string) map[string]any {
		t.Helper()
		if err := runner.DrainOnce(ctx, ws); err != nil {
			t.Fatalf("running %s: %v", taskID, err)
		}
		return object(callV2(user, "GET", "/space-permissions/transition/tasks/"+taskID, nil, 200))
	}
	grants := func(spaceKey string) int {
		t.Helper()
		var count int
		if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM wiki_space_permission_grants g
			JOIN wiki_spaces s ON s.id=g.space_id WHERE s.workspace_id=$1 AND s.key=$2`, ws, spaceKey).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	assignments := func(spaceKey string) int {
		t.Helper()
		var count int
		if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM wiki_space_role_assignments a
			JOIN wiki_spaces s ON s.id=a.space_id WHERE s.workspace_id=$1 AND s.key=$2`, ws, spaceKey).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	for _, key := range []string{"TRA", "TRB"} {
		callV2(admin, "POST", "/spaces", map[string]any{"key": key, "name": "Transition " + key}, 201)
		for _, operation := range []map[string]any{
			{"key": "read", "target": "space"}, {"key": "create", "target": "page"},
		} {
			callV1(admin, "POST", "/space/"+key+"/permission", map[string]any{
				"subject": map[string]any{"type": "user", "identifier": member}, "operation": operation}, 200)
		}
	}
	// A second person with a different set, so the scan has two combinations
	// to tell apart.
	callV1(admin, "POST", "/space/TRA/permission", map[string]any{
		"subject": map[string]any{"type": "user", "identifier": other}, "operation": map[string]any{"key": "read", "target": "space"}}, 200)

	// The transition is administration.
	callV2(member, "POST", "/space-permissions/transition/combinations", nil, 403)
	callV2(member, "GET", "/space-permissions/transition/combinations", nil, 403)

	accepted := object(callV2(admin, "POST", "/space-permissions/transition/combinations", nil, 202))
	scanTask, _ := accepted["taskId"].(string)
	if scanTask == "" || accepted["status"] != "IN_PROGRESS" {
		t.Fatalf("scan task: %v", accepted)
	}
	if finished := runTask(admin, scanTask); finished["status"] != "COMPLETED" {
		t.Fatalf("scan did not complete: %v", finished)
	}

	listed := object(callV2(admin, "GET", "/space-permissions/transition/combinations", nil, 200))
	results, _ := listed["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("expected two combinations: %v", listed)
	}
	byPermissions := map[string]map[string]any{}
	for _, raw := range results {
		entry, _ := raw.(map[string]any)
		permissions, _ := entry["permissions"].([]any)
		names := make([]string, 0, len(permissions))
		for _, permission := range permissions {
			names = append(names, permission.(string))
		}
		byPermissions[strings.Join(names, ",")] = entry
	}
	pair := byPermissions["create/page,read/space"]
	if pair == nil {
		t.Fatalf("the two-permission combination is missing: %v", listed)
	}
	pairID, _ := pair["combinationId"].(string)
	// One person holding the same set in two spaces is one principal in two
	// spaces, not two principals.
	if pair["principalCount"].(float64) != 1 || pair["spaceCount"].(float64) != 2 {
		t.Fatalf("combination counts: %v", pair)
	}
	single := byPermissions["read/space"]
	if single == nil || single["principalCount"].(float64) != 1 || single["spaceCount"].(float64) != 1 {
		t.Fatalf("single-permission combination: %v", byPermissions)
	}

	roles := object(callV2(admin, "GET", "/space-roles", nil, 200))
	roleResults, _ := roles["results"].([]any)
	roleID := roleResults[0].(map[string]any)["id"].(string)

	// A personal space selection reaches only personal spaces. TRA and TRB are
	// global, so a personal removal leaves their grants alone.
	personalRemoval := object(callV2(admin, "POST", "/space-permissions/transition/access-removals", map[string]any{
		"permissionCombinationIds": []string{pairID},
		"spaceSelection":           map[string]any{"spaceType": "PERSONAL"}}, 202))
	personalTask, _ := personalRemoval["taskId"].(string)
	if finished := runTask(admin, personalTask); finished["status"] != "COMPLETED" {
		t.Fatalf("personal removal did not complete: %v", finished)
	}
	if grants("TRA") != 3 || grants("TRB") != 2 {
		t.Fatalf("a personal selection touched global spaces: TRA=%d TRB=%d", grants("TRA"), grants("TRB"))
	}
	callV2(admin, "POST", "/space-permissions/transition/access-removals", map[string]any{
		"permissionCombinationIds": []string{pairID},
		"spaceSelection":           map[string]any{"spaceType": "sideways"}}, 400)
	callV2(admin, "POST", "/space-permissions/transition/access-removals", map[string]any{
		"permissionCombinationIds": []string{pairID},
		"spaceSelection":           map[string]any{"spaceType": "SPECIFIC"}}, 400)
	callV2(admin, "POST", "/space-permissions/transition/access-removals", map[string]any{
		"permissionCombinationIds": []string{}}, 400)
	callV2(admin, "POST", "/space-permissions/transition/role-assignments", map[string]any{
		"assignments": []any{map[string]any{"permissionCombinationId": pairID,
			"principalTypeAssignments": []any{map[string]any{"principalType": "USER", "removeAccess": false}}}}}, 400)

	// Assigning a role in one space leaves the other alone, which is what the
	// space selection is for.
	assignAccepted := object(callV2(admin, "POST", "/space-permissions/transition/role-assignments", map[string]any{
		"assignments": []any{map[string]any{
			"permissionCombinationId":  pairID,
			"principalTypeAssignments": []any{map[string]any{"principalType": "USER", "removeAccess": false, "roleId": roleID}},
		}},
		"spaceSelection": map[string]any{"spaceType": "SPECIFIC", "selectedSpaces": []any{map[string]any{"key": "TRA"}}},
	}, 202))
	assignTask, _ := assignAccepted["taskId"].(string)
	if finished := runTask(admin, assignTask); finished["status"] != "COMPLETED" {
		t.Fatalf("assignment did not complete: %v", finished)
	}
	if assignments("TRA") != 1 {
		t.Fatalf("the role was not assigned in the selected space: %d", assignments("TRA"))
	}
	if assignments("TRB") != 0 {
		t.Fatalf("the role was assigned in a space that was not selected: %d", assignments("TRB"))
	}
	// The transitioned subject's grants are gone; the site is meant to end up
	// governed by roles rather than by both at once. The other subject in the
	// same space kept theirs, because their combination was not transitioned.
	if grants("TRA") != 1 {
		t.Fatalf("TRA should keep only the untransitioned subject's grant: %d", grants("TRA"))
	}
	if grants("TRB") != 2 {
		t.Fatalf("TRB was touched despite not being selected: %d", grants("TRB"))
	}

	// Removing access drops the grants without assigning anything.
	removeAccepted := object(callV2(admin, "POST", "/space-permissions/transition/access-removals", map[string]any{
		"permissionCombinationIds": []string{pairID},
		"spaceSelection":           map[string]any{"spaceType": "ALL"},
	}, 202))
	removeTask, _ := removeAccepted["taskId"].(string)
	if finished := runTask(admin, removeTask); finished["status"] != "COMPLETED" {
		t.Fatalf("removal did not complete: %v", finished)
	}
	if grants("TRB") != 0 {
		t.Fatalf("the removal left grants behind: %d", grants("TRB"))
	}
	if assignments("TRB") != 0 {
		t.Fatalf("a removal should not assign a role: %d", assignments("TRB"))
	}
	if grants("TRA") != 1 {
		t.Fatalf("the untransitioned subject's grant was removed: %d", grants("TRA"))
	}

	// A task from another family is not a transition task.
	callV2(admin, "GET", "/space-permissions/transition/tasks/task_missing", nil, 404)
	callV2(member, "GET", "/space-permissions/transition/tasks/"+scanTask, nil, 403)
}
