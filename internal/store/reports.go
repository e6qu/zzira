package store

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// DORAReport derives a project report from current production deployments,
// linked commits, and immutable issue transitions. Issue security is applied
// before any event contributes to a metric.
func (s *Store) DORAReport(ctx context.Context, workspaceID, projectID, userID string, days int, until time.Time) (models.DORAReport, error) {
	if days != 7 && days != 30 && days != 90 {
		return models.DORAReport{}, fmt.Errorf("DORA window must be 7, 30, or 90 days")
	}
	until = until.UTC()
	since := until.Add(-time.Duration(days) * 24 * time.Hour)
	report := models.DORAReport{WindowDays: days, Since: since.Format("2006-01-02"), Until: until.Format("2006-01-02")}
	visible := `EXISTS (
		SELECT 1 FROM issues i
		WHERE i.workspace_id=$1 AND i.project_id=$2 AND i.key=ANY(d.issue_keys)
		  AND ` + VisibleIssuePredicate("i", "$3") + `)`
	rows, err := s.Pool.Query(ctx, `
		WITH current_deployments AS (
		  SELECT DISTINCT ON (pipeline_id,environment_id,entity_sequence_number)
		         workspace_id,pipeline_id,issue_keys,state,environment_type,occurred_at AS last_updated,
		         payload->>'displayName' AS display_name,payload->>'url' AS url,
		         payload->'environment'->>'displayName' AS environment_name,
		         entity_sequence_number
		  FROM software_delivery_facts
		  WHERE workspace_id=$1 AND fact_type='deployment'
		  ORDER BY pipeline_id,environment_id,entity_sequence_number,update_sequence_number DESC
		)
		SELECT d.pipeline_id,d.display_name,d.url,d.state,d.environment_name,d.environment_type,d.last_updated
		FROM current_deployments d
		WHERE d.workspace_id=$1 AND d.environment_type='production'
		  AND d.last_updated >= $4 AND d.last_updated < $5 AND `+visible+`
		ORDER BY d.last_updated DESC,d.pipeline_id,d.entity_sequence_number DESC`, workspaceID, projectID, userID, since, until)
	if err != nil {
		return models.DORAReport{}, err
	}
	type deploymentEvent struct {
		state string
		at    time.Time
	}
	events := make([]deploymentEvent, 0)
	for rows.Next() {
		var item models.DeliveryItem
		var at time.Time
		item.Kind = "deployment"
		if err := rows.Scan(&item.PipelineID, &item.DisplayName, &item.URL, &item.State, &item.EnvironmentName, &item.EnvironmentType, &at); err != nil {
			rows.Close()
			return models.DORAReport{}, err
		}
		item.LastUpdated = at.UTC().Format(time.RFC3339)
		events = append(events, deploymentEvent{state: item.State, at: at.UTC()})
		if len(report.Recent) < 10 {
			report.Recent = append(report.Recent, item)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return models.DORAReport{}, err
	}
	rows.Close()

	buckets := make(map[string]*models.DORADay)
	startDay := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, time.UTC)
	for index := 0; index < days; index++ {
		day := startDay.AddDate(0, 0, index)
		key := day.Format("2006-01-02")
		x := 8 + index*22
		buckets[key] = &models.DORADay{Date: key, Label: day.Format("Jan 2"), X: x, FailureX: x + 8}
	}
	for _, event := range events {
		day := buckets[event.at.Format("2006-01-02")]
		if day == nil {
			continue
		}
		switch event.state {
		case "successful":
			report.DeploymentFrequency++
			report.TotalChanges++
			day.Deployments++
		case "failed", "rolled_back":
			report.FailedChanges++
			report.TotalChanges++
			day.Failures++
		}
	}
	report.DeploymentsPerWeek = float64(report.DeploymentFrequency) * 7 / float64(days)
	if report.TotalChanges > 0 {
		report.ChangeFailureRate = float64(report.FailedChanges) * 100 / float64(report.TotalChanges)
	}
	maxEvents := 1
	for _, day := range buckets {
		if total := day.Deployments + day.Failures; total > maxEvents {
			maxEvents = total
		}
	}
	for index := 0; index < days; index++ {
		day := buckets[startDay.AddDate(0, 0, index).Format("2006-01-02")]
		day.DeploymentHeight = day.Deployments * 100 / maxEvents
		day.FailureHeight = day.Failures * 100 / maxEvents
		day.DeploymentY = 120 - day.DeploymentHeight
		day.FailureY = 120 - day.FailureHeight
		report.Daily = append(report.Daily, *day)
	}
	report.ChartWidth = 16 + days*22

	leads, err := s.doraLeadTimes(ctx, workspaceID, projectID, userID, since, until)
	if err != nil {
		return models.DORAReport{}, err
	}
	report.LeadTimeSamples = len(leads)
	report.LeadTimeSeconds = medianSeconds(leads)
	report.LeadTimeDisplay = reportDuration(report.LeadTimeSeconds, report.LeadTimeSamples)
	recoveries, err := s.doraRecoveryTimes(ctx, workspaceID, projectID, userID, since, until)
	if err != nil {
		return models.DORAReport{}, err
	}
	report.RecoveredIncidents = len(recoveries)
	report.MTTRSeconds = medianSeconds(recoveries)
	report.MTTRDisplay = reportDuration(report.MTTRSeconds, report.RecoveredIncidents)
	return report, nil
}

func (s *Store) doraLeadTimes(ctx context.Context, workspaceID, projectID, userID string, since, until time.Time) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH visible_keys AS (
		  SELECT i.key FROM issues i
		  WHERE i.workspace_id=$1 AND i.project_id=$2 AND `+VisibleIssuePredicate("i", "$3")+`
		), deployments AS (
		  SELECT DISTINCT ON (pipeline_id,environment_id,entity_sequence_number)
		         workspace_id,issue_keys,state,environment_type,occurred_at AS last_updated
		  FROM software_delivery_facts
		  WHERE workspace_id=$1 AND fact_type='deployment'
		  ORDER BY pipeline_id,environment_id,entity_sequence_number,update_sequence_number DESC
		)
		SELECT EXTRACT(EPOCH FROM (MIN(d.last_updated)-e.occurred_at))::bigint
		FROM development_entities e
		JOIN deployments d ON d.workspace_id=e.workspace_id
		 AND d.environment_type='production' AND d.state='successful'
		 AND d.last_updated >= e.occurred_at AND d.last_updated >= $4 AND d.last_updated < $5
		 AND d.issue_keys && e.issue_keys
		WHERE e.workspace_id=$1 AND e.entity_type='commit' AND e.occurred_at IS NOT NULL
		  AND EXISTS (SELECT 1 FROM visible_keys v WHERE v.key=ANY(e.issue_keys) AND v.key=ANY(d.issue_keys))
		GROUP BY e.repository_id,e.entity_id,e.occurred_at`, workspaceID, projectID, userID, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]int64, 0)
	for rows.Next() {
		var value int64
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		if value >= 0 {
			values = append(values, value)
		}
	}
	return values, rows.Err()
}

func (s *Store) doraRecoveryTimes(ctx context.Context, workspaceID, projectID, userID string, since, until time.Time) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH incidents AS (
		  SELECT i.id,MIN(created.created_at) AS opened_at
		  FROM issues i
		  JOIN actions created ON created.workspace_id=i.workspace_id AND created.entity_type='issue' AND created.entity_id=i.id AND created.op='upsert'
		  WHERE i.workspace_id=$1 AND i.project_id=$2 AND 'incident'=ANY(i.labels)
		    AND `+VisibleIssuePredicate("i", "$3")+`
		  GROUP BY i.id
		), recovered AS (
		  SELECT incident.id,incident.opened_at,MIN(change.created_at) AS recovered_at
		  FROM incidents incident
		  JOIN actions change ON change.workspace_id=$1 AND change.entity_type='issue' AND change.entity_id=incident.id
		  JOIN statuses status ON status.id=change.payload->'diff'->'status'->>'to' AND status.category='done'
		  WHERE change.created_at >= $4 AND change.created_at < $5
		  GROUP BY incident.id,incident.opened_at
		)
		SELECT EXTRACT(EPOCH FROM (recovered_at-opened_at))::bigint FROM recovered WHERE recovered_at >= opened_at`, workspaceID, projectID, userID, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]int64, 0)
	for rows.Next() {
		var value int64
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func medianSeconds(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	middle := len(values) / 2
	if len(values)%2 == 1 {
		return values[middle]
	}
	return (values[middle-1] + values[middle]) / 2
}

func reportDuration(seconds int64, samples int) string {
	if samples == 0 {
		return "No data"
	}
	duration := time.Duration(seconds) * time.Second
	if duration >= 48*time.Hour {
		return fmt.Sprintf("%.1f days", duration.Hours()/24)
	}
	if duration >= time.Hour {
		return fmt.Sprintf("%.1f hours", duration.Hours())
	}
	return fmt.Sprintf("%.0f minutes", duration.Minutes())
}
