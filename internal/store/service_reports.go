package store

import (
	"context"
	"fmt"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

func (s *Store) ServiceRequestChannels(ctx context.Context, workspaceID, serviceDeskID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT sr.channel FROM service_requests sr
		JOIN service_desks sd ON sd.id=sr.service_desk_id
		WHERE sr.workspace_id=$1 AND sd.id=$2 ORDER BY sr.channel`, workspaceID, serviceDeskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	channels := []string{}
	for rows.Next() {
		var channel string
		if err := rows.Scan(&channel); err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	return channels, rows.Err()
}

func (s *Store) ServiceReport(ctx context.Context, workspaceID, serviceDeskID string, days int, now time.Time) (*models.ServiceReport, error) {
	return s.ServiceReportFiltered(ctx, workspaceID, serviceDeskID, models.ServiceReportFilter{}, days, now)
}

func (s *Store) ServiceReportFiltered(ctx context.Context, workspaceID, serviceDeskID string, filter models.ServiceReportFilter, days int, now time.Time) (*models.ServiceReport, error) {
	if days != 7 && days != 30 && days != 90 {
		return nil, fmt.Errorf("service report window must be 7, 30, or 90 days")
	}
	if filter.Status != "" && filter.Status != "open" && filter.Status != "resolved" {
		return nil, fmt.Errorf("service report status must be open or resolved")
	}
	from := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(days - 1))
	report := &models.ServiceReport{WindowDays: days, Daily: make([]models.ServiceReportDay, 0), RequestTypes: []models.ServiceReportSegment{}, Channels: []models.ServiceReportSegment{}}
	if err := s.Pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE st.category <> 'done'),
		       count(*) FILTER (WHERE st.category = 'done')
		FROM service_requests sr
		JOIN service_desks sd ON sd.id=sr.service_desk_id
		JOIN issues i ON i.id=sr.issue_id
		JOIN statuses st ON st.id=i.status_id
		WHERE sr.workspace_id=$1 AND sd.id=$2 AND i.created_at >= $3
		  AND ($4='' OR sr.request_type_id=$4) AND ($5='' OR sr.channel=$5)
		  AND ($6='' OR ($6='open' AND st.category<>'done') OR ($6='resolved' AND st.category='done'))`, workspaceID, serviceDeskID, from, filter.RequestTypeID, filter.Channel, filter.Status).Scan(
		&report.TotalRequests, &report.OpenRequests, &report.ResolvedRequests); err != nil {
		return nil, err
	}
	if err := s.Pool.QueryRow(ctx, `
		SELECT count(f.rating),COALESCE(avg(f.rating),0)
		FROM service_request_feedback f
		JOIN service_requests sr ON sr.issue_id=f.request_issue_id
		JOIN issues i ON i.id=sr.issue_id
		JOIN statuses st ON st.id=i.status_id
		WHERE sr.workspace_id=$1 AND sr.service_desk_id=$2 AND i.created_at >= $3
		  AND ($4='' OR sr.request_type_id=$4) AND ($5='' OR sr.channel=$5)
		  AND ($6='' OR ($6='open' AND st.category<>'done') OR ($6='resolved' AND st.category='done'))`, workspaceID, serviceDeskID, from, filter.RequestTypeID, filter.Channel, filter.Status).Scan(
		&report.SatisfactionResponses, &report.AverageSatisfaction); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT d.day::date::text,count(filtered.created_at)
		FROM generate_series($3::date,$4::date,interval '1 day') d(day)
		LEFT JOIN (
		  SELECT i.created_at FROM service_requests sr JOIN issues i ON i.id=sr.issue_id JOIN statuses st ON st.id=i.status_id
		  WHERE sr.workspace_id=$1 AND sr.service_desk_id=$2 AND i.created_at >= $3
		    AND ($5='' OR sr.request_type_id=$5) AND ($6='' OR sr.channel=$6)
		    AND ($7='' OR ($7='open' AND st.category<>'done') OR ($7='resolved' AND st.category='done'))
		) filtered ON filtered.created_at::date=d.day::date
		GROUP BY d.day ORDER BY d.day`, workspaceID, serviceDeskID, from, now.UTC(), filter.RequestTypeID, filter.Channel, filter.Status)
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
		SELECT sr.issue_id FROM service_requests sr JOIN issues i ON i.id=sr.issue_id JOIN statuses st ON st.id=i.status_id
		WHERE sr.workspace_id=$1 AND sr.service_desk_id=$2 AND i.created_at >= $3
		  AND ($4='' OR sr.request_type_id=$4) AND ($5='' OR sr.channel=$5)
		  AND ($6='' OR ($6='open' AND st.category<>'done') OR ($6='resolved' AND st.category='done'))`, workspaceID, serviceDeskID, from, filter.RequestTypeID, filter.Channel, filter.Status)
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
	typeRows, err := s.Pool.Query(ctx, `
		SELECT rt.id,rt.name,count(*) FROM service_requests sr
		JOIN service_request_types rt ON rt.id=sr.request_type_id
		JOIN issues i ON i.id=sr.issue_id JOIN statuses st ON st.id=i.status_id
		WHERE sr.workspace_id=$1 AND sr.service_desk_id=$2 AND i.created_at >= $3
		  AND ($4='' OR sr.request_type_id=$4) AND ($5='' OR sr.channel=$5)
		  AND ($6='' OR ($6='open' AND st.category<>'done') OR ($6='resolved' AND st.category='done'))
		GROUP BY rt.id,rt.name ORDER BY count(*) DESC,rt.name`, workspaceID, serviceDeskID, from, filter.RequestTypeID, filter.Channel, filter.Status)
	if err != nil {
		return nil, err
	}
	for typeRows.Next() {
		var segment models.ServiceReportSegment
		if err := typeRows.Scan(&segment.ID, &segment.Name, &segment.Count); err != nil {
			typeRows.Close()
			return nil, err
		}
		report.RequestTypes = append(report.RequestTypes, segment)
	}
	typeRows.Close()
	if err := typeRows.Err(); err != nil {
		return nil, err
	}
	channelRows, err := s.Pool.Query(ctx, `
		SELECT sr.channel,sr.channel,count(*) FROM service_requests sr
		JOIN issues i ON i.id=sr.issue_id JOIN statuses st ON st.id=i.status_id
		WHERE sr.workspace_id=$1 AND sr.service_desk_id=$2 AND i.created_at >= $3
		  AND ($4='' OR sr.request_type_id=$4) AND ($5='' OR sr.channel=$5)
		  AND ($6='' OR ($6='open' AND st.category<>'done') OR ($6='resolved' AND st.category='done'))
		GROUP BY sr.channel ORDER BY count(*) DESC,sr.channel`, workspaceID, serviceDeskID, from, filter.RequestTypeID, filter.Channel, filter.Status)
	if err != nil {
		return nil, err
	}
	for channelRows.Next() {
		var segment models.ServiceReportSegment
		if err := channelRows.Scan(&segment.ID, &segment.Name, &segment.Count); err != nil {
			channelRows.Close()
			return nil, err
		}
		report.Channels = append(report.Channels, segment)
	}
	channelRows.Close()
	if err := channelRows.Err(); err != nil {
		return nil, err
	}
	return report, nil
}
