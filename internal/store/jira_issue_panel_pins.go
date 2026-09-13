package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const apiTaskIssuePanelPins = "jira-issue-panel-pins"

// ErrIssuePanelNotFound is an issue panel module id that names no installed panel.
var ErrIssuePanelNotFound = errors.New("issue panel module not found")

// IssuePanelPinAction pins or unpins a panel on one project.
type IssuePanelPinAction struct {
	ProjectIDOrKey string `json:"projectIdOrKey"`
	Action         string `json:"action"`
}

type issuePanelPinPayload struct {
	ModuleID string                `json:"moduleId"`
	Actions  []IssuePanelPinAction `json:"actions"`
}

// IssuePanelModule resolves a Forge panel module id,
// ari:cloud:ecosystem::extension/{app-id}/{environment-id}/static/{module-key},
// to an installed issue panel on the site.
func (s *Store) IssuePanelModule(ctx context.Context, workspaceID, moduleARI string) (string, error) {
	const prefix = "ari:cloud:ecosystem::extension/"
	if !strings.HasPrefix(moduleARI, prefix) {
		return "", ErrIssuePanelNotFound
	}
	parts := strings.Split(strings.TrimPrefix(moduleARI, prefix), "/")
	if len(parts) != 4 || parts[2] != "static" || parts[0] == "" || parts[3] == "" {
		return "", ErrIssuePanelNotFound
	}
	var moduleID string
	err := s.Pool.QueryRow(ctx, `SELECT m.id FROM app_modules m JOIN app_installations i ON i.id=m.installation_id
		WHERE i.workspace_id=$1 AND i.status='active' AND m.module_type='jira:issuePanel'
		  AND m.module_key=$2 AND (i.app_key=$3 OR i.id::text=$3)
		LIMIT 1`, workspaceID, parts[3], parts[0]).Scan(&moduleID)
	if err != nil {
		return "", ErrIssuePanelNotFound
	}
	return moduleID, nil
}

// EnqueueIssuePanelPins queues pinning and unpinning a panel across projects.
func (s *Store) EnqueueIssuePanelPins(ctx context.Context, workspaceID, actorID, moduleID string, actions []IssuePanelPinAction) (APITask, error) {
	task, err := queuedAPITask(workspaceID, actorID, "Pin or unpin an issue panel on projects", apiTaskIssuePanelPins, issuePanelPinPayload{ModuleID: moduleID, Actions: actions})
	if err != nil {
		return APITask{}, err
	}
	if err := s.enqueueAPITask(ctx, &task); err != nil {
		return APITask{}, err
	}
	return task, nil
}

// IssuePanelPinnedToProject reports whether a panel is pinned on a project.
func (s *Store) IssuePanelPinnedToProject(ctx context.Context, workspaceID, moduleID, projectID string) (bool, error) {
	var pinned bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issue_panel_project_pins WHERE workspace_id=$1 AND module_id=$2 AND project_id=$3)`, workspaceID, moduleID, projectID).Scan(&pinned)
	return pinned, err
}

func (s *Store) executeIssuePanelPinTask(ctx context.Context, task APITask) error {
	var payload issuePanelPinPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode issue panel pins: %w", err)
	}
	type outcome struct {
		ProjectIDOrKey string `json:"projectIdOrKey"`
		Action         string `json:"action"`
		Success        bool   `json:"success"`
		Error          string `json:"error,omitempty"`
	}
	results := []outcome{}
	for _, action := range payload.Actions {
		result := outcome{ProjectIDOrKey: action.ProjectIDOrKey, Action: action.Action}
		var projectID string
		err := s.Pool.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND lifecycle_state='ACTIVE' AND (id=$2 OR upper(key)=upper($2))`, task.WorkspaceID, action.ProjectIDOrKey).Scan(&projectID)
		if err != nil {
			result.Error = "The project was not found."
			results = append(results, result)
			continue
		}
		if action.Action == "PIN" {
			_, err = s.Pool.Exec(ctx, `INSERT INTO issue_panel_project_pins(workspace_id,module_id,project_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, task.WorkspaceID, payload.ModuleID, projectID)
		} else {
			_, err = s.Pool.Exec(ctx, `DELETE FROM issue_panel_project_pins WHERE workspace_id=$1 AND module_id=$2 AND project_id=$3`, task.WorkspaceID, payload.ModuleID, projectID)
		}
		if err != nil {
			return err
		}
		result.Success = true
		results = append(results, result)
	}
	return s.CompleteAPITask(ctx, task, "Issue panel pins updated.", map[string]any{"results": results})
}
