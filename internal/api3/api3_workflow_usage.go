package api3

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

func workflowUsagePage(r *http.Request, ids []string) (map[string]any, error) {
	maxResults := 50
	var err error
	if value := r.URL.Query().Get("maxResults"); value != "" {
		maxResults, err = strconv.Atoi(value)
		if err != nil || maxResults < 1 || maxResults > 200 {
			return nil, store.ErrAdminValidation
		}
	}
	start := 0
	if token := r.URL.Query().Get("nextPageToken"); token != "" {
		raw, decodeErr := base64.RawURLEncoding.DecodeString(token)
		if decodeErr != nil {
			return nil, store.ErrAdminValidation
		}
		start, err = strconv.Atoi(string(raw))
		if err != nil || start < 0 || start > len(ids) {
			return nil, store.ErrAdminValidation
		}
	}
	end := start + maxResults
	if end > len(ids) {
		end = len(ids)
	}
	values := make([]map[string]any, 0, end-start)
	for _, id := range ids[start:end] {
		values = append(values, map[string]any{"id": id})
	}
	page := map[string]any{"values": values}
	if end < len(ids) {
		page["nextPageToken"] = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	return page, nil
}

func (h *Handler) workflowUsageRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, userID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/workflow/"), "/")
	if r.Method == http.MethodDelete && len(parts) == 1 && parts[0] != "" {
		err := h.Store.DeleteInactiveWorkflow(r.Context(), workspaceID, userID, parts[0])
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, store.ErrAdminNotFound):
			jiraError(w, http.StatusNotFound, "The workflow was not found.")
		case errors.Is(err, store.ErrAdminValidation):
			jiraError(w, http.StatusBadRequest, err.Error())
		default:
			jiraError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if len(parts) < 2 || parts[0] == "" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	workflowID := parts[0]
	var ids []string
	var err error
	response := make(map[string]any)
	switch {
	case len(parts) == 2 && parts[1] == "projectUsages":
		ids, err = h.Store.WorkflowProjectUsages(r.Context(), workspaceID, workflowID)
		response["workflowId"] = workflowID
	case len(parts) == 2 && parts[1] == "workflowSchemes":
		ids, err = h.Store.WorkflowSchemeUsages(r.Context(), workspaceID, workflowID)
		response["workflowId"] = workflowID
	case len(parts) == 4 && parts[1] == "project" && parts[3] == "issueTypeUsages":
		ids, err = h.Store.WorkflowProjectIssueTypeUsages(r.Context(), workspaceID, workflowID, parts[2])
		response["workflowId"], response["projectId"] = workflowID, parts[2]
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	if err != nil {
		workflowSchemeAPIError(w, err)
		return
	}
	page, err := workflowUsagePage(r, ids)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "maxResults or nextPageToken is invalid.")
		return
	}
	if parts[1] == "workflowSchemes" {
		response["workflowSchemes"] = page
	} else if len(parts) == 4 {
		response["issueTypes"] = page
	} else {
		response["projects"] = page
	}
	writeJSON(w, http.StatusOK, response)
}
