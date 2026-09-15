package store

import (
	"context"
	"fmt"
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

// RecentlyCreated counts the project's work created each day of the window,
// split by whether it is resolved now, as Jira's recently created chart does.
func (s *Store) RecentlyCreated(ctx context.Context, workspaceID, userID, projectID string, days int, now time.Time) (models.RecentlyCreatedReport, error) {
	first, last := windowDays(days, now)
	report := models.RecentlyCreatedReport{}
	rows, err := s.Pool.Query(ctx, `
		SELECT d.day::date, count(i.id) FILTER (WHERE i.resolved_at IS NOT NULL), count(i.id) FILTER (WHERE i.resolved_at IS NULL)
		FROM generate_series($4::timestamptz, $5::timestamptz, interval '1 day') d(day)
		LEFT JOIN issues i ON i.workspace_id=$1 AND i.project_id=$2 AND i.created_at >= d.day AND i.created_at < d.day + interval '1 day'
		     AND `+VisibleIssuePredicate("i", "$3")+`
		GROUP BY d.day
		ORDER BY d.day`, workspaceID, projectID, userID, first, last)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var day time.Time
		var point models.RecentlyCreatedDay
		if err := rows.Scan(&day, &point.Resolved, &point.Unresolved); err != nil {
			return report, err
		}
		point.Date = day.Format("2006-01-02")
		report.Created += point.Resolved + point.Unresolved
		report.Resolved += point.Resolved
		report.Days = append(report.Days, point)
	}
	return report, rows.Err()
}

// AverageAge averages, at the end of each day of the window (or now, for
// today), the age of the project's work that was unresolved then. Like the
// created vs. resolved report it uses each item's current resolution.
func (s *Store) AverageAge(ctx context.Context, workspaceID, userID, projectID string, days int, now time.Time) (models.AverageAgeReport, error) {
	first, last := windowDays(days, now)
	report := models.AverageAgeReport{}
	rows, err := s.Pool.Query(ctx, `
		SELECT d.day::date, count(i.id), COALESCE(sum(EXTRACT(EPOCH FROM LEAST(d.day + interval '1 day', $6::timestamptz) - i.created_at))::bigint, 0)
		FROM generate_series($4::timestamptz, $5::timestamptz, interval '1 day') d(day)
		LEFT JOIN issues i ON i.workspace_id=$1 AND i.project_id=$2 AND i.created_at < LEAST(d.day + interval '1 day', $6::timestamptz)
		     AND (i.resolved_at IS NULL OR i.resolved_at >= LEAST(d.day + interval '1 day', $6::timestamptz))
		     AND `+VisibleIssuePredicate("i", "$3")+`
		GROUP BY d.day
		ORDER BY d.day`, workspaceID, projectID, userID, first, last, now.UTC())
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var day time.Time
		var point models.AverageAgeDay
		var seconds int64
		if err := rows.Scan(&day, &point.Unresolved, &seconds); err != nil {
			return report, err
		}
		point.Date = day.Format("2006-01-02")
		if point.Unresolved > 0 {
			point.AverageSeconds = seconds / int64(point.Unresolved)
		}
		report.Days = append(report.Days, point)
	}
	return report, rows.Err()
}

// timeSinceColumns are the date fields the time since chart counts by.
var timeSinceColumns = map[string]string{"created": "i.created_at", "updated": "i.updated_at", "resolved": "i.resolved_at"}

// TimeSince counts, for each day of the window, the project's work whose
// created, updated or resolved date fell on that day.
func (s *Store) TimeSince(ctx context.Context, workspaceID, userID, projectID, field string, days int, now time.Time) (models.TimeSinceReport, error) {
	column, ok := timeSinceColumns[field]
	if !ok {
		return models.TimeSinceReport{}, fmt.Errorf("%w: choose the created, updated or resolved date", ErrDashboardValidation)
	}
	first, last := windowDays(days, now)
	report := models.TimeSinceReport{Field: field}
	rows, err := s.Pool.Query(ctx, `
		SELECT d.day::date, count(i.id)
		FROM generate_series($4::timestamptz, $5::timestamptz, interval '1 day') d(day)
		LEFT JOIN issues i ON i.workspace_id=$1 AND i.project_id=$2 AND `+column+` >= d.day AND `+column+` < d.day + interval '1 day'
		     AND `+VisibleIssuePredicate("i", "$3")+`
		GROUP BY d.day
		ORDER BY d.day`, workspaceID, projectID, userID, first, last)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var day time.Time
		var point models.TimeSinceDay
		if err := rows.Scan(&day, &point.Count); err != nil {
			return report, err
		}
		point.Date = day.Format("2006-01-02")
		report.Total += point.Count
		report.Days = append(report.Days, point)
	}
	return report, rows.Err()
}
