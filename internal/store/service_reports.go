package store

import (
	"context"
	"fmt"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

func (s *Store) ServiceReport(ctx context.Context, workspaceID, serviceDeskID string, days int, now time.Time) (*models.ServiceReport, error) {
	if days != 7 && days != 30 && days != 90 {
		return nil, fmt.Errorf("service report window must be 7, 30, or 90 days")
	}
	from := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(days - 1))
	report := &models.ServiceReport{WindowDays: days, Daily: make([]models.ServiceReportDay, 0, days)}
	if err := s.Pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE st.category <> 'done'),
		       count(*) FILTER (WHERE st.category = 'done')
		FROM service_requests sr
		JOIN service_desks sd ON sd.id=sr.service_desk_id
		JOIN issues i ON i.id=sr.issue_id
		JOIN statuses st ON st.id=i.status_id
		WHERE sr.workspace_id=$1 AND sd.id=$2 AND i.created_at >= $3`, workspaceID, serviceDeskID, from).Scan(
		&report.TotalRequests, &report.OpenRequests, &report.ResolvedRequests); err != nil {
		return nil, err
	}
	if err := s.Pool.QueryRow(ctx, `
		SELECT count(f.rating),COALESCE(avg(f.rating),0)
		FROM service_request_feedback f
		JOIN service_requests sr ON sr.issue_id=f.request_issue_id
		JOIN issues i ON i.id=sr.issue_id
		WHERE sr.workspace_id=$1 AND sr.service_desk_id=$2 AND i.created_at >= $3`, workspaceID, serviceDeskID, from).Scan(
		&report.SatisfactionResponses, &report.AverageSatisfaction); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT d.day::date::text,count(i.id)
		FROM generate_series($3::date,$4::date,interval '1 day') d(day)
		LEFT JOIN service_requests sr ON sr.workspace_id=$1 AND sr.service_desk_id=$2
		LEFT JOIN issues i ON i.id=sr.issue_id AND i.created_at::date=d.day::date
		GROUP BY d.day ORDER BY d.day`, workspaceID, serviceDeskID, from, now.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var day models.ServiceReportDay
		if err := rows.Scan(&day.Day, &day.Count); err != nil {
			return nil, err
		}
		report.Daily = append(report.Daily, day)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	requestRows, err := s.Pool.Query(ctx, `
		SELECT sr.issue_id FROM service_requests sr JOIN issues i ON i.id=sr.issue_id
		WHERE sr.workspace_id=$1 AND sr.service_desk_id=$2 AND i.created_at >= $3`, workspaceID, serviceDeskID, from)
	if err != nil {
		return nil, err
	}
	requestIDs := make([]string, 0, report.TotalRequests)
	for requestRows.Next() {
		var issueID string
		if err := requestRows.Scan(&issueID); err != nil {
			requestRows.Close()
			return nil, err
		}
		requestIDs = append(requestIDs, issueID)
	}
	requestRows.Close()
	if err := requestRows.Err(); err != nil {
		return nil, err
	}
	for _, issueID := range requestIDs {
		slas, err := s.ServiceSLAs(ctx, workspaceID, issueID, now)
		if err != nil {
			return nil, err
		}
		breached := false
		for _, sla := range slas {
			if sla.OngoingCycle != nil && sla.OngoingCycle.Breached {
				breached = true
			}
			for _, cycle := range sla.CompletedCycles {
				breached = breached || cycle.Breached
			}
		}
		if breached {
			report.BreachedRequests++
		}
	}
	return report, nil
}
