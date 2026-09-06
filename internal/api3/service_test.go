package api3

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestServiceProjectAndRequestTypeContract(t *testing.T) {
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
	workspaceID, actorID, customerID, agentID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Service test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Service admin')`, actorID, actorID+"@example.test")
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Portal customer')`, customerID, customerID+"@example.test")
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Service agent')`, agentID, agentID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actorID)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, customerID)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, agentID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actorID, store.HashToken(actorID))
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, customerID, store.HashToken(customerID))
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, agentID, store.HashToken(agentID))
	t.Cleanup(func() {
		exec(`DELETE FROM notifications WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM organization_audit_events WHERE actor_id=$1`, actorID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id IN ($1,$2,$3)`, actorID, customerID, agentID)
		exec(`DELETE FROM users WHERE id IN ($1,$2,$3) OR email IN ('invited.customer@example.test','agent-created.customer@example.test')`, actorID, customerID, agentID)
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	callAs := func(accountID, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(accountID+"@example.test", accountID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	call := func(method, path, body string, want int) *httptest.ResponseRecorder {
		return callAs(actorID, method, path, body, want)
	}
	callMultipartAs := func(accountID, path, filename, content string, want int) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("POST", path, &body)
		request.SetBasicAuth(accountID+"@example.test", accountID)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		request.Header.Set("X-Atlassian-Token", "no-check")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("POST %s: %d want %d: %s", path, response.Code, want, response.Body.String())
		}
		return response
	}
	created := call("POST", "/rest/api/3/project", `{"key":"HELP","name":"Help Center","projectTypeKey":"service_desk","projectTemplateKey":"com.atlassian.servicedesk:simplified-it-service-management","leadAccountId":"`+actorID+`"}`, 201)
	if !strings.Contains(created.Body.String(), `"key":"HELP"`) {
		t.Fatal(created.Body.String())
	}
	project, err := st.ProjectByKey(ctx, workspaceID, "HELP")
	if err != nil || project.ProjectTypeKey != "service_desk" {
		t.Fatalf("service project = %+v, %v", project, err)
	}
	desks := call("GET", "/rest/servicedeskapi/servicedesk", "", 200)
	if !strings.Contains(desks.Body.String(), `"projectKey":"HELP"`) || !strings.Contains(desks.Body.String(), `"projectTypeKey":"service_desk"`) {
		t.Fatal(desks.Body.String())
	}
	var serviceDeskID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_desks WHERE project_id=$1`, project.ID).Scan(&serviceDeskID); err != nil {
		t.Fatal(err)
	}
	call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID, "", 200)
	requestTypes := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype", "", 200)
	if !strings.Contains(requestTypes.Body.String(), `"name":"Get IT help"`) || !strings.Contains(requestTypes.Body.String(), `"name":"Report an incident"`) {
		t.Fatal(requestTypes.Body.String())
	}
	var requestTypeID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 ORDER BY id::bigint LIMIT 1`, serviceDeskID).Scan(&requestTypeID); err != nil {
		t.Fatal(err)
	}
	fields := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/"+requestTypeID+"/field", "", 200)
	if !strings.Contains(fields.Body.String(), `"fieldId":"summary"`) {
		t.Fatal(fields.Body.String())
	}
	createdType := call("POST", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype", `{"name":"Ask a question","description":"General help","helpText":"What do you need?","issueTypeId":"it_task"}`, 200)
	if !strings.Contains(createdType.Body.String(), `"name":"Ask a question"`) {
		t.Fatal(createdType.Body.String())
	}
	var createdTypeID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 AND name='Ask a question'`, serviceDeskID).Scan(&createdTypeID); err != nil {
		t.Fatal(err)
	}
	call("DELETE", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/"+createdTypeID, "", 204)
	call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/requesttype/"+createdTypeID, "", 404)
	call("GET", "/rest/servicedeskapi/requesttype?searchQuery=incident", "", 200)
	call("GET", "/rest/servicedeskapi/info", "", 200)

	createdCustomer := call("POST", "/rest/servicedeskapi/customer", `{"email":"invited.customer@example.test","displayName":"Invited Customer"}`, 201)
	if !strings.Contains(createdCustomer.Body.String(), `"displayName":"Invited Customer"`) {
		t.Fatal(createdCustomer.Body.String())
	}
	var createdCustomerBean map[string]any
	if err := json.Unmarshal(createdCustomer.Body.Bytes(), &createdCustomerBean); err != nil {
		t.Fatal(err)
	}
	invitedCustomerID, _ := createdCustomerBean["accountId"].(string)
	onBehalf := call("POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","raiseOnBehalfOf":"invited.customer@example.test","requestFieldValues":{"summary":"New starter access"}}`, 201)
	var onBehalfBean map[string]any
	if err := json.Unmarshal(onBehalf.Body.Bytes(), &onBehalfBean); err != nil {
		t.Fatal(err)
	}
	onBehalfIssue, err := st.IssueByIDOrKey(ctx, workspaceID, onBehalfBean["issueKey"].(string))
	if err != nil || onBehalfIssue.Reporter == nil || onBehalfIssue.Reporter.ID != invitedCustomerID {
		t.Fatalf("on-behalf issue = %+v, %v", onBehalfIssue, err)
	}
	var onBehalfActor string
	if err := st.Pool.QueryRow(ctx, `SELECT actor_id FROM actions WHERE workspace_id=$1 AND entity_type='issue' AND entity_id=$2 AND op='upsert' ORDER BY seq LIMIT 1`, workspaceID, onBehalfIssue.ID).Scan(&onBehalfActor); err != nil || onBehalfActor != actorID {
		t.Fatalf("on-behalf actor = %q, %v", onBehalfActor, err)
	}
	invalid := callAs(customerID, "POST", "/rest/servicedeskapi/request/validate", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","requestFieldValues":{}}`, 200)
	if !strings.Contains(invalid.Body.String(), `"valid":false`) || !strings.Contains(invalid.Body.String(), `"summary"`) {
		t.Fatal(invalid.Body.String())
	}
	var incidentTypeID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 AND name='Report an incident'`, serviceDeskID).Scan(&incidentTypeID); err != nil {
		t.Fatal(err)
	}
	createdRequest := callAs(customerID, "POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+incidentTypeID+`","requestFieldValues":{"summary":"Production checkout is unavailable","description":"Customers receive an error."}}`, 201)
	var requestBean map[string]any
	if err := json.Unmarshal(createdRequest.Body.Bytes(), &requestBean); err != nil {
		t.Fatal(err)
	}
	issueKey, _ := requestBean["issueKey"].(string)
	if issueKey == "" {
		t.Fatal(createdRequest.Body.String())
	}
	issue, err := st.IssueByIDOrKey(ctx, workspaceID, issueKey)
	if err != nil || !strings.Contains(strings.Join(issue.Labels, ","), "incident") {
		t.Fatalf("incident issue = %+v, %v", issue, err)
	}
	approval, err := handler.Commands.CreateServiceApproval(ctx, actorID, workspaceID, issue.ID, "Production change approval", []string{customerID})
	if err != nil {
		t.Fatal(err)
	}
	approvalList := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/approval", "", 200)
	if !strings.Contains(approvalList.Body.String(), `"canAnswerApproval":true`) || !strings.Contains(approvalList.Body.String(), "Production change approval") {
		t.Fatal(approvalList.Body.String())
	}
	call("POST", "/rest/servicedeskapi/request/"+issueKey+"/approval/"+approval.ID, `{"decision":"approve"}`, 400)
	approved := callAs(customerID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/approval/"+approval.ID, `{"decision":"approve"}`, 200)
	if !strings.Contains(approved.Body.String(), `"finalDecision":"approved"`) || !strings.Contains(approved.Body.String(), `"completedDate"`) {
		t.Fatal(approved.Body.String())
	}
	callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/approval/"+approval.ID, "", 200)
	multiApproval, err := handler.Commands.CreateServiceApproval(ctx, actorID, workspaceID, issue.ID, "Two-person change approval", []string{customerID, agentID})
	if err != nil {
		t.Fatal(err)
	}
	pendingApproval := callAs(customerID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/approval/"+multiApproval.ID, `{"decision":"approve"}`, 200)
	if !strings.Contains(pendingApproval.Body.String(), `"finalDecision":"pending"`) {
		t.Fatal(pendingApproval.Body.String())
	}
	declinedApproval := callAs(agentID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/approval/"+multiApproval.ID, `{"decision":"decline"}`, 200)
	if !strings.Contains(declinedApproval.Body.String(), `"finalDecision":"declined"`) {
		t.Fatal(declinedApproval.Body.String())
	}

	temporaryResponse := callMultipartAs(customerID, "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/attachTemporaryFile", "customer-log.txt", "customer-visible-log", 201)
	var temporaryBean struct {
		TemporaryAttachments []struct {
			ID string `json:"temporaryAttachmentId"`
		} `json:"temporaryAttachments"`
	}
	if err := json.Unmarshal(temporaryResponse.Body.Bytes(), &temporaryBean); err != nil || len(temporaryBean.TemporaryAttachments) != 1 {
		t.Fatalf("temporary attachment: %v %s", err, temporaryResponse.Body.String())
	}
	createdAttachment := callAs(customerID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/attachment", `{"temporaryAttachmentIds":["`+temporaryBean.TemporaryAttachments[0].ID+`"],"public":true,"additionalComment":{"body":"Diagnostic log"}}`, 201)
	if !strings.Contains(createdAttachment.Body.String(), "customer-log.txt") || !strings.Contains(createdAttachment.Body.String(), "Diagnostic log") {
		t.Fatal(createdAttachment.Body.String())
	}
	var publicAttachmentID string
	if err := st.Pool.QueryRow(ctx, `SELECT a.id FROM attachments a WHERE a.issue_id=$1 AND a.filename='customer-log.txt'`, issue.ID).Scan(&publicAttachmentID); err != nil {
		t.Fatal(err)
	}
	var publicAttachmentCommentID string
	if err := st.Pool.QueryRow(ctx, `SELECT comment_id FROM service_request_attachments WHERE attachment_id=$1`, publicAttachmentID).Scan(&publicAttachmentCommentID); err != nil {
		t.Fatal(err)
	}
	commentAttachments := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/comment/"+publicAttachmentCommentID+"/attachment", "", 200)
	if !strings.Contains(commentAttachments.Body.String(), "customer-log.txt") {
		t.Fatal(commentAttachments.Body.String())
	}
	content := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/attachment/"+publicAttachmentID, "", 200)
	if content.Body.String() != "customer-visible-log" {
		t.Fatal(content.Body.String())
	}
	attachmentList := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/attachment?start=0&limit=50", "", 200)
	if !strings.Contains(attachmentList.Body.String(), "customer-log.txt") {
		t.Fatal(attachmentList.Body.String())
	}

	internalTemporary := callMultipartAs(actorID, "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/attachTemporaryFile", "agent-note.txt", "agent-only-note", 201)
	if err := json.Unmarshal(internalTemporary.Body.Bytes(), &temporaryBean); err != nil {
		t.Fatal(err)
	}
	call("POST", "/rest/servicedeskapi/request/"+issueKey+"/attachment", `{"temporaryAttachmentIds":["`+temporaryBean.TemporaryAttachments[0].ID+`"],"public":false,"additionalComment":{"body":"Internal evidence"}}`, 201)
	var internalAttachmentID string
	if err := st.Pool.QueryRow(ctx, `SELECT a.id FROM attachments a WHERE a.issue_id=$1 AND a.filename='agent-note.txt'`, issue.ID).Scan(&internalAttachmentID); err != nil {
		t.Fatal(err)
	}
	customerAttachmentList := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/attachment?start=0&limit=50", "", 200)
	if strings.Contains(customerAttachmentList.Body.String(), "agent-note.txt") {
		t.Fatal(customerAttachmentList.Body.String())
	}
	callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/attachment/"+internalAttachmentID, "", 404)
	callAs(customerID, "GET", "/rest/api/3/attachment/content/"+internalAttachmentID, "", 404)
	call("GET", "/rest/servicedeskapi/request/"+issueKey+"/attachment/"+internalAttachmentID+"/thumbnail", "", 200)
	expiredTemporary := callMultipartAs(customerID, "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/attachTemporaryFile", "expired.txt", "expired-content", 201)
	if err := json.Unmarshal(expiredTemporary.Body.Bytes(), &temporaryBean); err != nil {
		t.Fatal(err)
	}
	var expiredBlobRef string
	if err := st.Pool.QueryRow(ctx, `UPDATE service_temporary_attachments SET expires_at=now()-interval '1 minute' WHERE id=$1 RETURNING blob_ref`, temporaryBean.TemporaryAttachments[0].ID).Scan(&expiredBlobRef); err != nil {
		t.Fatal(err)
	}
	if err := (&commands.ServiceTemporaryAttachmentRunner{Service: handler.Commands}).DrainOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := blobs.Get(ctx, expiredBlobRef); !errors.Is(err, attachments.ErrNotFound) {
		t.Fatalf("expired temporary blob error = %v", err)
	}
	metrics, err := st.ServiceSLAMetrics(ctx, workspaceID, serviceDeskID)
	if err != nil || len(metrics) != 2 {
		t.Fatalf("SLA metrics = %+v, %v", metrics, err)
	}
	metricByKind := make(map[string]string, len(metrics))
	for _, metric := range metrics {
		metricByKind[metric.Kind] = metric.ID
	}
	callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/sla", "", 403)
	slaList := call("GET", "/rest/servicedeskapi/request/"+issueKey+"/sla", "", 200)
	if !strings.Contains(slaList.Body.String(), "Time to first response") || !strings.Contains(slaList.Body.String(), `"ongoingCycle"`) {
		t.Fatal(slaList.Body.String())
	}
	call("GET", "/rest/servicedeskapi/request/"+issueKey+"/sla/does-not-exist", "", 404)
	if err := handler.Commands.UpdateServiceCalendar(ctx, actorID, workspaceID, serviceDeskID, "Every day", "UTC", []int16{1, 2, 3, 4, 5, 6, 7}, 0, 1440); err != nil {
		t.Fatal(err)
	}
	if err := handler.Commands.UpdateServiceSLAMetric(ctx, actorID, workspaceID, serviceDeskID, metricByKind["first_response"], (2 * time.Hour).Milliseconds()); err != nil {
		t.Fatal(err)
	}
	configuredSLA := call("GET", "/rest/servicedeskapi/request/"+issueKey+"/sla/"+metricByKind["first_response"], "", 200)
	var configuredSLABean map[string]any
	if err := json.Unmarshal(configuredSLA.Body.Bytes(), &configuredSLABean); err != nil {
		t.Fatal(err)
	}
	ongoingCycle, _ := configuredSLABean["ongoingCycle"].(map[string]any)
	goalDuration, _ := ongoingCycle["goalDuration"].(map[string]any)
	if goalDuration["millis"] != float64((2 * time.Hour).Milliseconds()) {
		t.Fatal(configuredSLA.Body.String())
	}
	var serviceConfigAudits int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE actor_id=$1 AND action IN ('service.calendar.updated','service.sla.updated')`, actorID).Scan(&serviceConfigAudits); err != nil || serviceConfigAudits != 2 {
		t.Fatalf("service configuration audits = %d, %v", serviceConfigAudits, err)
	}
	escalationNow := time.Now().UTC().Truncate(time.Second)
	if _, err := st.Pool.Exec(ctx, `UPDATE service_sla_cycles SET started_at=$2 WHERE request_issue_id=$1 AND metric_id=$3`, issue.ID, escalationNow.Add(-110*time.Minute), metricByKind["first_response"]); err != nil {
		t.Fatal(err)
	}
	runner := &store.ServiceSLARunner{Store: st, Now: func() time.Time { return escalationNow }}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	var warningCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1 AND kind='service_sla_warning'`, workspaceID).Scan(&warningCount); err != nil || warningCount != 1 {
		t.Fatalf("warning notifications = %d, %v", warningCount, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE service_sla_cycles SET started_at=$2 WHERE request_issue_id=$1 AND metric_id=$3`, issue.ID, escalationNow.Add(-3*time.Hour), metricByKind["first_response"]); err != nil {
		t.Fatal(err)
	}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	var breachCount int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1 AND kind='service_sla_breached'`, workspaceID).Scan(&breachCount); err != nil || breachCount != 1 {
		t.Fatalf("breach notifications = %d, %v", breachCount, err)
	}
	var attentionQueueID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_queues WHERE service_desk_id=$1 AND kind='sla_attention'`, serviceDeskID).Scan(&attentionQueueID); err != nil {
		t.Fatal(err)
	}
	attention := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue/"+attentionQueueID+"/issue", "", 200)
	if !strings.Contains(attention.Body.String(), issueKey) {
		t.Fatal(attention.Body.String())
	}
	owned := callAs(customerID, "GET", "/rest/servicedeskapi/request", "", 200)
	if !strings.Contains(owned.Body.String(), issueKey) {
		t.Fatal(owned.Body.String())
	}
	callAs(customerID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/comment", `{"body":"I can reproduce this in two browsers.","public":true}`, 201)
	call("POST", "/rest/servicedeskapi/request/"+issueKey+"/comment", `{"body":"Escalate to the payments team.","public":false}`, 201)
	customerComments := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/comment", "", 200)
	if !strings.Contains(customerComments.Body.String(), "two browsers") || strings.Contains(customerComments.Body.String(), "payments team") {
		t.Fatal(customerComments.Body.String())
	}
	agentComments := call("GET", "/rest/servicedeskapi/request/"+issueKey+"/comment", "", 200)
	if !strings.Contains(agentComments.Body.String(), "payments team") {
		t.Fatal(agentComments.Body.String())
	}
	callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/status", "", 200)
	transitions := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/transition", "", 200)
	if !strings.Contains(transitions.Body.String(), `"id":"21"`) {
		t.Fatal(transitions.Body.String())
	}
	callAs(customerID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/transition", `{"id":"21","additionalComment":{"body":"Work can begin.","public":true}}`, 204)
	detail := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey, "", 200)
	if !strings.Contains(detail.Body.String(), `"status":"In Progress"`) || !strings.Contains(detail.Body.String(), "Work can begin") {
		t.Fatal(detail.Body.String())
	}
	participants := callAs(customerID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/participant", `{"usernames":["invited.customer@example.test"]}`, 200)
	if !strings.Contains(participants.Body.String(), "Invited Customer") {
		t.Fatal(participants.Body.String())
	}
	if _, err := st.ServiceRequest(ctx, workspaceID, invitedCustomerID, issueKey, false); err != nil {
		t.Fatalf("participant access: %v", err)
	}
	listedParticipants := callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/participant", "", 200)
	if !strings.Contains(listedParticipants.Body.String(), invitedCustomerID) {
		t.Fatal(listedParticipants.Body.String())
	}
	removedParticipants := callAs(customerID, "DELETE", "/rest/servicedeskapi/request/"+issueKey+"/participant", `{"accountIds":["`+invitedCustomerID+`"]}`, 200)
	if strings.Contains(removedParticipants.Body.String(), invitedCustomerID) {
		t.Fatal(removedParticipants.Body.String())
	}
	if _, err := st.ServiceRequest(ctx, workspaceID, invitedCustomerID, issueKey, false); err == nil {
		t.Fatal("removed participant retained request access")
	}
	allRequests := call("GET", "/rest/servicedeskapi/request?requestOwnership=ALL_REQUESTS", "", 200)
	if !strings.Contains(allRequests.Body.String(), issueKey) {
		t.Fatal(allRequests.Body.String())
	}
	call("POST", "/rest/api/3/project", `{"key":"OPS","name":"Operations desk","projectTypeKey":"service_desk","projectTemplateKey":"com.atlassian.servicedesk:simplified-it-service-management","leadAccountId":"`+actorID+`"}`, 201)
	otherProject, err := st.ProjectByKey(ctx, workspaceID, "OPS")
	if err != nil {
		t.Fatal(err)
	}
	var otherDeskID, otherTypeID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_desks WHERE project_id=$1`, otherProject.ID).Scan(&otherDeskID); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 ORDER BY id::bigint LIMIT 1`, otherDeskID).Scan(&otherTypeID); err != nil {
		t.Fatal(err)
	}
	otherRequest := callAs(customerID, "POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+otherDeskID+`","requestTypeId":"`+otherTypeID+`","requestFieldValues":{"summary":"Other desk request"}}`, 201)
	var otherRequestBean map[string]any
	if err := json.Unmarshal(otherRequest.Body.Bytes(), &otherRequestBean); err != nil {
		t.Fatal(err)
	}
	otherIssueKey, _ := otherRequestBean["issueKey"].(string)
	queues := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue?includeCount=true", "", 200)
	if !strings.Contains(queues.Body.String(), "Unassigned requests") || !strings.Contains(queues.Body.String(), `"issueCount"`) {
		t.Fatal(queues.Body.String())
	}
	callAs(customerID, "GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue", "", 403)
	callAs(agentID, "GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue", "", 403)
	callAs(agentID, "GET", "/rest/servicedeskapi/request?requestOwnership=ALL_REQUESTS", "", 403)
	if err := handler.Commands.SetServiceDeskAgent(ctx, actorID, workspaceID, serviceDeskID, agentID, true); err != nil {
		t.Fatal(err)
	}
	if err := handler.Commands.SetServiceDeskAgent(ctx, agentID, workspaceID, serviceDeskID, customerID, true); err == nil {
		t.Fatal("regular service agent managed the agent roster")
	}
	agentQueues := callAs(agentID, "GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue?includeCount=true", "", 200)
	if !strings.Contains(agentQueues.Body.String(), "Unassigned requests") {
		t.Fatal(agentQueues.Body.String())
	}
	agentRequests := callAs(agentID, "GET", "/rest/servicedeskapi/request?requestOwnership=ALL_REQUESTS", "", 200)
	if !strings.Contains(agentRequests.Body.String(), issueKey) || strings.Contains(agentRequests.Body.String(), otherIssueKey) {
		t.Fatal(agentRequests.Body.String())
	}
	callAs(agentID, "GET", "/rest/servicedeskapi/servicedesk/"+otherDeskID+"/queue", "", 403)
	callAs(agentID, "GET", "/rest/servicedeskapi/request/"+otherIssueKey, "", 404)
	agentCustomer := callAs(agentID, "POST", "/rest/servicedeskapi/customer", `{"email":"agent-created.customer@example.test","displayName":"Agent-created Customer"}`, 201)
	if !strings.Contains(agentCustomer.Body.String(), "Agent-created Customer") {
		t.Fatal(agentCustomer.Body.String())
	}
	agentRaised := callAs(agentID, "POST", "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","raiseOnBehalfOf":"agent-created.customer@example.test","requestFieldValues":{"summary":"Agent-raised customer request"}}`, 201)
	if !strings.Contains(agentRaised.Body.String(), "Agent-raised customer request") {
		t.Fatal(agentRaised.Body.String())
	}
	agentDetail := callAs(agentID, "GET", "/rest/servicedeskapi/request/"+issueKey, "", 200)
	if !strings.Contains(agentDetail.Body.String(), `"sla":[{`) {
		t.Fatal(agentDetail.Body.String())
	}
	callAs(agentID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/comment", `{"body":"Agent-only investigation detail.","public":false}`, 201)
	regularAgentComments := callAs(agentID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/comment", "", 200)
	if !strings.Contains(regularAgentComments.Body.String(), "Agent-only investigation detail") {
		t.Fatal(regularAgentComments.Body.String())
	}
	customerComments = callAs(customerID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/comment", "", 200)
	if strings.Contains(customerComments.Body.String(), "Agent-only investigation detail") {
		t.Fatal(customerComments.Body.String())
	}
	callAs(agentID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/comment", `{"body":"The service team is investigating.","public":true}`, 201)
	firstResponseSLA := callAs(agentID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/sla/"+metricByKind["first_response"], "", 200)
	if !strings.Contains(firstResponseSLA.Body.String(), `"completedCycles":[{`) || strings.Contains(firstResponseSLA.Body.String(), `"ongoingCycle"`) {
		t.Fatal(firstResponseSLA.Body.String())
	}
	var unassignedQueueID, mineQueueID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_queues WHERE service_desk_id=$1 AND kind='unassigned'`, serviceDeskID).Scan(&unassignedQueueID); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_queues WHERE service_desk_id=$1 AND kind='assigned_to_me'`, serviceDeskID).Scan(&mineQueueID); err != nil {
		t.Fatal(err)
	}
	unassigned := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue/"+unassignedQueueID+"/issue", "", 200)
	if !strings.Contains(unassigned.Body.String(), issueKey) {
		t.Fatal(unassigned.Body.String())
	}
	assigneeID := actorID
	if _, _, err := handler.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{ActorID: actorID, WorkspaceID: workspaceID, IssueIDOrKey: issueKey, AssigneeID: &assigneeID}); err != nil {
		t.Fatal(err)
	}
	assigned := call("GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue/"+mineQueueID+"/issue", "", 200)
	if !strings.Contains(assigned.Body.String(), issueKey) {
		t.Fatal(assigned.Body.String())
	}
	callAs(agentID, "POST", "/rest/servicedeskapi/request/"+issueKey+"/transition", `{"id":"31"}`, 204)
	resolutionSLA := callAs(agentID, "GET", "/rest/servicedeskapi/request/"+issueKey+"/sla/"+metricByKind["resolution"], "", 200)
	if !strings.Contains(resolutionSLA.Body.String(), `"completedCycles":[{`) || strings.Contains(resolutionSLA.Body.String(), `"ongoingCycle"`) {
		t.Fatal(resolutionSLA.Body.String())
	}
	if err := handler.Commands.SetServiceDeskAgent(ctx, actorID, workspaceID, serviceDeskID, agentID, false); err != nil {
		t.Fatal(err)
	}
	callAs(agentID, "GET", "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/queue", "", 403)
	formerAgentView := callAs(agentID, "GET", "/rest/servicedeskapi/request/"+issueKey, "", 200)
	if strings.Contains(formerAgentView.Body.String(), "Agent-only investigation detail") || strings.Contains(formerAgentView.Body.String(), "agent-note.txt") {
		t.Fatal(formerAgentView.Body.String())
	}
}
