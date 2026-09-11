package api3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/secretbox"
	"github.com/e6qu/zzira/internal/store"
)

// TestAppFieldOptions pins Jira's issue field option surface, which manages the
// options of a select list an app provides. The separation from the
// context-scoped options an administrator manages is the point: neither
// resource may be used on the other's fields.
func TestAppFieldOptions(t *testing.T) {
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
	workspaceID, adminID, memberID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'App field options')`, workspaceID)
	for _, value := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Option user')`, value.id, value.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, value.id, value.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, value.id, store.HashToken(value.id))
	}

	// An app that provides a select list is what this surface exists for.
	appKey := "options." + strings.ReplaceAll(strings.ToLower(store.NewID("test")), "_", "-")
	descriptorRaw := []byte(fmt.Sprintf(`{"key":%q,"name":"Option provider","baseUrl":"https://apps.example.test/options","authentication":{"type":"jwt"},"scopes":[],"modules":{"jiraIssueFields":[{"key":"impact-band","name":{"value":"Impact band"},"description":{"value":"How much this hurts"},"type":"single_select"}]}}`, appKey))
	descriptor, err := apps.ParseDescriptor(descriptorRaw)
	if err != nil {
		t.Fatalf("an app could not declare a single_select field: %v", err)
	}
	box, err := secretbox.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := box.Seal([]byte("a-test-shared-secret-that-is-long-enough"), workspaceID+"/"+appKey)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := st.InstallApp(ctx, workspaceID, adminID, descriptor, descriptorRaw, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM api_tasks WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM app_installations WHERE id=$1`, installation.ID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM organization_audit_events WHERE target_type='app' AND target_id=$1`, appKey)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE user_id=$1`, installation.PrincipalID)
		exec(`DELETE FROM users WHERE id=$1`, installation.PrincipalID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	fieldKey := appKey + "__impact-band"
	var appFieldID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM custom_fields WHERE app_installation_id=$1 AND app_module_key='impact-band'`,
		installation.ID).Scan(&appFieldID); err != nil {
		t.Fatal(err)
	}

	handler := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(userID, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(userID+"@example.test", userID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	decode := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, response.Body.String())
		}
		return out
	}
	text := func(value any) string {
		switch typed := value.(type) {
		case string:
			return typed
		case float64:
			return strconv.FormatInt(int64(typed), 10)
		}
		return ""
	}
	call(adminID, "POST", "/rest/api/3/project", `{"key":"OPT","name":"Option work","projectTypeKey":"software","leadAccountId":"`+adminID+`"}`, 201)
	var projectID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND key='OPT'`, workspaceID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}

	base := "/rest/api/3/field/" + fieldKey + "/option"
	if empty := decode(call(adminID, "GET", base, "", 200)); empty["total"].(float64) != 0 {
		t.Fatalf("a new select list starts empty: %v", empty)
	}
	high := decode(call(adminID, "POST", base, `{"value":"High","properties":{"weight":3}}`, 201))
	highID := text(high["id"])
	if highID == "" || high["value"] != "High" {
		t.Fatalf("created option: %v", high)
	}
	if properties, _ := high["properties"].(map[string]any); properties["weight"].(float64) != 3 {
		t.Fatalf("option properties: %v", high)
	}
	low := decode(call(adminID, "POST", base, `{"value":"Low"}`, 201))
	lowID := text(low["id"])
	// A duplicate value on the same select list is refused.
	call(adminID, "POST", base, `{"value":"High"}`, 400)
	call(adminID, "POST", base, `{"value":"   "}`, 400)

	listed := decode(call(adminID, "GET", base, "", 200))
	if listed["total"].(float64) != 2 {
		t.Fatalf("option list: %v", listed)
	}
	read := decode(call(adminID, "GET", base+"/"+highID, "", 200))
	if read["value"] != "High" {
		t.Fatalf("option read: %v", read)
	}
	call(adminID, "GET", base+"/99999999", "", 404)

	// The update replaces the value and can mark an option unselectable.
	updated := decode(call(adminID, "PUT", base+"/"+lowID,
		`{"value":"Low (retiring)","config":{"attributes":["notSelectable"]}}`, 200))
	if updated["value"] != "Low (retiring)" {
		t.Fatalf("option update: %v", updated)
	}
	config, _ := updated["config"].(map[string]any)
	attributes, _ := config["attributes"].([]any)
	if len(attributes) != 1 || attributes[0] != "notSelectable" {
		t.Fatalf("option attributes: %v", updated)
	}

	// Suggestions: search shows everything the user may see, edit only what
	// they may choose.
	search := decode(call(memberID, "GET", base+"/suggestions/search", "", 200))
	if search["total"].(float64) != 2 {
		t.Fatalf("search suggestions: %v", search)
	}
	edit := decode(call(memberID, "GET", base+"/suggestions/edit", "", 200))
	if edit["total"].(float64) != 1 {
		t.Fatalf("edit suggestions should drop the unselectable option: %v", edit)
	}

	// A per-option project scope narrows where the option is offered.
	scoped := decode(call(adminID, "POST", base,
		`{"value":"Only here","config":{"scope":{"projects":["`+projectID+`"]}}}`, 201))
	scopedID := text(scoped["id"])
	inScope := decode(call(memberID, "GET", base+"/suggestions/edit?projectId="+projectID, "", 200))
	if inScope["total"].(float64) != 2 {
		t.Fatalf("scoped option missing from its own project: %v", inScope)
	}
	outOfScope := decode(call(memberID, "GET", base+"/suggestions/edit?projectId=prj_elsewhere", "", 200))
	if outOfScope["total"].(float64) != 1 {
		t.Fatalf("scoped option offered outside its project: %v", outOfScope)
	}
	call(adminID, "DELETE", base+"/"+scopedID, "", 204)

	// The two option resources do not overlap, in either direction.
	call(adminID, "GET", "/rest/api/3/field/"+appFieldID+"/context/10000/option", "", 400)
	workspaceField := decode(call(adminID, "POST", "/rest/api/3/field", `{"name":"Locally made","type":"select"}`, 201))
	call(adminID, "GET", "/rest/api/3/field/"+text(workspaceField["id"])+"/option", "", 400)

	// An option a work item uses cannot simply be deleted.
	issue := call(adminID, "POST", "/rest/api/3/issue",
		`{"fields":{"project":{"key":"OPT"},"summary":"Uses the option","issuetype":{"name":"Task"},"`+appFieldID+`":"`+highID+`"}}`, 201)
	var work struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(issue.Body.Bytes(), &work); err != nil || work.Key == "" {
		t.Fatalf("created work item: %v %s", err, issue.Body.String())
	}
	call(adminID, "DELETE", base+"/"+highID, "", 409)

	// Deselecting is the way out, and it runs as an ordinary background task.
	// The replacement has to be one a contributor could have chosen, so the
	// unselectable option is not a candidate.
	medium := decode(call(adminID, "POST", base, `{"value":"Medium"}`, 201))
	mediumID := text(medium["id"])
	replaceWith := decode(call(adminID, "DELETE", base+"/"+highID+"/issue?replaceWith="+mediumID, "", 303))
	taskID, _ := replaceWith["id"].(string)
	if taskID == "" {
		t.Fatalf("deselect task: %v", replaceWith)
	}
	call(adminID, "DELETE", base+"/"+highID+"/issue?replaceWith="+highID, "", 400)
	call(adminID, "DELETE", base+"/"+highID+"/issue?replaceWith=99999999", "", 400)
	// An unselectable replacement is refused up front. Letting it through
	// reports a completed deselect that changed nothing, which is worse than
	// saying no.
	call(adminID, "DELETE", base+"/"+highID+"/issue?replaceWith="+lowID, "", 400)
	runner := &store.APITaskRunner{Store: st, BulkIssueExecutor: handler.Commands}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatalf("running the deselect task: %v", err)
	}
	// A task that failed would otherwise leave the work item unchanged and
	// look like a missing replacement.
	finished, err := st.APITaskByID(ctx, workspaceID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != "COMPLETE" {
		t.Fatalf("deselect task did not complete: %s %s", finished.Status, finished.Message)
	}
	moved := call(adminID, "GET", "/rest/api/3/issue/"+work.Key+"?fields="+appFieldID, "", 200)
	if !strings.Contains(moved.Body.String(), mediumID) {
		t.Fatalf("the deselect did not replace the option: %s", moved.Body.String())
	}
	// With the option no longer in use, it can be deleted.
	call(adminID, "DELETE", base+"/"+highID, "", 204)
	call(adminID, "GET", base+"/"+highID, "", 404)

	// The surface answers only for a select list an app provides.
	call(adminID, "GET", "/rest/api/3/field/nosuchapp__nothing/option", "", 404)
	call(memberID, "POST", base, `{"value":"Sneaky"}`, 403)
}
