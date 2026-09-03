package api3

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// securitySchemeBean renders the stored scheme (levels included).
func securitySchemeBean(scheme *models.SecurityScheme) map[string]any {
	levels := make([]map[string]any, 0, len(scheme.Levels))
	for _, lvl := range scheme.Levels {
		levels = append(levels, map[string]any{
			"id":      lvl.ID,
			"name":    lvl.Name,
			"members": lvl.Members,
		})
	}
	return map[string]any{
		"id":     scheme.ID,
		"name":   scheme.Name,
		"self":   "/rest/api/3/issuesecurityschemes/" + scheme.ID,
		"levels": levels,
	}
}

// securitySchemeRoute serves the V5 admin surface:
//
//	GET  /rest/api/3/issuesecurityschemes                → list
//	POST /rest/api/3/issuesecurityschemes                → create {id,name,levels:[{id,name,members}]}
//	GET  /rest/api/3/issuesecurityschemes/{id}           → one scheme
//	PUT  /rest/api/3/issuesecurityschemes/project/{key}  → assign scheme to project {id}
func (h *Handler) securitySchemeRoute(w http.ResponseWriter, r *http.Request) {
	wsID, _, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, "/rest/api/3")
	switch {
	case rel == "/issuesecurityschemes" && r.Method == http.MethodGet:
		schemes, err := h.Store.SecuritySchemes(r.Context())
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		values := make([]map[string]any, 0, len(schemes))
		for i := range schemes {
			values = append(values, securitySchemeBean(&schemes[i]))
		}
		writeJSON(w, http.StatusOK, map[string]any{"issuesecurityschemes": values})
	case rel == "/issuesecurityschemes" && r.Method == http.MethodPost:
		var req struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Levels []struct {
				ID      string   `json:"id"`
				Name    string   `json:"name"`
				Members []string `json:"members"`
			} `json:"levels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" || req.Name == "" || len(req.Levels) == 0 {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{
				"issuesecurityscheme": "id, name and at least one level are required.",
			})
			return
		}
		scheme := models.SecurityScheme{ID: req.ID, Name: req.Name}
		for _, l := range req.Levels {
			scheme.Levels = append(scheme.Levels, models.SecurityLevel{ID: l.ID, Name: l.Name, Members: l.Members})
		}
		if err := h.Store.CreateSecurityScheme(r.Context(), scheme); err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, securitySchemeBean(&scheme))
	case strings.HasPrefix(rel, "/issuesecurityschemes/project/"):
		keyOrID := strings.TrimPrefix(rel, "/issuesecurityschemes/project/")
		project, err := h.Store.ProjectByIDOrKey(r.Context(), wsID, keyOrID)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The project does not exist.")
			return
		}
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{
				"projectKeyOrId":        keyOrID,
				"issueSecuritySchemeId": project.SecuritySchemeID,
			})
			return
		}
		if r.Method == http.MethodPut {
			var req struct {
				ID string `json:"id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
				jiraFieldError(w, http.StatusBadRequest, map[string]string{"id": "issueSecurityScheme id is required."})
				return
			}
			if err := h.Store.AssignSecurityScheme(r.Context(), project.ID, req.ID); err != nil {
				jiraError(w, http.StatusBadRequest, err.Error())
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}
