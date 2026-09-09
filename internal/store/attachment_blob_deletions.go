package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type AttachmentBlobDeletion struct {
	BlobRef     string
	WorkspaceID string
	IssueID     string
	Attempts    int
}

// ClaimAttachmentBlobDeletion leases one pending blob cleanup. A crashed
// worker can be replaced after the lease expires, and blob deletion is
// idempotent in every supported attachment store.
func (s *Store) ClaimAttachmentBlobDeletion(ctx context.Context) (AttachmentBlobDeletion, error) {
	var deletion AttachmentBlobDeletion
	err := s.Pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT blob_ref FROM attachment_blob_deletions
			WHERE completed_at IS NULL AND available_at<=now()
			  AND (leased_until IS NULL OR leased_until<=now())
			ORDER BY available_at,created_at,blob_ref
			FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE attachment_blob_deletions deletion
		SET attempts=attempts+1,leased_until=now()+interval '2 minutes'
		FROM candidate WHERE deletion.blob_ref=candidate.blob_ref
		RETURNING deletion.blob_ref,deletion.workspace_id,deletion.issue_id,deletion.attempts`).
		Scan(&deletion.BlobRef, &deletion.WorkspaceID, &deletion.IssueID, &deletion.Attempts)
	return deletion, err
}

func (s *Store) CompleteAttachmentBlobDeletion(ctx context.Context, blobRef string) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE attachment_blob_deletions
		SET completed_at=now(),leased_until=NULL,last_error=''
		WHERE blob_ref=$1 AND completed_at IS NULL`, blobRef)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) DeferAttachmentBlobDeletion(ctx context.Context, blobRef, message string, attempts int) error {
	if attempts < 1 {
		attempts = 1
	}
	delay := time.Duration(min(3600, attempts*30)) * time.Second
	tag, err := s.Pool.Exec(ctx, `
		UPDATE attachment_blob_deletions
		SET available_at=now()+$2::interval,leased_until=NULL,last_error=$3
		WHERE blob_ref=$1 AND completed_at IS NULL`, blobRef, delay.String(), message)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) AttachmentBlobDeletionPending(ctx context.Context, blobRef string) (bool, error) {
	var pending bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM attachment_blob_deletions WHERE blob_ref=$1 AND completed_at IS NULL
	)`, blobRef).Scan(&pending)
	return pending, err
}
