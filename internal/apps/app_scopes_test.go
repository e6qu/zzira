package apps

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

// TestConfluenceOperationScopes pins the Connect scope an app needs for
// Confluence operations, taken per operation from the pinned specification:
// reads, writes, deletes, administration, email addresses, operations no app
// may call, and the broader scopes that include narrower ones.
func TestConfluenceOperationScopes(t *testing.T) {
	for _, c := range []struct{ method, path, want string }{
		{"GET", "/wiki/api/v2/pages", "read:confluence-content"},
		{"HEAD", "/wiki/api/v2/pages/42", "read:confluence-content"},
		{"PUT", "/wiki/api/v2/pages/42", "write:confluence-content"},
		{"DELETE", "/wiki/api/v2/pages/42", "delete:confluence-content"},
		{"POST", "/wiki/api/v2/space-roles", "admin:confluence"},
		{"GET", "/wiki/rest/api/user/email", "access:email-addresses"},
		{"GET", "/wiki/rest/api/audit", appScopeInaccessible},
		{"GET", "/wiki/api/v2/space-permissions/transition/combinations", "admin:confluence"},
		{"GET", "/rest/api/3/attachment/meta", "read:jira-work"},
		{"POST", "/rest/api/3/dashboard", "write:jira-work"},
		{"POST", "/rest/api/3/component", "admin:jira-project"},
		{"GET", "/rest/api/3/application-properties", "admin:jira"},
		{"DELETE", "/rest/api/3/mypreferences", "act-as-user:jira"},
		{"GET", "/rest/api/3/user/email", "access:email-addresses"},
		{"GET", "/rest/api/3/field/search", ""},
		{"GET", "/rest/api/3/announcementBanner", appScopeInaccessible},
		{"GET", "/rest/agile/1.0/board", "read:jira-work"},
		{"POST", "/rest/agile/1.0/backlog/issue", "write:jira-work"},
		{"DELETE", "/rest/devinfo/0.10/bulkByProperties", "delete:jira-work"},
		{"POST", "/rest/servicedeskapi/request", "write:jira-work"},
		{"POST", "/rest/servicedeskapi/customer", "admin:jira"},
		{"GET", "/rest/servicedeskapi/assets/workspace", appScopeInaccessible},
	} {
		scope, ok := appAPIScope(httptest.NewRequest(c.method, c.path, nil))
		if !ok || scope != c.want {
			t.Errorf("%s %s: got %q (%v) want %q", c.method, c.path, scope, ok, c.want)
		}
	}

	// Broader scopes include narrower ones, as Connect's levels nest.
	admin := &models.AppInstallation{Scopes: []string{"admin:confluence"}}
	writer := &models.AppInstallation{Scopes: []string{"write:confluence-content"}}
	for scope, want := range map[string]bool{"read:confluence-content": true, "write:confluence-content": true, "delete:confluence-content": true, "admin:confluence": true} {
		if appHoldsScope(admin, scope) != want {
			t.Errorf("admin holds %s: %v", scope, !want)
		}
	}
	if !appHoldsScope(writer, "read:confluence-content") || appHoldsScope(writer, "delete:confluence-content") || appHoldsScope(writer, "access:email-addresses") {
		t.Error("write scope implies only reading")
	}

	// A Jira project administrator can delete but not administer the site.
	projectAdmin := &models.AppInstallation{Scopes: []string{"admin:jira-project"}}
	if !appHoldsScope(projectAdmin, "delete:jira-work") || !appHoldsScope(projectAdmin, "read:jira-work") || appHoldsScope(projectAdmin, "admin:jira") {
		t.Error("project administration covers deleting and nothing above it")
	}

	// A Connect descriptor keeps its DELETE, PROJECT_ADMIN, ADMIN and
	// ACT_AS_USER levels.
	for connect, want := range map[string][]string{
		"DELETE":                 {"delete:confluence-content", "delete:jira-work"},
		"PROJECT_ADMIN":          {"admin:jira-project"},
		"ADMIN":                  {"admin:confluence", "delete:confluence-content", "admin:jira", "admin:jira-project"},
		"ACCESS_EMAIL_ADDRESSES": {"access:email-addresses"},
		"ACT_AS_USER":            {"act-as-user:jira"},
	} {
		raw, _ := json.Marshal(map[string]any{"key": "scoped-app", "name": "Scoped", "baseUrl": "https://apps.example.test/scoped", "scopes": []string{connect}, "authentication": map[string]string{"type": "jwt"}, "modules": map[string]any{}})
		descriptor, err := parseConnectDescriptor(raw)
		if err != nil {
			t.Fatalf("%s: %v", connect, err)
		}
		for _, scope := range want {
			if !appHoldsScope(&models.AppInstallation{Scopes: descriptor.Scopes}, scope) {
				t.Errorf("Connect %s does not grant %s: %v", connect, scope, descriptor.Scopes)
			}
		}
	}

	// Every pinned Confluence operation is in the table.
	count := 0
	for _, file := range []string{"../../api/specs/confluence-v2.json", "../../api/specs/confluence-v1.json"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var spec struct {
			Paths map[string]map[string]json.RawMessage `json:"paths"`
		}
		if err := json.Unmarshal(raw, &spec); err != nil {
			t.Fatal(err)
		}
		for _, item := range spec.Paths {
			for method := range item {
				switch method {
				case "get", "post", "put", "patch", "delete", "head", "options":
					count++
				}
			}
		}
	}
	if count != confluenceRows() {
		t.Fatalf("scope table has %d Confluence operations, the specifications %d; run api/conformance/app_scopes.py", confluenceRows(), count)
	}
}

func confluenceRows() int {
	count := 0
	for _, row := range appScopeTable {
		if row.Product == "confluence" {
			count++
		}
	}
	return count
}
