package web

import (
	"net/http/httptest"
	"testing"
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

func TestBackChannelLogoutNotFoundWithoutOIDCConfigured(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest("POST", "/auth/shauth/backchannel-logout", nil)
	rec := httptest.NewRecorder()
	h.BackChannelLogout(rec, req)
	if rec.Code != 404 {
		t.Fatalf("expected 404 when OIDC is not configured, got %d", rec.Code)
	}
}
