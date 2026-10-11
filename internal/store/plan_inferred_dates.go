package store

import (
	"context"
	"encoding/json"
	"github.com/e6qu/zzira/internal/models"
)

type planReleaseDates struct{ Start, End string }

// Read live releases rather than dates embedded in issue snapshots. A release
// must belong to the visible work item's project as well as its workspace.
func (s *Store) planReleaseDates(ctx context.Context, workspaceID string, items []*models.TimelineItem) (map[string]planReleaseDates, error) {
	ids, projects := []string{}, []string{}
	for _, item := range items {
		projects = append(projects, item.Issue.ProjectID)
		var releases []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(item.Issue.Fields["fixVersions"], &releases) == nil {
			for _, release := range releases {
				ids = append(ids, release.ID)
			}
		}
	}
	result := map[string]planReleaseDates{}
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT v.id,v.project_id,COALESCE(v.start_date::text,''),COALESCE(v.release_date::text,'') FROM project_versions v JOIN projects p ON p.id=v.project_id WHERE p.workspace_id=$1 AND v.id=ANY($2) AND v.project_id=ANY($3) AND NOT v.archived`, workspaceID, ids, projects)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, project string
		var dates planReleaseDates
		if err = rows.Scan(&id, &project, &dates.Start, &dates.End); err != nil {
			return nil, err
		}
		result[project+"/"+id] = dates
	}
	return result, rows.Err()
}

func fillPlanDates(item *models.TimelineItem, start, end, source string) {
	if _, valid := parseTimelineDate(start); item.StartDate == "" && valid {
		item.StartDate = planDayOf(start)
		item.InferredStart = source
	}
	if _, valid := parseTimelineDate(end); item.DueDate == "" && valid {
		item.DueDate = planDayOf(end)
		item.InferredEnd = source
	}
}

func inferPlanReleaseDates(item *models.TimelineItem, dates map[string]planReleaseDates) {
	start, end := planReleaseSpan(item.Issue, dates)
	fillPlanDates(item, start, end, "Release dates")
}

func planReleaseSpan(issue *models.Issue, dates map[string]planReleaseDates) (string, string) {
	var releases []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(issue.Fields["fixVersions"], &releases) != nil {
		return "", ""
	}
	var start, end string
	for _, release := range releases {
		bounds := dates[issue.ProjectID+"/"+release.ID]
		if _, valid := parseTimelineDate(bounds.Start); valid && (start == "" || bounds.Start < start) {
			start = bounds.Start
		}
		if _, valid := parseTimelineDate(bounds.End); valid && (end == "" || bounds.End > end) {
			end = bounds.End
		}
	}
	return start, end
}
