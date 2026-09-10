package authz

import (
	"context"

	"github.com/e6qu/zzira/internal/store"
)

// IsWorkspaceAdmin reports admin role (admins bypass issue security).
func IsWorkspaceAdmin(ctx context.Context, st *store.Store, workspaceID, userID string) (bool, error) {
	return Allowed(ctx, st, workspaceID, userID, AdministerSite)
}

// CanSeeIssue evaluates the issue's security level against the caller.
func CanSeeIssue(ctx context.Context, st *store.Store, workspaceID, projectID, userID, issueID, securityLevelID string) (bool, error) {
	browse, err := st.HasProjectPermission(ctx, workspaceID, userID, projectID, issueID, "BROWSE_PROJECTS")
	if err != nil || !browse {
		return browse, err
	}
	if securityLevelID == "" {
		return true, nil
	}
	admin, err := IsWorkspaceAdmin(ctx, st, workspaceID, userID)
	if err != nil {
		return false, err
	}
	if admin {
		return true, nil
	}
	return st.CanUseIssueSecurityLevel(ctx, workspaceID, projectID, issueID, userID, securityLevelID)
}

// excludedMembers lists workspace members who can NOT see the level.
func excludedMembers(ctx context.Context, st *store.Store, workspaceID, projectID, securityLevelID string) ([]string, error) {
	scheme, err := st.SecuritySchemeForProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if scheme == nil {
		return nil, nil // no scheme: nobody excluded
	}
	known := false
	for _, lvl := range scheme.Levels {
		if lvl.ID == securityLevelID {
			known = true
			break
		}
	}
	if !known {
		return nil, nil // level unknown: nobody excluded
	}
	members, err := st.MembersByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range members {
		admin, err := IsWorkspaceAdmin(ctx, st, workspaceID, m.ID)
		if err != nil {
			return nil, err
		}
		allowed, visibilityErr := st.CanUseIssueSecurityLevel(ctx, workspaceID, projectID, "", m.ID, securityLevelID)
		if visibilityErr != nil {
			return nil, visibilityErr
		}
		if !admin && !allowed {
			out = append(out, m.ID)
		}
	}
	return out, nil
}

// ExcludedMembersForLevel is the commands-layer entry point.
func ExcludedMembersForLevel(ctx context.Context, st *store.Store, workspaceID, projectID, securityLevelID string) ([]string, error) {
	if securityLevelID == "" {
		return nil, nil
	}
	return excludedMembers(ctx, st, workspaceID, projectID, securityLevelID)
}
