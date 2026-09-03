// Package commands is the single mutation layer. Both edges (web + REST) call
// into it; nothing else may write state or append actions.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type Service struct {
	Store *store.Store
	Blobs attachments.Store
}

type CreateIssueInput struct {
	ActorID     string
	WorkspaceID string
	ProjectKey  string
	Summary     string
	Description string // plain text in V0; stored as an ADF paragraph
	IssueTypeID string
	PriorityID  string
	AssigneeID  string
	Fields      map[string]json.RawMessage
}

// DeleteIssue removes the issue transactionally, then cleans up attachment
// blobs whose metadata was cascade-deleted with it.
func (s *Service) DeleteIssue(ctx context.Context, actorID, workspaceID, issueIDOrKey, reason string) (*models.Action, error) {
	issue, err := s.visibleIssue(ctx, actorID, workspaceID, issueIDOrKey)
	if err != nil {
		return nil, err
	}
	action, blobRefs, err := s.Store.DeleteIssue(ctx, actorID, workspaceID, issue.ID, reason)
	if err != nil {
		return nil, err
	}
	if s.Blobs == nil && len(blobRefs) > 0 {
		return action, fmt.Errorf("issue deleted but %d attachment blobs could not be cleaned up: storage not configured", len(blobRefs))
	}
	var cleanupErr error
	for _, ref := range blobRefs {
		if err := s.Blobs.Delete(ctx, ref); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete attachment blob %q: %w", ref, err))
		}
	}
	return action, cleanupErr
}

func (s *Service) CreateIssue(ctx context.Context, in CreateIssueInput) (*models.Issue, *models.Action, error) {
	if in.ActorID == "" {
		return nil, nil, fmt.Errorf("no actor")
	}
	if len(in.Summary) == 0 || len(in.Summary) > 255 {
		return nil, nil, fmt.Errorf("summary is required (max 255 chars)")
	}
	if in.IssueTypeID == "" {
		return nil, nil, fmt.Errorf("issue type is required")
	}
	project, err := s.Store.ProjectByKey(ctx, in.WorkspaceID, in.ProjectKey)
	if err != nil {
		return nil, nil, fmt.Errorf("project %q not found in workspace", in.ProjectKey)
	}
	issueType, err := s.Store.IssueTypeByIDOrName(ctx, in.IssueTypeID)
	if err != nil {
		return nil, nil, fmt.Errorf("issue type %q not found", in.IssueTypeID)
	}
	priorityID := ""
	if in.PriorityID != "" {
		priority, err := s.Store.PriorityByIDOrName(ctx, in.PriorityID)
		if err != nil {
			return nil, nil, fmt.Errorf("priority %q not found", in.PriorityID)
		}
		priorityID = priority.ID
	}
	description := plainTextToADF(in.Description)
	issue, action, err := s.Store.CreateIssue(ctx, in.ActorID, project.ID, in.Summary,
		description, "st_todo", issueType.ID, priorityID, in.AssigneeID, in.Fields)
	if err != nil {
		return nil, nil, err
	}
	return issue, action, nil
}

// plainTextToADF wraps plain text into the minimal ADF document. Replaced by the
// real ADF edge in V1/V3.
func plainTextToADF(text string) json.RawMessage {
	doc := map[string]any{
		"type":    "doc",
		"version": 1,
	}
	if text == "" {
		b, _ := json.Marshal(doc)
		return b
	}
	doc["content"] = []map[string]any{{
		"type": "paragraph",
		"content": []map[string]any{{
			"type": "text",
			"text": text,
		}},
	}}
	b, _ := json.Marshal(doc)
	return b
}
