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

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestAppTimeTrackingProvidersAndProjectTypeAccess covers Marketplace time
// tracking providers, which an app declares and an administrator selects, and
// accessible project types, which follow the site's products and the person's
// access to them.
func TestAppTimeTrackingProvidersAndProjectTypeAccess(t *testing.T) {
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
	appKey := fmt.Sprintf("timesheets-%d", time.Now().UnixNano()%1000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Providers')`, workspaceID)
	for _, identity := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Providers "+identity.id)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	principals := []string{}
	t.Cleanup(func() {
		exec(`DELETE FROM role_bindings WHERE scope_type='product' AND scope_id IN (SELECT p.id::text FROM products p JOIN sites si ON si.id=p.site_id WHERE si.workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM app_installations WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range append([]string{adminID, memberID}, principals...) {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	raw := []byte(`{"key":"` + appKey + `","name":"Timesheets","baseUrl":"https://time.example.test","authentication":{"type":"jwt"},"scopes":["READ"],
		"modules":{"adminPages":[{"key":"timesheet-settings","url":"/settings","name":{"value":"Timesheet settings"}}],
		"jiraTimeTrackingProviders":[{"key":"timesheets","name":{"value":"Timesheet tracking"},"adminPageKey":"timesheet-settings"}]}}`)
	descriptor, err := apps.ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := st.InstallApp(ctx, workspaceID, adminID, descriptor, raw, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	principals = append(principals, installation.PrincipalID)

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(user+"@example.test", user)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}

	providerKey := appKey + "__timesheets"
	var providers []struct {
		Key, Name, URL string
	}
	if err = json.Unmarshal([]byte(call(adminID, http.MethodGet, "/rest/api/3/configuration/timetracking/list", "", http.StatusOK)), &providers); err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 || providers[0].Key != "Jira" || providers[1].Key != providerKey || providers[1].Name != "Timesheet tracking" ||
		providers[1].URL != "https://zzira.test/plugins/servlet/ac/"+appKey+"/timesheet-settings" {
		t.Fatalf("providers = %+v", providers)
	}
	call(memberID, http.MethodGet, "/rest/api/3/configuration/timetracking/list", "", http.StatusForbidden)
	call(adminID, http.MethodPut, "/rest/api/3/configuration/timetracking", `{"key":"`+appKey+`__missing"}`, http.StatusBadRequest)
	call(adminID, http.MethodPut, "/rest/api/3/configuration/timetracking", `{"key":"`+providerKey+`"}`, http.StatusNoContent)
	if selected := call(adminID, http.MethodGet, "/rest/api/3/configuration/timetracking", "", http.StatusOK); !strings.Contains(selected, `"key":"`+providerKey+`"`) {
		t.Fatalf("selected provider = %s", selected)
	}
	// A suspended app's provider is no longer offered.
	exec(`UPDATE app_installations SET status='suspended' WHERE id=$1`, installation.ID)
	if listed := call(adminID, http.MethodGet, "/rest/api/3/configuration/timetracking/list", "", http.StatusOK); strings.Contains(listed, providerKey) {
		t.Fatalf("a suspended app's provider is listed: %s", listed)
	}
	call(adminID, http.MethodPut, "/rest/api/3/configuration/timetracking", `{"key":"`+providerKey+`"}`, http.StatusBadRequest)

	// Project types follow products: the member loses Jira Service Management.
	exec(`DELETE FROM role_bindings WHERE scope_type='product' AND principal_id=$2 AND scope_id IN (SELECT p.id::text FROM products p JOIN sites si ON si.id=p.site_id WHERE si.workspace_id=$1 AND p.product_key='jira-service-management')`, workspaceID, memberID)
	call(memberID, http.MethodGet, "/rest/api/3/project/type/software/accessible", "", http.StatusOK)
	call(memberID, http.MethodGet, "/rest/api/3/project/type/business/accessible", "", http.StatusOK)
	call(memberID, http.MethodGet, "/rest/api/3/project/type/service_desk/accessible", "", http.StatusNotFound)
	call(adminID, http.MethodGet, "/rest/api/3/project/type/service_desk/accessible", "", http.StatusOK)
	call(memberID, http.MethodGet, "/rest/api/3/project/type/missing/accessible", "", http.StatusNotFound)
	if licensed := call(memberID, http.MethodGet, "/rest/api/3/project/type/accessible", "", http.StatusOK); !strings.Contains(licensed, `"key":"service_desk"`) {
		t.Fatalf("licensed project types = %s", licensed)
	}
	// Without the product, its project type has no license.
	exec(`UPDATE products SET enabled=false WHERE product_key='jira-service-management' AND site_id IN (SELECT id FROM sites WHERE workspace_id=$1)`, workspaceID)
	if licensed := call(adminID, http.MethodGet, "/rest/api/3/project/type/accessible", "", http.StatusOK); strings.Contains(licensed, `"key":"service_desk"`) || !strings.Contains(licensed, `"key":"software"`) || !strings.Contains(licensed, `"key":"business"`) {
		t.Fatalf("licensed project types without service management = %s", licensed)
	}
	call(adminID, http.MethodGet, "/rest/api/3/project/type/service_desk/accessible", "", http.StatusNotFound)
	// Project types themselves stay readable to anyone.
	anonymous := httptest.NewRecorder()
	h.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, "/rest/api/3/project/type/service_desk", nil))
	if anonymous.Code != http.StatusOK {
		t.Fatalf("anonymous project type read: %d %s", anonymous.Code, anonymous.Body.String())
	}
}
