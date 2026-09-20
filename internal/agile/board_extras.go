package agile

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

// boardProjectBean is the shape Jira returns from a board's project endpoints.
func (h *Handler) boardProjectBean(board *models.Board, full bool) map[string]any {
	bean := map[string]any{
		"id": wireNumber(board.ProjectID), "key": board.ProjectKey, "name": board.ProjectName,
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
			"released": version.Released, "projectId": wireNumber(board.ProjectID),
			"self": h.BaseURL + "/rest/api/3/version/" + version.ID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"maxResults": 50, "startAt": 0, "total": len(values), "isLast": true, "values": values,
	})
}

func (h *Handler) boardSprintIssues(w http.ResponseWriter, r *http.Request, board *models.Board, sprintID, userID string) {
	sprint, err := h.Store.SprintByIDInWorkspace(r.Context(), board.WorkspaceID, sprintID)
	if err != nil || sprint.BoardID != board.ID {
		jiraError(w, http.StatusNotFound, "The sprint does not exist on this board.")
		return
	}
	h.agileIssueSearch(w, r, board.WorkspaceID, userID, boardSprintScope, []any{sprint.ID, board.ProjectID}, sprintOrder)
}

// boardFeatures reports the board features Jira toggles. Each one reads the
// configuration that actually decides it -- the board's type, its project's
// backlog feature, its swimlane grouping -- rather than a second copy of the
// same fact kept for this endpoint.
func (h *Handler) boardFeatures(w http.ResponseWriter, r *http.Request, board *models.Board) {
	features, err := h.boardFeatureList(r, board)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"features": features})
}

// boardFeatureList is the shape both the read and the toggle answer with.
func (h *Handler) boardFeatureList(r *http.Request, board *models.Board) ([]map[string]any, error) {
	backlog := true
	// A software project's backlog is a project feature, and the navigation
	// already hides the backlog when it is off; the board reports the same
	// state rather than claiming its own.
	if features, err := h.Store.ProjectFeatures(r.Context(), board.WorkspaceID, board.ProjectID); err == nil {
		for _, feature := range features {
			if feature.Key == "jsw.classic.backlog" {
				backlog = feature.State == "ENABLED"
			}
		}
	} else if !errors.Is(err, store.ErrProjectFeatureWrongType) {
		return nil, err
	}
	return []map[string]any{
		// A board's sprints follow its type, which this endpoint does not
		// change, so it is locked here as it is on a company-managed board.
		{"boardFeature": "SPRINTS", "boardId": board.JiraID, "state": featureState(board.Type == "scrum"), "toggleLocked": true},
		{"boardFeature": "BACKLOG", "boardId": board.JiraID, "state": featureState(backlog), "toggleLocked": false},
		{"boardFeature": "SWIMLANES", "boardId": board.JiraID, "state": featureState(board.SwimlaneStrategy != "none"), "toggleLocked": false},
	}, nil
}

// boardFeatureToggle turns a board feature on or off, through the setting
// that decides it: the project's backlog feature, or the board's swimlane
// grouping. Turning swimlanes on restores Jira's default grouping, by
// assignee, because a board with swimlanes on and no grouping is neither.
func (h *Handler) boardFeatureToggle(w http.ResponseWriter, r *http.Request, board *models.Board, userID string) {
	var input struct {
		BoardID  int64  `json:"boardId"`
		Feature  string `json:"boardFeature"`
		Enabling *bool  `json:"enabling"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&input); err != nil {
		jiraError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return
	}
	if input.Enabling == nil {
		jiraError(w, http.StatusBadRequest, "enabling is required.")
		return
	}
	enabling := *input.Enabling
	switch strings.ToUpper(strings.TrimSpace(input.Feature)) {
	case "BACKLOG":
		state := "DISABLED"
		if enabling {
			state = "ENABLED"
		}
		if _, err := h.Store.SetProjectFeature(r.Context(), board.WorkspaceID, userID, board.ProjectID, "jsw.classic.backlog", state); err != nil {
			switch {
			case errors.Is(err, store.ErrProjectFeatureWrongType):
				jiraError(w, http.StatusBadRequest, "Only a software project's board carries the backlog feature.")
			case errors.Is(err, store.ErrProjectPermission):
				jiraError(w, http.StatusForbidden, "Toggling a board feature needs project administration.")
			default:
				jiraError(w, http.StatusInternalServerError, "internal error")
			}
			return
		}
	case "SWIMLANES":
		strategy := "none"
		if enabling {
			strategy = "assignee"
			if board.SwimlaneStrategy != "none" {
				strategy = board.SwimlaneStrategy
			}
		}
		update := store.BoardConfigurationUpdate{
			QuickFilters: board.QuickFilters, SwimlaneStrategy: strategy, CardFields: board.CardFields,
			Columns: board.Columns, Swimlanes: board.Swimlanes, FilterJQL: board.FilterJQL,
			EstimationFieldID: board.EstimationFieldID,
		}
		updated, _, err := h.Store.UpdateBoardConfiguration(r.Context(), userID, board.WorkspaceID, board.ID, update)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrBoardValidation):
				jiraError(w, http.StatusBadRequest, err.Error())
			case errors.Is(err, store.ErrProjectPermission):
				jiraError(w, http.StatusForbidden, "Toggling a board feature needs board administration.")
			default:
				jiraError(w, http.StatusInternalServerError, "internal error")
			}
			return
		}
		board = updated
	case "SPRINTS":
		jiraError(w, http.StatusBadRequest, "Sprints follow the board's type, which this endpoint does not change.")
		return
	default:
		jiraError(w, http.StatusBadRequest, "Unknown board feature.")
		return
	}
	features, err := h.boardFeatureList(r, board)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
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
			"self": h.BaseURL + "/rest/agile/1.0/board/" + boardWireID(board) + "/properties/" + key,
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
			"self": h.BaseURL + "/rest/agile/1.0/board/" + boardWireID(board) + "/properties/" + key,
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
	if strings.HasPrefix(path, "/epic/") {
		h.softwareEpicRoute(w, r, strings.Split(strings.Trim(strings.TrimPrefix(path, "/epic/"), "/"), "/"))
		return
	}
	if strings.HasPrefix(path, "/sprint/") && r.Method == http.MethodGet {
		h.softwareSprintRoute(w, r, strings.Split(strings.Trim(strings.TrimPrefix(path, "/sprint/"), "/"), "/"))
		return
	}
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
	if err != nil || !h.canBrowseBoard(r, workspaceID, userID, board) {
		jiraError(w, http.StatusNotFound, "The board does not exist or you do not have permission to view it.")
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
		h.agileIssueSearch(w, r, workspaceID, userID, backlogScope, []any{board.ProjectID}, "")
	case len(parts) == 3 && parts[1] == "backlog" && parts[2] == "approximate-count":
		count(h.Store.BacklogIssues(r.Context(), board.ID, userID))
	case len(parts) == 2 && parts[1] == "issue":
		h.agileIssueSearch(w, r, workspaceID, userID, boardIssueScope, []any{board.ProjectID, board.StatusIDs(), board.Type}, "")
	case len(parts) == 3 && parts[1] == "issue" && parts[2] == "approximate-count":
		count(h.boardIssueList(r, board, userID))
	case len(parts) == 4 && parts[1] == "epic" && parts[3] == "issue":
		h.boardEpicIssues(w, r, board, workspaceID, userID, parts[2], false)
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
	for _, statusID := range board.StatusIDs() {
		issues = append(issues, columns[statusID]...)
	}
	return issues, nil
}

// softwareSprintRoute serves Jira's /rest/software/1.0 sprint issue read from
// the same implementation as its agile counterpart.
func (h *Handler) softwareSprintRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) != 2 || parts[1] != "issue" {
		jiraError(w, http.StatusNotFound, "No resource found for path "+r.URL.Path)
		return
	}
	workspaceID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	sprint, err := h.Store.SprintByIDInWorkspace(r.Context(), workspaceID, parts[0])
	if err != nil {
		jiraError(w, http.StatusNotFound, "The sprint does not exist.")
		return
	}
	if board, boardErr := h.Store.BoardByIDInWorkspace(r.Context(), workspaceID, sprint.BoardID); boardErr != nil || !h.canBrowseBoard(r, workspaceID, userID, board) {
		jiraError(w, http.StatusNotFound, "The sprint does not exist or you do not have permission to view it.")
		return
	}
	h.agileIssueSearch(w, r, workspaceID, userID, sprintScope, []any{sprint.ID}, sprintOrder)
}
