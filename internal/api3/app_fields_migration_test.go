package api3

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestAppFieldsMigrationAndServiceRegistry covers app custom field
// configuration and values, Connect app migration and the service registry.
func TestAppFieldsMigrationAndServiceRegistry(t *testing.T) {
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
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	ws, admin, member := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'App fields and migration')`, ws)
	for _, identity := range []struct{ id, role string }{{admin, "admin"}, {member, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, identity.id, identity.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	principals := []string{}
	installApp := func(key, format string) *models.AppInstallation {
		t.Helper()
		principal := "app_principal_" + store.NewID("t")
		principals = append(principals, principal)
		exec(`INSERT INTO users(id,email,password_hash,display_name,active) VALUES($1,$2,'!app-principal!',$3,true)`, principal, principal+"@apps.zzira.invalid", key)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, ws, principal)
		exec(`INSERT INTO app_installations(workspace_id,principal_id,app_key,name,base_url,version,secret_ciphertext,descriptor,descriptor_format)
			VALUES($1,$2,$3,$3,'https://app.example.test','1','\x00'::bytea,'{}'::jsonb,$4)`, ws, principal, key, format)
		installation, err := st.AppInstallation(ctx, ws, key)
		if err != nil {
			t.Fatal(err)
		}
		return installation
	}
	suffix := strings.ToLower(store.NewID("k"))
	connectApp := installApp("legacy-"+suffix, "connect")
	forgeApp := installApp("forge-"+suffix, "zzira")
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM service_registry_services WHERE workspace_id=$1`, `DELETE FROM api_tasks WHERE workspace_id=$1`, `DELETE FROM app_installations WHERE workspace_id=$1`,
			`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`,
			`DELETE FROM projects WHERE workspace_id=$1`, `DELETE FROM workflows WHERE workspace_id=$1`, `DELETE FROM statuses WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`,
		} {
			_, _ = st.Pool.Exec(ctx, query, ws)
		}
		for _, user := range append(principals, admin, member) {
			_, _ = st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE user_id=$1`, user)
			_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user)
		}
	})

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, BaseURL: "https://zzira.test", WorkspaceSlug: ws}
	send := func(app *models.AppInstallation, user, method, path, body string, headers map[string]string, want int) any {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if app != nil {
			request = request.WithContext(apps.ContextWithInstallation(context.Background(), app))
		} else {
			request.SetBasicAuth(user+"@example.test", user)
		}
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		var out any
		if trimmed := strings.TrimSpace(response.Body.String()); trimmed != "" {
			if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
				t.Fatal(trimmed, err)
			}
		}
		return out
	}
	object := func(value any) map[string]any { return value.(map[string]any) }

	key := fmt.Sprintf("AF%04d", time.Now().UnixNano()%10000)
	send(nil, admin, "POST", "/rest/api/3/project", `{"key":"`+key+`","name":"App data","projectTypeKey":"software","leadAccountId":"`+admin+`"}`, nil, 201)
	issue := object(send(nil, admin, "POST", "/rest/api/3/issue", `{"fields":{"project":{"key":"`+key+`"},"summary":"Migrated work","issuetype":{"name":"Task"}}}`, nil, 201))
	issueID := issue["id"].(string)
	var fieldID string
	if err := st.Pool.QueryRow(ctx, `INSERT INTO custom_fields(id,name,type,description,workspace_id,app_installation_id,app_module_key)
		VALUES('customfield_'||nextval('jira_app_custom_field_id'),'Risk','text','',$1,$2,'risk-score') RETURNING id`, ws, connectApp.ID).Scan(&fieldID); err != nil {
		t.Fatal(err)
	}
	var contextID int64
	// A new custom field comes with its default context.
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM custom_field_contexts WHERE field_id=$1 ORDER BY id LIMIT 1`, fieldID).Scan(&contextID); err != nil {
		t.Fatal(err)
	}
	contextText := strconv.FormatInt(contextID, 10)

	// Configuration.
	configPath := "/rest/api/3/app/field/" + fieldID + "/context/configuration"
	send(nil, member, "GET", configPath, "", nil, 403)
	send(forgeApp, "", "GET", configPath, "", nil, 403)
	send(nil, admin, "GET", "/rest/api/3/app/field/customfield_1/context/configuration", "", nil, 404)
	listed := object(send(connectApp, "", "GET", configPath, "", nil, 200))["values"].([]any)
	if len(listed) != 1 || object(listed[0])["fieldContextId"] != contextText {
		t.Fatal(listed)
	}
	configurationID := object(listed[0])["id"].(string)
	send(connectApp, "", "PUT", configPath, `{"configurations":[{"id":"`+configurationID+`","fieldContextId":"`+contextText+`","configuration":{"minimum":1},"schema":{"type":"number"}}]}`, nil, 200)
	send(connectApp, "", "PUT", configPath, `{"configurations":[{"id":"`+configurationID+`","fieldContextId":"1"}]}`, nil, 400)
	byIssue := object(send(nil, admin, "GET", configPath+"?issueId="+issueID, "", nil, 200))["values"].([]any)
	if len(byIssue) != 1 || object(object(byIssue[0])["configuration"])["minimum"] != float64(1) || object(object(byIssue[0])["schema"])["type"] != "number" {
		t.Fatal(byIssue)
	}
	if none := object(send(nil, admin, "GET", configPath+"?issueId=999999999", "", nil, 200))["values"].([]any); len(none) != 0 {
		t.Fatal(none)
	}
	if byContext := object(send(nil, admin, "GET", configPath+"?fieldContextId="+contextText, "", nil, 200))["values"].([]any); len(byContext) != 1 {
		t.Fatal(byContext)
	}
	send(nil, admin, "GET", configPath+"?id=1&fieldContextId=2", "", nil, 400)
	send(nil, admin, "GET", configPath+"?projectKeyOrId="+key, "", nil, 400)
	bulk := object(send(nil, admin, "POST", "/rest/api/3/app/field/context/configuration/list?projectKeyOrId="+key+"&issueTypeId=Task", `{"fieldIdsOrKeys":["`+connectApp.Key+`__risk-score"]}`, nil, 200))["values"].([]any)
	if len(bulk) != 1 || object(bulk[0])["customFieldId"] != fieldID {
		t.Fatal(bulk)
	}

	// Values.
	valuePath := "/rest/api/3/app/field/" + fieldID + "/value"
	send(forgeApp, "", "PUT", valuePath, `{"updates":[{"issueIds":[`+issueID+`],"value":"high"}]}`, nil, 403)
	send(nil, admin, "PUT", valuePath, `{"updates":[{"issueIds":[`+issueID+`],"value":"high"}]}`, nil, 403)
	send(connectApp, "", "PUT", valuePath+"?generateChangelog=maybe", `{"updates":[{"issueIds":[`+issueID+`],"value":"high"}]}`, nil, 400)
	send(connectApp, "", "PUT", valuePath, `{"updates":[{"issueIds":[`+issueID+`],"value":"high"}]}`, nil, 204)
	fieldValue := func() any {
		return object(object(send(nil, admin, "GET", "/rest/api/3/issue/"+issueID+"?fields="+fieldID, "", nil, 200))["fields"])[fieldID]
	}
	if got := fieldValue(); got != "high" {
		t.Fatalf("value = %v", got)
	}
	send(connectApp, "", "POST", "/rest/api/3/app/field/value", `{"updates":[{"customField":"`+fieldID+`","issueIds":[`+issueID+`,`+issueID+`],"value":"low"}]}`, nil, 400)
	send(connectApp, "", "POST", "/rest/api/3/app/field/value", `{"updates":[{"customField":"customfield_1","issueIds":[`+issueID+`],"value":"low"}]}`, nil, 404)
	send(connectApp, "", "POST", "/rest/api/3/app/field/value", `{"updates":[{"customField":"`+connectApp.Key+`__risk-score","issueIds":[`+issueID+`],"value":"low"}]}`, nil, 204)
	if got := fieldValue(); got != "low" {
		t.Fatalf("value = %v", got)
	}

	// Connect migration.
	transfer, err := st.CreateAppMigrationTransfer(ctx, ws, admin, connectApp.ID)
	if err != nil {
		t.Fatal(err)
	}
	transferHeader := map[string]string{"Atlassian-Transfer-Id": transfer}
	fieldNumber := strings.TrimPrefix(fieldID, "customfield_")
	migrateBody := `{"updateValueList":[{"_type":"StringIssueField","fieldID":` + fieldNumber + `,"issueID":` + issueID + `,"string":"medium"}]}`
	send(connectApp, "", "PUT", "/rest/atlassian-connect/1/migration/field", migrateBody, nil, 400)
	send(connectApp, "", "PUT", "/rest/atlassian-connect/1/migration/field", migrateBody, map[string]string{"Atlassian-Transfer-Id": "0b8e5c38-0000-4000-8000-000000000000"}, 403)
	send(forgeApp, "", "PUT", "/rest/atlassian-connect/1/migration/field", migrateBody, transferHeader, 403)
	send(connectApp, "", "PUT", "/rest/atlassian-connect/1/migration/field", migrateBody, transferHeader, 200)
	if got := fieldValue(); got != "medium" {
		t.Fatalf("migrated value = %v", got)
	}
	send(connectApp, "", "PUT", "/rest/atlassian-connect/1/migration/field", `{"updateValueList":[{"_type":"MultiSelectIssueField","fieldID":`+fieldNumber+`,"issueID":`+issueID+`,"optionID":"1"}]}`, transferHeader, 400)
	send(connectApp, "", "PUT", "/rest/atlassian-connect/1/migration/properties/IssueProperty", `[{"entityId":`+issueID+`,"key":"migrated","value":"{\"ok\":true}"}]`, transferHeader, 200)
	if property := object(send(nil, admin, "GET", "/rest/api/3/issue/"+issueID+"/properties/migrated", "", nil, 200)); object(property["value"])["ok"] != true {
		t.Fatal(property)
	}
	send(connectApp, "", "PUT", "/rest/atlassian-connect/1/migration/properties/UserProperty", `[{"entityId":1,"key":"k","value":"1"}]`, transferHeader, 400)
	send(connectApp, "", "PUT", "/rest/atlassian-connect/1/migration/properties/IssueProperty", `[{"entityId":999999999,"key":"k","value":"1"}]`, transferHeader, 400)

	workflowName := "Migrated rules " + suffix
	created := object(send(nil, admin, "POST", "/rest/api/3/workflows/create", `{"scope":{"type":"GLOBAL"},"statuses":[{"id":"st_todo","name":"To Do","statusCategory":"TODO","statusReference":"todo"},{"id":"st_done","name":"Done","statusCategory":"DONE","statusReference":"done"}],
		"workflows":[{"name":"`+workflowName+`","description":"","statuses":[{"statusReference":"todo","properties":{}},{"statusReference":"done","properties":{}}],
		"transitions":[{"id":"11","name":"Complete","type":"DIRECTED","toStatusReference":"done","links":[{"fromStatusReference":"todo"}],
		"actions":[{"ruleKey":"connect:remote-workflow-post-function","parameters":{"appKey":"`+connectApp.Key+`","key":"notify","config":"{}"}}]}]}]}`, nil, 200))
	entityID := fmt.Sprint(object(created["workflows"].([]any)[0])["id"])
	rules := object(send(connectApp, "", "GET", "/rest/api/3/workflow/rule/config?types=postfunction&workflowNames="+strings.ReplaceAll(workflowName, " ", "%20"), "", nil, 200))["values"].([]any)
	ruleID := fmt.Sprint(object(object(rules[0])["postFunctions"].([]any)[0])["id"])
	search := object(send(connectApp, "", "POST", "/rest/atlassian-connect/1/migration/workflow/rule/search", `{"workflowEntityId":"`+entityID+`","ruleIds":["`+ruleID+`","missing-rule"],"expand":"transition"}`, transferHeader, 200))
	valid := search["validRules"].([]any)
	if len(valid) != 1 || len(object(valid[0])["postFunctions"].([]any)) != 1 || fmt.Sprint(search["invalidRules"]) != "[missing-rule]" {
		t.Fatal(search)
	}
	send(connectApp, "", "POST", "/rest/atlassian-connect/1/migration/workflow/rule/search", `{"workflowEntityId":"not-a-uuid","ruleIds":["x"]}`, transferHeader, 400)

	// Connect to Forge field migration tasks.
	taskPath := "/rest/atlassian-connect/1/migration/" + connectApp.Key + "/risk-score/task"
	send(nil, admin, "GET", taskPath, "", nil, 401)
	send(forgeApp, "", "GET", taskPath, "", nil, 404)
	send(connectApp, "", "GET", taskPath, "", nil, 404)
	send(connectApp, "", "POST", taskPath, "", nil, 202)
	send(connectApp, "", "POST", taskPath, "", nil, 409)
	if err := (&store.APITaskRunner{Store: st}).DrainOnce(ctx, ws); err != nil {
		t.Fatal(err)
	}
	finished := object(send(connectApp, "", "GET", taskPath, "", nil, 200))
	if finished["status"] != "COMPLETE" || object(finished["result"])["migratedValues"] != float64(1) {
		t.Fatal(finished)
	}
	send(connectApp, "", "POST", taskPath, "", nil, 202)
	if same := object(send(connectApp, "", "GET", taskPath, "", nil, 200)); same["id"] != finished["id"] {
		t.Fatal(same)
	}
	send(connectApp, "", "POST", taskPath+"?retriggerCompletedMigration=true", "", nil, 202)
	if retriggered := object(send(connectApp, "", "GET", taskPath, "", nil, 200)); retriggered["id"] == finished["id"] || retriggered["status"] != "ENQUEUED" {
		t.Fatal(retriggered)
	}

	// Service registry.
	serviceID, err := st.CreateServiceRegistryService(ctx, ws, admin, "Payments API", "Takes payments", 1)
	if err != nil {
		t.Fatal(err)
	}
	send(forgeApp, "", "GET", "/rest/atlassian-connect/1/service-registry?serviceIds="+serviceID, "", nil, 403)
	send(connectApp, "", "GET", "/rest/atlassian-connect/1/service-registry", "", nil, 400)
	services := send(connectApp, "", "GET", "/rest/atlassian-connect/1/service-registry?serviceIds="+serviceID+"&serviceIds=b:"+base64.StdEncoding.EncodeToString([]byte(serviceID))+"&serviceIds=00000000-0000-0000-0000-000000000000", "", nil, 200).([]any)
	if len(services) != 1 || object(services[0])["name"] != "Payments API" || object(object(services[0])["serviceTier"])["level"] != float64(1) || object(services[0])["revision"] != "1" {
		t.Fatal(services)
	}
}
