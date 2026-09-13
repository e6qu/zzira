package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

// ---- issue links ----

const linkJoin = `
SELECT l.id, l.link_type_id, lt.name, lt.inward, lt.outward,
       l.inward_id, l.outward_id, l.workspace_id, l.jira_id, lt.jira_id
FROM issue_links l JOIN issue_link_types lt ON lt.id = l.link_type_id
`

func scanLink(row pgx.Row) (*models.IssueLink, error) {
	l := &models.IssueLink{}
	err := row.Scan(&l.ID, &l.TypeID, &l.TypeName, &l.Inward, &l.Outward, &l.InwardID, &l.OutwardID, &l.WorkspaceID, &l.JiraID, &l.TypeJiraID)
	return l, err
}

// CreateIssueLink links two issues; direction is semantic (inward ← outward).
func (s *Store) CreateIssueLink(ctx context.Context, actorID, workspaceID, typeID, inwardIssueID, outwardIssueID string) (*models.IssueLink, *models.Action, error) {
	var ltName, inward, outward string
	var typeJiraID int64
	err := s.Pool.QueryRow(ctx, `SELECT name, inward, outward, jira_id FROM issue_link_types WHERE id=$1 AND workspace_id=$2`, typeID, workspaceID).Scan(&ltName, &inward, &outward, &typeJiraID)
	if err != nil {
		return nil, nil, fmt.Errorf("link type %q does not exist", typeID)
	}
	if inwardIssueID == outwardIssueID {
		return nil, nil, fmt.Errorf("an issue cannot link to itself")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var issueCount int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM issues WHERE workspace_id=$1 AND id IN ($2,$3)`, workspaceID, inwardIssueID, outwardIssueID).Scan(&issueCount); err != nil {
		return nil, nil, err
	}
	if issueCount != 2 {
		return nil, nil, fmt.Errorf("linked issues must belong to workspace %q", workspaceID)
	}
	id := NewID("lnk")
	var jiraID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO issue_links (id, link_type_id, inward_id, outward_id, workspace_id)
		VALUES ($1,$2,$3,$4,$5) RETURNING jira_id`, id, typeID, inwardIssueID, outwardIssueID, workspaceID).Scan(&jiraID); err != nil {
		return nil, nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	link := &models.IssueLink{ID: id, TypeID: typeID, TypeName: ltName, Inward: inward, Outward: outward, InwardID: inwardIssueID, OutwardID: outwardIssueID, WorkspaceID: workspaceID, JiraID: jiraID, TypeJiraID: typeJiraID}
	payload, err := json.Marshal(models.IssueLinkPayload{Link: *link})
	if err != nil {
		return nil, nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssueLink, EntityID: id,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return link, action, nil
}

func (s *Store) LinksByIssue(ctx context.Context, issueID string) ([]*models.IssueLink, error) {
	rows, err := s.Pool.Query(ctx, linkJoin+`WHERE l.inward_id=$1 OR l.outward_id=$1 ORDER BY l.created_at`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.IssueLink
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// IssueLinkByID finds a link by the id clients see or the one stored.
func (s *Store) IssueLinkByID(ctx context.Context, workspaceID, id string) (*models.IssueLink, error) {
	return scanLink(s.Pool.QueryRow(ctx, linkJoin+`WHERE (l.id=$1 OR l.jira_id::text=$1) AND l.workspace_id=$2`, id, workspaceID))
}

func (s *Store) DeleteIssueLink(ctx context.Context, actorID, workspaceID, linkID string) (*models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var inwardID, outwardID string
	err = tx.QueryRow(ctx, `SELECT inward_id, outward_id FROM issue_links WHERE id=$1 AND workspace_id=$2`, linkID, workspaceID).Scan(&inwardID, &outwardID)
	if err != nil {
		return nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(models.IssueLinkDeletePayload{LinkID: linkID, InwardID: inwardID, OutwardID: outwardID})
	if err != nil {
		return nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssueLink, EntityID: linkID,
		Op: models.OpDelete, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if _, err := tx.Exec(ctx, `DELETE FROM issue_links WHERE id=$1 AND workspace_id=$2`, linkID, workspaceID); err != nil {
		return nil, err
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return action, nil
}

// ---- labels ----

// Labels lists distinct labels across the workspace with counts.
func (s *Store) Labels(ctx context.Context, workspaceID, userID string, query string) (totalCount int64, labels []string, err error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT l FROM issues i, unnest(i.labels) AS l
		WHERE i.workspace_id=$1 AND `+VisibleIssuePredicate("i", "$2")+`
		  AND l ILIKE '%'||$3||'%'
		ORDER BY l`, workspaceID, userID, query)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return 0, nil, err
		}
		labels = append(labels, l)
	}
	return int64(len(labels)), labels, rows.Err()
}
