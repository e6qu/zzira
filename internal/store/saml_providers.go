package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// A site signs people in through SAML by trusting one identity provider: what
// it calls itself, where it answers, and the certificates it signs with. The
// site keeps the sign-ins it started so an answer can be held to one, and the
// assertions it has read so none is read twice.

// SAMLProvider is a SAML identity provider a site signs people in through.
type SAMLProvider struct {
	ProviderKey, DisplayName string
	EntityID, SSOURL         string
	// Certificates are the provider's signing certificates as they were
	// pasted in: PEM, or the bare base64 its metadata carries.
	Certificates                  string
	EmailAttribute, NameAttribute string
	Enabled                       bool
	UpdatedAt                     time.Time
}

// ErrSAMLProviderNotFound is a provider a site does not have.
var ErrSAMLProviderNotFound = errors.New("saml identity provider not found")

// SAMLProviders are the SAML identity providers a site trusts.
func (s *Store) SAMLProviders(ctx context.Context, workspaceID string) ([]SAMLProvider, error) {
	rows, err := s.Pool.Query(ctx, `SELECT provider.provider_key,provider.display_name,provider.entity_id,provider.sso_url,
		provider.certificates,provider.email_attribute,provider.name_attribute,provider.enabled,provider.updated_at
		FROM saml_identity_providers provider JOIN sites site ON site.organization_id=provider.organization_id
		WHERE site.workspace_id=$1 ORDER BY lower(provider.display_name),provider.provider_key`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	providers := []SAMLProvider{}
	for rows.Next() {
		var provider SAMLProvider
		if err := rows.Scan(&provider.ProviderKey, &provider.DisplayName, &provider.EntityID, &provider.SSOURL,
			&provider.Certificates, &provider.EmailAttribute, &provider.NameAttribute, &provider.Enabled, &provider.UpdatedAt); err != nil {
			return nil, err
		}
		providers = append(providers, provider)
	}
	return providers, rows.Err()
}

// SAMLProvider is one provider by its key.
func (s *Store) SAMLProvider(ctx context.Context, workspaceID, providerKey string) (SAMLProvider, error) {
	var provider SAMLProvider
	err := s.Pool.QueryRow(ctx, `SELECT provider.provider_key,provider.display_name,provider.entity_id,provider.sso_url,
		provider.certificates,provider.email_attribute,provider.name_attribute,provider.enabled,provider.updated_at
		FROM saml_identity_providers provider JOIN sites site ON site.organization_id=provider.organization_id
		WHERE site.workspace_id=$1 AND provider.provider_key=$2`, workspaceID, providerKey).Scan(
		&provider.ProviderKey, &provider.DisplayName, &provider.EntityID, &provider.SSOURL,
		&provider.Certificates, &provider.EmailAttribute, &provider.NameAttribute, &provider.Enabled, &provider.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SAMLProvider{}, ErrSAMLProviderNotFound
	}
	return provider, err
}

// SaveSAMLProvider writes a provider a site administrator configured.
func (s *Store) SaveSAMLProvider(ctx context.Context, workspaceID, actorID string, provider SAMLProvider) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	organizationID, err := samlOrganization(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO saml_identity_providers
		(organization_id,provider_key,display_name,entity_id,sso_url,certificates,email_attribute,name_attribute,enabled,created_by,updated_by)
		VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)
		ON CONFLICT(organization_id,provider_key) DO UPDATE SET
		  display_name=EXCLUDED.display_name,entity_id=EXCLUDED.entity_id,sso_url=EXCLUDED.sso_url,
		  certificates=EXCLUDED.certificates,email_attribute=EXCLUDED.email_attribute,
		  name_attribute=EXCLUDED.name_attribute,enabled=EXCLUDED.enabled,
		  updated_by=EXCLUDED.updated_by,updated_at=now()`,
		organizationID, provider.ProviderKey, provider.DisplayName, provider.EntityID, provider.SSOURL,
		provider.Certificates, provider.EmailAttribute, provider.NameAttribute, provider.Enabled, actorID); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("another identity provider on this site already calls itself %q", provider.EntityID)
		}
		return err
	}
	if err := samlAudit(ctx, tx, workspaceID, actorID, "identity.saml.provider.saved", provider.ProviderKey, map[string]any{
		"entityId": provider.EntityID, "ssoUrl": provider.SSOURL, "enabled": provider.Enabled,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteSAMLProvider takes a provider away, and with it the sign-ins waiting
// for it.
func (s *Store) DeleteSAMLProvider(ctx context.Context, workspaceID, actorID, providerKey string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	organizationID, err := samlOrganization(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM saml_identity_providers WHERE organization_id=$1::uuid AND provider_key=$2`, organizationID, providerKey)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSAMLProviderNotFound
	}
	if err := samlAudit(ctx, tx, workspaceID, actorID, "identity.saml.provider.removed", providerKey, map[string]any{}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// StartSAMLSignIn remembers a sign-in this site began, so the answer can be
// held to it.
func (s *Store) StartSAMLSignIn(ctx context.Context, workspaceID, providerKey, requestID, relayState, linkUserID string) error {
	organizationID, err := samlOrganizationID(ctx, s, workspaceID)
	if err != nil {
		return err
	}
	// Sign-ins nobody finished are cleared out, so the table holds what is
	// still in flight rather than everything that ever started.
	if _, err := s.Pool.Exec(ctx, `DELETE FROM saml_sign_in_requests WHERE created_at < now() - INTERVAL '1 hour'`); err != nil {
		return err
	}
	var link any
	if linkUserID != "" {
		link = linkUserID
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO saml_sign_in_requests(request_id,organization_id,provider_key,relay_state,link_user_id)
		VALUES($1,$2::uuid,$3,$4,$5)`, requestID, organizationID, providerKey, relayState, link)
	return err
}

// SAMLSignIn is a sign-in this site started, read once: the row is taken
// away as it is read, so one answer cannot be posted twice.
type SAMLSignIn struct {
	ProviderKey, RelayState, LinkUserID string
	StartedAt                           time.Time
}

// ConsumeSAMLSignIn reads and forgets the sign-in an answer names.
func (s *Store) ConsumeSAMLSignIn(ctx context.Context, workspaceID, requestID string) (SAMLSignIn, error) {
	organizationID, err := samlOrganizationID(ctx, s, workspaceID)
	if err != nil {
		return SAMLSignIn{}, err
	}
	var signIn SAMLSignIn
	var link *string
	err = s.Pool.QueryRow(ctx, `DELETE FROM saml_sign_in_requests
		WHERE request_id=$1 AND organization_id=$2::uuid AND created_at > now() - INTERVAL '1 hour'
		RETURNING provider_key,relay_state,link_user_id,created_at`, requestID, organizationID).Scan(
		&signIn.ProviderKey, &signIn.RelayState, &link, &signIn.StartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SAMLSignIn{}, errors.New("that sign-in is not one this site started, or it has expired")
	}
	if link != nil {
		signIn.LinkUserID = *link
	}
	return signIn, err
}

// RememberSAMLAssertion records that an assertion has been read, and refuses
// one that has been read before: an answer somebody kept is worth nothing the
// second time.
func (s *Store) RememberSAMLAssertion(ctx context.Context, assertionID string, expires time.Time) error {
	if strings.TrimSpace(assertionID) == "" {
		return errors.New("the assertion has no id, so it cannot be read once")
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM saml_seen_assertions WHERE expires_at < now()`); err != nil {
		return err
	}
	tag, err := s.Pool.Exec(ctx, `INSERT INTO saml_seen_assertions(assertion_id,expires_at) VALUES($1,$2)
		ON CONFLICT(assertion_id) DO NOTHING`, assertionID, expires)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("that assertion has already been used")
	}
	return nil
}

func samlOrganization(ctx context.Context, tx pgx.Tx, workspaceID string) (string, error) {
	var organizationID string
	err := tx.QueryRow(ctx, `SELECT organization_id::text FROM sites WHERE workspace_id=$1`, workspaceID).Scan(&organizationID)
	return organizationID, err
}

func samlOrganizationID(ctx context.Context, s *Store, workspaceID string) (string, error) {
	var organizationID string
	err := s.Pool.QueryRow(ctx, `SELECT organization_id::text FROM sites WHERE workspace_id=$1`, workspaceID).Scan(&organizationID)
	return organizationID, err
}

// samlAudit records what an administrator did to the site's SAML setup.
func samlAudit(ctx context.Context, tx pgx.Tx, workspaceID, actorID, action, providerKey string, detail map[string]any) error {
	encoded, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,$3,'saml_identity_provider',$4,$5::jsonb FROM sites WHERE workspace_id=$1`,
		workspaceID, actorID, action, providerKey, string(encoded))
	return err
}
