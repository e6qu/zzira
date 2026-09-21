package store

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// DORAReport derives a project report from the deployments the project counts
// as production, linked commits, and immutable issue transitions. Issue
// security is applied before any event contributes to a metric.
func (s *Store) DORAReport(ctx context.Context, workspaceID, projectID, userID string, days int, until time.Time) (models.DORAReport, error) {
	if days != 7 && days != 30 && days != 90 {
		return models.DORAReport{}, fmt.Errorf("DORA window must be 7, 30, or 90 days")
	}
	settings, err := s.DORASettingsFor(ctx, workspaceID, projectID)
	if err != nil {
		return models.DORAReport{}, err
	}
	excluded, err := s.DORAExcludedPeriods(ctx, workspaceID, projectID)
	if err != nil {
		return models.DORAReport{}, err
	}
	until = until.UTC()
	since := until.Add(-time.Duration(days) * 24 * time.Hour)
	report := models.DORAReport{
		WindowDays: days, Since: since.Format("2006-01-02"), Until: until.Format("2006-01-02"),
		EnvironmentTypes: settings.EnvironmentTypes, Pipelines: len(settings.PipelineIDs), ExcludedPeriods: len(excluded),
	}
	// A day inside an excluded period is not delivery: its deployments and the
	// incidents opened in it are left out, as a code freeze is in Jira.
	inExcluded := excludedDay(excluded)
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
		WHERE d.workspace_id=$1 AND d.environment_type=ANY($6)
		  AND (COALESCE(cardinality($7::text[]),0)=0 OR d.pipeline_id=ANY($7))
		  AND d.last_updated >= $4 AND d.last_updated < $5 AND `+visible+`
		ORDER BY d.last_updated DESC,d.pipeline_id,d.entity_sequence_number DESC`,
		workspaceID, projectID, userID, since, until, settings.EnvironmentTypes, settings.PipelineIDs)
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
		if inExcluded(at.UTC()) {
			continue
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

	leads, err := s.doraLeadTimes(ctx, workspaceID, projectID, userID, since, until, settings)
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

// excludedDay answers whether a moment falls inside one of the periods a
// project leaves out of its metrics.
func excludedDay(periods []DORAExcludedPeriod) func(time.Time) bool {
	if len(periods) == 0 {
		return func(time.Time) bool { return false }
	}
	type window struct{ start, end time.Time }
	windows := make([]window, 0, len(periods))
	for _, period := range periods {
		start, err := time.Parse("2006-01-02", period.StartsOn)
		if err != nil {
			continue
		}
		end, err := time.Parse("2006-01-02", period.EndsOn)
		if err != nil {
			continue
		}
		windows = append(windows, window{start: start, end: end.AddDate(0, 0, 1)})
	}
	return func(at time.Time) bool {
		at = at.UTC()
		for _, w := range windows {
			if !at.Before(w.start) && at.Before(w.end) {
				return true
			}
		}
		return false
	}
}

func (s *Store) doraLeadTimes(ctx context.Context, workspaceID, projectID, userID string, since, until time.Time, settings DORASettings) ([]int64, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH visible_keys AS (
		  SELECT i.key FROM issues i
		  WHERE i.workspace_id=$1 AND i.project_id=$2 AND `+VisibleIssuePredicate("i", "$3")+`
		), deployments AS (
		  SELECT DISTINCT ON (pipeline_id,environment_id,entity_sequence_number)
		         workspace_id,issue_keys,state,environment_type,occurred_at AS last_updated
		  FROM software_delivery_facts
		  WHERE workspace_id=$1 AND fact_type='deployment'
		    AND (COALESCE(cardinality($7::text[]),0)=0 OR pipeline_id=ANY($7))
		  ORDER BY pipeline_id,environment_id,entity_sequence_number,update_sequence_number DESC
		)
		SELECT EXTRACT(EPOCH FROM (MIN(d.last_updated)-e.occurred_at))::bigint
		FROM development_entities e
		JOIN deployments d ON d.workspace_id=e.workspace_id
		 AND d.environment_type=ANY($6) AND d.state='successful'
		 AND d.last_updated >= e.occurred_at AND d.last_updated >= $4 AND d.last_updated < $5
		 AND NOT EXISTS (SELECT 1 FROM project_dora_excluded_periods x
		   WHERE x.project_id=$2 AND d.last_updated >= x.starts_on AND d.last_updated < x.ends_on + 1)
		 AND d.issue_keys && e.issue_keys
		WHERE e.workspace_id=$1 AND e.entity_type='commit' AND e.occurred_at IS NOT NULL
		  AND EXISTS (SELECT 1 FROM visible_keys v WHERE v.key=ANY(e.issue_keys) AND v.key=ANY(d.issue_keys))
		GROUP BY e.repository_id,e.entity_id,e.occurred_at`,
		workspaceID, projectID, userID, since, until, settings.EnvironmentTypes, settings.PipelineIDs)
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
		  -- An incident is a Jira Service Management incident request, which
		  -- is how every other incident reader here knows one; a label anyone
		  -- can add or remove is not.
		  JOIN service_request_operations operation ON operation.request_issue_id=i.id AND operation.kind='incident'
		  WHERE i.workspace_id=$1 AND i.project_id=$2
		    AND `+VisibleIssuePredicate("i", "$3")+`
		  GROUP BY i.id
		), counted AS (
		  -- An incident opened inside an excluded period is not counted, the
		  -- way a deployment made in one is not.
		  SELECT * FROM incidents incident WHERE NOT EXISTS (
		    SELECT 1 FROM project_dora_excluded_periods x
		    WHERE x.project_id=$2 AND incident.opened_at >= x.starts_on AND incident.opened_at < x.ends_on + 1)
		), recovered AS (
		  SELECT incident.id,incident.opened_at,MIN(change.created_at) AS recovered_at
		  FROM counted incident
		  -- An incident is restored when it is resolved: the first change
		  -- that gave it a resolution.
		  JOIN actions change ON change.workspace_id=$1 AND change.entity_type='issue' AND change.entity_id=incident.id
		  WHERE COALESCE(change.payload->'diff'->'resolution'->>'to','') <> ''
		    AND change.created_at >= $4 AND change.created_at < $5
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
