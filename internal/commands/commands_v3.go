package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func (s *Service) AddWorklog(ctx context.Context, actorID, workspaceID, issueIDOrKey string, comment json.RawMessage, seconds int) (*models.Worklog, *models.Action, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, nil, err
	}
	if seconds <= 0 {
		return nil, nil, fmt.Errorf("timeSpentSeconds must be positive")
	}
	return s.Store.CreateWorklog(ctx, actorID, workspaceID, issue.ID, comment, seconds)
}

func (s *Service) DeleteWorklog(ctx context.Context, actorID, workspaceID, worklogID string) (*models.Action, error) {
	w, err := s.Store.WorklogByID(ctx, workspaceID, worklogID)
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
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, nil, err
	}
	if s.Blobs == nil {
		return nil, nil, fmt.Errorf("attachment storage not configured")
	}
	filename, err = normalizedAttachmentFilename(filename)
	if err != nil {
		return nil, nil, err
	}
	mimeType, err = normalizedAttachmentMIMEType(mimeType)
	if err != nil {
		return nil, nil, err
	}
	blobRef := store.NewID("blob")
	size, err := s.Blobs.Put(ctx, blobRef, r)
	if err != nil {
		return nil, nil, fmt.Errorf("store blob: %w", err)
	}
	att, action, err := s.Store.CreateAttachment(ctx, actorID, workspaceID, issue.ID, filename, mimeType, size, blobRef)
	if err != nil {
		if cleanupErr := s.Blobs.Delete(ctx, blobRef); cleanupErr != nil {
			return nil, nil, errors.Join(err, fmt.Errorf("remove orphaned blob %q: %w", blobRef, cleanupErr))
		}
		return nil, nil, err
	}
	return att, action, nil
}

func normalizedAttachmentFilename(name string) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if idx := strings.LastIndexByte(name, '/'); idx >= 0 {
		name = name[idx+1:]
	}
	if name == "" || strings.ContainsAny(name, "\r\n\x00") {
		return "", fmt.Errorf("attachment filename is invalid")
	}
	return name, nil
}

func normalizedAttachmentMIMEType(value string) (string, error) {
	if value == "" {
		return "application/octet-stream", nil
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return "", fmt.Errorf("attachment MIME type is invalid")
	}
	return mediaType, nil
}

func (s *Service) DeleteAttachment(ctx context.Context, actorID, workspaceID, attachmentID string) (*models.Action, error) {
	att, err := s.Store.AttachmentByID(ctx, workspaceID, attachmentID)
	if err != nil {
		return nil, fmt.Errorf("attachment %q not found", attachmentID)
	}
	if _, err := s.visibleIssue(ctx, actorID, workspaceID, att.IssueID); err != nil {
		return nil, fmt.Errorf("attachment %q not found", attachmentID)
	}
	if att.AuthorID != actorID {
		return nil, fmt.Errorf("only the author may delete an attachment")
	}
	blobRef, _, action, err := s.Store.DeleteAttachment(ctx, actorID, workspaceID, attachmentID)
	if err != nil {
		return nil, err
	}
	if s.Blobs != nil {
		if err := s.Blobs.Delete(ctx, blobRef); err != nil {
			return action, fmt.Errorf("attachment metadata deleted but blob %q cleanup failed: %w", blobRef, err)
		}
	}
	return action, nil
}
