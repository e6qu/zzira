package web

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/build"
	"github.com/e6qu/zzira/internal/store"
	jose "github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
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
	if err := validOIDCEndpointURL("https://sso.example.test/authorize?tenant=one"); err != nil {
		t.Fatalf("discovered endpoint query rejected: %v", err)
	}
	if err := validOIDCEndpointURL("https://sso.example.test/authorize#fragment"); err == nil {
		t.Fatal("discovered endpoint fragment accepted")
	}
}

func TestOIDCAuthorizationURLValidatesTheFinalRedirect(t *testing.T) {
	t.Setenv("ZZIRA_ALLOW_INSECURE_OIDC", "")
	o := &OIDC{config: oauth2.Config{Endpoint: oauth2.Endpoint{AuthURL: "https://sso.example.test/authorize"}}}
	target, err := o.authorizationURL("state-value", "nonce-value", "verifier-value")
	if err != nil {
		t.Fatalf("authorization URL: %v", err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "https" || parsed.Host != "sso.example.test" || parsed.Query().Get("state") != "state-value" || parsed.Query().Get("nonce") != "nonce-value" {
		t.Fatalf("authorization URL = %q", target)
	}

	o.config.Endpoint.AuthURL = "javascript:alert(1)"
	if _, err := o.authorizationURL("state", "nonce", "verifier"); err == nil {
		t.Fatal("unsafe authorization redirect was accepted")
	}
}

func TestOIDCStateIsBoundToTheInitiatingBrowser(t *testing.T) {
	t.Setenv("COOKIE_SECURE", "true")
	firstRequest := httptest.NewRequest(http.MethodGet, "/auth/shauth", nil)
	firstResponse := httptest.NewRecorder()
	state, err := newOIDCState(firstResponse, firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	cookies := firstResponse.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("state cookie count = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != oidcSecureFlowCookie || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("state cookie is not host-only hardened: %#v", cookie)
	}
	callback := httptest.NewRequest(http.MethodGet, "/auth/shauth/callback?state="+url.QueryEscape(state), nil)
	callback.AddCookie(cookie)
	if !validOIDCStateBinding(callback, state) {
		t.Fatal("state did not validate in the initiating browser")
	}
	otherBrowser := httptest.NewRequest(http.MethodGet, "/auth/shauth/callback?state="+url.QueryEscape(state), nil)
	otherResponse := httptest.NewRecorder()
	if _, err := newOIDCState(otherResponse, otherBrowser); err != nil {
		t.Fatal(err)
	}
	otherBrowser.AddCookie(otherResponse.Result().Cookies()[0])
	if validOIDCStateBinding(otherBrowser, state) {
		t.Fatal("state from one browser validated with another browser's binding")
	}
	replacement := byte('x')
	if state[0] == replacement {
		replacement = 'y'
	}
	tampered := string(replacement) + state[1:]
	if validOIDCStateBinding(callback, tampered) {
		t.Fatal("tampered state validated")
	}
}

func TestOIDCClaimValidationHelpers(t *testing.T) {
	for _, test := range []struct {
		audience []string
		azp      string
		want     bool
	}{
		{[]string{"client"}, "", true},
		{[]string{"client", "api"}, "client", true},
		{[]string{"client", "api"}, "", false},
		{[]string{"client"}, "other", false},
	} {
		if got := validOIDCAuthorizedParty(test.audience, test.azp, "client"); got != test.want {
			t.Errorf("validOIDCAuthorizedParty(%v, %q) = %t, want %t", test.audience, test.azp, got, test.want)
		}
	}
	if email, err := normalizeOIDCEmail(" User@Example.COM "); err != nil || email != "user@example.com" {
		t.Fatalf("normalizeOIDCEmail() = %q, %v", email, err)
	}
	for _, invalid := range []string{"", "Display Name <user@example.com>", "not an email"} {
		if _, err := normalizeOIDCEmail(invalid); err == nil {
			t.Errorf("normalizeOIDCEmail(%q) succeeded", invalid)
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

// TestValidationRendersTheFullIdentityContract covers the page Shauth (and
// equivalent SSO validators) load after login to confirm the identity a
// session actually carries. Shauth's validator asserts on all four of
// data-testid="validation-username", "validation-email", "validation-role",
// and "validation-release" (see e6qu/shauth validator/validate.mjs
// assertValidationIdentity) -- a page exposing only two of the four still
// fails every check, exactly as this page did before validation-role and
// validation-release existed at all. Username must hold the account's login
// handle, not an arbitrary display name -- a real person's display name and
// an OIDC provider's preferred_username routinely differ, and a validator
// comparing this field against the identity it just authenticated needs the
// handle. Role must hold the identity provider's role claim, not ZZIRA's own
// workspace membership role -- they are different axes. Release must hold
// this build's own version, matching what the identity provider's catalog
// has registered for this deployment. The page's CSP (default-src 'none';
// style-src 'unsafe-inline') must also never depend on the shared app-shell
// template's external stylesheets, WASM, or scripts, since the browser blocks
// all of them under that policy.
func TestValidationRendersTheFullIdentityContract(t *testing.T) {
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
	if err := st.SetOIDCRole(ctx, userID, "developer"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	}()

	token, err := authn.LoginOIDC(ctx, st, userID, "id-token", "https://issuer.example.invalid", userID+"-subject", "")
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
	if !strings.Contains(body, `data-testid="validation-email">`+email+`<`) {
		t.Fatalf("validation-email did not render the account email: %s", body)
	}
	if !strings.Contains(body, `data-testid="validation-role">developer<`) {
		t.Fatalf("validation-role did not render the identity provider's role claim: %s", body)
	}
	if !strings.Contains(body, `data-testid="validation-release">`+build.Version+`<`) {
		t.Fatalf("validation-release did not render this build's version (%s): %s", build.Version, body)
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

// TestSignedOutOffersTheExactShauthSignInControlWhenOIDCIsConfigured covers
// the other half of failing closed: reaching the signed-out page must leave a
// real, visible way back in, not just an absent session. When Shauth SSO is
// configured, LoginForm's own "/login" never renders anything -- it always
// redirects straight through to "/auth/shauth" -- so the signed-out page's
// own control is the only place that link can appear, and it must say
// exactly "Sign in with Shauth": Shauth's SSO validator (and an anonymous
// human) has nothing else to click otherwise.
func TestSignedOutOffersTheExactShauthSignInControlWhenOIDCIsConfigured(t *testing.T) {
	h := &Handler{OIDC: &OIDC{}}
	req := httptest.NewRequest("GET", "/signed-out", nil)
	rec := httptest.NewRecorder()
	h.SignedOut(rec, req)
	if rec.Code != 200 {
		t.Fatalf("signed-out status = %d, want 200", rec.Code)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("signed-out Cache-Control = %q, want no-store", cache)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `href="/auth/shauth">Sign in with Shauth<`) {
		t.Fatalf("signed-out page did not offer a \"Sign in with Shauth\" link to /auth/shauth: %s", body)
	}
}

// Without SSO configured, the signed-out page must still offer ZZIRA's own
// password login rather than linking to an OIDC entry point that does not
// exist for this deployment.
func TestSignedOutOffersLocalLoginWithoutOIDCConfigured(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest("GET", "/signed-out", nil)
	rec := httptest.NewRecorder()
	h.SignedOut(rec, req)
	if rec.Code != 200 {
		t.Fatalf("signed-out status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `href="/login">Log in<`) {
		t.Fatalf("signed-out page did not offer a local \"Log in\" link: %s", body)
	}
	if strings.Contains(body, "Sign in with Shauth") {
		t.Fatalf("signed-out page offered Shauth sign-in without OIDC configured: %s", body)
	}
}

func TestBackChannelLogoutVerifiesAndRevokesTheIssuerScopedSession(t *testing.T) {
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

	const issuer = "https://issuer.example.invalid"
	const clientID = "zzira-test-client"
	userID := store.NewID("usr")
	if _, err := st.CreateUser(ctx, userID, userID+"@example.invalid", "test", "Logout test"); err != nil {
		t.Fatal(err)
	}
	subject := userID + "-subject"
	sid := userID + "-sid"
	tokenHash := store.HashToken("backchannel-session-" + userID)
	if err := st.CreateOIDCSession(ctx, tokenHash, userID, "id-token", issuer, subject, sid, time.Hour); err != nil {
		t.Fatal(err)
	}
	jti := store.NewID("jti")
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM oidc_logout_tokens WHERE jti=$1`, jti)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	}()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: privateKey}, (&jose.SignerOptions{}).WithType("logout+jwt"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	payload, err := json.Marshal(map[string]any{
		"iss": issuer, "aud": clientID, "sub": subject, "sid": sid,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(), "jti": jti,
		"events": map[string]any{backChannelLogoutEvent: map[string]any{"provider_extension": true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := signed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	verifier := oidc.NewVerifier(issuer, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{privateKey.Public()}}, &oidc.Config{ClientID: clientID})
	h := &Handler{Store: st, OIDC: &OIDC{verifier: verifier}}
	form := url.Values{"logout_token": {compact}}
	req := httptest.NewRequest(http.MethodPost, "/auth/shauth/backchannel-logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.BackChannelLogout(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("back-channel logout status=%d, want 204: %s", rec.Code, rec.Body.String())
	}
	if _, err := st.SessionUser(ctx, tokenHash); err == nil {
		t.Fatal("verified back-channel logout did not revoke its session")
	}
}

// TestFormActionOriginReturnsTheEndSessionEndpointOrigin covers the value
// SecurityHeaders' form-action directive must allow so a browser can follow
// RP-initiated OIDC logout's redirect to the identity provider without it
// being silently blocked.
func TestFormActionOriginReturnsTheEndSessionEndpointOrigin(t *testing.T) {
	var nilOIDC *OIDC
	if got := nilOIDC.FormActionOrigin(); got != "" {
		t.Fatalf("nil OIDC FormActionOrigin() = %q, want empty", got)
	}

	unconfigured := &OIDC{}
	if got := unconfigured.FormActionOrigin(); got != "" {
		t.Fatalf("OIDC with no end_session_endpoint FormActionOrigin() = %q, want empty", got)
	}

	configured := &OIDC{endSessionEndpoint: "https://auth.example.test/oauth2/sessions/logout?foo=bar"}
	if got := configured.FormActionOrigin(); got != "https://auth.example.test" {
		t.Fatalf("FormActionOrigin() = %q, want %q", got, "https://auth.example.test")
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
