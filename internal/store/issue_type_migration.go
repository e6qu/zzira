package store

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// migrateIssueType records each replacement in the same transaction as the
// metadata deletion. Read bounded batches so a large project does not require
// retaining every issue or action snapshot in memory.
func migrateIssueType(ctx context.Context, tx pgx.Tx, workspaceID, actorID string, source, target models.IssueType) error {
	after := ""
	for {
		rows, err := tx.Query(ctx, `SELECT id FROM issues WHERE workspace_id=$1 AND issuetype_id=$2 AND id>$3 ORDER BY id LIMIT 100 FOR UPDATE`, workspaceID, source.ID, after)
		if err != nil {
			return err
		}
		ids := make([]string, 0, 100)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		for _, id := range ids {
			seq, err := nextSeq(ctx, tx, workspaceID)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE issues SET issuetype_id=$3,updated_seq=$4,updated_at=now() WHERE workspace_id=$1 AND id=$2`, workspaceID, id, target.ID, seq); err != nil {
				return err
			}
			issue, err := scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.workspace_id=$1 AND i.id=$2`, workspaceID, id))
			if err != nil {
				return err
			}
			// Preserve public IDs before deleting the source metadata; a later
			// changelog read can no longer resolve its internal identifier.
			payload, err := json.Marshal(models.IssueUpdatePayload{
				Issue: *issue,
				Diff:  map[string]models.ChangeItem{"issuetype": diffItem("issuetype", strconv.FormatInt(source.JiraID, 10), source.Name, strconv.FormatInt(target.JiraID, 10), target.Name)},
			})
			if err != nil {
				return err
			}
			if err := appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssue, EntityID: id, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}); err != nil {
				return err
			}
		}
		after = ids[len(ids)-1]
	}
}
