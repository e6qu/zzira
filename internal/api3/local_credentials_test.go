package api3

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// An installation that accepts only its identity provider's sessions refuses
// an API token in Jira's own shape: the 401 body every other unauthenticated
// REST call returns, not a bare error page, so a client sees what it always
// sees when its credential is not accepted.
func TestLocalCredentialsOffRefusesAPITokensInJirasShape(t *testing.T) {
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
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspaceID, userID := store.NewID("ws"), store.NewID("usr")
	email := userID + "@example.test"
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Single sign-on only')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Token holder')`, userID, email)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, userID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, userID, store.HashToken(userID))
	t.Cleanup(func() {
		exec(`DELETE FROM sessions WHERE user_id=$1`, userID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, userID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, userID)
	})

	api := &Handler{Store: st, Commands: &commands.Service{Store: st, Blobs: blobs}, Blobs: blobs,
		WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(handler http.Handler, authorize func(*http.Request)) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/rest/api/3/myself", nil)
		authorize(request)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	token := func(r *http.Request) { r.SetBasicAuth(email, userID) }

	if response := call(api, token); response.Code != http.StatusOK {
		t.Fatalf("API token with local credentials on: %d %s", response.Code, response.Body.String())
	}

	closed := authn.RefuseLocalCredentials(api)
	response := call(closed, token)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("API token with local credentials off: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		ErrorMessages []string `json:"errorMessages"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("the refusal was not a Jira error body: %v: %s", err, response.Body.String())
	}
	if len(body.ErrorMessages) != 1 || body.ErrorMessages[0] != "You are not authenticated. Authentication required to perform this operation." {
		t.Fatalf("the refusal did not use Jira's wording: %s", response.Body.String())
	}

	// The session an identity provider established still reaches the API.
	session, _, err := authn.LoginOIDC(ctx, st, userID, "id-token", "https://issuer.example.invalid", userID+"-subject", "")
	if err != nil {
		t.Fatal(err)
	}
	if response := call(closed, func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: session})
	}); response.Code != http.StatusOK {
		t.Fatalf("single sign-on session with local credentials off: %d %s", response.Code, response.Body.String())
	}
}
