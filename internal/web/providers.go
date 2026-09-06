package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/secretbox"
)

const (
	googleIssuer     = "https://accounts.google.com"
	atlassianIssuer  = "https://auth.atlassian.com"
	atlassianProfile = "https://api.atlassian.com/me"
)

var microsoftTenantPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,254}$`)

// ProviderRegistry is the configured browser identity-provider catalog. The
// order is stable so the sign-in and administration surfaces do not jump when
// configuration changes elsewhere in the process environment.
type ProviderRegistry struct {
	mu       sync.RWMutex
	ordered  []*OIDC
	byKey    map[string]*OIDC
	byIssuer map[string]*OIDC
	disabled map[string]bool
}

type LoginProvider struct {
	Key         string
	DisplayName string
	Kind        string
	Issuer      string
	Enabled     bool
	Source      string
	Manageable  bool
}

type StoredProviderInput struct {
	Key          string
	DisplayName  string
	Issuer       string
	ClientID     string
	ClientSecret string
}

func NewIdentityProviders(ctx context.Context) (*ProviderRegistry, error) {
	registry := &ProviderRegistry{byKey: map[string]*OIDC{}, byIssuer: map[string]*OIDC{}, disabled: map[string]bool{}}
	shauth, err := NewOIDC(ctx)
	if err != nil {
		return nil, err
	}
	if shauth != nil {
		registry.add(shauth)
	}
	externalURL := strings.TrimRight(os.Getenv("ZZIRA_EXTERNAL_URL"), "/")

	google, err := configuredOIDCProvider(ctx, "google", "Google", googleIssuer, externalURL,
		"ZZIRA_GOOGLE_CLIENT_ID", "ZZIRA_GOOGLE_CLIENT_SECRET")
	if err != nil {
		return nil, err
	}
	if google != nil {
		registry.add(google)
	}

	microsoft, err := configuredMicrosoftProvider(ctx, externalURL)
	if err != nil {
		return nil, err
	}
	if microsoft != nil {
		registry.add(microsoft)
	}

	atlassian, err := configuredAtlassianProvider(externalURL)
	if err != nil {
		return nil, err
	}
	if atlassian != nil {
		registry.add(atlassian)
	}
	return registry, nil
}

func configuredOIDCProvider(ctx context.Context, key, displayName, issuer, externalURL, clientIDEnv, clientSecretEnv string) (*OIDC, error) {
	clientID := strings.TrimSpace(os.Getenv(clientIDEnv))
	clientSecret := strings.TrimSpace(os.Getenv(clientSecretEnv))
	present := 0
	for _, value := range []string{clientID, clientSecret} {
		if value != "" {
			present++
		}
	}
	if present == 0 {
		return nil, nil
	}
	if present != 2 || externalURL == "" {
		return nil, fmt.Errorf("%s, %s, and ZZIRA_EXTERNAL_URL must be set together", clientIDEnv, clientSecretEnv)
	}
	if err := validExternalURL(externalURL); err != nil {
		return nil, err
	}
	if err := validOIDCURL(issuer); err != nil {
		return nil, fmt.Errorf("%s issuer: %w", displayName, err)
	}
	return newDiscoveredOIDC(ctx, key, displayName, issuer, clientID, clientSecret, externalURL)
}

func configuredMicrosoftProvider(ctx context.Context, externalURL string) (*OIDC, error) {
	clientID := strings.TrimSpace(os.Getenv("ZZIRA_MICROSOFT_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("ZZIRA_MICROSOFT_CLIENT_SECRET"))
	tenant := strings.TrimSpace(os.Getenv("ZZIRA_MICROSOFT_TENANT_ID"))
	present := 0
	for _, value := range []string{clientID, clientSecret, tenant} {
		if value != "" {
			present++
		}
	}
	if present == 0 {
		return nil, nil
	}
	if present != 3 || externalURL == "" {
		return nil, fmt.Errorf("ZZIRA_MICROSOFT_CLIENT_ID, ZZIRA_MICROSOFT_CLIENT_SECRET, ZZIRA_MICROSOFT_TENANT_ID, and ZZIRA_EXTERNAL_URL must be set together")
	}
	reservedTenant := strings.ToLower(tenant)
	if !microsoftTenantPattern.MatchString(tenant) || reservedTenant == "common" || reservedTenant == "organizations" || reservedTenant == "consumers" {
		return nil, fmt.Errorf("ZZIRA_MICROSOFT_TENANT_ID must be a tenant UUID or verified tenant domain")
	}
	if err := validExternalURL(externalURL); err != nil {
		return nil, err
	}
	issuer := "https://login.microsoftonline.com/" + tenant + "/v2.0"
	return newDiscoveredOIDC(ctx, "microsoft", "Microsoft", issuer, clientID, clientSecret, externalURL)
}

func configuredAtlassianProvider(externalURL string) (*OIDC, error) {
	clientID := strings.TrimSpace(os.Getenv("ZZIRA_ATLASSIAN_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("ZZIRA_ATLASSIAN_CLIENT_SECRET"))
	present := 0
	for _, value := range []string{clientID, clientSecret} {
		if value != "" {
			present++
		}
	}
	if present == 0 {
		return nil, nil
	}
	if present != 2 || externalURL == "" {
		return nil, fmt.Errorf("ZZIRA_ATLASSIAN_CLIENT_ID, ZZIRA_ATLASSIAN_CLIENT_SECRET, and ZZIRA_EXTERNAL_URL must be set together")
	}
	if err := validExternalURL(externalURL); err != nil {
		return nil, err
	}
	return &OIDC{
		key: "atlassian", displayName: "Atlassian", issuer: atlassianIssuer, atlassian: true,
		profileEndpoint: atlassianProfile, httpClient: &http.Client{Timeout: 10 * time.Second},
		config: oauth2.Config{
			ClientID: clientID, ClientSecret: clientSecret,
			Endpoint:    oauth2.Endpoint{AuthURL: atlassianIssuer + "/authorize", TokenURL: atlassianIssuer + "/oauth/token"},
			RedirectURL: externalURL + "/auth/atlassian/callback", Scopes: []string{"read:me"},
		},
	}, nil
}

func validExternalURL(raw string) error {
	if err := validOIDCURL(raw); err != nil {
		return fmt.Errorf("ZZIRA_EXTERNAL_URL: %w", err)
	}
	parsed, _ := url.Parse(raw)
	if parsed.Path != "" && parsed.Path != "/" {
		return fmt.Errorf("ZZIRA_EXTERNAL_URL: must be an origin without a path")
	}
	return nil
}

func (r *ProviderRegistry) add(provider *OIDC) {
	if provider.source == "" {
		provider.source = "environment"
	}
	r.ordered = append(r.ordered, provider)
	r.byKey[provider.key] = provider
	r.byIssuer[provider.issuer] = provider
}

func BuildStoredOIDCProvider(ctx context.Context, input StoredProviderInput, externalURL string) (*OIDC, error) {
	input.Key = strings.TrimSpace(strings.ToLower(input.Key))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Issuer = strings.TrimRight(strings.TrimSpace(input.Issuer), "/")
	input.ClientID = strings.TrimSpace(input.ClientID)
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`).MatchString(input.Key) {
		return nil, fmt.Errorf("provider key must use 2-31 lowercase letters, numbers, or hyphens")
	}
	if input.DisplayName == "" || len(input.DisplayName) > 80 {
		return nil, fmt.Errorf("provider name must contain 1-80 characters")
	}
	if input.ClientID == "" || strings.TrimSpace(input.ClientSecret) == "" {
		return nil, fmt.Errorf("client ID and client secret are required")
	}
	if err := validExternalURL(externalURL); err != nil {
		return nil, err
	}
	if err := validOIDCURL(input.Issuer); err != nil {
		return nil, fmt.Errorf("provider issuer: %w", err)
	}
	provider, err := newDiscoveredOIDC(ctx, input.Key, input.DisplayName, input.Issuer, input.ClientID, input.ClientSecret, externalURL)
	if err != nil {
		return nil, err
	}
	provider.source = "database"
	return provider, nil
}

func (r *ProviderRegistry) LoadStored(ctx context.Context, registrations []models.IdentityProviderRegistration, box *secretbox.Box, workspaceID, externalURL string) error {
	if len(registrations) > 0 && box == nil {
		return fmt.Errorf("ZZIRA_IDENTITY_ENCRYPTION_KEY is required to load stored identity providers")
	}
	for _, registration := range registrations {
		secret, err := box.Open(registration.SecretCiphertext, workspaceID+"/"+registration.ProviderKey)
		if err != nil {
			return fmt.Errorf("decrypt identity provider %s: %w", registration.ProviderKey, err)
		}
		provider, err := BuildStoredOIDCProvider(ctx, StoredProviderInput{
			Key: registration.ProviderKey, DisplayName: registration.DisplayName, Issuer: registration.Issuer,
			ClientID: registration.ClientID, ClientSecret: string(secret),
		}, externalURL)
		for index := range secret {
			secret[index] = 0
		}
		if err != nil {
			return fmt.Errorf("load identity provider %s: %w", registration.ProviderKey, err)
		}
		if err := r.PersistStored(provider, func() error { return nil }); err != nil {
			return fmt.Errorf("register identity provider %s: %w", registration.ProviderKey, err)
		}
	}
	return nil
}

// PersistStored validates registry conflicts, commits encrypted registration
// state, then publishes the provider to new login and linking requests.
func (r *ProviderRegistry) PersistStored(provider *OIDC, persist func() error) error {
	if r == nil || provider == nil || provider.source != "database" {
		return fmt.Errorf("stored provider is invalid")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if current := r.byKey[provider.key]; current != nil && current.source != "database" {
		return fmt.Errorf("provider key is owned by deployment configuration")
	}
	if current := r.byIssuer[provider.issuer]; current != nil && current.key != provider.key {
		return fmt.Errorf("provider issuer is already registered")
	}
	if err := persist(); err != nil {
		return err
	}
	if current := r.byKey[provider.key]; current != nil {
		delete(r.byIssuer, current.issuer)
		for index := range r.ordered {
			if r.ordered[index].key == provider.key {
				r.ordered[index] = provider
				r.byKey[provider.key] = provider
				r.byIssuer[provider.issuer] = provider
				return nil
			}
		}
	}
	r.ordered = append(r.ordered, provider)
	r.byKey[provider.key] = provider
	r.byIssuer[provider.issuer] = provider
	return nil
}

func (r *ProviderRegistry) DeleteStored(key string, persist func(*OIDC) error) error {
	if r == nil {
		return fmt.Errorf("identity provider registry is unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	provider := r.byKey[key]
	if provider == nil || provider.source != "database" {
		return fmt.Errorf("stored identity provider was not found")
	}
	if err := persist(provider); err != nil {
		return err
	}
	delete(r.byKey, key)
	delete(r.byIssuer, provider.issuer)
	delete(r.disabled, key)
	for index := range r.ordered {
		if r.ordered[index].key == key {
			r.ordered = append(r.ordered[:index], r.ordered[index+1:]...)
			break
		}
	}
	return nil
}

func (r *ProviderRegistry) Provider(key string) *OIDC {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.disabled[key] {
		return nil
	}
	return r.byKey[key]
}

func (r *ProviderRegistry) ProviderByIssuer(issuer string) *OIDC {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byIssuer[issuer]
}

func (r *ProviderRegistry) ProviderByKeyAny(key string) *OIDC {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byKey[key]
}

func (r *ProviderRegistry) LoginProviders() []LoginProvider {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := make([]LoginProvider, 0, len(r.ordered))
	for _, provider := range r.ordered {
		if r.disabled[provider.key] {
			continue
		}
		kind := "OpenID Connect"
		if provider.atlassian {
			kind = "OAuth 2.0 (3LO)"
		}
		providers = append(providers, LoginProvider{Key: provider.key, DisplayName: provider.displayName, Kind: kind, Issuer: provider.issuer, Enabled: true, Source: provider.source, Manageable: provider.source == "database"})
	}
	return providers
}

func (r *ProviderRegistry) AdminProviders() []LoginProvider {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	providers := make([]LoginProvider, 0, len(r.ordered))
	for _, provider := range r.ordered {
		kind := "OpenID Connect"
		if provider.atlassian {
			kind = "OAuth 2.0 (3LO)"
		}
		providers = append(providers, LoginProvider{
			Key: provider.key, DisplayName: provider.displayName, Kind: kind, Issuer: provider.issuer, Enabled: !r.disabled[provider.key],
			Source: provider.source, Manageable: provider.source == "database",
		})
	}
	return providers
}

func (r *ProviderRegistry) ApplyEnabled(settings map[string]bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.disabled == nil {
		r.disabled = map[string]bool{}
	}
	for key, enabled := range settings {
		if _, configured := r.byKey[key]; configured {
			r.disabled[key] = !enabled
		}
	}
}

func (r *ProviderRegistry) SetEnabled(key string, enabled bool) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.disabled == nil {
		r.disabled = map[string]bool{}
	}
	if _, configured := r.byKey[key]; !configured {
		return false
	}
	r.disabled[key] = !enabled
	return true
}

func (r *ProviderRegistry) FormActionOrigins() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := map[string]bool{}
	for _, provider := range r.ordered {
		if origin := provider.FormActionOrigin(); origin != "" {
			seen[origin] = true
		}
	}
	origins := make([]string, 0, len(seen))
	for origin := range seen {
		origins = append(origins, origin)
	}
	sort.Strings(origins)
	return strings.Join(origins, " ")
}
