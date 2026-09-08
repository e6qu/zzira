package confluence

import (
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
)

func spacePermission(id, principalType, principalID, operation, targetType string) any {
	return map[string]any{
		"id":        id,
		"principal": map[string]string{"type": principalType, "id": principalID},
		"operation": map[string]string{"key": operation, "targetType": targetType},
	}
}

func spacePermissionValues(space *models.WikiSpace) []any {
	principalType, principalID := "role", "workspace-member"
	if space.Private {
		principalType, principalID = "user", space.AuthorID
	}
	values := []any{}
	sequence := 0
	add := func(operation, target string) {
		sequence++
		values = append(values, spacePermission(space.ID+"-"+strconv.Itoa(sequence), principalType, principalID, operation, target))
	}
	add("read", "space")
	for _, target := range []string{"page", "blogpost", "comment", "attachment", "folder", "embed", "database", "whiteboard"} {
		add("create", target)
		add("read", target)
		add("update", target)
		add("delete", target)
	}
	values = append(values, spacePermission(space.ID+"-admin", "role", "workspace-admin", "administer", "space"))
	return values
}

func (h *Handler) spacePermissions(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "cursor", "limit") {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	h.list(w, r, spacePermissionValues(space))
}
