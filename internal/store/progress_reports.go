package store

import (
	"context"
	"sort"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

type progressSnapshot struct {
	at       time.Time
	done     bool
	estimate *float64
}

// issueReplay answers what a work item looked like at a moment: whether it
// existed yet, whether it was done, and what it was estimated at. It reads
// the action log, which is the only record of what was true then.
type issueReplay struct {
	// StateAt reports existence, doneness and estimate at a moment.
	StateAt func(issue *models.Issue, at time.Time) (exists, done bool, estimate *float64)
	// Current is the same for right now, without consulting the log.
	Current func(issue *models.Issue) (done bool, estimate *float64)
	// Earliest is when the oldest of the work items was created.
	Earliest time.Time
}

// replayIssues loads the history of the given work so a report can ask what
// each work item looked like at any moment. Without an estimation field
// every work item counts as one, which is how the board counts it.
func (s *Store) replayIssues(ctx context.Context, workspaceID string, board *models.Board, issues []*models.Issue) (issueReplay, error) {
	fieldID := board.EstimationFieldID
	estimateOf := func(raw []byte) *float64 {
		if fieldID == "" {
			one := 1.0
			return &one
		}
		return decodeEstimate(raw)
	}
	replay := issueReplay{}
	ids := make([]string, 0, len(issues))
	created := map[string]time.Time{}
	for _, issue := range issues {
		ids = append(ids, issue.ID)
		if at, err := time.Parse(time.RFC3339, issue.CreatedAt); err == nil {
			created[issue.ID] = at
			if replay.Earliest.IsZero() || at.Before(replay.Earliest) {
				replay.Earliest = at
			}
		}
	}
	history := map[string][]progressSnapshot{}
	if len(ids) > 0 {
		rows, err := s.Pool.Query(ctx, `
			SELECT a.entity_id, a.created_at, COALESCE(a.payload->'issue'->'status'->>'category','')='done',
			       a.payload->'issue'->'fields'->$3::text
			FROM actions a
			WHERE a.workspace_id=$1 AND a.entity_type=$4 AND a.entity_id=ANY($2) AND a.op='upsert' AND a.payload ? 'issue'
			ORDER BY a.seq`, workspaceID, ids, fieldID, models.EntityIssue)
		if err != nil {
			return replay, err
		}
		for rows.Next() {
			var id string
			var snapshot progressSnapshot
			var raw []byte
			if err := rows.Scan(&id, &snapshot.at, &snapshot.done, &raw); err != nil {
				rows.Close()
				return replay, err
			}
			snapshot.estimate = estimateOf(raw)
			history[id] = append(history[id], snapshot)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return replay, err
		}
	}
	replay.Current = func(issue *models.Issue) (bool, *float64) {
		return issue.Status.Category == "done", estimateOf(issue.Fields[fieldID])
	}
	replay.StateAt = func(issue *models.Issue, t time.Time) (bool, bool, *float64) {
		if at, ok := created[issue.ID]; ok && at.After(t) {
			return false, false, nil
		}
		snapshots := history[issue.ID]
		if len(snapshots) == 0 {
			done, estimate := replay.Current(issue)
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
	return replay, nil
}

// ProgressReport replays the given work day by day from start to now: the
// estimate of the work that existed and of the work done at the end of each
// day, as Jira's epic and version reports chart it. Without an estimation
// field every work item counts as one.
func (s *Store) ProgressReport(ctx context.Context, workspaceID string, board *models.Board, issues []*models.Issue, start, now time.Time) (models.ProgressReport, error) {
	now = now.UTC()
	report := models.ProgressReport{Statistic: estimateStatistic(board), Progress: VersionProgress(issues)}
	replay, err := s.replayIssues(ctx, workspaceID, board, issues)
	if err != nil {
		return report, err
	}
	if !replay.Earliest.IsZero() && (start.IsZero() || replay.Earliest.Before(start)) {
		start = replay.Earliest
	}
	if start.IsZero() || start.After(now) {
		start = now
	}
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	current, stateAt := replay.Current, replay.StateAt
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

// BurndownReport charts the given work sprint by sprint, as Jira's epic
// burndown and release burndown do: what was finished in each sprint, what
// entered its scope, and what was left when it ended. Sprints the board has
// not started yet have nothing to say and are left out.
func (s *Store) BurndownReport(ctx context.Context, workspaceID string, board *models.Board, issues []*models.Issue, now time.Time) (models.BurndownReport, error) {
	now = now.UTC()
	report := models.BurndownReport{Statistic: estimateStatistic(board)}
	replay, err := s.replayIssues(ctx, workspaceID, board, issues)
	if err != nil {
		return report, err
	}
	sprints, err := s.SprintsByBoard(ctx, board.ID)
	if err != nil {
		return report, err
	}
	started := make([]*models.Sprint, 0, len(sprints))
	for _, sprint := range sprints {
		if sprint.State == "closed" || sprint.State == "active" {
			started = append(started, sprint)
		}
	}
	windows := make(map[string][2]time.Time, len(started))
	for _, sprint := range started {
		start, end, err := s.sprintWindow(ctx, sprint, now)
		if err != nil {
			// A sprint with no start has no window to report on, which is
			// not a reason to refuse the whole chart.
			continue
		}
		windows[sprint.ID] = [2]time.Time{start, end}
	}
	ordered := make([]*models.Sprint, 0, len(windows))
	for _, sprint := range started {
		if _, ok := windows[sprint.ID]; ok {
			ordered = append(ordered, sprint)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return windows[ordered[i].ID][0].Before(windows[ordered[j].ID][0]) })
	for _, sprint := range ordered {
		window := windows[sprint.ID]
		entry := models.BurndownSprint{Sprint: *sprint}
		for _, issue := range issues {
			existedBefore, doneBefore, _ := replay.StateAt(issue, window[0])
			exists, done, estimate := replay.StateAt(issue, window[1])
			if !exists {
				continue
			}
			if !existedBefore {
				entry.Added += estimateValue(estimate)
			}
			// Work already finished before the sprint is not finished again
			// in it.
			if done && !doneBefore {
				entry.Completed += estimateValue(estimate)
			}
			if !done {
				entry.Remaining += estimateValue(estimate)
			}
		}
		report.Sprints = append(report.Sprints, entry)
	}
	for _, issue := range issues {
		done, estimate := replay.Current(issue)
		if estimate == nil {
			report.Unestimated++
		}
		if !done {
			report.Remaining += estimateValue(estimate)
		}
	}
	return report, nil
}
