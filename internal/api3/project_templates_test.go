package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestProjectTemplates covers saved custom templates and creating a project
// from a custom template.
func TestProjectTemplates(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES ($1,$1,'Project templates')`, ws)
	for _, identity := range []struct{ id, role string }{{admin, "admin"}, {member, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES ($1,$2,'test','Test User')`, identity.id, identity.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES ($1,$2,$3)`, ws, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES ($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		for _, sql := range []string{`DELETE FROM api_tasks WHERE workspace_id=$1`, `DELETE FROM project_templates WHERE workspace_id=$1`,
			`DELETE FROM service_desks WHERE workspace_id=$1`, `DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`,
			`DELETE FROM projects WHERE workspace_id=$1`, `DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`} {
			_, _ = st.Pool.Exec(ctx, sql, ws)
		}
		for _, id := range []string{admin, member} {
			_, _ = st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE user_id=$1`, id)
			_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) (map[string]any, *httptest.ResponseRecorder) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(user+"@example.test", user)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		out := map[string]any{}
		if strings.TrimSpace(w.Body.String()) != "" {
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(w.Body.String(), err)
			}
		}
		return out, w
	}
	suffix := fmt.Sprintf("%04d", time.Now().UnixNano()%10000)
	key := "TP" + suffix
	project, _ := call(admin, "POST", "/rest/api/3/project", `{"key":"`+key+`","name":"Template source","projectTypeKey":"software","leadAccountId":"`+admin+`"}`, 201)
	projectID := fmt.Sprint(int64(project["id"].(float64)))

	save := func(name, kind string, want int) map[string]any {
		t.Helper()
		out, _ := call(admin, "POST", "/rest/api/3/project-template/save-template", `{"templateName":"`+name+`","templateDescription":"From the delivery project","templateFromProjectRequest":{"projectId":`+projectID+`,"templateType":"`+kind+`","templateGenerationOptions":{"enableScreenDelegatedAdminSupport":true}}}`, want)
		return out
	}
	call(member, "POST", "/rest/api/3/project-template/save-template", `{}`, 403)
	call(admin, "POST", "/rest/api/3/project-template/save-template", `{"templateName":"No source"}`, 400)
	call(admin, "POST", "/rest/api/3/project-template/save-template", `{"templateName":"Missing","templateFromProjectRequest":{"projectId":999999999,"templateType":"LIVE"}}`, 400)
	save(strings.Repeat("n", 51), "LIVE", 400)
	save("Odd type "+suffix, "COPY", 400)
	live := object(save("Delivery "+suffix, "LIVE", 200)["projectTemplateKey"])
	save("Delivery "+suffix, "SNAPSHOT", 400)
	snapshot := object(save("Frozen "+suffix, "SNAPSHOT", 200)["projectTemplateKey"])

	model, _ := call(admin, "GET", "/rest/api/3/project-template/live-template?templateKey="+live["key"].(string), "", 200)
	configuration := object(model["snapshotTemplate"])
	if model["type"] != "LIVE" || fmt.Sprint(model["liveTemplateProjectIdReference"]) != projectID || object(model["archetype"])["type"] != "SOFTWARE" ||
		configuration["projectTypeKey"] != "software" || configuration["boardType"] != "scrum" || object(model["templateGenerationOptions"])["enableScreenDelegatedAdminSupport"] != true ||
		object(model["projectTemplateKey"])["uuid"] != live["uuid"] {
		t.Fatal(model)
	}
	if byProject, _ := call(admin, "GET", "/rest/api/3/project-template/live-template?projectId="+key, "", 200); object(byProject["projectTemplateKey"])["key"] != live["key"] {
		t.Fatal(byProject)
	}
	call(admin, "GET", "/rest/api/3/project-template/live-template", "", 400)
	call(admin, "GET", "/rest/api/3/project-template/live-template?templateKey=nope", "", 404)

	call(admin, "PUT", "/rest/api/3/project-template/edit-template", `{"templateKey":"`+live["key"].(string)+`","templateName":"Renamed `+suffix+`","templateDescription":"Current delivery setup"}`, 200)
	if renamed, _ := call(admin, "GET", "/rest/api/3/project-template/live-template?templateKey="+live["key"].(string), "", 200); renamed["name"] != "Renamed "+suffix || renamed["description"] != "Current delivery setup" {
		t.Fatal(renamed)
	}
	call(admin, "PUT", "/rest/api/3/project-template/edit-template", `{"templateKey":"nope","templateName":"x"}`, 404)
	call(admin, "PUT", "/rest/api/3/project-template/edit-template", `{"templateKey":"`+live["key"].(string)+`","templateDescription":"`+strings.Repeat("d", 151)+`"}`, 400)
	call(admin, "DELETE", "/rest/api/3/project-template/remove-template?templateKey="+snapshot["key"].(string), "", 200)
	call(admin, "GET", "/rest/api/3/project-template/live-template?templateKey="+snapshot["key"].(string), "", 404)
	call(admin, "DELETE", "/rest/api/3/project-template/remove-template?templateKey="+snapshot["key"].(string), "", 404)
	call(admin, "DELETE", "/rest/api/3/project-template/remove-template", "", 400)

	// Creating a project from a custom template.
	newKey := "TN" + suffix
	details := `"details":{"key":"` + newKey + `","name":"From template","leadAccountId":"` + admin + `","assigneeType":"PROJECT_LEAD"}`
	call(admin, "POST", "/rest/api/3/project-template", `{`+details+`,"template":{"project":{"projectTypeKey":"software"},"workflow":{"workflows":[{"name":"new"}]}}}`, 400)
	call(admin, "POST", "/rest/api/3/project-template", `{`+details+`,"template":{"project":{"projectTypeKey":"software"},"scope":{"type":"PROJECT"}}}`, 400)
	call(admin, "POST", "/rest/api/3/project-template", `{`+details+`,"template":{"project":{"projectTypeKey":"software","permissionSchemeId":{"type":"REF","id":"new-scheme"}}}}`, 400)
	call(admin, "POST", "/rest/api/3/project-template", `{`+details+`,"template":{"project":{"projectTypeKey":"software","permissionSchemeId":{"type":"ID","id":"999999999"}}}}`, 400)
	call(admin, "POST", "/rest/api/3/project-template", `{"details":{"key":"x","name":"Bad key","leadAccountId":"`+admin+`"},"template":{"project":{"projectTypeKey":"software"}}}`, 400)
	call(admin, "POST", "/rest/api/3/project-template", `{"details":{"key":"`+key+`","name":"Taken","leadAccountId":"`+admin+`"},"template":{"project":{"projectTypeKey":"software"}}}`, 400)
	projectCapability := `"projectTypeKey":"software"`
	if id, ok := configuration["permissionSchemeId"]; ok {
		projectCapability += `,"permissionSchemeId":{"type":"ID","id":"` + fmt.Sprint(id) + `"}`
	}
	task, response := call(admin, "POST", "/rest/api/3/project-template", `{`+details+`,"template":{"scope":{"type":"GLOBAL"},"project":{`+projectCapability+`}}}`, 303)
	if response.Header().Get("Location") == "" || task["status"] != "ENQUEUED" {
		t.Fatal(task, response.Header())
	}
	if err := (&store.APITaskRunner{Store: st}).DrainOnce(ctx, ws); err != nil {
		t.Fatal(err)
	}
	finished, _ := call(admin, "GET", "/rest/api/3/task/"+fmt.Sprint(task["id"]), "", 200)
	if finished["status"] != "COMPLETE" || object(finished["result"])["projectKey"] != newKey {
		t.Fatal(finished)
	}
	created, _ := call(admin, "GET", "/rest/api/3/project/"+newKey, "", 200)
	if created["name"] != "From template" {
		t.Fatal(created)
	}
}

func object(value any) map[string]any { return value.(map[string]any) }
