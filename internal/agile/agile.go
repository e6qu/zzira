// Package agile is the REST edge for the Atlassian Jira Agile REST API 1.0
// contract: boards, sprints, board issues, and ranking.
package agile

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type Handler struct {
	Store         *store.Store
	IssueBean     func(*models.Issue) map[string]any
	BaseURL       string
	WorkspaceSlug string
}

func (h *Handler) visibleIssue(r *http.Request, workspaceID, userID, idOrKey string) (*models.Issue, error) {
	issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, idOrKey)
	if err != nil {
		return nil, err
	}
	visible, err := authz.CanSeeIssue(r.Context(), h.Store, workspaceID, issue.ProjectID, userID, issue.SecurityLevelID)
	if err != nil || !visible {
		return nil, fmt.Errorf("issue %q does not exist", idOrKey)
	}
	return issue, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/rest/agile/1.0")
	switch {
	case path == "/board" && r.Method == http.MethodGet:
		h.listBoards(w, r)
	case strings.HasPrefix(path, "/board/"):
		h.boardRoute(w, r, strings.Split(strings.TrimPrefix(path, "/board/"), "/"))
	case path == "/sprint" && r.Method == http.MethodPost:
		h.createSprint(w, r)
	case strings.HasPrefix(path, "/sprint/"):
		h.sprintRoute(w, r, strings.Split(strings.TrimPrefix(path, "/sprint/"), "/"))
	case path == "/issue/rank" && r.Method == http.MethodPost:
		h.rank(w, r)
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
		"id":   b.ID,
		"name": b.Name,
		"type": b.Type,
		"self": h.BaseURL + "/rest/agile/1.0/board/" + b.ID,
		"location": map[string]any{
			"projectKey":  b.ProjectKey,
			"projectName": b.ProjectKey,
			"projectId":   b.ProjectID,
		},
	}
}

func (h *Handler) listBoards(w http.ResponseWriter, r *http.Request) {
	wsID, _, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	boards, err := h.Store.BoardsByWorkspace(r.Context(), wsID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	values := make([]map[string]any, 0, len(boards))
	for _, b := range boards {
		values = append(values, h.boardBean(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"maxResults": 50,
		"startAt":    0,
		"total":      len(values),
		"isLast":     true,
		"values":     values,
	})
}

func (h *Handler) boardRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	id := parts[0]
	wsID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, id)
	if err != nil {
		jiraError(w, http.StatusNotFound, "The board does not exist.")
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, h.boardBean(board))
	case len(parts) == 2 && parts[1] == "issue" && r.Method == http.MethodGet:
		h.boardIssues(w, r, board, userID)
	case len(parts) == 2 && parts[1] == "backlog" && r.Method == http.MethodGet:
		h.boardIssues(w, r, board, userID)
	case len(parts) == 2 && parts[1] == "sprint" && r.Method == http.MethodGet:
		h.boardSprints(w, r, board)
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) boardIssues(w http.ResponseWriter, r *http.Request, board *models.Board, userID string) {
	columns, err := h.Store.BoardIssues(r.Context(), board.ID, userID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	startAt := 0
	if v := r.URL.Query().Get("startAt"); v != "" {
		var err error
		if startAt, err = strconv.Atoi(v); err != nil || startAt < 0 {
			jiraError(w, http.StatusBadRequest, "startAt must be a non-negative integer.")
			return
		}
	}
	issues := []*models.Issue{}
	for _, st := range board.ColumnStatusIDs {
		issues = append(issues, columns[st]...)
	}
	total := len(issues)
	if startAt > total {
		startAt = total
	}
	end := startAt + 50
	if end > total {
		end = total
	}
	beans := make([]map[string]any, 0, end-startAt)
	for _, i := range issues[startAt:end] {
		beans = append(beans, h.IssueBean(i))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"expand":         "schema,names",
		"startAt":        startAt,
		"maxResults":     50,
		"total":          total,
		"issues":         beans,
		"jqlInformation": map[string]any{},
	})
}

func (h *Handler) boardSprints(w http.ResponseWriter, r *http.Request, board *models.Board) {
	sprints, err := h.Store.SprintsByBoard(r.Context(), board.ID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	values := make([]map[string]any, 0, len(sprints))
	for _, s := range sprints {
		values = append(values, h.sprintBean(s))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"maxResults": 50,
		"startAt":    0,
		"isLast":     true,
		"values":     values,
	})
}

func (h *Handler) sprintBean(s *models.Sprint) map[string]any {
	return map[string]any{
		"id":    s.ID,
		"name":  s.Name,
		"state": s.State,
		"goal":  s.Goal,
		"self":  h.BaseURL + "/rest/agile/1.0/sprint/" + s.ID,
	}
}

func (h *Handler) createSprint(w http.ResponseWriter, r *http.Request) {
	wsID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	var req struct {
		Name          string `json:"name"`
		Goal          string `json:"goal"`
		OriginBoardID string `json:"originBoardId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		jiraError(w, http.StatusBadRequest, "A sprint name is required.")
		return
	}
	if _, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, req.OriginBoardID); err != nil {
		jiraError(w, http.StatusBadRequest, "The board does not exist.")
		return
	}
	sprint, _, err := h.Store.CreateSprint(r.Context(), userID, wsID, req.OriginBoardID, req.Name, req.Goal)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, h.sprintBean(sprint))
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
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, h.sprintBean(sprint))
	case len(parts) == 2 && parts[1] == "issue" && r.Method == http.MethodGet:
		h.sprintIssues(w, r, sprint, userID)
	case len(parts) == 2 && parts[1] == "issue" && r.Method == http.MethodPost:
		h.moveIssuesToSprint(w, r, wsID, sprint)
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) sprintIssues(w http.ResponseWriter, r *http.Request, sprint *models.Sprint, userID string) {
	issues, err := h.Store.IssuesBySprint(r.Context(), sprint.ID, userID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	beans := make([]map[string]any, 0, len(issues))
	for _, i := range issues {
		beans = append(beans, h.IssueBean(i))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"maxResults": 50,
		"startAt":    0,
		"total":      len(beans),
		"issues":     beans,
	})
}

func (h *Handler) moveIssuesToSprint(w http.ResponseWriter, r *http.Request, wsID string, sprint *models.Sprint) {
	_, userID, status, msg := h.authWorkspace(r)
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
	for _, key := range req.Issues {
		issue, err := h.visibleIssue(r, wsID, userID, key)
		if err != nil {
			jiraError(w, http.StatusNotFound, "Issue "+key+" does not exist.")
			return
		}
		rank, err := h.Store.RankBetween(r.Context(), wsID, issue.ProjectID, issue.Status.ID, "", "")
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := h.Store.AddIssueToSprint(r.Context(), userID, wsID, sprint.ID, issue.ID, rank); err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// rank implements POST /rest/agile/1.0/issue/rank.
func (h *Handler) rank(w http.ResponseWriter, r *http.Request) {
	wsID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	var req struct {
		Issues          []string `json:"issues"`
		RankBeforeIssue string   `json:"rankBeforeIssue"`
		RankAfterIssue  string   `json:"rankAfterIssue"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	if req.RankBeforeIssue == "" && req.RankAfterIssue == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{
			"rankBeforeIssue": "Either rankBeforeIssue or rankAfterIssue is required.",
		})
		return
	}
	for _, issueKey := range req.Issues {
		issue, err := h.visibleIssue(r, wsID, userID, issueKey)
		if err != nil {
			jiraError(w, http.StatusNotFound, "Issue "+issueKey+" does not exist.")
			return
		}
		resolveID := func(keyOrID string) (string, error) {
			ref, err := h.visibleIssue(r, wsID, userID, keyOrID)
			if err != nil {
				return "", fmt.Errorf("issue %q does not exist", keyOrID)
			}
			return ref.ID, nil
		}
		beforeID := ""
		afterID := ""
		if req.RankBeforeIssue != "" {
			if beforeID, err = resolveID(req.RankBeforeIssue); err != nil {
				jiraError(w, http.StatusNotFound, err.Error())
				return
			}
		}
		if req.RankAfterIssue != "" {
			if afterID, err = resolveID(req.RankAfterIssue); err != nil {
				jiraError(w, http.StatusNotFound, err.Error())
				return
			}
		}
		rank, err := h.Store.RankBetween(r.Context(), wsID, issue.ProjectID, issue.Status.ID, beforeID, afterID)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := h.Store.SetIssueRank(r.Context(), userID, wsID, issue.ID, rank, ""); err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
