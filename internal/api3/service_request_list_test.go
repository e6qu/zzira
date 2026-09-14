package api3

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/e6qu/zzira/internal/attachments"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestServiceRequestListFiltersAndExpansions covers Jira Service Management's
// request search: requestOwnership selects owned, participated, organization,
// approver and all requests; status, search and desk filters narrow them; and
// a request's optional parts, including its status chronology, appear only
// when expanded.
func TestServiceRequestListFiltersAndExpansions(t *testing.T) {
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
	adminID, reporterID, colleagueID, approverID := store.NewID("usr"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	stamp := time.Now().UnixNano() % 100000
	projectKey := fmt.Sprintf("RL%05d", stamp)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Request lists')`, workspaceID)
	people := []struct{ id, role, name string }{{adminID, "admin", "List admin"}, {reporterID, "member", "Rita Reporter"}, {colleagueID, "member", "Colin Colleague"}, {approverID, "member", "Abby Approver"}}
	for _, person := range people {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, person.id, person.id+"@example.test", person.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, person.id, person.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, person.id, store.HashToken(person.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM notifications WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM service_organizations WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM service_desks WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, person := range people {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, person.id)
			exec(`DELETE FROM users WHERE id=$1`, person.id)
		}
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
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
	callAs(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Request lists","projectTypeKey":"service_desk","projectTemplateKey":"com.atlassian.servicedesk:simplified-it-service-management","leadAccountId":"`+adminID+`"}`, http.StatusCreated)
	var serviceDeskID, requestTypeID string
	if err = st.Pool.QueryRow(ctx, `SELECT sd.id FROM service_desks sd JOIN projects p ON p.id=sd.project_id WHERE p.workspace_id=$1 AND p.key=$2`, workspaceID, projectKey).Scan(&serviceDeskID); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT id FROM service_request_types WHERE service_desk_id=$1 ORDER BY id::bigint LIMIT 1`, serviceDeskID).Scan(&requestTypeID); err != nil {
		t.Fatal(err)
	}
	raise := func(accountID, summary string) (key, issueID string) {
		t.Helper()
		var created struct {
			IssueID  string `json:"issueId"`
			IssueKey string `json:"issueKey"`
		}
		body := callAs(accountID, http.MethodPost, "/rest/servicedeskapi/request", `{"serviceDeskId":"`+serviceDeskID+`","requestTypeId":"`+requestTypeID+`","requestFieldValues":{"summary":"`+summary+`"}}`, http.StatusCreated)
		if err := json.Unmarshal([]byte(body), &created); err != nil {
			t.Fatal(err)
		}
		return created.IssueKey, created.IssueID
	}
	printer, _ := raise(reporterID, "Printer jammed")
	vpn, _ := raise(reporterID, "VPN outage")
	laptop, _ := raise(colleagueID, "Laptop request")
	badge, _ := raise(approverID, "Badge replacement")

	// The colleague participates in the printer request and shares an
	// organization, which the desk serves, with the reporter.
	callAs(reporterID, http.MethodPost, "/rest/servicedeskapi/request/"+printer+"/participant", `{"accountIds":["`+colleagueID+`"]}`, http.StatusOK)
	var organization struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal([]byte(callAs(adminID, http.MethodPost, "/rest/servicedeskapi/organization", `{"name":"Floor three `+fmt.Sprint(stamp)+`"}`, http.StatusCreated)), &organization); err != nil {
		t.Fatal(err)
	}
	callAs(adminID, http.MethodPost, "/rest/servicedeskapi/organization/"+organization.ID+"/user", `{"accountIds":["`+reporterID+`","`+colleagueID+`"],"usernames":[]}`, http.StatusNoContent)
	callAs(adminID, http.MethodPost, "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/organization", `{"organizationId":`+organization.ID+`}`, http.StatusNoContent)
	// The API gives the request's client-facing issue id; the command takes the key.
	if _, err = h.Commands.CreateServiceApproval(ctx, adminID, workspaceID, vpn, "Manager approval", []string{approverID}); err != nil {
		t.Fatal(err)
	}
	callAs(reporterID, http.MethodPost, "/rest/servicedeskapi/request/"+printer+"/transition", `{"id":"31"}`, http.StatusNoContent)

	list := func(accountID, query string) []string {
		t.Helper()
		var page struct {
			Values []struct {
				IssueKey string `json:"issueKey"`
			} `json:"values"`
		}
		if err := json.Unmarshal([]byte(callAs(accountID, http.MethodGet, "/rest/servicedeskapi/request"+query, "", http.StatusOK)), &page); err != nil {
			t.Fatal(err)
		}
		keys := []string{}
		for _, value := range page.Values {
			keys = append(keys, value.IssueKey)
		}
		slices.Sort(keys)
		return keys
	}
	expect := func(accountID, query string, want ...string) {
		t.Helper()
		slices.Sort(want)
		if got := list(accountID, query); !slices.Equal(got, want) {
			t.Fatalf("requests for %q = %v, want %v", query, got, want)
		}
	}
	expect(reporterID, "", printer, vpn, laptop)
	expect(reporterID, "?requestOwnership=OWNED_REQUESTS", printer, vpn)
	expect(colleagueID, "?requestOwnership=PARTICIPATED_REQUESTS", printer)
	expect(colleagueID, "?requestOwnership=OWNED_REQUESTS&requestOwnership=PARTICIPATED_REQUESTS", printer, laptop)
	expect(reporterID, "?requestOwnership=ORGANIZATION&organizationId="+organization.ID, printer, vpn, laptop)
	expect(approverID, "?requestOwnership=ALL_ORGANIZATIONS")
	expect(approverID, "?requestOwnership=APPROVER", vpn)
	expect(approverID, "?requestOwnership=APPROVER&approvalStatus=MY_PENDING_APPROVAL", vpn)
	expect(approverID, "?requestOwnership=APPROVER&approvalStatus=MY_HISTORY_APPROVAL")
	expect(reporterID, "?requestStatus=CLOSED_REQUESTS", printer)
	expect(reporterID, "?requestStatus=OPEN_REQUESTS", vpn, laptop)
	expect(reporterID, "?searchTerm="+url.QueryEscape("VPN*"), vpn)
	expect(adminID, "?requestOwnership=ALL_REQUESTS", printer, vpn, laptop, badge)
	for _, refused := range []string{"?requestOwnership=ORGANIZATION", "?organizationId=" + organization.ID, "?approvalStatus=MY_PENDING_APPROVAL", "?requestOwnership=EVERYTHING",
		"?requestStatus=SOMETIMES", "?requestTypeId=" + requestTypeID} {
		callAs(reporterID, http.MethodGet, "/rest/servicedeskapi/request"+refused, "", http.StatusBadRequest)
	}
	callAs(reporterID, http.MethodGet, "/rest/servicedeskapi/request?serviceDeskId=999999999", "", http.StatusNotFound)
	callAs(reporterID, http.MethodGet, "/rest/servicedeskapi/request?requestOwnership=ALL_REQUESTS", "", http.StatusForbidden)

	// A request's optional parts appear only when expanded.
	callAs(reporterID, http.MethodPost, "/rest/servicedeskapi/request/"+printer+"/comment", `{"body":"It is *still* jammed.","public":true}`, http.StatusCreated)
	plain := callAs(reporterID, http.MethodGet, "/rest/servicedeskapi/request/"+printer, "", http.StatusOK)
	if strings.Contains(plain, `"comments"`) || strings.Contains(plain, `"participants"`) || !strings.Contains(plain, `"_expands":["serviceDesk","requestType","participant","sla","status","attachment","action","comment"]`) ||
		!strings.Contains(plain, `"currentStatus":{"status":"Done","statusCategory":"DONE"`) {
		t.Fatalf("unexpanded request = %s", plain)
	}
	expanded := callAs(reporterID, http.MethodGet, "/rest/servicedeskapi/request/"+printer+"?expand=participant,status,action,comment", "", http.StatusOK)
	for _, want := range []string{`"participants":{`, "Colin Colleague", `"statusCategory":"NEW"`, `"addParticipant":{"allowed":true}`, `"comments":{`, "still", `"_expands":["serviceDesk","requestType","sla","attachment"]`} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded request lacks %s: %s", want, expanded)
		}
	}
	if strings.Contains(expanded, `"renderedBody"`) {
		t.Fatalf("comment bodies were rendered without comment.renderedBody: %s", expanded)
	}
	if rendered := callAs(reporterID, http.MethodGet, "/rest/servicedeskapi/request/"+printer+"?expand=comment&expand=comment.renderedBody", "", http.StatusOK); !strings.Contains(rendered, `"renderedBody"`) {
		t.Fatalf("comment.renderedBody left bodies unrendered: %s", rendered)
	}
	if colleague := callAs(colleagueID, http.MethodGet, "/rest/servicedeskapi/request/"+printer+"?expand=action", "", http.StatusOK); !strings.Contains(colleague, `"addParticipant":{"allowed":false}`) || !strings.Contains(colleague, `"addComment":{"allowed":true}`) {
		t.Fatalf("a participant's actions = %s", colleague)
	}

	// The status chronology lists the current status first.
	var chronology struct {
		Size   int `json:"size"`
		Values []struct {
			Status         string `json:"status"`
			StatusCategory string `json:"statusCategory"`
		} `json:"values"`
	}
	if err = json.Unmarshal([]byte(callAs(reporterID, http.MethodGet, "/rest/servicedeskapi/request/"+printer+"/status", "", http.StatusOK)), &chronology); err != nil {
		t.Fatal(err)
	}
	if chronology.Size != 2 || chronology.Values[0].StatusCategory != "DONE" || chronology.Values[1].StatusCategory != "NEW" {
		t.Fatalf("status chronology = %+v", chronology)
	}

	// Comments filter by public and internal and expand their attachments.
	callAs(adminID, http.MethodPost, "/rest/servicedeskapi/request/"+vpn+"/comment", `{"body":"Customer-visible update","public":true}`, http.StatusCreated)
	callAs(adminID, http.MethodPost, "/rest/servicedeskapi/request/"+vpn+"/comment", `{"body":"Internal investigation","public":false}`, http.StatusCreated)
	commentsPath := "/rest/servicedeskapi/request/" + vpn + "/comment"
	if internal := callAs(adminID, http.MethodGet, commentsPath+"?public=false", "", http.StatusOK); !strings.Contains(internal, "Internal investigation") || strings.Contains(internal, "Customer-visible update") {
		t.Fatalf("internal comments = %s", internal)
	}
	if public := callAs(adminID, http.MethodGet, commentsPath+"?internal=false", "", http.StatusOK); strings.Contains(public, "Internal investigation") || !strings.Contains(public, "Customer-visible update") {
		t.Fatalf("public comments = %s", public)
	}
	if customer := callAs(reporterID, http.MethodGet, commentsPath+"?public=true&internal=true", "", http.StatusOK); strings.Contains(customer, "Internal investigation") {
		t.Fatalf("a customer saw an internal comment: %s", customer)
	}
	callAs(adminID, http.MethodGet, commentsPath+"?public=sometimes", "", http.StatusBadRequest)
	// _expands names renderedBody, so look for the keys themselves.
	if plainComments := callAs(adminID, http.MethodGet, commentsPath, "", http.StatusOK); strings.Contains(plainComments, `"attachments":`) || strings.Contains(plainComments, `"renderedBody":`) || !strings.Contains(plainComments, `"_expands":["attachment","renderedBody"]`) {
		t.Fatalf("unexpanded comments = %s", plainComments)
	}

	// Request attachments are served with ranges, and thumbnails are images.
	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("file", "trace.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte("0123456789 connection trace")); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	uploadRequest := httptest.NewRequest(http.MethodPost, "/rest/servicedeskapi/servicedesk/"+serviceDeskID+"/attachTemporaryFile", &upload)
	uploadRequest.SetBasicAuth(reporterID+"@example.test", reporterID)
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	uploadRequest.Header.Set("X-Atlassian-Token", "no-check")
	uploadResponse := httptest.NewRecorder()
	h.ServeHTTP(uploadResponse, uploadRequest)
	var temporary struct {
		TemporaryAttachments []struct {
			TemporaryAttachmentID string `json:"temporaryAttachmentId"`
		} `json:"temporaryAttachments"`
	}
	if err = json.Unmarshal(uploadResponse.Body.Bytes(), &temporary); err != nil || len(temporary.TemporaryAttachments) != 1 {
		t.Fatalf("temporary upload = %d %s", uploadResponse.Code, uploadResponse.Body.String())
	}
	// Jira identifies a request attachment only by its links.
	var attached struct {
		Attachments struct {
			Values []struct {
				Links struct {
					Content string `json:"content"`
				} `json:"_links"`
			} `json:"values"`
		} `json:"attachments"`
	}
	if err = json.Unmarshal([]byte(callAs(reporterID, http.MethodPost, "/rest/servicedeskapi/request/"+vpn+"/attachment", `{"temporaryAttachmentIds":["`+temporary.TemporaryAttachments[0].TemporaryAttachmentID+`"],"public":true,"additionalComment":{"body":"Trace attached"}}`, http.StatusCreated)), &attached); err != nil || len(attached.Attachments.Values) != 1 {
		t.Fatalf("attached = %+v err=%v", attached, err)
	}
	attachmentPath := strings.TrimPrefix(attached.Attachments.Values[0].Links.Content, "https://zzira.test")
	if !strings.HasPrefix(attachmentPath, "/rest/servicedeskapi/request/"+vpn+"/attachment/") || strings.HasSuffix(attachmentPath, "/") {
		t.Fatalf("attachment content link = %q", attached.Attachments.Values[0].Links.Content)
	}
	rangeRequest := httptest.NewRequest(http.MethodGet, attachmentPath, nil)
	rangeRequest.SetBasicAuth(reporterID+"@example.test", reporterID)
	rangeRequest.Header.Set("Range", "bytes=0-9")
	rangeResponse := httptest.NewRecorder()
	h.ServeHTTP(rangeResponse, rangeRequest)
	if rangeResponse.Code != http.StatusPartialContent || rangeResponse.Body.String() != "0123456789" {
		t.Fatalf("ranged attachment content = %d %q", rangeResponse.Code, rangeResponse.Body.String())
	}
	thumbnailRequest := httptest.NewRequest(http.MethodGet, attachmentPath+"/thumbnail", nil)
	thumbnailRequest.SetBasicAuth(reporterID+"@example.test", reporterID)
	thumbnailResponse := httptest.NewRecorder()
	h.ServeHTTP(thumbnailResponse, thumbnailRequest)
	if thumbnailResponse.Code != http.StatusOK || !strings.HasPrefix(thumbnailResponse.Header().Get("Content-Type"), "image/") {
		t.Fatalf("attachment thumbnail = %d %s", thumbnailResponse.Code, thumbnailResponse.Header().Get("Content-Type"))
	}
	if expandedComments := callAs(reporterID, http.MethodGet, commentsPath+"?expand=attachment", "", http.StatusOK); !strings.Contains(expandedComments, `"attachments":{`) || !strings.Contains(expandedComments, "trace.txt") {
		t.Fatalf("comments with attachments = %s", expandedComments)
	}

	// A comment refused for its length leaves the request where it was.
	tooLong := strings.Repeat("x", 32768)
	callAs(reporterID, http.MethodPost, "/rest/servicedeskapi/request/"+vpn+"/transition", `{"id":"31","additionalComment":{"body":"`+tooLong+`"}}`, http.StatusBadRequest)
	if still := callAs(reporterID, http.MethodGet, "/rest/servicedeskapi/request/"+vpn, "", http.StatusOK); !strings.Contains(still, `"statusCategory":"NEW"`) {
		t.Fatalf("a refused transition comment moved the request: %s", still)
	}
}
