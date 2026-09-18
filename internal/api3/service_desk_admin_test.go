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
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestServiceDeskAdministrators covers Jira Service Management's service desk
// administrator permission: the administrators of a service project manage its
// request types, forms, customers and queues without being site
// administrators or agents, while other members and administrators of other
// projects cannot.
func TestServiceDeskAdministrators(t *testing.T) {
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
	workspaceID := store.NewID("ws")
	siteAdminID, deskAdminID, memberID, otherAdminID, customerID := store.NewID("usr"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	stamp := time.Now().UnixNano() % 100000
	deskKey, otherKey := fmt.Sprintf("DA%05d", stamp), fmt.Sprintf("DO%05d", stamp)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Desk administrators')`, workspaceID)
	people := []struct{ id, role, name string }{{siteAdminID, "admin", "Site admin"}, {deskAdminID, "member", "Desk admin"}, {memberID, "member", "Plain member"}, {otherAdminID, "member", "Other admin"}, {customerID, "member", "Portal customer"}}
	for _, person := range people {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, person.id, person.id+"@example.test", person.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, person.id, person.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, person.id, store.HashToken(person.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM notifications WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM service_desks WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM role_bindings WHERE scope_type='project' AND scope_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, person := range people {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, person.id)
			exec(`DELETE FROM users WHERE id=$1`, person.id)
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
	for _, key := range []string{deskKey, otherKey} {
		callAs(siteAdminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+key+`","name":"Desk `+key+`","projectTypeKey":"service_desk","projectTemplateKey":"com.atlassian.servicedesk:simplified-it-service-management","leadAccountId":"`+siteAdminID+`"}`, http.StatusCreated)
	}
	// The desk administrator administers only the desk's project; the other
	// administrator only the other project.
	callAs(siteAdminID, http.MethodPost, "/rest/api/3/project/"+deskKey+"/role/10000", `{"user":["`+deskAdminID+`"]}`, http.StatusOK)
	callAs(siteAdminID, http.MethodPost, "/rest/api/3/project/"+otherKey+"/role/10000", `{"user":["`+otherAdminID+`"]}`, http.StatusOK)
	var serviceDeskID, requestTypeID string
	if err = st.Pool.QueryRow(ctx, `SELECT sd.id FROM service_desks sd JOIN projects p ON p.id=sd.project_id WHERE p.workspace_id=$1 AND p.key=$2`, workspaceID, deskKey).Scan(&serviceDeskID); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 ORDER BY id::bigint LIMIT 1`, serviceDeskID).Scan(&requestTypeID); err != nil {
		t.Fatal(err)
	}
	if agent, agentErr := st.IsServiceAgent(ctx, workspaceID, serviceDeskID, deskAdminID); agentErr != nil || agent {
		t.Fatalf("the desk administrator is an agent: %v %v", agent, agentErr)
	}

	// Request types.
	typesPath := "/rest/servicedeskapi/servicedesk/" + serviceDeskID + "/requesttype"
	newType := `{"name":"Desk-admin type","description":"Made by the desk's administrator","helpText":"","issueTypeId":"it_task"}`
	for _, refused := range []string{memberID, otherAdminID} {
		callAs(refused, http.MethodPost, typesPath, newType, http.StatusForbidden)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal([]byte(callAs(deskAdminID, http.MethodPost, typesPath, newType, http.StatusOK)), &created); err != nil || created.ID == "" {
		t.Fatalf("request type created by the desk administrator = %+v err=%v", created, err)
	}
	// Jira answers 404 for a service desk or an issue type that is not there.
	callAs(deskAdminID, http.MethodPost, typesPath, `{"name":"No such work type","issueTypeId":"99999999"}`, http.StatusNotFound)
	callAs(siteAdminID, http.MethodPost, "/rest/servicedeskapi/servicedesk/999999999/requesttype", newType, http.StatusNotFound)

	// Jira leaves a request type created over REST in no group, which keeps it
	// off the customer portal until an administrator puts it in one. The
	// administrator arranges the groups, and the listing follows that order.
	if bean := callAs(deskAdminID, http.MethodGet, typesPath+"/"+created.ID, "", http.StatusOK); !strings.Contains(bean, `"groupIds":[]`) {
		t.Fatalf("a request type created over REST = %s", bean)
	}
	group, err := h.Commands.CreateServiceRequestTypeGroup(ctx, deskAdminID, workspaceID, serviceDeskID, "Desk admin group")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.Commands.CreateServiceRequestTypeGroup(ctx, memberID, workspaceID, serviceDeskID, "Member group"); err == nil {
		t.Fatal("a plain member added a request type group")
	}
	if err = h.Commands.SetServiceRequestTypeGroups(ctx, deskAdminID, workspaceID, serviceDeskID, created.ID, []string{group.ID}); err != nil {
		t.Fatal(err)
	}
	if grouped := callAs(deskAdminID, http.MethodGet, typesPath+"?groupId="+group.ID, "", http.StatusOK); !strings.Contains(grouped, `"id":"`+created.ID+`"`) {
		t.Fatalf("the group's request types = %s", grouped)
	}
	groupsPath := "/rest/servicedeskapi/servicedesk/" + serviceDeskID + "/requesttypegroup"
	listed := callAs(deskAdminID, http.MethodGet, groupsPath, "", http.StatusOK)
	if !strings.Contains(listed, `"name":"Desk admin group"`) || strings.Index(listed, `"Help and support"`) > strings.Index(listed, `"Desk admin group"`) {
		t.Fatalf("request type groups = %s", listed)
	}
	if err = h.Commands.MoveServiceRequestTypeGroup(ctx, deskAdminID, workspaceID, serviceDeskID, group.ID, "up"); err != nil {
		t.Fatal(err)
	}
	if moved := callAs(deskAdminID, http.MethodGet, groupsPath, "", http.StatusOK); strings.Index(moved, `"Desk admin group"`) > strings.Index(moved, `"Changes"`) {
		t.Fatalf("request type groups after the move = %s", moved)
	}
	callAs(otherAdminID, http.MethodDelete, typesPath+"/"+created.ID, "", http.StatusForbidden)
	callAs(deskAdminID, http.MethodDelete, typesPath+"/"+created.ID, "", http.StatusNoContent)
	if err = h.Commands.DeleteServiceRequestTypeGroup(ctx, deskAdminID, workspaceID, serviceDeskID, group.ID); err != nil {
		t.Fatal(err)
	}

	// Forms and queues, through the command layer.
	form := []models.ServiceRequestTypeField{{ID: "summary", Required: true}, {ID: "description"}}
	if err = h.Commands.SetServiceRequestTypeFields(ctx, deskAdminID, workspaceID, serviceDeskID, requestTypeID, form); err != nil {
		t.Fatalf("the desk administrator could not edit a form: %v", err)
	}
	if err = h.Commands.SetServiceRequestTypeFields(ctx, otherAdminID, workspaceID, serviceDeskID, requestTypeID, form); err == nil {
		t.Fatal("another project's administrator edited the desk's form")
	}
	if _, err = h.Commands.CreateServiceQueue(ctx, deskAdminID, workspaceID, serviceDeskID, "Desk admin queue", "project = "+deskKey); err != nil {
		t.Fatalf("the desk administrator could not create a queue: %v", err)
	}
	if _, err = h.Commands.CreateServiceQueue(ctx, memberID, workspaceID, serviceDeskID, "Member queue", "project = "+deskKey); err == nil {
		t.Fatal("a plain member created a queue")
	}

	// Customers. Raising a request makes the member an active service customer.
	callAs(customerID, http.MethodPost, "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","requestFieldValues":{"summary":"First request"}}`, http.StatusCreated)
	customersPath := "/rest/servicedeskapi/servicedesk/" + serviceDeskID + "/customer"
	callAs(memberID, http.MethodPost, customersPath, `{"accountIds":["`+customerID+`"]}`, http.StatusForbidden)
	callAs(otherAdminID, http.MethodPost, customersPath, `{"accountIds":["`+customerID+`"]}`, http.StatusForbidden)
	callAs(deskAdminID, http.MethodPost, customersPath, `{"accountIds":["`+customerID+`"]}`, http.StatusNoContent)
	// Inviting a customer also needs the Jira Administrator global permission.
	callAs(deskAdminID, http.MethodPost, customersPath+"/invite", `{"email":"invitee-`+deskKey+`@example.test","displayName":"Invitee"}`, http.StatusForbidden)

	// Calendars and SLA goals.
	weekdays := []int16{1, 2, 3, 4, 5}
	if err = h.Commands.UpdateServiceCalendar(ctx, deskAdminID, workspaceID, serviceDeskID, "Desk hours", "UTC", weekdays, 480, 1020); err != nil {
		t.Fatalf("the desk administrator could not change the calendar: %v", err)
	}
	if err = h.Commands.UpdateServiceCalendar(ctx, otherAdminID, workspaceID, serviceDeskID, "Other hours", "UTC", weekdays, 480, 1020); err == nil {
		t.Fatal("another project's administrator changed the desk's calendar")
	}
	metrics, err := st.ServiceSLAMetrics(ctx, workspaceID, serviceDeskID)
	if err != nil || len(metrics) == 0 {
		t.Fatalf("SLA metrics = %v err=%v", metrics, err)
	}
	if err = h.Commands.UpdateServiceSLAMetric(ctx, deskAdminID, workspaceID, serviceDeskID, metrics[0].ID, "", time.Hour.Milliseconds()); err != nil {
		t.Fatalf("the desk administrator could not change an SLA goal: %v", err)
	}
	if err = h.Commands.UpdateServiceSLAMetric(ctx, memberID, workspaceID, serviceDeskID, metrics[0].ID, "", time.Hour.Milliseconds()); err == nil {
		t.Fatal("a plain member changed an SLA goal")
	}

	// Hidden fields appear only to the desk's administrators.
	hidden := []models.ServiceRequestTypeField{{ID: "summary", Required: true}, {ID: "description", Hidden: true}}
	if err = h.Commands.SetServiceRequestTypeFields(ctx, deskAdminID, workspaceID, serviceDeskID, requestTypeID, hidden); err != nil {
		t.Fatalf("the desk administrator could not hide a field: %v", err)
	}
	fieldsPath := typesPath + "/" + requestTypeID + "/field?expand=hiddenFields"
	if fields := callAs(deskAdminID, http.MethodGet, fieldsPath, "", http.StatusOK); !strings.Contains(fields, `"fieldId":"description"`) {
		t.Fatalf("the desk administrator does not see the hidden field: %s", fields)
	}
	if fields := callAs(customerID, http.MethodGet, fieldsPath, "", http.StatusOK); strings.Contains(fields, `"fieldId":"description"`) {
		t.Fatalf("a customer sees the hidden field: %s", fields)
	}

	// Request type properties need an agent license as well.
	propertyPath := typesPath + "/" + requestTypeID + "/property/desk.admin"
	callAs(deskAdminID, http.MethodPut, propertyPath, `{"owner":"desk"}`, http.StatusForbidden)
	if err = h.Commands.SetServiceDeskAgent(ctx, siteAdminID, workspaceID, serviceDeskID, deskAdminID, true); err != nil {
		t.Fatal(err)
	}
	callAs(deskAdminID, http.MethodPut, propertyPath, `{"owner":"desk"}`, http.StatusCreated)
	callAs(otherAdminID, http.MethodDelete, propertyPath, "", http.StatusForbidden)
	callAs(deskAdminID, http.MethodDelete, propertyPath, "", http.StatusNoContent)
}
