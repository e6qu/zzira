package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var systemWikiSpaceRoles = []*models.WikiSpaceRole{
	{ID: "system-admin", Type: "SYSTEM", Name: "Space administrators", Description: "Administer the space and all of its content.", SpacePermissions: []string{"administer/space", "read/space", "create/page", "read/page", "update/page", "delete/page", "create/blogpost", "read/blogpost", "update/blogpost", "delete/blogpost", "create/comment", "read/comment", "update/comment", "delete/comment", "create/attachment", "read/attachment", "update/attachment", "delete/attachment", "create/folder", "read/folder", "update/folder", "delete/folder", "create/embed", "read/embed", "update/embed", "delete/embed", "create/database", "read/database", "update/database", "delete/database", "create/whiteboard", "read/whiteboard", "update/whiteboard", "delete/whiteboard"}},
	{ID: "system-member", Type: "SYSTEM", Name: "Space members", Description: "Create, read, update and delete knowledge content.", SpacePermissions: []string{"read/space", "create/page", "read/page", "update/page", "delete/page", "create/blogpost", "read/blogpost", "update/blogpost", "delete/blogpost", "create/comment", "read/comment", "update/comment", "delete/comment", "create/attachment", "read/attachment", "update/attachment", "delete/attachment", "create/folder", "read/folder", "update/folder", "delete/folder", "create/embed", "read/embed", "update/embed", "delete/embed", "create/database", "read/database", "update/database", "delete/database", "create/whiteboard", "read/whiteboard", "update/whiteboard", "delete/whiteboard"}},
	{ID: "system-viewer", Type: "SYSTEM", Name: "Space viewers", Description: "Read the space and its published knowledge content.", SpacePermissions: []string{"read/space", "read/page", "read/blogpost", "read/comment", "read/attachment", "read/folder", "read/embed", "read/database", "read/whiteboard"}},
}

const wikiSpaceRoleSelect = `SELECT r.id::text,r.workspace_id,'CUSTOM',r.name,r.description,r.space_permissions FROM wiki_space_roles r`

func scanWikiSpaceRole(row pgx.Row) (*models.WikiSpaceRole, error) {
	role := &models.WikiSpaceRole{}
	err := row.Scan(&role.ID, &role.WorkspaceID, &role.Type, &role.Name, &role.Description, &role.SpacePermissions)
	return role, err
}

func systemWikiSpaceRole(id string) *models.WikiSpaceRole {
	for _, role := range systemWikiSpaceRoles {
		if role.ID == id {
			copy := *role
			copy.SpacePermissions = append([]string(nil), role.SpacePermissions...)
			return &copy
		}
	}
	return nil
}

func (s *Store) WikiSpaceRoles(ctx context.Context, ws, actor string) ([]*models.WikiSpaceRole, error) {
	member, err := s.IsMember(ctx, ws, actor)
	if err != nil || !member {
		if err != nil {
			return nil, err
		}
		return nil, ErrProjectPermission
	}
	roles := make([]*models.WikiSpaceRole, 0, len(systemWikiSpaceRoles))
	for _, role := range systemWikiSpaceRoles {
		copy := *role
		copy.WorkspaceID = ws
		copy.SpacePermissions = append([]string(nil), role.SpacePermissions...)
		roles = append(roles, &copy)
	}
	rows, err := s.Pool.Query(ctx, wikiSpaceRoleSelect+` WHERE r.workspace_id=$1 ORDER BY r.name,r.id`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		role, err := scanWikiSpaceRole(rows)
		if err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func (s *Store) WikiSpaceRole(ctx context.Context, ws, actor, id string) (*models.WikiSpaceRole, error) {
	if role := systemWikiSpaceRole(id); role != nil {
		member, err := s.IsMember(ctx, ws, actor)
		if err != nil || !member {
			if err != nil {
				return nil, err
			}
			return nil, ErrProjectPermission
		}
		role.WorkspaceID = ws
		return role, nil
	}
	return scanWikiSpaceRole(s.Pool.QueryRow(ctx, wikiSpaceRoleSelect+` WHERE r.workspace_id=$1 AND r.id::text=$2`, ws, id))
}

func wikiRoleAction(ctx context.Context, tx pgx.Tx, ws, actor, entity, id, op string, value any) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{entity: value})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: entity, EntityID: id, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func (s *Store) CreateWikiSpaceRole(ctx context.Context, ws, actor, name, description string, permissions []string) (*models.WikiSpaceRole, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return nil, err
	}
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO wiki_space_roles(workspace_id,name,description,space_permissions) VALUES($1,$2,$3,$4) RETURNING id::text`, ws, name, description, permissions).Scan(&id); err != nil {
		return nil, err
	}
	role, err := scanWikiSpaceRole(tx.QueryRow(ctx, wikiSpaceRoleSelect+` WHERE r.id::text=$1`, id))
	if err != nil {
		return nil, err
	}
	if err := wikiRoleAction(ctx, tx, ws, actor, "wiki_space_role", id, models.OpUpsert, role); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return role, nil
}

func (s *Store) UpdateWikiSpaceRole(ctx context.Context, ws, actor, id, name, description string, permissions []string) (*models.WikiSpaceRole, error) {
	if systemWikiSpaceRole(id) != nil {
		return nil, fmt.Errorf("%w: system space roles cannot be changed", ErrWikiValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `UPDATE wiki_space_roles SET name=$3,description=$4,space_permissions=$5 WHERE workspace_id=$1 AND id::text=$2`, ws, id, name, description, permissions)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, pgx.ErrNoRows
	}
	role, err := scanWikiSpaceRole(tx.QueryRow(ctx, wikiSpaceRoleSelect+` WHERE r.id::text=$1`, id))
	if err != nil {
		return nil, err
	}
	if err := wikiRoleAction(ctx, tx, ws, actor, "wiki_space_role", id, models.OpUpsert, role); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return role, nil
}

func (s *Store) DeleteWikiSpaceRole(ctx context.Context, ws, actor, id string) error {
	if systemWikiSpaceRole(id) != nil {
		return fmt.Errorf("%w: system space roles cannot be deleted", ErrWikiValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return err
	}
	role, err := scanWikiSpaceRole(tx.QueryRow(ctx, wikiSpaceRoleSelect+` WHERE r.workspace_id=$1 AND r.id::text=$2 FOR UPDATE`, ws, id))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM wiki_space_role_assignments WHERE role_id=$1 AND space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$2)`, id, ws); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM wiki_space_roles WHERE id::text=$1`, id); err != nil {
		return err
	}
	if err := wikiRoleAction(ctx, tx, ws, actor, "wiki_space_role", id, models.OpDelete, role); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) WikiSpaceRoleAssignments(ctx context.Context, ws, actor, spaceID string) ([]models.WikiSpaceRoleAssignment, error) {
	space, err := s.WikiSpace(ctx, ws, actor, spaceID)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT role_id,principal_type,principal_id FROM wiki_space_role_assignments WHERE space_id::text=$1 ORDER BY role_id,principal_type,principal_id`, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	assignments := []models.WikiSpaceRoleAssignment{}
	for rows.Next() {
		assignment := models.WikiSpaceRoleAssignment{SpaceID: spaceID}
		if err := rows.Scan(&assignment.RoleID, &assignment.PrincipalType, &assignment.PrincipalID); err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(assignments) == 0 {
		if space.Private {
			return []models.WikiSpaceRoleAssignment{{SpaceID: spaceID, RoleID: "system-admin", PrincipalType: "USER", PrincipalID: space.AuthorID}}, nil
		}
		return []models.WikiSpaceRoleAssignment{
			{SpaceID: spaceID, RoleID: "system-member", PrincipalType: "ACCESS_CLASS", PrincipalID: "authenticated-users"},
			{SpaceID: spaceID, RoleID: "system-admin", PrincipalType: "ACCESS_CLASS", PrincipalID: "all-product-admins"},
		}, nil
	}
	return assignments, nil
}

func (s *Store) SetWikiSpaceRoleAssignments(ctx context.Context, ws, actor, spaceID string, assignments []models.WikiSpaceRoleAssignment) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return err
	}
	space, err := scanWikiSpace(tx.QueryRow(ctx, wikiSpaceSelect+` WHERE s.workspace_id=$1 AND s.id::text=$2 FOR UPDATE`, ws, spaceID))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM wiki_space_role_assignments WHERE space_id::text=$1`, spaceID); err != nil {
		return err
	}
	for _, assignment := range assignments {
		if systemWikiSpaceRole(assignment.RoleID) == nil {
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_space_roles WHERE workspace_id=$1 AND id::text=$2)`, ws, assignment.RoleID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("%w: assigned space role does not exist", ErrWikiValidation)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO wiki_space_role_assignments(space_id,role_id,principal_type,principal_id) VALUES($1::bigint,$2,$3,$4)`, spaceID, assignment.RoleID, assignment.PrincipalType, assignment.PrincipalID); err != nil {
			return err
		}
	}
	if err := wikiRoleAction(ctx, tx, ws, actor, "wiki_space_role_assignments", spaceID, models.OpUpsert, map[string]any{"space": space, "assignments": assignments}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
