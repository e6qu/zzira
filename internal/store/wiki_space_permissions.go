package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Confluence says who may do what in a space two ways: a role gathers
// permissions and is assigned to people, and a direct grant gives one subject
// one permission. This product started with roles only, so the older API had
// nothing to write to. Both exist now, and the permission check accepts either,
// which is what lets the two APIs describe one space rather than two.

var ErrWikiPermissionValidation = errors.New("invalid space permission")

// WikiSpacePermissionGrant is one subject's one permission in one space.
type WikiSpacePermissionGrant struct {
	ID          string
	SubjectType string
	SubjectID   string
	Permission  string
}

// wikiSpacePermissionCatalogue is every permission this product enforces,
// derived from the system roles so the catalogue cannot advertise a permission
// nothing checks.
func wikiSpacePermissionCatalogue() []string {
	seen := map[string]bool{}
	for _, role := range systemWikiSpaceRoles {
		for _, permission := range role.SpacePermissions {
			seen[permission] = true
		}
	}
	out := make([]string, 0, len(seen))
	for permission := range seen {
		out = append(out, permission)
	}
	sort.Strings(out)
	return out
}

// WikiSpacePermissions lists what a caller may grant.
func (s *Store) WikiSpacePermissions(ctx context.Context, ws, actor string) ([]string, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return nil, err
	}
	return wikiSpacePermissionCatalogue(), nil
}

// WikiSpaceRoleMode reports how this site governs spaces, read from what it
// actually holds: direct grants and no role assignments is a site that has not
// started; both is a site part-way through; only roles is a site that has
// finished.
func (s *Store) WikiSpaceRoleMode(ctx context.Context, ws, actor string) (string, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return "", err
	}
	var grants, assignments bool
	if err := s.Pool.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM wiki_space_permission_grants g JOIN wiki_spaces s ON s.id=g.space_id WHERE s.workspace_id=$1),
		EXISTS(SELECT 1 FROM wiki_space_role_assignments a JOIN wiki_spaces s ON s.id=a.space_id WHERE s.workspace_id=$1)`,
		ws).Scan(&grants, &assignments); err != nil {
		return "", err
	}
	switch {
	case grants && assignments:
		return "ROLES_TRANSITION", nil
	case grants:
		return "PRE_ROLES", nil
	default:
		return "ROLES", nil
	}
}

// permissionKey turns Confluence's {key, target} into the form this product
// checks. `read` on `space` is `read/space`, which is exactly how the system
// roles name it.
func permissionKey(key, target string) (string, error) {
	key, target = strings.TrimSpace(key), strings.TrimSpace(target)
	if key == "" || target == "" {
		return "", fmt.Errorf("%w: an operation key and target are required", ErrWikiPermissionValidation)
	}
	permission := key + "/" + target
	for _, known := range wikiSpacePermissionCatalogue() {
		if known == permission {
			return permission, nil
		}
	}
	return "", fmt.Errorf("%w: %s is not a permission this site enforces", ErrWikiPermissionValidation, permission)
}

// resolveSubject checks the subject exists, so a grant cannot be made to
// nobody and then read back as if it meant something.
func (s *Store) resolveSubject(ctx context.Context, ws, actor, subjectType, identifier string) (string, string, error) {
	switch strings.ToLower(strings.TrimSpace(subjectType)) {
	case "user":
		if _, err := s.WikiUserByAccountID(ctx, ws, actor, identifier); err != nil {
			return "", "", err
		}
		return "user", identifier, nil
	case "group":
		// Confluence allows a group to be named by id or by name.
		var groupID string
		err := s.Pool.QueryRow(ctx, `SELECT id::text FROM groups WHERE id::text=$1 OR name=$1`, identifier).Scan(&groupID)
		if err != nil {
			return "", "", err
		}
		return "group", groupID, nil
	default:
		return "", "", fmt.Errorf("%w: the subject type must be user or group", ErrWikiPermissionValidation)
	}
}

// AddWikiSpacePermission grants one permission in a space.
func (s *Store) AddWikiSpacePermission(ctx context.Context, ws, actor, spaceKey, subjectType, identifier, key, target string) (WikiSpacePermissionGrant, error) {
	spaceID, err := s.spaceForAdministration(ctx, ws, actor, spaceKey)
	if err != nil {
		return WikiSpacePermissionGrant{}, err
	}
	permission, err := permissionKey(key, target)
	if err != nil {
		return WikiSpacePermissionGrant{}, err
	}
	resolvedType, resolvedID, err := s.resolveSubject(ctx, ws, actor, subjectType, identifier)
	if err != nil {
		return WikiSpacePermissionGrant{}, err
	}
	grant := WikiSpacePermissionGrant{SubjectType: resolvedType, SubjectID: resolvedID, Permission: permission}
	err = s.Pool.QueryRow(ctx, `INSERT INTO wiki_space_permission_grants(space_id,subject_type,subject_id,permission)
		VALUES($1::bigint,$2,$3,$4)
		ON CONFLICT (space_id,subject_type,subject_id,permission) DO UPDATE SET permission=EXCLUDED.permission
		RETURNING id::text`, spaceID, resolvedType, resolvedID, permission).Scan(&grant.ID)
	return grant, err
}

// AddWikiSpaceCustomContentPermissions grants several at once, which is the
// shape the custom content endpoint uses.
func (s *Store) AddWikiSpaceCustomContentPermissions(ctx context.Context, ws, actor, spaceKey, subjectType, identifier string, operations [][2]string) error {
	if len(operations) == 0 {
		return fmt.Errorf("%w: at least one operation is required", ErrWikiPermissionValidation)
	}
	for _, operation := range operations {
		if _, err := s.AddWikiSpacePermission(ctx, ws, actor, spaceKey, subjectType, identifier, operation[0], operation[1]); err != nil {
			return err
		}
	}
	return nil
}

// RemoveWikiSpacePermission takes one grant away. Removing a subject's read
// takes the rest with it, because a permission that cannot be reached is not a
// permission — which is what Confluence documents for this operation.
func (s *Store) RemoveWikiSpacePermission(ctx context.Context, ws, actor, spaceKey, grantID string) error {
	spaceID, err := s.spaceForAdministration(ctx, ws, actor, spaceKey)
	if err != nil {
		return err
	}
	var subjectType, subjectID, permission string
	if err = s.Pool.QueryRow(ctx, `SELECT subject_type,subject_id,permission FROM wiki_space_permission_grants
		WHERE id::text=$1 AND space_id::text=$2`, grantID, spaceID).Scan(&subjectType, &subjectID, &permission); err != nil {
		return err
	}
	if permission == "read/space" {
		_, err = s.Pool.Exec(ctx, `DELETE FROM wiki_space_permission_grants
			WHERE space_id::text=$1 AND subject_type=$2 AND subject_id=$3`, spaceID, subjectType, subjectID)
		return err
	}
	_, err = s.Pool.Exec(ctx, `DELETE FROM wiki_space_permission_grants WHERE id::text=$1`, grantID)
	return err
}

// WikiSpacePermissionGrants lists a space's direct grants.
func (s *Store) WikiSpacePermissionGrants(ctx context.Context, ws, actor, spaceKey string) ([]WikiSpacePermissionGrant, error) {
	space, err := s.WikiSpaceByKey(ctx, ws, actor, spaceKey)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text,subject_type,subject_id,permission
		FROM wiki_space_permission_grants WHERE space_id::text=$1 ORDER BY id`, space.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := []WikiSpacePermissionGrant{}
	for rows.Next() {
		var grant WikiSpacePermissionGrant
		if err = rows.Scan(&grant.ID, &grant.SubjectType, &grant.SubjectID, &grant.Permission); err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

// spaceForAdministration resolves a space the caller is about to administer.
// A workspace administrator administers every space, so the lookup does not go
// through the ordinary visibility gate for them: once a space names who may
// read it, it hides itself from everyone it did not name, and an administrator
// locked out of the space they are configuring could not undo it.
func (s *Store) spaceForAdministration(ctx context.Context, ws, actor, spaceKey string) (string, error) {
	admin, err := s.IsAdmin(ctx, ws, actor)
	if err != nil {
		return "", err
	}
	if !admin {
		space, spaceErr := s.WikiSpaceByKey(ctx, ws, actor, spaceKey)
		if spaceErr != nil {
			return "", spaceErr
		}
		return space.ID, s.requireSpaceAdmin(ctx, ws, actor, space.ID)
	}
	var spaceID string
	if err = s.Pool.QueryRow(ctx, `SELECT id::text FROM wiki_spaces
		WHERE workspace_id=$1 AND key=$2`, ws, spaceKey).Scan(&spaceID); err != nil {
		return "", err
	}
	return spaceID, nil
}

func (s *Store) requireSpaceAdmin(ctx context.Context, ws, actor, spaceID string) error {
	admin, err := s.IsAdmin(ctx, ws, actor)
	if err != nil {
		return err
	}
	if admin {
		return nil
	}
	var allowed bool
	if err = s.Pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM wiki_spaces s WHERE s.workspace_id=$1 AND s.id::text=$3
		AND `+wikiSpacePermissionAllowed("administer/space")+`)`, ws, actor, spaceID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrProjectPermission
	}
	return nil
}

// CheckWikiContentPermission answers whether a subject may perform an operation
// on one piece of content. It resolves through the same rules the reads use, so
// the answer cannot disagree with what the subject would actually get.
func (s *Store) CheckWikiContentPermission(ctx context.Context, ws, actor, contentID, subjectType, identifier, operation string) (bool, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return false, err
	}
	resolvedType, resolvedID, err := s.resolveSubject(ctx, ws, actor, subjectType, identifier)
	if err != nil {
		return false, err
	}
	permission, err := permissionKey(strings.TrimSpace(operation), "page")
	if err != nil {
		return false, err
	}
	// A group is allowed when any of its people are, which is what asking
	// about a group means.
	userIDs := []string{resolvedID}
	if resolvedType == "group" {
		userIDs, err = s.groupMemberIDs(ctx, resolvedID)
		if err != nil {
			return false, err
		}
	}
	for _, userID := range userIDs {
		var visible bool
		if err = s.Pool.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
			WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+`
			AND `+wikiSpacePermissionAllowed(permission)+`
			AND p.id::text=$3 AND p.status='current')`, ws, userID, contentID).Scan(&visible); err != nil {
			return false, err
		}
		if visible {
			return true, nil
		}
	}
	// The content has to exist, or "no" would be indistinguishable from
	// "there is nothing here".
	var exists bool
	if err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_pages p
		JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1 AND p.id::text=$2)`,
		ws, contentID).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, pgx.ErrNoRows
	}
	return false, nil
}

func (s *Store) groupMemberIDs(ctx context.Context, groupID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT user_id FROM group_members WHERE group_id::text=$1`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
