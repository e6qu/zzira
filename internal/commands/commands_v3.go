package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func (s *Service) AddWorklog(ctx context.Context, actorID, workspaceID, issueIDOrKey string, comment json.RawMessage, seconds int) (*models.Worklog, *models.Action, error) {
	issue, err := s.Store.IssueByIDOrKey(ctx, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, nil, fmt.Errorf("issue %q not found", issueIDOrKey)
	}
	if seconds <= 0 {
		return nil, nil, fmt.Errorf("timeSpentSeconds must be positive")
	}
	return s.Store.CreateWorklog(ctx, actorID, workspaceID, issue.ID, comment, seconds)
}

func (s *Service) DeleteWorklog(ctx context.Context, actorID, workspaceID, worklogID string) (*models.Action, error) {
	w, err := s.Store.WorklogByID(ctx, worklogID)
	if err != nil {
		return nil, fmt.Errorf("worklog %q not found", worklogID)
	}
	if w.AuthorID != actorID {
		return nil, fmt.Errorf("only the author may delete a worklog")
	}
	return s.Store.DeleteWorklog(ctx, actorID, workspaceID, worklogID)
}

// AddAttachment streams the blob to storage, then records metadata + action in
// one transaction. On DB failure the blob is removed again.
func (s *Service) AddAttachment(ctx context.Context, actorID, workspaceID, issueIDOrKey, filename, mimeType string, r io.Reader) (*models.Attachment, *models.Action, error) {
	issue, err := s.Store.IssueByIDOrKey(ctx, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, nil, fmt.Errorf("issue %q not found", issueIDOrKey)
	}
	if s.Blobs == nil {
		return nil, nil, fmt.Errorf("attachment storage not configured")
	}
	blobRef := store.NewID("blob")
	size, err := s.Blobs.Put(ctx, blobRef, r)
	if err != nil {
		return nil, nil, fmt.Errorf("store blob: %w", err)
	}
	att, action, err := s.Store.CreateAttachment(ctx, actorID, workspaceID, issue.ID, filename, mimeType, size, blobRef)
	if err != nil {
		_ = s.Blobs.Delete(ctx, blobRef)
		return nil, nil, err
	}
	return att, action, nil
}

func (s *Service) DeleteAttachment(ctx context.Context, actorID, workspaceID, attachmentID string) (*models.Action, error) {
	blobRef, _, action, err := s.Store.DeleteAttachment(ctx, actorID, workspaceID, attachmentID)
	if err != nil {
		return nil, err
	}
	if s.Blobs != nil {
		_ = s.Blobs.Delete(ctx, blobRef)
	}
	return action, nil
}

var _ = attachments.ErrNotFound
