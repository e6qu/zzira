package store

import (
	"context"
	"sort"
)

// navigableColumnLabels names Jira's navigable system fields, the columns the
// issue navigator and filters can show.
var navigableColumnLabels = map[string]string{
	"issuekey": "Key", "summary": "Summary", "issuetype": "Issue Type", "status": "Status",
	"priority": "Priority", "assignee": "Assignee", "reporter": "Reporter", "creator": "Creator",
	"created": "Created", "updated": "Updated", "duedate": "Due date", "resolution": "Resolution",
	"resolutiondate": "Resolved", "labels": "Labels", "components": "Components",
	"fixVersions": "Fix versions", "versions": "Affects versions", "project": "Project",
	"parent": "Parent", "environment": "Environment", "description": "Description",
	"security": "Security Level", "watches": "Watchers", "votes": "Votes", "lastViewed": "Last Viewed",
	"timeestimate": "Remaining Estimate", "timeoriginalestimate": "Original estimate",
	"timespent": "Time Spent", "aggregatetimespent": "Σ Time Spent",
	"aggregatetimeestimate": "Σ Remaining Estimate", "aggregatetimeoriginalestimate": "Σ Original Estimate",
	"aggregateprogress": "Σ Progress", "progress": "Progress", "workratio": "Work Ratio",
	"subtasks": "Sub-tasks", "statuscategorychangedate": "Status Category Changed",
}

// NavigableColumn is a field the issue navigator and filters can show as a
// column.
type NavigableColumn struct {
	ID, Label string
}

// NavigableColumns lists the site's navigable fields: Jira's navigable system
// fields, by label, followed by the site's custom fields.
func (s *Store) NavigableColumns(ctx context.Context, workspaceID string) ([]NavigableColumn, error) {
	columns := make([]NavigableColumn, 0, len(navigableColumnLabels))
	for id, label := range navigableColumnLabels {
		columns = append(columns, NavigableColumn{ID: id, Label: label})
	}
	sort.Slice(columns, func(i, j int) bool { return columns[i].Label < columns[j].Label })
	fields, err := s.CustomFieldsForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, field := range fields {
		columns = append(columns, NavigableColumn{ID: field.ID, Label: field.Name})
	}
	return columns, nil
}

// navigableColumnIndex indexes the site's navigable columns by id.
func (s *Store) navigableColumnIndex(ctx context.Context, workspaceID string) (map[string]string, error) {
	columns, err := s.NavigableColumns(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	index := make(map[string]string, len(columns))
	for _, column := range columns {
		index[column.ID] = column.Label
	}
	return index, nil
}

// NavigableColumnLabels maps each of the site's navigable column ids to its
// label.
func (s *Store) NavigableColumnLabels(ctx context.Context, workspaceID string) (map[string]string, error) {
	return s.navigableColumnIndex(ctx, workspaceID)
}
