package web

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
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
	// StatusID is the status a card dropped on this column takes: the first
	// of the column's statuses. The column shows work in any of them.
	StatusID string
	Name     string
	Cards    []boardCard
}

type boardColumnHeader struct {
	StatusID     string
	Name         string
	StatusNames  []string
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

// boardSettingColumn is one column on the settings page, with the statuses it
// gathers named for the reader.
type boardSettingColumn struct {
	Name      string
	Limit     int
	Statuses  []models.Status
	StatusIDs []string
}

type boardSettingsData struct {
	Board        *models.Board
	Admins       []models.BoardAdmin
	Members      []*models.User
	Groups       []*models.Group
	Columns      []boardSettingColumn
	Error        string
	Saved        bool
	ShowPriority bool
	ShowAssignee bool
	ShowLabels   bool
	EmptyFilter  models.BoardQuickFilter
	// Statuses are every status the project uses; UnmappedStatuses are those
	// no column shows, which are invisible on the board until a column takes
	// them.
	Statuses         []models.Status
	UnmappedStatuses []models.Status
	EstimationFields []*models.CustomField
	// SwimlaneChoices are the groupings the board offers, with the one in
	// force marked.
	SwimlaneChoices []boardSwimlaneChoice
	// EmptySwimlane is the blank row the page adds a named query with.
	EmptySwimlane models.BoardSwimlane
}

// boardSwimlaneChoice is one grouping on the settings page.
type boardSwimlaneChoice struct {
	Value    string
	Label    string
	Selected bool
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
	data := boardViewData{
		Board: board, Members: members, SelectedFilters: selected, SelectedAssignee: assignee,
		ClearFiltersURL: boardPageURL(board.ID, nil, ""), HasFilters: len(selected) > 0 || assignee != "",
		HasSwimlanes: board.SwimlaneStrategy != "none", Admin: admin,
	}
	// A column counts the work in every status it gathers, and takes its
	// category from the first of them, which is what colours the header.
	for _, boardColumn := range board.Columns {
		header := boardColumnHeader{Name: boardColumn.Name, Limit: boardColumn.Limit}
		for index, statusID := range boardColumn.StatusIDs {
			status, err := h.Store.StatusByIDForProject(r.Context(), statusID, board.ProjectID)
			if err != nil {
				return boardViewData{}, err
			}
			if index == 0 {
				header.StatusID = statusID
				header.Category = status.Category
			}
			header.StatusNames = append(header.StatusNames, status.Name)
			header.VisibleCount += len(columns[statusID])
			header.TotalCount += len(allColumns[statusID])
		}
		header.OverLimit = header.Limit > 0 && header.TotalCount > header.Limit
		data.ColumnHeaders = append(data.ColumnHeaders, header)
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
	// A lane is a group of the board's work and a name for it. Which grouping
	// is in force is the board's own setting; every one of them lands here as
	// the same shape, so the rendering below does not know which it is.
	lanes := []laneIssues{}
	group := func(laneOf func(*models.Issue) (id string, name string, ok bool), order []string, names map[string]string, lastName string) {
		grouped := map[string]map[string][]*models.Issue{}
		laneNames := map[string]string{}
		seen := []string{}
		for _, statusID := range board.StatusIDs() {
			for _, issue := range columns[statusID] {
				laneID, laneName, ok := laneOf(issue)
				if !ok {
					laneID, laneName = "", lastName
				}
				if grouped[laneID] == nil {
					grouped[laneID] = map[string][]*models.Issue{}
					laneNames[laneID] = laneName
					seen = append(seen, laneID)
				}
				grouped[laneID][statusID] = append(grouped[laneID][statusID], issue)
			}
		}
		// The board's own order comes first where there is one -- members,
		// named queries -- and whatever it does not name follows in the order
		// the work put it there, so a lane is never dropped.
		emitted := map[string]bool{}
		emit := func(laneID string) {
			if emitted[laneID] {
				return
			}
			issues, ok := grouped[laneID]
			if !ok {
				return
			}
			emitted[laneID] = true
			name := laneNames[laneID]
			if given, ok := names[laneID]; ok && given != "" {
				name = given
			}
			lanes = append(lanes, laneIssues{id: laneID, name: name, issues: issues})
		}
		for _, laneID := range order {
			emit(laneID)
		}
		for _, laneID := range seen {
			if laneID != "" {
				emit(laneID)
			}
		}
		// The catch-all is last, as it is in Jira.
		emit("")
	}
	switch board.SwimlaneStrategy {
	case "assignee":
		order := make([]string, 0, len(members))
		names := make(map[string]string, len(members))
		for _, member := range members {
			order = append(order, member.ID)
			names[member.ID] = member.DisplayName
		}
		group(func(issue *models.Issue) (string, string, bool) {
			if issue.Assignee == nil {
				return "", "", false
			}
			return issue.Assignee.ID, issue.Assignee.DisplayName, true
		}, order, names, "Unassigned")
	case "epic":
		ids := make([]string, 0)
		for _, statusID := range board.StatusIDs() {
			for _, issue := range columns[statusID] {
				ids = append(ids, issue.ID)
			}
		}
		epics, err := h.Store.BoardEpicLanes(r.Context(), board.WorkspaceID, ids)
		if err != nil {
			return boardViewData{}, err
		}
		group(func(issue *models.Issue) (string, string, bool) {
			epic, ok := epics[issue.ID]
			if !ok {
				return "", "", false
			}
			return epic.ID, epic.Key + " " + epic.Summary, true
		}, nil, nil, "Work under no epic")
	case "project":
		group(func(issue *models.Issue) (string, string, bool) {
			if issue.ProjectID == "" {
				return "", "", false
			}
			name := issue.ProjectID
			// A key is what a reader recognises a project by, and every work
			// item carries its own.
			if key, _, found := strings.Cut(issue.Key, "-"); found {
				name = key
			}
			return issue.ProjectID, name, true
		}, nil, nil, "No project")
	case "query":
		membership, err := h.Store.BoardSwimlaneMembership(r.Context(), board, user.ID)
		if err != nil {
			return boardViewData{}, err
		}
		order := make([]string, 0, len(board.Swimlanes))
		names := make(map[string]string, len(board.Swimlanes))
		for index, lane := range board.Swimlanes {
			laneID := strconv.Itoa(index)
			order = append(order, laneID)
			names[laneID] = lane.Name
		}
		group(func(issue *models.Issue) (string, string, bool) {
			index, ok := membership[issue.ID]
			if !ok {
				return "", "", false
			}
			laneID := strconv.Itoa(index)
			return laneID, names[laneID], true
		}, order, names, "Everything else")
	default:
		lanes = append(lanes, laneIssues{id: "all", name: "All work", issues: columns})
	}
	for _, lane := range lanes {
		view := boardSwimlane{ID: lane.id, Name: lane.name}
		for _, boardColumnConfig := range board.Columns {
			column := boardColumn{Name: boardColumnConfig.Name}
			if len(boardColumnConfig.StatusIDs) > 0 {
				column.StatusID = boardColumnConfig.StatusIDs[0]
			}
			// Work from several statuses shares one column, and the board's
			// order is rank, so the column is ranked as a whole rather than
			// status after status.
			gathered := []*models.Issue{}
			for _, statusID := range boardColumnConfig.StatusIDs {
				gathered = append(gathered, lane.issues[statusID]...)
			}
			sort.SliceStable(gathered, func(i, j int) bool {
				if gathered[i].Rank != gathered[j].Rank {
					return gathered[i].Rank < gathered[j].Rank
				}
				return gathered[i].Key < gathered[j].Key
			})
			for _, issue := range gathered {
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
		log.Print("board: role lookup failed: ", strconv.Quote(err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data, err := h.buildBoardView(r, user, wsID, board, admin)
	if errors.Is(err, store.ErrBoardValidation) {
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	if err != nil {
		log.Print("board: build failed: ", strconv.Quote(err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_board", user, wsID, data, "board", board.ProjectID)
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
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	if err != nil {
		log.Print("board: fragment build failed: ", strconv.Quote(err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeFragment(w, "board_fragment", data)
}

func (h *Handler) boardSettingsData(r *http.Request, board *models.Board, message string) (boardSettingsData, error) {
	data := boardSettingsData{Board: board, Error: message, Saved: r.URL.Query().Get("saved") == "1"}
	if data.Error == "" {
		data.Error = strings.TrimSpace(r.URL.Query().Get("error"))
	}
	admins, err := h.Store.BoardAdmins(r.Context(), board.ID)
	if err != nil {
		return boardSettingsData{}, err
	}
	members, err := h.Store.MembersByWorkspace(r.Context(), board.WorkspaceID)
	if err != nil {
		return boardSettingsData{}, err
	}
	groups, err := h.Store.GroupsForWorkspace(r.Context(), board.WorkspaceID)
	if err != nil {
		return boardSettingsData{}, err
	}
	data.Admins, data.Members, data.Groups = admins, members, groups
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
	// The statuses the project's workflows use are what a column may gather,
	// so the page offers all of them and marks which column holds each.
	available, err := h.Store.StatusesForProject(r.Context(), board.WorkspaceID, board.ProjectID, true)
	if err != nil {
		return boardSettingsData{}, err
	}
	byID := make(map[string]models.Status, len(available))
	for _, status := range available {
		byID[status.ID] = status
	}
	data.Statuses = available
	placed := map[string]bool{}
	for _, column := range board.Columns {
		view := boardSettingColumn{Name: column.Name, Limit: column.Limit, StatusIDs: column.StatusIDs}
		for _, statusID := range column.StatusIDs {
			placed[statusID] = true
			status, ok := byID[statusID]
			if !ok {
				// A status the project stopped using still stands in the
				// column until someone moves it, and is named by its id
				// rather than hidden.
				status = models.Status{ID: statusID, Name: statusID}
			}
			view.Statuses = append(view.Statuses, status)
		}
		data.Columns = append(data.Columns, view)
	}
	for _, status := range available {
		if !placed[status.ID] {
			data.UnmappedStatuses = append(data.UnmappedStatuses, status)
		}
	}
	for _, choice := range []struct{ value, label string }{
		{"none", "No swimlanes"},
		{"assignee", "Group by assignee"},
		{"epic", "Group by epic"},
		{"project", "Group by project"},
		{"query", "Group by the queries below"},
	} {
		data.SwimlaneChoices = append(data.SwimlaneChoices, boardSwimlaneChoice{
			Value: choice.value, Label: choice.label, Selected: board.SwimlaneStrategy == choice.value,
		})
	}
	// A scrum board estimates with a number field, so those are the choices.
	fields, err := h.Store.CustomFieldsForWorkspace(r.Context(), board.WorkspaceID)
	if err != nil {
		return boardSettingsData{}, err
	}
	for _, field := range fields {
		if field.Type == models.CustomFieldNumber {
			data.EstimationFields = append(data.EstimationFields, field)
		}
	}
	return data, nil
}

// requireBoardAdminPage resolves a board the signed-in person may configure.
// Jira Software lets a board's administrators configure it, as well as the
// administrators of the project it is located in.
func (h *Handler) requireBoardAdminPage(w http.ResponseWriter, r *http.Request, boardID string) (*models.User, string, *models.Board, bool) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return nil, "", nil, false
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), wsID, boardID)
	if err != nil {
		http.NotFound(w, r)
		return nil, "", nil, false
	}
	allowed, err := h.Store.CanAdministerBoard(r.Context(), wsID, user.ID, board)
	if err != nil || !allowed {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil, "", nil, false
	}
	return user, wsID, board, true
}

// AddBoardAdministrator adds a person or a group to a board's administrators.
func (h *Handler) AddBoardAdministrator(w http.ResponseWriter, r *http.Request, boardID string) {
	user, wsID, _, ok := h.requireBoardAdminPage(w, r, boardID)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	err := h.Commands.AddBoardAdmin(r.Context(), user.ID, wsID, boardID, store.BoardAdminInput{
		Type: r.PostFormValue("type"), AccountID: r.PostFormValue("accountId"), GroupID: r.PostFormValue("groupId"),
	})
	redirectBoardSettings(w, r, boardID, err)
}

// RemoveBoardAdministrator takes a board administrator off the board.
func (h *Handler) RemoveBoardAdministrator(w http.ResponseWriter, r *http.Request, boardID, adminID string) {
	user, wsID, _, ok := h.requireBoardAdminPage(w, r, boardID)
	if !ok {
		return
	}
	parsed, convErr := strconv.ParseInt(adminID, 10, 64)
	if convErr != nil {
		http.NotFound(w, r)
		return
	}
	err := h.Commands.DeleteBoardAdmin(r.Context(), user.ID, wsID, boardID, parsed)
	redirectBoardSettings(w, r, boardID, err)
}

// redirectBoardSettings returns to the board settings page, reporting an
// administrator a board could not take.
func redirectBoardSettings(w http.ResponseWriter, r *http.Request, boardID string, err error) {
	target := "/board/" + url.PathEscape(boardID) + "/settings"
	switch {
	case errors.Is(err, store.ErrBoardAdminValidation):
		message := err.Error()
		if _, detail, found := strings.Cut(message, ": "); found {
			message = detail
		}
		target += "?error=" + url.QueryEscape(message)
	case err != nil:
		log.Print("board administrators: update failed: ", strconv.Quote(err.Error()))
		target += "?error=" + url.QueryEscape("The board administrators could not be changed.")
	default:
		target += "?saved=1"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (h *Handler) BoardSettingsPage(w http.ResponseWriter, r *http.Request, boardID string) {
	user, wsID, board, ok := h.requireBoardAdminPage(w, r, boardID)
	if !ok {
		return
	}
	data, err := h.boardSettingsData(r, board, "")
	if err != nil {
		log.Print("board settings: build failed: ", strconv.Quote(err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_board_settings", user, wsID, data, "board", board.ProjectID)
}

func boardConfigurationForm(r *http.Request, board *models.Board) (store.BoardConfigurationUpdate, error) {
	if err := r.ParseForm(); err != nil {
		return store.BoardConfigurationUpdate{}, boardFilterError("invalid form")
	}
	input := store.BoardConfigurationUpdate{
		SwimlaneStrategy: r.PostFormValue("swimlanes"), CardFields: r.PostForm["cardField"],
		FilterJQL: r.PostFormValue("filterJQL"), EstimationFieldID: r.PostFormValue("estimationField"),
	}
	// The named queries a board groups by, kept whichever grouping is in
	// force so choosing another and coming back does not lose them.
	laneNames := r.PostForm["swimlaneName"]
	laneQueries := r.PostForm["swimlaneJQL"]
	if len(laneNames) != len(laneQueries) {
		return input, boardFilterError("swimlane fields are incomplete")
	}
	deletedLanes := map[string]bool{}
	for _, index := range r.PostForm["deleteSwimlane"] {
		deletedLanes[index] = true
	}
	for index := range laneNames {
		if deletedLanes[strconv.Itoa(index)] {
			continue
		}
		input.Swimlanes = append(input.Swimlanes, models.BoardSwimlane{Name: laneNames[index], JQL: laneQueries[index]})
	}
	// The columns arrive as parallel lists in display order -- a key, a name
	// and a limit each -- and every status names the column it stands in.
	// Asking each status once is what keeps a status out of two columns: the
	// form cannot express it.
	deletedColumns := map[string]bool{}
	for _, key := range r.PostForm["deleteColumn"] {
		deletedColumns[key] = true
	}
	placement := map[string][]string{}
	for _, statusID := range r.PostForm["statusID"] {
		key := r.PostFormValue("statusColumn_" + statusID)
		if key == "" || deletedColumns[key] {
			// A status in no column, or in one being deleted, leaves the
			// board rather than following its column into nothing.
			continue
		}
		placement[key] = append(placement[key], statusID)
	}
	columnKeys := r.PostForm["columnKey"]
	columnNames := r.PostForm["columnName"]
	columnLimits := r.PostForm["columnLimit"]
	if len(columnKeys) != len(columnNames) || len(columnKeys) != len(columnLimits) {
		return input, boardFilterError("column fields are incomplete")
	}
	for index, key := range columnKeys {
		// "new" is the row that adds a column; it is read from
		// newColumnName below, so the list of existing columns skips it.
		if key == "new" || deletedColumns[key] {
			continue
		}
		column := models.BoardColumn{Name: columnNames[index], StatusIDs: placement[key]}
		if value := strings.TrimSpace(columnLimits[index]); value != "" {
			limit, err := strconv.Atoi(value)
			if err != nil {
				return input, boardFilterError("column limits must be whole numbers")
			}
			column.Limit = limit
		}
		input.Columns = append(input.Columns, column)
	}
	if name := strings.TrimSpace(r.PostFormValue("newColumnName")); name != "" {
		input.Columns = append(input.Columns, models.BoardColumn{Name: name, StatusIDs: placement["new"]})
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
	user, wsID, board, ok := h.requireBoardAdminPage(w, r, boardID)
	if !ok {
		return
	}
	input, err := boardConfigurationForm(r, board)
	if err == nil {
		// The board the page came from is kept until the save succeeds. A
		// refused configuration answers no board, and assigning that over
		// this one left the rejection path rendering a nil board: every
		// refusal the store made -- an unknown card field, unparseable
		// swimlane JQL -- crashed the handler instead of coming back with
		// the message.
		updated, updateErr := h.Commands.UpdateBoardConfiguration(r.Context(), user.ID, wsID, boardID, input)
		if updateErr == nil {
			board = updated
		}
		err = updateErr
	}
	if errors.Is(err, store.ErrBoardValidation) {
		// The page comes back holding what was typed, so nothing is retyped
		// after a rejected save.
		board.QuickFilters = input.QuickFilters
		board.SwimlaneStrategy = input.SwimlaneStrategy
		board.CardFields = input.CardFields
		if len(input.Columns) > 0 {
			board.Columns = input.Columns
		}
		if input.FilterJQL != "" {
			board.FilterJQL = input.FilterJQL
		}
		board.Swimlanes = input.Swimlanes
		data, dataErr := h.boardSettingsData(r, board, strings.TrimPrefix(err.Error(), store.ErrBoardValidation.Error()+": "))
		if dataErr != nil {
			log.Print("board settings: validation response failed")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.writeWorkspacePageStatus(w, r, "page_board_settings", user, wsID, data, "board", board.ProjectID, http.StatusBadRequest)
		return
	}
	if err != nil {
		log.Print("board settings: update failed: ", strconv.Quote(err.Error()))
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
		http.Error(w, err.Error(), commandErrorStatus(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func boardHasStatus(b *models.Board, statusID string) bool {
	for _, s := range b.StatusIDs() {
		if s == statusID {
			return true
		}
	}
	return false
}
