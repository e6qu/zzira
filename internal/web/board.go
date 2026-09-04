package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type boardCardField struct {
	Kind   string
	Text   string
	User   *models.User
	Labels []string
}

type boardCard struct {
	Issue  *models.Issue
	Fields []boardCardField
}

type boardColumn struct {
	StatusID string
	Name     string
	Cards    []boardCard
}

type boardColumnHeader struct {
	StatusID     string
	Name         string
	Category     string
	VisibleCount int
	TotalCount   int
	Limit        int
	OverLimit    bool
}

type boardSwimlane struct {
	ID      string
	Name    string
	Columns []boardColumn
}

type boardQuickFilterView struct {
	Filter models.BoardQuickFilter
	Active bool
	URL    string
}

type boardViewData struct {
	Board            *models.Board
	ColumnHeaders    []boardColumnHeader
	Swimlanes        []boardSwimlane
	QuickFilters     []boardQuickFilterView
	Members          []*models.User
	SelectedFilters  []string
	SelectedAssignee string
	ClearFiltersURL  string
	HasFilters       bool
	HasSwimlanes     bool
	Admin            bool
}

type boardSettingColumn struct {
	Status models.Status
	Limit  int
}

type boardSettingsData struct {
	Board        *models.Board
	Columns      []boardSettingColumn
	Error        string
	Saved        bool
	ShowPriority bool
	ShowAssignee bool
	ShowLabels   bool
	EmptyFilter  models.BoardQuickFilter
}

func boardPageURL(boardID string, selectedFilters []string, assignee string) string {
	values := url.Values{}
	for _, id := range selectedFilters {
		values.Add("qf", id)
	}
	if assignee != "" {
		values.Set("assignee", assignee)
	}
	target := "/board/" + url.PathEscape(boardID)
	if query := values.Encode(); query != "" {
		target += "?" + query
	}
	return target
}

func selectedBoardFilters(values url.Values) []string {
	requested := values["qf"]
	selected := make([]string, 0, min(len(requested), 20))
	seen := map[string]bool{}
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] || len(selected) == 20 {
			continue
		}
		seen[id] = true
		selected = append(selected, id)
	}
	return selected
}

func boardCardFor(issue *models.Issue, fields []string) boardCard {
	card := boardCard{Issue: issue, Fields: make([]boardCardField, 0, len(fields))}
	for _, field := range fields {
		switch field {
		case "priority":
			text := "No priority"
			if issue.Priority != nil {
				text = issue.Priority.Name
			}
			card.Fields = append(card.Fields, boardCardField{Kind: field, Text: text})
		case "assignee":
			card.Fields = append(card.Fields, boardCardField{Kind: field, User: issue.Assignee})
		case "labels":
			if len(issue.Labels) > 0 {
				card.Fields = append(card.Fields, boardCardField{Kind: field, Labels: issue.Labels})
			}
		}
	}
	return card
}

func boardFilterError(message string) error {
	return fmt.Errorf("%w: %s", store.ErrBoardValidation, message)
}

func (h *Handler) buildBoardView(r *http.Request, user *models.User, wsID string, board *models.Board, admin bool) (boardViewData, error) {
	selected := selectedBoardFilters(r.URL.Query())
	assignee := strings.TrimSpace(r.URL.Query().Get("assignee"))
	members, err := h.Store.MembersByWorkspace(r.Context(), wsID)
	if err != nil {
		return boardViewData{}, err
	}
	if assignee != "" && assignee != "unassigned" {
		valid := false
		for _, member := range members {
			if member.ID == assignee {
				valid = true
				break
			}
		}
		if !valid {
			return boardViewData{}, boardFilterError("assignee does not belong to this workspace")
		}
	}
	columns, err := h.Store.BoardIssuesFiltered(r.Context(), board.ID, user.ID, selected, assignee)
	if err != nil {
		return boardViewData{}, err
	}
	allColumns := columns
	if len(selected) > 0 || assignee != "" {
		allColumns, err = h.Store.BoardIssues(r.Context(), board.ID, user.ID)
		if err != nil {
			return boardViewData{}, err
		}
	}
	statuses := make(map[string]models.Status, len(board.ColumnStatusIDs))
	data := boardViewData{
		Board: board, Members: members, SelectedFilters: selected, SelectedAssignee: assignee,
		ClearFiltersURL: boardPageURL(board.ID, nil, ""), HasFilters: len(selected) > 0 || assignee != "",
		HasSwimlanes: board.SwimlaneStrategy == "assignee", Admin: admin,
	}
	for _, statusID := range board.ColumnStatusIDs {
		status, err := h.Store.StatusByID(r.Context(), statusID)
		if err != nil {
			return boardViewData{}, err
		}
		statuses[statusID] = status
		limit := board.ColumnLimits[statusID]
		total := len(allColumns[statusID])
		data.ColumnHeaders = append(data.ColumnHeaders, boardColumnHeader{
			StatusID: statusID, Name: status.Name, Category: status.Category,
			VisibleCount: len(columns[statusID]), TotalCount: total, Limit: limit, OverLimit: limit > 0 && total > limit,
		})
	}
	active := map[string]bool{}
	for _, id := range selected {
		active[id] = true
	}
	for _, filter := range board.QuickFilters {
		next := append([]string{}, selected...)
		if active[filter.ID] {
			for index, id := range next {
				if id == filter.ID {
					next = append(next[:index], next[index+1:]...)
					break
				}
			}
		} else {
			next = append(next, filter.ID)
		}
		data.QuickFilters = append(data.QuickFilters, boardQuickFilterView{
			Filter: filter, Active: active[filter.ID], URL: boardPageURL(board.ID, next, assignee),
		})
	}

	type laneIssues struct {
		id, name string
		issues   map[string][]*models.Issue
	}
	lanes := []laneIssues{}
	if board.SwimlaneStrategy == "assignee" {
		byAssignee := map[string]map[string][]*models.Issue{}
		for _, statusID := range board.ColumnStatusIDs {
			for _, issue := range columns[statusID] {
				laneID := "unassigned"
				if issue.Assignee != nil {
					laneID = issue.Assignee.ID
				}
				if byAssignee[laneID] == nil {
					byAssignee[laneID] = map[string][]*models.Issue{}
				}
				byAssignee[laneID][statusID] = append(byAssignee[laneID][statusID], issue)
			}
		}
		for _, member := range members {
			if issues := byAssignee[member.ID]; issues != nil {
				lanes = append(lanes, laneIssues{id: member.ID, name: member.DisplayName, issues: issues})
			}
		}
		if issues := byAssignee["unassigned"]; issues != nil {
			lanes = append(lanes, laneIssues{id: "unassigned", name: "Unassigned", issues: issues})
		}
	} else {
		lanes = append(lanes, laneIssues{id: "all", name: "All work", issues: columns})
	}
	for _, lane := range lanes {
		view := boardSwimlane{ID: lane.id, Name: lane.name}
		for _, statusID := range board.ColumnStatusIDs {
			column := boardColumn{StatusID: statusID, Name: statuses[statusID].Name}
			for _, issue := range lane.issues[statusID] {
				column.Cards = append(column.Cards, boardCardFor(issue, board.CardFields))
			}
			view.Columns = append(view.Columns, column)
		}
		data.Swimlanes = append(data.Swimlanes, view)
	}
	return data, nil
}

// BoardPage serves /board/{id} — the sprint board.
func (h *Handler) BoardPage(w http.ResponseWriter, r *http.Request, id string) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	admin, err := h.Store.IsAdmin(r.Context(), wsID, user.ID)
	if err != nil {
		log.Print("board: role lookup failed")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data, err := h.buildBoardView(r, user, wsID, board, admin)
	if errors.Is(err, store.ErrBoardValidation) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		log.Print("board: build failed")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writePage(w, "page_board", pageData{User: user, Data: data, Active: "board"})
}

// BoardFragment serves GET /board/{id}/fragment — the live-swap region.
func (h *Handler) BoardFragment(w http.ResponseWriter, r *http.Request, id string) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, err := h.buildBoardView(r, user, wsID, board, false)
	if errors.Is(err, store.ErrBoardValidation) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		log.Print("board: fragment build failed")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeFragment(w, "board_fragment", data)
}

func (h *Handler) boardSettingsData(r *http.Request, board *models.Board, message string) (boardSettingsData, error) {
	data := boardSettingsData{Board: board, Error: message, Saved: r.URL.Query().Get("saved") == "1"}
	for _, field := range board.CardFields {
		switch field {
		case "priority":
			data.ShowPriority = true
		case "assignee":
			data.ShowAssignee = true
		case "labels":
			data.ShowLabels = true
		}
	}
	for _, statusID := range board.ColumnStatusIDs {
		status, err := h.Store.StatusByID(r.Context(), statusID)
		if err != nil {
			return boardSettingsData{}, err
		}
		data.Columns = append(data.Columns, boardSettingColumn{Status: status, Limit: board.ColumnLimits[statusID]})
	}
	return data, nil
}

func (h *Handler) BoardSettingsPage(w http.ResponseWriter, r *http.Request, boardID string) {
	user, wsID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, boardID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, err := h.boardSettingsData(r, board, "")
	if err != nil {
		log.Print("board settings: build failed")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writePage(w, "page_board_settings", pageData{User: user, Data: data, Active: "board"})
}

func boardConfigurationForm(r *http.Request, board *models.Board) (store.BoardConfigurationUpdate, error) {
	if err := r.ParseForm(); err != nil {
		return store.BoardConfigurationUpdate{}, boardFilterError("invalid form")
	}
	input := store.BoardConfigurationUpdate{
		SwimlaneStrategy: r.PostFormValue("swimlanes"), CardFields: r.PostForm["cardField"], ColumnLimits: map[string]int{},
	}
	for _, statusID := range board.ColumnStatusIDs {
		value := strings.TrimSpace(r.PostFormValue("limit_" + statusID))
		if value == "" {
			continue
		}
		limit, err := strconv.Atoi(value)
		if err != nil {
			return input, boardFilterError("column limits must be whole numbers")
		}
		input.ColumnLimits[statusID] = limit
	}
	ids := r.PostForm["quickFilterID"]
	names := r.PostForm["quickFilterName"]
	descriptions := r.PostForm["quickFilterDescription"]
	queries := r.PostForm["quickFilterJQL"]
	if len(ids) != len(names) || len(ids) != len(descriptions) || len(ids) != len(queries) {
		return input, boardFilterError("quick filter fields are incomplete")
	}
	deleted := map[string]bool{}
	for _, id := range r.PostForm["deleteQuickFilter"] {
		deleted[id] = true
	}
	for index := range ids {
		if deleted[ids[index]] {
			continue
		}
		filter := models.BoardQuickFilter{ID: ids[index], Name: names[index], Description: descriptions[index], JQL: queries[index]}
		if filter.ID == "" && strings.TrimSpace(filter.Name) == "" && strings.TrimSpace(filter.Description) == "" && strings.TrimSpace(filter.JQL) == "" {
			continue
		}
		input.QuickFilters = append(input.QuickFilters, filter)
	}
	return input, nil
}

func (h *Handler) UpdateBoardSettings(w http.ResponseWriter, r *http.Request, boardID string) {
	user, wsID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, boardID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	input, err := boardConfigurationForm(r, board)
	if err == nil {
		board, err = h.Commands.UpdateBoardConfiguration(r.Context(), user.ID, wsID, boardID, input)
	}
	if errors.Is(err, store.ErrBoardValidation) {
		board.QuickFilters = input.QuickFilters
		board.SwimlaneStrategy = input.SwimlaneStrategy
		board.CardFields = input.CardFields
		board.ColumnLimits = input.ColumnLimits
		data, dataErr := h.boardSettingsData(r, board, strings.TrimPrefix(err.Error(), store.ErrBoardValidation.Error()+": "))
		if dataErr != nil {
			log.Print("board settings: validation response failed")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		writePage(w, "page_board_settings", pageData{User: user, Data: data, Active: "board"})
		return
	}
	if err != nil {
		log.Print("board settings: update failed")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/board/"+url.PathEscape(boardID)+"/settings?saved=1", http.StatusSeeOther)
}

// RankIssue applies a drag: rank between neighbors, optionally new status.
func (h *Handler) RankIssue(w http.ResponseWriter, r *http.Request, boardID string) {
	user := h.currentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsID, ok := h.memberWorkspace(r, user)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !parseForm(w, r) {
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, boardID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !boardHasStatus(board, r.PostFormValue("status")) {
		http.Error(w, "status is not a board column", http.StatusBadRequest)
		return
	}
	if err := h.Commands.SetIssueRank(r.Context(), user.ID, wsID,
		r.PostFormValue("issue"), r.PostFormValue("before"), r.PostFormValue("after"),
		r.PostFormValue("status")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func boardHasStatus(b *models.Board, statusID string) bool {
	for _, s := range b.ColumnStatusIDs {
		if s == statusID {
			return true
		}
	}
	return false
}
