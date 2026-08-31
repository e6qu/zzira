package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/models"
)

// ---- shared tx helpers ----

func nextSeq(ctx context.Context, tx pgx.Tx, workspaceID string) (int64, error) {
	var seq int64
	err := tx.QueryRow(ctx, `UPDATE workspaces SET seq = seq + 1 WHERE id=$1 RETURNING seq`, workspaceID).Scan(&seq)
	return seq, err
}

func appendAction(ctx context.Context, tx pgx.Tx, a *models.Action) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO actions (workspace_id, seq, entity_type, entity_id, op, schema_v, payload, actor_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		a.WorkspaceID, a.Seq, a.EntityType, a.EntityID, a.Op, a.SchemaV, a.Payload, a.ActorID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SELECT pg_notify('zzira_actions', $1 || '|' || $2)`, a.WorkspaceID, fmt.Sprintf("%d", a.Seq))
	return err
}

// ---- Issue update / delete (V1) ----

type IssueUpdate struct {
	Summary         *string
	Description     json.RawMessage // non-nil = replace
	PriorityID      *string         // "" = clear, nil = unchanged
	AssigneeID      *string         // "" = unassign, nil = unchanged
	StatusID        *string         // transitions only; "" invalid
	SecurityLevelID *string         // "" = public, nil = unchanged
	Fields          map[string]json.RawMessage
}

func diffItem(field, from, fromString, to, toString string) models.ChangeItem {
	return models.ChangeItem{Field: field, FieldType: "std", From: from, FromString: fromString, To: to, ToString: toString}
}

func (s *Store) UpdateIssue(ctx context.Context, actorID, workspaceID, issueID string, up IssueUpdate) (*models.Issue, *models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.workspace_id=$1 AND i.id=$2`, workspaceID, issueID))
	if err != nil {
		return nil, nil, err
	}
	diff := map[string]models.ChangeItem{}

	sets := []string{}
	args := []any{}
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }

	if up.Summary != nil && *up.Summary != current.Summary {
		diff["summary"] = diffItem("summary", "", current.Summary, "", *up.Summary)
		sets = append(sets, "summary = "+arg(*up.Summary))
	}
	if up.Description != nil && !adf.Equal(up.Description, current.Description) {
		diff["description"] = diffItem("description", "", adf.PlainText(current.Description), "", adf.PlainText(up.Description))
		sets = append(sets, "description = "+arg(string(up.Description))+"::jsonb")
	}
	if up.SecurityLevelID != nil && *up.SecurityLevelID != current.SecurityLevelID {
		diff["security"] = diffItem("security", current.SecurityLevelID, securityDisplayName(ctx, tx, current.ProjectID, current.SecurityLevelID), *up.SecurityLevelID, securityDisplayName(ctx, tx, current.ProjectID, *up.SecurityLevelID))
		sets = append(sets, "security_level_id = "+arg(nilIfEmpty(*up.SecurityLevelID)))
	}
	if up.Fields != nil {
		merged, err := mergeFields(current.Fields, up.Fields)
		if err != nil {
			return nil, nil, err
		}
		fieldsJSON, err := json.Marshal(merged)
		if err != nil {
			return nil, nil, err
		}
		sets = append(sets, "fields = "+arg(fieldsJSON)+"::jsonb")
	}
	if up.StatusID != nil && *up.StatusID != current.Status.ID {
		var newName, newCategory string
		if err := tx.QueryRow(ctx, `SELECT name, category FROM statuses WHERE id=$1`, *up.StatusID).Scan(&newName, &newCategory); err != nil {
			return nil, nil, fmt.Errorf("unknown status %q", *up.StatusID)
		}
		diff["status"] = diffItem("status", current.Status.ID, current.Status.Name, *up.StatusID, newName)
		sets = append(sets, "status_id = "+arg(*up.StatusID))
	}
	if up.PriorityID != nil {
		newID := *up.PriorityID
		if newID != currentPriorityID(current) {
			newName := ""
			if newID != "" {
				if err := tx.QueryRow(ctx, `SELECT name FROM priorities WHERE id=$1`, newID).Scan(&newName); err != nil {
					return nil, nil, fmt.Errorf("unknown priority %q", newID)
				}
			}
			oldID, oldName := "", ""
			if current.Priority != nil {
				oldID, oldName = current.Priority.ID, current.Priority.Name
			}
			diff["priority"] = diffItem("priority", oldID, oldName, newID, newName)
			sets = append(sets, "priority_id = "+arg(nilIfEmpty(newID)))
		}
	}
	if up.AssigneeID != nil {
		newID := *up.AssigneeID
		if newID != currentUserID(current.Assignee) {
			newName := ""
			if newID != "" {
				if err := tx.QueryRow(ctx, `SELECT display_name FROM users WHERE id=$1`, newID).Scan(&newName); err != nil {
					return nil, nil, fmt.Errorf("unknown assignee %q", newID)
				}
			}
			oldID, oldName := "", ""
			if current.Assignee != nil {
				oldID, oldName = current.Assignee.ID, current.Assignee.DisplayName
			}
			diff["assignee"] = diffItem("assignee", oldID, oldName, newID, newName)
			sets = append(sets, "assignee_id = "+arg(nilIfEmpty(newID)))
		}
	}

	if len(sets) == 0 {
		return current, nil, nil // nothing to do: no action
	}

	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	sets = append(sets, "updated_seq = "+arg(seq), "updated_at = now()")
	whereID := arg(issueID)
	whereWS := arg(workspaceID)
	if _, err := tx.Exec(ctx, `UPDATE issues SET `+joinSets(sets)+` WHERE id=`+whereID+` AND workspace_id=`+whereWS,
		args...); err != nil {
		return nil, nil, err
	}

	updated, err := scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.workspace_id=$1 AND i.id=$2`, workspaceID, issueID))
	if err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(models.IssueUpdatePayload{Diff: diff, Issue: *updated})
	if err != nil {
		return nil, nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssue, EntityID: issueID,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return updated, action, nil
}

func (s *Store) DeleteIssue(ctx context.Context, actorID, workspaceID, issueID, reason string) (*models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(models.DeletePayload{Reason: reason})
	if err != nil {
		return nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssue, EntityID: issueID,
		Op: models.OpDelete, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if _, err := tx.Exec(ctx, `DELETE FROM issues WHERE id=$1 AND workspace_id=$2`, issueID, workspaceID); err != nil {
		return nil, err
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return action, nil
}

// ---- Comments (V1) ----

const commentJoin = `
SELECT c.id, c.issue_id, c.author_id, COALESCE(u.display_name,''), c.body,
       to_char(c.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
FROM comments c LEFT JOIN users u ON u.id = c.author_id
`

func scanComment(row pgx.Row) (*models.Comment, error) {
	c := &models.Comment{}
	err := row.Scan(&c.ID, &c.IssueID, &c.AuthorID, &c.AuthorName, &c.Body, &c.Created)
	return c, err
}

func (s *Store) CreateComment(ctx context.Context, actorID, workspaceID, issueID string, body json.RawMessage) (*models.Comment, *models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	commentID := NewID("cmt")
	if _, err := tx.Exec(ctx,
		`INSERT INTO comments (id, issue_id, workspace_id, author_id, body) VALUES ($1,$2,$3,$4,$5)`,
		commentID, issueID, workspaceID, actorID, body); err != nil {
		return nil, nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE comments SET updated_seq=$2 WHERE id=$1`, commentID, seq); err != nil {
		return nil, nil, err
	}
	comment, err := scanComment(tx.QueryRow(ctx, commentJoin+`WHERE c.id=$1`, commentID))
	if err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(models.CommentUpsertPayload{Comment: *comment})
	if err != nil {
		return nil, nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityComment, EntityID: commentID,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return comment, action, nil
}

func (s *Store) CommentsByIssue(ctx context.Context, issueID string) ([]*models.Comment, error) {
	rows, err := s.Pool.Query(ctx, commentJoin+`WHERE c.issue_id=$1 ORDER BY c.created_at, c.id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CommentByID(ctx context.Context, workspaceID, commentID string) (*models.Comment, error) {
	return scanComment(s.Pool.QueryRow(ctx, commentJoin+`WHERE c.id=$1 AND c.workspace_id=$2`, commentID, workspaceID))
}

func (s *Store) DeleteComment(ctx context.Context, actorID, workspaceID, commentID string) (*models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	comment, err := scanComment(tx.QueryRow(ctx, commentJoin+`WHERE c.id=$1 AND c.workspace_id=$2`, commentID, workspaceID))
	if err != nil {
		return nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(models.CommentDeletePayload{CommentID: commentID, IssueID: comment.IssueID})
	if err != nil {
		return nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityComment, EntityID: commentID,
		Op: models.OpDelete, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if _, err := tx.Exec(ctx, `DELETE FROM comments WHERE id=$1 AND workspace_id=$2`, commentID, workspaceID); err != nil {
		return nil, err
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return action, nil
}

// ---- Changelog (derived from the log) ----

func (s *Store) IssueChangelog(ctx context.Context, workspaceID, issueID string) ([]models.ChangelogEntry, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT a.seq, a.actor_id, COALESCE(u.display_name,''),
		       to_char(a.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), a.payload
		FROM actions a LEFT JOIN users u ON u.id = a.actor_id
		WHERE a.workspace_id=$1 AND a.entity_type='issue' AND a.entity_id=$2
		  AND a.op='upsert' AND a.payload ? 'diff' AND a.schema_v >= 2
		ORDER BY a.seq`, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ChangelogEntry
	for rows.Next() {
		var (
			e         models.ChangelogEntry
			actorID   string
			actorName string
			payload   json.RawMessage
		)
		if err := rows.Scan(&e.Seq, &actorID, &actorName, &e.Created, &payload); err != nil {
			return nil, err
		}
		var p models.IssueUpdatePayload
		if err := json.Unmarshal(payload, &p); err != nil {
			continue
		}
		e.AuthorID = actorID
		e.Author = &models.User{ID: actorID, DisplayName: actorName, Active: true, AccountType: "atlassian"}
		e.Items = models.SortedDiffItems(p.Diff)
		out = append(out, e)
	}
	return out, rows.Err()
}

func currentPriorityID(i *models.Issue) string {
	if i.Priority != nil {
		return i.Priority.ID
	}
	return ""
}

func currentUserID(u *models.User) string {
	if u != nil {
		return u.ID
	}
	return ""
}

func joinSets(sets []string) string {
	out := ""
	for i, s := range sets {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// securityDisplayName resolves a level's display name from the project scheme.
func securityDisplayName(ctx context.Context, tx pgx.Tx, projectID, levelID string) string {
	if levelID == "" {
		return ""
	}
	var name string
	err := tx.QueryRow(ctx, `
		SELECT lvl->>'name'
		FROM projects p
		JOIN security_schemes ss ON ss.id = p.security_scheme_id
		, jsonb_array_elements(ss.levels) lvl
		WHERE p.id = $2 AND lvl->>'id' = $1`, levelID, projectID).Scan(&name)
	if err != nil {
		return levelID
	}
	return name
}

// mergeFields overlays custom-field values onto the issue's current fields.
func mergeFields(current map[string]json.RawMessage, updates map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(current)+len(updates))
	for k, v := range current {
		out[k] = v
	}
	for k, v := range updates {
		if !isCustomFieldKey(k) {
			return nil, fmt.Errorf("field key %q is not a custom field", k)
		}
		if len(v) == 0 || string(v) == "null" {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	return out, nil
}

func isCustomFieldKey(k string) bool {
	if len(k) <= len("customfield_") {
		return false
	}
	prefix := k[:len("customfield_")]
	digits := k[len("customfield_"):]
	if prefix != "customfield_" {
		return false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// EmitTombstone appends a per-user tombstone telling that user's replica to
// drop the issue. Excluded users receive these; members never do.
func (s *Store) EmitTombstone(ctx context.Context, workspaceID, issueID, userID, reason string) (*models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(models.TombstonePayload{IssueID: issueID, UserID: userID, Reason: reason})
	if err != nil {
		return nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityTombstone, EntityID: issueID,
		Op: models.OpDelete, SchemaV: models.SchemaVersion, Payload: payload, ActorID: userID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return action, nil
}

// CreateCustomField registers a custom field.
func (s *Store) CreateCustomField(ctx context.Context, id, name, fieldType, description string) (*models.CustomField, error) {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO custom_fields (id, name, type, description) VALUES ($1,$2,$3,$4)`,
		id, name, fieldType, description)
	if err != nil {
		return nil, err
	}
	return &models.CustomField{ID: id, Name: name, Type: fieldType, Description: description}, nil
}

func (s *Store) CustomFields(ctx context.Context) ([]*models.CustomField, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, name, type, COALESCE(description,'') FROM custom_fields ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.CustomField
	for rows.Next() {
		f := &models.CustomField{}
		if err := rows.Scan(&f.ID, &f.Name, &f.Type, &f.Description); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// CustomFieldsForProject returns fields with a global or project-scoped context.
func (s *Store) CustomFieldsForProject(ctx context.Context, projectID string) ([]*models.CustomField, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT cf.id, cf.name, cf.type, COALESCE(cf.description,'')
		FROM custom_fields cf
		LEFT JOIN field_contexts fc ON fc.field_id = cf.id
		WHERE fc.project_id IS NULL OR fc.project_id = $1
		ORDER BY cf.id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.CustomField
	for rows.Next() {
		f := &models.CustomField{}
		if err := rows.Scan(&f.ID, &f.Name, &f.Type, &f.Description); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ---- webhooks ----

// CreateWebhook registers the hook with a watermark at the workspace's
// current head: only actions committed after registration are delivered.
func (s *Store) CreateWebhook(ctx context.Context, workspaceID, url string, events []string, jql string) (*models.Webhook, error) {
	head, err := s.Head(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	id := NewID("wh")
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO webhooks (id, workspace_id, url, events, jql, start_seq) VALUES ($1,$2,$3,$4,$5,$6)`,
		id, workspaceID, url, events, jql, head); err != nil {
		return nil, err
	}
	return &models.Webhook{ID: id, URL: url, Events: events, JQL: jql, Active: true}, nil
}

func (s *Store) Webhooks(ctx context.Context, workspaceID string) ([]*models.Webhook, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, url, events, jql, active, start_seq FROM webhooks WHERE workspace_id=$1 ORDER BY created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Webhook
	for rows.Next() {
		w := &models.Webhook{}
		if err := rows.Scan(&w.ID, &w.URL, &w.Events, &w.JQL, &w.Active, &w.StartSeq); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) DeleteWebhook(ctx context.Context, workspaceID, id string) error {
	result, err := s.Pool.Exec(ctx, `DELETE FROM webhooks WHERE id=$1 AND workspace_id=$2`, id, workspaceID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ClaimNewWebhookSeqs registers (webhook, seq) pairs for delivery; idempotent.
func (s *Store) ClaimNewWebhookSeqs(ctx context.Context, workspaceID string, upto int64) error {
	webhooks, err := s.Webhooks(ctx, workspaceID)
	if err != nil {
		return err
	}
	for _, w := range webhooks {
		if !w.Active {
			continue
		}
		if _, err := s.Pool.Exec(ctx, `
			INSERT INTO webhook_deliveries (webhook_id, seq, state)
			SELECT $1, seq, 'pending' FROM generate_series($2 + 1, $3) seq
			ON CONFLICT (webhook_id, seq) DO NOTHING`, w.ID, w.StartSeq, upto); err != nil {
			return err
		}
	}
	return nil
}

// ClaimPendingWebhookBatch atomically claims one active webhook's pending
// deliveries (FOR UPDATE SKIP LOCKED keeps replicas out of each other's way).
// Returns ok=false when there is no claimable work.
func (s *Store) ClaimPendingWebhookBatch(ctx context.Context, workspaceID string, n int) (*models.Webhook, []int64, bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	w := &models.Webhook{}
	err = tx.QueryRow(ctx, `
		SELECT w.id, w.url, w.events, w.jql
		FROM webhooks w
		WHERE w.workspace_id=$1 AND w.active
		  AND EXISTS (
			SELECT 1 FROM webhook_deliveries d
			WHERE d.webhook_id = w.id
			  AND (d.state = 'pending' OR (d.state = 'failed' AND d.next_attempt_at <= now()))
		  )
		ORDER BY (
			SELECT MIN(seq) FROM webhook_deliveries d
			WHERE d.webhook_id = w.id
			  AND (d.state = 'pending' OR (d.state = 'failed' AND d.next_attempt_at <= now()))
		)
		LIMIT 1
		FOR UPDATE OF w SKIP LOCKED`, workspaceID).Scan(&w.ID, &w.URL, &w.Events, &w.JQL)
	if err == pgx.ErrNoRows {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	seqRows, err := tx.Query(ctx, `
		WITH due AS (
			SELECT webhook_id, seq FROM webhook_deliveries
			WHERE webhook_id=$1
			  AND (state='pending' OR (state='failed' AND next_attempt_at <= now()))
			ORDER BY seq
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE webhook_deliveries d
			SET state='delivering', claimed_at=now(), next_attempt_at=NULL
			FROM due
			WHERE d.webhook_id=due.webhook_id AND d.seq=due.seq
			RETURNING d.seq
		)
		SELECT seq FROM claimed ORDER BY seq`, w.ID, n)
	if err != nil {
		return nil, nil, false, err
	}
	var seqs []int64
	for seqRows.Next() {
		var seq int64
		if err := seqRows.Scan(&seq); err != nil {
			seqRows.Close()
			return nil, nil, false, err
		}
		seqs = append(seqs, seq)
	}
	seqRows.Close()
	if err := seqRows.Err(); err != nil {
		return nil, nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, false, err
	}
	return w, seqs, true, nil
}

// ActionBySeq loads one action (webhook payloads).
func (s *Store) ActionBySeq(ctx context.Context, workspaceID string, seq int64) (*models.Action, error) {
	a := &models.Action{}
	err := s.Pool.QueryRow(ctx, `
		SELECT workspace_id, seq, entity_type, entity_id, op, schema_v, payload, actor_id,
		       to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM actions WHERE workspace_id=$1 AND seq=$2`, workspaceID, seq).
		Scan(&a.WorkspaceID, &a.Seq, &a.EntityType, &a.EntityID, &a.Op, &a.SchemaV, &a.Payload, &a.ActorID, &a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// MarkWebhookDelivery records a delivery attempt result.
func (s *Store) MarkWebhookDelivery(ctx context.Context, webhookID string, seq int64, delivered bool, lastErr string) error {
	if delivered {
		_, err := s.Pool.Exec(ctx, `
			UPDATE webhook_deliveries
			SET state='delivered', attempts=attempts+1, last_error=$3, next_attempt_at=NULL
			WHERE webhook_id=$1 AND seq=$2`, webhookID, seq, lastErr)
		return err
	}
	_, err := s.Pool.Exec(ctx, `
		UPDATE webhook_deliveries
		SET state='failed', attempts=attempts+1, last_error=$3,
		    next_attempt_at=now() + make_interval(secs => LEAST(POWER(2, attempts)::int, 300))
		WHERE webhook_id=$1 AND seq=$2`, webhookID, seq, lastErr)
	return err
}

// ---- filters CRUD (V5) ----

func (s *Store) CreateFilter(ctx context.Context, id, workspaceID, name, jql, description, ownerID string) (*models.Filter, error) {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO filters (id, workspace_id, name, jql, description, owner_id, favourite) VALUES ($1,$2,$3,$4,$5,$6,FALSE)`,
		id, workspaceID, name, jql, description, nilIfEmpty(ownerID))
	if err != nil {
		return nil, err
	}
	return s.FilterByID(ctx, workspaceID, ownerID, id)
}

func (s *Store) UpdateFilter(ctx context.Context, workspaceID, userID, id, name, jql, description string) (*models.Filter, error) {
	result, err := s.Pool.Exec(ctx,
		`UPDATE filters SET name=$4, jql=$5, description=$6 WHERE id=$1 AND workspace_id=$2 AND owner_id=$3`, id, workspaceID, userID, name, jql, description)
	if err != nil {
		return nil, err
	}
	if result.RowsAffected() == 0 {
		return nil, pgx.ErrNoRows
	}
	return s.FilterByID(ctx, workspaceID, userID, id)
}

func (s *Store) DeleteFilter(ctx context.Context, workspaceID, userID, id string) error {
	result, err := s.Pool.Exec(ctx, `DELETE FROM filters WHERE id=$1 AND workspace_id=$2 AND owner_id=$3`, id, workspaceID, userID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (s *Store) SetFilterFavourite(ctx context.Context, workspaceID, userID, id string, favourite bool) error {
	if favourite {
		_, err := s.Pool.Exec(ctx, `
			INSERT INTO filter_favourites (filter_id, user_id)
			SELECT id, $3 FROM filters WHERE id=$1 AND workspace_id=$2
			ON CONFLICT DO NOTHING`, id, workspaceID, userID)
		return err
	}
	_, err := s.Pool.Exec(ctx, `
		DELETE FROM filter_favourites ff
		USING filters f
		WHERE ff.filter_id=f.id AND f.id=$1 AND f.workspace_id=$2 AND ff.user_id=$3`, id, workspaceID, userID)
	return err
}

// NextCustomFieldNumber returns the next suffix for customfield_NNNNN ids.
func (s *Store) NextCustomFieldNumber(ctx context.Context) (int, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM custom_fields`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	maxNum := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		var n int
		if _, err := fmt.Sscanf(id, "customfield_%d", &n); err == nil && n > maxNum {
			maxNum = n
		}
	}
	return maxNum - 10000 + 1, rows.Err()
}
