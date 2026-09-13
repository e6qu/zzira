package store

import (
	"context"
	"strconv"
)

// IssueCreatorID is the person whose action created an issue, which Jira
// reports as the creator even when someone else is the reporter.
func (s *Store) IssueCreatorID(ctx context.Context, workspaceID, issueID string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT actor_id FROM actions WHERE workspace_id=$1 AND entity_type='issue' AND entity_id=$2 ORDER BY seq LIMIT 1`, workspaceID, issueID).Scan(&id)
	return id, err
}

// QueueIssueEmail queues one message to each address.
func (s *Store) QueueIssueEmail(ctx context.Context, workspaceID string, recipients []string, subject, body string) error {
	for _, recipient := range recipients {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO email_outbox(workspace_id,recipient,subject,body) VALUES($1,$2,$3,$4)`, workspaceID, recipient, subject, body); err != nil {
			return err
		}
	}
	return nil
}

// IssueIDsByJiraIDs maps a site's numeric issue ids to stored ids, skipping unknown ones.
func (s *Store) IssueIDsByJiraIDs(ctx context.Context, workspaceID string, jiraIDs []int64) (map[int64]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT jira_id, id FROM issues WHERE workspace_id=$1 AND jira_id = ANY($2)`, workspaceID, jiraIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var jiraID int64
		var id string
		if err = rows.Scan(&jiraID, &id); err != nil {
			return nil, err
		}
		out[jiraID] = id
	}
	return out, rows.Err()
}

// IssueJiraIDString is the id clients see for a stored issue id.
func (s *Store) IssueJiraIDString(ctx context.Context, issueID string) string {
	var jiraID int64
	if err := s.Pool.QueryRow(ctx, `SELECT jira_id FROM issues WHERE id=$1`, issueID).Scan(&jiraID); err != nil {
		return ""
	}
	return strconv.FormatInt(jiraID, 10)
}
