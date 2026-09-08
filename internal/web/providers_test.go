package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/secretbox"
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
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		response := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r}
		switch r.URL.Path {
		case "/oauth/token":
			if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("token request = %s %s", r.Method, r.Header.Get("Content-Type"))
			}
			if err := json.NewDecoder(r.Body).Decode(&tokenRequest); err != nil {
				t.Error(err)
			}
			response.Body = io.NopCloser(strings.NewReader(`{"access_token":"access","token_type":"Bearer"}`))
		case "/me":
			if r.Header.Get("Authorization") != "Bearer access" {
				t.Errorf("profile authorization = %q", r.Header.Get("Authorization"))
			}
			response.Body = io.NopCloser(strings.NewReader(`{"account_type":"atlassian","account_id":"account-1","account_status":"active","email":"User@Example.com","name":"Example User","nickname":"example"}`))
		default:
			response.StatusCode = http.StatusNotFound
			response.Body = io.NopCloser(strings.NewReader("not found"))
		}
		return response, nil
	})}
	provider := &OIDC{
		atlassian: true, issuer: atlassianIssuer, httpClient: client,
		config: oauth2.Config{ClientID: "client", ClientSecret: "secret", RedirectURL: "https://zzira.example/auth/atlassian/callback"},
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

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

func TestProviderRegistryAppliesDurableAvailabilityWithoutLosingConfiguration(t *testing.T) {
	provider := &OIDC{key: "atlassian", displayName: "Atlassian", issuer: atlassianIssuer, atlassian: true}
	registry := &ProviderRegistry{ordered: []*OIDC{provider}, byKey: map[string]*OIDC{"atlassian": provider}, byIssuer: map[string]*OIDC{atlassianIssuer: provider}}
	registry.ApplyEnabled(map[string]bool{"atlassian": false})
	if registry.Provider("atlassian") != nil || len(registry.LoginProviders()) != 0 {
		t.Fatal("disabled provider remained available for login")
	}
	adminProviders := registry.AdminProviders()
	if len(adminProviders) != 1 || adminProviders[0].Enabled {
		t.Fatalf("admin providers = %#v", adminProviders)
	}
	if registry.ProviderByIssuer(atlassianIssuer) != provider {
		t.Fatal("disabled provider configuration was lost for logout/re-enable")
	}
	if !registry.SetEnabled("atlassian", true) || registry.Provider("atlassian") != provider {
		t.Fatal("provider did not become available after re-enable")
	}
	if registry.SetEnabled("missing", false) {
		t.Fatal("unknown provider accepted an availability change")
	}
}

func TestProviderRegistryPersistsRotatesAndDeletesStoredProvider(t *testing.T) {
	environment := &OIDC{key: "google", displayName: "Google", issuer: googleIssuer, source: "environment"}
	registry := &ProviderRegistry{
		ordered: []*OIDC{environment}, byKey: map[string]*OIDC{"google": environment},
		byIssuer: map[string]*OIDC{googleIssuer: environment}, disabled: map[string]bool{},
	}
	stored := &OIDC{key: "company", displayName: "Company SSO", issuer: "https://id.example.test", source: "database"}
	persisted := 0
	if err := registry.PersistStored(stored, func() error { persisted++; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := registry.Provider("company"); got != stored || persisted != 1 {
		t.Fatalf("stored provider = %p, persisted %d", got, persisted)
	}
	rotated := &OIDC{key: "company", displayName: "Company SSO", issuer: stored.issuer, source: "database"}
	if err := registry.PersistStored(rotated, func() error { persisted++; return nil }); err != nil {
		t.Fatal(err)
	}
	if registry.Provider("company") != rotated || len(registry.AdminProviders()) != 2 {
		t.Fatal("rotation duplicated or failed to replace provider")
	}
	if err := registry.DeleteStored("company", func(provider *OIDC) error {
		if provider != rotated {
			t.Fatal("delete received stale provider")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if registry.ProviderByKeyAny("company") != nil || registry.ProviderByIssuer(stored.issuer) != nil {
		t.Fatal("deleted provider remained in registry")
	}
	blocked := &OIDC{key: "google", displayName: "Override", issuer: "https://other.example.test", source: "database"}
	if err := registry.PersistStored(blocked, func() error { t.Fatal("environment override persisted"); return nil }); err == nil {
		t.Fatal("environment-managed provider was overwritten")
	}
}

func TestProviderRegistryLoadsEncryptedRegistration(t *testing.T) {
	t.Setenv("ZZIRA_ALLOW_INSECURE_OIDC", "true")
	var issuer string
	discovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
			"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/jwks",
			"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	}))
	defer discovery.Close()
	issuer = discovery.URL
	box, err := secretbox.New(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := box.Seal([]byte("stored-secret"), "ws_default/company")
	if err != nil {
		t.Fatal(err)
	}
	registry := &ProviderRegistry{byKey: map[string]*OIDC{}, byIssuer: map[string]*OIDC{}, disabled: map[string]bool{}}
	if err := registry.LoadStored(t.Context(), []models.IdentityProviderRegistration{{
		ProviderKey: "company", DisplayName: "Company", Issuer: issuer, ClientID: "stored-client", SecretCiphertext: ciphertext,
	}}, box, "ws_default", "http://localhost:8080"); err != nil {
		t.Fatal(err)
	}
	provider := registry.Provider("company")
	if provider == nil || provider.config.ClientID != "stored-client" || provider.config.ClientSecret != "stored-secret" {
		t.Fatalf("loaded provider = %#v", provider)
	}
}
