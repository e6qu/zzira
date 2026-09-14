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

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestWorkflowHistoryAndAppRules covers workflow version history, the calling
// app's transition rule configuration and the bulk workflow read.
func TestWorkflowHistoryAndAppRules(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Workflow history')`, ws)
	for _, identity := range []struct{ id, role string }{{admin, "admin"}, {member, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, identity.id, identity.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, ws, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
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
	rulesApp := installApp("rules-"+strings.ToLower(store.NewID("k")), "connect")
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
			`DELETE FROM app_installations WHERE workspace_id=$1`, `DELETE FROM workflows WHERE workspace_id=$1`, `DELETE FROM statuses WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`,
		} {
			exec(query, ws)
		}
		for _, user := range append(principals, admin, member) {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, BaseURL: "https://zzira.test", WorkspaceSlug: ws}
	send := func(app *models.AppInstallation, user, method, path, body string, want int) map[string]any {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if app != nil {
			request = request.WithContext(apps.ContextWithInstallation(context.Background(), app))
		} else {
			request.SetBasicAuth(user+"@example.test", user)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		if len(response.Body.Bytes()) > 0 {
			if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
				t.Fatal(response.Body.String(), err)
			}
		}
		return out
	}

	name := "App rules " + store.NewID("w")
	appRule := func(kind, key, config, tag, appKey string) string {
		rule := `{"ruleKey":"connect:remote-workflow-` + kind + `","parameters":{"appKey":"` + appKey + `","key":"` + key + `","config":` + fmt.Sprintf("%q", config)
		if tag != "" {
			rule += `,"tag":"` + tag + `"`
		}
		return rule + `}}`
	}
	createBody := `{"scope":{"type":"GLOBAL"},"statuses":[{"id":"st_todo","name":"To Do","statusCategory":"TODO","statusReference":"todo"},{"id":"st_done","name":"Done","statusCategory":"DONE","statusReference":"done"}],
		"workflows":[{"name":"` + name + `","description":"Apps decide","statuses":[{"statusReference":"todo","properties":{}},{"statusReference":"done","properties":{}}],
		"transitions":[{"id":"11","name":"Complete","type":"DIRECTED","toStatusReference":"done","links":[{"fromStatusReference":"todo"}],
		"conditions":{"operation":"ALL","conditions":[` + appRule("condition", "approval-check", `{"level":1}`, "", rulesApp.Key) + `],"conditionGroups":[]},
		"validators":[` + appRule("validator", "notes-check", `{}`, "legacy", rulesApp.Key) + `],
		"actions":[` + appRule("post-function", "notify", `{"channel":"ops"}`, "", rulesApp.Key) + `,` + appRule("post-function", "foreign", `{}`, "", "someone-else") + `]}]}]}`
	created := send(nil, admin, http.MethodPost, "/rest/api/3/workflows/create", createBody, http.StatusOK)
	entityID := fmt.Sprint(created["workflows"].([]any)[0].(map[string]any)["id"])

	// The calling app sees only its own rules.
	send(nil, admin, http.MethodGet, "/rest/api/3/workflow/rule/config?types=postfunction", "", http.StatusForbidden)
	send(rulesApp, "", http.MethodGet, "/rest/api/3/workflow/rule/config", "", http.StatusBadRequest)
	listing := send(rulesApp, "", http.MethodGet, "/rest/api/3/workflow/rule/config?types=postfunction,condition,validator&expand=transition&workflowNames="+strings.ReplaceAll(name, " ", "%20"), "", http.StatusOK)
	entries := listing["values"].([]any)
	if len(entries) != 1 {
		t.Fatal(listing)
	}
	entry := entries[0].(map[string]any)
	postFunctions, conditions, validators := entry["postFunctions"].([]any), entry["conditions"].([]any), entry["validators"].([]any)
	if len(postFunctions) != 1 || len(conditions) != 1 || len(validators) != 1 ||
		postFunctions[0].(map[string]any)["key"] != "notify" || postFunctions[0].(map[string]any)["transition"].(map[string]any)["name"] != "Complete" {
		t.Fatal(entry)
	}
	postFunctionID := fmt.Sprint(postFunctions[0].(map[string]any)["id"])
	validatorID := fmt.Sprint(validators[0].(map[string]any)["id"])
	if tagged := send(rulesApp, "", http.MethodGet, "/rest/api/3/workflow/rule/config?types=validator&withTags=legacy", "", http.StatusOK); len(tagged["values"].([]any)) != 1 {
		t.Fatal(tagged)
	}
	if untagged := send(rulesApp, "", http.MethodGet, "/rest/api/3/workflow/rule/config?types=postfunction&withTags=legacy", "", http.StatusOK); len(untagged["values"].([]any)) != 0 {
		t.Fatal(untagged)
	}

	// Updating a rule's configuration publishes a new version.
	updated := send(rulesApp, "", http.MethodPut, "/rest/api/3/workflow/rule/config", `{"workflows":[{"workflowId":{"name":"`+name+`","draft":false},
		"postFunctions":[{"id":"`+postFunctionID+`","configuration":{"value":"{\"channel\":\"eng\"}","disabled":true,"tag":"v2"}}],
		"validators":[{"id":"missing","configuration":{"value":"{}"}}]}]}`, http.StatusOK)
	result := updated["updateResults"].([]any)[0].(map[string]any)
	if len(result["updateErrors"].([]any)) != 0 || result["ruleUpdateErrors"].(map[string]any)["missing"] == nil {
		t.Fatal(updated)
	}
	reread := send(rulesApp, "", http.MethodGet, "/rest/api/3/workflow/rule/config?types=postfunction&withTags=v2", "", http.StatusOK)["values"].([]any)
	if len(reread) != 1 {
		t.Fatal(reread)
	}
	configuration := reread[0].(map[string]any)["postFunctions"].([]any)[0].(map[string]any)["configuration"].(map[string]any)
	if configuration["value"] != `{"channel":"eng"}` || configuration["disabled"] != true {
		t.Fatal(configuration)
	}
	missing := send(rulesApp, "", http.MethodPut, "/rest/api/3/workflow/rule/config", `{"workflows":[{"workflowId":{"name":"No such workflow"}}]}`, http.StatusOK)
	if len(missing["updateResults"].([]any)[0].(map[string]any)["updateErrors"].([]any)) != 1 {
		t.Fatal(missing)
	}

	// History lists both versions and reads each.
	send(nil, member, http.MethodPost, "/rest/api/3/workflow/history/list", `{"workflowId":"`+entityID+`"}`, http.StatusForbidden)
	send(nil, admin, http.MethodPost, "/rest/api/3/workflow/history/list", `{}`, http.StatusBadRequest)
	history := send(nil, admin, http.MethodPost, "/rest/api/3/workflow/history/list", `{"workflowId":"`+entityID+`"}`, http.StatusOK)["entries"].([]any)
	if len(history) != 2 || fmt.Sprint(history[0].(map[string]any)["workflowVersion"]) != "2" || history[0].(map[string]any)["workflowId"] != entityID {
		t.Fatal(history)
	}
	first := send(nil, admin, http.MethodPost, "/rest/api/3/workflow/history", `{"workflowId":"`+entityID+`","version":1}`, http.StatusOK)
	document := first["workflows"].([]any)[0].(map[string]any)
	if document["name"] != name || fmt.Sprint(document["version"].(map[string]any)["versionNumber"]) != "1" || len(first["statuses"].([]any)) == 0 {
		t.Fatal(first)
	}
	if !strings.Contains(fmt.Sprint(document["transitions"]), `{"channel":"ops"}`) {
		t.Fatalf("version 1 should keep its original configuration: %v", document["transitions"])
	}
	send(nil, admin, http.MethodPost, "/rest/api/3/workflow/history", `{"workflowId":"`+entityID+`","version":99}`, http.StatusBadRequest)

	// Deleting rules is for Connect apps.
	send(forgeApp, "", http.MethodPut, "/rest/api/3/workflow/rule/config/delete", `{"workflows":[]}`, http.StatusForbidden)
	deleted := send(rulesApp, "", http.MethodPut, "/rest/api/3/workflow/rule/config/delete", `{"workflows":[{"workflowId":{"name":"`+name+`"},"workflowRuleIds":["`+validatorID+`","nope"]}]}`, http.StatusOK)
	if deleted["updateResults"].([]any)[0].(map[string]any)["ruleUpdateErrors"].(map[string]any)["nope"] == nil {
		t.Fatal(deleted)
	}
	if remaining := send(rulesApp, "", http.MethodGet, "/rest/api/3/workflow/rule/config?types=validator", "", http.StatusOK); len(remaining["values"].([]any)) != 0 {
		t.Fatal(remaining)
	}

	// Bulk workflow read.
	bulk := send(nil, admin, http.MethodPost, "/rest/api/3/workflows", `{"workflowNames":["`+name+`"]}`, http.StatusOK)
	if len(bulk["workflows"].([]any)) != 1 || len(bulk["statuses"].([]any)) == 0 {
		t.Fatal(bulk)
	}
	if everything := send(nil, admin, http.MethodPost, "/rest/api/3/workflows", `{}`, http.StatusOK); len(everything["workflows"].([]any)) < 2 {
		t.Fatal(everything)
	}
}
