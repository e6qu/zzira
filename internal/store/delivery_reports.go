package store

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// Deployment frequency and cycle time are two of the four measures the DORA
// report totals. Each also answers a question of its own -- how often we ship,
// and how long work takes once it starts -- which a single number on another
// page cannot.

// DeploymentPeriod is one bucket of the deployment frequency report.
type DeploymentPeriod struct {
	// Start is the first day of the bucket and Label how it is read.
	Start      string
	Label      string
	Successful int
	Failed     int
}

// DeploymentFrequencyReport counts the deployments a project calls production
// over a window, bucketed by day, week or month.
type DeploymentFrequencyReport struct {
	Periods    []DeploymentPeriod
	Grouping   string
	WindowDays int
	Since      string
	Until      string
	Successful int
	Failed     int
	PerWeek    float64
	// EnvironmentTypes and Pipelines are the mapping the count was read
	// through, as the DORA report reports them.
	EnvironmentTypes []string
	Pipelines        int
}

// DeploymentFrequencyGroupings are the buckets the report offers.
var DeploymentFrequencyGroupings = []string{"day", "week", "month"}

func (s *Store) DeploymentFrequencyReport(ctx context.Context, workspaceID, projectID, userID string, days int, grouping string, until time.Time) (DeploymentFrequencyReport, error) {
	if days != 7 && days != 30 && days != 90 {
		return DeploymentFrequencyReport{}, fmt.Errorf("the window must be 7, 30, or 90 days")
	}
	known := false
	for _, value := range DeploymentFrequencyGroupings {
		known = known || value == grouping
	}
	if !known {
		return DeploymentFrequencyReport{}, fmt.Errorf("deployments are grouped by day, week or month")
	}
	settings, err := s.DORASettingsFor(ctx, workspaceID, projectID)
	if err != nil {
		return DeploymentFrequencyReport{}, err
	}
	until = s.windowEnd(ctx, until)
	since := until.Add(-time.Duration(days) * 24 * time.Hour)
	report := DeploymentFrequencyReport{
		Grouping: grouping, WindowDays: days, Since: since.Format("2006-01-02"), Until: until.Format("2006-01-02"),
		EnvironmentTypes: settings.EnvironmentTypes, Pipelines: len(settings.PipelineIDs), Periods: []DeploymentPeriod{},
	}
	visible := `EXISTS (
		SELECT 1 FROM issues i
		WHERE i.workspace_id=$1 AND i.project_id=$2 AND i.key=ANY(d.issue_keys)
		  AND ` + VisibleIssuePredicate("i", "$3") + `)`
	rows, err := s.Pool.Query(ctx, `
		WITH current_deployments AS (
		  SELECT DISTINCT ON (pipeline_id,environment_id,entity_sequence_number)
		         workspace_id,pipeline_id,issue_keys,state,environment_type,occurred_at AS last_updated,entity_sequence_number
		  FROM software_delivery_facts
		  WHERE workspace_id=$1 AND fact_type='deployment'
		    AND (COALESCE(cardinality($7::text[]),0)=0 OR pipeline_id=ANY($7))
		  ORDER BY pipeline_id,environment_id,entity_sequence_number,update_sequence_number DESC
		)
		SELECT d.state,d.last_updated
		FROM current_deployments d
		WHERE d.workspace_id=$1 AND d.environment_type=ANY($6)
		  AND d.last_updated >= $4 AND d.last_updated < $5 AND `+visible,
		workspaceID, projectID, userID, since, until, settings.EnvironmentTypes, settings.PipelineIDs)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	type event struct {
		state string
		at    time.Time
	}
	events := make([]event, 0)
	for rows.Next() {
		var item event
		if err := rows.Scan(&item.state, &item.at); err != nil {
			return report, err
		}
		events = append(events, event{state: item.state, at: item.at.UTC()})
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	buckets := map[string]*DeploymentPeriod{}
	order := []string{}
	for _, start := range deploymentPeriodStarts(since, until, grouping) {
		key := start.Format("2006-01-02")
		buckets[key] = &DeploymentPeriod{Start: key, Label: deploymentPeriodLabel(start, grouping)}
		order = append(order, key)
	}
	for _, item := range events {
		bucket := buckets[deploymentPeriodStart(item.at, grouping).Format("2006-01-02")]
		if bucket == nil {
			continue
		}
		switch item.state {
		case "successful":
			bucket.Successful++
			report.Successful++
		case "failed", "rolled_back":
			bucket.Failed++
			report.Failed++
		}
	}
	for _, key := range order {
		report.Periods = append(report.Periods, *buckets[key])
	}
	report.PerWeek = float64(report.Successful) * 7 / float64(days)
	return report, nil
}

// deploymentPeriodStart is the first day of the bucket a time falls in. A week
// starts on Monday, as Jira's reports read a week.
func deploymentPeriodStart(at time.Time, grouping string) time.Time {
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
	switch grouping {
	case "week":
		offset := (int(day.Weekday()) + 6) % 7
		return day.AddDate(0, 0, -offset)
	case "month":
		return time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return day
}

func deploymentPeriodStarts(since, until time.Time, grouping string) []time.Time {
	starts := []time.Time{}
	for at := deploymentPeriodStart(since, grouping); !at.After(until); {
		starts = append(starts, at)
		switch grouping {
		case "week":
			at = at.AddDate(0, 0, 7)
		case "month":
			at = at.AddDate(0, 1, 0)
		default:
			at = at.AddDate(0, 0, 1)
		}
	}
	return starts
}

func deploymentPeriodLabel(start time.Time, grouping string) string {
	switch grouping {
	case "week":
		return "Week of " + start.Format("Jan 2")
	case "month":
		return start.Format("January 2006")
	}
	return start.Format("Jan 2")
}

// CycleTimeGroup is how long one kind of work takes from the moment it starts
// to the moment it is done.
type CycleTimeGroup struct {
	Name      string
	Samples   int
	P50       int64
	P85       int64
	P95       int64
	LongestID string
}

// CycleTimeReport is the distribution of completed work's cycle times, whole
// and per work type.
type CycleTimeReport struct {
	Samples []models.CycleSample
	Groups  []CycleTimeGroup
	P50     int64
	P85     int64
	P95     int64
	Days    int
}

// CycleTimeReport measures the work a board completed in the window: the time
// from the first move into an in-progress status to the move into a done one,
// which is what the control chart plots and what Jira's cycle time report
// summarises.
func (s *Store) CycleTimeReport(ctx context.Context, board *models.Board, userID string, days int, now time.Time) (CycleTimeReport, error) {
	report := CycleTimeReport{Samples: []models.CycleSample{}, Groups: []CycleTimeGroup{}, Days: days}
	chart, err := s.ControlChart(ctx, board, userID, days, now)
	if err != nil {
		return report, err
	}
	report.Samples = chart.Samples
	work, err := s.boardWorkHistory(ctx, board, userID)
	if err != nil {
		return report, err
	}
	typeOf := make(map[string]string, len(work))
	for _, item := range work {
		typeOf[item.key] = item.issueType
	}
	byType := map[string][]int64{}
	longest := map[string]models.CycleSample{}
	all := make([]int64, 0, len(report.Samples))
	for index, sample := range report.Samples {
		name := typeOf[sample.Key]
		if name == "" {
			name = "Work"
		}
		report.Samples[index].IssueType = name
		byType[name] = append(byType[name], sample.CycleSeconds)
		all = append(all, sample.CycleSeconds)
		if current, seen := longest[name]; !seen || sample.CycleSeconds > current.CycleSeconds {
			longest[name] = report.Samples[index]
		}
	}
	report.P50, report.P85, report.P95 = cyclePercentile(all, 50), cyclePercentile(all, 85), cyclePercentile(all, 95)
	for name, values := range byType {
		report.Groups = append(report.Groups, CycleTimeGroup{
			Name: name, Samples: len(values),
			P50: cyclePercentile(values, 50), P85: cyclePercentile(values, 85), P95: cyclePercentile(values, 95),
			LongestID: longest[name].Key,
		})
	}
	sort.SliceStable(report.Groups, func(i, j int) bool { return report.Groups[i].Name < report.Groups[j].Name })
	return report, nil
}

// cyclePercentile is the nearest-rank percentile, which needs no interpolation
// to explain: the value at or below which that share of the work finished.
func cyclePercentile(values []int64, percentile int) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := (percentile*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}
