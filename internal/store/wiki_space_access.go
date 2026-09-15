package store

import (
	"context"
	"errors"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// WikiSpacePermissionAssignment is one principal's one permission in a space
// as the v2 API reports it, whether it comes from a role assigned to the
// principal or from a direct grant.
type WikiSpacePermissionAssignment struct {
	ID, PrincipalType, PrincipalID, Operation, Target string
}

// wikiPrincipalTypes maps role assignment principals to the principal types a
// space permission names; an access class is reported as a role.
var wikiPrincipalTypes = map[string]string{"USER": "user", "GROUP": "group", "ACCESS_CLASS": "role", "user": "user", "group": "group"}

// WikiSpacePermissionAssignments lists what everyone may do in a space: every
// permission of every role assigned in it, and every direct grant, each once.
// Ids are stable for the same principal and permission in the same space.
func (s *Store) WikiSpacePermissionAssignments(ctx context.Context, ws, actor, spaceID string) ([]WikiSpacePermissionAssignment, error) {
	space, err := s.WikiSpace(ctx, ws, actor, spaceID)
	if err != nil {
		return nil, err
	}
	assignments, err := s.WikiSpaceRoleAssignments(ctx, ws, actor, spaceID)
	if err != nil {
		return nil, err
	}
	out := []WikiSpacePermissionAssignment{}
	seen := map[string]bool{}
	add := func(principalType, principalID, permission string) {
		operation, target, ok := strings.Cut(permission, "/")
		principal := wikiPrincipalTypes[principalType]
		key := principal + "\x00" + principalID + "\x00" + permission
		if !ok || principal == "" || seen[key] {
			return
		}
		seen[key] = true
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(space.ID + "\x00" + key))
		out = append(out, WikiSpacePermissionAssignment{ID: strconv.FormatUint(hash.Sum64()>>1, 10), PrincipalType: principal, PrincipalID: principalID, Operation: operation, Target: target})
	}
	for _, assignment := range assignments {
		role, err := s.WikiSpaceRole(ctx, ws, actor, assignment.RoleID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, permission := range role.SpacePermissions {
			add(assignment.PrincipalType, assignment.PrincipalID, permission)
		}
	}
	rows, err := s.Pool.Query(ctx, `SELECT subject_type,subject_id,permission FROM wiki_space_permission_grants WHERE space_id::text=$1 ORDER BY id`, space.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var subjectType, subjectID, permission string
		if err := rows.Scan(&subjectType, &subjectID, &permission); err != nil {
			return nil, err
		}
		add(subjectType, subjectID, permission)
	}
	return out, rows.Err()
}

// WikiSpaceFavouriteKeys lists the keys of the spaces a person has starred,
// which Confluence keeps as favourite relations from the person to the space.
func (s *Store) WikiSpaceFavouriteKeys(ctx context.Context, ws, actor, accountID string) (map[string]bool, error) {
	if accountID == CurrentUserKey {
		accountID = actor
	}
	rows, err := s.Pool.Query(ctx, `SELECT target_key FROM wiki_relations WHERE workspace_id=$1 AND name='favourite'
		AND source_type='user' AND source_key=$2 AND target_type='space'`, ws, accountID)
	if err != nil {
		return nil, err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(keys))
	for _, key := range keys {
		out[key] = true
	}
	return out, nil
}
