package confluence

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

func roleAssignmentBean(assignment models.WikiSpaceRoleAssignment) map[string]any {
	return map[string]any{"roleId": assignment.RoleID, "principal": assignment.Principal()}
}

func (h *Handler) spaceRoles(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "space-id", "role-type", "principal-id", "principal-type", "cursor", "limit") {
		return
	}
	q := r.URL.Query()
	if (q.Get("principal-id") == "") != (q.Get("principal-type") == "") {
		failure(w, 400, "principal-id and principal-type must be provided together.")
		return
	}
	if q.Get("principal-id") != "" && q.Get("space-id") == "" {
		failure(w, 400, "Principal filtering currently requires space-id.")
		return
	}
	if roleType := q.Get("role-type"); roleType != "" && roleType != "SYSTEM" && roleType != "CUSTOM" {
		failure(w, 400, "role-type must be SYSTEM or CUSTOM.")
		return
	}
	roles, err := h.Store.WikiSpaceRoles(r.Context(), ws, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	available := map[string]bool{}
	if spaceID := q.Get("space-id"); spaceID != "" {
		if !validPageID(w, spaceID) {
			return
		}
		assignments, assignmentErr := h.Store.WikiSpaceRoleAssignments(r.Context(), ws, actor, spaceID)
		if assignmentErr != nil {
			writeError(w, assignmentErr)
			return
		}
		for _, assignment := range assignments {
			if q.Get("principal-id") != "" && (assignment.PrincipalID != q.Get("principal-id") || assignment.PrincipalType != q.Get("principal-type")) {
				continue
			}
			available[assignment.RoleID] = true
		}
	}
	values := []any{}
	for _, role := range roles {
		if q.Get("role-type") != "" && role.Type != q.Get("role-type") {
			continue
		}
		if q.Get("space-id") != "" && !available[role.ID] {
			continue
		}
		values = append(values, role)
	}
	h.list(w, r, values)
}

type spaceRoleWrite struct {
	Name, Description string
	SpacePermissions  []string `json:"spacePermissions"`
	AnonymousRoleID   string   `json:"anonymousReassignmentRoleId"`
	GuestRoleID       string   `json:"guestReassignmentRoleId"`
}

func (h *Handler) createSpaceRole(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	var input spaceRoleWrite
	if !decode(w, r, &input) {
		return
	}
	role, err := h.Commands.CreateWikiSpaceRole(r.Context(), ws, actor, input.Name, input.Description, input.SpacePermissions)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 201, role)
}

func (h *Handler) spaceRole(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if strings.TrimSpace(id) == "" || !supportedQuery(w, r) {
		return
	}
	role, err := h.Store.WikiSpaceRole(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, role)
}

func (h *Handler) updateSpaceRole(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if strings.TrimSpace(id) == "" || !supportedQuery(w, r) {
		return
	}
	var input spaceRoleWrite
	if !decode(w, r, &input) {
		return
	}
	if input.AnonymousRoleID != "" || input.GuestRoleID != "" {
		failure(w, 400, "Guest and anonymous assignment migration is not available.")
		return
	}
	role, err := h.Commands.UpdateWikiSpaceRole(r.Context(), ws, actor, id, input.Name, input.Description, input.SpacePermissions)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, role)
}

func (h *Handler) deleteSpaceRole(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if strings.TrimSpace(id) == "" || !supportedQuery(w, r) {
		return
	}
	if err := h.Commands.DeleteWikiSpaceRole(r.Context(), ws, actor, id); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) spaceRoleAssignments(w http.ResponseWriter, r *http.Request, ws, actor, spaceID string) {
	if !validPageID(w, spaceID) || !supportedQuery(w, r, "role-id", "role-type", "principal-id", "principal-type", "cursor", "limit") {
		return
	}
	q := r.URL.Query()
	if (q.Get("principal-id") == "") != (q.Get("principal-type") == "") {
		failure(w, 400, "principal-id and principal-type must be provided together.")
		return
	}
	if roleType := q.Get("role-type"); roleType != "" && roleType != "SYSTEM" && roleType != "CUSTOM" {
		failure(w, 400, "role-type must be SYSTEM or CUSTOM.")
		return
	}
	assignments, err := h.Store.WikiSpaceRoleAssignments(r.Context(), ws, actor, spaceID)
	if err != nil {
		writeError(w, err)
		return
	}
	values := []any{}
	for _, assignment := range assignments {
		if q.Get("role-id") != "" && assignment.RoleID != q.Get("role-id") || q.Get("principal-id") != "" && (assignment.PrincipalID != q.Get("principal-id") || assignment.PrincipalType != q.Get("principal-type")) {
			continue
		}
		if roleType := q.Get("role-type"); roleType != "" {
			role, roleErr := h.Store.WikiSpaceRole(r.Context(), ws, actor, assignment.RoleID)
			if roleErr != nil {
				writeError(w, roleErr)
				return
			}
			if role.Type != roleType {
				continue
			}
		}
		values = append(values, roleAssignmentBean(assignment))
	}
	h.list(w, r, values)
}

func (h *Handler) setSpaceRoleAssignments(w http.ResponseWriter, r *http.Request, ws, actor, spaceID string) {
	if !validPageID(w, spaceID) || !supportedQuery(w, r) {
		return
	}
	var input []struct {
		RoleID    string `json:"roleId"`
		Principal struct {
			Type string `json:"principalType"`
			ID   string `json:"principalId"`
		} `json:"principal"`
	}
	if !decode(w, r, &input) {
		return
	}
	assignments := make([]models.WikiSpaceRoleAssignment, len(input))
	for i := range input {
		assignments[i] = models.WikiSpaceRoleAssignment{SpaceID: spaceID, RoleID: input[i].RoleID, PrincipalType: input[i].Principal.Type, PrincipalID: input[i].Principal.ID}
	}
	if err := h.Commands.SetWikiSpaceRoleAssignments(r.Context(), ws, actor, spaceID, assignments); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}
