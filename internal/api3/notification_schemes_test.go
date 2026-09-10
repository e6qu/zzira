package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestNotificationSchemeContractAndDelivery(t *testing.T) {
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
	adminID, recipientID := store.NewID("usr"), store.NewID("usr")
	projectKey := fmt.Sprintf("N%08d", time.Now().UnixNano()%100000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Notification scheme contract')`, workspaceID)
	var defaultSchemeID int64
	if err = st.Pool.QueryRow(ctx, `SELECT id FROM notification_schemes WHERE workspace_id=$1 AND is_default`, workspaceID).Scan(&defaultSchemeID); err != nil {
		t.Fatal(err)
	}
	for _, identity := range []struct{ id, role string }{{adminID, "admin"}, {recipientID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Notify "+identity.id)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, recipientID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if user != "" {
			request.SetBasicAuth(user+"@example.test", user)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	projectResponse := call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Notifications","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)
	var project struct {
		ID int64 `json:"id"`
	}
	if err = json.Unmarshal(projectResponse.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	projectID := strconv.FormatInt(project.ID, 10)

	call(recipientID, http.MethodGet, "/rest/api/3/notificationscheme", "", http.StatusForbidden)
	defaultList := call(adminID, http.MethodGet, "/rest/api/3/notificationscheme?onlyDefault=true&expand=notificationSchemeEvents", "", http.StatusOK)
	if !strings.Contains(defaultList.Body.String(), `"name":"Default Notification Scheme"`) || !strings.Contains(defaultList.Body.String(), `"notificationType":"CurrentAssignee"`) {
		t.Fatal(defaultList.Body.String())
	}
	call(adminID, http.MethodGet, "/rest/api/3/notificationscheme?onlyDefault=maybe", "", http.StatusBadRequest)
	call(adminID, http.MethodPost, "/rest/api/3/notificationscheme", `{"name":"Bad","notificationSchemeEvents":[{"event":{"id":"999"},"notifications":[{"notificationType":"Reporter"}]}]}`, http.StatusBadRequest)
	created := call(adminID, http.MethodPost, "/rest/api/3/notificationscheme", `{"name":"Delivery team","description":"Notify the release recipient","notificationSchemeEvents":[{"event":{"id":"1"},"notifications":[{"notificationType":"User","parameter":"`+recipientID+`"}]}]}`, http.StatusCreated)
	var scheme struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &scheme); err != nil || scheme.ID == "" {
		t.Fatalf("scheme=%+v err=%v body=%s", scheme, err, created.Body.String())
	}
	schemeID, _ := strconv.ParseInt(scheme.ID, 10, 64)
	schemePath := "/rest/api/3/notificationscheme/" + scheme.ID
	detail := call(adminID, http.MethodGet, schemePath+"?expand=all", "", http.StatusOK)
	if !strings.Contains(detail.Body.String(), recipientID) || !strings.Contains(detail.Body.String(), `"Issue created"`) {
		t.Fatal(detail.Body.String())
	}
	call(adminID, http.MethodPut, schemePath, `{"name":"Delivery notifications","description":"Release delivery events"}`, http.StatusNoContent)
	call(adminID, http.MethodPut, schemePath+"/notification", `{"notificationSchemeEvents":[{"event":{"id":"6"},"notifications":[{"notificationType":"AllWatchers"}]}]}`, http.StatusNoContent)
	detail = call(adminID, http.MethodGet, schemePath+"?expand=notificationSchemeEvents", "", http.StatusOK)
	var decoded struct {
		NotificationSchemeEvents []struct {
			Notifications []struct {
				ID               int64  `json:"id"`
				NotificationType string `json:"notificationType"`
			} `json:"notifications"`
		} `json:"notificationSchemeEvents"`
	}
	if err = json.Unmarshal(detail.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	var watcherNotificationID int64
	for _, event := range decoded.NotificationSchemeEvents {
		for _, notification := range event.Notifications {
			if notification.NotificationType == "AllWatchers" {
				watcherNotificationID = notification.ID
			}
		}
	}
	if watcherNotificationID == 0 {
		t.Fatal(detail.Body.String())
	}
	call(adminID, http.MethodDelete, schemePath+"/notification/"+strconv.FormatInt(watcherNotificationID, 10), "", http.StatusNoContent)

	if err = st.AssignNotificationScheme(ctx, workspaceID, adminID, projectID, schemeID); err != nil {
		t.Fatal(err)
	}
	mappings := call(adminID, http.MethodGet, "/rest/api/3/notificationscheme/project?notificationSchemeId="+scheme.ID+"&projectId="+projectID, "", http.StatusOK)
	if !strings.Contains(mappings.Body.String(), `"notificationSchemeId":"`+scheme.ID+`"`) || !strings.Contains(mappings.Body.String(), `"projectId":"`+projectID+`"`) {
		t.Fatal(mappings.Body.String())
	}
	call(adminID, http.MethodGet, "/rest/api/3/project/"+projectKey+"/notificationscheme?expand=all", "", http.StatusOK)
	call(recipientID, http.MethodGet, "/rest/api/3/project/"+projectKey+"/notificationscheme", "", http.StatusNotFound)

	issueResponse := call(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Ship notification schemes","issuetype":{"name":"Task"}}}`, http.StatusCreated)
	var issue struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err = json.Unmarshal(issueResponse.Body.Bytes(), &issue); err != nil {
		t.Fatal(err)
	}
	var notificationCount, emailCount, deliveryCount int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1 AND user_id=$2 AND entity_id=(SELECT id FROM issues WHERE jira_id=$3::bigint)`, workspaceID, recipientID, issue.ID).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE workspace_id=$1 AND recipient=$2`, workspaceID, recipientID+"@example.test").Scan(&emailCount); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM notification_event_deliveries WHERE workspace_id=$1 AND event_id=1`, workspaceID).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if notificationCount != 1 || emailCount != 1 || deliveryCount != 1 {
		t.Fatalf("inbox=%d email=%d deliveries=%d", notificationCount, emailCount, deliveryCount)
	}
	var issueID string
	var issueActionSeq int64
	if err = st.Pool.QueryRow(ctx, `SELECT i.id,a.seq FROM issues i JOIN actions a ON a.workspace_id=i.workspace_id AND a.entity_type='issue' AND a.entity_id=i.id WHERE i.workspace_id=$1 AND i.jira_id=$2::bigint ORDER BY a.seq LIMIT 1`, workspaceID, issue.ID).Scan(&issueID, &issueActionSeq); err != nil {
		t.Fatal(err)
	}
	if err = st.DeliverIssueNotification(ctx, workspaceID, adminID, issueID, issueActionSeq, 1, "issue_created", "created "+issue.Key); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1 AND user_id=$2`, workspaceID, recipientID).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if notificationCount != 1 {
		t.Fatalf("replayed delivery created %d inbox rows", notificationCount)
	}
	securitySchemeID := store.NewID("sec")
	exec(`INSERT INTO security_schemes(id,name,levels) VALUES($1,'Restricted notification', $2::jsonb)`, securitySchemeID, `[{"id":"private","name":"Private","members":["`+adminID+`"]}]`)
	exec(`UPDATE projects SET security_scheme_id=$2 WHERE id=$1`, projectID, securitySchemeID)
	call(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+projectKey+`"},"summary":"Private notification","issuetype":{"name":"Task"},"security":{"id":"private"}}}`, http.StatusCreated)
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id=$1 AND user_id=$2`, workspaceID, recipientID).Scan(&notificationCount); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE workspace_id=$1 AND recipient=$2`, workspaceID, recipientID+"@example.test").Scan(&emailCount); err != nil {
		t.Fatal(err)
	}
	if notificationCount != 1 || emailCount != 1 {
		t.Fatalf("restricted delivery leaked: inbox=%d email=%d", notificationCount, emailCount)
	}

	call(adminID, http.MethodDelete, schemePath, "", http.StatusBadRequest)
	if err = st.AssignNotificationScheme(ctx, workspaceID, adminID, projectID, defaultSchemeID); err != nil {
		t.Fatal(err)
	}
	call(adminID, http.MethodDelete, schemePath, "", http.StatusNoContent)
	call(adminID, http.MethodGet, schemePath, "", http.StatusNotFound)
	var actions int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type IN ('notification_scheme','event_notification','project_notification_scheme','notification')`, workspaceID).Scan(&actions); err != nil || actions < 6 {
		t.Fatalf("actions=%d err=%v", actions, err)
	}
}
