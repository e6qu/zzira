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

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestJiraPlatformContract pins Jira's app data, UI modification, webhook,
// data classification, data policy, licensing, audit and project platform
// reads as apps and administrators see them.
func TestJiraPlatformContract(t *testing.T) {
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
	ws, admin, member := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Platform contract')`, ws)
	for _, identity := range []struct{ id, role, name string }{{admin, "admin", "Ada Admin"}, {member, "member", "Mo Member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", identity.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	var projectID string
	if err = st.Pool.QueryRow(ctx, `SELECT nextval('jira_project_id')::text`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	projectKey := fmt.Sprintf("P%06d", time.Now().UnixNano()%1000000)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id,project_type_key) VALUES($1,$2,$3,'Platform','wf_default',$4,'software')`, projectID, ws, projectKey, admin)
	issueID := store.NewID("iss")
	exec(`INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,reporter_id,labels,updated_seq) VALUES($1,$2,$3,$4,'Labelled','st_todo','it_task',$5,'{alpha,beta}',0)`, issueID, ws, projectID, projectKey+"-1", admin)
	exec(`INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,reporter_id,labels,updated_seq) VALUES($1,$2,$3,$4,'Also labelled','st_todo','it_task',$5,'{beta,gamma}',0)`, store.NewID("iss"), ws, projectID, projectKey+"-2", admin)
	exec(`UPDATE projects SET issue_seq=2 WHERE id=$1`, projectID)

	installApp := func(key, format string) *models.AppInstallation {
		t.Helper()
		principal := "app_principal_" + store.NewID("t")
		exec(`INSERT INTO users(id,email,password_hash,display_name,active) VALUES($1,$2,'!app-principal!',$3,true)`, principal, principal+"@apps.zzira.invalid", key)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, ws, principal)
		exec(`INSERT INTO app_installations(workspace_id,principal_id,app_key,name,base_url,version,secret_ciphertext,descriptor,descriptor_format)
			VALUES($1,$2,$3,$3,'https://app.example.test','1','\x00'::bytea,'{}'::jsonb,$4)`, ws, principal, key, format)
		installation, installErr := st.AppInstallation(ctx, ws, key)
		if installErr != nil {
			t.Fatal(installErr)
		}
		return installation
	}
	connectApp := installApp("connect-"+strings.ToLower(store.NewID("k")), "connect")
	forgeApp := installApp("forge-"+strings.ToLower(store.NewID("k")), "zzira")
	t.Cleanup(func() {
		var principals []string
		rows, _ := st.Pool.Query(ctx, `SELECT principal_id FROM app_installations WHERE workspace_id=$1`, ws)
		for rows.Next() {
			var principal string
			_ = rows.Scan(&principal)
			principals = append(principals, principal)
		}
		rows.Close()
		for _, query := range []string{
			`DELETE FROM webhooks WHERE workspace_id=$1`, `DELETE FROM ui_modifications WHERE workspace_id=$1`, `DELETE FROM app_installations WHERE workspace_id=$1`,
			`DELETE FROM worklogs WHERE workspace_id=$1`, `DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM custom_fields WHERE workspace_id=$1`,
			`DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(query, ws)
		}
		for _, user := range append(principals, admin, member) {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, BaseURL: "https://zzira.test", WorkspaceSlug: ws}
	send := func(app *models.AppInstallation, user, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if app != nil {
			request = request.WithContext(apps.ContextWithInstallation(context.Background(), app))
		} else if user != "" {
			request.SetBasicAuth(user+"@example.test", user)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	object := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		out := map[string]any{}
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatal(response.Body.String(), err)
		}
		return out
	}
	list := func(response *httptest.ResponseRecorder) []any {
		t.Helper()
		var out []any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatal(response.Body.String(), err)
		}
		return out
	}

	// Connect add-on properties belong to the app whose key matches.
	addon := "/rest/atlassian-connect/1/addons/" + connectApp.Key + "/properties"
	send(forgeApp, "", http.MethodGet, addon, "", http.StatusUnauthorized)
	send(nil, admin, http.MethodGet, addon, "", http.StatusUnauthorized)
	if created := object(send(connectApp, "", http.MethodPut, addon+"/config", `{"theme":"dark"}`, http.StatusCreated)); fmt.Sprint(created["statusCode"]) != "201" {
		t.Fatal(created)
	}
	send(connectApp, "", http.MethodPut, addon+"/config", `{"theme":"light"}`, http.StatusOK)
	if property := object(send(connectApp, "", http.MethodGet, addon+"/config", "", http.StatusOK)); property["value"].(map[string]any)["theme"] != "light" {
		t.Fatal(property)
	}
	send(connectApp, "", http.MethodPut, addon+"/broken", `{`, http.StatusBadRequest)
	send(connectApp, "", http.MethodPut, addon+"/"+strings.Repeat("k", 128), `1`, http.StatusBadRequest)
	if reserved := object(send(connectApp, "", http.MethodGet, addon+"/"+connectClientKeyProperty, "", http.StatusOK)); reserved["value"] != connectApp.ID {
		t.Fatal(reserved)
	}
	send(connectApp, "", http.MethodPut, addon+"/"+connectClientKeyProperty, `"x"`, http.StatusForbidden)
	send(connectApp, "", http.MethodDelete, addon+"/"+connectClientKeyProperty, "", http.StatusForbidden)
	if keys := object(send(connectApp, "", http.MethodGet, addon, "", http.StatusOK))["keys"].([]any); len(keys) != 1 || keys[0].(map[string]any)["key"] != "config" {
		t.Fatal(keys)
	}
	send(connectApp, "", http.MethodDelete, addon+"/config", "", http.StatusNoContent)
	send(connectApp, "", http.MethodDelete, addon+"/config", "", http.StatusNotFound)
	send(connectApp, "", http.MethodGet, addon+"/config", "", http.StatusNotFound)

	// Forge app properties are for Forge apps only.
	send(connectApp, "", http.MethodGet, "/rest/forge/1/app/properties", "", http.StatusForbidden)
	send(nil, admin, http.MethodGet, "/rest/forge/1/app/properties", "", http.StatusUnauthorized)
	send(forgeApp, "", http.MethodPut, "/rest/forge/1/app/properties/enabled", `true`, http.StatusCreated)
	if property := object(send(forgeApp, "", http.MethodGet, "/rest/forge/1/app/properties/enabled", "", http.StatusOK)); property["value"] != true {
		t.Fatal(property)
	}
	if keys := object(send(forgeApp, "", http.MethodGet, "/rest/forge/1/app/properties", "", http.StatusOK))["keys"].([]any); len(keys) != 1 {
		t.Fatal(keys)
	}
	send(forgeApp, "", http.MethodDelete, "/rest/forge/1/app/properties/enabled", "", http.StatusNoContent)

	// UI modifications.
	send(connectApp, "", http.MethodGet, "/rest/api/3/uiModifications", "", http.StatusForbidden)
	send(forgeApp, "", http.MethodPost, "/rest/api/3/uiModifications", `{"name":""}`, http.StatusBadRequest)
	send(forgeApp, "", http.MethodPost, "/rest/api/3/uiModifications", `{"name":"Too wild","contexts":[{"viewType":"GIC"}]}`, http.StatusBadRequest)
	created := object(send(forgeApp, "", http.MethodPost, "/rest/api/3/uiModifications", `{"name":"Hide priority","description":"On create","data":"{\"hide\":true}",
		"contexts":[{"projectId":"`+projectID+`","issueTypeId":"10002","viewType":"GIC"},{"portalId":"1","requestTypeId":"2","viewType":"JSMRequestCreate"}]}`, http.StatusCreated))
	modificationID, _ := created["id"].(string)
	if modificationID == "" || !strings.HasSuffix(fmt.Sprint(created["self"]), modificationID) {
		t.Fatal(created)
	}
	plain := object(send(forgeApp, "", http.MethodGet, "/rest/api/3/uiModifications", "", http.StatusOK))
	if fmt.Sprint(plain["total"]) != "1" || plain["values"].([]any)[0].(map[string]any)["data"] != nil {
		t.Fatal(plain)
	}
	expanded := object(send(forgeApp, "", http.MethodGet, "/rest/api/3/uiModifications?expand=data,contexts", "", http.StatusOK))["values"].([]any)[0].(map[string]any)
	contexts := expanded["contexts"].([]any)
	if expanded["data"] != `{"hide":true}` || len(contexts) != 2 || contexts[0].(map[string]any)["isAvailable"] != true {
		t.Fatal(expanded)
	}
	send(forgeApp, "", http.MethodPut, "/rest/api/3/uiModifications/"+modificationID, `{"name":"Renamed","contexts":[{"projectId":"`+projectID+`","viewType":"IssueView"}]}`, http.StatusNoContent)
	renamed := object(send(forgeApp, "", http.MethodGet, "/rest/api/3/uiModifications?expand=contexts", "", http.StatusOK))["values"].([]any)[0].(map[string]any)
	if renamed["name"] != "Renamed" || len(renamed["contexts"].([]any)) != 1 {
		t.Fatal(renamed)
	}
	send(forgeApp, "", http.MethodPut, "/rest/api/3/uiModifications/00000000-0000-0000-0000-000000000000", `{"name":"Missing"}`, http.StatusNotFound)
	send(forgeApp, "", http.MethodDelete, "/rest/api/3/uiModifications/"+modificationID, "", http.StatusNoContent)
	send(forgeApp, "", http.MethodDelete, "/rest/api/3/uiModifications/"+modificationID, "", http.StatusNotFound)

	// Dynamic webhooks belong to the registering app.
	send(nil, admin, http.MethodPost, "/rest/api/3/webhook", `{}`, http.StatusForbidden)
	registered := object(send(connectApp, "", http.MethodPost, "/rest/api/3/webhook", `{"url":"https://app.example.test/webhooks","webhooks":[
		{"events":["jira:issue_created","jira:issue_updated"],"jqlFilter":"project = `+projectKey+`","fieldIdsFilter":["summary"]},
		{"events":["jira:unknown"],"jqlFilter":"project = `+projectKey+`"}]}`, http.StatusOK))["webhookRegistrationResult"].([]any)
	webhookID := fmt.Sprint(registered[0].(map[string]any)["createdWebhookId"])
	if webhookID == "<nil>" || registered[1].(map[string]any)["errors"] == nil {
		t.Fatal(registered)
	}
	other := object(send(connectApp, "", http.MethodPost, "/rest/api/3/webhook", `{"url":"https://app.example.test/other","webhooks":[{"events":["jira:issue_deleted"],"jqlFilter":"project = `+projectKey+`"}]}`, http.StatusOK))
	if !strings.Contains(fmt.Sprint(other), "single URL") {
		t.Fatal(other)
	}
	send(connectApp, "", http.MethodPost, "/rest/api/3/webhook", `{"url":"https://elsewhere.example/hook","webhooks":[{"events":["jira:issue_deleted"],"jqlFilter":"project = X"}]}`, http.StatusBadRequest)
	page := object(send(connectApp, "", http.MethodGet, "/rest/api/3/webhook", "", http.StatusOK))
	listed := page["values"].([]any)[0].(map[string]any)
	if fmt.Sprint(page["total"]) != "1" || fmt.Sprint(listed["id"]) != webhookID || fmt.Sprint(listed["fieldIdsFilter"]) != "[summary]" {
		t.Fatal(page)
	}
	send(connectApp, "", http.MethodPut, "/rest/api/3/webhook/refresh", `{}`, http.StatusBadRequest)
	refreshed := object(send(connectApp, "", http.MethodPut, "/rest/api/3/webhook/refresh", `{"webhookIds":[`+webhookID+`]}`, http.StatusOK))
	if expiration, _ := refreshed["expirationDate"].(float64); int64(expiration) < time.Now().Add(29*24*time.Hour).UnixMilli() {
		t.Fatal(refreshed)
	}
	exec(`INSERT INTO webhook_deliveries(webhook_id,seq,state,attempts,failed_at,body) SELECT id,1,'abandoned',5,now(),'{"webhookEvent":"jira:issue_created"}' FROM webhooks WHERE jira_id::text=$1`, webhookID)
	failed := object(send(connectApp, "", http.MethodGet, "/rest/api/3/webhook/failed", "", http.StatusOK))
	failures := failed["values"].([]any)
	if len(failures) != 1 || failures[0].(map[string]any)["id"] != webhookID+"-1" || failed["next"] == nil {
		t.Fatal(failed)
	}
	send(forgeApp, "", http.MethodGet, "/rest/api/3/webhook/failed", "", http.StatusForbidden)
	send(connectApp, "", http.MethodDelete, "/rest/api/3/webhook", `{"webhookIds":[`+webhookID+`]}`, http.StatusAccepted)
	if remaining := object(send(connectApp, "", http.MethodGet, "/rest/api/3/webhook", "", http.StatusOK)); fmt.Sprint(remaining["total"]) != "0" {
		t.Fatal(remaining)
	}

	// Administrator webhooks.
	adminHook := `{"name":"Deploys","url":"https://hooks.example.test/deploy","events":["jira:issue_updated"],"filters":{"issue-related-events-section":"project = ` + projectKey + `"}}`
	send(nil, member, http.MethodPost, "/rest/webhooks/1.0/webhook", adminHook, http.StatusForbidden)
	hook := object(send(nil, admin, http.MethodPost, "/rest/webhooks/1.0/webhook", adminHook, http.StatusCreated))
	hookPath := strings.TrimPrefix(fmt.Sprint(hook["self"]), "https://zzira.test")
	if hook["enabled"] != true || hook["name"] != "Deploys" || hook["lastUpdatedUser"] != admin {
		t.Fatal(hook)
	}
	if hooks := list(send(nil, admin, http.MethodGet, "/rest/webhooks/1.0/webhook", "", http.StatusOK)); len(hooks) != 1 {
		t.Fatal(hooks)
	}
	updatedHook := object(send(nil, admin, http.MethodPut, hookPath, `{"name":"Deploys v2","url":"https://hooks.example.test/deploy","events":["jira:issue_updated"],"enabled":false}`, http.StatusOK))
	if updatedHook["name"] != "Deploys v2" || updatedHook["enabled"] != false {
		t.Fatal(updatedHook)
	}
	send(nil, admin, http.MethodPut, hookPath, `{"name":"Bad","url":"https://hooks.example.test/deploy","filters":{"issue-related-events-section":"project in ("}}`, http.StatusBadRequest)
	send(nil, admin, http.MethodDelete, hookPath, "", http.StatusNoContent)
	send(nil, admin, http.MethodGet, hookPath, "", http.StatusNotFound)

	// Data classification and the project default.
	if levels := object(send(nil, member, http.MethodGet, "/rest/api/3/classification-levels?orderBy=-rank", "", http.StatusOK))["classifications"].([]any); len(levels) != 4 || levels[0].(map[string]any)["id"] != "restricted" {
		t.Fatal(levels)
	}
	if archived := object(send(nil, member, http.MethodGet, "/rest/api/3/classification-levels?status=ARCHIVED", "", http.StatusOK))["classifications"].([]any); len(archived) != 0 {
		t.Fatal(archived)
	}
	send(nil, member, http.MethodGet, "/rest/api/3/classification-levels?orderBy=name", "", http.StatusBadRequest)
	classification := "/rest/api/3/project/" + projectKey + "/classification-level/default"
	if empty := object(send(nil, member, http.MethodGet, classification, "", http.StatusOK)); len(empty) != 0 {
		t.Fatal(empty)
	}
	send(nil, member, http.MethodPut, classification, `{"id":"confidential"}`, http.StatusUnauthorized)
	send(nil, admin, http.MethodPut, classification, `{"id":"nope"}`, http.StatusBadRequest)
	send(nil, admin, http.MethodPut, classification, `{"id":"confidential"}`, http.StatusNoContent)
	if level := object(send(nil, member, http.MethodGet, classification, "", http.StatusOK)); level["id"] != "confidential" {
		t.Fatal(level)
	}
	config := object(send(nil, admin, http.MethodGet, "/rest/api/3/project/"+projectKey+"/classification-config", "", http.StatusOK))
	if config["projectDefaultClassificationLevel"].(map[string]any)["id"] != "confidential" || len(config["permittedClassificationLevels"].([]any)) != 4 {
		t.Fatal(config)
	}
	send(nil, admin, http.MethodDelete, classification, "", http.StatusNoContent)

	// Data policies are for apps.
	send(nil, admin, http.MethodGet, "/rest/api/3/data-policy", "", http.StatusForbidden)
	if policy := object(send(connectApp, "", http.MethodGet, "/rest/api/3/data-policy", "", http.StatusOK)); policy["anyContentBlocked"] != false {
		t.Fatal(policy)
	}
	policies := object(send(connectApp, "", http.MethodGet, "/rest/api/3/data-policy/project?ids="+projectID, "", http.StatusOK))["projectDataPolicies"].([]any)
	if len(policies) != 1 || fmt.Sprint(policies[0].(map[string]any)["id"]) != projectID {
		t.Fatal(policies)
	}
	send(connectApp, "", http.MethodGet, "/rest/api/3/data-policy/project?ids=abc", "", http.StatusBadRequest)

	// Licensing.
	if license := object(send(nil, member, http.MethodGet, "/rest/api/3/instance/license", "", http.StatusOK)); len(license["applications"].([]any)) == 0 {
		t.Fatal(license)
	}
	send(nil, member, http.MethodGet, "/rest/api/3/license/approximateLicenseCount", "", http.StatusForbidden)
	if count := object(send(nil, admin, http.MethodGet, "/rest/api/3/license/approximateLicenseCount", "", http.StatusOK)); count["key"] != "jira" || count["value"] == "0" {
		t.Fatal(count)
	}
	if count := object(send(nil, admin, http.MethodGet, "/rest/api/3/license/approximateLicenseCount/product/jira-software", "", http.StatusOK)); count["key"] != "jira-software" {
		t.Fatal(count)
	}
	send(nil, admin, http.MethodGet, "/rest/api/3/license/approximateLicenseCount/product/jira-bogus", "", http.StatusBadRequest)

	// Audit records come from the site's audit log.
	exec(`INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,'workflow.created','workflow','wf_audit','{"name":"Delivery flow","ip":"10.0.0.1"}' FROM sites WHERE workspace_id=$1`, ws, admin)
	send(nil, member, http.MethodGet, "/rest/api/3/auditing/record", "", http.StatusForbidden)
	audit := object(send(nil, admin, http.MethodGet, "/rest/api/3/auditing/record?filter=delivery%20workflow", "", http.StatusOK))
	records := audit["records"].([]any)
	if fmt.Sprint(audit["total"]) != "1" || len(records) != 1 {
		t.Fatal(audit)
	}
	record := records[0].(map[string]any)
	if record["summary"] != "Workflow created" || record["category"] != "workflows" || record["remoteAddress"] != "10.0.0.1" || record["objectItem"].(map[string]any)["name"] != "Delivery flow" {
		t.Fatal(record)
	}
	if none := object(send(nil, admin, http.MethodGet, "/rest/api/3/auditing/record?from=2999-01-01", "", http.StatusOK)); fmt.Sprint(none["total"]) != "0" {
		t.Fatal(none)
	}
	send(nil, admin, http.MethodGet, "/rest/api/3/auditing/record?from=soon", "", http.StatusBadRequest)

	// Project statuses per issue type and the issue type hierarchy.
	statuses := list(send(nil, member, http.MethodGet, "/rest/api/3/project/"+projectKey+"/statuses", "", http.StatusOK))
	if len(statuses) == 0 || len(statuses[0].(map[string]any)["statuses"].([]any)) == 0 {
		t.Fatal(statuses)
	}
	hierarchy := object(send(nil, member, http.MethodGet, "/rest/api/3/project/"+projectID+"/hierarchy", "", http.StatusOK))
	if fmt.Sprint(hierarchy["projectId"]) != projectID || len(hierarchy["hierarchy"].([]any)) == 0 {
		t.Fatal(hierarchy)
	}
	send(nil, member, http.MethodGet, "/rest/api/3/project/"+projectKey+"/hierarchy", "", http.StatusBadRequest)

	// Labels page like Jira's PageBean.
	labels := object(send(nil, member, http.MethodGet, "/rest/api/3/label?maxResults=2", "", http.StatusOK))
	if fmt.Sprint(labels["total"]) != "3" || fmt.Sprint(labels["values"]) != "[alpha beta]" || labels["isLast"] != false || labels["nextPage"] == nil {
		t.Fatal(labels)
	}
	if last := object(send(nil, member, http.MethodGet, "/rest/api/3/label?startAt=2", "", http.StatusOK)); fmt.Sprint(last["values"]) != "[gamma]" || last["isLast"] != true {
		t.Fatal(last)
	}

	// Server information.
	info := object(send(nil, "", http.MethodGet, "/rest/api/3/serverInfo", "", http.StatusOK))
	if info["deploymentType"] != "Cloud" || len(info["versionNumbers"].([]any)) != 3 || info["serverTime"] == nil || info["product"] != nil {
		t.Fatal(info)
	}

	// Internal worklog keys.
	worklog := object(send(nil, admin, http.MethodPost, "/rest/api/3/issue/"+projectKey+"-1/worklog", `{"timeSpentSeconds":60}`, http.StatusCreated))
	issueBean := object(send(nil, admin, http.MethodGet, "/rest/api/3/issue/"+projectKey+"-1", "", http.StatusOK))
	keys := object(send(nil, admin, http.MethodPost, "/rest/internal/api/latest/worklog/bulk",
		`{"requests":[{"issueId":`+fmt.Sprint(issueBean["id"])+`,"worklogId":`+fmt.Sprint(worklog["id"])+`},{"issueId":`+fmt.Sprint(issueBean["id"])+`,"worklogId":999999999}]}`, http.StatusOK))
	if found := keys["worklogs"].([]any); len(found) != 1 || fmt.Sprint(found[0].(map[string]any)["worklogId"]) != fmt.Sprint(worklog["id"]) {
		t.Fatal(keys)
	}
	send(nil, admin, http.MethodPost, "/rest/internal/api/latest/worklog/bulk", `{"requests":[]}`, http.StatusBadRequest)
}
