package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

func (s *Store) ServiceCalendar(ctx context.Context, workspaceID, serviceDeskID string) (*models.ServiceCalendar, error) {
	calendar := &models.ServiceCalendar{}
	err := s.Pool.QueryRow(ctx, `
		SELECT c.id,c.service_desk_id,c.name,c.time_zone,c.weekdays,c.start_minute,c.end_minute
		FROM service_calendars c JOIN service_desks sd ON sd.id=c.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2`, workspaceID, serviceDeskID).Scan(
		&calendar.ID, &calendar.ServiceDeskID, &calendar.Name, &calendar.TimeZone,
		&calendar.Weekdays, &calendar.StartMinute, &calendar.EndMinute)
	if err != nil {
		return nil, err
	}
	calendar.Holidays = make(map[string]string)
	rows, err := s.Pool.Query(ctx, `SELECT holiday::text,name FROM service_calendar_holidays WHERE calendar_id=$1 ORDER BY holiday`, calendar.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var day, name string
		if err := rows.Scan(&day, &name); err != nil {
			return nil, err
		}
		calendar.Holidays[day] = name
	}
	return calendar, rows.Err()
}

func (s *Store) ServiceSLAMetrics(ctx context.Context, workspaceID, serviceDeskID string) ([]models.ServiceSLAMetric, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT m.id,m.service_desk_id,m.calendar_id,m.name,m.kind,m.goal_millis,m.position
		FROM service_sla_metrics m JOIN service_desks sd ON sd.id=m.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 ORDER BY m.position,m.id::bigint`, workspaceID, serviceDeskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	metrics := make([]models.ServiceSLAMetric, 0)
	for rows.Next() {
		var metric models.ServiceSLAMetric
		if err := rows.Scan(&metric.ID, &metric.ServiceDeskID, &metric.CalendarID, &metric.Name, &metric.Kind, &metric.GoalMillis, &metric.Position); err != nil {
			return nil, err
		}
		metrics = append(metrics, metric)
	}
	return metrics, rows.Err()
}

func (s *Store) UpdateServiceSLAMetric(ctx context.Context, workspaceID, actorID, serviceDeskID, metricID string, goalMillis int64) error {
	if goalMillis < time.Minute.Milliseconds() || goalMillis > (365*24*time.Hour).Milliseconds() {
		return fmt.Errorf("SLA goal must be between one minute and 365 days")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		UPDATE service_sla_metrics m SET goal_millis=$4
		FROM service_desks sd WHERE sd.id=m.service_desk_id
		  AND sd.workspace_id=$1 AND sd.id=$2 AND m.id=$3`, workspaceID, serviceDeskID, metricID, goalMillis)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("SLA metric does not exist")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT si.organization_id,$2,'service.sla.updated','service_sla',$4,jsonb_build_object('serviceDeskId',$3::text,'goalMillis',$5::bigint)
		FROM sites si WHERE si.workspace_id=$1`, workspaceID, actorID, serviceDeskID, metricID, goalMillis); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) UpdateServiceCalendar(ctx context.Context, workspaceID, actorID, serviceDeskID, name, timeZone string, weekdays []int16, startMinute, endMinute int16) error {
	name, timeZone = strings.TrimSpace(name), strings.TrimSpace(timeZone)
	if name == "" || len(name) > 255 {
		return fmt.Errorf("calendar name is required and accepts at most 255 characters")
	}
	if _, err := time.LoadLocation(timeZone); err != nil {
		return fmt.Errorf("calendar time zone is invalid")
	}
	if startMinute < 0 || endMinute > 1440 || startMinute >= endMinute {
		return fmt.Errorf("calendar hours are invalid")
	}
	seen := make(map[int16]bool)
	for _, weekday := range weekdays {
		if weekday < 1 || weekday > 7 || seen[weekday] {
			return fmt.Errorf("calendar weekdays must be unique ISO weekdays from 1 to 7")
		}
		seen[weekday] = true
	}
	if len(weekdays) == 0 {
		return fmt.Errorf("calendar requires at least one working day")
	}
	sort.Slice(weekdays, func(i, j int) bool { return weekdays[i] < weekdays[j] })
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		UPDATE service_calendars c SET name=$3,time_zone=$4,weekdays=$5,start_minute=$6,end_minute=$7
		FROM service_desks sd WHERE sd.id=c.service_desk_id AND sd.workspace_id=$1 AND sd.id=$2`,
		workspaceID, serviceDeskID, name, timeZone, weekdays, startMinute, endMinute)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("service calendar does not exist")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT si.organization_id,$2,'service.calendar.updated','service_calendar',c.id,
		       jsonb_build_object('serviceDeskId',$3::text,'name',$4::text,'timeZone',$5::text,'weekdays',$6::smallint[],'startMinute',$7::smallint,'endMinute',$8::smallint)
		FROM sites si JOIN service_calendars c ON c.service_desk_id=$3 WHERE si.workspace_id=$1`,
		workspaceID, actorID, serviceDeskID, name, timeZone, weekdays, startMinute, endMinute); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CompleteServiceSLA(ctx context.Context, workspaceID, requestIssueID, kind string, at time.Time) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE service_sla_cycles c SET stopped_at=$4
		FROM service_sla_metrics m,service_requests sr
		WHERE c.metric_id=m.id AND c.request_issue_id=sr.issue_id
		  AND sr.workspace_id=$1 AND sr.issue_id=$2 AND m.kind=$3 AND c.stopped_at IS NULL`, workspaceID, requestIssueID, kind, at)
	return err
}

func (s *Store) EnsureResolutionSLA(ctx context.Context, workspaceID, requestIssueID string, at time.Time) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO service_sla_cycles(request_issue_id,metric_id,cycle_number,started_at)
		SELECT sr.issue_id,m.id,COALESCE((SELECT max(c.cycle_number)+1 FROM service_sla_cycles c WHERE c.request_issue_id=sr.issue_id AND c.metric_id=m.id),1),$3
		FROM service_requests sr JOIN service_sla_metrics m ON m.service_desk_id=sr.service_desk_id AND m.kind='resolution'
		WHERE sr.workspace_id=$1 AND sr.issue_id=$2
		  AND NOT EXISTS(SELECT 1 FROM service_sla_cycles c WHERE c.request_issue_id=sr.issue_id AND c.metric_id=m.id AND c.stopped_at IS NULL)
		ON CONFLICT DO NOTHING`, workspaceID, requestIssueID, at)
	return err
}

func serviceCalendarDay(calendar *models.ServiceCalendar, value time.Time, location *time.Location) bool {
	local := value.In(location)
	weekday := int16(local.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	working := false
	for _, configured := range calendar.Weekdays {
		if configured == weekday {
			working = true
			break
		}
	}
	_, holiday := calendar.Holidays[local.Format("2006-01-02")]
	return working && !holiday
}

func serviceWindow(calendar *models.ServiceCalendar, value time.Time, location *time.Location) (time.Time, time.Time) {
	local := value.In(location)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	return day.Add(time.Duration(calendar.StartMinute) * time.Minute), day.Add(time.Duration(calendar.EndMinute) * time.Minute)
}

func serviceBusinessDuration(calendar *models.ServiceCalendar, start, end time.Time, location *time.Location) time.Duration {
	if !end.After(start) {
		return 0
	}
	localStart, localEnd := start.In(location), end.In(location)
	day := time.Date(localStart.Year(), localStart.Month(), localStart.Day(), 0, 0, 0, 0, location)
	last := time.Date(localEnd.Year(), localEnd.Month(), localEnd.Day(), 0, 0, 0, 0, location)
	var elapsed time.Duration
	for !day.After(last) {
		if serviceCalendarDay(calendar, day, location) {
			windowStart, windowEnd := serviceWindow(calendar, day, location)
			from, to := windowStart, windowEnd
			if localStart.After(from) {
				from = localStart
			}
			if localEnd.Before(to) {
				to = localEnd
			}
			if to.After(from) {
				elapsed += to.Sub(from)
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return elapsed
}

func serviceBreachTime(calendar *models.ServiceCalendar, start time.Time, goal time.Duration, location *time.Location) time.Time {
	cursor := start.In(location)
	remaining := goal
	for days := 0; days < 3660; days++ {
		if serviceCalendarDay(calendar, cursor, location) {
			windowStart, windowEnd := serviceWindow(calendar, cursor, location)
			if cursor.Before(windowStart) {
				cursor = windowStart
			}
			if cursor.Before(windowEnd) {
				available := windowEnd.Sub(cursor)
				if remaining <= available {
					return cursor.Add(remaining)
				}
				remaining -= available
			}
		}
		next := cursor.AddDate(0, 0, 1)
		cursor = time.Date(next.Year(), next.Month(), next.Day(), 0, 0, 0, 0, location)
	}
	return start.Add(goal)
}

func serviceDurationLabel(millis int64) string {
	sign := ""
	if millis < 0 {
		sign, millis = "-", -millis
	}
	minutes := millis / time.Minute.Milliseconds()
	return fmt.Sprintf("%s%dh %dm", sign, minutes/60, minutes%60)
}

func serviceWithinCalendar(calendar *models.ServiceCalendar, at time.Time, location *time.Location) bool {
	if !serviceCalendarDay(calendar, at, location) {
		return false
	}
	start, end := serviceWindow(calendar, at, location)
	local := at.In(location)
	return !local.Before(start) && local.Before(end)
}

func calculateServiceSLACycle(calendar *models.ServiceCalendar, location *time.Location, id string, started time.Time, stopped *time.Time, goalMillis int64, now time.Time) models.ServiceSLACycle {
	end := now
	if stopped != nil {
		end = *stopped
	}
	elapsed := serviceBusinessDuration(calendar, started, end, location)
	goal := time.Duration(goalMillis) * time.Millisecond
	remaining := goal - elapsed
	breach := serviceBreachTime(calendar, started, goal, location)
	within := serviceWithinCalendar(calendar, end, location)
	return models.ServiceSLACycle{
		ID: id, StartTime: started, StopTime: stopped, BreachTime: breach,
		GoalMillis: goalMillis, ElapsedMillis: elapsed.Milliseconds(), RemainingMillis: remaining.Milliseconds(),
		GoalLabel: serviceDurationLabel(goalMillis), ElapsedLabel: serviceDurationLabel(elapsed.Milliseconds()), RemainingLabel: serviceDurationLabel(remaining.Milliseconds()),
		Breached: !end.Before(breach), Paused: stopped == nil && !within, WithinCalendarHours: within,
	}
}

func (s *Store) ServiceSLAs(ctx context.Context, workspaceID, requestIssueID string, now time.Time) ([]models.ServiceSLA, error) {
	var serviceDeskID string
	if err := s.Pool.QueryRow(ctx, `SELECT service_desk_id FROM service_requests WHERE workspace_id=$1 AND issue_id=$2`, workspaceID, requestIssueID).Scan(&serviceDeskID); err != nil {
		return nil, err
	}
	calendar, err := s.ServiceCalendar(ctx, workspaceID, serviceDeskID)
	if err != nil {
		return nil, err
	}
	location, err := time.LoadLocation(calendar.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("load service calendar time zone: %w", err)
	}
	metrics, err := s.ServiceSLAMetrics(ctx, workspaceID, serviceDeskID)
	if err != nil {
		return nil, err
	}
	values := make([]models.ServiceSLA, len(metrics))
	byID := make(map[string]int, len(metrics))
	for index, metric := range metrics {
		values[index].ServiceSLAMetric = metric
		values[index].CompletedCycles = make([]models.ServiceSLACycle, 0)
		byID[metric.ID] = index
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,metric_id,started_at,stopped_at FROM service_sla_cycles WHERE request_issue_id=$1 ORDER BY metric_id::bigint,cycle_number`, requestIssueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, metricID string
		var started time.Time
		var stopped *time.Time
		if err := rows.Scan(&id, &metricID, &started, &stopped); err != nil {
			return nil, err
		}
		index, ok := byID[metricID]
		if !ok {
			continue
		}
		cycle := calculateServiceSLACycle(calendar, location, id, started, stopped, values[index].GoalMillis, now)
		if stopped == nil {
			values[index].OngoingCycle = &cycle
		} else {
			values[index].CompletedCycles = append(values[index].CompletedCycles, cycle)
		}
	}
	return values, rows.Err()
}
