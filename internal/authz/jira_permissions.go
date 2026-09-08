package authz

import (
	"context"

	"github.com/e6qu/zzira/internal/store"
)

// JiraPermissions returns the Jira permission keys that the current product
// authorization model grants to an active workspace user. Workflow validators
// consume this map at transition execution time.
func JiraPermissions(ctx context.Context, st *store.Store, workspaceID, userID string) (map[string]bool, error) {
	roles, err := st.RolesForUserInWorkspace(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	permissions := make(map[string]bool)
	member, admin := false, false
	for _, role := range roles {
		member = member || permissionRoles[ViewSite][role]
		admin = admin || permissionRoles[AdministerSite][role]
	}
	if !member {
		return permissions, nil
	}
	for _, key := range []string{
		"BROWSE_PROJECTS", "CREATE_ISSUES", "EDIT_ISSUES", "TRANSITION_ISSUES",
		"ADD_COMMENTS", "CREATE_ATTACHMENTS", "DELETE_OWN_COMMENTS", "DELETE_OWN_ATTACHMENTS",
	} {
		permissions[key] = true
	}
	if admin {
		for _, key := range []string{"ADMINISTER_PROJECTS", "EDIT_WORKFLOW", "EDIT_ISSUE_LAYOUT"} {
			permissions[key] = true
		}
	}
	return permissions, nil
}
