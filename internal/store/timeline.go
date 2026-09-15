package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// StartDateFieldID returns the workspace's Jira Start date field, or an empty
// id when the site has none.
func (s *Store) StartDateFieldID(ctx context.Context, workspaceID string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `
		SELECT id FROM custom_fields
		WHERE workspace_id=$1 AND app_installation_id IS NULL AND name='Start date' AND type=$2 AND trashed_at IS NULL
		ORDER BY id LIMIT 1`, workspaceID, models.CustomFieldDate).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// ProjectTimeline lists the project's epics in rank order with the child work
// under each, limited to work the user can browse.
func (s *Store) ProjectTimeline(ctx context.Context, workspaceID, userID, projectID string) (models.ProjectTimeline, error) {
	fieldID, err := s.StartDateFieldID(ctx, workspaceID)
	if err != nil {
		return models.ProjectTimeline{}, err
	}
	timeline := models.ProjectTimeline{StartFieldID: fieldID, Epics: []models.TimelineItem{}}
	epics, err := s.EpicsInProjects(ctx, workspaceID, userID, []string{projectID})
	if err != nil || len(epics) == 0 {
		return timeline, err
	}
	item := func(issue *models.Issue) models.TimelineItem {
		scheduled := models.TimelineItem{Issue: issue, DueDate: issue.DueDate}
		if raw, ok := issue.Fields[fieldID]; ok && fieldID != "" {
			_ = json.Unmarshal(raw, &scheduled.StartDate)
		}
		return scheduled
	}
	epicIDs := make([]string, 0, len(epics))
	for _, epic := range epics {
		epicIDs = append(epicIDs, epic.ID)
	}
	work, err := s.EpicChildren(ctx, workspaceID, userID, epicIDs)
	if err != nil {
		return timeline, err
	}
	children := map[string][]models.TimelineItem{}
	for _, child := range work {
		if child.Parent != nil {
			children[child.Parent.ID] = append(children[child.Parent.ID], item(child))
		}
	}
	for _, epic := range epics {
		scheduled := item(epic)
		scheduled.Children = children[epic.ID]
		timeline.Epics = append(timeline.Epics, scheduled)
	}
	return timeline, nil
}

// EpicChildren lists the standard work under the given epics the user can
// browse, in rank order; sub-tasks stay with their parents.
func (s *Store) EpicChildren(ctx context.Context, workspaceID, userID string, epicIDs []string) ([]*models.Issue, error) {
	rows, err := s.Pool.Query(ctx, searchSelect+" "+searchJoin+`
		WHERE i.workspace_id=$1 AND i.parent_id=ANY($2) AND NOT it.subtask AND `+VisibleIssuePredicate("i", "$3")+`
		ORDER BY i.rank, i.key`, workspaceID, epicIDs, userID)
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
