package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func TestAtlassianAuthorizationUsesDocumentedThreeLeggedOAuthParameters(t *testing.T) {
	provider := &OIDC{
		atlassian: true,
		config: oauth2.Config{
			ClientID: "client", RedirectURL: "https://zzira.example/auth/atlassian/callback",
			Endpoint: oauth2.Endpoint{AuthURL: "https://auth.atlassian.com/authorize"}, Scopes: []string{"read:me"},
		},
	}
	target, err := provider.authorizationURL("state", "nonce", "verifier")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"audience": "api.atlassian.com", "prompt": "consent", "scope": "read:me",
		"response_type": "code", "state": "state", "redirect_uri": "https://zzira.example/auth/atlassian/callback",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if query.Has("nonce") || query.Has("code_challenge") {
		t.Fatalf("Atlassian authorization included undocumented OIDC/PKCE parameters: %s", query.Encode())
	}
}

func TestAtlassianAuthenticationExchangesJSONAndLoadsActiveProfile(t *testing.T) {
	var tokenRequest map[string]string
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("token request = %s %s", r.Method, r.Header.Get("Content-Type"))
			}
			if err := json.NewDecoder(r.Body).Decode(&tokenRequest); err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "access", "token_type": "Bearer"})
		case "/me":
			if r.Header.Get("Authorization") != "Bearer access" {
				t.Errorf("profile authorization = %q", r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(map[string]string{
				"account_type": "atlassian", "account_id": "account-1", "account_status": "active",
				"email": "User@Example.com", "name": "Example User", "nickname": "example",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer providerServer.Close()
	provider := &OIDC{
		atlassian: true, issuer: atlassianIssuer, profileEndpoint: providerServer.URL + "/me",
		config: oauth2.Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://zzira.example/auth/atlassian/callback", Endpoint: oauth2.Endpoint{TokenURL: providerServer.URL + "/oauth/token"}},
	}
	identity, err := provider.authenticateAtlassian(t.Context(), "authorization-code")
	if err != nil {
		t.Fatal(err)
	}
	if tokenRequest["code"] != "authorization-code" || tokenRequest["client_secret"] != "secret" || tokenRequest["grant_type"] != "authorization_code" {
		t.Fatalf("token request = %#v", tokenRequest)
	}
	if identity.Issuer != atlassianIssuer || identity.Subject != "account-1" || identity.Email != "user@example.com" || identity.DisplayName != "Example User" {
		t.Fatalf("identity = %#v", identity)
	}
}

func TestLoginPageOffersEveryConfiguredProviderAndPassword(t *testing.T) {
	registry := &ProviderRegistry{ordered: []*OIDC{
		{key: "google", displayName: "Google", issuer: googleIssuer},
		{key: "microsoft", displayName: "Microsoft", issuer: "https://login.microsoftonline.com/tenant/v2.0"},
		{key: "atlassian", displayName: "Atlassian", issuer: atlassianIssuer, atlassian: true},
	}}
	h := &Handler{IdentityProviders: registry}
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	h.LoginForm(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, control := range []string{
		`href="/auth/google">Continue with Google<`,
		`href="/auth/microsoft">Continue with Microsoft<`,
		`href="/auth/atlassian">Continue with Atlassian<`,
		`action="/login"`,
	} {
		if !strings.Contains(body, control) {
			t.Errorf("login page omitted %q: %s", control, body)
		}
	}
}

func TestMicrosoftProviderRequiresTenantSpecificAuthority(t *testing.T) {
	t.Setenv("ZZIRA_MICROSOFT_CLIENT_ID", "client")
	t.Setenv("ZZIRA_MICROSOFT_CLIENT_SECRET", "secret")
	t.Setenv("ZZIRA_MICROSOFT_TENANT_ID", "common")
	if _, err := configuredMicrosoftProvider(t.Context(), "https://zzira.example"); err == nil || !strings.Contains(err.Error(), "tenant UUID") {
		t.Fatalf("common authority error = %v", err)
	}
}
