package models

type Organization struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type Site struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organizationId"`
	WorkspaceID    string `json:"-"`
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	CreatedAt      string `json:"createdAt"`
}

type Product struct {
	ID        string `json:"id"`
	SiteID    string `json:"siteId"`
	Key       string `json:"key"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"createdAt"`
}

type ProductUserActivity struct {
	ProductID    string
	ProductKey   string
	LastActiveAt string
}

type IdentityProviderRegistration struct {
	OrganizationID   string `json:"organizationId"`
	ProviderKey      string `json:"providerKey"`
	DisplayName      string `json:"displayName"`
	Issuer           string `json:"issuer"`
	ClientID         string `json:"clientId"`
	SecretCiphertext []byte `json:"-"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

type Directory struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organizationId"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Active         bool   `json:"active"`
	CreatedAt      string `json:"createdAt"`
}

type OrganizationDomain struct {
	ID                string
	OrganizationID    string
	Name              string
	ClaimType         string
	ClaimStatus       string
	VerificationToken string
	CreatedAt         string
	VerifiedAt        string
}

type OrganizationPolicyResource struct {
	ID                string
	ApplicationStatus string
	Meta              map[string]any
	Links             map[string]any
}

type OrganizationPolicy struct {
	ID             string
	OrganizationID string
	Type           string
	Name           string
	Status         string
	Rule           map[string]any
	Resources      []*OrganizationPolicyResource
	CreatedAt      string
	UpdatedAt      string
}

type Group struct {
	ID          string `json:"id"`
	DirectoryID string `json:"directoryId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	MemberCount int    `json:"memberCount"`
}

type RoleBinding struct {
	ID            int64  `json:"id"`
	ScopeType     string `json:"scopeType"`
	ScopeID       string `json:"scopeId"`
	RoleKey       string `json:"roleKey"`
	PrincipalType string `json:"principalType"`
	PrincipalID   string `json:"principalId"`
	Source        string `json:"source"`
	CreatedAt     string `json:"createdAt"`
	Assignment    string `json:"assignment,omitempty"`
}

type OrganizationAuditEvent struct {
	ID             int64          `json:"id"`
	OrganizationID string         `json:"organizationId"`
	ActorID        string         `json:"actorId,omitempty"`
	ActorName      string         `json:"-"`
	ActorEmail     string         `json:"-"`
	Action         string         `json:"action"`
	TargetType     string         `json:"targetType"`
	TargetID       string         `json:"targetId"`
	Detail         map[string]any `json:"detail"`
	CreatedAt      string         `json:"createdAt"`
}
