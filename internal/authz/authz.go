// Package authz: server-side permission decisions. V0 scope: workspace
// membership grants the workspace log; Issue security levels arrive in V5.
package authz

import (
	"context"

	"github.com/e6qu/zzira/internal/store"
)

// CanSeeWorkspace reports whether userID may read the workspace's sync log.
func CanSeeWorkspace(ctx context.Context, st *store.Store, workspaceID, userID string) (bool, error) {
	if userID == "" {
		return false, nil
	}
	return st.IsMember(ctx, workspaceID, userID)
}
