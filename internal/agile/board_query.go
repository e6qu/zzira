package agile

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// splitValues reads a comma separated, possibly repeated query parameter.
func splitValues(r *http.Request, name string) []string {
	values := []string{}
	for _, raw := range r.URL.Query()[name] {
		for _, value := range strings.Split(raw, ",") {
			if value = strings.TrimSpace(value); value != "" {
				values = append(values, value)
			}
		}
	}
	return values
}

func queryFlag(w http.ResponseWriter, r *http.Request, name string, fallback bool) (bool, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		jiraError(w, http.StatusBadRequest, name+" must be true or false.")
		return false, false
	}
	return value, true
}

// canBrowseBoard reports whether the caller may see the board's project.
func (h *Handler) canBrowseBoard(r *http.Request, workspaceID, userID string, board *models.Board) bool {
	allowed, err := h.Store.HasProjectPermission(r.Context(), workspaceID, userID, board.ProjectID, "", "BROWSE_PROJECTS")
	return err == nil && allowed
}

// boardAdminsBean reports the users and groups who administer a board. A board
// with no administrators of its own falls back to the lead of the project it is
// located in, who administers it.
func (h *Handler) boardAdminsBean(r *http.Request, workspaceID string, board *models.Board) map[string]any {
	users, groups := []map[string]any{}, []map[string]any{}
	admins, err := h.Store.BoardAdmins(r.Context(), board.ID)
	if err == nil {
		for _, admin := range admins {
			if admin.Type == "group" {
				groups = append(groups, map[string]any{
					"name": admin.GroupName,
					"self": h.BaseURL + "/rest/api/3/group?groupName=" + url.QueryEscape(admin.GroupName),
				})
				continue
			}
			users = append(users, map[string]any{
				"accountId": admin.AccountID, "displayName": admin.UserName, "active": true,
				"self": h.BaseURL + "/rest/api/3/user?accountId=" + admin.AccountID,
			})
		}
	}
	if len(users) == 0 && len(groups) == 0 {
		if project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, board.ProjectID); err == nil && project.LeadAccountID != "" {
			if lead, err := h.Store.UserByID(r.Context(), project.LeadAccountID); err == nil {
				users = append(users, map[string]any{
					"accountId": lead.ID, "displayName": lead.DisplayName, "active": true,
					"self": h.BaseURL + "/rest/api/3/user?accountId=" + lead.ID,
				})
			}
		}
	}
	return map[string]any{"users": users, "groups": groups}
}

// listBoardsFiltered serves GET /rest/agile/1.0/board with Jira's filters.
func (h *Handler) listBoardsFiltered(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	query := r.URL.Query()
	startAt, maxResults := 0, 50
	if raw := query.Get("startAt"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			jiraError(w, http.StatusBadRequest, "startAt must be a non-negative integer.")
			return
		}
		startAt = parsed
	}
	if raw := query.Get("maxResults"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			jiraError(w, http.StatusBadRequest, "maxResults must be a positive integer.")
			return
		}
		maxResults = min(parsed, 50)
	}
	types := map[string]bool{}
	for _, kind := range splitValues(r, "type") {
		if kind != "scrum" && kind != "kanban" && kind != "simple" {
			jiraError(w, http.StatusBadRequest, "The board type "+kind+" is not valid. Valid values: scrum, kanban, simple.")
			return
		}
		types[kind] = true
	}
	projectTypes := map[string]bool{}
	for _, kind := range splitValues(r, "projectTypeLocation") {
		if kind != "software" && kind != "service_desk" {
			jiraError(w, http.StatusBadRequest, "The project type "+kind+" is not valid. Valid values: software, service_desk.")
			return
		}
		projectTypes[kind] = true
	}
	if len(projectTypes) == 0 {
		projectTypes["software"] = true
	}
	orderBy := query.Get("orderBy")
	if orderBy != "" && orderBy != "name" && orderBy != "+name" && orderBy != "-name" {
		jiraError(w, http.StatusBadRequest, "orderBy must be name, +name or -name.")
		return
	}
	negate, ok := queryFlag(w, r, "negateLocationFiltering", false)
	if !ok {
		return
	}
	if _, ok = queryFlag(w, r, "includePrivate", false); !ok {
		return
	}
	expand := map[string]bool{}
	for _, option := range splitValues(r, "expand") {
		expand[option] = true
	}
	var filterID *int64
	if raw := query.Get("filterId"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "filterId must be a number.")
			return
		}
		filterID = &parsed
	}
	locationProject := ""
	locationFiltered := false
	for _, name := range []string{"projectKeyOrId", "projectLocation"} {
		if ref := strings.TrimSpace(query.Get(name)); ref != "" {
			project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, ref)
			if err != nil {
				jiraError(w, http.StatusBadRequest, "No project could be found with key or id '"+ref+"'.")
				return
			}
			if locationProject != "" && locationProject != project.ID {
				locationProject = "-"
			} else {
				locationProject = project.ID
			}
			locationFiltered = true
		}
	}
	accountLocation := strings.TrimSpace(query.Get("accountIdLocation")) != ""
	if accountLocation {
		locationFiltered = true
	}
	name := strings.ToLower(strings.TrimSpace(query.Get("name")))

	boards, err := h.Store.BoardsByWorkspace(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	matched := []*models.Board{}
	for _, board := range boards {
		if !projectTypes[board.ProjectTypeKey] || (len(types) > 0 && !types[board.Type]) {
			continue
		}
		if name != "" && !strings.Contains(strings.ToLower(board.Name), name) {
			continue
		}
		if filterID != nil && board.FilterJiraID != *filterID && board.SourceFilterJiraID != *filterID {
			continue
		}
		if locationFiltered {
			// Boards are located in projects; none is located in a person.
			inLocation := !accountLocation && board.ProjectID == locationProject
			if inLocation == negate {
				continue
			}
		}
		if !h.canBrowseBoard(r, workspaceID, userID, board) {
			continue
		}
		matched = append(matched, board)
	}
	if orderBy != "" {
		sort.SliceStable(matched, func(i, j int) bool {
			left, right := strings.ToLower(matched[i].Name), strings.ToLower(matched[j].Name)
			if orderBy == "-name" {
				return left > right
			}
			return left < right
		})
	}
	start := min(startAt, len(matched))
	end := min(start+maxResults, len(matched))
	values := make([]map[string]any, 0, end-start)
	for _, board := range matched[start:end] {
		bean := h.boardBean(board)
		if expand["admins"] {
			bean["admins"] = h.boardAdminsBean(r, workspaceID, board)
		}
		if expand["permissions"] {
			canEdit, err := h.Store.HasProjectPermission(r.Context(), workspaceID, userID, board.ProjectID, "", "ADMINISTER_PROJECTS")
			bean["canEdit"] = err == nil && canEdit
		}
		values = append(values, bean)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"maxResults": maxResults, "startAt": startAt, "total": len(matched), "isLast": end >= len(matched), "values": values,
	})
}

var sprintStateOrder = map[string]int{"closed": 0, "active": 1, "future": 2}

// boardSprintsFiltered serves GET /rest/agile/1.0/board/{boardId}/sprint.
func (h *Handler) boardSprintsFiltered(w http.ResponseWriter, r *http.Request, board *models.Board) {
	startAt, maxResults := 0, 50
	query := r.URL.Query()
	if raw := query.Get("startAt"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			jiraError(w, http.StatusBadRequest, "startAt must be a non-negative integer.")
			return
		}
		startAt = parsed
	}
	if raw := query.Get("maxResults"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			jiraError(w, http.StatusBadRequest, "maxResults must be a positive integer.")
			return
		}
		maxResults = min(parsed, 50)
	}
	states := map[string]bool{}
	for _, state := range splitValues(r, "state") {
		if _, known := sprintStateOrder[state]; !known {
			jiraError(w, http.StatusBadRequest, "The sprint state "+state+" is not valid. Valid values: future, active, closed.")
			return
		}
		states[state] = true
	}
	sprints, err := h.Store.SprintsByBoard(r.Context(), board.ID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	matched := []*models.Sprint{}
	for _, sprint := range sprints {
		if len(states) == 0 || states[sprint.State] {
			matched = append(matched, sprint)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool { return sprintStateOrder[matched[i].State] < sprintStateOrder[matched[j].State] })
	start := min(startAt, len(matched))
	end := min(start+maxResults, len(matched))
	values := make([]map[string]any, 0, end-start)
	for _, sprint := range matched[start:end] {
		values = append(values, h.sprintBean(sprint))
	}
	writeJSON(w, http.StatusOK, map[string]any{"maxResults": maxResults, "startAt": startAt, "isLast": end >= len(matched), "values": values})
}

// ---- issue reads ----

// projectAgileIssue keeps the fields a client asked for; by default every
// navigable and Agile field.
func projectAgileIssue(bean map[string]any, requested []string) map[string]any {
	if len(requested) == 0 {
		return bean
	}
	all, positive := false, false
	include, exclude := map[string]bool{}, map[string]bool{}
	for _, field := range requested {
		if strings.HasPrefix(field, "-") {
			exclude[strings.TrimPrefix(field, "-")] = true
			continue
		}
		positive = true
		if field == "*all" || field == "*navigable" {
			all = true
		} else {
			include[field] = true
		}
	}
	if !positive {
		all = true
	}
	out := map[string]any{}
	for key, value := range bean {
		if key != "fields" {
			out[key] = value
		}
	}
	fields := map[string]any{}
	if source, ok := bean["fields"].(map[string]any); ok {
		for field, value := range source {
			if (all || include[field]) && !exclude[field] {
				fields[field] = value
			}
		}
	}
	out["fields"] = fields
	return out
}

// agileIssueSearch serves the Agile issue reads inside a scope with Jira's jql,
// validateQuery, fields and expand parameters. defaultOrder replaces rank order
// when the query has no ORDER BY.
func (h *Handler) agileIssueSearch(w http.ResponseWriter, r *http.Request, workspaceID, userID, scope string, scopeArgs []any, defaultOrder string) {
	startAt, maxResults, ok := pageParams(w, r)
	if !ok {
		return
	}
	validate, ok := queryFlag(w, r, "validateQuery", true)
	if !ok {
		return
	}
	requested := splitValues(r, "fields")
	compiled, err := h.compileJQL(r.Context(), workspaceID, userID, r.URL.Query().Get("jql"))
	if err != nil {
		if validate {
			jiraError(w, http.StatusBadRequest, "Error in the JQL Query: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"expand": "schema,names", "startAt": startAt, "maxResults": maxResults, "total": 0, "issues": []any{},
			"warningMessages": []string{"Error in the JQL Query: " + err.Error()},
		})
		return
	}
	if defaultOrder != "" && compiled.OrderSQL == "i.rank, i.key" {
		compiled.OrderSQL = defaultOrder
	}
	issues, total, err := h.Store.SearchScoped(r.Context(), workspaceID, userID, compiled, scope, scopeArgs, maxResults, startAt)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	beans, err := h.agileIssueBeans(r.Context(), issues)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	projected := make([]map[string]any, 0, len(beans))
	for _, bean := range beans {
		projected = append(projected, projectAgileIssue(bean, requested))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"expand": "schema,names", "startAt": startAt, "maxResults": maxResults, "total": total, "issues": projected,
	})
}

const (
	// boardIssueScope is the board's project work in a status mapped to one of
	// its columns; a scrum board leaves out epics.
	boardIssueScope = `i.project_id = {1} AND i.status_id = ANY({2}) AND ({3} <> 'scrum' OR it.hierarchy_level <> 1)`
	// backlogScope is the board's project work in no active or future sprint.
	backlogScope = `i.project_id = {1} AND NOT EXISTS (
		SELECT 1 FROM sprint_issues si JOIN sprints s ON s.id = si.sprint_id
		WHERE si.issue_id = i.id AND s.state IN ('future', 'active'))`
	// sprintScope is the work in one sprint, kept in the sprint's order.
	sprintScope      = `EXISTS (SELECT 1 FROM sprint_issues si WHERE si.issue_id = i.id AND si.sprint_id = {1})`
	sprintOrder      = `(SELECT si.rank FROM sprint_issues si WHERE si.issue_id = i.id AND si.sprint_id = {1}), i.key`
	boardSprintScope = `i.project_id = {2} AND EXISTS (SELECT 1 FROM sprint_issues si WHERE si.issue_id = i.id AND si.sprint_id = {1})`
)

// moveIssuesToSprintRanked serves POST /rest/agile/1.0/sprint/{sprintId}/issue:
// up to 50 issues of the sprint's project move into an open sprint and are
// ranked around a reference issue when one is given.
func (h *Handler) moveIssuesToSprintRanked(w http.ResponseWriter, r *http.Request, workspaceID string, sprint *models.Sprint) {
	_, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	req, ok := decodeRankRequest(w, r, false)
	if !ok {
		return
	}
	if sprint.State != "future" && sprint.State != "active" {
		jiraError(w, http.StatusBadRequest, "Issues can only be moved to open or active sprints.")
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), workspaceID, sprint.BoardID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	issues := make([]*models.Issue, 0, len(req.Issues))
	seen := map[string]bool{}
	for _, ref := range req.Issues {
		issue, err := h.visibleIssue(r, workspaceID, userID, ref)
		if err != nil {
			jiraError(w, http.StatusNotFound, "Issue "+ref+" does not exist or you do not have permission to see it.")
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
		if err := h.Commands.PlanIssue(r.Context(), userID, workspaceID, board.ID, issue.ID, sprint.ID, "", ""); err != nil {
			jiraError(w, commandStatus(err), err.Error())
			return
		}
	}
	if req.RankBeforeIssue == "" && req.RankAfterIssue == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.applyIssueMoves(w, r, workspaceID, userID, req, nil)
}
