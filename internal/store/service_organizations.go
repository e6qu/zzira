package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

func scanServiceOrganization(row interface{ Scan(...any) error }) (*models.ServiceOrganization, error) {
	organization := &models.ServiceOrganization{}
	err := row.Scan(&organization.ID, &organization.WorkspaceID, &organization.Name, &organization.CreatedAt)
	return organization, err
}

const serviceOrganizationSelect = `SELECT id,workspace_id,name,created_at FROM service_organizations `

func (s *Store) ServiceOrganizations(ctx context.Context, workspaceID, viewerID, accountID string, agent bool) ([]models.ServiceOrganization, error) {
	rows, err := s.Pool.Query(ctx, serviceOrganizationSelect+`
		WHERE workspace_id=$1
		  AND ($4 OR EXISTS(SELECT 1 FROM service_organization_users u WHERE u.organization_id=service_organizations.id AND u.user_id=$2))
		  AND ($3='' OR EXISTS(SELECT 1 FROM service_organization_users u WHERE u.organization_id=service_organizations.id AND u.user_id=$3))
		ORDER BY lower(name),id::bigint`, workspaceID, viewerID, accountID, agent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	organizations := make([]models.ServiceOrganization, 0)
	for rows.Next() {
		organization, err := scanServiceOrganization(rows)
		if err != nil {
			return nil, err
		}
		organizations = append(organizations, *organization)
	}
	return organizations, rows.Err()
}

func (s *Store) ServiceOrganization(ctx context.Context, workspaceID, organizationID, viewerID string, agent bool) (*models.ServiceOrganization, error) {
	return scanServiceOrganization(s.Pool.QueryRow(ctx, serviceOrganizationSelect+`
		WHERE workspace_id=$1 AND id=$2
		  AND ($4 OR EXISTS(SELECT 1 FROM service_organization_users u WHERE u.organization_id=service_organizations.id AND u.user_id=$3))`, workspaceID, organizationID, viewerID, agent))
}

func (s *Store) CreateServiceOrganization(ctx context.Context, workspaceID, name string) (*models.ServiceOrganization, error) {
	return scanServiceOrganization(s.Pool.QueryRow(ctx, `
		INSERT INTO service_organizations(workspace_id,name) VALUES($1,$2)
		ON CONFLICT(workspace_id,lower(name)) DO UPDATE SET name=service_organizations.name
		RETURNING id,workspace_id,name,created_at`, workspaceID, name))
}

func (s *Store) DeleteServiceOrganization(ctx context.Context, workspaceID, organizationID string) error {
	result, err := s.Pool.Exec(ctx, `DELETE FROM service_organizations WHERE workspace_id=$1 AND id=$2`, workspaceID, organizationID)
	if err == nil && result.RowsAffected() == 0 {
		return fmt.Errorf("service organization does not exist")
	}
	return err
}

func (s *Store) ServiceOrganizationUsers(ctx context.Context, workspaceID, organizationID string) ([]*models.User, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT ou.user_id FROM service_organization_users ou
		JOIN service_organizations o ON o.id=ou.organization_id
		WHERE o.workspace_id=$1 AND o.id=$2 ORDER BY ou.user_id`, workspaceID, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	userIDs := make([]string, 0)
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	users := make([]*models.User, 0, len(userIDs))
	for _, userID := range userIDs {
		user, err := s.UserByID(ctx, userID)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, nil
}

func (s *Store) SetServiceOrganizationUsers(ctx context.Context, workspaceID, organizationID string, userIDs []string, add bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_organizations WHERE workspace_id=$1 AND id=$2)`, workspaceID, organizationID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("service organization does not exist")
	}
	for _, userID := range userIDs {
		if add {
			result, err := tx.Exec(ctx, `
				INSERT INTO service_organization_users(organization_id,user_id)
				SELECT $2,$3 WHERE EXISTS(SELECT 1 FROM service_customers WHERE workspace_id=$1 AND user_id=$3 AND active)
				ON CONFLICT DO NOTHING`, workspaceID, organizationID, userID)
			if err != nil {
				return err
			}
			if result.RowsAffected() == 0 {
				var customer bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_customers WHERE workspace_id=$1 AND user_id=$2 AND active)`, workspaceID, userID).Scan(&customer); err != nil {
					return err
				}
				if !customer {
					return fmt.Errorf("customer %q is not active", userID)
				}
			}
		} else if _, err := tx.Exec(ctx, `DELETE FROM service_organization_users WHERE organization_id=$1 AND user_id=$2`, organizationID, userID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) ServiceOrganizationPropertyKeys(ctx context.Context, workspaceID, organizationID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT p.key FROM service_organization_properties p JOIN service_organizations o ON o.id=p.organization_id
		WHERE o.workspace_id=$1 AND o.id=$2 ORDER BY p.key`, workspaceID, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *Store) ServiceOrganizationProperty(ctx context.Context, workspaceID, organizationID, key string) (json.RawMessage, error) {
	var value json.RawMessage
	err := s.Pool.QueryRow(ctx, `
		SELECT p.value FROM service_organization_properties p JOIN service_organizations o ON o.id=p.organization_id
		WHERE o.workspace_id=$1 AND o.id=$2 AND p.key=$3`, workspaceID, organizationID, key).Scan(&value)
	return value, err
}

func (s *Store) SetServiceOrganizationProperty(ctx context.Context, workspaceID, organizationID, key string, value json.RawMessage) error {
	result, err := s.Pool.Exec(ctx, `
		INSERT INTO service_organization_properties(organization_id,key,value)
		SELECT id,$3,$4 FROM service_organizations WHERE workspace_id=$1 AND id=$2
		ON CONFLICT(organization_id,key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, workspaceID, organizationID, key, value)
	if err == nil && result.RowsAffected() == 0 {
		return fmt.Errorf("service organization does not exist")
	}
	return err
}

func (s *Store) DeleteServiceOrganizationProperty(ctx context.Context, workspaceID, organizationID, key string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM service_organization_properties p USING service_organizations o WHERE p.organization_id=o.id AND o.workspace_id=$1 AND o.id=$2 AND p.key=$3`, workspaceID, organizationID, key)
	return err
}

func (s *Store) ServiceDeskOrganizations(ctx context.Context, workspaceID, serviceDeskID string) ([]models.ServiceOrganization, error) {
	rows, err := s.Pool.Query(ctx, `SELECT service_organizations.id,service_organizations.workspace_id,service_organizations.name,service_organizations.created_at FROM service_organizations
		JOIN service_desk_organizations dso ON dso.organization_id=service_organizations.id
		JOIN service_desks sd ON sd.id=dso.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 ORDER BY lower(service_organizations.name),service_organizations.id::bigint`, workspaceID, serviceDeskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	organizations := make([]models.ServiceOrganization, 0)
	for rows.Next() {
		organization, err := scanServiceOrganization(rows)
		if err != nil {
			return nil, err
		}
		organizations = append(organizations, *organization)
	}
	return organizations, rows.Err()
}

func (s *Store) SetServiceDeskOrganization(ctx context.Context, workspaceID, serviceDeskID, organizationID string, add bool) error {
	if add {
		result, err := s.Pool.Exec(ctx, `
			INSERT INTO service_desk_organizations(service_desk_id,organization_id)
			SELECT sd.id,o.id FROM service_desks sd JOIN service_organizations o ON o.workspace_id=sd.workspace_id
			WHERE sd.workspace_id=$1 AND sd.id=$2 AND o.id=$3 ON CONFLICT DO NOTHING`, workspaceID, serviceDeskID, organizationID)
		if err == nil && result.RowsAffected() == 0 {
			var valid bool
			if queryErr := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_desks sd JOIN service_organizations o ON o.workspace_id=sd.workspace_id WHERE sd.workspace_id=$1 AND sd.id=$2 AND o.id=$3)`, workspaceID, serviceDeskID, organizationID).Scan(&valid); queryErr != nil {
				return queryErr
			}
			if !valid {
				return fmt.Errorf("service desk or organization does not exist")
			}
		}
		return err
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM service_desk_organizations dso USING service_desks sd WHERE dso.service_desk_id=sd.id AND sd.workspace_id=$1 AND sd.id=$2 AND dso.organization_id=$3`, workspaceID, serviceDeskID, organizationID)
	return err
}

func (s *Store) ServiceDeskCustomers(ctx context.Context, workspaceID, serviceDeskID, query string) ([]*models.User, error) {
	query = strings.TrimSpace(query)
	rows, err := s.Pool.Query(ctx, `
		SELECT dc.user_id FROM service_desk_customers dc JOIN service_desks sd ON sd.id=dc.service_desk_id
		JOIN users u ON u.id=dc.user_id JOIN service_customers sc ON sc.workspace_id=sd.workspace_id AND sc.user_id=u.id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND dc.active AND sc.active
		  AND ($3='' OR u.display_name ILIKE '%'||$3||'%' OR u.email ILIKE '%'||$3||'%' OR u.id ILIKE '%'||$3||'%')
		ORDER BY lower(u.display_name),u.id`, workspaceID, serviceDeskID, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	userIDs := make([]string, 0)
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		userIDs = append(userIDs, userID)
	}
	users := make([]*models.User, 0, len(userIDs))
	for _, userID := range userIDs {
		user, err := s.UserByID(ctx, userID)
		if err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) SetServiceDeskCustomers(ctx context.Context, workspaceID, serviceDeskID string, userIDs []string, add bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var open bool
	if err := tx.QueryRow(ctx, `SELECT customer_access_open FROM service_desks WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, workspaceID, serviceDeskID).Scan(&open); err != nil {
		return err
	}
	if !add && open {
		return fmt.Errorf("customers can only be removed when portal access is closed")
	}
	for _, userID := range userIDs {
		var customer bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_customers WHERE workspace_id=$1 AND user_id=$2 AND active)`, workspaceID, userID).Scan(&customer); err != nil {
			return err
		}
		if !customer {
			return fmt.Errorf("customer %q is not active", userID)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO service_desk_customers(service_desk_id,user_id,active) VALUES($1,$2,$3)
			ON CONFLICT(service_desk_id,user_id) DO UPDATE SET active=EXCLUDED.active,added_at=CASE WHEN EXCLUDED.active THEN now() ELSE service_desk_customers.added_at END`, serviceDeskID, userID, add); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) SetServiceDeskCustomerAccess(ctx context.Context, workspaceID, serviceDeskID string, open bool) error {
	result, err := s.Pool.Exec(ctx, `UPDATE service_desks SET customer_access_open=$3 WHERE workspace_id=$1 AND id=$2`, workspaceID, serviceDeskID, open)
	if err == nil && result.RowsAffected() == 0 {
		return fmt.Errorf("service desk does not exist")
	}
	return err
}

func (s *Store) RevokePortalOnlyServiceCustomer(ctx context.Context, workspaceID, userID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var portalOnly bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM service_customers sc WHERE sc.workspace_id=$1 AND sc.user_id=$2 AND sc.active
		AND NOT EXISTS(
			SELECT 1 FROM role_bindings rb JOIN sites si ON rb.scope_type='site' AND rb.scope_id=si.id::text
			WHERE si.workspace_id=$1 AND rb.principal_type='user' AND rb.principal_id=$2 AND rb.role_key<>'atlassian/customer'
		))`, workspaceID, userID).Scan(&portalOnly); err != nil {
		return err
	}
	if !portalOnly {
		return fmt.Errorf("account is not an active portal-only customer")
	}
	if _, err := tx.Exec(ctx, `UPDATE service_customers SET active=FALSE,revoked_at=now() WHERE workspace_id=$1 AND user_id=$2`, workspaceID, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE service_desk_customers dc SET active=FALSE FROM service_desks sd WHERE dc.service_desk_id=sd.id AND sd.workspace_id=$1 AND dc.user_id=$2`, workspaceID, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM service_organization_users ou USING service_organizations o WHERE ou.organization_id=o.id AND o.workspace_id=$1 AND ou.user_id=$2`, workspaceID, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM role_bindings rb USING sites si WHERE rb.scope_type='site' AND rb.scope_id=si.id::text AND si.workspace_id=$1 AND rb.principal_type='user' AND rb.principal_id=$2 AND rb.role_key='atlassian/customer'`, workspaceID, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
