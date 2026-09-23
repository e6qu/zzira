package store

import (
	"context"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
)

// A customer raising a request says who it is for: themselves, or one of the
// organizations they belong to that this desk serves. Everybody in an
// organization it is shared with reads it and its public replies, which is
// what a shared request is for.

// ServiceRequestOrganizations are the organizations a request is shared with.
func (s *Store) ServiceRequestOrganizations(ctx context.Context, requestIssueID string) ([]models.ServiceOrganization, error) {
	rows, err := s.Pool.Query(ctx, `SELECT o.id,o.name FROM service_request_organizations ro
		JOIN service_organizations o ON o.id=ro.organization_id
		WHERE ro.request_issue_id=$1 ORDER BY lower(o.name),o.id`, requestIssueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	organizations := []models.ServiceOrganization{}
	for rows.Next() {
		var organization models.ServiceOrganization
		if err := rows.Scan(&organization.ID, &organization.Name); err != nil {
			return nil, err
		}
		organizations = append(organizations, organization)
	}
	return organizations, rows.Err()
}

// ShareServiceRequest says who a request is for. An empty list keeps it to the
// person who raised it. An organization the desk does not serve, or one the
// customer does not belong to, is refused: a customer can only share what they
// are part of.
func (s *Store) ShareServiceRequest(ctx context.Context, workspaceID, requestIssueID string, organizationIDs []string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var deskID, customerID string
	if err := tx.QueryRow(ctx, `SELECT service_desk_id,customer_id FROM service_requests WHERE workspace_id=$1 AND issue_id=$2 FOR UPDATE`,
		workspaceID, requestIssueID).Scan(&deskID, &customerID); err != nil {
		return fmt.Errorf("that request does not exist")
	}
	for _, organizationID := range organizationIDs {
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM service_desk_organizations dso
			JOIN service_organization_users sou ON sou.organization_id=dso.organization_id AND sou.user_id=$3
			WHERE dso.service_desk_id=$1 AND dso.organization_id=$2)`, deskID, organizationID, customerID).Scan(&allowed); err != nil {
			return err
		}
		if !allowed {
			return fmt.Errorf("a request is shared with an organization the customer belongs to and the desk serves")
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM service_request_organizations WHERE request_issue_id=$1`, requestIssueID); err != nil {
		return err
	}
	for _, organizationID := range organizationIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO service_request_organizations(request_issue_id,organization_id) VALUES($1,$2)
			ON CONFLICT DO NOTHING`, requestIssueID, organizationID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ServiceRequestShareOptions are the organizations one customer may share a
// request with at this desk: the ones they belong to that it serves.
func (s *Store) ServiceRequestShareOptions(ctx context.Context, deskID, customerID string) ([]models.ServiceOrganization, error) {
	rows, err := s.Pool.Query(ctx, `SELECT o.id,o.name FROM service_desk_organizations dso
		JOIN service_organizations o ON o.id=dso.organization_id
		JOIN service_organization_users sou ON sou.organization_id=o.id AND sou.user_id=$2
		WHERE dso.service_desk_id=$1 ORDER BY lower(o.name),o.id`, deskID, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	organizations := []models.ServiceOrganization{}
	for rows.Next() {
		var organization models.ServiceOrganization
		if err := rows.Scan(&organization.ID, &organization.Name); err != nil {
			return nil, err
		}
		organizations = append(organizations, organization)
	}
	return organizations, rows.Err()
}
