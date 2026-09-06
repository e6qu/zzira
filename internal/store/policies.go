package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

type PolicyResourceInput struct {
	ID    string
	Meta  map[string]any
	Links map[string]any
}

type PolicyInput struct {
	Type      string
	Name      string
	Status    string
	Values    []string
	Resources []PolicyResourceInput
}

func normalizePolicyInput(input PolicyInput) (PolicyInput, error) {
	input.Type = strings.TrimSpace(input.Type)
	input.Name = strings.TrimSpace(input.Name)
	input.Status = strings.TrimSpace(input.Status)
	if input.Type != "ip-allowlist" && input.Type != "data-residency" {
		return input, fmt.Errorf("%w: policy type must be ip-allowlist or data-residency", ErrAdminValidation)
	}
	if input.Name == "" || len(input.Name) > 255 {
		return input, fmt.Errorf("%w: policy name must contain between 1 and 255 characters", ErrAdminValidation)
	}
	if input.Status == "" {
		input.Status = "disabled"
	}
	if input.Status != "enabled" && input.Status != "disabled" {
		return input, fmt.Errorf("%w: policy status must be enabled or disabled", ErrAdminValidation)
	}
	if len(input.Values) == 0 || len(input.Values) > 500 {
		return input, fmt.Errorf("%w: policy rule must contain between 1 and 500 values", ErrAdminValidation)
	}
	seenValues := map[string]bool{}
	for index, value := range input.Values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 255 || seenValues[value] {
			return input, fmt.Errorf("%w: policy rule values must be unique and non-empty", ErrAdminValidation)
		}
		if input.Type == "ip-allowlist" {
			if net.ParseIP(value) == nil {
				if _, _, err := net.ParseCIDR(value); err != nil {
					return input, fmt.Errorf("%w: %q is not an IP address or CIDR range", ErrAdminValidation, value)
				}
			}
		}
		seenValues[value] = true
		input.Values[index] = value
	}
	if len(input.Resources) > 100 {
		return input, fmt.Errorf("%w: policy accepts at most 100 resources", ErrAdminValidation)
	}
	seenResources := map[string]bool{}
	for index, resource := range input.Resources {
		resource.ID = strings.TrimSpace(resource.ID)
		if resource.ID == "" || seenResources[resource.ID] {
			return input, fmt.Errorf("%w: policy resources must be unique and non-empty", ErrAdminValidation)
		}
		if resource.Meta == nil {
			resource.Meta = map[string]any{}
		}
		if resource.Links == nil {
			resource.Links = map[string]any{}
		}
		seenResources[resource.ID] = true
		input.Resources[index] = resource
	}
	return input, nil
}

func policyOrganizationForWorkspace(ctx context.Context, tx pgx.Tx, workspaceID string) (string, error) {
	var organizationID string
	err := tx.QueryRow(ctx, `SELECT organization_id::text FROM sites WHERE workspace_id=$1`, workspaceID).Scan(&organizationID)
	return organizationID, err
}

func validatePolicyResource(ctx context.Context, tx pgx.Tx, organizationID, resourceID string) error {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM products p JOIN sites si ON si.id=p.site_id
		  WHERE si.organization_id=$1::uuid
		    AND 'ari:cloud:' || p.product_key || '::site/' || p.site_id::text=$2
		)`, organizationID, resourceID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: policy resource does not belong to this organization", ErrAdminNotFound)
	}
	return nil
}

func scanPolicy(row pgx.Row) (*models.OrganizationPolicy, error) {
	policy := &models.OrganizationPolicy{}
	var rule []byte
	var createdAt, updatedAt time.Time
	err := row.Scan(&policy.ID, &policy.OrganizationID, &policy.Type, &policy.Name, &policy.Status, &rule, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rule, &policy.Rule); err != nil {
		return nil, err
	}
	policy.CreatedAt = formatAdminTime(createdAt)
	policy.UpdatedAt = formatAdminTime(updatedAt)
	policy.Resources = []*models.OrganizationPolicyResource{}
	return policy, nil
}

func (s *Store) policyResources(ctx context.Context, policyID string) ([]*models.OrganizationPolicyResource, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT resource_id,application_status,meta,links
		FROM organization_policy_resources WHERE policy_id::text=$1 ORDER BY resource_id`, policyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	resources := make([]*models.OrganizationPolicyResource, 0)
	for rows.Next() {
		resource := &models.OrganizationPolicyResource{}
		var meta, links []byte
		if err := rows.Scan(&resource.ID, &resource.ApplicationStatus, &meta, &links); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(meta, &resource.Meta); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(links, &resource.Links); err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	return resources, rows.Err()
}

func (s *Store) OrganizationPolicies(ctx context.Context, organizationID, policyType string) ([]*models.OrganizationPolicy, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id::text,organization_id::text,policy_type,name,status,rule,created_at,updated_at
		FROM organization_policies
		WHERE organization_id::text=$1 AND ($2='' OR policy_type=$2)
		ORDER BY name,id`, organizationID, policyType)
	if err != nil {
		return nil, err
	}
	policies := make([]*models.OrganizationPolicy, 0)
	for rows.Next() {
		policy, err := scanPolicy(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		policies = append(policies, policy)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for _, policy := range policies {
		policy.Resources, err = s.policyResources(ctx, policy.ID)
		if err != nil {
			return nil, err
		}
	}
	return policies, nil
}

func (s *Store) OrganizationPolicy(ctx context.Context, organizationID, policyID string) (*models.OrganizationPolicy, error) {
	policy, err := scanPolicy(s.Pool.QueryRow(ctx, `
		SELECT id::text,organization_id::text,policy_type,name,status,rule,created_at,updated_at
		FROM organization_policies WHERE organization_id::text=$1 AND id::text=$2`, organizationID, policyID))
	if err != nil {
		return nil, err
	}
	policy.Resources, err = s.policyResources(ctx, policy.ID)
	return policy, err
}

func addPolicyAudit(ctx context.Context, tx pgx.Tx, organizationID, actorID, action, policyID string, detail map[string]any) error {
	encoded, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,$3,'policy',$4,$5)`, organizationID, actorID, action, policyID, encoded)
	return err
}

func insertPolicyResources(ctx context.Context, tx pgx.Tx, organizationID, policyID string, resources []PolicyResourceInput) error {
	for _, resource := range resources {
		if err := validatePolicyResource(ctx, tx, organizationID, resource.ID); err != nil {
			return err
		}
		meta, err := json.Marshal(resource.Meta)
		if err != nil {
			return err
		}
		links, err := json.Marshal(resource.Links)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO organization_policy_resources(policy_id,resource_id,meta,links)
			VALUES($1::uuid,$2,$3,$4)`, policyID, resource.ID, meta, links); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CreateOrganizationPolicy(ctx context.Context, workspaceID, actorID string, input PolicyInput) (*models.OrganizationPolicy, error) {
	input, err := normalizePolicyInput(input)
	if err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	organizationID, err := policyOrganizationForWorkspace(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	rule, err := json.Marshal(map[string]any{"in": input.Values})
	if err != nil {
		return nil, err
	}
	policy, err := scanPolicy(tx.QueryRow(ctx, `
		INSERT INTO organization_policies(organization_id,policy_type,name,status,rule)
		VALUES($1::uuid,$2,$3,$4,$5)
		RETURNING id::text,organization_id::text,policy_type,name,status,rule,created_at,updated_at`,
		organizationID, input.Type, input.Name, input.Status, rule))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: policy name already exists", ErrAdminConflict)
	}
	if err != nil {
		return nil, err
	}
	if err := insertPolicyResources(ctx, tx, organizationID, policy.ID, input.Resources); err != nil {
		return nil, err
	}
	if err := addPolicyAudit(ctx, tx, organizationID, actorID, "policy.created", policy.ID, map[string]any{"name": policy.Name, "type": policy.Type, "status": policy.Status}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.OrganizationPolicy(ctx, organizationID, policy.ID)
}

func (s *Store) UpdateOrganizationPolicy(ctx context.Context, workspaceID, actorID, policyID string, input PolicyInput) (*models.OrganizationPolicy, error) {
	input, err := normalizePolicyInput(input)
	if err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	organizationID, err := policyOrganizationForWorkspace(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	rule, err := json.Marshal(map[string]any{"in": input.Values})
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE organization_policies SET policy_type=$3,name=$4,status=$5,rule=$6,updated_at=now()
		WHERE id::text=$1 AND organization_id::text=$2`, policyID, organizationID, input.Type, input.Name, input.Status, rule)
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: policy name already exists", ErrAdminConflict)
	}
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrAdminNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM organization_policy_resources WHERE policy_id::text=$1`, policyID); err != nil {
		return nil, err
	}
	if err := insertPolicyResources(ctx, tx, organizationID, policyID, input.Resources); err != nil {
		return nil, err
	}
	if err := addPolicyAudit(ctx, tx, organizationID, actorID, "policy.updated", policyID, map[string]any{"name": input.Name, "type": input.Type, "status": input.Status}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.OrganizationPolicy(ctx, organizationID, policyID)
}

func (s *Store) DeleteOrganizationPolicy(ctx context.Context, workspaceID, actorID, policyID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	organizationID, err := policyOrganizationForWorkspace(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	var name string
	err = tx.QueryRow(ctx, `DELETE FROM organization_policies WHERE id::text=$1 AND organization_id::text=$2 RETURNING name`, policyID, organizationID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAdminNotFound
	}
	if err != nil {
		return err
	}
	if err := addPolicyAudit(ctx, tx, organizationID, actorID, "policy.deleted", policyID, map[string]any{"name": name}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AddOrganizationPolicyResource(ctx context.Context, workspaceID, actorID, policyID string, resource PolicyResourceInput) (*models.OrganizationPolicy, error) {
	resource.ID = strings.TrimSpace(resource.ID)
	if resource.ID == "" {
		return nil, fmt.Errorf("%w: resource id is required", ErrAdminValidation)
	}
	if resource.Meta == nil {
		resource.Meta = map[string]any{}
	}
	if resource.Links == nil {
		resource.Links = map[string]any{}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	organizationID, err := policyOrganizationForWorkspace(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM organization_policies WHERE id::text=$1 AND organization_id::text=$2)`, policyID, organizationID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrAdminNotFound
	}
	if err := validatePolicyResource(ctx, tx, organizationID, resource.ID); err != nil {
		return nil, err
	}
	meta, err := json.Marshal(resource.Meta)
	if err != nil {
		return nil, err
	}
	links, err := json.Marshal(resource.Links)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO organization_policy_resources(policy_id,resource_id,meta,links) VALUES($1::uuid,$2,$3,$4)`, policyID, resource.ID, meta, links)
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: resource is already attached to policy", ErrAdminConflict)
	}
	if err != nil {
		return nil, err
	}
	if err := addPolicyAudit(ctx, tx, organizationID, actorID, "policy.resource.added", policyID, map[string]any{"resourceId": resource.ID}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.OrganizationPolicy(ctx, organizationID, policyID)
}

func (s *Store) UpdateOrganizationPolicyResource(ctx context.Context, workspaceID, actorID, policyID, resourceID string, resource PolicyResourceInput) (*models.OrganizationPolicy, error) {
	if resource.Meta == nil {
		resource.Meta = map[string]any{}
	}
	if resource.Links == nil {
		resource.Links = map[string]any{}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	organizationID, err := policyOrganizationForWorkspace(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	meta, err := json.Marshal(resource.Meta)
	if err != nil {
		return nil, err
	}
	links, err := json.Marshal(resource.Links)
	if err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE organization_policy_resources pr SET meta=$4,links=$5,updated_at=now()
		FROM organization_policies p
		WHERE pr.policy_id=p.id AND p.id::text=$1 AND p.organization_id::text=$2 AND pr.resource_id=$3`,
		policyID, organizationID, resourceID, meta, links)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrAdminNotFound
	}
	if err := addPolicyAudit(ctx, tx, organizationID, actorID, "policy.resource.updated", policyID, map[string]any{"resourceId": resourceID}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.OrganizationPolicy(ctx, organizationID, policyID)
}

func (s *Store) DeleteOrganizationPolicyResource(ctx context.Context, workspaceID, actorID, policyID, resourceID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	organizationID, err := policyOrganizationForWorkspace(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		DELETE FROM organization_policy_resources pr USING organization_policies p
		WHERE pr.policy_id=p.id AND p.id::text=$1 AND p.organization_id::text=$2 AND pr.resource_id=$3`, policyID, organizationID, resourceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAdminNotFound
	}
	if err := addPolicyAudit(ctx, tx, organizationID, actorID, "policy.resource.removed", policyID, map[string]any{"resourceId": resourceID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
