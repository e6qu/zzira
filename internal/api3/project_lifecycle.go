package api3

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

func isProjectLifecyclePath(path string, method string) bool {
	if path == "/project/recent" {
		return true
	}
	if !strings.HasPrefix(path, "/project/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/project/"), "/")
	if len(parts) == 1 {
		return method == http.MethodDelete
	}
	return len(parts) == 2 && (parts[1] == "archive" || parts[1] == "restore" || parts[1] == "delete")
}

func projectLifecycleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrProjectPermission):
		jiraError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, store.ErrProjectArchived):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrProjectLifecycleConflict):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		jiraError(w, http.StatusNotFound, "Project does not exist or you do not have permission to manage it.")
	default:
		log.Print("project lifecycle: ", err)
		jiraError(w, http.StatusInternalServerError, "Could not update project lifecycle.")
	}
}

func (h *Handler) projectLifecycleRoute(w http.ResponseWriter, r *http.Request, path string) {
	if path == "/project/recent" {
		h.recentProjects(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/project/"), "/")
	if len(parts) == 1 && r.Method == http.MethodDelete {
		h.deleteProject(w, r, parts[0])
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	switch parts[1] {
	case "archive":
		h.archiveProject(w, r, parts[0])
	case "restore":
		h.restoreProject(w, r, parts[0])
	case "delete":
		h.deleteProjectAsync(w, r, parts[0])
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) recentProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	for key := range r.URL.Query() {
		if key != "expand" && key != "properties" {
			jiraError(w, http.StatusBadRequest, "Unsupported recent-project parameter: "+key)
			return
		}
	}
	expands := commaQuerySet(r, "expand")
	for expand := range expands {
		switch expand {
		case "description", "projectkeys", "lead", "issuetypes", "url", "permissions", "insight", "*":
		default:
			jiraError(w, http.StatusBadRequest, "Unsupported project expansion: "+expand)
			return
		}
	}
	projects, err := h.Store.RecentProjects(r.Context(), workspaceID, userID)
	if err != nil {
		projectLifecycleError(w, err)
		return
	}
	categories, err := h.projectCategoryMap(r.Context(), workspaceID)
	if err != nil {
		projectLifecycleError(w, err)
		return
	}
	propertyKeys := commaQuerySet(r, "properties")
	all := querySetContains(expands, "*")
	isAdmin, err := h.Store.IsAdmin(r.Context(), workspaceID, userID)
	if err != nil {
		projectLifecycleError(w, err)
		return
	}
	var issueTypes any
	if all || querySetContains(expands, "issueTypes") {
		issueTypes, err = h.Store.IssueTypes(r.Context())
		if err != nil {
			projectLifecycleError(w, err)
			return
		}
	}
	values := make([]map[string]any, 0, len(projects))
	for _, project := range projects {
		bean := h.projectBean(project)
		if category := categories[project.CategoryID]; category != nil {
			bean["projectCategory"] = h.categoryBean(category)
		}
		if all || querySetContains(expands, "projectKeys") {
			bean["projectKeys"] = []string{project.Key}
		}
		if (all || querySetContains(expands, "lead")) && project.LeadAccountID != "" {
			lead, leadErr := h.Store.UserByID(r.Context(), project.LeadAccountID)
			if leadErr != nil {
				projectLifecycleError(w, leadErr)
				return
			}
			bean["lead"] = h.userBean(lead)
		}
		if all || querySetContains(expands, "issueTypes") {
			bean["issueTypes"] = issueTypes
		}
		if all || querySetContains(expands, "permissions") {
			bean["permissions"] = map[string]any{
				"BROWSE_PROJECTS":     map[string]any{"id": "10", "key": "BROWSE_PROJECTS", "name": "Browse Projects", "havePermission": true},
				"ADMINISTER_PROJECTS": map[string]any{"id": "23", "key": "ADMINISTER_PROJECTS", "name": "Administer Projects", "havePermission": isAdmin},
			}
		}
		if all || querySetContains(expands, "insight") {
			count, updated, insightErr := h.Store.ProjectInsight(r.Context(), workspaceID, project.ID)
			if insightErr != nil {
				projectLifecycleError(w, insightErr)
				return
			}
			bean["insight"] = map[string]any{"totalIssueCount": count, "lastIssueUpdateTime": updated}
		}
		if len(propertyKeys) > 0 {
			properties, propertyErr := h.Store.ProjectProperties(r.Context(), workspaceID, project.ID)
			if propertyErr != nil {
				projectLifecycleError(w, propertyErr)
				return
			}
			selected := map[string]any{}
			for _, property := range properties {
				if querySetContains(propertyKeys, property.Key) {
					selected[property.Key] = property.Value
				}
			}
			bean["properties"] = selected
		}
		values = append(values, bean)
	}
	writeJSON(w, http.StatusOK, values)
}

func (h *Handler) archiveProject(w http.ResponseWriter, r *http.Request, idOrKey string) {
	workspaceID, userID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if _, err := h.Store.ArchiveProject(r.Context(), workspaceID, userID, idOrKey); err != nil {
		projectLifecycleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) restoreProject(w http.ResponseWriter, r *http.Request, idOrKey string) {
	workspaceID, userID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	project, err := h.Store.RestoreProject(r.Context(), workspaceID, userID, idOrKey)
	if err != nil {
		projectLifecycleError(w, err)
		return
	}
	h.writeProject(w, r, project)
}

func parseEnableUndo(r *http.Request) (bool, error) {
	raw := r.URL.Query().Get("enableUndo")
	if raw == "" {
		return true, nil
	}
	return strconv.ParseBool(raw)
}

func (h *Handler) deleteProject(w http.ResponseWriter, r *http.Request, idOrKey string) {
	workspaceID, userID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	enableUndo, err := parseEnableUndo(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "enableUndo must be true or false.")
		return
	}
	if enableUndo {
		_, err = h.Store.TrashProject(r.Context(), workspaceID, userID, idOrKey)
	} else {
		err = h.Store.PermanentDeleteProject(r.Context(), workspaceID, userID, idOrKey, nil)
	}
	if err != nil {
		projectLifecycleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteProjectAsync(w http.ResponseWriter, r *http.Request, idOrKey string) {
	workspaceID, userID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	task, err := h.Store.EnqueueProjectDeleteTask(r.Context(), workspaceID, userID, idOrKey)
	if err != nil {
		projectLifecycleError(w, err)
		return
	}
	location := h.BaseURL + "/rest/api/3/task/" + url.PathEscape(task.ID)
	w.Header().Set("Location", location)
	writeJSON(w, http.StatusSeeOther, h.apiTaskBean(task))
}
