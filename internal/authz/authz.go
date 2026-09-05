// Package authz contains the server-side permission decisions shared by every
// product edge, background worker, export, and permission-shaped sync stream.
package authz

import (
	"context"

	"github.com/e6qu/zzira/internal/store"
)

type Permission string

const (
	ViewSite            Permission = "site:view"
	AdministerSite      Permission = "site:administer"
	ManageDirectories   Permission = "directories:manage"
	ManageProductAccess Permission = "product-access:manage"
)

var permissionRoles = map[Permission]map[string]bool{
	ViewSite: {
		"atlassian/org-admin":     true,
		"atlassian/site-admin":    true,
		"atlassian/site-user":     true,
		"atlassian/product-admin": true,
		"atlassian/product-user":  true,
	},
	AdministerSite: {
		"atlassian/org-admin":  true,
		"atlassian/site-admin": true,
	},
	ManageDirectories: {
		"atlassian/org-admin":  true,
		"atlassian/site-admin": true,
	},
	ManageProductAccess: {
		"atlassian/org-admin":     true,
		"atlassian/site-admin":    true,
		"atlassian/product-admin": true,
	},
}

// Allowed evaluates direct and group role bindings in the workspace's
// organization/site/product context. Unknown permissions are denied.
func Allowed(ctx context.Context, st *store.Store, workspaceID, userID string, permission Permission) (bool, error) {
	if userID == "" {
		return false, nil
	}
	accepted, known := permissionRoles[permission]
	if !known {
		return false, nil
	}
	roles, err := st.RolesForUserInWorkspace(ctx, workspaceID, userID)
	if err != nil {
		return false, err
	}
	for _, role := range roles {
		if accepted[role] {
			return true, nil
		}
	}
	return false, nil
}

// CanSeeWorkspace reports whether userID may read the workspace's sync log.
func CanSeeWorkspace(ctx context.Context, st *store.Store, workspaceID, userID string) (bool, error) {
	return Allowed(ctx, st, workspaceID, userID, ViewSite)
}
