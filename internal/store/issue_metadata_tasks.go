package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Deleting a priority or a resolution rewrites every issue that carries it, so
// Jira runs both as tasks and answers with where to follow them.

// ErrAPITaskConflict is a request to start a task that is already running.
var ErrAPITaskConflict = errors.New("a task for this item is already running")

const (
	apiTaskDeletePriority   = "priority-delete"
	apiTaskDeleteResolution = "resolution-delete"
)

type deletePriorityTaskPayload struct {
	PriorityID string `json:"priorityId"`
}

type deleteResolutionTaskPayload struct {
	ResolutionID string `json:"resolutionId"`
	ReplaceWith  string `json:"replaceWith"`
}

// metadataTaskRunning reports whether a deletion of the same item is already
// queued or running; Jira refuses a second one with 409.
func (s *Store) metadataTaskRunning(ctx context.Context, workspaceID, kind, field, id string) (bool, error) {
	var active bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM api_tasks WHERE workspace_id=$1 AND kind=$2
		AND status IN ('ENQUEUED','RUNNING') AND payload->>$3=$4)`, workspaceID, kind, field, id).Scan(&active)
	return active, err
}

// EnqueuePriorityDeletion checks the priority can be deleted and queues it.
func (s *Store) EnqueuePriorityDeletion(ctx context.Context, workspaceID, actorID, idOrName string) (APITask, error) {
	p, err := s.PriorityInWorkspace(ctx, workspaceID, idOrName)
	if err != nil {
		return APITask{}, err
	}
	if p.IsDefault {
		return APITask{}, fmt.Errorf("%w: the default priority cannot be deleted", ErrIssueMetadataValidation)
	}
	if running, err := s.metadataTaskRunning(ctx, workspaceID, apiTaskDeletePriority, "priorityId", p.ID); err != nil {
		return APITask{}, err
	} else if running {
		return APITask{}, ErrAPITaskConflict
	}
	task, err := queuedAPITask(workspaceID, actorID, "Delete priority "+strings.TrimSpace(p.Name), apiTaskDeletePriority, deletePriorityTaskPayload{PriorityID: p.ID})
	if err != nil {
		return APITask{}, err
	}
	if err := s.enqueueAPITask(ctx, &task); err != nil {
		return APITask{}, err
	}
	return task, nil
}

// EnqueueResolutionDeletion checks the resolution and its replacement and
// queues the deletion.
func (s *Store) EnqueueResolutionDeletion(ctx context.Context, workspaceID, actorID, idOrName, replaceWith string) (APITask, error) {
	current, err := s.ResolutionInWorkspace(ctx, workspaceID, idOrName)
	if err != nil {
		return APITask{}, err
	}
	replacement, err := s.ResolutionInWorkspace(ctx, workspaceID, replaceWith)
	if err != nil {
		return APITask{}, fmt.Errorf("%w: the replacement resolution does not exist", ErrIssueMetadataValidation)
	}
	if replacement.ID == current.ID {
		return APITask{}, fmt.Errorf("%w: a resolution cannot be replaced with itself", ErrIssueMetadataValidation)
	}
	if running, err := s.metadataTaskRunning(ctx, workspaceID, apiTaskDeleteResolution, "resolutionId", current.ID); err != nil {
		return APITask{}, err
	} else if running {
		return APITask{}, ErrAPITaskConflict
	}
	task, err := queuedAPITask(workspaceID, actorID, "Delete resolution "+strings.TrimSpace(current.Name), apiTaskDeleteResolution,
		deleteResolutionTaskPayload{ResolutionID: current.ID, ReplaceWith: replacement.ID})
	if err != nil {
		return APITask{}, err
	}
	if err := s.enqueueAPITask(ctx, &task); err != nil {
		return APITask{}, err
	}
	return task, nil
}

func (s *Store) executePriorityDeletion(ctx context.Context, task APITask) error {
	var payload deletePriorityTaskPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode priority deletion: %w", err)
	}
	moved, err := s.deletePriorityNow(ctx, task.WorkspaceID, payload.PriorityID)
	if err != nil {
		return err
	}
	return s.CompleteAPITask(ctx, task, "Priority deleted.", map[string]any{"issuesUpdated": moved})
}

func (s *Store) executeResolutionDeletion(ctx context.Context, task APITask) error {
	var payload deleteResolutionTaskPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode resolution deletion: %w", err)
	}
	moved, err := s.deleteResolutionNow(ctx, task.WorkspaceID, payload.ResolutionID, payload.ReplaceWith)
	if err != nil {
		return err
	}
	return s.CompleteAPITask(ctx, task, "Resolution deleted.", map[string]any{"issuesUpdated": moved})
}

// SaveIssueTypeAvatar stores an avatar image for an issue type and selects it,
// which is what the issue type avatar upload does in one step.
func (s *Store) SaveIssueTypeAvatar(ctx context.Context, workspaceID, issueTypeID, mediaType string, data []byte) (int64, error) {
	avatar, err := s.StoreAvatar(ctx, workspaceID, "issuetype", issueTypeID, mediaType, data)
	if err != nil {
		return 0, err
	}
	return avatar.ID, s.SelectAvatar(ctx, workspaceID, "issuetype", issueTypeID, avatar.ID)
}
