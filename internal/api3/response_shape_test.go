package api3

import (
	"context"
	"encoding/json"
	"net/http"
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

// pinnedSchema is one schema from the pinned specification, with the
// properties it publishes and whether it refuses any other.
type pinnedSchema struct {
	AdditionalProperties any                        `json:"additionalProperties"`
	Properties           map[string]json.RawMessage `json:"properties"`
	Items                json.RawMessage            `json:"items"`
	Ref                  string                     `json:"$ref"`
}

// publishedProperties reads the properties an operation's answer publishes,
// following the schema the operation names. rowOf names a property whose items
// carry the rows of a paged answer, so a page's rows are checked rather than
// the page.
func publishedProperties(t *testing.T, method, template, rowOf string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "specs", "jira-v3.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Content map[string]struct {
					Schema json.RawMessage `json:"schema"`
				} `json:"content"`
			} `json:"responses"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	resolve := func(encoded json.RawMessage) pinnedSchema {
		var schema pinnedSchema
		for {
			if err := json.Unmarshal(encoded, &schema); err != nil {
				t.Fatalf("%s %s: schema: %v", method, template, err)
			}
			if schema.Ref == "" {
				return schema
			}
			next, ok := document.Components.Schemas[strings.TrimPrefix(schema.Ref, "#/components/schemas/")]
			if !ok {
				t.Fatalf("%s %s: %s is not in the pinned specification", method, template, schema.Ref)
			}
			encoded, schema = next, pinnedSchema{}
		}
	}
	operation, ok := document.Paths[template][strings.ToLower(method)]
	if !ok {
		t.Fatalf("%s %s is not in the pinned specification", method, template)
	}
	var answer json.RawMessage
	for _, code := range []string{"200", "201"} {
		if response, ok := operation.Responses[code]; ok {
			for _, media := range response.Content {
				answer = media.Schema
			}
		}
		if answer != nil {
			break
		}
	}
	if answer == nil {
		t.Fatalf("%s %s publishes no successful answer", method, template)
	}
	schema := resolve(answer)
	if rowOf != "" {
		rows, ok := schema.Properties[rowOf]
		if !ok {
			t.Fatalf("%s %s: the answer has no %s", method, template, rowOf)
		}
		schema = resolve(rows)
		if schema.Items == nil {
			t.Fatalf("%s %s: %s carries no rows", method, template, rowOf)
		}
		schema = resolve(schema.Items)
	}
	if allowed, isBool := schema.AdditionalProperties.(bool); !isBool || allowed {
		t.Fatalf("%s %s no longer refuses undocumented properties", method, template)
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// TestAnswersCarryOnlyPublishedProperties pins the answers whose schema
// refuses anything it does not publish. A property a client cannot read is
// not a kindness: a schema that sets additionalProperties to false makes a
// validating client reject the whole answer. Creating something publishes
// less than reading it does, and a paged search publishes less than a read of
// one row, so each is checked against the schema its own operation names.
func TestAnswersCarryOnlyPublishedProperties(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Shapes')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Shape admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM filters WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM project_field_configuration_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM field_configuration_scheme_items WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM field_configuration_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM field_configuration_items WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM field_configurations WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, adminID)
		exec(`DELETE FROM users WHERE id=$1`, adminID)
	})
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	answer := func(method, path, body string, want int) map[string]any {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(adminID+"@example.test", adminID)
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		if response.Body.Len() > 0 && response.Body.Bytes()[0] == '{' {
			if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
				t.Fatalf("%s %s: decode: %v", method, path, err)
			}
		}
		return out
	}
	id := func(value any) string {
		switch typed := value.(type) {
		case string:
			return typed
		case float64:
			return strconv.FormatInt(int64(typed), 10)
		}
		return ""
	}

	answer(http.MethodPost, "/rest/api/3/project",
		`{"key":"SHP","name":"Shape work","projectTypeKey":"software","leadAccountId":"`+adminID+`"}`, http.StatusCreated)
	screen := answer(http.MethodPost, "/rest/api/3/screens", `{"name":"Shape screen"}`, http.StatusCreated)
	screenScheme := answer(http.MethodPost, "/rest/api/3/screenscheme",
		`{"name":"Shape screen scheme","description":"Shapes","screens":{"default":"`+id(screen["id"])+`"}}`, http.StatusCreated)
	typeScheme := answer(http.MethodPost, "/rest/api/3/issuetypescreenscheme",
		`{"name":"Shape work type screens","issueTypeMappings":[{"issueTypeId":"default","screenSchemeId":"`+id(screenScheme["id"])+`"}]}`, http.StatusCreated)
	project := answer(http.MethodGet, "/rest/api/3/project/SHP", "", http.StatusOK)
	// Governing a project is what puts a row in the assignments answer.
	answer(http.MethodPut, "/rest/api/3/issuetypescreenscheme/project",
		`{"issueTypeScreenSchemeId":"`+id(typeScheme["id"])+`","projectId":"`+id(project["id"])+`"}`, http.StatusNoContent)
	answer(http.MethodPost, "/rest/api/3/field", `{"name":"Shape field","description":"Shapes","type":"text"}`, http.StatusCreated)
	answer(http.MethodPost, "/rest/api/3/filter",
		`{"name":"Shape filter","description":"Shapes","jql":"project = SHP","sharePermissions":[{"type":"authenticated"}]}`, http.StatusOK)

	for _, c := range []struct {
		name, method, template, path, body, rowOf string
		want                                      int
	}{
		{name: "creating a screen scheme", method: http.MethodPost, template: "/rest/api/3/screenscheme",
			path: "/rest/api/3/screenscheme", body: `{"name":"Shape screen scheme two","description":"Shapes","screens":{"default":"` + id(screen["id"]) + `"}}`, want: http.StatusCreated},
		{name: "creating a field association scheme", method: http.MethodPost, template: "/rest/api/3/config/fieldschemes",
			path: "/rest/api/3/config/fieldschemes", body: `{"name":"Shape field scheme","description":"Shapes"}`, want: http.StatusOK},
		{name: "the site configuration", method: http.MethodGet, template: "/rest/api/3/configuration",
			path: "/rest/api/3/configuration", want: http.StatusOK},
		{name: "a searched field", method: http.MethodGet, template: "/rest/api/3/field/search",
			path: "/rest/api/3/field/search", rowOf: "values", want: http.StatusOK},
		{name: "a searched filter", method: http.MethodGet, template: "/rest/api/3/filter/search",
			path: "/rest/api/3/filter/search?expand=description,owner,jql,searchUrl,viewUrl,favourite,favouritedCount,sharePermissions,editPermissions,subscriptions,approximateLastUsed", rowOf: "values", want: http.StatusOK},
		{name: "a governed project", method: http.MethodGet, template: "/rest/api/3/issuetypescreenscheme/project",
			path: "/rest/api/3/issuetypescreenscheme/project", rowOf: "values", want: http.StatusOK},
		{name: "a read field association scheme", method: http.MethodGet, template: "/rest/api/3/config/fieldschemes",
			path: "/rest/api/3/config/fieldschemes", rowOf: "values", want: http.StatusOK},
	} {
		published := publishedProperties(t, c.method, c.template, c.rowOf)
		got := answer(c.method, c.path, c.body, c.want)
		checked := got
		if c.rowOf != "" {
			rows, _ := got[c.rowOf].([]any)
			if len(rows) == 0 {
				t.Errorf("%s: the answer carries no rows to check", c.name)
				continue
			}
			checked, _ = rows[0].(map[string]any)
		}
		for property := range checked {
			if !slices.Contains(published, property) {
				t.Errorf("%s carries %q, which that operation does not publish; it publishes %v", c.name, property, published)
			}
		}
	}

	// The assignments name the scheme and every project it governs, rather
	// than one project per row.
	governed := answer(http.MethodGet, "/rest/api/3/issuetypescreenscheme/project", "", http.StatusOK)
	rows, _ := governed["values"].([]any)
	if len(rows) == 0 {
		t.Fatal("no project assignments to read")
	}
	row := rows[0].(map[string]any)
	scheme, named := row["issueTypeScreenScheme"].(map[string]any)
	if !named || id(scheme["id"]) != id(typeScheme["id"]) {
		t.Errorf("an assignment names %v, want the work type screen scheme", row)
	}
	if projects, listed := row["projectIds"].([]any); !listed || len(projects) == 0 {
		t.Errorf("an assignment lists %v, want the projects the scheme governs", row["projectIds"])
	}
}
