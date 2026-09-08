package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

func (s *Store) IdentityProviderRegistrationsByWorkspace(ctx context.Context, workspaceID string) ([]models.IdentityProviderRegistration, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT registration.organization_id::text,registration.provider_key,registration.display_name,
		       registration.issuer,registration.client_id,registration.secret_ciphertext,
		       registration.created_at,registration.updated_at
		FROM identity_provider_registrations registration
		JOIN sites site ON site.organization_id=registration.organization_id
		WHERE site.workspace_id=$1 ORDER BY registration.display_name,registration.provider_key`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	registrations := []models.IdentityProviderRegistration{}
	for rows.Next() {
		var registration models.IdentityProviderRegistration
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&registration.OrganizationID, &registration.ProviderKey, &registration.DisplayName,
			&registration.Issuer, &registration.ClientID, &registration.SecretCiphertext, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		registration.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		registration.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
		registrations = append(registrations, registration)
	}
	return registrations, rows.Err()
}

func (s *Store) SaveIdentityProviderRegistration(ctx context.Context, workspaceID, actorID string, registration models.IdentityProviderRegistration) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID string
	if err := tx.QueryRow(ctx, `SELECT organization_id::text FROM sites WHERE workspace_id=$1`, workspaceID).Scan(&organizationID); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_provider_registrations WHERE organization_id=$1::uuid AND provider_key=$2)`, organizationID, registration.ProviderKey).Scan(&exists); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO identity_provider_registrations
		  (organization_id,provider_key,display_name,issuer,client_id,secret_ciphertext,created_by,updated_by)
		VALUES($1::uuid,$2,$3,$4,$5,$6,$7,$7)
		ON CONFLICT(organization_id,provider_key) DO UPDATE SET
		  display_name=EXCLUDED.display_name,issuer=EXCLUDED.issuer,client_id=EXCLUDED.client_id,
		  secret_ciphertext=EXCLUDED.secret_ciphertext,updated_by=EXCLUDED.updated_by,updated_at=now()`,
		organizationID, registration.ProviderKey, registration.DisplayName, registration.Issuer,
		registration.ClientID, registration.SecretCiphertext, actorID); err != nil {
		return err
	}
	action := "identity.provider.registered"
	if exists {
		action = "identity.provider.credentials.rotated"
	}
	detail, err := json.Marshal(map[string]any{
		"provider": registration.ProviderKey, "issuer": registration.Issuer, "credentialChanged": true,
	})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,$3,'identity-provider',$4,$5::jsonb)`,
		organizationID, actorID, action, registration.ProviderKey, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteIdentityProviderRegistration(ctx context.Context, workspaceID, actorID, providerKey, issuer string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID string
	if err := tx.QueryRow(ctx, `SELECT organization_id::text FROM sites WHERE workspace_id=$1`, workspaceID).Scan(&organizationID); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `DELETE FROM identity_provider_registrations WHERE organization_id=$1::uuid AND provider_key=$2`, organizationID, providerKey)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrAdminNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM identity_provider_settings WHERE organization_id=$1::uuid AND provider_key=$2`, organizationID, providerKey); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE oidc_issuer=$1`, issuer); err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]any{"provider": providerKey, "issuer": issuer})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,'identity.provider.deleted','identity-provider',$3,$4::jsonb)`, organizationID, actorID, providerKey, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
