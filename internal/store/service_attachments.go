package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
)

func (s *Store) CreateServiceTemporaryAttachment(ctx context.Context, value models.ServiceTemporaryAttachment) error {
	result, err := s.Pool.Exec(ctx, `INSERT INTO service_temporary_attachments(id,workspace_id,service_desk_id,author_id,filename,mime_type,size,blob_ref)
		SELECT $1,$2,sd.id,$4,$5,$6,$7,$8 FROM service_desks sd WHERE sd.workspace_id=$2 AND sd.id=$3`, value.ID, value.WorkspaceID, value.ServiceDeskID, value.AuthorID, value.Filename, value.MimeType, value.Size, value.BlobRef)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("service desk does not exist")
	}
	return nil
}

func (s *Store) FinalizeServiceAttachments(ctx context.Context, actorID, workspaceID, requestIssueID, commentID string, temporaryIDs []string, public bool) ([]models.ServiceRequestAttachment, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var deskID string
	if err := tx.QueryRow(ctx, `SELECT service_desk_id FROM service_requests WHERE workspace_id=$1 AND issue_id=$2 FOR UPDATE`, workspaceID, requestIssueID).Scan(&deskID); err != nil {
		return nil, err
	}
	values := make([]models.ServiceRequestAttachment, 0, len(temporaryIDs))
	seen := map[string]bool{}
	for _, temporaryID := range temporaryIDs {
		if seen[temporaryID] {
			continue
		}
		seen[temporaryID] = true
		var filename, mimeType, blobRef string
		var size int64
		err := tx.QueryRow(ctx, `SELECT filename,mime_type,size,blob_ref FROM service_temporary_attachments
			WHERE id=$1 AND workspace_id=$2 AND service_desk_id=$3 AND author_id=$4 AND expires_at>now() FOR UPDATE`, temporaryID, workspaceID, deskID, actorID).Scan(&filename, &mimeType, &size, &blobRef)
		if err != nil {
			return nil, fmt.Errorf("temporary attachment %q does not exist or cannot be used", temporaryID)
		}
		attachmentID := NewID("att")
		if _, err := tx.Exec(ctx, `INSERT INTO attachments(id,issue_id,workspace_id,filename,mime_type,size,blob_ref,author_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, attachmentID, requestIssueID, workspaceID, filename, mimeType, size, blobRef, actorID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO service_request_attachments(attachment_id,request_issue_id,comment_id,public) VALUES($1,$2,$3,$4)`, attachmentID, requestIssueID, commentID, public); err != nil {
			return nil, err
		}
		attachment, err := scanAttachment(tx.QueryRow(ctx, attachmentJoin+`WHERE a.id=$1`, attachmentID))
		if err != nil {
			return nil, err
		}
		seq, err := nextSeq(ctx, tx, workspaceID)
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(models.AttachmentUpsertPayload{Attachment: *attachment})
		if err != nil {
			return nil, err
		}
		action := &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityAttachment, EntityID: attachmentID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}
		if err := appendAction(ctx, tx, action); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM service_temporary_attachments WHERE id=$1`, temporaryID); err != nil {
			return nil, err
		}
		values = append(values, models.ServiceRequestAttachment{Attachment: *attachment, CommentID: commentID, Public: public})
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("at least one temporary attachment is required")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return values, nil
}

func (s *Store) ServiceRequestAttachments(ctx context.Context, requestIssueID string, includeInternal bool) ([]models.ServiceRequestAttachment, error) {
	rows, err := s.Pool.Query(ctx, `SELECT a.id,a.issue_id,a.filename,a.mime_type,a.size,a.author_id,
		COALESCE(u.display_name,''),to_char(a.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),sra.comment_id,sra.public
		FROM attachments a LEFT JOIN users u ON u.id=a.author_id JOIN service_request_attachments sra ON sra.attachment_id=a.id
		WHERE sra.request_issue_id=$1 AND ($2 OR sra.public) ORDER BY a.created_at,a.id`, requestIssueID, includeInternal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]models.ServiceRequestAttachment, 0)
	for rows.Next() {
		var value models.ServiceRequestAttachment
		if err := rows.Scan(&value.Attachment.ID, &value.Attachment.IssueID, &value.Attachment.Filename, &value.Attachment.MimeType, &value.Attachment.Size, &value.Attachment.AuthorID, &value.Attachment.AuthorName, &value.Attachment.Created, &value.CommentID, &value.Public); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) ServiceCommentAttachments(ctx context.Context, requestIssueID, commentID string, includeInternal bool) ([]models.ServiceRequestAttachment, error) {
	values, err := s.ServiceRequestAttachments(ctx, requestIssueID, includeInternal)
	if err != nil {
		return nil, err
	}
	filtered := make([]models.ServiceRequestAttachment, 0)
	for _, value := range values {
		if value.CommentID == commentID {
			filtered = append(filtered, value)
		}
	}
	return filtered, nil
}

func (s *Store) ServiceRequestAttachment(ctx context.Context, requestIssueID, attachmentID string, includeInternal bool) (*models.ServiceRequestAttachment, error) {
	value := &models.ServiceRequestAttachment{}
	err := s.Pool.QueryRow(ctx, `SELECT a.id,a.issue_id,a.filename,a.mime_type,a.size,a.author_id,
		COALESCE(u.display_name,''),to_char(a.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),sra.comment_id,sra.public
		FROM attachments a LEFT JOIN users u ON u.id=a.author_id JOIN service_request_attachments sra ON sra.attachment_id=a.id
		WHERE sra.request_issue_id=$1 AND a.id=$2 AND ($3 OR sra.public)`, requestIssueID, attachmentID, includeInternal).Scan(
		&value.Attachment.ID, &value.Attachment.IssueID, &value.Attachment.Filename, &value.Attachment.MimeType, &value.Attachment.Size, &value.Attachment.AuthorID, &value.Attachment.AuthorName, &value.Attachment.Created, &value.CommentID, &value.Public)
	return value, err
}

func (s *Store) TakeExpiredServiceTemporaryAttachments(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `DELETE FROM service_temporary_attachments WHERE id IN (
		SELECT id FROM service_temporary_attachments WHERE expires_at<=now() ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT $1
	) RETURNING blob_ref`, limit)
	if err != nil {
		return nil, err
	}
	refs := make([]string, 0)
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return refs, nil
}

func (s *Store) ServiceAttachmentIsPublic(ctx context.Context, attachmentID string) (isService, public bool, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_request_attachments WHERE attachment_id=$1),COALESCE((SELECT public FROM service_request_attachments WHERE attachment_id=$1),false)`, attachmentID).Scan(&isService, &public)
	return
}
