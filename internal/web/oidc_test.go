package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/store"
)

func TestValidOIDCURL(t *testing.T) {
	t.Setenv("ZZIRA_ALLOW_INSECURE_OIDC", "")
	if err := validOIDCURL("https://sso.example.test/tenant"); err != nil {
		t.Fatalf("HTTPS URL rejected: %v", err)
	}
	for _, raw := range []string{"", "relative/path", "http://sso.example.test"} {
		if err := validOIDCURL(raw); err == nil {
			t.Fatalf("insecure or non-absolute URL %q accepted", raw)
		}
	}
	t.Setenv("ZZIRA_ALLOW_INSECURE_OIDC", "true")
	if err := validOIDCURL("http://localhost:8080"); err != nil {
		t.Fatalf("explicit local insecure OIDC URL rejected: %v", err)
	}
	if err := validOIDCURL("http://sso.example.test"); err == nil {
		t.Fatal("non-loopback insecure OIDC URL accepted")
	}
	for _, raw := range []string{"https://user:pass@sso.example.test", "https://sso.example.test?x=1", "https://sso.example.test#fragment"} {
		if err := validOIDCURL(raw); err == nil {
			t.Fatalf("ambiguous OIDC URL %q accepted", raw)
		}
	}
}

// A caller with no session must fail closed to the signed-out page, not back
// into the sign-in flow: redirecting to /auth/shauth here would silently
// re-enter the flow and read as "remained authenticated" to a check that only
// looks for this redirect. authn.Identify never touches the store when the
// request carries neither a session cookie nor Basic auth, so a nil Store is
// safe for this path.
func TestValidationRedirectsAnonymousCallerToSignedOut(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest("GET", "/auth/validation", nil)
	rec := httptest.NewRecorder()
	h.Validation(rec, req)
	if rec.Code != 302 {
		t.Fatalf("expected 302 Found, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/signed-out" {
		t.Fatalf("expected redirect to /signed-out, got %q", loc)
	}
}

// TestValidationRendersTheAuthenticatedUsernameNotDisplayName covers the page
// Shauth (and equivalent SSO validators) load after login to confirm the
// identity a session actually carries: data-testid="validation-username" must
// hold the account's login handle, not an arbitrary display name -- a real
// person's display name and an OIDC provider's preferred_username routinely
// differ, and a validator comparing this field against the identity it just
// authenticated needs the handle. The page's CSP (default-src 'none';
// style-src 'unsafe-inline') must also never depend on the shared app-shell
// template's external stylesheets, WASM, or scripts, since the browser blocks
// all of them under that policy.
func TestValidationRendersTheAuthenticatedUsernameNotDisplayName(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}

	userID := store.NewID("usr")
	email := userID + "@example.invalid"
	if _, err := st.CreateUser(ctx, userID, email, "test", "Arbitrary Display Name"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOIDCUsername(ctx, userID, "shauth-validator-2"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID) }()

	token, err := authn.LoginOIDC(ctx, st, userID, "id-token", "")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	h := &Handler{Store: st}
	req := httptest.NewRequest("GET", "/auth/validation", nil)
	req.AddCookie(&http.Cookie{Name: "zzira_session", Value: token})
	rec := httptest.NewRecorder()
	h.Validation(rec, req)

	if rec.Code != 200 {
		t.Fatalf("authenticated validation status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-testid="validation-username">shauth-validator-2<`) {
		t.Fatalf("validation-username did not render the login handle: %s", body)
	}
	if strings.Contains(body, "Arbitrary Display Name") {
		t.Fatalf("validation page rendered the display name instead of the username: %s", body)
	}
	for _, blocked := range []string{"/static/css/tokens.css", "/static/htmx/htmx.min.js", "/static/wasm/wasm_exec.js", "/static/app.js", "/static/zzira-worker.wasm", "/static/sqlite/sqlite3.wasm"} {
		if strings.Contains(body, blocked) {
			t.Fatalf("validation page referenced %q, which its own CSP (default-src 'none') blocks: %s", blocked, body)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("Content-Security-Policy = %q, want default-src 'none'", csp)
	}
}

func TestBackChannelLogoutNotFoundWithoutOIDCConfigured(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest("POST", "/auth/shauth/backchannel-logout", nil)
	rec := httptest.NewRecorder()
	h.BackChannelLogout(rec, req)
	if rec.Code != 404 {
		t.Fatalf("expected 404 when OIDC is not configured, got %d", rec.Code)
	}
}
