package authz

import (
	"context"

	"github.com/e6qu/zzira/internal/store"
)

// JiraPermissions evaluates the named project permissions for a person
// against the project's permission scheme, for the work item when one is
// named. Workflow validators read the result at transition time.
func JiraPermissions(ctx context.Context, st *store.Store, workspaceID, userID, projectID, issueID string, keys []string) (map[string]bool, error) {
	permissions := make(map[string]bool, len(keys))
	for _, key := range keys {
		if _, seen := permissions[key]; seen {
			continue
		}
		allowed, err := st.HasProjectPermission(ctx, workspaceID, userID, projectID, issueID, key)
		if err != nil {
			return nil, err
		}
		permissions[key] = allowed
	}
	return permissions, nil
}
