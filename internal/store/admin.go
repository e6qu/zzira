package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/e6qu/zzira/internal/models"
)

var (
	ErrAdminValidation = errors.New("admin validation")
	ErrAdminConflict   = errors.New("admin conflict")
	ErrAdminNotFound   = errors.New("admin object not found")
)

func isUniqueViolation(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && databaseError.Code == "23505"
}

func formatAdminTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func scanOrganization(row pgx.Row) (*models.Organization, error) {
	organization := &models.Organization{}
	var createdAt, updatedAt time.Time
	if err := row.Scan(&organization.ID, &organization.Name, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	organization.CreatedAt = formatAdminTime(createdAt)
	organization.UpdatedAt = formatAdminTime(updatedAt)
	return organization, nil
}

func (s *Store) OrganizationByWorkspace(ctx context.Context, workspaceID string) (*models.Organization, error) {
	return scanOrganization(s.Pool.QueryRow(ctx, `
		SELECT o.id::text,o.name,o.created_at,o.updated_at
		FROM organizations o JOIN sites si ON si.organization_id=o.id
		WHERE si.workspace_id=$1`, workspaceID))
}

func (s *Store) OrganizationsForAdmin(ctx context.Context, userID string) ([]*models.Organization, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT o.id::text,o.name,o.created_at,o.updated_at
		FROM organizations o
		LEFT JOIN sites si ON si.organization_id=o.id
		WHERE EXISTS (
			SELECT 1 FROM role_bindings rb
			WHERE rb.role_key IN ('atlassian/org-admin','atlassian/site-admin')
			  AND ((rb.principal_type='user' AND rb.principal_id=$1)
			    OR (rb.principal_type='group' AND EXISTS (
			      SELECT 1 FROM group_members gm WHERE gm.group_id::text=rb.principal_id AND gm.user_id=$1)))
			  AND ((rb.scope_type='organization' AND rb.scope_id=o.id::text)
			    OR (rb.scope_type='site' AND rb.scope_id=si.id::text))
		)
		ORDER BY o.name,o.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	organizations := make([]*models.Organization, 0)
	for rows.Next() {
		organization, err := scanOrganization(rows)
		if err != nil {
			return nil, err
		}
		organizations = append(organizations, organization)
	}
	return organizations, rows.Err()
}

func (s *Store) OrganizationByIDForAdmin(ctx context.Context, organizationID, userID string) (*models.Organization, error) {
	return scanOrganization(s.Pool.QueryRow(ctx, `
		SELECT o.id::text,o.name,o.created_at,o.updated_at
		FROM organizations o
		WHERE o.id::text=$1 AND EXISTS (
			SELECT 1 FROM role_bindings rb
			LEFT JOIN sites si ON si.id::text=rb.scope_id AND rb.scope_type='site'
			WHERE rb.role_key IN ('atlassian/org-admin','atlassian/site-admin')
			  AND ((rb.principal_type='user' AND rb.principal_id=$2)
			    OR (rb.principal_type='group' AND EXISTS (
			      SELECT 1 FROM group_members gm WHERE gm.group_id::text=rb.principal_id AND gm.user_id=$2)))
			  AND ((rb.scope_type='organization' AND rb.scope_id=o.id::text)
			    OR si.organization_id=o.id)
		)`, organizationID, userID))
}

func (s *Store) SiteByWorkspace(ctx context.Context, workspaceID string) (*models.Site, error) {
	site := &models.Site{WorkspaceID: workspaceID}
	var createdAt time.Time
	err := s.Pool.QueryRow(ctx, `
		SELECT id::text,organization_id::text,slug,name,created_at
		FROM sites WHERE workspace_id=$1`, workspaceID).
		Scan(&site.ID, &site.OrganizationID, &site.Slug, &site.Name, &createdAt)
	if err != nil {
		return nil, err
	}
	site.CreatedAt = formatAdminTime(createdAt)
	return site, nil
}

func (s *Store) SitesByOrganization(ctx context.Context, organizationID string) ([]*models.Site, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id::text,organization_id::text,workspace_id,slug,name,created_at
		FROM sites WHERE organization_id::text=$1 ORDER BY name,id`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sites := make([]*models.Site, 0)
	for rows.Next() {
		site := &models.Site{}
		var createdAt time.Time
		if err := rows.Scan(&site.ID, &site.OrganizationID, &site.WorkspaceID, &site.Slug, &site.Name, &createdAt); err != nil {
			return nil, err
		}
		site.CreatedAt = formatAdminTime(createdAt)
		sites = append(sites, site)
	}
	return sites, rows.Err()
}

func (s *Store) ProductsBySite(ctx context.Context, siteID string) ([]*models.Product, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id::text,site_id::text,product_key,name,enabled,created_at
		FROM products WHERE site_id::text=$1 ORDER BY product_key`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	products := make([]*models.Product, 0)
	for rows.Next() {
		product := &models.Product{}
		var createdAt time.Time
		if err := rows.Scan(&product.ID, &product.SiteID, &product.Key, &product.Name, &product.Enabled, &createdAt); err != nil {
			return nil, err
		}
		product.CreatedAt = formatAdminTime(createdAt)
		products = append(products, product)
	}
	return products, rows.Err()
}

func (s *Store) RecordProductUserActivity(ctx context.Context, workspaceID, userID, productKey string) error {
	var recorded bool
	err := s.Pool.QueryRow(ctx, `
		WITH accessible_product AS (
		  SELECT p.id FROM products p
		  JOIN sites si ON si.id=p.site_id
		  JOIN users u ON u.id=$2 AND u.active
		  WHERE si.workspace_id=$1 AND p.product_key=$3 AND p.enabled
		    AND EXISTS (
		      SELECT 1 FROM directories d JOIN directory_users du ON du.directory_id=d.id
		      WHERE d.organization_id=si.organization_id AND d.active AND du.user_id=$2 AND du.active)
		    AND EXISTS (
		      SELECT 1 FROM role_bindings rb
		      WHERE rb.scope_type='product' AND rb.scope_id=p.id::text
		        AND rb.role_key IN ('atlassian/user','atlassian/admin','atlassian/guest','atlassian/customer',
		          'atlassian/contributor','atlassian/basic','atlassian/stakeholder',
		          'atlassian/product-user','atlassian/product-admin')
		        AND (rb.principal_type='user' AND rb.principal_id=$2 OR
		          rb.principal_type='group' AND EXISTS (
		            SELECT 1 FROM group_members gm WHERE gm.group_id::text=rb.principal_id AND gm.user_id=$2)))
		)
		INSERT INTO product_user_activity(user_id,product_id,last_active_at)
		SELECT $2,id,now() FROM accessible_product
		ON CONFLICT (user_id,product_id) DO UPDATE SET last_active_at=GREATEST(product_user_activity.last_active_at,excluded.last_active_at)
		RETURNING true`, workspaceID, userID, productKey).Scan(&recorded)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAdminNotFound
	}
	return err
}

func (s *Store) ProductUserActivities(ctx context.Context, organizationID, userID string) (string, []*models.ProductUserActivity, error) {
	var addedAt time.Time
	if err := s.Pool.QueryRow(ctx, `
		SELECT min(du.added_at) FROM directory_users du
		JOIN directories d ON d.id=du.directory_id
		WHERE d.organization_id=$1::uuid AND du.user_id=$2
		HAVING count(*) > 0`, organizationID, userID).Scan(&addedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil, ErrAdminNotFound
		}
		return "", nil, err
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT p.id::text,p.product_key,pua.last_active_at
		FROM product_user_activity pua
		JOIN products p ON p.id=pua.product_id
		JOIN sites si ON si.id=p.site_id
		WHERE si.organization_id=$1::uuid AND pua.user_id=$2
		ORDER BY pua.last_active_at DESC,p.id`, organizationID, userID)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	activities := make([]*models.ProductUserActivity, 0)
	for rows.Next() {
		activity := &models.ProductUserActivity{}
		var lastActiveAt time.Time
		if err := rows.Scan(&activity.ProductID, &activity.ProductKey, &lastActiveAt); err != nil {
			return "", nil, err
		}
		activity.LastActiveAt = formatAdminTime(lastActiveAt)
		activities = append(activities, activity)
	}
	return formatAdminTime(addedAt), activities, rows.Err()
}

func (s *Store) DirectoriesByOrganization(ctx context.Context, organizationID string) ([]*models.Directory, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id::text,organization_id::text,name,directory_type,active,created_at
		FROM directories WHERE organization_id::text=$1 ORDER BY name,id`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	directories := make([]*models.Directory, 0)
	for rows.Next() {
		directory := &models.Directory{}
		var createdAt time.Time
		if err := rows.Scan(&directory.ID, &directory.OrganizationID, &directory.Name, &directory.Type, &directory.Active, &createdAt); err != nil {
			return nil, err
		}
		directory.CreatedAt = formatAdminTime(createdAt)
		directories = append(directories, directory)
	}
	return directories, rows.Err()
}

func (s *Store) GroupsByDirectory(ctx context.Context, directoryID string) ([]*models.Group, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT g.id::text,g.directory_id::text,g.name,g.description,g.created_at,g.updated_at,
		       count(gm.user_id)::int
		FROM groups g LEFT JOIN group_members gm ON gm.group_id=g.id
		WHERE g.directory_id::text=$1
		GROUP BY g.id ORDER BY g.name,g.id`, directoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]*models.Group, 0)
	for rows.Next() {
		group := &models.Group{}
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&group.ID, &group.DirectoryID, &group.Name, &group.Description, &createdAt, &updatedAt, &group.MemberCount); err != nil {
			return nil, err
		}
		group.CreatedAt = formatAdminTime(createdAt)
		group.UpdatedAt = formatAdminTime(updatedAt)
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (s *Store) DirectoryGroup(ctx context.Context, directoryID, groupID string) (*models.Group, error) {
	group := &models.Group{}
	var createdAt, updatedAt time.Time
	err := s.Pool.QueryRow(ctx, `
		SELECT g.id::text,g.directory_id::text,g.name,g.description,g.created_at,g.updated_at,
		       count(gm.user_id)::int
		FROM groups g LEFT JOIN group_members gm ON gm.group_id=g.id
		WHERE g.directory_id::text=$1 AND g.id::text=$2
		GROUP BY g.id`, directoryID, groupID).
		Scan(&group.ID, &group.DirectoryID, &group.Name, &group.Description, &createdAt, &updatedAt, &group.MemberCount)
	if err != nil {
		return nil, err
	}
	group.CreatedAt = formatAdminTime(createdAt)
	group.UpdatedAt = formatAdminTime(updatedAt)
	return group, nil
}

func (s *Store) DirectoryUsers(ctx context.Context, directoryID string) ([]*models.User, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT u.id,u.email,u.display_name,u.time_zone,(du.active AND u.active),u.active,
		       du.added_at,du.suspended_at,u.deactivated_at,du.management_source,
		       u.nickname,u.job_title,u.department,u.organization_name,u.location,
		       u.picture_url,u.avatar_url,u.email_verified,u.mfa_enabled
		FROM directory_users du JOIN users u ON u.id=du.user_id
		WHERE du.directory_id::text=$1 ORDER BY u.display_name,u.id`, directoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]*models.User, 0)
	for rows.Next() {
		user := &models.User{AccountType: "atlassian"}
		var addedAt time.Time
		var suspendedAt, deactivatedAt *time.Time
		if err := rows.Scan(&user.ID, &user.Email, &user.DisplayName, &user.TimeZone, &user.Active, &user.AccountActive,
			&addedAt, &suspendedAt, &deactivatedAt, &user.ManagementSource,
			&user.Nickname, &user.JobTitle, &user.Department, &user.OrganizationName, &user.Location,
			&user.PictureURL, &user.AvatarURL, &user.EmailVerified, &user.MFAEnabled); err != nil {
			return nil, err
		}
		user.AddedAt = formatAdminTime(addedAt)
		if suspendedAt != nil {
			user.SuspendedAt = formatAdminTime(*suspendedAt)
		}
		if deactivatedAt != nil {
			user.DeactivatedAt = formatAdminTime(*deactivatedAt)
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (s *Store) GroupMemberIDs(ctx context.Context, groupID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT user_id FROM group_members WHERE group_id::text=$1 ORDER BY user_id`, groupID)
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
	return userIDs, rows.Err()
}

func (s *Store) RolesForUserInWorkspace(ctx context.Context, workspaceID, userID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH context AS (
			SELECT si.id AS site_id,si.organization_id FROM sites si WHERE si.workspace_id=$1
		), principals AS (
			SELECT 'user'::text AS principal_type,$2::text AS principal_id
			UNION ALL
			SELECT 'group',gm.group_id::text FROM group_members gm WHERE gm.user_id=$2
		)
		SELECT DISTINCT rb.role_key
		FROM role_bindings rb
		JOIN principals pr ON pr.principal_type=rb.principal_type AND pr.principal_id=rb.principal_id
		JOIN users u ON u.id=$2 AND u.active
		CROSS JOIN context c
		WHERE EXISTS (
		  SELECT 1 FROM directory_users du JOIN directories d ON d.id=du.directory_id
		  WHERE du.user_id=$2 AND du.active AND d.active AND d.organization_id=c.organization_id
		) AND ((rb.scope_type='organization' AND rb.scope_id=c.organization_id::text)
		   OR (rb.scope_type='site' AND rb.scope_id=c.site_id::text)
		   OR (rb.scope_type='product' AND rb.scope_id IN (
		     SELECT p.id::text FROM products p WHERE p.site_id=c.site_id AND p.enabled)))
		ORDER BY rb.role_key`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := make([]string, 0)
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

// RoleBindingsForPrincipal returns the direct assignments on resources owned
// by the workspace organization. For users, group-derived assignments are
// included and labeled group_direct so effective access is visible.
func (s *Store) RoleBindingsForPrincipal(ctx context.Context, workspaceID, principalType, principalID string) ([]*models.RoleBinding, error) {
	if principalType != "user" && principalType != "group" {
		return nil, fmt.Errorf("%w: principal type must be user or group", ErrAdminValidation)
	}
	rows, err := s.Pool.Query(ctx, `
		WITH context AS (
			SELECT si.id AS site_id,si.organization_id
			FROM sites si WHERE si.workspace_id=$1
		), principals AS (
			SELECT $2::text AS principal_type,$3::text AS principal_id,'direct'::text AS assignment
			UNION ALL
			SELECT 'group',gm.group_id::text,'group_direct'
			FROM group_members gm WHERE $2='user' AND gm.user_id=$3
		)
		SELECT rb.id,rb.scope_type,rb.scope_id,rb.role_key,rb.principal_type,
		       rb.principal_id,rb.source,rb.created_at,pr.assignment
		FROM role_bindings rb
		JOIN principals pr ON pr.principal_type=rb.principal_type AND pr.principal_id=rb.principal_id
		CROSS JOIN context c
		WHERE (rb.scope_type='organization' AND rb.scope_id=c.organization_id::text)
		   OR (rb.scope_type='site' AND rb.scope_id=c.site_id::text)
		   OR (rb.scope_type='product' AND rb.scope_id IN (
		     SELECT p.id::text FROM products p WHERE p.site_id=c.site_id))
		ORDER BY rb.scope_type,rb.scope_id,rb.role_key,pr.assignment`, workspaceID, principalType, principalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bindings := make([]*models.RoleBinding, 0)
	for rows.Next() {
		binding := &models.RoleBinding{}
		var createdAt time.Time
		if err := rows.Scan(&binding.ID, &binding.ScopeType, &binding.ScopeID, &binding.RoleKey,
			&binding.PrincipalType, &binding.PrincipalID, &binding.Source, &createdAt, &binding.Assignment); err != nil {
			return nil, err
		}
		binding.CreatedAt = formatAdminTime(createdAt)
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

// SetRoleBinding changes one direct role assignment and records the mutation
// in the organization audit log atomically. The scope and principal must both
// belong to the workspace organization.
func (s *Store) SetRoleBinding(ctx context.Context, workspaceID, actorID, principalType, principalID, scopeType, scopeID, roleKey string, assign bool) error {
	if principalType != "user" && principalType != "group" {
		return fmt.Errorf("%w: principal type must be user or group", ErrAdminValidation)
	}
	if scopeType != "organization" && scopeType != "site" && scopeType != "product" {
		return fmt.Errorf("%w: unsupported role scope", ErrAdminValidation)
	}
	if strings.TrimSpace(roleKey) == "" {
		return fmt.Errorf("%w: role is required", ErrAdminValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID string
	if err := tx.QueryRow(ctx, `SELECT organization_id::text FROM sites WHERE workspace_id=$1`, workspaceID).Scan(&organizationID); err != nil {
		return err
	}
	var principalExists, scopeExists bool
	if principalType == "user" {
		err = tx.QueryRow(ctx, `
			SELECT EXISTS(
			  SELECT 1 FROM directory_users du JOIN directories d ON d.id=du.directory_id
			  WHERE d.organization_id=$1::uuid AND du.user_id=$2
			)`, organizationID, principalID).Scan(&principalExists)
	} else {
		err = tx.QueryRow(ctx, `
			SELECT EXISTS(
			  SELECT 1 FROM groups g JOIN directories d ON d.id=g.directory_id
			  WHERE d.organization_id=$1::uuid AND g.id::text=$2
			)`, organizationID, principalID).Scan(&principalExists)
	}
	if err != nil {
		return err
	}
	if !principalExists {
		return fmt.Errorf("%w: principal was not found in this organization", ErrAdminNotFound)
	}
	switch scopeType {
	case "organization":
		scopeExists = scopeID == organizationID
	case "site":
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sites WHERE workspace_id=$1 AND id::text=$2)`, workspaceID, scopeID).Scan(&scopeExists)
	case "product":
		err = tx.QueryRow(ctx, `
			SELECT EXISTS(
			  SELECT 1 FROM products p JOIN sites si ON si.id=p.site_id
			  WHERE si.workspace_id=$1 AND p.id::text=$2 AND p.enabled
			)`, workspaceID, scopeID).Scan(&scopeExists)
	}
	if err != nil {
		return err
	}
	if !scopeExists {
		return fmt.Errorf("%w: role resource was not found", ErrAdminNotFound)
	}

	changed := false
	if assign {
		tag, err := tx.Exec(ctx, `
			INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source)
			VALUES($1,$2,$3,$4,$5,'manual') ON CONFLICT DO NOTHING`, scopeType, scopeID, roleKey, principalType, principalID)
		if err != nil {
			return err
		}
		changed = tag.RowsAffected() > 0
	} else {
		tag, err := tx.Exec(ctx, `
			DELETE FROM role_bindings
			WHERE scope_type=$1 AND scope_id=$2 AND role_key=$3
			  AND principal_type=$4 AND principal_id=$5`, scopeType, scopeID, roleKey, principalType, principalID)
		if err != nil {
			return err
		}
		changed = tag.RowsAffected() > 0
	}
	if !changed {
		return nil
	}
	detail, err := json.Marshal(map[string]any{
		"principalType": principalType,
		"principalId":   principalID,
		"scopeType":     scopeType,
		"scopeId":       scopeID,
		"role":          roleKey,
	})
	if err != nil {
		return err
	}
	action := "role.revoked"
	if assign {
		action = "role.assigned"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,$3,$4,$5,$6)`, organizationID, actorID, action, principalType, principalID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetDirectoryUserActive(ctx context.Context, workspaceID, actorID, directoryID, userID string, active bool) error {
	if actorID == userID && !active {
		return fmt.Errorf("%w: you cannot suspend your own account", ErrAdminValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID string
	err = tx.QueryRow(ctx, `
		SELECT d.organization_id::text FROM directory_users du
		JOIN directories d ON d.id=du.directory_id
		JOIN sites si ON si.organization_id=d.organization_id
		WHERE du.directory_id::text=$1 AND du.user_id=$2 AND si.workspace_id=$3`, directoryID, userID, workspaceID).Scan(&organizationID)
	if err != nil {
		return err
	}
	if active {
		var accountActive bool
		if err := tx.QueryRow(ctx, `SELECT active FROM users WHERE id=$1`, userID).Scan(&accountActive); err != nil {
			return err
		}
		if !accountActive {
			return fmt.Errorf("%w: a deactivated account cannot be restored to a directory", ErrAdminConflict)
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE directory_users SET active=$1,suspended_at=CASE WHEN $1 THEN NULL ELSE now() END
		WHERE directory_id=$2::uuid AND user_id=$3 AND active<>$1`, active, directoryID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: directory membership is already in the requested state", ErrAdminConflict)
	}
	var hasActiveDirectory bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM directory_users WHERE user_id=$1 AND active)`, userID).Scan(&hasActiveDirectory); err != nil {
		return err
	}
	if !hasActiveDirectory {
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM api_tokens WHERE user_id=$1`, userID); err != nil {
			return err
		}
	}
	action := "user.suspended"
	if active {
		action = "user.restored"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id)
		VALUES($1::uuid,$2,$3,'user',$4)`, organizationID, actorID, action, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type ManagedProfileUpdate struct {
	DisplayName      string
	Nickname         string
	JobTitle         string
	Department       string
	OrganizationName string
	Location         string
	TimeZone         string
}

func (s *Store) UpdateDirectoryUserProfile(ctx context.Context, workspaceID, actorID, directoryID, userID string, update ManagedProfileUpdate) error {
	update.DisplayName = strings.TrimSpace(update.DisplayName)
	update.Nickname = strings.TrimSpace(update.Nickname)
	update.JobTitle = strings.TrimSpace(update.JobTitle)
	update.Department = strings.TrimSpace(update.Department)
	update.OrganizationName = strings.TrimSpace(update.OrganizationName)
	update.Location = strings.TrimSpace(update.Location)
	update.TimeZone = strings.TrimSpace(update.TimeZone)
	if update.DisplayName == "" || len(update.DisplayName) > 255 || len(update.Nickname) > 255 ||
		len(update.JobTitle) > 255 || len(update.Department) > 255 || len(update.OrganizationName) > 255 ||
		len(update.Location) > 255 || len(update.TimeZone) > 100 {
		return fmt.Errorf("%w: managed profile fields exceed their limits or the display name is empty", ErrAdminValidation)
	}
	if update.TimeZone == "" {
		update.TimeZone = "UTC"
	}
	if _, err := time.LoadLocation(update.TimeZone); err != nil {
		return fmt.Errorf("%w: time zone must be an IANA location", ErrAdminValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID string
	if err := tx.QueryRow(ctx, `
		SELECT d.organization_id::text FROM directory_users du
		JOIN directories d ON d.id=du.directory_id
		JOIN sites si ON si.organization_id=d.organization_id
		WHERE du.directory_id::text=$1 AND du.user_id=$2 AND si.workspace_id=$3`, directoryID, userID, workspaceID).Scan(&organizationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET display_name=$2,nickname=$3,job_title=$4,department=$5,
		 organization_name=$6,location=$7,time_zone=$8 WHERE id=$1`, userID,
		update.DisplayName, update.Nickname, update.JobTitle, update.Department,
		update.OrganizationName, update.Location, update.TimeZone); err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]any{"directoryId": directoryID, "fields": []string{"name", "nickname", "jobTitle", "department", "organization", "location", "timeZone"}})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,'user.profile.updated','user',$3,$4)`, organizationID, actorID, userID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RemoveDirectoryUser(ctx context.Context, workspaceID, actorID, directoryID, userID string) error {
	if actorID == userID {
		return fmt.Errorf("%w: you cannot remove your own account", ErrAdminValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID string
	err = tx.QueryRow(ctx, `
		SELECT d.organization_id::text FROM directory_users du
		JOIN directories d ON d.id=du.directory_id
		JOIN sites si ON si.organization_id=d.organization_id
		WHERE du.directory_id::text=$1 AND du.user_id=$2 AND si.workspace_id=$3`, directoryID, userID, workspaceID).Scan(&organizationID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM memberships WHERE workspace_id=$1 AND user_id=$2`, workspaceID, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM group_members gm USING groups g WHERE gm.group_id=g.id AND g.directory_id=$1::uuid AND gm.user_id=$2`, directoryID, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM role_bindings rb
		WHERE rb.principal_type='user' AND rb.principal_id=$1
		  AND (rb.scope_id=$2
		    OR rb.scope_id IN (SELECT id::text FROM sites WHERE organization_id=$2::uuid)
		    OR rb.scope_id IN (SELECT p.id::text FROM products p JOIN sites si ON si.id=p.site_id WHERE si.organization_id=$2::uuid))`, userID, organizationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM directory_users WHERE directory_id=$1::uuid AND user_id=$2`, directoryID, userID); err != nil {
		return err
	}
	var hasActiveDirectory bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM directory_users WHERE user_id=$1 AND active)`, userID).Scan(&hasActiveDirectory); err != nil {
		return err
	}
	if !hasActiveDirectory {
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM api_tokens WHERE user_id=$1`, userID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id)
		VALUES($1::uuid,$2,'user.removed','user',$3)`, organizationID, actorID, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type InviteRoleAssignment struct {
	ScopeType string
	ScopeID   string
	Role      string
	Resource  string
}

type InviteOptions struct {
	GroupIDs     []string
	Roles        []InviteRoleAssignment
	EmailSubject string
	EmailBody    string
}

func (s *Store) InviteDirectoryUser(ctx context.Context, workspaceID, actorID, directoryID, email, displayName, passwordHash string) (*models.User, error) {
	return s.InviteDirectoryUserWithAccess(ctx, workspaceID, actorID, directoryID, email, displayName, passwordHash, InviteOptions{})
}

// InviteDirectoryUserWithAccess provisions the directory membership, group
// memberships, product roles, optional email delivery, and audit evidence in
// one transaction so a successful invitation is never only partly applied.
func (s *Store) InviteDirectoryUserWithAccess(ctx context.Context, workspaceID, actorID, directoryID, email, displayName, passwordHash string, options InviteOptions) (*models.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	displayName = strings.TrimSpace(displayName)
	if email == "" || !strings.Contains(email, "@") || displayName == "" {
		return nil, fmt.Errorf("%w: valid email and display name are required", ErrAdminValidation)
	}
	if (options.EmailSubject == "") != (strings.TrimSpace(options.EmailBody) == "") {
		return nil, fmt.Errorf("%w: invitation email subject and body must be provided together", ErrAdminValidation)
	}
	seenGroups := map[string]bool{}
	for _, groupID := range options.GroupIDs {
		if strings.TrimSpace(groupID) == "" || seenGroups[groupID] {
			return nil, fmt.Errorf("%w: invitation groups must be unique and non-empty", ErrAdminValidation)
		}
		seenGroups[groupID] = true
	}
	seenRoles := map[string]bool{}
	for _, assignment := range options.Roles {
		key := assignment.ScopeType + "\x00" + assignment.ScopeID + "\x00" + assignment.Role
		if strings.TrimSpace(assignment.ScopeType) == "" || strings.TrimSpace(assignment.ScopeID) == "" || strings.TrimSpace(assignment.Role) == "" || seenRoles[key] {
			return nil, fmt.Errorf("%w: invitation roles must be unique and complete", ErrAdminValidation)
		}
		seenRoles[key] = true
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID string
	if err := tx.QueryRow(ctx, `
		SELECT d.organization_id::text FROM directories d JOIN sites si ON si.organization_id=d.organization_id
		WHERE d.id::text=$1 AND si.workspace_id=$2 AND d.active`, directoryID, workspaceID).Scan(&organizationID); err != nil {
		return nil, err
	}
	user := &models.User{Email: email, DisplayName: displayName, Active: true, AccountType: "atlassian"}
	err = tx.QueryRow(ctx, `SELECT id,display_name,active FROM users WHERE lower(email)=lower($1)`, email).Scan(&user.ID, &user.DisplayName, &user.Active)
	if errors.Is(err, pgx.ErrNoRows) {
		user.ID = NewID("usr")
		if _, err := tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,$3,$4)`, user.ID, email, passwordHash, displayName); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if !user.Active {
		return nil, fmt.Errorf("%w: this account is suspended", ErrAdminConflict)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO directory_users(directory_id,user_id) VALUES($1::uuid,$2) ON CONFLICT DO NOTHING`, directoryID, user.ID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: this account is already in the directory", ErrAdminConflict)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member') ON CONFLICT DO NOTHING`, workspaceID, user.ID); err != nil {
		return nil, err
	}
	for _, groupID := range options.GroupIDs {
		tag, err := tx.Exec(ctx, `
			INSERT INTO group_members(group_id,user_id)
			SELECT g.id,$2 FROM groups g WHERE g.id::text=$1 AND g.directory_id::text=$3
			ON CONFLICT DO NOTHING`, groupID, user.ID, directoryID)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, fmt.Errorf("%w: invitation group was not found in the directory", ErrAdminNotFound)
		}
	}
	for _, assignment := range options.Roles {
		valid := false
		switch assignment.ScopeType {
		case "organization":
			valid = assignment.ScopeID == organizationID
		case "site":
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sites WHERE id::text=$1 AND workspace_id=$2 AND organization_id::text=$3)`, assignment.ScopeID, workspaceID, organizationID).Scan(&valid); err != nil {
				return nil, err
			}
		case "product":
			if err := tx.QueryRow(ctx, `
				SELECT EXISTS(SELECT 1 FROM products p JOIN sites si ON si.id=p.site_id
				WHERE p.id::text=$1 AND si.workspace_id=$2 AND si.organization_id::text=$3 AND p.enabled)`, assignment.ScopeID, workspaceID, organizationID).Scan(&valid); err != nil {
				return nil, err
			}
		}
		if !valid || strings.TrimSpace(assignment.Role) == "" {
			return nil, fmt.Errorf("%w: invitation role resource is invalid", ErrAdminNotFound)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id,source)
			VALUES($1,$2,$3,'user',$4,'manual') ON CONFLICT DO NOTHING`, assignment.ScopeType, assignment.ScopeID, assignment.Role, user.ID); err != nil {
			return nil, err
		}
	}
	if options.EmailSubject != "" {
		if strings.TrimSpace(options.EmailBody) == "" {
			return nil, fmt.Errorf("%w: invitation email body is required", ErrAdminValidation)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO email_outbox(workspace_id,recipient,subject,body)
			VALUES($1,$2,$3,$4)`, workspaceID, email, options.EmailSubject, options.EmailBody); err != nil {
			return nil, err
		}
	}
	detail, err := json.Marshal(map[string]any{
		"email": email, "groups": options.GroupIDs, "roles": options.Roles,
		"notificationQueued": options.EmailSubject != "",
	})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,'user.invited','user',$3,$4)`, organizationID, actorID, user.ID, detail); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return user, nil
}

func (s *Store) CreateDirectoryGroup(ctx context.Context, workspaceID, actorID, directoryID, name, description string) (*models.Group, error) {
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	if name == "" || len(name) > 255 || len(description) > 1000 {
		return nil, fmt.Errorf("%w: group name is required and fields must fit their limits", ErrAdminValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID string
	if err := tx.QueryRow(ctx, `
		SELECT d.organization_id::text FROM directories d
		JOIN sites si ON si.organization_id=d.organization_id
		WHERE d.id::text=$1 AND si.workspace_id=$2 AND d.active`, directoryID, workspaceID).
		Scan(&organizationID); err != nil {
		return nil, err
	}
	group := &models.Group{DirectoryID: directoryID, Name: name, Description: description}
	var createdAt, updatedAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO groups(directory_id,name,description) VALUES($1::uuid,$2,$3)
		RETURNING id::text,created_at,updated_at`, directoryID, name, description).
		Scan(&group.ID, &createdAt, &updatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: a group with this name already exists", ErrAdminConflict)
		}
		return nil, err
	}
	detail, err := json.Marshal(map[string]any{"name": name, "directoryId": directoryID})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,'group.created','group',$3,$4)`, organizationID, actorID, group.ID, detail); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	group.CreatedAt = formatAdminTime(createdAt)
	group.UpdatedAt = formatAdminTime(updatedAt)
	return group, nil
}

// DeleteDirectoryGroup removes memberships and direct role grants with the
// group, and records the administrative change in the same transaction.
func (s *Store) DeleteDirectoryGroup(ctx context.Context, workspaceID, actorID, directoryID, groupID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID, name string
	if err := tx.QueryRow(ctx, `
		SELECT d.organization_id::text,g.name
		FROM groups g
		JOIN directories d ON d.id=g.directory_id
		JOIN sites si ON si.organization_id=d.organization_id
		WHERE d.id::text=$1 AND g.id::text=$2 AND si.workspace_id=$3`, directoryID, groupID, workspaceID).
		Scan(&organizationID, &name); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM role_bindings WHERE principal_type='group' AND principal_id=$1`, groupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM groups WHERE id::text=$1 AND directory_id::text=$2`, groupID, directoryID); err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]any{"name": name, "directoryId": directoryID})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,'group.deleted','group',$3,$4)`, organizationID, actorID, groupID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetGroupMember(ctx context.Context, workspaceID, actorID, directoryID, groupID, userID string, member bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID string
	var valid bool
	if err := tx.QueryRow(ctx, `
		SELECT d.organization_id::text,
		  EXISTS(SELECT 1 FROM directory_users du WHERE du.directory_id=d.id AND du.user_id=$4)
		FROM directories d
		JOIN sites si ON si.organization_id=d.organization_id
		JOIN groups g ON g.directory_id=d.id
		WHERE d.id::text=$1 AND g.id::text=$2 AND si.workspace_id=$3`, directoryID, groupID, workspaceID, userID).
		Scan(&organizationID, &valid); err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("%w: user is not in this directory", ErrAdminValidation)
	}
	action := "group.member.added"
	if member {
		tag, err := tx.Exec(ctx, `INSERT INTO group_members(group_id,user_id) VALUES($1::uuid,$2) ON CONFLICT DO NOTHING`, groupID, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: user is already a group member", ErrAdminConflict)
		}
	} else {
		action = "group.member.removed"
		tag, err := tx.Exec(ctx, `DELETE FROM group_members WHERE group_id=$1::uuid AND user_id=$2`, groupID, userID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: group membership does not exist", ErrAdminNotFound)
		}
	}
	detail, err := json.Marshal(map[string]any{"userId": userID})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,$3,'group',$4,$5)`, organizationID, actorID, action, groupID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) OrganizationAuditEvents(ctx context.Context, organizationID string, limit int) ([]*models.OrganizationAuditEvent, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("%w: audit limit must be between 1 and 100", ErrAdminValidation)
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id,organization_id::text,COALESCE(actor_id,''),action,target_type,target_id,detail,created_at
		FROM organization_audit_events WHERE organization_id::text=$1
		ORDER BY created_at DESC,id DESC LIMIT $2`, organizationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]*models.OrganizationAuditEvent, 0)
	for rows.Next() {
		event := &models.OrganizationAuditEvent{}
		var detail []byte
		var createdAt time.Time
		if err := rows.Scan(&event.ID, &event.OrganizationID, &event.ActorID, &event.Action, &event.TargetType, &event.TargetID, &detail, &createdAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(detail, &event.Detail); err != nil {
			return nil, err
		}
		event.CreatedAt = formatAdminTime(createdAt)
		events = append(events, event)
	}
	return events, rows.Err()
}

type OrganizationAuditFilter struct {
	Query     string
	Action    string
	Actors    []string
	IPs       []string
	Products  []string
	Locations []string
	From      *time.Time
	To        *time.Time
	Offset    int
	Limit     int
	Ascending bool
}

func (s *Store) QueryOrganizationAuditEvents(ctx context.Context, organizationID string, filter OrganizationAuditFilter) ([]*models.OrganizationAuditEvent, bool, error) {
	if filter.Limit < 1 || filter.Limit > 500 || filter.Offset < 0 {
		return nil, false, fmt.Errorf("%w: audit limit must be between 1 and 500 and offset must be non-negative", ErrAdminValidation)
	}
	args := []any{organizationID}
	where := []string{"e.organization_id::text=$1"}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, strings.ReplaceAll(clause, "%d", fmt.Sprint(len(args))))
	}
	if filter.Query != "" {
		add("(e.action ILIKE $%d OR e.target_type ILIKE $%d OR e.target_id ILIKE $%d OR COALESCE(u.display_name,'') ILIKE $%d OR COALESCE(u.email,'') ILIKE $%d OR e.detail::text ILIKE $%d)", "%"+filter.Query+"%")
	}
	if filter.Action != "" {
		add("e.action=$%d", filter.Action)
	}
	if len(filter.Actors) > 0 {
		add("(e.actor_id=ANY($%d::text[]) OR u.email=ANY($%d::text[]) OR u.display_name=ANY($%d::text[]))", filter.Actors)
	}
	if len(filter.IPs) > 0 {
		add("e.detail->>'ip'=ANY($%d::text[])", filter.IPs)
	}
	if len(filter.Products) > 0 {
		add("e.detail->>'product'=ANY($%d::text[])", filter.Products)
	}
	if len(filter.Locations) > 0 {
		add("e.detail::text ILIKE ANY($%d::text[])", filter.Locations)
	}
	if filter.From != nil {
		add("e.created_at >= $%d", *filter.From)
	}
	if filter.To != nil {
		add("e.created_at <= $%d", *filter.To)
	}
	order := "DESC"
	if filter.Ascending {
		order = "ASC"
	}
	args = append(args, filter.Limit+1, filter.Offset)
	query := `SELECT e.id,e.organization_id::text,COALESCE(e.actor_id,''),COALESCE(u.display_name,''),COALESCE(u.email,''),
		e.action,e.target_type,e.target_id,e.detail,e.created_at
		FROM organization_audit_events e LEFT JOIN users u ON u.id=e.actor_id
		WHERE ` + strings.Join(where, " AND ") + ` ORDER BY e.created_at ` + order + `,e.id ` + order +
		fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	events := make([]*models.OrganizationAuditEvent, 0, filter.Limit+1)
	for rows.Next() {
		event := &models.OrganizationAuditEvent{}
		var detail []byte
		var createdAt time.Time
		if err := rows.Scan(&event.ID, &event.OrganizationID, &event.ActorID, &event.ActorName, &event.ActorEmail,
			&event.Action, &event.TargetType, &event.TargetID, &detail, &createdAt); err != nil {
			return nil, false, err
		}
		if err := json.Unmarshal(detail, &event.Detail); err != nil {
			return nil, false, err
		}
		event.CreatedAt = formatAdminTime(createdAt)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(events) > filter.Limit
	if hasMore {
		events = events[:filter.Limit]
	}
	return events, hasMore, nil
}

func (s *Store) OrganizationAuditEventByID(ctx context.Context, organizationID string, eventID int64) (*models.OrganizationAuditEvent, error) {
	event := &models.OrganizationAuditEvent{}
	var detail []byte
	var createdAt time.Time
	err := s.Pool.QueryRow(ctx, `
		SELECT e.id,e.organization_id::text,COALESCE(e.actor_id,''),COALESCE(u.display_name,''),COALESCE(u.email,''),
		       e.action,e.target_type,e.target_id,e.detail,e.created_at
		FROM organization_audit_events e LEFT JOIN users u ON u.id=e.actor_id
		WHERE e.organization_id::text=$1 AND e.id=$2`, organizationID, eventID).Scan(
		&event.ID, &event.OrganizationID, &event.ActorID, &event.ActorName, &event.ActorEmail,
		&event.Action, &event.TargetType, &event.TargetID, &detail, &createdAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(detail, &event.Detail); err != nil {
		return nil, err
	}
	event.CreatedAt = formatAdminTime(createdAt)
	return event, nil
}
