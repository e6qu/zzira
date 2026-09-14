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
	"github.com/e6qu/zzira/internal/store"
)

// TestJQLSanitizeAndPersonalDataCleaner covers Jira's two query rewriters:
// sanitize turns what a viewer may not see into IDs, leaving the rest of the
// query as written, and the personal data cleaner turns the people a query
// names into account IDs, reporting the ones it cannot find.
func TestJQLSanitizeAndPersonalDataCleaner(t *testing.T) {
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
	stamp := time.Now().UnixNano() % 1000000
	secretKey, openKey := fmt.Sprintf("SQ%06d", stamp), fmt.Sprintf("SO%06d", stamp)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'JQL sanitize')`, workspaceID)
	for _, identity := range []struct{ id, role, name string }{{adminID, "admin", "Sanitize Admin " + adminID}, {memberID, "member", "Sanitize Member " + memberID}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", identity.name)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})

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
	object := func(body string) map[string]any {
		t.Helper()
		decoded := map[string]any{}
		if decodeErr := json.Unmarshal([]byte(body), &decoded); decodeErr != nil {
			t.Fatalf("decode %s: %v", body, decodeErr)
		}
		return decoded
	}
	wireID := func(value any) string {
		if number, ok := value.(float64); ok {
			return fmt.Sprintf("%.0f", number)
		}
		return fmt.Sprint(value)
	}

	// Two projects with a component of the same name, a version and a custom
	// field, none of which the member may browse.
	secretID := wireID(object(call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+secretKey+`","name":"Secret `+secretKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated))["id"])
	openID := wireID(object(call(adminID, http.MethodPost, "/rest/api/3/project", `{"key":"`+openKey+`","name":"Open `+openKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated))["id"])
	secretComponent := wireID(object(call(adminID, http.MethodPost, "/rest/api/3/component", `{"name":"Backend","project":"`+secretKey+`"}`, http.StatusCreated))["id"])
	openComponent := wireID(object(call(adminID, http.MethodPost, "/rest/api/3/component", `{"name":"backend","project":"`+openKey+`"}`, http.StatusCreated))["id"])
	version := wireID(object(call(adminID, http.MethodPost, "/rest/api/3/version", `{"name":"Launch `+secretKey+`","projectId":`+secretID+`}`, http.StatusCreated))["id"])
	fieldName := "Ring " + secretKey
	fieldID := fmt.Sprint(object(call(adminID, http.MethodPost, "/rest/api/3/field", `{"name":"`+fieldName+`","type":"select"}`, http.StatusCreated))["id"])
	exec(`DELETE FROM permission_scheme_grants WHERE workspace_id=$1 AND permission_key='BROWSE_PROJECTS' AND holder_type='projectRole' AND holder_value='10001'`, workspaceID)
	call(memberID, http.MethodGet, "/rest/api/3/project/"+secretKey, "", http.StatusNotFound)

	sanitize := func(body string) []map[string]any {
		t.Helper()
		var response struct {
			Queries []map[string]any `json:"queries"`
		}
		if decodeErr := json.Unmarshal([]byte(call(adminID, http.MethodPost, "/rest/api/3/jql/sanitize", body, http.StatusOK)), &response); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return response.Queries
	}
	query := `project = "Secret ` + secretKey + `" AND component = Backend AND fixVersion in ("Launch ` + secretKey + `", 1.0) AND "` + fieldName + `" = x ORDER BY key`
	encoded, _ := json.Marshal(query)
	results := sanitize(`{"queries":[{"accountId":"` + memberID + `","query":` + string(encoded) + `},{"accountId":"` + adminID + `","query":` + string(encoded) + `},{"query":` + string(encoded) + `},{"accountId":"nobody","query":` + string(encoded) + `},{"accountId":"` + memberID + `","query":"status ="}]}`)
	hidden := `project = ` + secretID + ` AND component in (` + secretComponent + `, ` + openComponent + `) AND fixVersion in (` + version + `, 1.0) AND cf[` + strings.TrimPrefix(fieldID, "customfield_") + `] = x ORDER BY key`
	if len(results) != 5 || results[0]["sanitizedQuery"] != hidden || results[0]["accountId"] != memberID {
		t.Fatalf("member sanitize = %v\nwant %s", results[0], hidden)
	}
	if results[1]["sanitizedQuery"] != query {
		t.Fatalf("admin sanitize = %v", results[1])
	}
	if _, present := results[2]["accountId"]; present || results[2]["sanitizedQuery"] != hidden {
		t.Fatalf("anonymous sanitize = %v", results[2])
	}
	if _, present := results[3]["sanitizedQuery"]; present || results[3]["errors"] == nil {
		t.Fatalf("unknown account sanitize = %v", results[3])
	}
	if _, present := results[4]["sanitizedQuery"]; present || !strings.Contains(fmt.Sprint(results[4]["errors"]), "Error in the JQL Query") {
		t.Fatalf("unparsable sanitize = %v", results[4])
	}
	call(memberID, http.MethodPost, "/rest/api/3/jql/sanitize", `{"queries":[{"query":"project = X"}]}`, http.StatusForbidden)
	call(adminID, http.MethodPost, "/rest/api/3/jql/sanitize", `{"queries":[]}`, http.StatusBadRequest)
	call(adminID, http.MethodPost, "/rest/api/3/jql/sanitize", `{"queries":[{"query":"project = X"},{"query":"project = X"}]}`, http.StatusBadRequest)

	// A sanitized project clause still finds the project's work, as do its
	// key and name.
	issueKey := object(call(adminID, http.MethodPost, "/rest/api/3/issue", `{"fields":{"project":{"key":"`+secretKey+`"},"summary":"Sanitized work","issuetype":{"name":"Task"}}}`, http.StatusCreated))["key"].(string)
	for _, clause := range []string{"project = " + secretID, "project = " + strings.ToLower(secretKey), `project in ("Secret ` + secretKey + `")`} {
		encodedClause, _ := json.Marshal(clause)
		if found := call(adminID, http.MethodPost, "/rest/api/3/search/jql", `{"jql":`+string(encodedClause)+`,"fields":["summary"]}`, http.StatusOK); !strings.Contains(found, issueKey) {
			t.Fatalf("%s found %s", clause, found)
		}
	}
	encodedOther, _ := json.Marshal("project = " + openID)
	if found := call(adminID, http.MethodPost, "/rest/api/3/search/jql", `{"jql":`+string(encodedOther)+`,"fields":["summary"]}`, http.StatusOK); strings.Contains(found, issueKey) {
		t.Fatalf("another project's id found %s", found)
	}

	// The personal data cleaner.
	var cleaned struct {
		QueryStrings            []string            `json:"queryStrings"`
		QueriesWithUnknownUsers []map[string]string `json:"queriesWithUnknownUsers"`
	}
	cleanerBody, _ := json.Marshal(map[string]any{"queryStrings": []string{
		"assignee = " + memberID + "@example.test AND project = " + secretKey,
		`reporter in ("Sanitize Member ` + memberID + `", mia)`,
		`status CHANGED BY "sanitize admin ` + adminID + `" ORDER BY key`,
		"assignee was " + adminID + " AND assignee in (EMPTY, currentUser())",
	}})
	if err = json.Unmarshal([]byte(call(memberID, http.MethodPost, "/rest/api/3/jql/pdcleaner", string(cleanerBody), http.StatusOK)), &cleaned); err != nil {
		t.Fatal(err)
	}
	wantConverted := []string{
		"assignee = " + memberID + " AND project = " + secretKey,
		"status CHANGED BY " + adminID + " ORDER BY key",
		"assignee was " + adminID + " AND assignee in (EMPTY, currentUser())",
	}
	if strings.Join(cleaned.QueryStrings, "\n") != strings.Join(wantConverted, "\n") {
		t.Fatalf("converted = %q", cleaned.QueryStrings)
	}
	if len(cleaned.QueriesWithUnknownUsers) != 1 || cleaned.QueriesWithUnknownUsers[0]["convertedQuery"] != "reporter in ("+memberID+", unknown)" || !strings.HasPrefix(cleaned.QueriesWithUnknownUsers[0]["originalQuery"], "reporter in") {
		t.Fatalf("unknown users = %v", cleaned.QueriesWithUnknownUsers)
	}
	call(memberID, http.MethodPost, "/rest/api/3/jql/pdcleaner", `{"queryStrings":["assignee = mia","status ="]}`, http.StatusBadRequest)
}
