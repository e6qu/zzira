package api3

import (
	"encoding/json"
	"net/http"

	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
)

// ---- link types ----

func linkTypeBean(lt *models.LinkType) map[string]any {
	return map[string]any{
		"id":      lt.ID,
		"name":    lt.Name,
		"inward":  lt.Inward,
		"outward": lt.Outward,
		"self":    "/rest/api/3/issueLinkType/" + lt.ID,
	}
}

func (h *Handler) listLinkTypes(w http.ResponseWriter, r *http.Request) {
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	links, err := h.Store.LinkTypes(r.Context())
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	values := make([]map[string]any, 0, len(links))
	for _, lt := range links {
		values = append(values, linkTypeBean(lt))
	}
	writeJSON(w, http.StatusOK, map[string]any{"issueLinkTypes": values})
}

// ---- issue links ----

func (h *Handler) createIssueLink(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var req struct {
		Type struct {
			Name string `json:"name"`
		} `json:"type"`
		InwardIssue struct {
			Key string `json:"key"`
		} `json:"inwardIssue"`
		OutwardIssue struct {
			Key string `json:"key"`
		} `json:"outwardIssue"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Type.Name == "" ||
		req.InwardIssue.Key == "" || req.OutwardIssue.Key == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{
			"linkType": "type.name, inwardIssue.key and outwardIssue.key are required.",
		})
		return
	}
	typeID, err := h.Store.LinkTypeIDByName(r.Context(), req.Type.Name)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "The link type "+req.Type.Name+" does not exist.")
		return
	}
	inward, e := h.resolveIssue(r, wsID, req.InwardIssue.Key)
	if e != nil {
		writeJerr(w, e)
		return
	}
	outward, e := h.resolveIssue(r, wsID, req.OutwardIssue.Key)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if _, _, err := h.Store.CreateIssueLink(r.Context(), userID, wsID, typeID, inward.ID, outward.ID); err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteIssueLink(w http.ResponseWriter, r *http.Request, id string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	link, err := h.Store.IssueLinkByID(r.Context(), wsID, id)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Link does not exist.")
		return
	}
	if _, e := h.resolveIssue(r, wsID, link.InwardID); e != nil {
		jiraError(w, http.StatusNotFound, "Link does not exist.")
		return
	}
	if _, e := h.resolveIssue(r, wsID, link.OutwardID); e != nil {
		jiraError(w, http.StatusNotFound, "Link does not exist.")
		return
	}
	if _, err := h.Store.DeleteIssueLink(r.Context(), userID, wsID, id); err != nil {
		jiraError(w, http.StatusNotFound, "Link does not exist.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- labels ----

func (h *Handler) labelsEndpoint(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	query := r.URL.Query().Get("query")
	total, labels, err := h.Store.Labels(r.Context(), wsID, userID, query)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"totalCount": total, "labels": labels})
}

// ---- metadata registries ----

func (h *Handler) issueTypesEndpoint(w http.ResponseWriter, r *http.Request) {
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	issueType, err := h.Store.FirstIssueType(r.Context())
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, []map[string]any{{
		"id": issueType.ID, "name": issueType.Name, "subtask": false,
		"iconUrl": h.BaseURL + "/static/img/issuetype-task.svg",
		"self":    h.BaseURL + "/rest/api/3/issuetype/" + issueType.ID,
	}})
}

func (h *Handler) prioritiesEndpoint(w http.ResponseWriter, r *http.Request) {
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	priorities, err := h.Store.Priorities(r.Context())
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(priorities))
	for _, p := range priorities {
		out = append(out, map[string]any{"id": p.ID, "name": p.Name})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) statusesEndpoint(w http.ResponseWriter, r *http.Request) {
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	statuses, err := h.Store.AllStatuses(r.Context())
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, h.statusBean(st))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) statusCategoryEndpoint(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []map[string]any{
		{"key": "new", "name": "To Do"},
		{"key": "indeterminate", "name": "In Progress"},
		{"key": "done", "name": "Done"},
	})
}

func (h *Handler) resolutionsEndpoint(w http.ResponseWriter, r *http.Request) {
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	writeJSON(w, http.StatusOK, []map[string]any{
		{"id": "res_done", "name": "Done", "description": "Work has been completed."},
	})
}

// ---- permissions introspection ----

func (h *Handler) myPermissions(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	admin, err := authz.IsWorkspaceAdmin(r.Context(), h.Store, wsID, userID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	perm := func(have bool) map[string]any {
		return map[string]any{"havePermission": have, "description": ""}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"permissions": map[string]any{
			"ADMINISTER":         perm(admin),
			"BROWSE_PROJECTS":    perm(true),
			"CREATE_ISSUES":      perm(true),
			"EDIT_ISSUES":        perm(true),
			"TRANSITION_ISSUES":  perm(true),
			"ADD_COMMENTS":       perm(true),
			"CREATE_ATTACHMENTS": perm(true),
			"DELETE_OWN_CONTENT": perm(true),
		},
	})
}

func (h *Handler) permissionsCheck(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	admin, err := authz.IsWorkspaceAdmin(r.Context(), h.Store, wsID, userID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	var req struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Permissions) == 0 {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"permissions": "At least one permission is required."})
		return
	}
	granted := map[string]any{}
	for _, name := range req.Permissions {
		permitted := permissionKnown(name)
		if name == "ADMINISTER" {
			permitted = admin
		}
		granted[name] = map[string]any{"permitted": permitted}
	}
	writeJSON(w, http.StatusOK, granted)
}

// permissionKnown: V5 grants every workspace member the known permissions;
// unknown keys are reported as not permitted rather than silently true.
func permissionKnown(name string) bool {
	switch name {
	case "BROWSE_PROJECTS", "CREATE_ISSUES", "EDIT_ISSUES", "TRANSITION_ISSUES",
		"ADD_COMMENTS", "CREATE_ATTACHMENTS", "DELETE_OWN_CONTENT":
		return true
	}
	return false
}
