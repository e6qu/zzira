package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/e6qu/zzira/internal/authn"
)

const oidcStateTTL = 10 * time.Minute

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
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery: %w", err)
	}
	var metadata struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	if err := provider.Claims(&metadata); err != nil {
		return nil, fmt.Errorf("OIDC discovery claims: %w", err)
	}
	if metadata.EndSessionEndpoint != "" {
		if err := validOIDCURL(metadata.EndSessionEndpoint); err != nil {
			return nil, fmt.Errorf("OIDC end_session_endpoint: %w", err)
		}
	}
	return &OIDC{
		config: oauth2.Config{
			ClientID: clientID, ClientSecret: clientSecret, Endpoint: provider.Endpoint(),
			RedirectURL: externalURL + "/auth/shauth/callback", Scopes: []string{oidc.ScopeOpenID, "profile", "email"},
		},
		verifier:              provider.Verifier(&oidc.Config{ClientID: clientID}),
		endSessionEndpoint:    metadata.EndSessionEndpoint,
		postLogoutRedirectURL: externalURL + "/auth/shauth/logout/complete",
	}, nil
}

func validOIDCURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("must be an absolute URL")
	}
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

func oidcRandom() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (h *Handler) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	if h.OIDC == nil {
		http.NotFound(w, r)
		return
	}
	state, err := oidcRandom()
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
	http.Redirect(w, r, h.OIDC.config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}

func (h *Handler) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	if h.OIDC == nil {
		http.NotFound(w, r)
		return
	}
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		http.Error(w, "invalid sign-in callback", http.StatusBadRequest)
		return
	}
	nonce, verifier, err := h.Store.ConsumeOIDCLoginState(r.Context(), state)
	if err != nil {
		http.Error(w, "invalid or expired sign-in state", http.StatusBadRequest)
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
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Nonce         string `json:"nonce"`
	}
	if err := idToken.Claims(&claims); err != nil || claims.Email == "" || !claims.EmailVerified || claims.Nonce != nonce {
		http.Error(w, "identity token claims are not acceptable", http.StatusUnauthorized)
		return
	}
	userID, err := h.Store.ResolveOIDCUser(r.Context(), idToken.Issuer, idToken.Subject, claims.Email)
	if err != nil {
		http.Error(w, "this identity is not a ZZIRA member", http.StatusForbidden)
		return
	}
	session, err := authn.LoginOIDC(r.Context(), h.Store, userID, rawIDToken)
	if err != nil {
		http.Error(w, "could not create sign-in session", http.StatusInternalServerError)
		return
	}
	authn.SetSessionCookie(w, session)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Validation is the shared relying-party contract endpoint the Shauth SSO
// validator (and equivalent checks) loads after login to confirm the
// identity a browser session actually carries. It fails closed to the
// signed-out page rather than back into the sign-in flow: redirecting to
// /auth/shauth on an absent session would silently re-enter the flow and
// read as "remained authenticated" to a caller checking for that redirect.
func (h *Handler) Validation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user := h.currentUser(r)
	if user == nil {
		http.Redirect(w, r, "/signed-out", http.StatusFound)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writePage(w, "page_validation", user)
}

const (
	backChannelLogoutEvent    = "http://schemas.openid.net/event/backchannel-logout"
	maxBackChannelLogoutBytes = 64 << 10 // 64 KiB: a single logout JWT, kilobytes at most.
)

type oidcLogoutClaims struct {
	Subject string                     `json:"sub"`
	SID     string                     `json:"sid"`
	Nonce   json.RawMessage            `json:"nonce"`
	JTI     string                     `json:"jti"`
	Issued  int64                      `json:"iat"`
	Expires int64                      `json:"exp"`
	Events  map[string]json.RawMessage `json:"events"`
}

// BackChannelLogout implements OpenID Connect Back-Channel Logout 1.0: Shauth
// posts a logout_token here when a session it owns ends, and this revokes
// every zzira session bound to that token's subject. The verifier is the same
// one OIDCCallback uses (same issuer, same client ID, so iss/aud already
// match); a logout token additionally carries no nonce and must name a sid or
// a sub, checked explicitly below since the ID-token verifier does not.
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
	logoutToken, err := h.OIDC.verifier.Verify(r.Context(), rawLogoutToken)
	if err != nil {
		http.Error(w, "logout token verification failed", http.StatusBadRequest)
		return
	}
	var claims oidcLogoutClaims
	if err := logoutToken.Claims(&claims); err != nil || claims.JTI == "" || claims.Issued == 0 || claims.Expires == 0 || len(claims.Nonce) != 0 || (claims.SID == "" && claims.Subject == "") {
		http.Error(w, "logout token claims are invalid", http.StatusBadRequest)
		return
	}
	rawEvent, ok := claims.Events[backChannelLogoutEvent]
	if !ok {
		http.Error(w, "logout token event is invalid", http.StatusBadRequest)
		return
	}
	var eventClaims map[string]json.RawMessage
	if err := json.Unmarshal(rawEvent, &eventClaims); err != nil || eventClaims == nil || len(eventClaims) != 0 {
		http.Error(w, "logout token event is invalid", http.StatusBadRequest)
		return
	}
	now := time.Now()
	issuedAt := time.Unix(claims.Issued, 0)
	if issuedAt.Before(now.Add(-5*time.Minute)) || issuedAt.After(now.Add(time.Minute)) {
		http.Error(w, "logout token is stale", http.StatusBadRequest)
		return
	}
	claimed, err := h.Store.ClaimOIDCLogoutAndDeleteSessions(r.Context(), claims.JTI, time.Unix(claims.Expires, 0), logoutToken.Issuer, claims.Subject)
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
