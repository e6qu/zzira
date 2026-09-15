package store

import (
	"context"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

type progressSnapshot struct {
	at       time.Time
	done     bool
	estimate *float64
}

// ProgressReport replays the given work day by day from start to now: the
// estimate of the work that existed and of the work done at the end of each
// day, as Jira's epic and version reports chart it. Without an estimation
// field every work item counts as one.
func (s *Store) ProgressReport(ctx context.Context, workspaceID string, board *models.Board, issues []*models.Issue, start, now time.Time) (models.ProgressReport, error) {
	now = now.UTC()
	report := models.ProgressReport{Statistic: estimateStatistic(board), Progress: VersionProgress(issues)}
	fieldID := board.EstimationFieldID
	estimateOf := func(raw []byte) *float64 {
		if fieldID == "" {
			one := 1.0
			return &one
		}
		return decodeEstimate(raw)
	}
	ids := make([]string, 0, len(issues))
	created := map[string]time.Time{}
	for _, issue := range issues {
		ids = append(ids, issue.ID)
		if at, err := time.Parse(time.RFC3339, issue.CreatedAt); err == nil {
			created[issue.ID] = at
			if start.IsZero() || at.Before(start) {
				start = at
			}
		}
	}
	if start.IsZero() || start.After(now) {
		start = now
	}
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	history := map[string][]progressSnapshot{}
	if len(ids) > 0 {
		rows, err := s.Pool.Query(ctx, `
			SELECT a.entity_id, a.created_at, COALESCE(a.payload->'issue'->'status'->>'category','')='done',
			       a.payload->'issue'->'fields'->$3::text
			FROM actions a
			WHERE a.workspace_id=$1 AND a.entity_type=$4 AND a.entity_id=ANY($2) AND a.op='upsert' AND a.payload ? 'issue'
			ORDER BY a.seq`, workspaceID, ids, fieldID, models.EntityIssue)
		if err != nil {
			return report, err
		}
		for rows.Next() {
			var id string
			var snapshot progressSnapshot
			var raw []byte
			if err := rows.Scan(&id, &snapshot.at, &snapshot.done, &raw); err != nil {
				rows.Close()
				return report, err
			}
			snapshot.estimate = estimateOf(raw)
			history[id] = append(history[id], snapshot)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return report, err
		}
	}
	current := func(issue *models.Issue) (bool, *float64) {
		return issue.Status.Category == "done", estimateOf(issue.Fields[fieldID])
	}
	stateAt := func(issue *models.Issue, t time.Time) (bool, bool, *float64) {
		if at, ok := created[issue.ID]; ok && at.After(t) {
			return false, false, nil
		}
		snapshots := history[issue.ID]
		if len(snapshots) == 0 {
			done, estimate := current(issue)
			return true, done, estimate
		}
		state := snapshots[0]
		for _, snapshot := range snapshots {
			if snapshot.at.After(t) {
				break
			}
			state = snapshot
		}
		return true, state.done, state.estimate
	}
	for day := start; !day.After(now); day = day.AddDate(0, 0, 1) {
		end := day.AddDate(0, 0, 1).Add(-time.Nanosecond)
		if end.After(now) {
			end = now
		}
		point := models.ProgressPoint{Date: day.Format("2006-01-02")}
		for _, issue := range issues {
			if exists, done, estimate := stateAt(issue, end); exists {
				point.Total += estimateValue(estimate)
				if done {
					point.Completed += estimateValue(estimate)
				}
			}
		}
		report.Points = append(report.Points, point)
	}
	for _, issue := range issues {
		done, estimate := current(issue)
		row := models.SprintReportIssue{Key: issue.Key, Summary: issue.Summary, IssueType: issue.IssueType.Name, Status: issue.Status.Name, EstimateStart: formatEstimate(estimate), EstimateEnd: formatEstimate(estimate)}
		if estimate == nil {
			report.Unestimated++
		}
		report.TotalEstimate += estimateValue(estimate)
		if done {
			report.CompletedEstimate += estimateValue(estimate)
			report.Completed = append(report.Completed, row)
		} else {
			report.Incomplete = append(report.Incomplete, row)
		}
	}
	report.Start, report.End = start.Format(time.RFC3339), now.Format(time.RFC3339)
	return report, nil
}
