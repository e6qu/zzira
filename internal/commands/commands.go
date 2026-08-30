// Package commands is the single mutation layer. Both edges (web + REST) call
// into it; nothing else may write state or append actions.
package commands

import (
	"context"
	"encoding/json"
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

func (s *Service) CreateIssue(ctx context.Context, in CreateIssueInput) (*models.Issue, *models.Action, error) {
	if in.ActorID == "" {
		return nil, nil, fmt.Errorf("no actor")
	}
	if len(in.Summary) == 0 || len(in.Summary) > 255 {
		return nil, nil, fmt.Errorf("summary is required (max 255 chars)")
	}
	project, err := s.Store.ProjectByKey(ctx, in.WorkspaceID, in.ProjectKey)
	if err != nil {
		return nil, nil, fmt.Errorf("project %q not found in workspace", in.ProjectKey)
	}
	description := plainTextToADF(in.Description)
	issueTypeID := in.IssueTypeID
	if issueTypeID == "" {
		issueTypeID = "it_task"
	}
	issue, action, err := s.Store.CreateIssue(ctx, in.ActorID, project.ID, in.Summary,
		description, "st_todo", issueTypeID, in.PriorityID, in.AssigneeID, in.Fields)
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
