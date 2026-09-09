package api3

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestJiraSiteConfigurationContractJourney(t *testing.T) {
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
	workspaceID, adminID, memberID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Site configuration contract')`, workspaceID)
	for _, entry := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, entry.id, entry.id+"@example.test", entry.role)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, entry.id, entry.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, entry.id, store.HashToken(entry.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
		exec(`DELETE FROM organizations WHERE name='Site configuration contract'`)
	})

	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := &commands.Service{Store: st, Blobs: blobs}
	h := &Handler{Store: st, Commands: service, Blobs: blobs, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(userID, method, path, body, contentType string, want int) any {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(userID+"@example.test", userID)
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, r)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		if response.Body.Len() == 0 {
			return nil
		}
		var value any
		if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value
	}

	configuration := call(memberID, http.MethodGet, "/rest/api/3/configuration", "", "", http.StatusOK).(map[string]any)
	if configuration["attachmentsEnabled"] != true || configuration["timeTrackingEnabled"] != true {
		t.Fatalf("default configuration = %#v", configuration)
	}
	call(memberID, http.MethodGet, "/rest/api/3/announcementBanner", "", "", http.StatusForbidden)
	call(adminID, http.MethodPut, "/rest/api/3/announcementBanner", `{"message":"Planned maintenance","isEnabled":true,"isDismissible":true,"visibility":"private"}`, "application/json", http.StatusNoContent)
	banner := call(adminID, http.MethodGet, "/rest/api/3/announcementBanner", "", "", http.StatusOK).(map[string]any)
	if banner["message"] != "Planned maintenance" || banner["hashId"] == "" {
		t.Fatalf("banner = %#v", banner)
	}

	providers := call(adminID, http.MethodGet, "/rest/api/3/configuration/timetracking/list", "", "", http.StatusOK).([]any)
	if len(providers) != 1 || providers[0].(map[string]any)["key"] != "Jira" {
		t.Fatalf("providers = %#v", providers)
	}
	call(adminID, http.MethodPut, "/rest/api/3/configuration/timetracking", `{"key":"missing"}`, "application/json", http.StatusBadRequest)
	call(adminID, http.MethodPut, "/rest/api/3/configuration/timetracking", `{"key":"Jira"}`, "application/json", http.StatusNoContent)
	optionsBody := `{"defaultUnit":"hour","timeFormat":"hours","workingDaysPerWeek":4.5,"workingHoursPerDay":7.5}`
	options := call(adminID, http.MethodPut, "/rest/api/3/configuration/timetracking/options", optionsBody, "application/json", http.StatusOK).(map[string]any)
	if options["defaultUnit"] != "hour" || options["workingDaysPerWeek"] != 4.5 {
		t.Fatalf("options = %#v", options)
	}

	properties := call(adminID, http.MethodGet, "/rest/api/3/application-properties?keyFilter=clone", "", "", http.StatusOK).([]any)
	if len(properties) != 1 {
		t.Fatalf("filtered properties = %#v", properties)
	}
	property := call(adminID, http.MethodPut, "/rest/api/3/application-properties/jira.clone.prefix", `{"id":"jira.clone.prefix","value":"COPY -"}`, "application/json", http.StatusOK).(map[string]any)
	if property["value"] != "COPY -" {
		t.Fatalf("updated property = %#v", property)
	}
	property = call(adminID, http.MethodGet, "/rest/api/3/application-properties?key=jira.clone.prefix", "", "", http.StatusOK).(map[string]any)
	if property["value"] != "COPY -" {
		t.Fatalf("persisted property = %#v", property)
	}
	call(adminID, http.MethodPut, "/rest/api/3/application-properties/unknown", `{"value":"x"}`, "application/json", http.StatusNotFound)

	form := url.Values{"columns": {"issuekey", "summary", "status"}}.Encode()
	call(adminID, http.MethodPut, "/rest/api/3/settings/columns", form, "application/x-www-form-urlencoded", http.StatusOK)
	columns := call(adminID, http.MethodGet, "/rest/api/3/settings/columns", "", "", http.StatusOK).([]any)
	if len(columns) != 3 || columns[2].(map[string]any)["value"] != "status" {
		t.Fatalf("columns = %#v", columns)
	}
	call(adminID, http.MethodPut, "/rest/api/3/settings/columns", strings.Repeat("columns=summary&", 70000), "application/x-www-form-urlencoded", http.StatusBadRequest)
	var oversizedMultipart bytes.Buffer
	writer := multipart.NewWriter(&oversizedMultipart)
	field, err := writer.CreateFormField("columns")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = field.Write([]byte(strings.Repeat("x", (1<<20)+1))); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	call(adminID, http.MethodPut, "/rest/api/3/settings/columns", oversizedMultipart.String(), writer.FormDataContentType(), http.StatusBadRequest)
	call(adminID, http.MethodPut, "/rest/api/3/settings/columns", "", "application/x-www-form-urlencoded", http.StatusOK)
	columns = call(adminID, http.MethodGet, "/rest/api/3/settings/columns", "", "", http.StatusOK).([]any)
	if len(columns) != 0 {
		t.Fatalf("cleared columns = %#v", columns)
	}

	if err := service.UpdateGlobalJiraConfiguration(ctx, workspaceID, adminID, models.JiraSiteConfiguration{AttachmentsEnabled: true, IssueLinkingEnabled: true, SubTasksEnabled: true, UnassignedIssuesAllowed: true, VotingEnabled: true, WatchingEnabled: true}); err != nil {
		t.Fatal(err)
	}
	call(adminID, http.MethodGet, "/rest/api/3/configuration/timetracking", "", "", http.StatusNoContent)
	configuration = call(memberID, http.MethodGet, "/rest/api/3/configuration", "", "", http.StatusOK).(map[string]any)
	if configuration["timeTrackingEnabled"] != false {
		t.Fatalf("disabled time tracking = %#v", configuration)
	}
	if _, present := configuration["timeTrackingConfiguration"]; present {
		t.Fatalf("disabled configuration exposes options: %#v", configuration)
	}
	attachmentSettings := call(memberID, http.MethodGet, "/rest/api/3/attachment/meta", "", "", http.StatusOK).(map[string]any)
	if attachmentSettings["enabled"] != true { // global update kept attachments enabled
		t.Fatalf("attachment settings = %#v", attachmentSettings)
	}

	var audits, actions int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE target_type='jira_configuration' AND actor_id=$1`, adminID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type='jira_configuration'`, workspaceID).Scan(&actions); err != nil {
		t.Fatal(err)
	}
	if audits != 7 || actions != audits {
		t.Fatalf("audit/action counts = %d/%d", audits, actions)
	}
}
