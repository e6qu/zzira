package store

import (
	"context"
	"strings"
)

// WikiSpaceHeldPermissions reports which of the space permissions this product
// enforces the caller holds in a space they can see, through roles assigned to
// them, their groups or an access class, through direct grants, or through a
// space that says nothing about who may do what.
func (s *Store) WikiSpaceHeldPermissions(ctx context.Context, ws, actor, spaceID string) (map[string]bool, error) {
	catalogue := wikiSpacePermissionCatalogue()
	columns := make([]string, len(catalogue))
	for i, permission := range catalogue {
		columns[i] = wikiSpacePermissionAllowed(permission)
	}
	values := make([]bool, len(catalogue))
	targets := make([]any, len(catalogue))
	for i := range values {
		targets[i] = &values[i]
	}
	err := s.Pool.QueryRow(ctx, `SELECT `+strings.Join(columns, ",")+` FROM wiki_spaces s
		WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND s.id::text=$3`, ws, actor, spaceID).Scan(targets...)
	if err != nil {
		return nil, err
	}
	held := make(map[string]bool, len(catalogue))
	for i, permission := range catalogue {
		held[permission] = values[i]
	}
	return held, nil
}
