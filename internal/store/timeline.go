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
	return s.siteDateFieldID(ctx, workspaceID, "Start date")
}

// siteDateFieldID returns the site's own date field with the given name, or an
// empty id when it has none.
func (s *Store) siteDateFieldID(ctx context.Context, workspaceID, name string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `
		SELECT id FROM custom_fields
		WHERE workspace_id=$1 AND app_installation_id IS NULL AND name=$3 AND type=$2 AND trashed_at IS NULL
		ORDER BY id LIMIT 1`, workspaceID, models.CustomFieldDate, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// ProjectTimeline lists the project's work above the story level in rank
// order, each under whatever is above it, with the story-level work under the
// lowest of them -- so a site with levels above the epic reads its roadmap
// through all of them. Everything is limited to work the user can browse.
func (s *Store) ProjectTimeline(ctx context.Context, workspaceID, userID, projectID string) (models.ProjectTimeline, error) {
	fieldID, err := s.StartDateFieldID(ctx, workspaceID)
	if err != nil {
		return models.ProjectTimeline{}, err
	}
	timeline := models.ProjectTimeline{StartFieldID: fieldID, Epics: []models.TimelineItem{}}
	parents, err := s.ParentWorkInProjects(ctx, workspaceID, userID, []string{projectID})
	if err != nil || len(parents) == 0 {
		return timeline, err
	}
	item := func(issue *models.Issue) models.TimelineItem {
		scheduled := models.TimelineItem{Issue: issue, DueDate: issue.DueDate}
		if raw, ok := issue.Fields[fieldID]; ok && fieldID != "" {
			_ = json.Unmarshal(raw, &scheduled.StartDate)
		}
		return scheduled
	}
	epicIDs := make([]string, 0, len(parents))
	for _, parent := range parents {
		if parent.IssueType.HierarchyLevel == 1 {
			epicIDs = append(epicIDs, parent.ID)
		}
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
	// The parents arrive deepest level first, so a level is finished before
	// the level above it reads its children.
	built := map[string]models.TimelineItem{}
	order := make([]string, 0, len(parents))
	for index := len(parents) - 1; index >= 0; index-- {
		issue := parents[index]
		scheduled := item(issue)
		scheduled.Children = append(scheduled.Children, children[issue.ID]...)
		built[issue.ID] = scheduled
		order = append(order, issue.ID)
	}
	placed := map[string]bool{}
	for _, id := range order {
		scheduled := built[id]
		parent := scheduled.Issue.Parent
		if parent == nil {
			continue
		}
		above, ok := built[parent.ID]
		if !ok || above.Issue.IssueType.HierarchyLevel <= scheduled.Issue.IssueType.HierarchyLevel {
			continue
		}
		above.Children = append(above.Children, scheduled)
		built[parent.ID] = above
		placed[id] = true
	}
	for _, parent := range parents {
		if !placed[parent.ID] {
			timeline.Epics = append(timeline.Epics, built[parent.ID])
		}
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

// WorkBeneath lists every piece of standard work under the given parents, at
// any depth: the children of an epic, and for a level above the epic the work
// under each of its epics as well. Sub-tasks are left out, as they are in the
// epic report, because they are counted through the work they belong to.
func (s *Store) WorkBeneath(ctx context.Context, workspaceID, userID string, parentIDs []string) ([]*models.Issue, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH RECURSIVE beneath AS (
		  SELECT id FROM issues WHERE workspace_id=$1 AND parent_id=ANY($2)
		  UNION
		  SELECT child.id FROM issues child JOIN beneath ON child.parent_id=beneath.id
		  WHERE child.workspace_id=$1
		)
		`+searchSelect+" "+searchJoin+`
		WHERE i.workspace_id=$1 AND i.id IN (SELECT id FROM beneath) AND NOT it.subtask AND `+VisibleIssuePredicate("i", "$3")+`
		ORDER BY i.rank, i.key`, workspaceID, parentIDs, userID)
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
