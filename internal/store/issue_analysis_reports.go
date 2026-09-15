package store

import (
	"context"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// windowDays is the first and last day of a window ending today, in UTC.
func windowDays(days int, now time.Time) (time.Time, time.Time) {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return today.AddDate(0, 0, -(days - 1)), today
}

// CreatedVsResolved counts the project's work created and resolved each day of
// the window, as Jira's report does: by creation date and by the date of the
// current resolution, so reopened work no longer counts as resolved.
func (s *Store) CreatedVsResolved(ctx context.Context, workspaceID, userID, projectID string, days int, now time.Time) (models.CreatedResolvedReport, error) {
	first, last := windowDays(days, now)
	report := models.CreatedResolvedReport{}
	rows, err := s.Pool.Query(ctx, `
		SELECT d.day::date,
		       (SELECT count(*) FROM issues i WHERE i.workspace_id=$1 AND i.project_id=$2 AND i.created_at >= d.day AND i.created_at < d.day + interval '1 day' AND `+VisibleIssuePredicate("i", "$3")+`),
		       (SELECT count(*) FROM issues i WHERE i.workspace_id=$1 AND i.project_id=$2 AND i.resolved_at >= d.day AND i.resolved_at < d.day + interval '1 day' AND `+VisibleIssuePredicate("i", "$3")+`)
		FROM generate_series($4::timestamptz, $5::timestamptz, interval '1 day') d(day)
		ORDER BY d.day`, workspaceID, projectID, userID, first, last)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var day time.Time
		var point models.CreatedResolvedDay
		if err := rows.Scan(&day, &point.Created, &point.Resolved); err != nil {
			return report, err
		}
		report.CreatedTotal += point.Created
		report.ResolvedTotal += point.Resolved
		point.Date, point.CreatedTotal, point.ResolvedTotal = day.Format("2006-01-02"), report.CreatedTotal, report.ResolvedTotal
		report.Days = append(report.Days, point)
	}
	return report, rows.Err()
}

// ResolutionTime averages, for each day of the window, how long the work
// resolved that day took from creation to resolution.
func (s *Store) ResolutionTime(ctx context.Context, workspaceID, userID, projectID string, days int, now time.Time) (models.ResolutionTimeReport, error) {
	first, last := windowDays(days, now)
	report := models.ResolutionTimeReport{}
	rows, err := s.Pool.Query(ctx, `
		SELECT d.day::date, count(i.id), COALESCE(sum(EXTRACT(EPOCH FROM i.resolved_at - i.created_at))::bigint, 0)
		FROM generate_series($4::timestamptz, $5::timestamptz, interval '1 day') d(day)
		LEFT JOIN issues i ON i.workspace_id=$1 AND i.project_id=$2 AND i.resolved_at >= d.day AND i.resolved_at < d.day + interval '1 day'
		     AND `+VisibleIssuePredicate("i", "$3")+`
		GROUP BY d.day
		ORDER BY d.day`, workspaceID, projectID, userID, first, last)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var day time.Time
		var point models.ResolutionDay
		var seconds int64
		if err := rows.Scan(&day, &point.Resolved, &seconds); err != nil {
			return report, err
		}
		point.Date = day.Format("2006-01-02")
		if point.Resolved > 0 {
			point.AverageSeconds = seconds / int64(point.Resolved)
		}
		report.Resolved += point.Resolved
		total += seconds
		report.Days = append(report.Days, point)
	}
	if report.Resolved > 0 {
		report.AverageSeconds = total / int64(report.Resolved)
	}
	return report, rows.Err()
}
