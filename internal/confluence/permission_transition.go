package confluence

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

// transitionTaskBean renders a transition task the way Confluence's status
// endpoint does: three states, and a message only when something went wrong.
func (h *Handler) transitionTaskBean(task store.APITask) map[string]any {
	status := "IN_PROGRESS"
	switch task.Status {
	case "COMPLETE":
		status = "COMPLETED"
	case "FAILED", "CANCELLED":
		status = "FAILED"
	}
	bean := map[string]any{"taskId": task.ID, "status": status}
	if status == "FAILED" {
		bean["errorMessage"] = task.Message
	}
	return bean
}

func (h *Handler) transitionAccepted(w http.ResponseWriter, task store.APITask) {
	self := h.BaseURL + "/wiki/api/v2/space-permissions/transition/tasks/" + task.ID
	w.Header().Set("Location", self)
	respond(w, 202, map[string]any{"taskId": task.ID, "status": "IN_PROGRESS", "statusUrl": self})
}

func (h *Handler) permissionCombinations(w http.ResponseWriter, r *http.Request, ws, actor string) {
	switch r.Method {
	case http.MethodGet:
		if !supportedQuery(w, r, "cursor", "limit") {
			return
		}
		combinations, generatedAt, err := h.Store.WikiPermissionCombinations(r.Context(), ws, actor)
		if err != nil {
			writeError(w, err)
			return
		}
		results := make([]any, 0, len(combinations))
		for _, combination := range combinations {
			results = append(results, map[string]any{
				"combinationId": combination.ID, "spaceCount": combination.SpaceCount,
				"principalCount": combination.PrincipalCount,
				"permissions":    combination.Permissions, "principalTypes": combination.PrincipalTypes,
			})
		}
		respond(w, 200, map[string]any{"results": results, "generatedAt": generatedAt})
	case http.MethodPost:
		if !supportedQuery(w, r) {
			return
		}
		task, err := h.Store.EnqueueWikiPermissionCombinations(r.Context(), ws, actor)
		if err != nil {
			writeError(w, err)
			return
		}
		h.transitionAccepted(w, task)
	default:
		failure(w, 405, "Method not allowed.")
	}
}

type spaceSelectionInput struct {
	SpaceType      string `json:"spaceType"`
	SelectedSpaces []struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	} `json:"selectedSpaces"`
}

// selection accepts a space named by id or by key, as Confluence does.
func (input spaceSelectionInput) selection() store.WikiSpaceSelection {
	selected := make([]string, 0, len(input.SelectedSpaces))
	for _, space := range input.SelectedSpaces {
		if identifier := strings.TrimSpace(space.ID); identifier != "" {
			selected = append(selected, identifier)
			continue
		}
		if identifier := strings.TrimSpace(space.Key); identifier != "" {
			selected = append(selected, identifier)
		}
	}
	return store.WikiSpaceSelection{SpaceType: strings.ToUpper(strings.TrimSpace(input.SpaceType)), SelectedSpaces: selected}
}

func (h *Handler) permissionRoleAssignments(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	var input struct {
		Assignments []struct {
			PermissionCombinationID  string `json:"permissionCombinationId"`
			PrincipalTypeAssignments []struct {
				PrincipalType string `json:"principalType"`
				RemoveAccess  bool   `json:"removeAccess"`
				RoleID        string `json:"roleId"`
			} `json:"principalTypeAssignments"`
		} `json:"assignments"`
		SpaceSelection spaceSelectionInput `json:"spaceSelection"`
	}
	if !decode(w, r, &input) {
		return
	}
	assignments := make([]store.WikiRoleAssignmentRequest, 0, len(input.Assignments))
	for _, assignment := range input.Assignments {
		converted := store.WikiRoleAssignmentRequest{CombinationID: assignment.PermissionCombinationID}
		for _, principal := range assignment.PrincipalTypeAssignments {
			converted.PrincipalTypeAssignments = append(converted.PrincipalTypeAssignments,
				store.WikiPrincipalTypeAssignment{
					PrincipalType: principal.PrincipalType,
					RemoveAccess:  principal.RemoveAccess,
					RoleID:        principal.RoleID,
				})
		}
		assignments = append(assignments, converted)
	}
	task, err := h.Store.EnqueueWikiPermissionRoleAssignments(r.Context(), ws, actor, assignments, input.SpaceSelection.selection())
	if err != nil {
		writeError(w, err)
		return
	}
	h.transitionAccepted(w, task)
}

func (h *Handler) permissionAccessRemovals(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	var input struct {
		PermissionCombinationIDs []string            `json:"permissionCombinationIds"`
		SpaceSelection           spaceSelectionInput `json:"spaceSelection"`
	}
	if !decode(w, r, &input) {
		return
	}
	task, err := h.Store.EnqueueWikiPermissionAccessRemovals(r.Context(), ws, actor,
		input.PermissionCombinationIDs, input.SpaceSelection.selection())
	if err != nil {
		writeError(w, err)
		return
	}
	h.transitionAccepted(w, task)
}

func (h *Handler) permissionTransitionTask(w http.ResponseWriter, r *http.Request, ws, actor, taskID string) {
	if !supportedQuery(w, r) {
		return
	}
	task, err := h.Store.WikiPermissionTransitionTask(r.Context(), ws, actor, taskID)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.transitionTaskBean(task))
}
