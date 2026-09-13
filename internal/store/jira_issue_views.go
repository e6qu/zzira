package store

import (
	"context"
)

// RecordIssueView remembers that a person viewed an issue, for the issue
// picker's history. Only the latest view of each issue is kept.
func (s *Store) RecordIssueView(ctx context.Context, workspaceID, userID, issueID string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO issue_views(workspace_id,user_id,issue_id,viewed_at) VALUES($1,$2,$3,now())
		ON CONFLICT (workspace_id,user_id,issue_id) DO UPDATE SET viewed_at=now()`, workspaceID, userID, issueID)
	return err
}

// RecentlyViewedIssueIDs lists the issues a person viewed most recently.
func (s *Store) RecentlyViewedIssueIDs(ctx context.Context, workspaceID, userID string, limit int) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT issue_id FROM issue_views WHERE workspace_id=$1 AND user_id=$2 ORDER BY viewed_at DESC, issue_id LIMIT $3`, workspaceID, userID, limit)
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
