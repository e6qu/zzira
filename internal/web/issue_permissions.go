package web

import (
	"context"

	"github.com/e6qu/zzira/internal/models"
)

// issuePermissions resolves each control's permission for this reader and issue.
func (h *Handler) issuePermissions(ctx context.Context, workspaceID, readerID string, issue *models.Issue) (map[string]bool, error) {
	permissions := make(map[string]bool)
	for _, permission := range []string{
		"EDIT_ISSUES", "ASSIGN_ISSUES", "SCHEDULE_ISSUES", "SET_ISSUE_SECURITY", "RESOLVE_ISSUES", "TRANSITION_ISSUES",
		"DELETE_ISSUES", "ADD_COMMENTS", "WORK_ON_ISSUES", "LINK_ISSUES", "CREATE_ATTACHMENTS",
		"DELETE_ALL_ATTACHMENTS", "DELETE_OWN_ATTACHMENTS", "DELETE_ALL_WORKLOGS", "DELETE_OWN_WORKLOGS",
	} {
		allowed, err := h.Store.HasProjectPermission(ctx, workspaceID, readerID, issue.ProjectID, issue.ID, permission)
		if err != nil {
			return nil, err
		}
		permissions[permission] = allowed
	}
	return permissions, nil
}
