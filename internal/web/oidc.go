package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
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
	config   oauth2.Config
	verifier *oidc.IDTokenVerifier
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
	return &OIDC{
		config: oauth2.Config{
			ClientID: clientID, ClientSecret: clientSecret, Endpoint: provider.Endpoint(),
			RedirectURL: externalURL + "/auth/shauth/callback", Scopes: []string{oidc.ScopeOpenID, "profile", "email"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
	}, nil
}

func validOIDCURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("must be an absolute URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && os.Getenv("ZZIRA_ALLOW_INSECURE_OIDC") == "true") {
		return fmt.Errorf("must use HTTPS (set ZZIRA_ALLOW_INSECURE_OIDC=true only for local development)")
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
