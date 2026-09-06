package store

import (
	"context"

	"github.com/e6qu/zzira/internal/models"
)

// ChildIssues returns direct children in stable key order. Jira's native
// sub-task hierarchy is one level deep, so descendants are intentionally not
// traversed here.
func (s *Store) ChildIssues(ctx context.Context, workspaceID, parentID string) ([]*models.Issue, error) {
	rows, err := s.Pool.Query(ctx, issueJoin+`
		WHERE i.workspace_id=$1 AND i.parent_id=$2 ORDER BY i.key`, workspaceID, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.Issue{}
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, issue)
	}
	return out, rows.Err()
}

// IssueHierarchyStatuses supplies the parent/child facts consumed by workflow
// rules immediately before a transition is applied.
func (s *Store) IssueHierarchyStatuses(ctx context.Context, workspaceID, issueID string) (string, []string, error) {
	var parentStatus string
	if err := s.Pool.QueryRow(ctx, `
		SELECT COALESCE(parent.status_id,'') FROM issues issue
		LEFT JOIN issues parent ON parent.id=issue.parent_id
		WHERE issue.workspace_id=$1 AND issue.id=$2`, workspaceID, issueID).Scan(&parentStatus); err != nil {
		return "", nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT status_id FROM issues WHERE workspace_id=$1 AND parent_id=$2 ORDER BY id`, workspaceID, issueID)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	children := []string{}
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return "", nil, err
		}
		children = append(children, status)
	}
	return parentStatus, children, rows.Err()
}
