package confluence

import (
	"net/http"
)

func spacePermission(id, principalType, principalID, operation, targetType string) any {
	return map[string]any{
		"id":        id,
		"principal": map[string]string{"type": principalType, "id": principalID},
		"operation": map[string]string{"key": operation, "targetType": targetType},
	}
}

// spacePermissionValues reports a space's permissions as they are held: the
// permissions of each role assigned in the space, for the principal it is
// assigned to, and each direct grant.
func (h *Handler) spacePermissionValues(r *http.Request, ws, actor, spaceID string) ([]any, error) {
	assignments, err := h.Store.WikiSpacePermissionAssignments(r.Context(), ws, actor, spaceID)
	if err != nil {
		return nil, err
	}
	values := make([]any, 0, len(assignments))
	for _, assignment := range assignments {
		values = append(values, spacePermission(assignment.ID, assignment.PrincipalType, assignment.PrincipalID, assignment.Operation, assignment.Target))
	}
	return values, nil
}

func (h *Handler) spacePermissions(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "cursor", "limit") {
		return
	}
	values, err := h.spacePermissionValues(r, ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	h.list(w, r, values)
}
