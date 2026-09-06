package store

import "context"

// IssueStatusHistory returns statuses departed by real status changes, oldest
// first. It derives history from the immutable sync log used by changelog.
func (s *Store) IssueStatusHistory(ctx context.Context, workspaceID, issueID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT payload->'diff'->'status'->>'from'
		FROM actions
		WHERE workspace_id=$1 AND entity_type='issue' AND entity_id=$2
		  AND payload->'diff'->'status'->>'from' IS NOT NULL
		  AND payload->'diff'->'status'->>'from' <> payload->'diff'->'status'->>'to'
		ORDER BY seq`, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var statuses []string
	for rows.Next() {
		var statusID string
		if err := rows.Scan(&statusID); err != nil {
			return nil, err
		}
		statuses = append(statuses, statusID)
	}
	return statuses, rows.Err()
}
