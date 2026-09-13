package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// DeleteSiteGroup removes a group. With a swap group, everything granted to the
// deleted group — permissions, security levels, roles, shares, restrictions —
// moves to the swap group, as Jira does; without one those grants go with it.
func (s *Store) DeleteSiteGroup(ctx context.Context, ws, actor, groupID, swapID string) error {
	if err := s.requireSiteAdmin(ctx, ws, actor); err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var swapName string
	if swapID != "" {
		if err = tx.QueryRow(ctx, `SELECT name FROM groups WHERE id::text=$1`, swapID).Scan(&swapName); err != nil {
			return fmt.Errorf("%w: the swap group does not exist", ErrPeopleNotFound)
		}
	}
	type reference struct{ drop, move string }
	references := []reference{
		{
			`DELETE FROM permission_scheme_grants g WHERE workspace_id=$1 AND holder_type='group' AND holder_value=$2
			   AND ($3='' OR EXISTS (SELECT 1 FROM permission_scheme_grants o WHERE o.workspace_id=g.workspace_id AND o.scheme_id=g.scheme_id AND o.permission_key=g.permission_key AND o.holder_type='group' AND o.holder_value=$3))`,
			`UPDATE permission_scheme_grants SET holder_value=$3, holder_parameter=$4 WHERE workspace_id=$1 AND holder_type='group' AND holder_value=$2`,
		},
		{
			`DELETE FROM issue_security_level_members g WHERE workspace_id=$1 AND holder_type='group' AND holder_value=$2
			   AND ($3='' OR EXISTS (SELECT 1 FROM issue_security_level_members o WHERE o.scheme_id=g.scheme_id AND o.level_id=g.level_id AND o.holder_type='group' AND o.holder_value=$3))`,
			`UPDATE issue_security_level_members SET holder_value=$3, holder_parameter=$4 WHERE workspace_id=$1 AND holder_type='group' AND holder_value=$2`,
		},
		{
			`DELETE FROM project_role_default_actors g WHERE workspace_id=$1 AND principal_type='group' AND principal_id=$2
			   AND ($3='' OR EXISTS (SELECT 1 FROM project_role_default_actors o WHERE o.workspace_id=g.workspace_id AND o.role_id=g.role_id AND o.principal_type='group' AND o.principal_id=$3))`,
			`UPDATE project_role_default_actors SET principal_id=$3 WHERE workspace_id=$1 AND principal_type='group' AND principal_id=$2`,
		},
		{
			`DELETE FROM role_bindings g WHERE $1<>'' AND principal_type='group' AND principal_id=$2
			   AND ($3='' OR EXISTS (SELECT 1 FROM role_bindings o WHERE o.scope_type=g.scope_type AND o.scope_id=g.scope_id AND o.role_key=g.role_key AND o.principal_type='group' AND o.principal_id=$3))`,
			`UPDATE role_bindings SET principal_id=$3 WHERE $1<>'' AND principal_type='group' AND principal_id=$2`,
		},
		{
			`DELETE FROM wiki_page_restrictions g WHERE $1<>'' AND subject_type='group' AND subject_id=$2
			   AND ($3='' OR EXISTS (SELECT 1 FROM wiki_page_restrictions o WHERE o.page_id=g.page_id AND o.operation=g.operation AND o.subject_type='group' AND o.subject_id=$3))`,
			`UPDATE wiki_page_restrictions SET subject_id=$3 WHERE $1<>'' AND subject_type='group' AND subject_id=$2`,
		},
		{
			`DELETE FROM wiki_space_permission_grants g WHERE $1<>'' AND subject_type='group' AND subject_id=$2
			   AND ($3='' OR EXISTS (SELECT 1 FROM wiki_space_permission_grants o WHERE o.space_id=g.space_id AND o.permission=g.permission AND o.subject_type='group' AND o.subject_id=$3))`,
			`UPDATE wiki_space_permission_grants SET subject_id=$3 WHERE $1<>'' AND subject_type='group' AND subject_id=$2`,
		},
		{
			`DELETE FROM wiki_space_role_assignments g WHERE $1<>'' AND lower(principal_type)='group' AND principal_id=$2
			   AND ($3='' OR EXISTS (SELECT 1 FROM wiki_space_role_assignments o WHERE o.space_id=g.space_id AND o.role_id=g.role_id AND o.principal_type=g.principal_type AND o.principal_id=$3))`,
			`UPDATE wiki_space_role_assignments SET principal_id=$3 WHERE $1<>'' AND lower(principal_type)='group' AND principal_id=$2`,
		},
		{
			`DELETE FROM filter_share_permissions g WHERE $1<>'' AND group_id::text=$2
			   AND ($3='' OR EXISTS (SELECT 1 FROM filter_share_permissions o WHERE o.filter_id=g.filter_id AND o.permission_type=g.permission_type AND o.rights=g.rights AND o.group_id::text=$3))`,
			`UPDATE filter_share_permissions SET group_id=$3::uuid WHERE $1<>'' AND group_id::text=$2`,
		},
	}
	for _, ref := range references {
		if _, err = tx.Exec(ctx, ref.drop, ws, groupID, swapID); err != nil {
			return err
		}
		if swapID == "" {
			continue
		}
		move := ref.move
		args := []any{ws, groupID, swapID}
		if strings.Contains(move, "$4") {
			args = append(args, swapName)
		}
		if _, err = tx.Exec(ctx, move, args...); err != nil {
			return err
		}
	}
	// Request types keep an array of the groups that may raise them.
	if swapID == "" {
		_, err = tx.Exec(ctx, `UPDATE service_request_types SET group_ids=array_remove(group_ids,$1) WHERE $1=ANY(group_ids)`, groupID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE service_request_types SET group_ids=(SELECT COALESCE(array_agg(DISTINCT x),'{}') FROM unnest(array_replace(group_ids,$1,$2)) x) WHERE $1=ANY(group_ids)`, groupID, swapID)
	}
	if err != nil {
		return err
	}
	if swapID != "" {
		// Members of the deleted group keep their access through the swap group.
		if _, err = tx.Exec(ctx, `INSERT INTO group_members(group_id,user_id) SELECT $2::uuid,user_id FROM group_members WHERE group_id::text=$1 ON CONFLICT DO NOTHING`, groupID, swapID); err != nil {
			return err
		}
	}
	tag, err := tx.Exec(ctx, `DELETE FROM groups WHERE id::text=$1`, groupID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return tx.Commit(ctx)
}

// UserIssueRelations are the ways a structured user query ties people to issues.
var UserIssueRelations = map[string]string{
	"assignee":     `SELECT i.assignee_id FROM issues i WHERE i.workspace_id=$1 AND (i.project_id=ANY($2) OR i.id=ANY($3)) AND i.assignee_id IS NOT NULL AND i.assignee_id<>''`,
	"reporter":     `SELECT i.reporter_id FROM issues i WHERE i.workspace_id=$1 AND (i.project_id=ANY($2) OR i.id=ANY($3)) AND i.reporter_id IS NOT NULL AND i.reporter_id<>''`,
	"watcher":      `SELECT w.user_id FROM watchers w JOIN issues i ON i.id=w.issue_id WHERE i.workspace_id=$1 AND (i.project_id=ANY($2) OR i.id=ANY($3))`,
	"voter":        `SELECT v.user_id FROM issue_votes v JOIN issues i ON i.id=v.issue_id WHERE i.workspace_id=$1 AND (i.project_id=ANY($2) OR i.id=ANY($3))`,
	"commenter":    `SELECT c.author_id FROM comments c JOIN issues i ON i.id=c.issue_id WHERE i.workspace_id=$1 AND (i.project_id=ANY($2) OR i.id=ANY($3))`,
	"transitioner": `SELECT a.actor_id FROM actions a JOIN issues i ON i.id=a.entity_id WHERE a.workspace_id=$1 AND a.entity_type='issue' AND (i.project_id=ANY($2) OR i.id=ANY($3)) AND a.payload->'diff'->'status'->>'from' IS NOT NULL AND a.payload->'diff'->'status'->>'from' <> a.payload->'diff'->'status'->>'to'`,
}

// UsersRelatedToIssues lists the people who hold a relation to issues in the
// given projects or to the given issues.
func (s *Store) UsersRelatedToIssues(ctx context.Context, ws, relation string, projectIDs, issueIDs []string) ([]string, error) {
	query, ok := UserIssueRelations[relation]
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a user relation", ErrPeopleValidation, relation)
	}
	if projectIDs == nil {
		projectIDs = []string{}
	}
	if issueIDs == nil {
		issueIDs = []string{}
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT id FROM (`+query+`) r(id) JOIN memberships m ON m.user_id=r.id AND m.workspace_id=$1`, ws, projectIDs, issueIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
