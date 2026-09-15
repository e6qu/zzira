package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

// BoardsByProject lists a project's boards by name.
func (s *Store) BoardsByProject(ctx context.Context, workspaceID, projectID string) ([]*models.Board, error) {
	rows, err := s.Pool.Query(ctx, boardJoin+`WHERE p.workspace_id=$1 AND b.project_id=$2 ORDER BY b.name, b.id`, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Board
	for rows.Next() {
		board, err := scanBoard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, board)
	}
	return out, rows.Err()
}

// membershipChange is one sprint membership action for a work item.
type membershipChange struct {
	at time.Time
	in bool
}

// issueSnapshot is a work item's status category and estimate after an action.
type issueSnapshot struct {
	at       time.Time
	done     bool
	estimate *float64
}

// sprintIssueHistory is what the action log records about one work item in
// one sprint.
type sprintIssueHistory struct {
	key, summary, issueType, status string
	// member is whether the item is in the sprint now; changes replays how
	// it got there.
	member    bool
	changes   []membershipChange
	snapshots []issueSnapshot
	done      bool
	estimate  *float64
}

// inSprint reports membership at t. Inclusive counts changes made at t.
func (h *sprintIssueHistory) inSprint(t time.Time, inclusive bool) bool {
	in := h.member
	if len(h.changes) > 0 {
		// Membership recorded before the action log began shows as a first
		// change that removes it.
		in = !h.changes[0].in
	}
	for _, change := range h.changes {
		if change.at.After(t) || (!inclusive && change.at.Equal(t)) {
			break
		}
		in = change.in
	}
	return in
}

// state reports the status category and estimate at t.
func (h *sprintIssueHistory) state(t time.Time, inclusive bool) (bool, *float64) {
	if len(h.snapshots) == 0 {
		return h.done, h.estimate
	}
	current := h.snapshots[0]
	for _, snapshot := range h.snapshots {
		if snapshot.at.After(t) || (!inclusive && snapshot.at.Equal(t)) {
			break
		}
		current = snapshot
	}
	return current.done, current.estimate
}

// joinedAt is when the work entered the sprint during the window, or start
// when it was already there.
func (h *sprintIssueHistory) joinedAt(start, end time.Time) (time.Time, bool) {
	if h.inSprint(start, true) {
		return start, true
	}
	for _, change := range h.changes {
		if change.in && change.at.After(start) && !change.at.After(end) {
			return change.at, true
		}
	}
	return time.Time{}, false
}

func decodeEstimate(raw []byte) *float64 {
	// A cleared estimate is stored as null, which is no estimate, not zero.
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value float64
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return &value
}

// sprintHistories replays the sprint's membership and its work items'
// changes, limited to work the user can view. Without an estimation field
// every item counts as one.
func (s *Store) sprintHistories(ctx context.Context, workspaceID, userID, sprintID, fieldID string) (map[string]*sprintIssueHistory, []string, error) {
	histories := make(map[string]*sprintIssueHistory)
	changes := make(map[string][]membershipChange)
	rows, err := s.Pool.Query(ctx, `
		SELECT a.payload->>'issueId', a.op <> 'delete' AND NOT COALESCE((a.payload->>'removed')::boolean,false), a.created_at
		FROM actions a
		WHERE a.workspace_id=$1 AND a.entity_type=$2 AND a.entity_id=$3
		ORDER BY a.seq`, workspaceID, models.EntitySprintIssue, sprintID)
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var issueID string
		var change membershipChange
		if err := rows.Scan(&issueID, &change.in, &change.at); err != nil {
			rows.Close()
			return nil, nil, err
		}
		if _, seen := changes[issueID]; !seen {
			ids = append(ids, issueID)
		}
		changes[issueID] = append(changes[issueID], change)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	members := make(map[string]bool)
	rows, err = s.Pool.Query(ctx, `SELECT issue_id FROM sprint_issues WHERE sprint_id=$1`, sprintID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var issueID string
		if err := rows.Scan(&issueID); err != nil {
			rows.Close()
			return nil, nil, err
		}
		members[issueID] = true
		if _, seen := changes[issueID]; !seen {
			ids = append(ids, issueID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(ids) == 0 {
		return histories, nil, nil
	}

	rows, err = s.Pool.Query(ctx, `
		SELECT i.id, i.key, i.summary, it.name, st.name, st.category='done', i.fields->$3::text
		FROM issues i
		JOIN issue_types it ON it.id=i.issuetype_id
		JOIN statuses st ON st.id=i.status_id
		WHERE i.workspace_id=$1 AND i.id=ANY($2) AND `+VisibleIssuePredicate("i", "$4")+`
		ORDER BY i.created_at, i.id`, workspaceID, ids, fieldID, userID)
	if err != nil {
		return nil, nil, err
	}
	order := make([]string, 0, len(ids))
	for rows.Next() {
		var issueID string
		var estimate []byte
		history := &sprintIssueHistory{}
		if err := rows.Scan(&issueID, &history.key, &history.summary, &history.issueType, &history.status, &history.done, &estimate); err != nil {
			rows.Close()
			return nil, nil, err
		}
		history.member = members[issueID]
		history.changes = changes[issueID]
		history.estimate = decodeEstimate(estimate)
		histories[issueID] = history
		order = append(order, issueID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(order) == 0 {
		return histories, order, nil
	}

	rows, err = s.Pool.Query(ctx, `
		SELECT a.entity_id, a.created_at, COALESCE(a.payload->'issue'->'status'->>'category','')='done',
		       a.payload->'issue'->'fields'->$3::text
		FROM actions a
		WHERE a.workspace_id=$1 AND a.entity_type=$4 AND a.entity_id=ANY($2) AND a.op='upsert' AND a.payload ? 'issue'
		ORDER BY a.seq`, workspaceID, order, fieldID, models.EntityIssue)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var issueID string
		var snapshot issueSnapshot
		var estimate []byte
		if err := rows.Scan(&issueID, &snapshot.at, &snapshot.done, &estimate); err != nil {
			return nil, nil, err
		}
		snapshot.estimate = decodeEstimate(estimate)
		histories[issueID].snapshots = append(histories[issueID].snapshots, snapshot)
	}
	if fieldID == "" {
		one := 1.0
		for _, history := range histories {
			history.estimate = &one
			for index := range history.snapshots {
				history.snapshots[index].estimate = &one
			}
		}
	}
	return histories, order, rows.Err()
}

// sprintWindow is when a sprint started and where its report stops: its
// completion, or now while it runs. It reads the stored timestamps because
// the formatted dates drop the precision that orders same-second actions.
func (s *Store) sprintWindow(ctx context.Context, sprint *models.Sprint, now time.Time) (time.Time, time.Time, error) {
	var activated, completed, planned *time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT activated_at, completed_at, start_date FROM sprints WHERE id=$1`, sprint.ID).Scan(&activated, &completed, &planned); err != nil {
		return time.Time{}, time.Time{}, err
	}
	if activated == nil {
		activated = planned
	}
	if activated == nil {
		return time.Time{}, time.Time{}, fmt.Errorf("sprint %s has not started", sprint.ID)
	}
	start, end := activated.UTC(), now.UTC()
	if sprint.State == "closed" && completed != nil {
		end = completed.UTC()
	}
	if end.Before(start) {
		end = start
	}
	return start, end, nil
}

func estimateValue(estimate *float64) float64 {
	if estimate == nil {
		return 0
	}
	return *estimate
}

func formatEstimate(estimate *float64) string {
	if estimate == nil {
		return ""
	}
	return strconv.FormatFloat(*estimate, 'f', -1, 64)
}

func estimateStatistic(board *models.Board) string {
	if board.EstimationFieldID == "" {
		return "Issue count"
	}
	return board.EstimationFieldName
}

// SprintReport builds Jira's sprint report for a started sprint on board.
func (s *Store) SprintReport(ctx context.Context, workspaceID, userID string, board *models.Board, sprint *models.Sprint, now time.Time) (models.SprintReport, error) {
	start, end, err := s.sprintWindow(ctx, sprint, now)
	if err != nil {
		return models.SprintReport{}, err
	}
	histories, order, err := s.sprintHistories(ctx, workspaceID, userID, sprint.ID, board.EstimationFieldID)
	if err != nil {
		return models.SprintReport{}, err
	}
	report := models.SprintReport{Sprint: *sprint, Statistic: estimateStatistic(board), Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339)}
	for _, issueID := range order {
		history := histories[issueID]
		joined, ok := history.joinedAt(start, end)
		if !ok {
			continue
		}
		doneJoined, estimateJoined := history.state(joined, true)
		doneEnd, estimateEnd := history.state(end, true)
		row := models.SprintReportIssue{
			Key: history.key, Summary: history.summary, IssueType: history.issueType, Status: history.status,
			EstimateStart: formatEstimate(estimateJoined), EstimateEnd: formatEstimate(estimateEnd),
			AddedAfterStart: joined.After(start),
		}
		switch {
		case !history.inSprint(end, true):
			report.Removed = append(report.Removed, row)
			report.RemovedTotal += estimateValue(estimateEnd)
		case doneJoined && doneEnd:
			report.CompletedOutside = append(report.CompletedOutside, row)
		case doneEnd:
			report.Completed = append(report.Completed, row)
			report.CompletedTotal += estimateValue(estimateEnd)
		default:
			report.NotCompleted = append(report.NotCompleted, row)
			report.NotCompletedTotal += estimateValue(estimateEnd)
		}
	}
	report.Burndown = sprintBurndown(histories, order, start, end, sprint.State == "closed")
	return report, nil
}

// sprintBurndown replays each change to the sprint's scope and remaining
// work between start and end.
func sprintBurndown(histories map[string]*sprintIssueHistory, order []string, start, end time.Time, closed bool) []models.SprintBurndownEvent {
	moments := map[int64]time.Time{}
	for _, issueID := range order {
		history := histories[issueID]
		for _, change := range history.changes {
			if change.at.After(start) && !change.at.After(end) {
				moments[change.at.UnixNano()] = change.at
			}
		}
		for _, snapshot := range history.snapshots {
			if snapshot.at.After(start) && !snapshot.at.After(end) {
				moments[snapshot.at.UnixNano()] = snapshot.at
			}
		}
	}
	times := make([]time.Time, 0, len(moments))
	for _, moment := range moments {
		times = append(times, moment)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })

	contribution := func(history *sprintIssueHistory, t time.Time, inclusive bool) (in, done bool, estimate *float64) {
		in = history.inSprint(t, inclusive)
		done, estimate = history.state(t, inclusive)
		return in, done, estimate
	}
	var scope, remaining float64
	for _, issueID := range order {
		in, done, estimate := contribution(histories[issueID], start, true)
		if in {
			scope += estimateValue(estimate)
			if !done {
				remaining += estimateValue(estimate)
			}
		}
	}
	events := []models.SprintBurndownEvent{{At: start.Format(time.RFC3339), Label: "Sprint start", Remaining: remaining, Scope: scope}}
	for _, moment := range times {
		for _, issueID := range order {
			history := histories[issueID]
			wasIn, wasDone, wasEstimate := contribution(history, moment, false)
			isIn, isDone, isEstimate := contribution(history, moment, true)
			before, after := 0.0, 0.0
			if wasIn && !wasDone {
				before = estimateValue(wasEstimate)
			}
			if isIn && !isDone {
				after = estimateValue(isEstimate)
			}
			scopeBefore, scopeAfter := 0.0, 0.0
			if wasIn {
				scopeBefore = estimateValue(wasEstimate)
			}
			if isIn {
				scopeAfter = estimateValue(isEstimate)
			}
			if wasIn == isIn && wasDone == isDone && formatEstimate(wasEstimate) == formatEstimate(isEstimate) {
				continue
			}
			if !wasIn && !isIn {
				continue
			}
			label := ""
			switch {
			case !wasIn && isIn:
				label = "Added to sprint"
			case wasIn && !isIn:
				label = "Removed from sprint"
			case !wasDone && isDone:
				label = "Work completed"
			case wasDone && !isDone:
				label = "Work reopened"
			default:
				label = "Estimate changed from " + formatEstimateOrNone(wasEstimate) + " to " + formatEstimateOrNone(isEstimate)
			}
			remaining += after - before
			scope += scopeAfter - scopeBefore
			events = append(events, models.SprintBurndownEvent{
				At: moment.Format(time.RFC3339), Label: label, IssueKey: history.key,
				Change: after - before, Remaining: remaining, Scope: scope,
			})
		}
	}
	if closed {
		events = append(events, models.SprintBurndownEvent{At: end.Format(time.RFC3339), Label: "Sprint completed", Remaining: remaining, Scope: scope})
	}
	return events
}

func formatEstimateOrNone(estimate *float64) string {
	if estimate == nil {
		return "none"
	}
	return formatEstimate(estimate)
}

// VelocityReport compares commitment and completed work for the board's
// seven most recently completed sprints.
func (s *Store) VelocityReport(ctx context.Context, workspaceID, userID string, board *models.Board) (models.VelocityReport, error) {
	sprints, err := s.SprintsByBoard(ctx, board.ID)
	if err != nil {
		return models.VelocityReport{}, err
	}
	closed := make([]*models.Sprint, 0)
	for _, sprint := range sprints {
		if sprint.State == "closed" && sprint.CompleteDate != "" {
			closed = append(closed, sprint)
		}
	}
	// RFC 3339 UTC timestamps order as text.
	sort.SliceStable(closed, func(i, j int) bool { return closed[i].CompleteDate > closed[j].CompleteDate })
	if len(closed) > 7 {
		closed = closed[:7]
	}
	report := models.VelocityReport{Statistic: estimateStatistic(board)}
	for index := len(closed) - 1; index >= 0; index-- {
		sprint := closed[index]
		start, end, err := s.sprintWindow(ctx, sprint, time.Now())
		if err != nil {
			return models.VelocityReport{}, err
		}
		histories, order, err := s.sprintHistories(ctx, workspaceID, userID, sprint.ID, board.EstimationFieldID)
		if err != nil {
			return models.VelocityReport{}, err
		}
		entry := models.VelocitySprint{Sprint: *sprint}
		for _, issueID := range order {
			history := histories[issueID]
			if history.inSprint(start, true) {
				_, estimate := history.state(start, true)
				entry.Commitment += estimateValue(estimate)
			}
			joined, ok := history.joinedAt(start, end)
			if !ok || !history.inSprint(end, true) {
				continue
			}
			doneJoined, _ := history.state(joined, true)
			doneEnd, estimateEnd := history.state(end, true)
			if doneEnd && !doneJoined {
				entry.Completed += estimateValue(estimateEnd)
			}
		}
		report.Sprints = append(report.Sprints, entry)
	}
	return report, nil
}
