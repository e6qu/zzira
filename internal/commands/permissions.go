package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrPermission is what every refusal for a missing project permission wraps,
// so an edge can answer them all the same way.
var ErrPermission = errors.New("missing project permission")

// PermissionError names the Jira project permission a command needed.
type PermissionError struct {
	Permission string
	Message    string
}

func (e *PermissionError) Error() string { return e.Message }

func (e *PermissionError) Unwrap() error { return ErrPermission }

// permissionMessages are the refusals Jira gives for each project permission.
var permissionMessages = map[string]string{
	"ASSIGN_ISSUES":             "You do not have permission to assign work items in this project.",
	"CREATE_ISSUES":             "You do not have permission to create work items in this project.",
	"DELETE_ISSUES":             "You do not have permission to delete this work item.",
	"EDIT_ISSUES":               "You do not have permission to edit work items in this project.",
	"LINK_ISSUES":               "You do not have permission to link work items in this project.",
	"MANAGE_SPRINTS_PERMISSION": "You do not have permission to manage sprints in this project.",
	"MANAGE_WATCHERS":           "You do not have the permission to manage the watcher list.",
	"MODIFY_REPORTER":           "You do not have permission to change the reporter in this project.",
	"MOVE_ISSUES":               "You do not have permission to move work items out of this project.",
	"RESOLVE_ISSUES":            "You do not have permission to set fix versions in this project.",
	"SCHEDULE_ISSUES":           "You do not have permission to schedule work items in this project.",
	"SET_ISSUE_SECURITY":        "You do not have permission to set the security level of work items in this project.",
	"TRANSITION_ISSUES":         "You do not have permission to transition this work item.",
}

// requirePermission refuses the command unless the actor holds permission in
// the project, for the work item when one is named.
func (s *Service) requirePermission(ctx context.Context, workspaceID, actorID, projectID, issueID, permission string) error {
	allowed, err := s.Store.HasProjectPermission(ctx, workspaceID, actorID, projectID, issueID, permission)
	if err != nil {
		return err
	}
	if allowed {
		return nil
	}
	message, ok := permissionMessages[permission]
	if !ok {
		message = fmt.Sprintf("You do not have the %s permission in this project.", permission)
	}
	return &PermissionError{Permission: permission, Message: message}
}

// requirePermissions checks each permission in turn and returns the first
// refusal.
func (s *Service) requirePermissions(ctx context.Context, workspaceID, actorID, projectID, issueID string, permissions ...string) error {
	for _, permission := range permissions {
		if err := s.requirePermission(ctx, workspaceID, actorID, projectID, issueID, permission); err != nil {
			return err
		}
	}
	return nil
}

// ErrNotAssignable refuses an assignee who lacks Assignable user.
var ErrNotAssignable = errors.New("cannot be assigned work items in this project")

// requireAssignable refuses an assignee who does not hold Assignable user in
// the project, as Jira does for every assignment.
func (s *Service) requireAssignable(ctx context.Context, workspaceID, projectID, issueID, assigneeID string) error {
	if assigneeID == "" {
		return nil
	}
	allowed, err := s.Store.HasProjectPermission(ctx, workspaceID, assigneeID, projectID, issueID, "ASSIGNABLE_USER")
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("user %q %w", assigneeID, ErrNotAssignable)
	}
	return nil
}

// setsFixVersions reports whether a field map writes fix versions, which
// Jira ties to Resolve issues.
func setsFixVersions(fields map[string]json.RawMessage) bool {
	raw, ok := fields["fixVersions"]
	if !ok {
		return false
	}
	var values []json.RawMessage
	return json.Unmarshal(raw, &values) != nil || len(values) > 0
}
