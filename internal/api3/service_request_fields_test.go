package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestServiceRequestTypeFieldsAndHiddenPresets covers Jira Service
// Management request type forms: field metadata carries Jira schemas, the
// options a select offers and what the caller may do; a hidden field is shown
// only to administrators who ask for it and fills requests with its preset
// value; and request type searches follow Jira's hidden and restriction filters.
func TestServiceRequestTypeFieldsAndHiddenPresets(t *testing.T) {
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
	workspaceID, adminID, customerID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	stamp := time.Now().UnixNano() % 100000
	projectKey := fmt.Sprintf("SF%05d", stamp)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Service fields')`, workspaceID)
	for _, identity := range []struct{ id, role string }{{adminID, "admin"}, {customerID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Fields "+identity.id)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	fieldIDs := []string{}
	t.Cleanup(func() {
		exec(`DELETE FROM notifications WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM service_desks WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		for _, id := range fieldIDs {
			exec(`DELETE FROM custom_fields WHERE id=$1`, id)
		}
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, customerID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	callAs := func(accountID, method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(accountID+"@example.test", accountID)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, accountID, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	call := func(method, path, body string, want int) string {
		t.Helper()
		return callAs(adminID, method, path, body, want)
	}
	object := func(body string) map[string]any {
		t.Helper()
		out := map[string]any{}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		return out
	}
	call(http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Fields desk","projectTypeKey":"service_desk","projectTemplateKey":"com.atlassian.servicedesk:simplified-it-service-management","leadAccountId":"`+adminID+`"}`, http.StatusCreated)
	var serviceDeskID, requestTypeID string
	if err = st.Pool.QueryRow(ctx, `SELECT sd.id FROM service_desks sd JOIN projects p ON p.id=sd.project_id WHERE p.workspace_id=$1 AND p.key=$2`, workspaceID, projectKey).Scan(&serviceDeskID); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 ORDER BY id::bigint LIMIT 1`, serviceDeskID).Scan(&requestTypeID); err != nil {
		t.Fatal(err)
	}

	// A select offering two options, and a text field the portal never shows.
	impactID := fmt.Sprint(object(call(http.MethodPost, "/rest/api/3/field", `{"name":"Impact `+fmt.Sprint(stamp)+`","type":"com.atlassian.jira.plugin.system.customfieldtypes:select"}`, http.StatusCreated))["id"])
	sourceID := fmt.Sprint(object(call(http.MethodPost, "/rest/api/3/field", `{"name":"Intake source `+fmt.Sprint(stamp)+`","type":"com.atlassian.jira.plugin.system.customfieldtypes:textfield"}`, http.StatusCreated))["id"])
	fieldIDs = append(fieldIDs, impactID, sourceID)
	contexts := object(call(http.MethodGet, "/rest/api/3/field/"+impactID+"/context", "", http.StatusOK))["values"].([]any)
	contextID := fmt.Sprint(contexts[0].(map[string]any)["id"])
	options := object(call(http.MethodPost, "/rest/api/3/field/"+impactID+"/context/"+contextID+"/option", `{"options":[{"value":"High"},{"value":"Low"}]}`, http.StatusOK))["options"].([]any)
	highID := fmt.Sprint(options[0].(map[string]any)["id"])

	form := []models.ServiceRequestTypeField{
		{ID: "summary", Required: true},
		{ID: impactID, Required: true},
		{ID: sourceID, Required: true, Hidden: true, PresetValue: json.RawMessage(`"Portal"`)},
	}
	if err = h.Commands.SetServiceRequestTypeFields(ctx, adminID, workspaceID, serviceDeskID, requestTypeID, form); err != nil {
		t.Fatal(err)
	}
	if err = h.Commands.SetServiceRequestTypeFields(ctx, adminID, workspaceID, serviceDeskID, requestTypeID, []models.ServiceRequestTypeField{{ID: "summary", Hidden: true}}); err == nil {
		t.Fatal("summary was hidden from the portal")
	}
	if err = h.Commands.SetServiceRequestTypeFields(ctx, adminID, workspaceID, serviceDeskID, requestTypeID, []models.ServiceRequestTypeField{{ID: "summary"}, {ID: sourceID, Required: true, Hidden: true}}); err == nil {
		t.Fatal("a hidden required field without a preset was accepted")
	}

	fieldsPath := "/rest/servicedeskapi/servicedesk/" + serviceDeskID + "/requesttype/" + requestTypeID + "/field"
	customerView := callAs(customerID, http.MethodGet, fieldsPath, "", http.StatusOK)
	for _, want := range []string{`"canRaiseOnBehalfOf":false`, `"canAddRequestParticipants":false`, `"fieldId":"` + impactID + `"`, `"label":"High"`, `"value":"` + highID + `"`,
		`"custom":"com.atlassian.jira.plugin.system.customfieldtypes:select"`, `"type":"option"`} {
		if !strings.Contains(customerView, want) {
			t.Fatalf("customer field metadata lacks %s: %s", want, customerView)
		}
	}
	if strings.Contains(customerView, sourceID) {
		t.Fatalf("a customer sees a hidden field: %s", customerView)
	}
	if hidden := callAs(customerID, http.MethodGet, fieldsPath+"?expand=hiddenFields", "", http.StatusOK); strings.Contains(hidden, sourceID) {
		t.Fatalf("a customer expanded hidden fields: %s", hidden)
	}
	adminView := call(http.MethodGet, fieldsPath+"?expand=hiddenFields", "", http.StatusOK)
	for _, want := range []string{`"canRaiseOnBehalfOf":true`, `"fieldId":"` + sourceID + `"`, `"visible":false`, `"presetValues":["Portal"]`} {
		if !strings.Contains(adminView, want) {
			t.Fatalf("administrator field metadata lacks %s: %s", want, adminView)
		}
	}

	// A customer cannot answer the hidden field; the request takes its preset.
	request := func(extra string) string {
		return `{"serviceDeskId":"` + serviceDeskID + `","requestTypeId":"` + requestTypeID + `","requestFieldValues":{"summary":"Printer on fire","` + impactID + `":{"id":"` + highID + `"}` + extra + `}}`
	}
	if refused := callAs(customerID, http.MethodPost, "/rest/servicedeskapi/request", request(`,"`+sourceID+`":"Email"`), http.StatusBadRequest); !strings.Contains(refused, sourceID) {
		t.Fatalf("submitting a hidden field = %s", refused)
	}
	created := object(callAs(customerID, http.MethodPost, "/rest/servicedeskapi/request", request(""), http.StatusCreated))
	var source string
	if err = st.Pool.QueryRow(ctx, `SELECT fields->>$3 FROM issues WHERE workspace_id=$1 AND key=$2`, workspaceID, created["issueKey"], sourceID).Scan(&source); err != nil || source != "Portal" {
		t.Fatalf("hidden field value = %q err=%v (request %v)", source, err, created)
	}

	// Request type searches leave out types in no group unless asked.
	exec(`UPDATE service_request_types SET group_ids='{}' WHERE id=$1`, requestTypeID)
	var requestTypeName string
	if err = st.Pool.QueryRow(ctx, `SELECT name FROM service_request_types WHERE id=$1`, requestTypeID).Scan(&requestTypeName); err != nil {
		t.Fatal(err)
	}
	search := "/rest/servicedeskapi/servicedesk/" + serviceDeskID + "/requesttype?searchQuery=" + strings.ReplaceAll(requestTypeName, " ", "+")
	if found := call(http.MethodGet, search, "", http.StatusOK); strings.Contains(found, `"id":"`+requestTypeID+`"`) {
		t.Fatalf("a hidden request type matched a search: %s", found)
	}
	if found := call(http.MethodGet, search+"&includeHiddenRequestTypesInSearch=true", "", http.StatusOK); !strings.Contains(found, `"id":"`+requestTypeID+`"`) {
		t.Fatalf("includeHiddenRequestTypesInSearch left out the hidden type: %s", found)
	}
	if listed := call(http.MethodGet, "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype", "", http.StatusOK); !strings.Contains(listed, `"id":"`+requestTypeID+`"`) {
		t.Fatalf("a listing without a search left out a hidden type: %s", listed)
	}
	if restricted := call(http.MethodGet, "/rest/servicedeskapi/requesttype?restrictionStatus=RESTRICTED", "", http.StatusOK); strings.Contains(restricted, `"id":`) {
		t.Fatalf("restricted request types = %s", restricted)
	}
	if grouped := call(http.MethodGet, "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype?groupId=incidents", "", http.StatusOK); strings.Contains(grouped, `"id":"`+requestTypeID+`"`) || !strings.Contains(grouped, `"groupIds":["incidents"]`) {
		t.Fatalf("request types in the incidents group = %s", grouped)
	}
	call(http.MethodGet, "/rest/servicedeskapi/requesttype?restrictionStatus=SECRET", "", http.StatusBadRequest)

	// A queue's requests carry only the fields the queue shows.
	var queueID string
	var queueFields []string
	if err = st.Pool.QueryRow(ctx, `SELECT id,fields FROM service_queues WHERE service_desk_id=$1 AND kind='all_open'`, serviceDeskID).Scan(&queueID, &queueFields); err != nil {
		t.Fatal(err)
	}
	var queuePage struct {
		Values []struct {
			Key    string         `json:"key"`
			Fields map[string]any `json:"fields"`
		} `json:"values"`
	}
	if err = json.Unmarshal([]byte(call(http.MethodGet, "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue/"+queueID+"/issue", "", http.StatusOK)), &queuePage); err != nil || len(queuePage.Values) == 0 {
		t.Fatalf("queue issues = %+v err=%v", queuePage, err)
	}
	for _, value := range queuePage.Values {
		for field := range value.Fields {
			if !slices.Contains(queueFields, field) {
				t.Fatalf("queue issue %s carries %s outside the queue's fields %v", value.Key, field, queueFields)
			}
		}
	}

	// Deleting a request type in use removes it from its requests, which remain.
	requestTypePath := "/rest/servicedeskapi/servicedesk/" + serviceDeskID + "/requesttype/" + requestTypeID
	callAs(customerID, http.MethodDelete, requestTypePath, "", http.StatusForbidden)
	call(http.MethodDelete, requestTypePath, "", http.StatusNoContent)
	call(http.MethodGet, requestTypePath, "", http.StatusNotFound)
	call(http.MethodDelete, requestTypePath, "", http.StatusNotFound)
	if orphan := callAs(customerID, http.MethodGet, "/rest/servicedeskapi/request/"+fmt.Sprint(created["issueKey"]), "", http.StatusOK); !strings.Contains(orphan, `"requestTypeId":""`) {
		t.Fatalf("a request of a deleted request type = %s", orphan)
	}
	var audited bool
	if err = st.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_audit_events WHERE action='service.request_type.deleted' AND target_id=$1 AND (detail->>'requests')::int >= 1)`, requestTypeID).Scan(&audited); err != nil || !audited {
		t.Fatalf("request type deletion audit = %v err=%v", audited, err)
	}
	call(http.MethodGet, "/rest/servicedeskapi/requesttype?includeHiddenRequestTypesInSearch=maybe", "", http.StatusBadRequest)
}
