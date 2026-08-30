// Package authz: server-side permission decisions.
// V5: workspace membership + role (admins see everything) + issue security
// levels from the project's security scheme.
package authz

import (
	"context"

	"github.com/e6qu/zzira/internal/store"
)

// IsWorkspaceAdmin reports admin role (admins bypass issue security).
func IsWorkspaceAdmin(ctx context.Context, st *store.Store, workspaceID, userID string) (bool, error) {
	return st.IsAdmin(ctx, workspaceID, userID)
}

// CanSeeIssue evaluates the issue's security level against the caller.
func CanSeeIssue(ctx context.Context, st *store.Store, workspaceID, projectID, userID, securityLevelID string) (bool, error) {
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
	scheme, err := st.SecuritySchemeForProject(ctx, projectID)
	if err != nil {
		return false, err
	}
	if scheme == nil {
		return true, nil
	}
	for _, lvl := range scheme.Levels {
		if lvl.ID != securityLevelID {
			continue
		}
		for _, m := range lvl.Members {
			if m == userID {
				return true, nil
			}
		}
		return false, nil
	}
	// level no longer exists in the scheme: treat as visible
	return true, nil
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
	var allowed map[string]bool
	for _, lvl := range scheme.Levels {
		if lvl.ID == securityLevelID {
			allowed = map[string]bool{}
			for _, m := range lvl.Members {
				allowed[m] = true
			}
			break
		}
	}
	if allowed == nil {
		return nil, nil // level unknown: nobody excluded
	}
	members, err := st.MembersByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range members {
		if allowed[m.ID] {
			continue
		}
		admin, err := IsWorkspaceAdmin(ctx, st, workspaceID, m.ID)
		if err != nil {
			return nil, err
		}
		if !admin {
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
