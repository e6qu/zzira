package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// ProductUserLimit is the most users a product's plan allows, zero meaning no
// limit: free plans cap Jira and Confluence at ten users and Jira Service
// Management at three agents.
func ProductUserLimit(product *models.Product) int {
	if product.Plan != "free" {
		return 0
	}
	if product.Key == "jira-service-management" {
		return 3
	}
	return 10
}

// OrganizationProducts lists the products on an organization's sites.
func (s *Store) OrganizationProducts(ctx context.Context, organizationID string) ([]*models.Product, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT p.id::text,p.site_id::text,p.product_key,p.name,p.enabled,p.plan
		FROM products p JOIN sites si ON si.id=p.site_id
		WHERE si.organization_id::text=$1 ORDER BY p.product_key,p.id`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	products := make([]*models.Product, 0)
	for rows.Next() {
		product := &models.Product{}
		if err := rows.Scan(&product.ID, &product.SiteID, &product.Key, &product.Name, &product.Enabled, &product.Plan); err != nil {
			return nil, err
		}
		products = append(products, product)
	}
	return products, rows.Err()
}

// ProductUserCount counts the users holding a role on a product, directly or
// through a group.
func (s *Store) ProductUserCount(ctx context.Context, productID string) (int, error) {
	var count int
	err := s.Pool.QueryRow(ctx, `
		SELECT count(DISTINCT user_id) FROM (
			SELECT principal_id AS user_id FROM role_bindings WHERE scope_type='product' AND scope_id=$1 AND principal_type='user'
			UNION
			SELECT gm.user_id FROM role_bindings rb JOIN group_members gm ON gm.group_id::text=rb.principal_id
			WHERE rb.scope_type='product' AND rb.scope_id=$1 AND rb.principal_type='group'
		) holders`, productID).Scan(&count)
	return count, err
}

// SetProductPlan changes the plan of a product on the workspace's site and
// audits the change.
func (s *Store) SetProductPlan(ctx context.Context, workspaceID, actorID, productID, plan string) error {
	switch plan {
	case "free", "standard", "premium", "enterprise":
	default:
		return fmt.Errorf("%w: plan must be free, standard, premium or enterprise", ErrAdminValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID, previous string
	err = tx.QueryRow(ctx, `
		SELECT si.organization_id::text,p.plan FROM products p JOIN sites si ON si.id=p.site_id
		WHERE si.workspace_id=$1 AND p.id::text=$2 FOR UPDATE OF p`, workspaceID, productID).Scan(&organizationID, &previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: product was not found", ErrAdminNotFound)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE products SET plan=$2 WHERE id::text=$1`, productID, plan); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,'product.plan.updated','product',$3,jsonb_build_object('from',$4::text,'to',$5::text))`, organizationID, actorID, productID, previous, plan); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
