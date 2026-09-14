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

	"github.com/e6qu/zzira/internal/models"
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
	// An anonymous caller has no account to remember projects against.
	projects := []*models.Project{}
	if userID != "" {
		recent, err := h.Store.RecentProjects(r.Context(), workspaceID, userID)
		if err != nil {
			projectLifecycleError(w, err)
			return
		}
		for _, project := range recent {
			allowed, err := h.canBrowseProject(r, workspaceID, userID, project.ID)
			if err != nil {
				projectLifecycleError(w, err)
				return
			}
			if allowed {
				projects = append(projects, project)
			}
		}
	}
	view, err := h.newProjectView(r, workspaceID, false)
	if err != nil {
		projectLifecycleError(w, err)
		return
	}
	view.insightMillis = true
	values := make([]map[string]any, 0, len(projects))
	for _, project := range projects {
		bean, beanErr := h.projectRepresentation(r, workspaceID, userID, project, view)
		if beanErr != nil {
			projectLifecycleError(w, beanErr)
			return
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
	h.writeProject(w, r, userID, project)
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
	location := h.BaseURL + "/rest/api/3/task/" + url.PathEscape(task.WireID())
	w.Header().Set("Location", location)
	writeJSON(w, http.StatusSeeOther, h.apiTaskBean(task))
}
