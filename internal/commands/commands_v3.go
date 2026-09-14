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
	return s.AddWorklogWithEstimate(ctx, actorID, workspaceID, issueIDOrKey, comment, seconds, store.WorklogEstimate{Notify: true})
}

// ErrWorklogPermission refuses logging or changing work without the Work on
// issues, Edit own or all worklogs, or Delete own or all worklogs permission.
var ErrWorklogPermission = errors.New("you do not have permission to change this work log")

// worklogPermitted reports whether the actor may act on work logged by author:
// the "all" permission covers anyone's work, the "own" permission their own.
func (s *Service) worklogPermitted(ctx context.Context, workspaceID, actorID string, issue *models.Issue, authorID, allPermission, ownPermission string) (bool, error) {
	allowed, err := s.Store.HasProjectPermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, allPermission)
	if err != nil || allowed || authorID != actorID {
		return allowed, err
	}
	return s.Store.HasProjectPermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, ownPermission)
}

// AddWorklogWithEstimate logs work with Work on issues and moves the remaining
// estimate as the adjustment says.
func (s *Service) AddWorklogWithEstimate(ctx context.Context, actorID, workspaceID, issueIDOrKey string, comment json.RawMessage, seconds int, estimate store.WorklogEstimate) (*models.Worklog, *models.Action, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, nil, err
	}
	configuration, err := s.jiraSiteConfiguration(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if !configuration.TimeTrackingEnabled {
		return nil, nil, fmt.Errorf("time tracking is disabled for this site")
	}
	if seconds <= 0 {
		return nil, nil, fmt.Errorf("timeSpentSeconds must be positive")
	}
	allowed, err := s.Store.HasProjectPermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, "WORK_ON_ISSUES")
	if err != nil {
		return nil, nil, err
	}
	if !allowed {
		return nil, nil, ErrWorklogPermission
	}
	worklog, action, err := s.Store.CreateWorklogWithEstimate(ctx, actorID, workspaceID, issue.ID, comment, seconds, estimate)
	if err == nil && estimate.Notify {
		err = s.deliverIssueEvent(ctx, workspaceID, actorID, issue, action, 11, "worklog_created", "logged work on")
	}
	return worklog, action, err
}

// UpdateWorklog changes logged work with Edit all worklogs, or Edit own
// worklogs for the actor's own, moving the remaining estimate as asked.
func (s *Service) UpdateWorklog(ctx context.Context, actorID, workspaceID, worklogID string, comment json.RawMessage, seconds *int, estimate store.WorklogEstimate) (*models.Worklog, *models.Action, error) {
	w, err := s.Store.WorklogByID(ctx, workspaceID, worklogID)
	if err != nil {
		return nil, nil, fmt.Errorf("worklog %q not found", worklogID)
	}
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, w.IssueID)
	if err != nil {
		return nil, nil, fmt.Errorf("worklog %q not found", worklogID)
	}
	configuration, err := s.jiraSiteConfiguration(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if !configuration.TimeTrackingEnabled {
		return nil, nil, fmt.Errorf("time tracking is disabled for this site")
	}
	allowed, err := s.worklogPermitted(ctx, workspaceID, actorID, issue, w.AuthorID, "EDIT_ALL_WORKLOGS", "EDIT_OWN_WORKLOGS")
	if err != nil {
		return nil, nil, err
	}
	if !allowed {
		return nil, nil, ErrWorklogPermission
	}
	worklog, action, err := s.Store.UpdateWorklogWithEstimate(ctx, actorID, workspaceID, w.ID, comment, seconds, estimate)
	if err == nil && estimate.Notify {
		err = s.deliverIssueEvent(ctx, workspaceID, actorID, issue, action, 14, "worklog_updated", "updated work logged on")
	}
	return worklog, action, err
}

func (s *Service) DeleteWorklog(ctx context.Context, actorID, workspaceID, worklogID string) (*models.Action, error) {
	return s.DeleteWorklogWithEstimate(ctx, actorID, workspaceID, worklogID, store.WorklogEstimate{Notify: true})
}

// DeleteWorklogWithEstimate removes logged work with Delete all worklogs, or
// Delete own worklogs for the actor's own, moving the remaining estimate back.
func (s *Service) DeleteWorklogWithEstimate(ctx context.Context, actorID, workspaceID, worklogID string, estimate store.WorklogEstimate) (*models.Action, error) {
	w, err := s.Store.WorklogByID(ctx, workspaceID, worklogID)
	if err != nil {
		return nil, fmt.Errorf("worklog %q not found", worklogID)
	}
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, w.IssueID)
	if err != nil {
		return nil, fmt.Errorf("worklog %q not found", worklogID)
	}
	allowed, err := s.worklogPermitted(ctx, workspaceID, actorID, issue, w.AuthorID, "DELETE_ALL_WORKLOGS", "DELETE_OWN_WORKLOGS")
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrWorklogPermission
	}
	action, err := s.Store.DeleteWorklogWithEstimate(ctx, actorID, workspaceID, w.ID, estimate)
	if err == nil && estimate.Notify {
		err = s.deliverIssueEvent(ctx, workspaceID, actorID, issue, action, 15, "worklog_deleted", "deleted work logged on")
	}
	return action, err
}

// AddAttachment streams the blob to storage, then records metadata + action in
// one transaction. On DB failure the blob is removed again.
func (s *Service) AddAttachment(ctx context.Context, actorID, workspaceID, issueIDOrKey, filename, mimeType string, r io.Reader) (*models.Attachment, *models.Action, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, nil, err
	}
	allowed, err := s.Store.HasProjectPermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, "CREATE_ATTACHMENTS")
	if err != nil {
		return nil, nil, err
	}
	if !allowed {
		return nil, nil, ErrAttachmentCreatePermission
	}
	configuration, err := s.jiraSiteConfiguration(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if !configuration.AttachmentsEnabled {
		return nil, nil, fmt.Errorf("attachments are disabled for this site")
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
	limit := configuration.AttachmentUploadLimit
	if limit <= 0 {
		limit = 32 << 20
	}
	size, err := s.Blobs.Put(ctx, blobRef, io.LimitReader(r, limit+1))
	if err != nil {
		return nil, nil, fmt.Errorf("store blob: %w", err)
	}
	if size > limit {
		if cleanupErr := s.Blobs.Delete(ctx, blobRef); cleanupErr != nil {
			return nil, nil, errors.Join(ErrAttachmentTooLarge, cleanupErr)
		}
		return nil, nil, ErrAttachmentTooLarge
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

// ErrAttachmentCreatePermission refuses attaching files without the Create
// attachments project permission.
var ErrAttachmentCreatePermission = errors.New("you do not have permission to create attachments for this work item")

// ErrAttachmentTooLarge refuses an attachment larger than the site's
// maximum attachment size.
var ErrAttachmentTooLarge = errors.New("the attachment exceeds the maximum attachment size")

// ErrAttachmentDeletePermission refuses deleting an attachment without the
// Delete own attachments or Delete all attachments permission it needs.
var ErrAttachmentDeletePermission = errors.New("you do not have permission to delete this attachment")

func (s *Service) DeleteAttachment(ctx context.Context, actorID, workspaceID, attachmentID string) (*models.Action, error) {
	att, err := s.Store.AttachmentByID(ctx, workspaceID, attachmentID)
	if err != nil {
		return nil, fmt.Errorf("attachment %q not found", attachmentID)
	}
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, att.IssueID)
	if err != nil {
		return nil, fmt.Errorf("attachment %q not found", attachmentID)
	}
	// Delete own attachments covers the caller's attachments; Delete all
	// attachments covers anyone's.
	allowed, err := s.Store.HasProjectPermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, "DELETE_ALL_ATTACHMENTS")
	if err != nil {
		return nil, err
	}
	if !allowed && att.AuthorID == actorID {
		if allowed, err = s.Store.HasProjectPermission(ctx, workspaceID, actorID, issue.ProjectID, issue.ID, "DELETE_OWN_ATTACHMENTS"); err != nil {
			return nil, err
		}
	}
	if !allowed {
		return nil, ErrAttachmentDeletePermission
	}
	blobRef, _, action, err := s.Store.DeleteAttachment(ctx, actorID, workspaceID, att.ID)
	if err != nil {
		return nil, err
	}
	if s.Blobs != nil {
		if err := s.Blobs.Delete(ctx, blobRef); err != nil {
			if deferErr := s.Store.DeferAttachmentBlobDeletion(ctx, blobRef, err.Error(), 1); deferErr != nil {
				return action, fmt.Errorf("attachment metadata deleted but blob %q cleanup deferral failed after %v: %w", blobRef, err, deferErr)
			}
			return action, nil
		}
		if err := s.Store.CompleteAttachmentBlobDeletion(ctx, blobRef); err != nil {
			return action, fmt.Errorf("attachment blob %q deleted but cleanup completion failed: %w", blobRef, err)
		}
	}
	return action, nil
}
