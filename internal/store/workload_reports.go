package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
)

// ErrReportValidation is a report asked for something it does not offer.
var ErrReportValidation = errors.New("invalid report")

// unresolvedInProject is the work a workload report counts: what the viewer
// can browse, in this project, that nobody has resolved yet.
const unresolvedInProject = `i.workspace_id=$1 AND i.project_id=$2 AND i.resolution_id IS NULL`

// UserWorkload groups a project's unresolved work by the person holding it,
// as Jira's user workload report does, with the time each of them has left.
// Work nobody is assigned is its own row, because it is workload the project
// still carries.
func (s *Store) UserWorkload(ctx context.Context, workspaceID, userID, projectID string) (models.WorkloadReport, error) {
	report := models.WorkloadReport{Rows: []models.WorkloadRow{}}
	rows, err := s.Pool.Query(ctx, `
		SELECT COALESCE(i.assignee_id,''), COALESCE(u.display_name,''), count(*),
		       COALESCE(sum(i.remaining_estimate_seconds),0)::bigint,
		       count(*) FILTER (WHERE i.remaining_estimate_seconds IS NOT NULL)
		FROM issues i
		LEFT JOIN users u ON u.id=i.assignee_id
		WHERE `+unresolvedInProject+` AND `+VisibleIssuePredicate("i", "$3")+`
		GROUP BY 1, 2
		ORDER BY 4 DESC, 3 DESC, 2, 1`, workspaceID, projectID, userID)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var row models.WorkloadRow
		var estimated int
		if err := rows.Scan(&row.Key, &row.Name, &row.Issues, &row.RemainingSeconds, &estimated); err != nil {
			return report, err
		}
		if row.Name == "" {
			row.Name = "Unassigned"
		}
		report.Rows = append(report.Rows, row)
		report.Issues += row.Issues
		report.RemainingSeconds += row.RemainingSeconds
		report.Estimated += estimated
	}
	return report, rows.Err()
}

// VersionWorkload is the same question asked of one version's unresolved
// work: who holds it, and what kind of work it is.
func (s *Store) VersionWorkload(ctx context.Context, workspaceID, userID, projectID, versionID string) (models.WorkloadReport, error) {
	report := models.WorkloadReport{Rows: []models.WorkloadRow{}, Types: []models.WorkloadRow{}}
	inVersion := `EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(i.fields->'fixVersions')='array' THEN i.fields->'fixVersions' ELSE '[]'::jsonb END) fix_version WHERE fix_version->>'id'=$4)`
	group := func(dimension, join string, into *[]models.WorkloadRow, fallback string) error {
		rows, err := s.Pool.Query(ctx, `
			SELECT `+dimension+`, count(*), COALESCE(sum(i.remaining_estimate_seconds),0)::bigint,
			       count(*) FILTER (WHERE i.remaining_estimate_seconds IS NOT NULL)
			FROM issues i `+join+`
			WHERE `+unresolvedInProject+` AND `+inVersion+` AND `+VisibleIssuePredicate("i", "$3")+`
			GROUP BY 1, 2
			ORDER BY 4 DESC, 3 DESC, 2, 1`, workspaceID, projectID, userID, versionID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row models.WorkloadRow
			var estimated int
			if err := rows.Scan(&row.Key, &row.Name, &row.Issues, &row.RemainingSeconds, &estimated); err != nil {
				return err
			}
			if row.Name == "" {
				row.Name = fallback
			}
			*into = append(*into, row)
		}
		return rows.Err()
	}
	if err := group(`COALESCE(i.assignee_id,''), COALESCE(u.display_name,'')`, `LEFT JOIN users u ON u.id=i.assignee_id`, &report.Rows, "Unassigned"); err != nil {
		return report, err
	}
	if err := group(`it.id, COALESCE(ito.name, it.name)`,
		`JOIN issue_types it ON it.id=i.issuetype_id
		 LEFT JOIN issue_metadata_overrides ito ON ito.workspace_id=i.workspace_id AND ito.entity_type='issuetype' AND ito.entity_id=it.id`,
		&report.Types, "Work"); err != nil {
		return report, err
	}
	for _, row := range report.Rows {
		report.Issues += row.Issues
		report.RemainingSeconds += row.RemainingSeconds
	}
	if err := s.Pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE i.remaining_estimate_seconds IS NOT NULL)
		FROM issues i
		WHERE `+unresolvedInProject+` AND `+inVersion+` AND `+VisibleIssuePredicate("i", "$3"),
		workspaceID, projectID, userID, versionID).Scan(&report.Estimated); err != nil {
		return report, err
	}
	return report, nil
}

// TimeTracking lists a project's unresolved work with what it was estimated
// to take, what is left and what it has cost, which is Jira's time tracking
// report. A version narrows it to the work fixed in that version.
func (s *Store) TimeTracking(ctx context.Context, workspaceID, userID, projectID, versionID string) (models.TimeTrackingReport, error) {
	report := models.TimeTrackingReport{Rows: []models.TimeTrackingRow{}}
	where := unresolvedInProject
	args := []any{workspaceID, projectID, userID}
	if versionID != "" {
		args = append(args, versionID)
		where += ` AND EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(i.fields->'fixVersions')='array' THEN i.fields->'fixVersions' ELSE '[]'::jsonb END) fix_version WHERE fix_version->>'id'=$4)`
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT i.key, i.summary,
		       COALESCE(i.original_estimate_seconds,0)::bigint,
		       COALESCE(i.remaining_estimate_seconds,0)::bigint,
		       COALESCE((SELECT sum(w.time_spent_seconds) FROM worklogs w WHERE w.issue_id=i.id),0)::bigint
		FROM issues i
		WHERE `+where+` AND `+VisibleIssuePredicate("i", "$3")+`
		ORDER BY i.key`, args...)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var row models.TimeTrackingRow
		if err := rows.Scan(&row.Key, &row.Summary, &row.OriginalSeconds, &row.RemainingSeconds, &row.SpentSeconds); err != nil {
			return report, err
		}
		// Jira's accuracy is what the estimate has left over once the work
		// done and the work left are taken off it.
		row.AccuracySeconds = row.OriginalSeconds - (row.SpentSeconds + row.RemainingSeconds)
		report.Rows = append(report.Rows, row)
		report.OriginalSeconds += row.OriginalSeconds
		report.RemainingSeconds += row.RemainingSeconds
		report.SpentSeconds += row.SpentSeconds
		report.AccuracySeconds += row.AccuracySeconds
	}
	return report, rows.Err()
}

// groupByFields are the fields the single level group by report groups on,
// with the expression that names each group.
var groupByFields = map[string]struct{ key, name, join string }{
	"assignee": {"COALESCE(i.assignee_id,'')", "COALESCE(u.display_name,'')", "LEFT JOIN users u ON u.id=i.assignee_id"},
	"reporter": {"COALESCE(i.reporter_id,'')", "COALESCE(r.display_name,'')", "LEFT JOIN users r ON r.id=i.reporter_id"},
	"status":   {"st.id", "st.name", "JOIN statuses st ON st.id=i.status_id"},
	"priority": {"COALESCE(pr.id,'')", "COALESCE(pr.name,'')", "LEFT JOIN priorities pr ON pr.id=i.priority_id"},
	"issuetype": {"it.id", "COALESCE(ito.name, it.name)",
		`JOIN issue_types it ON it.id=i.issuetype_id
		 LEFT JOIN issue_metadata_overrides ito ON ito.workspace_id=i.workspace_id AND ito.entity_type='issuetype' AND ito.entity_id=it.id`},
	"resolution": {"COALESCE(res.id,'')", "COALESCE(res.name,'')", "LEFT JOIN resolutions res ON res.id=i.resolution_id"},
}

// GroupByFields are the fields a single level group by report offers, in the
// order the page lists them.
var GroupByFields = []string{"assignee", "issuetype", "status", "priority", "resolution", "reporter"}

// GroupBy counts a project's work by one field, which is Jira's single level
// group by report. Every work item is counted, resolved or not, because the
// report is about how the project's work divides rather than what is left.
func (s *Store) GroupBy(ctx context.Context, workspaceID, userID, projectID, field string) (models.GroupByReport, error) {
	report := models.GroupByReport{Field: field, Rows: []models.GroupByRow{}}
	grouping, ok := groupByFields[field]
	if !ok {
		return report, fmt.Errorf("%w: a report groups by assignee, work type, status, priority, resolution or reporter", ErrReportValidation)
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT `+grouping.key+`, `+grouping.name+`, count(*)
		FROM issues i `+grouping.join+`
		WHERE i.workspace_id=$1 AND i.project_id=$2 AND `+VisibleIssuePredicate("i", "$3")+`
		GROUP BY 1, 2
		ORDER BY 3 DESC, 2, 1`, workspaceID, projectID, userID)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var row models.GroupByRow
		if err := rows.Scan(&row.Key, &row.Name, &row.Issues); err != nil {
			return report, err
		}
		if row.Name == "" {
			row.Name = groupByFallback(field)
		}
		report.Rows = append(report.Rows, row)
		report.Issues += row.Issues
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	for index := range report.Rows {
		if report.Issues > 0 {
			report.Rows[index].Percent = int(float64(report.Rows[index].Issues)*100/float64(report.Issues) + 0.5)
		}
	}
	return report, nil
}

// groupByFallback names the group a work item falls in when the field it is
// grouped by holds nothing.
func groupByFallback(field string) string {
	switch field {
	case "assignee":
		return "Unassigned"
	case "priority":
		return "No priority"
	case "resolution":
		return "Unresolved"
	}
	return "None"
}
