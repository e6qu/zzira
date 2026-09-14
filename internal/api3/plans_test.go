package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestPlansAndPlanTeams covers Jira's plans and the teams planned in them.
func TestPlansAndPlanTeams(t *testing.T) {
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
	ws, admin, member := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Plans test')`, ws)
	for _, identity := range []struct{ id, role string }{{admin, "admin"}, {member, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Test User')`, identity.id, identity.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{`DELETE FROM plans WHERE workspace_id=$1`, `DELETE FROM atlassian_teams WHERE workspace_id=$1`, `DELETE FROM filters WHERE workspace_id=$1`,
			`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`,
			`DELETE FROM projects WHERE workspace_id=$1`, `DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`} {
			exec(sql, ws)
		}
		for _, id := range []string{admin, member} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(user+"@example.test", user)
		if method == "PUT" && strings.HasPrefix(strings.TrimSpace(body), "[") {
			r.Header.Set("Content-Type", "application/json-patch+json")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		out := map[string]any{}
		if trimmed := strings.TrimSpace(w.Body.String()); trimmed != "" {
			var decoded any
			if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
				t.Fatal(trimmed, err)
			}
			if object, ok := decoded.(map[string]any); ok {
				out = object
			} else {
				out["value"] = decoded
			}
		}
		return out
	}

	key := fmt.Sprintf("PL%04d", time.Now().UnixNano()%10000)
	project := call(admin, "POST", "/rest/api/3/project", `{"key":"`+key+`","name":"Planned work","projectTypeKey":"software","leadAccountId":"`+admin+`"}`, 201)
	projectID := int64(project["id"].(float64))
	var boardID, filterID int64
	if err := st.Pool.QueryRow(ctx, `SELECT b.jira_id FROM boards b WHERE b.project_id=$1 LIMIT 1`, strconv.FormatInt(projectID, 10)).Scan(&boardID); err != nil {
		t.Fatal(err)
	}
	filter := call(admin, "POST", "/rest/api/3/filter", `{"name":"Plan scope `+key+`","jql":"project = `+key+`"}`, 200)
	filterID, _ = strconv.ParseInt(fmt.Sprint(filter["id"]), 10, 64)
	teamID, err := st.CreateAtlassianTeam(ctx, ws, admin, "Platform", "Builds the platform")
	if err != nil {
		t.Fatal(err)
	}

	base := "/rest/api/3/plans/plan"
	call(member, "GET", base, "", 403)
	call(admin, "POST", base, `{"name":"No scheduling","issueSources":[{"type":"Project","value":`+strconv.FormatInt(projectID, 10)+`}]}`, 400)
	call(admin, "POST", base, `{"name":"Missing board","scheduling":{"estimation":"Days"},"issueSources":[{"type":"Board","value":999999999}]}`, 400)
	call(admin, "POST", base, `{"name":"Bad date","scheduling":{"estimation":"Days","startDate":{"type":"DateCustomField"}},"issueSources":[{"type":"Project","value":`+strconv.FormatInt(projectID, 10)+`}]}`, 400)
	call(admin, "POST", base, `{"name":"Unknown field","scheduling":{"estimation":"Days"},"issueSources":[{"type":"Project","value":`+strconv.FormatInt(projectID, 10)+`}],"surprise":true}`, 400)
	call(admin, "POST", base, `{"name":"Unknown group","scheduling":{"estimation":"Days"},"issueSources":[{"type":"Project","value":`+strconv.FormatInt(projectID, 10)+`}],"permissions":[{"type":"View","holder":{"type":"Group","value":"nobody-here"}}]}`, 400)

	created := call(admin, "POST", base, `{"name":"Roadmap","leadAccountId":"`+admin+`","scheduling":{"estimation":"StoryPoints"},
		"issueSources":[{"type":"Board","value":`+strconv.FormatInt(boardID, 10)+`},{"type":"Project","value":`+strconv.FormatInt(projectID, 10)+`}],
		"exclusionRules":{"numberOfDaysToShowCompletedIssues":14,"workStatusCategoryIds":[3]},
		"crossProjectReleases":[{"name":"Launch","releaseIds":[]}],
		"permissions":[{"type":"Edit","holder":{"type":"AccountId","value":"`+member+`"}}]}`, 201)
	planID := int64(created["value"].(float64))
	planPath := base + "/" + strconv.FormatInt(planID, 10)
	plan := call(admin, "GET", planPath, "", 200)
	scheduling := plan["scheduling"].(map[string]any)
	if plan["name"] != "Roadmap" || plan["status"] != "Active" || scheduling["dependencies"] != "Sequential" || scheduling["startDate"].(map[string]any)["type"] != "TargetStartDate" ||
		len(plan["issueSources"].([]any)) != 2 || plan["lastSaved"] == nil || plan["exclusionRules"].(map[string]any)["numberOfDaysToShowCompletedIssues"] != float64(14) {
		t.Fatal(plan)
	}
	call(admin, "GET", base+"/999999999", "", 404)

	call(admin, "PUT", planPath, `[{"op":"replace","path":"/scheduling/estimation","value":"Days"},{"op":"add","path":"/issueSources/-","value":{"type":"Filter","value":`+strconv.FormatInt(filterID, 10)+`}},{"op":"replace","path":"/name","value":"Roadmap 2027"}]`, 204)
	patched := call(admin, "GET", planPath, "", 200)
	if patched["name"] != "Roadmap 2027" || patched["scheduling"].(map[string]any)["estimation"] != "Days" || len(patched["issueSources"].([]any)) != 3 {
		t.Fatal(patched)
	}
	call(admin, "PUT", planPath, `[{"op":"replace","path":"/scheduling/estimation","value":"Weeks"}]`, 400)
	call(admin, "PUT", planPath, `[{"op":"replace","path":"/nowhere","value":1}]`, 400)
	call(admin, "PUT", planPath, `[{"op":"replace","path":"/id","value":1}]`, 400)

	// Teams.
	var sourceID int64
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM plan_issue_sources WHERE plan_id=$1 AND type='Board'`, planID).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	planOnly := call(admin, "POST", planPath+"/team/planonly", `{"name":"Mobile","planningStyle":"Scrum","sprintLength":14,"capacity":30,"issueSourceId":`+strconv.FormatInt(sourceID, 10)+`,"memberAccountIds":["`+member+`"]}`, 201)
	planOnlyID := strconv.FormatInt(int64(planOnly["value"].(float64)), 10)
	call(admin, "POST", planPath+"/team/planonly", `{"name":"Nobody","planningStyle":"Waterfall"}`, 400)
	call(admin, "POST", planPath+"/team/planonly", `{"name":"Stranger","planningStyle":"Kanban","memberAccountIds":["usr_nobody"]}`, 400)
	call(admin, "POST", planPath+"/team/atlassian", `{"id":"`+teamID+`","planningStyle":"Kanban","capacity":12}`, 204)
	call(admin, "POST", planPath+"/team/atlassian", `{"id":"`+teamID+`","planningStyle":"Kanban"}`, 400)
	call(admin, "POST", planPath+"/team/atlassian", `{"id":"00000000-0000-0000-0000-000000000000","planningStyle":"Kanban"}`, 404)
	teams := call(admin, "GET", planPath+"/team", "", 200)
	values := teams["values"].([]any)
	if len(values) != 2 || values[0].(map[string]any)["type"] != "PlanOnly" || values[0].(map[string]any)["name"] != "Mobile" || values[1].(map[string]any)["id"] != teamID || teams["total"] != float64(2) {
		t.Fatal(teams)
	}
	mobile := call(admin, "GET", planPath+"/team/planonly/"+planOnlyID, "", 200)
	if mobile["name"] != "Mobile" || mobile["sprintLength"] != float64(14) || len(mobile["memberAccountIds"].([]any)) != 1 {
		t.Fatal(mobile)
	}
	call(admin, "PUT", planPath+"/team/planonly/"+planOnlyID, `[{"op":"replace","path":"/capacity","value":40},{"op":"replace","path":"/memberAccountIds","value":[]}]`, 204)
	if updated := call(admin, "GET", planPath+"/team/planonly/"+planOnlyID, "", 200); updated["capacity"] != float64(40) || len(updated["memberAccountIds"].([]any)) != 0 {
		t.Fatal(updated)
	}
	call(admin, "PUT", planPath+"/team/atlassian/"+teamID, `[{"op":"add","path":"/name","value":"Renamed"}]`, 400)
	call(admin, "PUT", planPath+"/team/atlassian/"+teamID, `[{"op":"replace","path":"/planningStyle","value":"Scrum"}]`, 204)
	if atlassian := call(admin, "GET", planPath+"/team/atlassian/"+teamID, "", 200); atlassian["id"] != teamID || atlassian["planningStyle"] != "Scrum" || atlassian["capacity"] != float64(12) {
		t.Fatal(atlassian)
	}

	// Duplicate, page, archive and trash.
	copyID := int64(call(admin, "POST", planPath+"/duplicate", `{"name":"Roadmap copy"}`, 201)["value"].(float64))
	copyPath := base + "/" + strconv.FormatInt(copyID, 10)
	if copied := call(admin, "GET", copyPath+"/team", "", 200); copied["total"] != float64(2) {
		t.Fatal(copied)
	}
	call(admin, "DELETE", planPath+"/team/planonly/"+planOnlyID, "", 204)
	call(admin, "GET", planPath+"/team/planonly/"+planOnlyID, "", 404)
	call(admin, "DELETE", planPath+"/team/atlassian/"+teamID, "", 204)

	first := call(admin, "GET", base+"?maxResults=1", "", 200)
	if first["size"] != float64(1) || first["last"] != false || first["total"] != float64(2) || first["values"].([]any)[0].(map[string]any)["id"] != strconv.FormatInt(planID, 10) {
		t.Fatal(first)
	}
	second := call(admin, "GET", base+"?maxResults=1&cursor="+first["nextPageCursor"].(string), "", 200)
	if second["last"] != true || second["values"].([]any)[0].(map[string]any)["id"] != strconv.FormatInt(copyID, 10) {
		t.Fatal(second)
	}
	call(admin, "GET", base+"?cursor=%21", "", 400)

	call(admin, "PUT", copyPath+"/archive", "", 204)
	call(admin, "PUT", copyPath+"/archive", "", 409)
	call(admin, "PUT", copyPath, `[{"op":"replace","path":"/name","value":"Late"}]`, 409)
	call(admin, "GET", copyPath+"/team/planonly/1", "", 409)
	call(admin, "POST", copyPath+"/duplicate", `{"name":"Again"}`, 409)
	if active := call(admin, "GET", base, "", 200); active["total"] != float64(1) {
		t.Fatal(active)
	}
	if archived := call(admin, "GET", base+"?includeArchived=true", "", 200); archived["total"] != float64(2) {
		t.Fatal(archived)
	}
	call(admin, "PUT", planPath+"/trash", "", 204)
	if trashed := call(admin, "GET", base+"?includeTrashed=true", "", 200); trashed["total"] != float64(1) || trashed["values"].([]any)[0].(map[string]any)["status"] != "Trashed" {
		t.Fatal(trashed)
	}
}
