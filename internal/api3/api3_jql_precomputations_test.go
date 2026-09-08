package api3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/store"
)

func TestJQLFunctionPrecomputationAppJourney(t *testing.T) {
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
	workspaceID, otherWorkspaceID := store.NewID("ws"), store.NewID("ws")
	appPrincipal, otherPrincipal, human := store.NewID("app"), store.NewID("app"), store.NewID("usr")
	installationID, otherInstallationID := store.NewID("install"), store.NewID("install")
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, statement, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	for _, workspace := range []string{workspaceID, otherWorkspaceID} {
		exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'JQL app test')`, workspace)
	}
	for _, principal := range []string{appPrincipal, otherPrincipal, human} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'!test!',$1)`, principal, principal+"@example.test")
	}
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member'),($1,$3,'member')`, workspaceID, appPrincipal, human)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, otherWorkspaceID, otherPrincipal)
	exec(`INSERT INTO app_installations(id,workspace_id,principal_id,app_key,name,base_url,version,status,secret_ciphertext,descriptor)
		VALUES($1,$2,$3,'com.example.primary','Primary app','https://app.example.test','1','active',$4,'{}')`, installationID, workspaceID, appPrincipal, []byte("secret"))
	exec(`INSERT INTO app_installations(id,workspace_id,principal_id,app_key,name,base_url,version,status,secret_ciphertext,descriptor)
		VALUES($1,$2,$3,'com.example.other','Other app','https://other.example.test','1','active',$4,'{}')`, otherInstallationID, otherWorkspaceID, otherPrincipal, []byte("secret"))
	t.Cleanup(func() {
		exec(`DELETE FROM memberships WHERE workspace_id=ANY($1)`, []string{workspaceID, otherWorkspaceID})
		exec(`DELETE FROM workspaces WHERE id=ANY($1)`, []string{workspaceID, otherWorkspaceID})
		exec(`DELETE FROM users WHERE id=ANY($1)`, []string{appPrincipal, otherPrincipal, human})
	})

	firstUsed := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	first, err := st.EnsureJQLFunctionPrecomputation(ctx, installationID, "com.example.primary__risk", "riskIssues", "issue", "in", []string{"high"}, firstUsed)
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.EnsureJQLFunctionPrecomputation(ctx, installationID, "com.example.primary__team", "teamIssues", "issue", "in", []string{"platform"}, firstUsed.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.EnsureJQLFunctionPrecomputation(ctx, otherInstallationID, "com.example.other__private", "privateIssues", "issue", "in", nil, firstUsed.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	again, err := st.EnsureJQLFunctionPrecomputation(ctx, installationID, "com.example.primary__risk", "riskIssues", "issue", "IN", []string{"high"}, firstUsed.Add(3*time.Hour))
	if err != nil || again.ID != first.ID || !again.UsedAt.Equal(firstUsed.Add(3*time.Hour)) {
		t.Fatalf("idempotent invocation = %+v, %v", again, err)
	}

	handler := &Handler{Store: st, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(principal, method, path string, body any, want int) map[string]any {
		t.Helper()
		var input string
		if body != nil {
			raw, marshalErr := json.Marshal(body)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			input = string(raw)
		}
		request := httptest.NewRequest(method, path, strings.NewReader(input))
		request = request.WithContext(authn.WithPrincipal(request.Context(), principal))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		if response.Code == http.StatusNoContent {
			return nil
		}
		var result map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatalf("decode response: %v: %s", err, response.Body.String())
		}
		return result
	}

	call(human, http.MethodGet, "/rest/api/3/jql/function/computation", nil, http.StatusForbidden)
	page := call(appPrincipal, http.MethodGet, "/rest/api/3/jql/function/computation?maxResults=1&orderBy=-used", nil, http.StatusOK)
	if page["total"] != float64(2) || page["isLast"] != false || page["nextPage"] == "" {
		t.Fatalf("precomputation page = %#v", page)
	}
	values := page["values"].([]any)
	if values[0].(map[string]any)["id"] != first.ID {
		t.Fatalf("descending used order = %#v", values)
	}
	filtered := call(appPrincipal, http.MethodGet, "/rest/api/3/jql/function/computation?functionKey=com.example.primary__team", nil, http.StatusOK)
	if filtered["total"] != float64(1) || filtered["values"].([]any)[0].(map[string]any)["id"] != second.ID {
		t.Fatalf("function filter = %#v", filtered)
	}
	call(appPrincipal, http.MethodGet, "/rest/api/3/jql/function/computation?functionKey=com.example.primary__missing", nil, http.StatusNotFound)
	call(appPrincipal, http.MethodGet, "/rest/api/3/jql/function/computation?orderBy=name", nil, http.StatusBadRequest)

	fragment := "issue in (APP-1, APP-2)"
	missingID := "00000000-0000-0000-0000-000000000000"
	call(appPrincipal, http.MethodPost, "/rest/api/3/jql/function/computation", map[string]any{"values": []any{
		map[string]any{"id": first.ID, "value": fragment}, map[string]any{"id": missingID, "error": "gone"},
	}}, http.StatusNotFound)
	unchanged := call(appPrincipal, http.MethodPost, "/rest/api/3/jql/function/computation/search", map[string]any{"precomputationIDs": []string{first.ID}}, http.StatusOK)
	if _, exists := unchanged["precomputations"].([]any)[0].(map[string]any)["value"]; exists {
		t.Fatalf("failed batch changed a value: %#v", unchanged)
	}
	updated := call(appPrincipal, http.MethodPost, "/rest/api/3/jql/function/computation?skipNotFoundPrecomputations=true", map[string]any{"values": []any{
		map[string]any{"id": first.ID, "value": fragment}, map[string]any{"id": missingID, "error": "gone"},
	}}, http.StatusOK)
	if updated["notFoundPrecomputationIDs"].([]any)[0] != missingID {
		t.Fatalf("skipped response = %#v", updated)
	}
	message := "Function result is stale"
	call(appPrincipal, http.MethodPost, "/rest/api/3/jql/function/computation", map[string]any{"values": []any{map[string]any{"id": second.ID, "error": message}}}, http.StatusNoContent)
	result := call(appPrincipal, http.MethodPost, "/rest/api/3/jql/function/computation/search?orderBy=functionKey", map[string]any{"precomputationIDs": []string{first.ID, second.ID, other.ID, missingID}}, http.StatusOK)
	if len(result["precomputations"].([]any)) != 2 || len(result["notFoundPrecomputationIDs"].([]any)) != 2 {
		t.Fatalf("owned ID search = %#v", result)
	}
	beans := result["precomputations"].([]any)
	if beans[0].(map[string]any)["value"] != fragment || beans[1].(map[string]any)["error"] != message {
		t.Fatalf("updated beans = %#v", beans)
	}
	call(appPrincipal, http.MethodPost, "/rest/api/3/jql/function/computation", map[string]any{"values": []any{map[string]any{"id": first.ID, "value": "x", "error": "y"}}}, http.StatusBadRequest)
	call(appPrincipal, http.MethodPost, "/rest/api/3/jql/function/computation/search", map[string]any{"precomputationIDs": []string{first.ID, first.ID}}, http.StatusBadRequest)
}
