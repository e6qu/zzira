package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

func normalizeOrganizationDomain(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(value, ".")))
	if len(value) < 3 || len(value) > 253 || !strings.Contains(value, ".") {
		return "", fmt.Errorf("%w: domain must be a fully qualified DNS name", ErrAdminValidation)
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("%w: domain contains an invalid DNS label", ErrAdminValidation)
		}
		for _, character := range label {
			if character < 'a' || character > 'z' {
				if character < '0' || character > '9' {
					if character != '-' {
						return "", fmt.Errorf("%w: domain contains an invalid DNS character", ErrAdminValidation)
					}
				}
			}
		}
	}
	return value, nil
}

func scanOrganizationDomain(row pgx.Row) (*models.OrganizationDomain, error) {
	domain := &models.OrganizationDomain{}
	var createdAt time.Time
	var verifiedAt *time.Time
	err := row.Scan(&domain.ID, &domain.OrganizationID, &domain.Name, &domain.ClaimType,
		&domain.ClaimStatus, &domain.VerificationToken, &createdAt, &verifiedAt)
	if err != nil {
		return nil, err
	}
	domain.CreatedAt = formatAdminTime(createdAt)
	if verifiedAt != nil {
		domain.VerifiedAt = formatAdminTime(*verifiedAt)
	}
	return domain, nil
}

func (s *Store) OrganizationDomains(ctx context.Context, organizationID string) ([]*models.OrganizationDomain, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id::text,organization_id::text,name,claim_type,claim_status,verification_token,created_at,verified_at
		FROM organization_domains WHERE organization_id::text=$1 ORDER BY name,id`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	domains := make([]*models.OrganizationDomain, 0)
	for rows.Next() {
		domain, err := scanOrganizationDomain(rows)
		if err != nil {
			return nil, err
		}
		domains = append(domains, domain)
	}
	return domains, rows.Err()
}

func (s *Store) OrganizationDomain(ctx context.Context, organizationID, domainID string) (*models.OrganizationDomain, error) {
	return scanOrganizationDomain(s.Pool.QueryRow(ctx, `
		SELECT id::text,organization_id::text,name,claim_type,claim_status,verification_token,created_at,verified_at
		FROM organization_domains WHERE organization_id::text=$1 AND id::text=$2`, organizationID, domainID))
}

func (s *Store) CreateOrganizationDomain(ctx context.Context, workspaceID, actorID, name string) (*models.OrganizationDomain, error) {
	name, err := normalizeOrganizationDomain(name)
	if err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID string
	if err := tx.QueryRow(ctx, `SELECT organization_id::text FROM sites WHERE workspace_id=$1`, workspaceID).Scan(&organizationID); err != nil {
		return nil, err
	}
	domain, err := scanOrganizationDomain(tx.QueryRow(ctx, `
		INSERT INTO organization_domains(organization_id,name,verification_token)
		VALUES($1::uuid,$2,$3)
		RETURNING id::text,organization_id::text,name,claim_type,claim_status,verification_token,created_at,verified_at`,
		organizationID, name, NewID("domain")))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: domain is already claimed in this organization", ErrAdminConflict)
	}
	if err != nil {
		return nil, err
	}
	detail, err := json.Marshal(map[string]any{"name": domain.Name, "claimType": domain.ClaimType, "claimStatus": domain.ClaimStatus})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,'domain.created','domain',$3,$4)`, organizationID, actorID, domain.ID, detail); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return domain, nil
}

func (s *Store) VerifyOrganizationDomain(ctx context.Context, workspaceID, actorID, domainID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID, name string
	err = tx.QueryRow(ctx, `
		UPDATE organization_domains d SET claim_status='verified',verified_at=now()
		FROM sites si
		WHERE d.id::text=$1 AND si.workspace_id=$2 AND si.organization_id=d.organization_id
		  AND d.claim_status<>'verified'
		RETURNING d.organization_id::text,d.name`, domainID, workspaceID).Scan(&organizationID, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAdminNotFound
	}
	if err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]any{"name": name, "claimStatus": "verified"})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,'domain.verified','domain',$3,$4)`, organizationID, actorID, domainID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteOrganizationDomain(ctx context.Context, workspaceID, actorID, domainID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID, name string
	err = tx.QueryRow(ctx, `
		DELETE FROM organization_domains d USING sites si
		WHERE d.id::text=$1 AND si.workspace_id=$2 AND si.organization_id=d.organization_id
		RETURNING d.organization_id::text,d.name`, domainID, workspaceID).Scan(&organizationID, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAdminNotFound
	}
	if err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]any{"name": name})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,'domain.deleted','domain',$3,$4)`, organizationID, actorID, domainID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
