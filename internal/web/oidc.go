package web

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/build"
	"github.com/e6qu/zzira/internal/store"
)

const (
	oidcStateTTL         = 10 * time.Minute
	oidcFlowCookie       = "zzira_oidc_flow"
	oidcSecureFlowCookie = "__Host-zzira_oidc_flow"
	oidcStateSeparator   = "."
)

// OIDC is optional browser SSO. The SHAUTH prefix is retained for consistency
// with the reference deployment, but any standards-compliant OIDC provider is
// accepted through discovery.
type OIDC struct {
	config                oauth2.Config
	verifier              *oidc.IDTokenVerifier
	endSessionEndpoint    string
	postLogoutRedirectURL string
}

func NewOIDC(ctx context.Context) (*OIDC, error) {
	issuer := os.Getenv("ZZIRA_SHAUTH_ISSUER")
	clientID := os.Getenv("ZZIRA_SHAUTH_CLIENT_ID")
	clientSecret := os.Getenv("ZZIRA_SHAUTH_CLIENT_SECRET")
	externalURL := strings.TrimRight(os.Getenv("ZZIRA_EXTERNAL_URL"), "/")
	values := []string{issuer, clientID, clientSecret, externalURL}
	present := 0
	for _, value := range values {
		if value != "" {
			present++
		}
	}
	if present == 0 {
		return nil, nil
	}
	if present != len(values) {
		return nil, fmt.Errorf("ZZIRA_SHAUTH_ISSUER, ZZIRA_SHAUTH_CLIENT_ID, ZZIRA_SHAUTH_CLIENT_SECRET, and ZZIRA_EXTERNAL_URL must be set together")
	}
	if err := validOIDCURL(issuer); err != nil {
		return nil, fmt.Errorf("ZZIRA_SHAUTH_ISSUER: %w", err)
	}
	if err := validOIDCURL(externalURL); err != nil {
		return nil, fmt.Errorf("ZZIRA_EXTERNAL_URL: %w", err)
	}
	if parsed, _ := url.Parse(externalURL); parsed.Path != "" && parsed.Path != "/" {
		return nil, fmt.Errorf("ZZIRA_EXTERNAL_URL: must be an origin without a path")
	}
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery: %w", err)
	}
	var metadata struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
		JWKSURI            string `json:"jwks_uri"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, fmt.Errorf("OIDC discovery claims: %w", err)
	}
	if metadata.EndSessionEndpoint != "" {
		if err := validOIDCEndpointURL(metadata.EndSessionEndpoint); err != nil {
			return nil, fmt.Errorf("OIDC end_session_endpoint: %w", err)
		}
	}
	endpoint := provider.Endpoint()
	for name, raw := range map[string]string{
		"authorization_endpoint": endpoint.AuthURL,
		"token_endpoint":         endpoint.TokenURL,
		"jwks_uri":               metadata.JWKSURI,
	} {
		if err := validOIDCEndpointURL(raw); err != nil {
			return nil, fmt.Errorf("OIDC %s: %w", name, err)
		}
	}
	return &OIDC{
		config: oauth2.Config{
			ClientID: clientID, ClientSecret: clientSecret, Endpoint: endpoint,
			RedirectURL: externalURL + "/auth/shauth/callback", Scopes: []string{oidc.ScopeOpenID, "profile", "email"},
		},
		verifier:              provider.Verifier(&oidc.Config{ClientID: clientID}),
		endSessionEndpoint:    metadata.EndSessionEndpoint,
		postLogoutRedirectURL: externalURL + "/auth/shauth/logout/complete",
	}, nil
}

// FormActionOrigin returns the identity provider's end_session_endpoint
// origin, for SecurityHeaders' form-action directive: RP-initiated OIDC
// logout posts the sign-out form to this same origin's own /logout, which
// then redirects the browser to this endpoint to end the SSO session before
// returning. Chrome enforces form-action against that whole redirect chain,
// not just the form's literal same-origin action target, so without this the
// redirect is silently blocked. "" (including a nil receiver, or a provider
// that advertises no end_session_endpoint) means no logout redirect ever
// leaves this origin and nothing needs to be allowed.
func (o *OIDC) FormActionOrigin() string {
	if o == nil || o.endSessionEndpoint == "" {
		return ""
	}
	parsed, err := url.Parse(o.endSessionEndpoint)
	if err != nil {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func validOIDCURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("must be an absolute URL")
	}
	return validOIDCTransport(u)
}

// Discovery permits endpoint URLs to carry a query component. They retain
// the same credential, fragment, and transport restrictions as the issuer.
func validOIDCEndpointURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("must be an absolute URL")
	}
	return validOIDCTransport(u)
}

func validOIDCTransport(u *url.URL) error {
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme != "http" || os.Getenv("ZZIRA_ALLOW_INSECURE_OIDC") != "true" {
		return fmt.Errorf("must use HTTPS (set ZZIRA_ALLOW_INSECURE_OIDC=true only for local development)")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("HTTP is allowed only for a loopback provider in local development")
		}
	}
	return nil
}

// authorizationURL constructs the intentional cross-origin handoff to the
// identity provider and validates the final URL, including OAuth parameters,
// immediately before it reaches the redirect sink. Discovery endpoints are
// remote configuration and must not be trusted solely because discovery used
// TLS successfully.
func (o *OIDC) authorizationURL(state, nonce, verifier string) (string, error) {
	target := o.config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	if err := validOIDCEndpointURL(target); err != nil {
		return "", fmt.Errorf("authorization redirect: %w", err)
	}
	return target, nil
}

func oidcRandom() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func oidcFlowCookieName() string {
	if authn.SecureCookies() {
		return oidcSecureFlowCookie
	}
	return oidcFlowCookie
}

// newOIDCState binds a fresh, single-use transaction to a short-lived secret
// held only by the initiating browser. The database still owns replay
// prevention; the HMAC prevents a callback started in another browser from
// logging its identity into this one (login CSRF/session swapping).
func newOIDCState(w http.ResponseWriter, r *http.Request) (string, error) {
	binding := ""
	if cookie, err := r.Cookie(oidcFlowCookieName()); err == nil {
		if decoded, decodeErr := base64.RawURLEncoding.DecodeString(cookie.Value); decodeErr == nil && len(decoded) == 32 {
			binding = cookie.Value
		}
	}
	if binding == "" {
		var err error
		binding, err = oidcRandom()
		if err != nil {
			return "", err
		}
	}
	http.SetCookie(w, &http.Cookie{ // #nosec G124 -- SecureCookies defaults to true for an HTTPS external URL and is covered by regression tests.
		Name: oidcFlowCookieName(), Value: binding, Path: "/", HttpOnly: true,
		Secure: authn.SecureCookies(), SameSite: http.SameSiteLaxMode,
		MaxAge: int(oidcStateTTL.Seconds()), Expires: time.Now().Add(oidcStateTTL),
	})
	transaction, err := oidcRandom()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(binding))
	_, _ = mac.Write([]byte(transaction))
	return transaction + oidcStateSeparator + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func validOIDCStateBinding(r *http.Request, state string) bool {
	transaction, encodedMAC, ok := strings.Cut(state, oidcStateSeparator)
	if !ok || transaction == "" || encodedMAC == "" || strings.Contains(encodedMAC, oidcStateSeparator) {
		return false
	}
	cookie, err := r.Cookie(oidcFlowCookieName())
	if err != nil {
		return false
	}
	provided, err := base64.RawURLEncoding.DecodeString(encodedMAC)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(cookie.Value))
	_, _ = mac.Write([]byte(transaction))
	return hmac.Equal(provided, mac.Sum(nil))
}

func (h *Handler) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	if h.OIDC == nil {
		http.NotFound(w, r)
		return
	}
	state, err := newOIDCState(w, r)
	if err != nil {
		http.Error(w, "could not begin sign-in", http.StatusInternalServerError)
		return
	}
	nonce, err := oidcRandom()
	if err != nil {
		http.Error(w, "could not begin sign-in", http.StatusInternalServerError)
		return
	}
	verifier, err := oidcRandom()
	if err != nil {
		http.Error(w, "could not begin sign-in", http.StatusInternalServerError)
		return
	}
	if err := h.Store.CreateOIDCLoginState(r.Context(), state, nonce, verifier, oidcStateTTL); err != nil {
		http.Error(w, "could not begin sign-in", http.StatusInternalServerError)
		return
	}
	authorizationURL, err := h.OIDC.authorizationURL(state, nonce, verifier)
	if err != nil {
		http.Error(w, "could not begin sign-in", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, authorizationURL, http.StatusFound) // #nosec G710 -- final URL is HTTPS/loopback validated above; cross-origin IdP redirect is the intended OIDC flow.
}

func (h *Handler) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	noStoreAuthResponse(w)
	if h.OIDC == nil {
		http.NotFound(w, r)
		return
	}
	state := r.URL.Query().Get("state")
	if state == "" || !validOIDCStateBinding(r, state) {
		http.Error(w, "invalid sign-in callback", http.StatusBadRequest)
		return
	}
	nonce, verifier, err := h.Store.ConsumeOIDCLoginState(r.Context(), state)
	if err != nil {
		http.Error(w, "invalid or expired sign-in state", http.StatusBadRequest)
		return
	}
	if r.URL.Query().Get("error") != "" {
		http.Error(w, "sign-in was declined or could not be completed", http.StatusUnauthorized)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "invalid sign-in callback", http.StatusBadRequest)
		return
	}
	token, err := h.OIDC.config.Exchange(r.Context(), code, oauth2.VerifierOption(verifier))
	if err != nil {
		http.Error(w, "sign-in exchange failed", http.StatusUnauthorized)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		http.Error(w, "provider response omitted id_token", http.StatusUnauthorized)
		return
	}
	idToken, err := h.OIDC.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		http.Error(w, "invalid identity token", http.StatusUnauthorized)
		return
	}
	var claims struct {
		Email             string `json:"email"`
		EmailVerified     bool   `json:"email_verified"`
		Name              string `json:"name"`
		Nonce             string `json:"nonce"`
		PreferredUsername string `json:"preferred_username"`
		SID               string `json:"sid"`
		AuthorizedParty   string `json:"azp"`
		Role              string `json:"role"`
	}
	if err := idToken.Claims(&claims); err != nil || idToken.Subject == "" || !claims.EmailVerified ||
		!validOIDCAuthorizedParty(idToken.Audience, claims.AuthorizedParty, h.OIDC.config.ClientID) ||
		subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		http.Error(w, "identity token claims are not acceptable", http.StatusUnauthorized)
		return
	}
	email, err := normalizeOIDCEmail(claims.Email)
	if err != nil {
		http.Error(w, "identity token claims are not acceptable", http.StatusUnauthorized)
		return
	}
	displayName := strings.TrimSpace(claims.Name)
	if displayName == "" {
		displayName = strings.TrimSpace(claims.PreferredUsername)
	}
	if displayName == "" {
		displayName, _, _ = strings.Cut(email, "@")
	}
	userID, err := h.Store.ResolveOIDCUser(r.Context(), idToken.Issuer, idToken.Subject, email, displayName, authn.UnusablePasswordHash)
	if err != nil {
		if errors.Is(err, store.ErrInactiveUser) {
			http.Error(w, "this account is inactive", http.StatusForbidden)
			return
		}
		http.Error(w, "could not create sign-in session", http.StatusInternalServerError)
		return
	}
	if username := strings.TrimSpace(claims.PreferredUsername); username != "" {
		if err := h.Store.SetOIDCUsername(r.Context(), userID, username); err != nil {
			http.Error(w, "could not create sign-in session", http.StatusInternalServerError)
			return
		}
	}
	if role := strings.TrimSpace(claims.Role); role != "" {
		if err := h.Store.SetOIDCRole(r.Context(), userID, role); err != nil {
			http.Error(w, "could not create sign-in session", http.StatusInternalServerError)
			return
		}
	}
	session, err := authn.LoginOIDC(r.Context(), h.Store, userID, rawIDToken, idToken.Issuer, idToken.Subject, claims.SID)
	if err != nil {
		http.Error(w, "could not create sign-in session", http.StatusInternalServerError)
		return
	}
	authn.SetSessionCookie(w, session)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func noStoreAuthResponse(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func normalizeOIDCEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Name != "" || address.Address != email {
		return "", fmt.Errorf("email claim is not a plain address")
	}
	return email, nil
}

func validOIDCAuthorizedParty(audience []string, authorizedParty, clientID string) bool {
	if authorizedParty != "" {
		return authorizedParty == clientID
	}
	return len(audience) == 1
}

// Validation is the shared relying-party contract endpoint the Shauth SSO
// validator (and equivalent checks) loads after login to confirm the
// identity a browser session actually carries. It fails closed to the
// signed-out page rather than back into the sign-in flow: redirecting to
// /auth/shauth on an absent session would silently re-enter the flow and
// read as "remained authenticated" to a caller checking for that redirect.
// validationPageData is deliberately its own type, not models.User: the
// identity provider's role claim and this build's release revision are
// validation-contract concerns, not Jira-API-shaped user data the shared
// renderer/wasm-client model carries elsewhere.
type validationPageData struct {
	Username string
	Email    string
	Role     string
	Release  string
}

func (h *Handler) Validation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/signed-out", http.StatusFound)
		return
	}
	role, err := h.Store.OIDCRole(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writePage(w, "page_validation", validationPageData{
		Username: user.Username,
		Email:    user.Email,
		Role:     role,
		Release:  build.Version,
	})
}

const (
	backChannelLogoutEvent    = "http://schemas.openid.net/event/backchannel-logout"
	maxBackChannelLogoutBytes = 64 << 10 // 64 KiB: a single logout JWT, kilobytes at most.
)

// monitoringSchemaVersion is e6qu.monitoring/v2: the application-observation
// variant of the shared monitoring contract, whose cost estimate is optional
// because zzira is not itself a priced cloud resource.
const monitoringSchemaVersion = "e6qu.monitoring/v2"

// Monitoring publishes a real, live observation of zzira's own health for the
// deployment's centralized monitoring to collect -- bearer-authenticated
// against ZZIRA_MONITORING_TOKEN, never a cached or fabricated figure.
func (h *Handler) Monitoring(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(os.Getenv("ZZIRA_MONITORING_TOKEN"))
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if token == "" || !strings.HasPrefix(auth, prefix) ||
		subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, prefix)), []byte(token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	dbHealthy, issueCount, err := h.Store.MonitoringSnapshot(r.Context())
	if err != nil {
		http.Error(w, "could not collect observation", http.StatusInternalServerError)
		return
	}
	health := "healthy"
	if !dbHealthy {
		health = "unhealthy"
	}
	issueMetric := map[string]any{
		"name": "issues_total", "label": "Issues", "unit": "count", "status": "available", "value": issueCount,
	}
	if !dbHealthy {
		issueMetric = map[string]any{"name": "issues_total", "label": "Issues", "unit": "count", "status": "unavailable"}
	}
	body := map[string]any{
		"schema_version": monitoringSchemaVersion,
		"observed_at":    time.Now().UTC().Format(time.RFC3339),
		"resources": []map[string]any{
			{
				"id": "zzira-database", "name": "ZZIRA PostgreSQL", "kind": "database", "health": health,
				"metrics": []map[string]any{issueMetric},
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("encode monitoring observation: %v", err)
	}
}

type oidcLogoutClaims struct {
	Events map[string]json.RawMessage `json:"events"`
}

// BackChannelLogout implements OpenID Connect Back-Channel Logout 1.0: Shauth
// posts a logout_token here when a session it owns ends. The dedicated logout
// verifier checks the signature, issuer, audience, expiry, event, prohibited
// nonce, jti, and required sid/sub shape before issuer-scoped revocation.
func (h *Handler) BackChannelLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.OIDC == nil {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBackChannelLogoutBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid logout request", http.StatusBadRequest)
		return
	}
	rawLogoutToken := r.PostForm.Get("logout_token")
	if rawLogoutToken == "" {
		http.Error(w, "logout_token is required", http.StatusBadRequest)
		return
	}
	logoutToken, err := h.OIDC.verifier.VerifyLogout(r.Context(), rawLogoutToken)
	if err != nil {
		http.Error(w, "logout token verification failed", http.StatusBadRequest)
		return
	}
	var claims oidcLogoutClaims
	if err := logoutToken.Claims(&claims); err != nil || logoutToken.IssuedAt.IsZero() {
		http.Error(w, "logout token claims are invalid", http.StatusBadRequest)
		return
	}
	rawEvent, ok := claims.Events[backChannelLogoutEvent]
	if !ok {
		http.Error(w, "logout token event is invalid", http.StatusBadRequest)
		return
	}
	var eventClaims map[string]json.RawMessage
	if err := json.Unmarshal(rawEvent, &eventClaims); err != nil || eventClaims == nil {
		http.Error(w, "logout token event is invalid", http.StatusBadRequest)
		return
	}
	now := time.Now()
	issuedAt := logoutToken.IssuedAt
	if issuedAt.Before(now.Add(-5*time.Minute)) || issuedAt.After(now.Add(time.Minute)) {
		http.Error(w, "logout token is stale", http.StatusBadRequest)
		return
	}
	claimed, err := h.Store.ClaimOIDCLogoutAndDeleteSessions(r.Context(), logoutToken.TokenID, logoutToken.Expiry, logoutToken.Issuer, logoutToken.Subject, logoutToken.SessionID)
	if err != nil {
		http.Error(w, "browser sessions could not be revoked", http.StatusInternalServerError)
		return
	}
	if !claimed {
		http.Error(w, "logout token was already used", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
