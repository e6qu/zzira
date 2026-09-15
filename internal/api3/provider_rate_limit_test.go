package api3

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestProviderRateLimits pins Jira Software's per-minute limit on the DevOps
// provider APIs: every bulk submission reports the window in its headers, and
// a caller who spends the window is answered 429 with Retry-After, in each
// API's error shape, while another API keeps its own window.
func TestProviderRateLimits(t *testing.T) {
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
	workspaceID, actorID := store.NewID("ws"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Rate limit test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Provider')`, actorID, actorID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actorID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actorID, store.HashToken(actorID))
	t.Cleanup(func() {
		exec(`DELETE FROM provider_rate_windows WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, actorID)
		exec(`DELETE FROM users WHERE id=$1`, actorID)
	})
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test", ProviderRateLimit: 2}
	call := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		request.SetBasicAuth(actorID+"@example.test", actorID)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for i, wantRemaining := range []string{"1", "0"} {
		response := call("/rest/builds/0.1/bulk")
		if response.Code == 429 || response.Header().Get("X-RateLimit-Limit") != "2" || response.Header().Get("X-RateLimit-Remaining") != wantRemaining || response.Header().Get("X-RateLimit-Reset") == "" {
			t.Fatalf("builds request %d: %d %v %s", i+1, response.Code, response.Header(), response.Body.String())
		}
	}
	limited := call("/rest/builds/0.1/bulk")
	if limited.Code != 429 || limited.Header().Get("Retry-After") == "" || !strings.Contains(limited.Body.String(), "API rate limit has been exceeded.") {
		t.Fatalf("spent builds window: %d %v %s", limited.Code, limited.Header(), limited.Body.String())
	}
	// Provider APIs answer in their own error shape.
	call("/rest/featureflags/0.1/bulk")
	call("/rest/featureflags/0.1/bulk")
	if flags := call("/rest/featureflags/0.1/bulk"); flags.Code != 429 || !strings.Contains(flags.Body.String(), `[{"message":"API rate limit has been exceeded."}]`) {
		t.Fatalf("spent feature flag window: %d %s", flags.Code, flags.Body.String())
	}
	// Another provider API has its own window.
	if other := call("/rest/deployments/0.1/bulk"); other.Code == 429 || other.Header().Get("X-RateLimit-Remaining") != "1" {
		t.Fatalf("deployments window: %d %v", other.Code, other.Header())
	}
	call("/rest/devinfo/0.10/bulk")
	call("/rest/devinfo/0.10/bulk")
	if devinfo := call("/rest/devinfo/0.10/bulk"); devinfo.Code != 429 || devinfo.Header().Get("Retry-After") == "" || !strings.Contains(devinfo.Body.String(), "API rate limit has been exceeded.") {
		t.Fatalf("spent devinfo window: %d %v %s", devinfo.Code, devinfo.Header(), devinfo.Body.String())
	}
}
