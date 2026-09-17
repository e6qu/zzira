package store

import (
	"context"

	"github.com/e6qu/zzira/internal/models"
)

// AssignableUsersForProject lists the people a work item in the project may be
// assigned to: active members, not app accounts, who hold Assignable user.
// This is the set the assignee picker offers, so an automation rule cannot
// assign work to someone a person could not.
func (s *Store) AssignableUsersForProject(ctx context.Context, workspaceID, projectID, issueID string) ([]*models.User, error) {
	users, err := s.SiteUsers(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	assignable := []*models.User{}
	for _, user := range users {
		if !user.Active || user.AccountType == "app" {
			continue
		}
		allowed, err := s.HasProjectPermission(ctx, workspaceID, user.ID, projectID, issueID, "ASSIGNABLE_USER")
		if err != nil {
			return nil, err
		}
		if allowed {
			assignable = append(assignable, user)
		}
	}
	return assignable, nil
}

// OpenWorkByAssignee counts each person's unresolved work in the project:
// work with no resolution, which is what Jira means by unresolved.
func (s *Store) OpenWorkByAssignee(ctx context.Context, projectID string) (map[string]int, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT i.assignee_id, count(*)
		FROM issues i JOIN statuses st ON st.id = i.status_id
		WHERE i.project_id=$1 AND i.assignee_id IS NOT NULL AND i.resolution_id IS NULL
		GROUP BY i.assignee_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var assignee string
		var count int
		if err := rows.Scan(&assignee, &count); err != nil {
			return nil, err
		}
		counts[assignee] = count
	}
	return counts, rows.Err()
}

// LastAssignedAtByAssignee reports when each person last had work in the
// project assigned to them, which is the order a round-robin rotates through.
func (s *Store) LastAssignedAtByAssignee(ctx context.Context, projectID string) (map[string]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT assignee_id, max(updated_at)::text
		FROM issues WHERE project_id=$1 AND assignee_id IS NOT NULL
		GROUP BY assignee_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]string{}
	for rows.Next() {
		var assignee, at string
		if err := rows.Scan(&assignee, &at); err != nil {
			return nil, err
		}
		seen[assignee] = at
	}
	return seen, rows.Err()
}
