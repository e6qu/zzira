package web

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type backlogIssueRow struct {
	Issue           *models.Issue
	BoardID         string
	SprintID        string
	Sprints         []*models.Sprint
	MoveUpBeforeID  string
	MoveDownAfterID string
}

type backlogSection struct {
	Sprint         *models.Sprint
	Issues         []backlogIssueRow
	StartDate      string
	EndDate        string
	SuggestedStart string
	SuggestedEnd   string
}

type backlogPageData struct {
	Board    *models.Board
	Sections []backlogSection
	Backlog  []backlogIssueRow
	Sprints  []*models.Sprint
	Total    int
	Error    string
}

// sanitizeLogValue keeps untrusted route, form, and database values on a
// single physical log line. Escaping rather than dropping the separators also
// preserves enough context to diagnose validation and storage failures.
func sanitizeLogValue(value string) string {
	value = strings.ReplaceAll(value, "\r", `\r`)
	return strings.ReplaceAll(value, "\n", `\n`)
}

func backlogRows(issues []*models.Issue, boardID, sprintID string, sprints []*models.Sprint) []backlogIssueRow {
	rows := make([]backlogIssueRow, len(issues))
	for index, issue := range issues {
		rows[index] = backlogIssueRow{Issue: issue, BoardID: boardID, SprintID: sprintID, Sprints: sprints}
		if index > 0 {
			rows[index].MoveUpBeforeID = issues[index-1].ID
		}
		if index+1 < len(issues) {
			rows[index].MoveDownAfterID = issues[index+1].ID
		}
	}
	return rows
}

func sprintInputDate(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", err
	}
	return parsed.UTC().Format("2006-01-02"), nil
}

func (h *Handler) buildBacklogData(r *http.Request, user *models.User, board *models.Board) (backlogPageData, error) {
	sprints, err := h.Store.SprintsByBoard(r.Context(), board.ID)
	if err != nil {
		return backlogPageData{}, err
	}
	planningSprints := make([]*models.Sprint, 0, len(sprints))
	for _, sprint := range sprints {
		if sprint.State == "active" {
			planningSprints = append(planningSprints, sprint)
		}
	}
	for _, sprint := range sprints {
		if sprint.State == "future" {
			planningSprints = append(planningSprints, sprint)
		}
	}
	now := time.Now().UTC()
	data := backlogPageData{
		Board: board, Sprints: planningSprints, Sections: make([]backlogSection, 0, len(planningSprints)),
		Error: strings.TrimSpace(r.URL.Query().Get("error")),
	}
	for _, sprint := range planningSprints {
		issues, err := h.Store.IssuesBySprint(r.Context(), sprint.ID, user.ID)
		if err != nil {
			return backlogPageData{}, err
		}
		startDate, err := sprintInputDate(sprint.StartDate)
		if err != nil {
			return backlogPageData{}, err
		}
		endDate, err := sprintInputDate(sprint.EndDate)
		if err != nil {
			return backlogPageData{}, err
		}
		section := backlogSection{
			Sprint: sprint, Issues: backlogRows(issues, board.ID, sprint.ID, planningSprints),
			StartDate: startDate, EndDate: endDate,
			SuggestedStart: now.Format("2006-01-02"), SuggestedEnd: now.AddDate(0, 0, 14).Format("2006-01-02"),
		}
		data.Total += len(issues)
		data.Sections = append(data.Sections, section)
	}
	issues, err := h.Store.BacklogIssues(r.Context(), board.ID, user.ID)
	if err != nil {
		return backlogPageData{}, err
	}
	data.Backlog = backlogRows(issues, board.ID, "", planningSprints)
	data.Total += len(issues)
	return data, nil
}

func (h *Handler) BacklogPage(w http.ResponseWriter, r *http.Request, boardID string) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, boardID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, err := h.buildBacklogData(r, user, board)
	if err != nil {
		log.Printf("backlog %s: %s", sanitizeLogValue(boardID), sanitizeLogValue(err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writePage(w, "page_backlog", pageData{User: user, Data: data, Active: "backlog"})
}

func backlogURL(boardID, message string) string {
	target := "/board/" + url.PathEscape(boardID) + "/backlog"
	if message != "" {
		target += "?error=" + url.QueryEscape(message)
	}
	return target
}

func redirectBacklog(w http.ResponseWriter, r *http.Request, boardID, message string) {
	http.Redirect(w, r, backlogURL(boardID, message), http.StatusSeeOther)
}

func (h *Handler) CreateBacklogSprint(w http.ResponseWriter, r *http.Request, boardID string) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	goal := strings.TrimSpace(r.PostFormValue("goal"))
	if name == "" {
		redirectBacklog(w, r, boardID, "Enter a sprint name.")
		return
	}
	if _, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, boardID); err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.Commands.CreateSprint(r.Context(), user.ID, wsID, boardID, name, goal); err != nil {
		log.Printf("create sprint on %s: %s", sanitizeLogValue(boardID), sanitizeLogValue(err.Error()))
		redirectBacklog(w, r, boardID, "The sprint could not be created.")
		return
	}
	redirectBacklog(w, r, boardID, "")
}

func sprintFormDate(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return nil, errors.New("choose a valid date")
	}
	return &parsed, nil
}

func (h *Handler) UpdateBacklogSprint(w http.ResponseWriter, r *http.Request, boardID, sprintID string) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	sprint, err := h.Store.SprintByIDInWorkspace(r.Context(), wsID, sprintID)
	if err != nil || sprint.BoardID != boardID {
		http.NotFound(w, r)
		return
	}
	startDate, err := sprintFormDate(r.PostFormValue("startDate"))
	if err != nil {
		redirectBacklog(w, r, boardID, "Choose a valid sprint start date.")
		return
	}
	endDate, err := sprintFormDate(r.PostFormValue("endDate"))
	if err != nil {
		redirectBacklog(w, r, boardID, "Choose a valid sprint end date.")
		return
	}
	_, err = h.Commands.UpdateSprint(r.Context(), user.ID, wsID, sprintID, store.SprintUpdate{
		Name: r.PostFormValue("name"), Goal: r.PostFormValue("goal"), State: r.PostFormValue("state"),
		StartDate: startDate, EndDate: endDate,
	})
	if errors.Is(err, store.ErrSprintValidation) || errors.Is(err, store.ErrSprintConflict) {
		message := err.Error()
		if _, detail, found := strings.Cut(message, ": "); found {
			message = detail
		}
		redirectBacklog(w, r, boardID, message)
		return
	}
	if err != nil {
		log.Printf("update sprint %s: %s", sanitizeLogValue(sprintID), sanitizeLogValue(err.Error()))
		redirectBacklog(w, r, boardID, "The sprint could not be updated.")
		return
	}
	redirectBacklog(w, r, boardID, "")
}

func (h *Handler) MoveBacklogIssue(w http.ResponseWriter, r *http.Request, boardID string) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	err := h.Commands.PlanIssue(r.Context(), user.ID, wsID, boardID, r.PostFormValue("issue"),
		r.PostFormValue("sprint"), r.PostFormValue("before"), r.PostFormValue("after"))
	if err != nil {
		log.Printf("plan issue on board %s: %s", sanitizeLogValue(boardID), sanitizeLogValue(err.Error()))
		redirectBacklog(w, r, boardID, "The work item could not be moved.")
		return
	}
	redirectBacklog(w, r, boardID, "")
}
