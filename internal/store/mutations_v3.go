package store

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

const worklogJoin = `
SELECT w.id, w.issue_id, w.author_id, COALESCE(u.display_name,''),
       COALESCE(w.comment::text,''), w.time_spent_seconds,
       to_char(w.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
FROM worklogs w LEFT JOIN users u ON u.id = w.author_id
`

func scanWorklog(row pgx.Row) (*models.Worklog, error) {
	w := &models.Worklog{}
	var comment string
	if err := row.Scan(&w.ID, &w.IssueID, &w.AuthorID, &w.AuthorName, &comment, &w.TimeSpentSeconds, &w.Created); err != nil {
		return nil, err
	}
	if comment != "" {
		w.Comment = json.RawMessage(comment)
	}
	return w, nil
}

func (s *Store) CreateWorklog(ctx context.Context, actorID, workspaceID, issueID string, comment json.RawMessage, seconds int) (*models.Worklog, *models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id := NewID("wl")
	var commentArg any
	if len(comment) > 0 {
		commentArg = comment
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO worklogs (id, issue_id, workspace_id, author_id, comment, time_spent_seconds)
		VALUES ($1,$2,$3,$4,$5,$6)`, id, issueID, workspaceID, actorID, commentArg, seconds); err != nil {
		return nil, nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	worklog, err := scanWorklog(tx.QueryRow(ctx, worklogJoin+`WHERE w.id=$1`, id))
	if err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(models.WorklogUpsertPayload{Worklog: *worklog})
	if err != nil {
		return nil, nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityWorklog, EntityID: id,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return worklog, action, nil
}

func (s *Store) WorklogsByIssue(ctx context.Context, issueID string) ([]*models.Worklog, error) {
	rows, err := s.Pool.Query(ctx, worklogJoin+`WHERE w.issue_id=$1 ORDER BY w.created_at, w.id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Worklog
	for rows.Next() {
		w, err := scanWorklog(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) WorklogByID(ctx context.Context, id string) (*models.Worklog, error) {
	return scanWorklog(s.Pool.QueryRow(ctx, worklogJoin+`WHERE w.id=$1`, id))
}

func (s *Store) DeleteWorklog(ctx context.Context, actorID, workspaceID, worklogID string) (*models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	w, err := scanWorklog(tx.QueryRow(ctx, worklogJoin+`WHERE w.id=$1`, worklogID))
	if err != nil {
		return nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(models.WorklogDeletePayload{WorklogID: worklogID, IssueID: w.IssueID})
	if err != nil {
		return nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityWorklog, EntityID: worklogID,
		Op: models.OpDelete, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if _, err := tx.Exec(ctx, `DELETE FROM worklogs WHERE id=$1`, worklogID); err != nil {
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

// ---- attachments ----

const attachmentJoin = `
SELECT a.id, a.issue_id, a.filename, a.mime_type, a.size, a.author_id,
       COALESCE(u.display_name,''),
       to_char(a.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
FROM attachments a LEFT JOIN users u ON u.id = a.author_id
`

func scanAttachment(row pgx.Row) (*models.Attachment, error) {
	a := &models.Attachment{}
	err := row.Scan(&a.ID, &a.IssueID, &a.Filename, &a.MimeType, &a.Size, &a.AuthorID, &a.AuthorName, &a.Created)
	return a, err
}

func (s *Store) CreateAttachment(ctx context.Context, actorID, workspaceID, issueID, filename, mimeType string, size int64, blobRef string) (*models.Attachment, *models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id := NewID("att")
	if _, err := tx.Exec(ctx, `
		INSERT INTO attachments (id, issue_id, workspace_id, filename, mime_type, size, blob_ref, author_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, id, issueID, workspaceID, filename, mimeType, size, blobRef, actorID); err != nil {
		return nil, nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	att, err := scanAttachment(tx.QueryRow(ctx, attachmentJoin+`WHERE a.id=$1`, id))
	if err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(models.AttachmentUpsertPayload{Attachment: *att})
	if err != nil {
		return nil, nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityAttachment, EntityID: id,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return att, action, nil
}

func (s *Store) AttachmentsByIssue(ctx context.Context, issueID string) ([]*models.Attachment, error) {
	rows, err := s.Pool.Query(ctx, attachmentJoin+`WHERE a.issue_id=$1 ORDER BY a.created_at, a.id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AttachmentBlobRef resolves the storage key for an attachment id.
func (s *Store) AttachmentByID(ctx context.Context, id string) (*models.Attachment, error) {
	return scanAttachment(s.Pool.QueryRow(ctx, attachmentJoin+`WHERE a.id=$1`, id))
}

func (s *Store) AttachmentBlobRef(ctx context.Context, id string) (blobRef string, filename string, mimeType string, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT blob_ref, filename, mime_type FROM attachments WHERE id=$1`, id).
		Scan(&blobRef, &filename, &mimeType)
	return
}

func (s *Store) DeleteAttachment(ctx context.Context, actorID, workspaceID, attachmentID string) (blobRef string, issueID string, action *models.Action, err error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", "", nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	err = tx.QueryRow(ctx, `SELECT blob_ref, issue_id FROM attachments WHERE id=$1`, attachmentID).Scan(&blobRef, &issueID)
	if err != nil {
		return "", "", nil, err
	}
	seq, e := nextSeq(ctx, tx, workspaceID)
	if e != nil {
		return "", "", nil, e
	}
	payload, e := json.Marshal(models.AttachmentDeletePayload{AttachmentID: attachmentID, IssueID: issueID})
	if e != nil {
		return "", "", nil, e
	}
	action = &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityAttachment, EntityID: attachmentID,
		Op: models.OpDelete, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if _, err = tx.Exec(ctx, `DELETE FROM attachments WHERE id=$1`, attachmentID); err != nil {
		return "", "", nil, err
	}
	if err = appendAction(ctx, tx, action); err != nil {
		return "", "", nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", "", nil, err
	}
	return blobRef, issueID, action, nil
}
