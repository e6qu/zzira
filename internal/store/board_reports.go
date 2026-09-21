package store

import (
	"context"
	"sort"
	"time"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
)

// boardWork is a work item a board shows, with the status it held after each
// recorded change.
type boardWork struct {
	key, summary string
	// issueType is the work type's name, which the cycle time report groups
	// by: how long a bug takes is not how long a story takes.
	issueType string
	created   time.Time
	statusID  string
	category  string
	changes   []statusChange
}

type statusChange struct {
	at       time.Time
	statusID string
	category string
}

// statusAt is the item's status at t, or false before the item existed.
func (w *boardWork) statusAt(t time.Time) (string, bool) {
	if w.created.After(t) {
		return "", false
	}
	if len(w.changes) == 0 {
		return w.statusID, true
	}
	status := w.changes[0].statusID
	for _, change := range w.changes {
		if change.at.After(t) {
			break
		}
		status = change.statusID
	}
	return status, true
}

// boardWorkHistory loads the work the board's filter shows the user, with each
// item's status history from the action log.
func (s *Store) boardWorkHistory(ctx context.Context, board *models.Board, userID string) ([]*boardWork, error) {
	query, err := boardFilterQuery(board, nil, "")
	if err != nil {
		return nil, err
	}
	if err := s.ExpandAppJQL(ctx, board.WorkspaceID, query); err != nil {
		return nil, err
	}
	resolver, err := s.JQLResolver(ctx, board.WorkspaceID)
	if err != nil {
		return nil, err
	}
	compiled := jql.CompileAt(query, userID, resolver, 3)
	if compiled.Err != nil {
		return nil, compiled.Err
	}
	args := append([]any{board.ProjectID, userID}, compiled.Args...)
	rows, err := s.Pool.Query(ctx, `SELECT i.id, i.key, i.summary, i.created_at, st.id, st.category, COALESCE(ito.name,it.name) `+issueJoinTables()+`
		WHERE i.project_id=$1 AND `+VisibleIssuePredicate("i", "$2")+` AND (`+compiled.Where+`)
		ORDER BY i.created_at, i.id`, args...)
	if err != nil {
		return nil, err
	}
	byID := map[string]*boardWork{}
	ids := []string{}
	work := []*boardWork{}
	for rows.Next() {
		var id string
		item := &boardWork{}
		if err := rows.Scan(&id, &item.key, &item.summary, &item.created, &item.statusID, &item.category, &item.issueType); err != nil {
			rows.Close()
			return nil, err
		}
		byID[id] = item
		ids = append(ids, id)
		work = append(work, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return work, err
	}
	rows, err = s.Pool.Query(ctx, `
		SELECT a.entity_id, a.created_at, COALESCE(a.payload->'issue'->'status'->>'id',''), COALESCE(a.payload->'issue'->'status'->>'category','')
		FROM actions a
		WHERE a.workspace_id=$1 AND a.entity_type=$2 AND a.entity_id=ANY($3) AND a.op='upsert' AND a.payload ? 'issue'
		ORDER BY a.seq`, board.WorkspaceID, models.EntityIssue, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var change statusChange
		if err := rows.Scan(&id, &change.at, &change.statusID, &change.category); err != nil {
			return nil, err
		}
		item := byID[id]
		// Only a change of status is a step in the item's flow.
		if n := len(item.changes); change.statusID != "" && (n == 0 || item.changes[n-1].statusID != change.statusID) {
			item.changes = append(item.changes, change)
		}
	}
	return work, rows.Err()
}

func dayEnds(days int, now time.Time) []time.Time {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	ends := make([]time.Time, 0, days)
	for index := days - 1; index >= 0; index-- {
		end := today.AddDate(0, 0, -index+1).Add(-time.Nanosecond)
		if end.After(now) {
			end = now
		}
		ends = append(ends, end)
	}
	return ends
}

// CumulativeFlow counts the board's work in each column at the end of each of
// the last days, today included.
func (s *Store) CumulativeFlow(ctx context.Context, board *models.Board, userID string, days int, now time.Time) (models.CumulativeFlow, error) {
	now = s.windowEnd(ctx, now)
	flow := models.CumulativeFlow{}
	// One count per column, so statuses grouped into a column are counted
	// together -- which is what the column means.
	column := map[string]int{}
	for index, boardColumn := range board.Columns {
		first := ""
		if len(boardColumn.StatusIDs) > 0 {
			first = boardColumn.StatusIDs[0]
		}
		flow.Columns = append(flow.Columns, models.FlowColumn{StatusID: first, Name: boardColumn.Name})
		for _, statusID := range boardColumn.StatusIDs {
			column[statusID] = index
		}
	}
	work, err := s.boardWorkHistory(ctx, board, userID)
	if err != nil {
		return flow, err
	}
	for _, end := range dayEnds(days, now) {
		day := models.FlowDay{Date: end.Format("2006-01-02"), Counts: make([]int, len(flow.Columns))}
		for _, item := range work {
			if status, existed := item.statusAt(end); existed {
				if index, shown := column[status]; shown {
					day.Counts[index]++
				}
			}
		}
		flow.Days = append(flow.Days, day)
	}
	return flow, nil
}

// ControlChart measures the cycle time of the board's work that reached done
// in the last days: from the first move into an in-progress status to the
// move into done that completed it.
func (s *Store) ControlChart(ctx context.Context, board *models.Board, userID string, days int, now time.Time) (models.ControlChart, error) {
	now = s.windowEnd(ctx, now)
	since := dayEnds(days, now)[0].Add(time.Nanosecond).AddDate(0, 0, -1)
	chart := models.ControlChart{Samples: []models.CycleSample{}}
	work, err := s.boardWorkHistory(ctx, board, userID)
	if err != nil {
		return chart, err
	}
	for _, item := range work {
		var started, completed time.Time
		for index, change := range item.changes {
			switch {
			case change.category == "indeterminate" && started.IsZero():
				started = change.at
			case change.category == "done" && index > 0 && item.changes[index-1].category != "done":
				completed = change.at
			case change.category != "done":
				completed = time.Time{}
			}
		}
		if completed.IsZero() || started.IsZero() || completed.Before(since) || completed.After(now) || !started.Before(completed) {
			continue
		}
		chart.Samples = append(chart.Samples, models.CycleSample{
			Key: item.key, Summary: item.summary, CompletedAt: completed.Format(time.RFC3339),
			CycleSeconds: int64(completed.Sub(started).Seconds()),
		})
	}
	sort.SliceStable(chart.Samples, func(i, j int) bool { return chart.Samples[i].CompletedAt < chart.Samples[j].CompletedAt })
	if len(chart.Samples) > 0 {
		cycles := make([]int64, 0, len(chart.Samples))
		var total int64
		for _, sample := range chart.Samples {
			cycles = append(cycles, sample.CycleSeconds)
			total += sample.CycleSeconds
		}
		sort.Slice(cycles, func(i, j int) bool { return cycles[i] < cycles[j] })
		chart.AverageSeconds = total / int64(len(cycles))
		middle := len(cycles) / 2
		chart.MedianSeconds = cycles[middle]
		if len(cycles)%2 == 0 {
			chart.MedianSeconds = (cycles[middle-1] + cycles[middle]) / 2
		}
	}
	return chart, nil
}
