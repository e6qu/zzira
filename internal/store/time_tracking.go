package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

// WorklogEstimate says how logging, changing or deleting work moves a work
// item's remaining estimate, as Jira's adjustEstimate does:
//   - "auto" moves it by the time logged, never below zero, and leaves an
//     unestimated work item unestimated;
//   - "new" sets it to NewSeconds;
//   - "manual" reduces it by ReduceBySeconds when work is logged and increases
//     it by IncreaseBySeconds when work is deleted;
//   - "leave" keeps it.
type WorklogEstimate struct {
	Mode              string
	NewSeconds        int64
	ReduceBySeconds   int64
	IncreaseBySeconds int64
	// Notify sends the work logged, updated or deleted event to the people the
	// project's notification scheme names, as Jira's notifyUsers does.
	Notify bool
}

// ValidateWorklogEstimate checks an adjustment is one Jira accepts.
func ValidateWorklogEstimate(estimate WorklogEstimate) error {
	switch estimate.Mode {
	case "", "auto", "leave", "new", "manual":
	default:
		return fmt.Errorf("%w: adjustEstimate must be auto, new, manual or leave", ErrWorklogValidation)
	}
	if estimate.NewSeconds < 0 || estimate.ReduceBySeconds < 0 || estimate.IncreaseBySeconds < 0 {
		return fmt.Errorf("%w: estimate adjustments cannot be negative", ErrWorklogValidation)
	}
	return nil
}

// adjustRemainingEstimate applies a worklog change to the work item's
// remaining estimate and records the time tracking change as an issue update.
// delta is the change in time logged: positive when work is logged, negative
// when it is removed.
func adjustRemainingEstimate(ctx context.Context, tx pgx.Tx, workspaceID, actorID, issueID string, delta int64, estimate WorklogEstimate) error {
	var current *int64
	if err := tx.QueryRow(ctx, `SELECT remaining_estimate_seconds FROM issues WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, issueID, workspaceID).Scan(&current); err != nil {
		return err
	}
	next := current
	switch estimate.Mode {
	case "", "auto":
		if current != nil {
			value := max(*current-delta, 0)
			next = &value
		}
	case "new":
		value := estimate.NewSeconds
		next = &value
	case "manual":
		base := int64(0)
		if current != nil {
			base = *current
		}
		value := max(base-estimate.ReduceBySeconds+estimate.IncreaseBySeconds, 0)
		next = &value
	}
	diff := map[string]models.ChangeItem{}
	if (current == nil) != (next == nil) || current != nil && *current != *next {
		diff["timeestimate"] = estimateChange("timeestimate", current, next)
	}
	if delta != 0 {
		var spent int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(time_spent_seconds),0) FROM worklogs WHERE issue_id=$1`, issueID).Scan(&spent); err != nil {
			return err
		}
		before := spent - delta
		diff["timespent"] = models.ChangeItem{Field: "timespent", FieldType: "jira", From: strconv.FormatInt(before, 10), FromString: strconv.FormatInt(before, 10), To: strconv.FormatInt(spent, 10), ToString: strconv.FormatInt(spent, 10)}
	}
	if len(diff) == 0 {
		return nil
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE issues SET remaining_estimate_seconds=$3,updated_seq=$4,updated_at=now() WHERE id=$1 AND workspace_id=$2`, issueID, workspaceID, next, seq); err != nil {
		return err
	}
	updated, err := scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.workspace_id=$1 AND i.id=$2`, workspaceID, issueID))
	if err != nil {
		return err
	}
	payload, err := json.Marshal(models.IssueUpdatePayload{Diff: diff, Issue: *updated})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssue, EntityID: issueID,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	})
}
