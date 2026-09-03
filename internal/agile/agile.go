// Package agile is the REST edge for the Atlassian Jira Agile REST API 1.0
// contract: boards, sprints, board issues, and ranking.
package agile

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	case path == "/backlog/issue" && r.Method == http.MethodPost:
		h.moveIssuesToBacklog(w, r)
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
			"projectName": b.ProjectName,
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
		h.boardBacklog(w, r, board, userID)
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
	issues := []*models.Issue{}
	for _, st := range board.ColumnStatusIDs {
		issues = append(issues, columns[st]...)
	}
	h.writeIssuePage(w, r, issues)
}

func (h *Handler) boardBacklog(w http.ResponseWriter, r *http.Request, board *models.Board, userID string) {
	issues, err := h.Store.BacklogIssues(r.Context(), board.ID, userID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeIssuePage(w, r, issues)
}

func (h *Handler) writeIssuePage(w http.ResponseWriter, r *http.Request, issues []*models.Issue) {
	startAt := 0
	if v := r.URL.Query().Get("startAt"); v != "" {
		var err error
		if startAt, err = strconv.Atoi(v); err != nil || startAt < 0 {
			jiraError(w, http.StatusBadRequest, "startAt must be a non-negative integer.")
			return
		}
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
	bean := map[string]any{
		"id":            s.ID,
		"name":          s.Name,
		"state":         s.State,
		"goal":          s.Goal,
		"originBoardId": s.BoardID,
		"self":          h.BaseURL + "/rest/agile/1.0/sprint/" + s.ID,
	}
	if s.StartDate != "" {
		bean["startDate"] = s.StartDate
	}
	if s.EndDate != "" {
		bean["endDate"] = s.EndDate
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
		Name          string `json:"name"`
		Goal          string `json:"goal"`
		OriginBoardID string `json:"originBoardId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		jiraError(w, http.StatusBadRequest, "A sprint name is required.")
		return
	}
	if _, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, req.OriginBoardID); err != nil {
		jiraError(w, http.StatusBadRequest, "The board does not exist.")
		return
	}
	sprint, err := h.Commands.CreateSprint(r.Context(), userID, wsID, req.OriginBoardID, req.Name, req.Goal)
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
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, h.sprintBean(sprint))
	case len(parts) == 1 && r.Method == http.MethodPut:
		h.updateSprint(w, r, wsID, userID, sprint)
	case len(parts) == 2 && parts[1] == "issue" && r.Method == http.MethodGet:
		h.sprintIssues(w, r, sprint, userID)
	case len(parts) == 2 && parts[1] == "issue" && r.Method == http.MethodPost:
		h.moveIssuesToSprint(w, r, wsID, sprint)
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
	if sprint.State == "closed" {
		jiraError(w, http.StatusBadRequest, "Issues cannot be added to a closed sprint.")
		return
	}
	var req struct {
		Issues []string `json:"issues"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Issues) == 0 {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"issues": "At least one issue key or id is required."})
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, sprint.BoardID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	issues := make([]*models.Issue, 0, len(req.Issues))
	seen := make(map[string]bool, len(req.Issues))
	for _, key := range req.Issues {
		issue, err := h.visibleIssue(r, wsID, userID, key)
		if err != nil {
			jiraError(w, http.StatusNotFound, "Issue "+key+" does not exist.")
			return
		}
		if issue.ProjectID != board.ProjectID {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"issues": "All issues must belong to the sprint's project."})
			return
		}
		if !seen[issue.ID] {
			seen[issue.ID] = true
			issues = append(issues, issue)
		}
	}
	for _, issue := range issues {
		if err := h.Commands.PlanIssue(r.Context(), userID, wsID, board.ID, issue.ID, sprint.ID, "", ""); err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
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
	if len(req.Issues) == 0 {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"issues": "At least one issue key or id is required."})
		return
	}
	if req.RankBeforeIssue == "" && req.RankAfterIssue == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{
			"rankBeforeIssue": "Either rankBeforeIssue or rankAfterIssue is required.",
		})
		return
	}
	if req.RankBeforeIssue != "" && req.RankAfterIssue != "" {
		jiraError(w, http.StatusBadRequest, "Specify only one of rankBeforeIssue or rankAfterIssue.")
		return
	}
	issues := make([]*models.Issue, 0, len(req.Issues))
	seenIssues := make(map[string]bool, len(req.Issues))
	for _, issueKey := range req.Issues {
		issue, err := h.visibleIssue(r, wsID, userID, issueKey)
		if err != nil {
			jiraError(w, http.StatusNotFound, "Issue "+issueKey+" does not exist.")
			return
		}
		if !seenIssues[issue.ID] {
			seenIssues[issue.ID] = true
			issues = append(issues, issue)
		}
	}
	resolveIssue := func(keyOrID string) (*models.Issue, error) {
		ref, err := h.visibleIssue(r, wsID, userID, keyOrID)
		if err != nil {
			return nil, fmt.Errorf("issue %q does not exist", keyOrID)
		}
		return ref, nil
	}
	beforeID := ""
	afterID := ""
	var reference *models.Issue
	var err error
	if req.RankBeforeIssue != "" {
		if reference, err = resolveIssue(req.RankBeforeIssue); err != nil {
			jiraError(w, http.StatusNotFound, err.Error())
			return
		}
		beforeID = reference.ID
	} else {
		if reference, err = resolveIssue(req.RankAfterIssue); err != nil {
			jiraError(w, http.StatusNotFound, err.Error())
			return
		}
		afterID = reference.ID
	}
	for _, issue := range issues {
		if issue.ProjectID != reference.ProjectID || issue.Status.ID != reference.Status.ID {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{
				"issues": "All ranked issues and the reference issue must be in the same board column.",
			})
			return
		}
	}
	rankOne := func(issue *models.Issue) bool {
		if err := h.Commands.SetIssueRank(r.Context(), userID, wsID, issue.ID, beforeID, afterID, ""); err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return false
		}
		if beforeID != "" {
			beforeID = issue.ID
		} else {
			afterID = issue.ID
		}
		return true
	}
	if beforeID != "" {
		for i := len(issues) - 1; i >= 0; i-- {
			if !rankOne(issues[i]) {
				return
			}
		}
	} else {
		for _, issue := range issues {
			if !rankOne(issue) {
				return
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
