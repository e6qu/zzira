// Package agile is the REST edge for the Atlassian Jira Agile REST API 1.0
// contract: boards, sprints, board issues, and ranking.
package agile

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type Handler struct {
	Store         *store.Store
	Commands      *commands.Service
	IssueBean     func(*models.Issue) map[string]any
	BaseURL       string
	WorkspaceSlug string
}

func (h *Handler) visibleIssue(r *http.Request, workspaceID, userID, idOrKey string) (*models.Issue, error) {
	issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, idOrKey)
	if err != nil {
		return nil, err
	}
	visible, err := authz.CanSeeIssue(r.Context(), h.Store, workspaceID, issue.ProjectID, userID, issue.ID, issue.SecurityLevelID)
	if err != nil || !visible {
		return nil, fmt.Errorf("issue %q does not exist", idOrKey)
	}
	return issue, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Jira serves the same board reads under /rest/software/1.0 with an
	// approximate-count variant, so both base paths reach one implementation.
	if strings.HasPrefix(r.URL.Path, "/rest/software/1.0/") {
		h.softwareRoute(w, r, strings.TrimPrefix(r.URL.Path, "/rest/software/1.0"))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/rest/agile/1.0")
	switch {
	case path == "/board" && r.Method == http.MethodGet:
		h.listBoardsFiltered(w, r)
	case path == "/board" && r.Method == http.MethodPost:
		h.createBoard(w, r)
	case strings.HasPrefix(path, "/board/filter/") && r.Method == http.MethodGet:
		h.boardsByFilter(w, r, strings.TrimPrefix(path, "/board/filter/"))
	case strings.HasPrefix(path, "/board/"):
		h.boardRoute(w, r, strings.Split(strings.TrimPrefix(path, "/board/"), "/"))
	case path == "/sprint" && r.Method == http.MethodPost:
		h.createSprint(w, r)
	case strings.HasPrefix(path, "/sprint/"):
		h.sprintRoute(w, r, strings.Split(strings.TrimPrefix(path, "/sprint/"), "/"))
	case path == "/backlog/issue" && r.Method == http.MethodPost:
		h.moveIssuesToBacklog(w, r)
	case strings.HasPrefix(path, "/backlog/") && strings.HasSuffix(path, "/issue") && r.Method == http.MethodPost:
		h.moveIssuesToBacklogForBoard(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/backlog/"), "/issue"))
	case path == "/issue/rank" && (r.Method == http.MethodPut || r.Method == http.MethodPost):
		h.rankIssues(w, r)
	case strings.HasPrefix(path, "/issue/"):
		h.agileIssueRoute(w, r, strings.Split(strings.TrimPrefix(path, "/issue/"), "/"))
	case strings.HasPrefix(path, "/epic/"):
		h.epicRoute(w, r, strings.Split(strings.TrimPrefix(path, "/epic/"), "/"))
	default:
		jiraError(w, http.StatusNotFound, fmt.Sprintf("No resource found for path %s", r.URL.Path))
	}
}

func (h *Handler) authWorkspace(r *http.Request) (wsID, userID string, status int, msg string) {
	userID, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		return "", "", http.StatusUnauthorized, "You are not authenticated. Authentication required to perform this operation."
	}
	if h.WorkspaceSlug == "" {
		return "", "", http.StatusInternalServerError, "workspace is not configured"
	}
	wsID, err = h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		return "", "", http.StatusInternalServerError, "no workspace configured"
	}
	ok, err := authz.CanSeeWorkspace(r.Context(), h.Store, wsID, userID)
	if err != nil || !ok {
		return "", "", http.StatusForbidden, "You do not have permission to perform this operation."
	}
	return wsID, userID, 0, ""
}

func (h *Handler) boardBean(b *models.Board) map[string]any {
	return map[string]any{
		"id":   b.JiraID,
		"name": b.Name,
		"type": b.Type,
		"self": h.BaseURL + "/rest/agile/1.0/board/" + boardWireID(b),
		"location": map[string]any{
			"projectKey":     b.ProjectKey,
			"projectName":    b.ProjectName,
			"projectId":      wireNumber(b.ProjectID),
			"projectTypeKey": b.ProjectTypeKey,
			"displayName":    b.ProjectName + " (" + b.ProjectKey + ")",
			"name":           b.ProjectName + " (" + b.ProjectKey + ")",
		},
		"isPrivate": false,
	}
}

// boardWireID is the id clients know a board by.
func boardWireID(b *models.Board) string {
	return strconv.FormatInt(b.JiraID, 10)
}

// boardFilterWireID is the id of the filter a board shows: the saved filter it
// was created from, or its own board filter.
func boardFilterWireID(b *models.Board) string {
	if b.SourceFilterJiraID != 0 {
		return strconv.FormatInt(b.SourceFilterJiraID, 10)
	}
	return strconv.FormatInt(b.FilterJiraID, 10)
}

// sprintWireID is the id clients know a sprint by.
func sprintWireID(s *models.Sprint) string {
	return strconv.FormatInt(s.JiraID, 10)
}

// wireNumber is a numeric id as a JSON number, or the id unchanged.
func wireNumber(id string) any {
	if number, err := strconv.ParseInt(id, 10, 64); err == nil {
		return number
	}
	return id
}

func (h *Handler) boardRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	id := parts[0]
	wsID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, id)
	if err != nil || !h.canBrowseBoard(r, wsID, userID, board) {
		jiraError(w, http.StatusNotFound, "The board does not exist or you do not have permission to view it.")
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		bean := h.boardBean(board)
		for _, option := range splitValues(r, "expand") {
			if option == "admins" {
				bean["admins"] = h.boardAdminsBean(r, wsID, board)
			}
		}
		writeJSON(w, http.StatusOK, bean)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		h.deleteBoard(w, r, wsID, userID, board)
	case len(parts) == 2 && parts[1] == "issue" && r.Method == http.MethodGet:
		h.agileIssueSearch(w, r, wsID, userID, boardIssueScope, []any{board.ProjectID, board.ColumnStatusIDs, board.Type}, "")
	case len(parts) == 2 && parts[1] == "issue" && r.Method == http.MethodPost:
		h.moveIssuesToBoard(w, r, wsID, userID, board)
	case len(parts) == 2 && parts[1] == "backlog" && r.Method == http.MethodGet:
		h.agileIssueSearch(w, r, wsID, userID, backlogScope, []any{board.ProjectID}, "")
	case len(parts) == 2 && parts[1] == "sprint" && r.Method == http.MethodGet:
		h.boardSprintsFiltered(w, r, board)
	case len(parts) == 2 && parts[1] == "configuration" && r.Method == http.MethodGet:
		h.boardConfiguration(w, r, board)
	case len(parts) == 2 && parts[1] == "quickfilter" && r.Method == http.MethodGet:
		h.boardQuickFilters(w, r, board)
	case len(parts) == 3 && parts[1] == "quickfilter" && r.Method == http.MethodGet:
		h.boardQuickFilter(w, board, parts[2])
	case len(parts) == 2 && parts[1] == "project" && r.Method == http.MethodGet:
		h.boardProjects(w, r, board, false)
	case len(parts) == 3 && parts[1] == "project" && parts[2] == "full" && r.Method == http.MethodGet:
		h.boardProjects(w, r, board, true)
	case len(parts) == 2 && parts[1] == "version" && r.Method == http.MethodGet:
		h.boardVersions(w, r, board)
	case len(parts) == 2 && parts[1] == "epic" && r.Method == http.MethodGet:
		h.boardEpics(w, r, board, wsID, userID)
	case len(parts) == 4 && parts[1] == "epic" && parts[3] == "issue" && r.Method == http.MethodGet:
		h.boardEpicIssues(w, r, board, wsID, userID, parts[2], false)
	case len(parts) == 4 && parts[1] == "sprint" && parts[3] == "issue" && r.Method == http.MethodGet:
		h.boardSprintIssues(w, r, board, parts[2], userID)
	case len(parts) == 2 && parts[1] == "features" && r.Method == http.MethodGet:
		h.boardFeatures(w, r, board)
	case len(parts) == 2 && parts[1] == "features" && r.Method == http.MethodPut:
		jiraError(w, http.StatusBadRequest, "Board features follow the board configuration and cannot be toggled here.")
	case len(parts) == 2 && parts[1] == "reports" && r.Method == http.MethodGet:
		h.boardReports(w, r, board)
	case len(parts) == 2 && parts[1] == "properties":
		h.boardProperties(w, r, board)
	case len(parts) == 3 && parts[1] == "properties":
		h.boardProperty(w, r, board, parts[2])
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) boardConfiguration(w http.ResponseWriter, r *http.Request, board *models.Board) {
	columns := make([]map[string]any, 0, len(board.ColumnStatusIDs))
	constraintType := "none"
	for _, statusID := range board.ColumnStatusIDs {
		status, err := h.Store.StatusByIDForProject(r.Context(), statusID, board.ProjectID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		column := map[string]any{
			"name": status.Name,
			"statuses": []map[string]any{{
				"id": strconv.FormatInt(status.JiraID, 10), "self": h.BaseURL + "/rest/api/3/status/" + strconv.FormatInt(status.JiraID, 10),
			}},
		}
		if limit := board.ColumnLimits[statusID]; limit > 0 {
			column["max"] = limit
			constraintType = "issueCount"
		}
		columns = append(columns, column)
	}
	response := map[string]any{
		"id": board.JiraID, "name": board.Name, "type": board.Type,
		"self": h.BaseURL + "/rest/agile/1.0/board/" + boardWireID(board) + "/configuration",
		"filter": map[string]any{
			"id": boardFilterWireID(board), "self": h.BaseURL + "/rest/api/3/filter/" + boardFilterWireID(board),
		},
		"location": map[string]any{
			"id": wireNumber(board.ProjectID), "key": board.ProjectKey, "name": board.ProjectName,
			"projectId": wireNumber(board.ProjectID), "projectKey": board.ProjectKey, "projectName": board.ProjectName,
			"displayName": board.ProjectName, "type": "project",
			"self": h.BaseURL + "/rest/api/3/project/" + board.ProjectID,
		},
		"columnConfig": map[string]any{"constraintType": constraintType, "columns": columns},
		"ranking":      map[string]any{"rankCustomFieldId": rankCustomFieldID},
	}
	if board.Type == "scrum" {
		response["estimation"] = map[string]any{"type": "issueCount"}
		if board.EstimationFieldID != "" {
			response["estimation"] = map[string]any{"type": "field", "field": map[string]any{
				"fieldId": board.EstimationFieldID, "displayName": board.EstimationFieldName,
			}}
		}
	} else if board.FilterJQL != "" {
		response["subQuery"] = map[string]any{"query": board.FilterJQL}
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) quickFilterBean(boardID int64, filter models.BoardQuickFilter) map[string]any {
	return map[string]any{
		"id": filter.JiraID, "boardId": boardID, "name": filter.Name, "description": filter.Description,
		"jql": filter.JQL, "position": filter.Position,
	}
}

func (h *Handler) boardQuickFilters(w http.ResponseWriter, r *http.Request, board *models.Board) {
	startAt := 0
	maxResults := 50
	var err error
	if value := r.URL.Query().Get("startAt"); value != "" {
		startAt, err = strconv.Atoi(value)
		if err != nil || startAt < 0 {
			jiraError(w, http.StatusBadRequest, "startAt must be a non-negative integer.")
			return
		}
	}
	if value := r.URL.Query().Get("maxResults"); value != "" {
		maxResults, err = strconv.Atoi(value)
		if err != nil || maxResults < 1 || maxResults > 100 {
			jiraError(w, http.StatusBadRequest, "maxResults must be between 1 and 100.")
			return
		}
	}
	filters := append([]models.BoardQuickFilter(nil), board.QuickFilters...)
	sort.SliceStable(filters, func(i, j int) bool { return filters[i].Position < filters[j].Position })
	total := len(filters)
	if startAt > total {
		startAt = total
	}
	end := min(startAt+maxResults, total)
	values := make([]map[string]any, 0, end-startAt)
	for _, filter := range filters[startAt:end] {
		values = append(values, h.quickFilterBean(board.JiraID, filter))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"startAt": startAt, "maxResults": maxResults, "total": total, "isLast": end == total, "values": values,
	})
}

func (h *Handler) boardQuickFilter(w http.ResponseWriter, board *models.Board, quickFilterID string) {
	for _, filter := range board.QuickFilters {
		if strconv.FormatInt(filter.JiraID, 10) == quickFilterID {
			writeJSON(w, http.StatusOK, h.quickFilterBean(board.JiraID, filter))
			return
		}
	}
	jiraError(w, http.StatusNotFound, "The quick filter does not exist.")
}

func (h *Handler) sprintBean(s *models.Sprint) map[string]any {
	bean := map[string]any{
		"id":            s.JiraID,
		"name":          s.Name,
		"state":         s.State,
		"goal":          s.Goal,
		"originBoardId": s.BoardJiraID,
		"self":          h.BaseURL + "/rest/agile/1.0/sprint/" + sprintWireID(s),
	}
	if s.StartDate != "" {
		bean["startDate"] = s.StartDate
	}
	if s.EndDate != "" {
		bean["endDate"] = s.EndDate
	}
	if s.CreatedDate != "" {
		bean["createdDate"] = s.CreatedDate
	}
	if s.CompleteDate != "" {
		bean["completeDate"] = s.CompleteDate
	}
	return bean
}

func (h *Handler) createSprint(w http.ResponseWriter, r *http.Request) {
	wsID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	var req struct {
		Name          string          `json:"name"`
		Goal          string          `json:"goal"`
		OriginBoard   json.RawMessage `json:"originBoardId"`
		OriginBoardID string          `json:"-"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	req.OriginBoardID = strings.Trim(string(req.OriginBoard), `"`)
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		jiraError(w, http.StatusBadRequest, "A sprint name is required.")
		return
	}
	originBoard, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, req.OriginBoardID)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "The board does not exist.")
		return
	}
	sprint, err := h.Commands.CreateSprint(r.Context(), userID, wsID, originBoard.ID, req.Name, req.Goal)
	if errors.Is(err, store.ErrSprintValidation) {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, h.sprintBean(sprint))
}

func (h *Handler) moveIssuesToBacklog(w http.ResponseWriter, r *http.Request) {
	wsID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	var req struct {
		Issues []string `json:"issues"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Issues) == 0 {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"issues": "At least one issue key or id is required."})
		return
	}
	planned := make([]*models.Issue, 0, len(req.Issues))
	seen := make(map[string]bool, len(req.Issues))
	for _, idOrKey := range req.Issues {
		issue, err := h.visibleIssue(r, wsID, userID, idOrKey)
		if err != nil {
			jiraError(w, http.StatusNotFound, "Issue "+idOrKey+" does not exist.")
			return
		}
		if seen[issue.ID] {
			continue
		}
		seen[issue.ID] = true
		planned = append(planned, issue)
	}
	for _, issue := range planned {
		if err := h.Commands.MoveIssueToBacklog(r.Context(), userID, wsID, issue.ID); err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) sprintRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	id := parts[0]
	wsID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	sprint, err := h.Store.SprintByIDInWorkspace(r.Context(), wsID, id)
	if err != nil {
		jiraError(w, http.StatusNotFound, "The sprint does not exist.")
		return
	}
	if board, boardErr := h.Store.BoardByIDInWorkspace(r.Context(), wsID, sprint.BoardID); boardErr != nil || !h.canBrowseBoard(r, wsID, userID, board) {
		jiraError(w, http.StatusNotFound, "The sprint does not exist or you do not have permission to view it.")
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, h.sprintBean(sprint))
	case len(parts) == 1 && r.Method == http.MethodPut:
		h.updateSprint(w, r, wsID, userID, sprint)
	case len(parts) == 2 && parts[1] == "issue" && r.Method == http.MethodGet:
		h.agileIssueSearch(w, r, wsID, userID, sprintScope, []any{sprint.ID}, sprintOrder)
	case len(parts) == 2 && parts[1] == "issue" && r.Method == http.MethodPost:
		h.moveIssuesToSprintRanked(w, r, wsID, sprint)
	case len(parts) == 1 && r.Method == http.MethodPost:
		// Jira's POST is the partial update; PUT replaces. Both land on the
		// same handler because it already treats absent fields as unchanged.
		h.updateSprint(w, r, wsID, userID, sprint)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		h.deleteSprint(w, r, wsID, userID, sprint)
	case len(parts) == 2 && parts[1] == "swap" && r.Method == http.MethodPost:
		h.swapSprint(w, r, wsID, userID, sprint)
	case len(parts) == 2 && parts[1] == "properties":
		h.sprintProperties(w, r, sprint)
	case len(parts) == 3 && parts[1] == "properties":
		h.sprintProperty(w, r, sprint, parts[2])
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func parseSprintDate(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, fmt.Errorf("dates must use RFC 3339 format")
	}
	return &parsed, nil
}

func (h *Handler) updateSprint(w http.ResponseWriter, r *http.Request, wsID, userID string, current *models.Sprint) {
	var req struct {
		Name      *string `json:"name"`
		Goal      *string `json:"goal"`
		State     *string `json:"state"`
		StartDate *string `json:"startDate"`
		EndDate   *string `json:"endDate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	name, goal, state := current.Name, current.Goal, current.State
	if req.Name != nil {
		name = *req.Name
	}
	if req.Goal != nil {
		goal = *req.Goal
	}
	if req.State != nil {
		state = *req.State
	}
	startValue, endValue := current.StartDate, current.EndDate
	if req.StartDate != nil {
		startValue = *req.StartDate
	}
	if req.EndDate != nil {
		endValue = *req.EndDate
	}
	startDate, err := parseSprintDate(startValue)
	if err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"startDate": err.Error()})
		return
	}
	endDate, err := parseSprintDate(endValue)
	if err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"endDate": err.Error()})
		return
	}
	updated, err := h.Commands.UpdateSprint(r.Context(), userID, wsID, current.ID, store.SprintUpdate{
		Name: name, Goal: goal, State: state, StartDate: startDate, EndDate: endDate,
	})
	if errors.Is(err, store.ErrSprintValidation) || errors.Is(err, store.ErrSprintConflict) {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, h.sprintBean(updated))
}
