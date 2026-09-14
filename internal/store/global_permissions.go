package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

var (
	ErrGlobalPermissionValidation = errors.New("global permission grant is invalid")
	ErrGlobalPermissionConflict   = errors.New("global permission grant already exists")
	ErrGlobalPermissionNotFound   = errors.New("global permission grant does not exist")
)

// GlobalPermissionGrant gives a global permission to a group or to everyone
// with access to a product.
type GlobalPermissionGrant struct {
	ID         int64
	Permission string
	GroupID    string
	GroupName  string
	ProductKey string
}

// globalPermissionGranted is the SQL deciding whether a grant of permission $3
// reaches the active member $2 of workspace $1. A product grant reaches every
// member while the site has the product enabled, or has no product records.
const globalPermissionGranted = `EXISTS(
	SELECT 1 FROM global_permission_grants g
	WHERE g.workspace_id=$1 AND g.permission_key=$3
	  AND EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND u.active)
	  AND ((g.group_id IS NOT NULL AND EXISTS(
	        SELECT 1 FROM group_members gm JOIN groups gr ON gr.id=gm.group_id JOIN directories d ON d.id=gr.directory_id
	        WHERE gm.group_id=g.group_id AND gm.user_id=$2 AND d.active))
	    OR (g.product_key IS NOT NULL AND (
	        EXISTS(SELECT 1 FROM products p JOIN sites si ON si.id=p.site_id WHERE si.workspace_id=$1 AND p.product_key=g.product_key AND p.enabled)
	        OR NOT EXISTS(SELECT 1 FROM products p JOIN sites si ON si.id=p.site_id WHERE si.workspace_id=$1)))))`

// GlobalPermissionGrants lists the site's global permission grants.
func (s *Store) GlobalPermissionGrants(ctx context.Context, workspaceID string) ([]GlobalPermissionGrant, error) {
	rows, err := s.Pool.Query(ctx, `SELECT g.id, g.permission_key, COALESCE(g.group_id::text,''), COALESCE(gr.name,''), COALESCE(g.product_key,'')
		FROM global_permission_grants g LEFT JOIN groups gr ON gr.id=g.group_id
		WHERE g.workspace_id=$1 ORDER BY g.permission_key, g.product_key NULLS LAST, lower(gr.name), g.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := []GlobalPermissionGrant{}
	for rows.Next() {
		var grant GlobalPermissionGrant
		if err = rows.Scan(&grant.ID, &grant.Permission, &grant.GroupID, &grant.GroupName, &grant.ProductKey); err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

// AddGlobalPermissionGrant grants a global permission, other than Administer
// Jira, to a site group or to everyone with a product.
func (s *Store) AddGlobalPermissionGrant(ctx context.Context, workspaceID, actorID, permission, groupID, productKey string) (GlobalPermissionGrant, error) {
	permission, groupID, productKey = strings.TrimSpace(permission), strings.TrimSpace(groupID), strings.TrimSpace(productKey)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return GlobalPermissionGrant{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return GlobalPermissionGrant{}, err
	}
	if definition, ok := PermissionDefinitionByKey(permission); ok {
		if definition.Type != "GLOBAL" || permission == "ADMINISTER" {
			return GlobalPermissionGrant{}, fmt.Errorf("%w: %s cannot be granted here", ErrGlobalPermissionValidation, permission)
		}
	} else if definition, _, found, lookupErr := appPermission(ctx, tx, workspaceID, permission); lookupErr != nil {
		return GlobalPermissionGrant{}, lookupErr
	} else if !found || definition.Type != "GLOBAL" {
		return GlobalPermissionGrant{}, fmt.Errorf("%w: %s is not a global permission", ErrGlobalPermissionValidation, permission)
	}
	grant := GlobalPermissionGrant{Permission: permission, ProductKey: productKey}
	switch {
	case (groupID == "") == (productKey == ""):
		return GlobalPermissionGrant{}, fmt.Errorf("%w: grant to exactly one group or product", ErrGlobalPermissionValidation)
	case productKey != "" && productKey != "jira-software" && productKey != "jira-service-management":
		return GlobalPermissionGrant{}, fmt.Errorf("%w: product %s is not a Jira product", ErrGlobalPermissionValidation, productKey)
	case groupID != "":
		err = tx.QueryRow(ctx, `SELECT g.id::text, g.name FROM groups g JOIN directories d ON d.id=g.directory_id JOIN sites si ON si.organization_id=d.organization_id
			WHERE si.workspace_id=$1 AND d.active AND g.id::text=$2`, workspaceID, groupID).Scan(&grant.GroupID, &grant.GroupName)
		if errors.Is(err, pgx.ErrNoRows) {
			return GlobalPermissionGrant{}, fmt.Errorf("%w: group does not exist", ErrGlobalPermissionValidation)
		}
		if err != nil {
			return GlobalPermissionGrant{}, err
		}
	}
	err = tx.QueryRow(ctx, `INSERT INTO global_permission_grants(workspace_id,permission_key,group_id,product_key)
		VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')) ON CONFLICT DO NOTHING RETURNING id`, workspaceID, permission, grant.GroupID, productKey).Scan(&grant.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return GlobalPermissionGrant{}, ErrGlobalPermissionConflict
	}
	if err != nil {
		return GlobalPermissionGrant{}, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "global_permission_grant", fmt.Sprint(grant.ID), models.OpUpsert, grant); err != nil {
		return GlobalPermissionGrant{}, err
	}
	return grant, tx.Commit(ctx)
}

// RemoveGlobalPermissionGrant removes one global permission grant.
func (s *Store) RemoveGlobalPermissionGrant(ctx context.Context, workspaceID, actorID string, grantID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `DELETE FROM global_permission_grants WHERE workspace_id=$1 AND id=$2`, workspaceID, grantID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrGlobalPermissionNotFound
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "global_permission_grant", fmt.Sprint(grantID), models.OpDelete, map[string]any{"id": grantID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
