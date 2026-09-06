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
	r.ordered = append(r.ordered, provider)
	r.byKey[provider.key] = provider
	r.byIssuer[provider.issuer] = provider
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
		providers = append(providers, LoginProvider{Key: provider.key, DisplayName: provider.displayName, Kind: kind, Issuer: provider.issuer, Enabled: true})
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
