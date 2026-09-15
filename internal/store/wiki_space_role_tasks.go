package store

import (
	"context"
	"fmt"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// Changing or deleting a space role is a long task in Confluence, because it
// reaches every space the role is assigned in. Here the change is made while
// the request waits, and the task records it as finished so a client following
// Confluence's contract finds the outcome where it looks.

const (
	apiTaskWikiSpaceRoleUpdate = "wiki-space-role-update"
	apiTaskWikiSpaceRoleDelete = "wiki-space-role-delete"
)

// finishedWikiTask records work already done as a completed long task.
func (s *Store) finishedWikiTask(ctx context.Context, ws, actor, description, kind string, payload, result any, message string) (APITask, error) {
	task, err := queuedAPITask(ws, actor, description, kind, payload)
	if err != nil {
		return APITask{}, err
	}
	started := time.Now().UTC()
	task.Status, task.Message, task.StartedAt = "RUNNING", "Task is running.", &started
	if err := s.enqueueAPITask(ctx, &task); err != nil {
		return APITask{}, err
	}
	if err := s.CompleteAPITask(ctx, task, message, result); err != nil {
		return APITask{}, err
	}
	return s.APITaskByID(ctx, ws, task.ID)
}

func wikiSpaceRoleExists(ctx context.Context, tx pgx.Tx, ws, roleID string) (bool, error) {
	if systemWikiSpaceRole(roleID) != nil {
		return true, nil
	}
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_space_roles WHERE workspace_id=$1 AND id::text=$2)`, ws, roleID).Scan(&exists)
	return exists, err
}

// moveWikiRoleAssignments moves the assignments a filter selects from one role
// to another in every space of the site, keeping one assignment where the
// principal already held the other role.
func moveWikiRoleAssignments(ctx context.Context, tx pgx.Tx, ws, fromRole, toRole, principalFilter string) (int64, error) {
	if _, err := tx.Exec(ctx, `INSERT INTO wiki_space_role_assignments(space_id,role_id,principal_type,principal_id)
		SELECT a.space_id,$3,a.principal_type,a.principal_id FROM wiki_space_role_assignments a JOIN wiki_spaces s ON s.id=a.space_id
		WHERE s.workspace_id=$1 AND a.role_id=$2 AND `+principalFilter+` ON CONFLICT DO NOTHING`, ws, fromRole, toRole); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM wiki_space_role_assignments a USING wiki_spaces s
		WHERE s.id=a.space_id AND s.workspace_id=$1 AND a.role_id=$2 AND `+principalFilter, ws, fromRole)
	return tag.RowsAffected(), err
}

// wikiGuestPrincipal selects assignments of guests: people, or groups, holding
// the guest role on the site's Confluence.
const wikiGuestPrincipal = `EXISTS (SELECT 1 FROM role_bindings grb
	JOIN products gp ON grb.scope_type='product' AND gp.id::text=grb.scope_id AND gp.product_key='confluence'
	JOIN sites gsi ON gsi.id=gp.site_id AND gsi.workspace_id=$1
	WHERE grb.role_key='atlassian/guest' AND (
	  a.principal_type='USER' AND (grb.principal_type='user' AND grb.principal_id=a.principal_id
	    OR grb.principal_type='group' AND EXISTS (SELECT 1 FROM group_members ggm WHERE ggm.group_id::text=grb.principal_id AND ggm.user_id=a.principal_id))
	  OR a.principal_type='GROUP' AND grb.principal_type='group' AND grb.principal_id=a.principal_id))`

// UpdateWikiSpaceRole renames a custom role and replaces its permissions. When
// asked, anonymous access and guests assigned the role move to another role.
func (s *Store) UpdateWikiSpaceRole(ctx context.Context, ws, actor, id, name, description string, permissions []string, anonymousRoleID, guestRoleID string) (*models.WikiSpaceRole, APITask, error) {
	if systemWikiSpaceRole(id) != nil {
		return nil, APITask{}, fmt.Errorf("%w: system space roles cannot be changed", ErrWikiValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, APITask{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return nil, APITask{}, err
	}
	for _, target := range []string{anonymousRoleID, guestRoleID} {
		if target == "" {
			continue
		}
		exists, err := wikiSpaceRoleExists(ctx, tx, ws, target)
		if err != nil {
			return nil, APITask{}, err
		}
		if !exists || target == id {
			return nil, APITask{}, fmt.Errorf("%w: assignments move to another existing space role", ErrWikiValidation)
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE wiki_space_roles SET name=$3,description=$4,space_permissions=$5 WHERE workspace_id=$1 AND id::text=$2`, ws, id, name, description, permissions)
	if err != nil {
		return nil, APITask{}, err
	}
	if tag.RowsAffected() == 0 {
		return nil, APITask{}, pgx.ErrNoRows
	}
	var movedAnonymous, movedGuests int64
	if anonymousRoleID != "" {
		if movedAnonymous, err = moveWikiRoleAssignments(ctx, tx, ws, id, anonymousRoleID, `a.principal_type='ACCESS_CLASS' AND a.principal_id='anonymous-users'`); err != nil {
			return nil, APITask{}, err
		}
	}
	if guestRoleID != "" {
		if movedGuests, err = moveWikiRoleAssignments(ctx, tx, ws, id, guestRoleID, wikiGuestPrincipal); err != nil {
			return nil, APITask{}, err
		}
	}
	role, err := scanWikiSpaceRole(tx.QueryRow(ctx, wikiSpaceRoleSelect+` WHERE r.id::text=$1`, id))
	if err != nil {
		return nil, APITask{}, err
	}
	if err := wikiRoleAction(ctx, tx, ws, actor, "wiki_space_role", id, models.OpUpsert, role); err != nil {
		return nil, APITask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, APITask{}, err
	}
	task, err := s.finishedWikiTask(ctx, ws, actor, "Update space role", apiTaskWikiSpaceRoleUpdate,
		map[string]any{"roleId": id, "anonymousReassignmentRoleId": anonymousRoleID, "guestReassignmentRoleId": guestRoleID},
		map[string]any{"roleId": id, "anonymousAssignmentsMoved": movedAnonymous, "guestAssignmentsMoved": movedGuests}, "Updated the space role.")
	return role, task, err
}

// DeleteWikiSpaceRole removes a custom role and its assignments in every space.
func (s *Store) DeleteWikiSpaceRole(ctx context.Context, ws, actor, id string) (APITask, error) {
	if systemWikiSpaceRole(id) != nil {
		return APITask{}, fmt.Errorf("%w: system space roles cannot be deleted", ErrWikiValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return APITask{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return APITask{}, err
	}
	role, err := scanWikiSpaceRole(tx.QueryRow(ctx, wikiSpaceRoleSelect+` WHERE r.workspace_id=$1 AND r.id::text=$2 FOR UPDATE`, ws, id))
	if err != nil {
		return APITask{}, err
	}
	removed, err := tx.Exec(ctx, `DELETE FROM wiki_space_role_assignments WHERE role_id=$1 AND space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$2)`, id, ws)
	if err != nil {
		return APITask{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM wiki_space_roles WHERE id::text=$1`, id); err != nil {
		return APITask{}, err
	}
	if err := wikiRoleAction(ctx, tx, ws, actor, "wiki_space_role", id, models.OpDelete, role); err != nil {
		return APITask{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return APITask{}, err
	}
	return s.finishedWikiTask(ctx, ws, actor, "Delete space role", apiTaskWikiSpaceRoleDelete,
		map[string]any{"roleId": id}, map[string]any{"roleId": id, "assignmentsRemoved": removed.RowsAffected()}, "Deleted the space role.")
}
