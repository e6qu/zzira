package agile

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// boardProjectBean is the shape Jira returns from a board's project endpoints.
func (h *Handler) boardProjectBean(board *models.Board, full bool) map[string]any {
	bean := map[string]any{
		"id": board.ProjectID, "key": board.ProjectKey, "name": board.ProjectName,
		"self": h.BaseURL + "/rest/api/3/project/" + board.ProjectKey,
		"avatarUrls": map[string]any{
			"48x48": h.BaseURL + "/static/img/project-avatar.svg",
		},
	}
	if full {
		bean["projectTypeKey"] = "software"
		bean["simplified"] = false
		bean["style"] = "classic"
	}
	return bean
}

func (h *Handler) boardProjects(w http.ResponseWriter, r *http.Request, board *models.Board, full bool) {
	values := []map[string]any{h.boardProjectBean(board, full)}
	writeJSON(w, http.StatusOK, map[string]any{
		"maxResults": 50, "startAt": 0, "total": len(values), "isLast": true, "values": values,
	})
}

// boardVersions lists the versions of the board's project, which is what a
// board's version endpoint reports.
func (h *Handler) boardVersions(w http.ResponseWriter, r *http.Request, board *models.Board) {
	versions, err := h.Store.ProjectVersions(r.Context(), board.ProjectID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	released := r.URL.Query().Get("released")
	values := []map[string]any{}
	for _, version := range versions {
		if released == "true" && !version.Released {
			continue
		}
		if released == "false" && version.Released {
			continue
		}
		values = append(values, map[string]any{
			"id": version.ID, "name": version.Name, "archived": version.Archived,
			"released": version.Released, "projectId": board.ProjectID,
			"self": h.BaseURL + "/rest/api/3/version/" + version.ID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"maxResults": 50, "startAt": 0, "total": len(values), "isLast": true, "values": values,
	})
}

// boardEpics reports the board's epics. ZZIRA's work type hierarchy is a single
// parent level with sub-tasks and has no epic type, so the list is empty and
// "issues without an epic" is every issue on the board.
func (h *Handler) boardEpics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"maxResults": 50, "startAt": 0, "total": 0, "isLast": true, "values": []any{},
	})
}

func (h *Handler) boardSprintIssues(w http.ResponseWriter, r *http.Request, board *models.Board, sprintID, userID string) {
	sprint, err := h.Store.SprintByID(r.Context(), sprintID)
	if err != nil || sprint.BoardID != board.ID {
		jiraError(w, http.StatusNotFound, "The sprint does not exist on this board.")
		return
	}
	issues, err := h.Store.IssuesBySprint(r.Context(), sprint.ID, userID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeIssuePage(w, r, issues)
}

// boardFeatures reports the board features Jira toggles. ZZIRA models the
// board's own configuration instead, so each feature reports its real state and
// is not togglable through this endpoint.
func (h *Handler) boardFeatures(w http.ResponseWriter, r *http.Request, board *models.Board) {
	features := []map[string]any{
		{"boardFeature": "SPRINTS", "boardId": board.ID, "state": featureState(board.Type == "scrum"), "toggleLocked": true},
		{"boardFeature": "BACKLOG", "boardId": board.ID, "state": featureState(true), "toggleLocked": true},
		{"boardFeature": "SWIMLANES", "boardId": board.ID, "state": featureState(board.SwimlaneStrategy != "none"), "toggleLocked": true},
	}
	writeJSON(w, http.StatusOK, map[string]any{"features": features})
}

func featureState(enabled bool) string {
	if enabled {
		return "ENABLED"
	}
	return "DISABLED"
}

// boardReports reports the board's available reports.
func (h *Handler) boardReports(w http.ResponseWriter, r *http.Request, board *models.Board) {
	reports := []map[string]any{}
	if board.Type == "scrum" {
		reports = append(reports,
			map[string]any{"id": "sprint", "name": "Sprint report"},
			map[string]any{"id": "burndown", "name": "Burndown chart"})
	}
	reports = append(reports, map[string]any{"id": "cumulative-flow", "name": "Cumulative flow diagram"})
	writeJSON(w, http.StatusOK, map[string]any{"reports": reports})
}

func (h *Handler) boardProperties(w http.ResponseWriter, r *http.Request, board *models.Board) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	keys, err := h.Store.BoardPropertyKeys(r.Context(), board.ID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	values := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		values = append(values, map[string]any{
			"key":  key,
			"self": h.BaseURL + "/rest/agile/1.0/board/" + board.ID + "/properties/" + key,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": values})
}

func (h *Handler) boardProperty(w http.ResponseWriter, r *http.Request, board *models.Board, key string) {
	switch r.Method {
	case http.MethodGet:
		value, err := h.Store.BoardProperty(r.Context(), board.ID, key)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The board property does not exist.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"key": key, "value": json.RawMessage(value),
			"self": h.BaseURL + "/rest/agile/1.0/board/" + board.ID + "/properties/" + key,
		})
	case http.MethodPut:
		body := http.MaxBytesReader(w, r.Body, 64<<10)
		raw, err := io.ReadAll(body)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The property value is invalid.")
			return
		}
		created, err := h.Store.SetBoardProperty(r.Context(), board.ID, key, raw)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The property key or value is invalid.")
			return
		}
		if created {
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		if err := h.Store.DeleteBoardProperty(r.Context(), board.ID, key); err != nil {
			if err == pgx.ErrNoRows {
				jiraError(w, http.StatusNotFound, "The board property does not exist.")
				return
			}
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// softwareRoute serves Jira's /rest/software/1.0 board reads. They return the
// same data as their /rest/agile/1.0 counterparts, plus the approximate-count
// variants a board UI uses before it pages.
func (h *Handler) softwareRoute(w http.ResponseWriter, r *http.Request, path string) {
	if !strings.HasPrefix(path, "/board/") {
		jiraError(w, http.StatusNotFound, "No resource found for path "+r.URL.Path)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/board/"), "/"), "/")
	if len(parts) < 2 || r.Method != http.MethodGet {
		jiraError(w, http.StatusNotFound, "No resource found for path "+r.URL.Path)
		return
	}
	workspaceID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), workspaceID, parts[0])
	if err != nil {
		jiraError(w, http.StatusNotFound, "The board does not exist.")
		return
	}
	count := func(issues []*models.Issue, err error) {
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"issuesCount": len(issues), "issueCount": len(issues)})
	}
	switch {
	case len(parts) == 2 && parts[1] == "backlog":
		h.boardBacklog(w, r, board, userID)
	case len(parts) == 3 && parts[1] == "backlog" && parts[2] == "approximate-count":
		count(h.Store.BacklogIssues(r.Context(), board.ID, userID))
	case len(parts) == 2 && parts[1] == "issue":
		h.boardIssues(w, r, board, userID)
	case len(parts) == 3 && parts[1] == "issue" && parts[2] == "approximate-count":
		count(h.boardIssueList(r, board, userID))
	case len(parts) == 4 && parts[1] == "epic" && parts[2] == "none" && parts[3] == "issue":
		h.boardIssues(w, r, board, userID)
	case len(parts) == 4 && parts[1] == "epic" && parts[3] == "issue":
		jiraError(w, http.StatusNotFound, "The epic does not exist.")
	case len(parts) == 4 && parts[1] == "sprint" && parts[3] == "issue":
		h.boardSprintIssues(w, r, board, parts[2], userID)
	default:
		jiraError(w, http.StatusNotFound, "No resource found for path "+r.URL.Path)
	}
}

func (h *Handler) boardIssueList(r *http.Request, board *models.Board, userID string) ([]*models.Issue, error) {
	columns, err := h.Store.BoardIssues(r.Context(), board.ID, userID)
	if err != nil {
		return nil, err
	}
	issues := []*models.Issue{}
	for _, statusID := range board.ColumnStatusIDs {
		issues = append(issues, columns[statusID]...)
	}
	return issues, nil
}
