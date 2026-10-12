package api3

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// documentedStatuses reads the statuses the pinned specification publishes for
// one operation, so the expectations below are tied to the contract rather
// than to a reading of it.
func documentedStatuses(t *testing.T, method, template string) []int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "specs", "jira-v3.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]map[string]struct {
			Responses map[string]json.RawMessage `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	item, ok := document.Paths[template]
	if !ok {
		t.Fatalf("%s is not in the pinned specification", template)
	}
	operation, ok := item[strings.ToLower(method)]
	if !ok {
		t.Fatalf("%s %s is not in the pinned specification", method, template)
	}
	statuses := []int{}
	for code := range operation.Responses {
		if number, err := strconv.Atoi(code); err == nil {
			statuses = append(statuses, number)
		}
	}
	slices.Sort(statuses)
	return statuses
}

// TestMissingResourceStatusFollowsTheContract pins the status each operation
// answers for a resource it cannot find. Jira does not report every one as
// 404: an operation publishes the statuses it can answer, and where 404 is
// absent while 400 is present, a resource Jira cannot find leaves the request
// invalid and 400 is the answer. Deletes are the clearest case, reporting 400
// for ids whose reads report 404. A paginated listing that publishes only
// success for the id it is given answers an empty page instead, and an
// operation that publishes neither 400 nor 404 keeps the platform's 404.
func TestMissingResourceStatusFollowsTheContract(t *testing.T) {
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
	workspaceID, adminID := store.NewID("ws"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Error statuses')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Status admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, adminID)
		exec(`DELETE FROM users WHERE id=$1`, adminID)
	})
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}

	for _, c := range []struct {
		method, template, path, body string
		want                         int
		// undocumented marks an operation that publishes no status for a
		// resource it cannot find, where the platform's own 404 stands.
		undocumented bool
	}{
		// Not one priority scheme operation publishes 404.
		{method: "GET", template: "/rest/api/3/priorityscheme/{schemeId}/priorities", path: "/rest/api/3/priorityscheme/99999/priorities", want: 400},
		{method: "GET", template: "/rest/api/3/priorityscheme/{schemeId}/projects", path: "/rest/api/3/priorityscheme/99999/projects", want: 400},
		{method: "DELETE", template: "/rest/api/3/priorityscheme/{schemeId}", path: "/rest/api/3/priorityscheme/99999", want: 400},
		{method: "PUT", template: "/rest/api/3/priorityscheme/{schemeId}", path: "/rest/api/3/priorityscheme/99999", body: `{"name":"Renamed"}`, want: 400},

		// Deleting a dashboard, and creating or deleting a permission scheme
		// grant, report 400 where reading the same ids reports 404.
		{method: "DELETE", template: "/rest/api/3/dashboard/{id}", path: "/rest/api/3/dashboard/99999", want: 400},
		{method: "GET", template: "/rest/api/3/dashboard/{id}", path: "/rest/api/3/dashboard/99999", want: 404},
		{method: "POST", template: "/rest/api/3/permissionscheme/{schemeId}/permission", path: "/rest/api/3/permissionscheme/99999/permission", body: `{"holder":{"type":"anyone"},"permission":"BROWSE_PROJECTS"}`, want: 400},
		{method: "DELETE", template: "/rest/api/3/permissionscheme/{schemeId}/permission/{permissionId}", path: "/rest/api/3/permissionscheme/99999/permission/99999", want: 400},
		{method: "GET", template: "/rest/api/3/permissionscheme/{schemeId}/permission/{permissionId}", path: "/rest/api/3/permissionscheme/99999/permission/99999", want: 404},

		// Creating a draft is alone among the workflow scheme operations in
		// publishing no 404.
		{method: "POST", template: "/rest/api/3/workflowscheme/{id}/createdraft", path: "/rest/api/3/workflowscheme/99999/createdraft", want: 400},
		{method: "GET", template: "/rest/api/3/workflowscheme/{id}", path: "/rest/api/3/workflowscheme/99999", want: 404},

		// The field operations: 400 where the contract publishes it, an empty
		// page where a listing publishes only success, 404 where the
		// operation publishes it.
		{method: "GET", template: "/rest/api/3/field/{fieldKey}/option", path: "/rest/api/3/field/customfield_99999/option", want: 400},
		{method: "GET", template: "/rest/api/3/field/{fieldId}/association/project", path: "/rest/api/3/field/customfield_99999/association/project", want: 400},
		{method: "GET", template: "/rest/api/3/field/{fieldId}/contexts", path: "/rest/api/3/field/customfield_99999/contexts", want: 200},
		{method: "GET", template: "/rest/api/3/field/{fieldId}/context/issuetypemapping", path: "/rest/api/3/field/customfield_99999/context/issuetypemapping", want: 200},
		{method: "GET", template: "/rest/api/3/field/{fieldId}/context", path: "/rest/api/3/field/customfield_99999/context", want: 404},
		{method: "GET", template: "/rest/api/3/field/{fieldId}/context/projectmapping", path: "/rest/api/3/field/customfield_99999/context/projectmapping", want: 404},
		{method: "GET", template: "/rest/api/3/config/fieldschemes/{id}/fields/{fieldId}/parameters", path: "/rest/api/3/config/fieldschemes/99999/fields/summary/parameters", want: 400},
		{method: "GET", template: "/rest/api/3/config/fieldschemes/{id}", path: "/rest/api/3/config/fieldschemes/99999", want: 404},

		// A path parameter outside its enumerated set addresses no resource,
		// which the platform answers with 404 and the contract leaves unsaid.
		{method: "GET", template: "/rest/api/3/license/approximateLicenseCount/product/{applicationKey}", path: "/rest/api/3/license/approximateLicenseCount/product/jira-nope", want: 404, undocumented: true},
		{method: "GET", template: "/rest/api/3/avatar/{type}/system", path: "/rest/api/3/avatar/nope/system", want: 404, undocumented: true},

		// Reading an issue security scheme publishes neither 400 nor 404,
		// while every operation on the same id publishes 404.
		{method: "GET", template: "/rest/api/3/issuesecurityschemes/{id}", path: "/rest/api/3/issuesecurityschemes/99999", want: 404, undocumented: true},
	} {
		documented := documentedStatuses(t, c.method, c.template)
		if slices.Contains(documented, c.want) == c.undocumented {
			t.Errorf("%s %s: expecting %d, which the contract publishes as %v", c.method, c.template, c.want, documented)
		}
		if c.want == 400 && slices.Contains(documented, 404) {
			t.Errorf("%s %s: the contract publishes 404, so a missing resource is not 400", c.method, c.template)
		}
		request := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		request.SetBasicAuth(adminID+"@example.test", adminID)
		if c.body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != c.want {
			t.Errorf("%s %s: %d want %d: %s", c.method, c.path, response.Code, c.want, response.Body.String())
			continue
		}
		if c.want != 200 {
			continue
		}
		// A listing that answers for an id it cannot find answers an empty
		// page, not a page of someone else's rows.
		var page struct {
			Total  int   `json:"total"`
			Values []any `json:"values"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Errorf("%s %s: decode: %v", c.method, c.path, err)
			continue
		}
		if page.Total != 0 || len(page.Values) != 0 {
			t.Errorf("%s %s: answered %d rows for a field that does not exist", c.method, c.path, page.Total)
		}
	}
}
