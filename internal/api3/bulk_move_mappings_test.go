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

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestBulkMoveMappingsAndBulkNotifications covers Jira's bulk move mappings
// and bulk change emails: sub-tasks move with their parent, classifications
// and required fields are mapped or inferred, and a bulk operation sends one
// email per recipient only when asked.
func TestBulkMoveMappingsAndBulkNotifications(t *testing.T) {
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
	workspaceID, adminID, watcherID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	stamp := time.Now().UnixNano() % 1000000
	sourceKey, destinationKey := fmt.Sprintf("BS%06d", stamp), fmt.Sprintf("BD%06d", stamp)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Bulk move mappings')`, workspaceID)
	for _, identity := range []struct{ id, role, name string }{{adminID, "admin", "Bulk Admin"}, {watcherID, "member", "Bulk Watcher"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", identity.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM email_outbox WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, watcherID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	service := &commands.Service{Store: st}
	h := &Handler{Store: st, Commands: service, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(adminID+"@example.test", adminID)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	object := func(body string) map[string]any {
		t.Helper()
		decoded := map[string]any{}
		if decodeErr := json.Unmarshal([]byte(body), &decoded); decodeErr != nil {
			t.Fatalf("decode %s: %v", body, decodeErr)
		}
		return decoded
	}
	createProject := func(key string) string {
		t.Helper()
		return fmt.Sprintf("%.0f", object(call(http.MethodPost, "/rest/api/3/project", `{"key":"`+key+`","name":"Bulk `+key+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated))["id"])
	}
	sourceID, destinationID := createProject(sourceKey), createProject(destinationKey)
	exec(`UPDATE projects SET default_classification_level='internal' WHERE workspace_id=$1 AND key=$2`, workspaceID, destinationKey)

	// The watcher hears about moved and deleted work.
	var schemes struct {
		Values []struct {
			ID int64 `json:"id"`
		} `json:"values"`
	}
	if err = json.Unmarshal([]byte(call(http.MethodGet, "/rest/api/3/notificationscheme", "", http.StatusOK)), &schemes); err != nil || len(schemes.Values) == 0 {
		t.Fatalf("schemes = %+v err=%v", schemes, err)
	}
	schemePath := fmt.Sprintf("/rest/api/3/notificationscheme/%d/notification", schemes.Values[0].ID)
	for _, event := range []string{"9", "10"} {
		request := httptest.NewRequest(http.MethodPut, schemePath, strings.NewReader(`{"notificationSchemeEvents":[{"event":{"id":"`+event+`"},"notifications":[{"notificationType":"AllWatchers"}]}]}`))
		request.SetBasicAuth(adminID+"@example.test", adminID)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code >= 300 {
			t.Fatalf("scheme event %s: %d %s", event, response.Code, response.Body.String())
		}
	}

	// The destination requires a Release ring dropdown.
	ringField := object(call(http.MethodPost, "/rest/api/3/field", `{"name":"Release ring `+destinationKey+`","type":"select"}`, http.StatusCreated))["id"].(string)
	var contexts struct {
		Values []struct {
			ID any `json:"id"`
		} `json:"values"`
	}
	if err = json.Unmarshal([]byte(call(http.MethodGet, "/rest/api/3/field/"+ringField+"/context", "", http.StatusOK)), &contexts); err != nil || len(contexts.Values) == 0 {
		t.Fatalf("contexts = %+v err=%v", contexts, err)
	}
	call(http.MethodPost, fmt.Sprintf("/rest/api/3/field/%s/context/%v/option", ringField, contexts.Values[0].ID), `{"options":[{"value":"Canary"}]}`, http.StatusOK)
	var configurations struct {
		Values []struct {
			ID        any  `json:"id"`
			IsDefault bool `json:"isDefault"`
		} `json:"values"`
	}
	if err = json.Unmarshal([]byte(call(http.MethodGet, "/rest/api/3/fieldconfiguration", "", http.StatusOK)), &configurations); err != nil {
		t.Fatal(err)
	}
	for _, configuration := range configurations.Values {
		if configuration.IsDefault {
			request := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/rest/api/3/fieldconfiguration/%v/fields", configuration.ID), strings.NewReader(`{"fieldConfigurationItems":[{"id":"`+ringField+`","isRequired":true}]}`))
			request.SetBasicAuth(adminID+"@example.test", adminID)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code >= 300 {
				t.Fatalf("require field: %d %s", response.Code, response.Body.String())
			}
		}
	}

	createIssue := func(fields string) map[string]any {
		t.Helper()
		return object(call(http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+sourceKey+`"},`+fields+`}}`, http.StatusCreated))
	}
	parent := createIssue(`"summary":"Parent work","issuetype":{"name":"Task"},"` + ringField + `":{"value":"Canary"}`)
	parentKey := parent["key"].(string)
	subtask := createIssue(`"summary":"Child work","issuetype":{"name":"Sub-task"},"parent":{"key":"` + parentKey + `"},"` + ringField + `":{"value":"Canary"}`)
	subtaskKey := subtask["key"].(string)
	// Every project uses the default field configuration, so the value is set
	// at creation and removed below to leave the work item without it.
	unclassified := createIssue(`"summary":"Unclassified work","issuetype":{"name":"Task"},"` + ringField + `":{"value":"Canary"}`)
	unclassifiedKey := unclassified["key"].(string)
	storedID := func(key string) string {
		t.Helper()
		var id string
		if lookupErr := st.Pool.QueryRow(ctx, `SELECT id FROM issues WHERE workspace_id=$1 AND key=$2`, workspaceID, key).Scan(&id); lookupErr != nil {
			t.Fatal(lookupErr)
		}
		return id
	}
	parentStoredID, unclassifiedStoredID := storedID(parentKey), storedID(unclassifiedKey)
	exec(`UPDATE issues SET classification_level='confidential' WHERE workspace_id=$1 AND key IN ($2,$3)`, workspaceID, parentKey, subtaskKey)
	exec(`UPDATE issues SET classification_level=NULL, fields=fields - $3 WHERE workspace_id=$1 AND key=$2`, workspaceID, unclassifiedKey, ringField)
	for _, key := range []string{parentKey, subtaskKey, unclassifiedKey} {
		call(http.MethodPost, "/rest/api/3/issue/"+key+"/watchers", `"`+watcherID+`"`, http.StatusNoContent)
	}
	taskTypeID := parent["fields"]
	_ = taskTypeID
	var issueTypeID string
	if err = st.Pool.QueryRow(ctx, `SELECT issuetype_id FROM issues WHERE workspace_id=$1 AND key=$2`, workspaceID, parentKey).Scan(&issueTypeID); err != nil {
		t.Fatal(err)
	}
	var wireTypeID string
	if err = st.Pool.QueryRow(ctx, `SELECT COALESCE(jira_id::text, id) FROM issue_types WHERE id=$1`, issueTypeID).Scan(&wireTypeID); err != nil {
		wireTypeID = issueTypeID
	}
	runner := &store.APITaskRunner{Store: st, BulkIssueExecutor: service}
	result := func(taskID string) map[string]any {
		t.Helper()
		if drainErr := runner.DrainOnce(ctx, workspaceID); drainErr != nil {
			t.Fatal(drainErr)
		}
		task := object(call(http.MethodGet, "/rest/api/3/task/"+taskID, "", http.StatusOK))
		if task["status"] != "COMPLETE" {
			t.Fatalf("task = %v", task)
		}
		outcome, _ := task["result"].(map[string]any)
		return outcome
	}
	submitMove := func(mapping string) string {
		t.Helper()
		return object(call(http.MethodPost, "/rest/api/3/bulk/issues/move", `{"targetToSourcesMapping":{"`+destinationID+`,`+wireTypeID+`":`+mapping+`}}`, http.StatusCreated))["taskId"].(string)
	}

	// Without mappings for its classification the parent is refused.
	refused := result(submitMove(`{"inferClassificationDefaults":false,"inferFieldDefaults":true,"inferStatusDefaults":true,"inferSubtaskTypeDefault":true,"issueIdsOrKeys":["` + parentKey + `"]}`))
	if !strings.Contains(fmt.Sprint(refused["failedAccessibleIssues"]), "requires a target classification") {
		t.Fatalf("unmapped classification result = %v", refused)
	}

	// Mapped classification and retained field values move the parent and its
	// sub-task together, with one bulk email to the watcher.
	moved := result(submitMove(`{"inferClassificationDefaults":false,"inferFieldDefaults":false,"inferStatusDefaults":true,"inferSubtaskTypeDefault":true,"issueIdsOrKeys":["` + parentKey + `"],
		"targetClassification":[{"classifications":{"restricted":["confidential"]}}],
		"targetMandatoryFields":[{"fields":{"` + ringField + `":{"retain":true,"type":"raw","value":["Canary"]}}}]}`))
	if processed, _ := moved["processedAccessibleIssues"].([]any); len(processed) != 2 {
		t.Fatalf("moved result = %v", moved)
	}
	var parentProject, subtaskProject, subtaskParent, parentLevel, subtaskLevel string
	if err = st.Pool.QueryRow(ctx, `SELECT p.id, COALESCE(i.classification_level,'') FROM issues i JOIN projects p ON p.id=i.project_id WHERE i.id=$1`, parentStoredID).Scan(&parentProject, &parentLevel); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT project_id, COALESCE(parent_id,''), COALESCE(classification_level,'') FROM issues WHERE workspace_id=$1 AND id IN (SELECT id FROM issues WHERE workspace_id=$1 AND parent_id IS NOT NULL)`, workspaceID).Scan(&subtaskProject, &subtaskParent, &subtaskLevel); err != nil {
		t.Fatal(err)
	}
	if parentProject != destinationID || subtaskProject != destinationID || parentLevel != "restricted" || subtaskLevel != "restricted" || subtaskParent == "" {
		t.Fatalf("parent in %s (%s), sub-task in %s under %q (%s)", parentProject, parentLevel, subtaskProject, subtaskParent, subtaskLevel)
	}
	var bulkEmails, singleEmails int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE dedupe_key LIKE 'bulk-notification:%'), count(*) FILTER (WHERE dedupe_key LIKE 'issue-notification:%') FROM email_outbox WHERE workspace_id=$1 AND recipient=$2`, workspaceID, watcherID+"@example.test").Scan(&bulkEmails, &singleEmails); err != nil {
		t.Fatal(err)
	}
	if bulkEmails != 1 {
		t.Fatalf("bulk emails = %d, single emails = %d", bulkEmails, singleEmails)
	}

	// An unclassified work item adopts the destination default, but its missing
	// required value is refused until one is provided.
	missing := result(submitMove(`{"inferClassificationDefaults":true,"inferFieldDefaults":true,"inferStatusDefaults":true,"inferSubtaskTypeDefault":true,"issueIdsOrKeys":["` + unclassifiedKey + `"]}`))
	if !strings.Contains(fmt.Sprint(missing["failedAccessibleIssues"]), "is required in the destination") {
		t.Fatalf("missing field result = %v", missing)
	}
	filled := result(submitMove(`{"inferClassificationDefaults":true,"inferFieldDefaults":false,"inferStatusDefaults":true,"inferSubtaskTypeDefault":true,"issueIdsOrKeys":["` + unclassifiedKey + `"],
		"targetMandatoryFields":[{"fields":{"` + ringField + `":{"retain":true,"type":"raw","value":["Canary"]}}}]}`))
	if processed, _ := filled["processedAccessibleIssues"].([]any); len(processed) != 1 {
		t.Fatalf("filled result = %v", filled)
	}
	var adoptedLevel string
	var ring []byte
	if err = st.Pool.QueryRow(ctx, `SELECT COALESCE(classification_level,''), fields->$3 FROM issues WHERE workspace_id=$1 AND id=$2`, workspaceID, unclassifiedStoredID, ringField).Scan(&adoptedLevel, &ring); err != nil {
		t.Fatal(err)
	}
	// Option values are stored by option id.
	var canaryOption string
	if err = st.Pool.QueryRow(ctx, `SELECT o.id::text FROM custom_field_options o JOIN custom_field_contexts c ON c.id=o.context_id WHERE c.field_id=$1 AND o.value='Canary'`, ringField).Scan(&canaryOption); err != nil {
		t.Fatal(err)
	}
	if adoptedLevel != "internal" || !strings.Contains(string(ring), canaryOption) {
		t.Fatalf("adopted level = %q ring = %s, want option %s", adoptedLevel, ring, canaryOption)
	}

	// A bulk delete asked not to notify sends no email.
	exec(`DELETE FROM email_outbox WHERE workspace_id=$1`, workspaceID)
	silent := object(call(http.MethodPost, "/rest/api/3/bulk/issues/delete", `{"selectedIssueIdsOrKeys":["`+unclassifiedKey+`"],"sendBulkNotification":false}`, http.StatusCreated))["taskId"].(string)
	result(silent)
	var emails int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE workspace_id=$1`, workspaceID).Scan(&emails); err != nil || emails != 0 {
		t.Fatalf("emails after a silent bulk delete = %d err=%v", emails, err)
	}
	_ = sourceID
}
