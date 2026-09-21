package agile

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// rankCustomFieldID is the id of Jira's Rank field, the only rank field a site has.
const rankCustomFieldID int64 = 10019

// maxIssuesPerMove is Jira's limit for issue moves and ranking.
const maxIssuesPerMove = 50

const (
	// epicChildScope selects an epic's standard issues.
	epicChildScope = `i.parent_id = {1} AND it.hierarchy_level = 0`
	// noEpicScope selects standard issues that are not in an epic.
	noEpicScope = `it.hierarchy_level = 0 AND NOT EXISTS (
		SELECT 1 FROM issues epic JOIN issue_types epic_type ON epic_type.id = epic.issuetype_id
		WHERE epic.id = i.parent_id AND epic_type.hierarchy_level = 1)`
)

var epicColors = map[string]bool{
	"color_1": true, "color_2": true, "color_3": true, "color_4": true, "color_5": true, "color_6": true, "color_7": true,
	"color_8": true, "color_9": true, "color_10": true, "color_11": true, "color_12": true, "color_13": true, "color_14": true,
}

// compileJQL compiles an optional JQL filter for the Agile issue reads. Without
// an ORDER BY the issues keep Jira's rank order.
func (h *Handler) compileJQL(ctx context.Context, workspaceID, userID, raw string) (jql.Compiled, error) {
	if strings.TrimSpace(raw) == "" {
		return jql.Compiled{OrderSQL: "i.rank, i.key"}, nil
	}
	query, err := jql.Parse(raw)
	if err != nil {
		return jql.Compiled{}, err
	}
	if err = h.Store.ExpandAppJQL(ctx, workspaceID, query); err != nil {
		return jql.Compiled{}, err
	}
	resolver, err := h.Store.JQLResolver(ctx, workspaceID)
	if err != nil {
		return jql.Compiled{}, err
	}
	compiled := jql.CompileAt(query, userID, resolver, 2)
	if compiled.Err != nil {
		return compiled, compiled.Err
	}
	if query.OrderBy == nil && len(query.Orders) == 0 {
		compiled.OrderSQL = "i.rank, i.key"
	}
	return compiled, nil
}

// pageParams reads Jira's startAt and maxResults paging parameters.
func pageParams(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	startAt, maxResults := 0, 50
	var err error
	if value := r.URL.Query().Get("startAt"); value != "" {
		if startAt, err = strconv.Atoi(value); err != nil || startAt < 0 {
			jiraError(w, http.StatusBadRequest, "startAt must be a non-negative integer.")
			return 0, 0, false
		}
	}
	if value := r.URL.Query().Get("maxResults"); value != "" {
		if maxResults, err = strconv.Atoi(value); err != nil || maxResults < 1 || maxResults > 100 {
			jiraError(w, http.StatusBadRequest, "maxResults must be between 1 and 100.")
			return 0, 0, false
		}
	}
	return startAt, maxResults, true
}

// agileIssueBeans adds Jira Software's fields to issue beans: the open sprint,
// closed sprints, the flag and the epic.
func (h *Handler) agileIssueBeans(ctx context.Context, issues []*models.Issue) ([]map[string]any, error) {
	beans := make([]map[string]any, 0, len(issues))
	if len(issues) == 0 {
		return beans, nil
	}
	issueIDs := make([]string, 0, len(issues))
	parentIDs := []string{}
	for _, issue := range issues {
		issueIDs = append(issueIDs, issue.ID)
		if issue.Parent != nil {
			parentIDs = append(parentIDs, issue.Parent.ID)
		}
	}
	sprints, err := h.Store.SprintsForIssues(ctx, issueIDs)
	if err != nil {
		return nil, err
	}
	epicIDs, err := h.Store.EpicIDs(ctx, parentIDs)
	if err != nil {
		return nil, err
	}
	details, err := h.Store.EpicDetailsForIssues(ctx, issues[0].WorkspaceID, parentIDs)
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		bean := h.IssueBean(issue)
		fields, _ := bean["fields"].(map[string]any)
		if fields == nil {
			fields = map[string]any{}
			bean["fields"] = fields
		}
		var openSprint map[string]any
		closedSprints := []map[string]any{}
		for _, sprint := range sprints[issue.ID] {
			if sprint.State == "closed" {
				closedSprints = append(closedSprints, h.sprintBean(sprint))
			} else {
				openSprint = h.sprintBean(sprint)
			}
		}
		fields["sprint"] = openSprint
		if len(closedSprints) > 0 {
			fields["closedSprints"] = closedSprints
		}
		fields["flagged"] = false
		fields["epic"] = nil
		if issue.Parent != nil && epicIDs[issue.Parent.ID] {
			fields["epic"] = h.epicBean(issue.Parent.JiraID, issue.Parent.Key, issue.Parent.Summary, details[issue.Parent.ID])
		}
		beans = append(beans, bean)
	}
	return beans, nil
}

// epicBean is Jira Software's epic: its name defaults to the summary and its
// color to one of the fourteen epic colors.
func (h *Handler) epicBean(jiraID int64, key, summary string, details store.EpicDetails) map[string]any {
	id := strconv.FormatInt(jiraID, 10)
	name := details.Name
	if name == "" {
		name = summary
	}
	color := details.ColorKey
	if color == "" {
		color = "color_" + strconv.FormatInt(jiraID%14+1, 10)
	}
	return map[string]any{
		"id": jiraID, "key": key, "self": h.BaseURL + "/rest/agile/1.0/epic/" + id,
		"name": name, "summary": summary, "color": map[string]any{"key": color}, "done": details.Done,
	}
}

func (h *Handler) epicByRef(r *http.Request, workspaceID, userID, ref string) (*models.Issue, bool) {
	issue, err := h.visibleIssue(r, workspaceID, userID, ref)
	if err != nil || issue.IssueType.HierarchyLevel != 1 {
		return nil, false
	}
	return issue, true
}

func (h *Handler) writeEpic(w http.ResponseWriter, r *http.Request, workspaceID string, epic *models.Issue) {
	details, err := h.Store.EpicDetailsForIssues(r.Context(), workspaceID, []string{epic.ID})
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, h.epicBean(epic.JiraID, epic.Key, epic.Summary, details[epic.ID]))
}

// scopedIssuePage writes a startAt-paged issue search inside a scope.
func (h *Handler) scopedIssuePage(w http.ResponseWriter, r *http.Request, workspaceID, userID, scope string, scopeArgs []any) {
	startAt, maxResults, ok := pageParams(w, r)
	if !ok {
		return
	}
	compiled, err := h.compileJQL(r.Context(), workspaceID, userID, r.URL.Query().Get("jql"))
	if err != nil {
		jiraError(w, http.StatusBadRequest, jql.QueryMessage(err.Error()))
		return
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
	writeJSON(w, http.StatusOK, map[string]any{
		"expand": "schema,names", "startAt": startAt, "maxResults": maxResults, "total": total, "issues": beans,
	})
}

// scopedIssueScroll writes the token-paged issue search of Jira's enhanced
// /rest/software/1.0 reads.
func (h *Handler) scopedIssueScroll(w http.ResponseWriter, r *http.Request, workspaceID, userID, scope string, scopeArgs []any) {
	query := r.URL.Query()
	maxResults := 50
	if value := query.Get("maxResults"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 {
			jiraError(w, http.StatusBadRequest, "maxResults must be a positive integer.")
			return
		}
		maxResults = min(parsed, 5000)
	}
	offset := 0
	if token := query.Get("nextPageToken"); token != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(token)
		value, found := strings.CutPrefix(string(decoded), "offset:")
		parsed, parseErr := strconv.Atoi(value)
		if err != nil || !found || parseErr != nil || parsed < 0 {
			jiraError(w, http.StatusBadRequest, "Invalid nextPageToken.")
			return
		}
		offset = parsed
	}
	compiled, err := h.compileJQL(r.Context(), workspaceID, userID, query.Get("jql"))
	if err != nil {
		jiraError(w, http.StatusBadRequest, jql.QueryMessage(err.Error()))
		return
	}
	issues, total, err := h.Store.SearchScoped(r.Context(), workspaceID, userID, compiled, scope, scopeArgs, maxResults, offset)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	beans, err := h.agileIssueBeans(r.Context(), issues)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	next := offset + len(issues)
	response := map[string]any{"expand": "schema,names", "isLast": next >= total, "issues": beans}
	if next < total {
		response["nextPageToken"] = base64.RawURLEncoding.EncodeToString([]byte("offset:" + strconv.Itoa(next)))
	}
	writeJSON(w, http.StatusOK, response)
}

// ---- epics ----

func (h *Handler) epicRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	workspaceID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	if len(parts) == 2 && parts[0] == "none" && parts[1] == "issue" {
		switch r.Method {
		case http.MethodGet:
			h.scopedIssuePage(w, r, workspaceID, userID, noEpicScope, nil)
		case http.MethodPost:
			h.removeIssuesFromEpic(w, r, workspaceID, userID)
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if len(parts) == 0 || len(parts) > 2 || parts[0] == "" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	epic, ok := h.epicByRef(r, workspaceID, userID, parts[0])
	if !ok {
		jiraError(w, http.StatusNotFound, "Epic "+parts[0]+" does not exist or you do not have permission to see it.")
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		h.writeEpic(w, r, workspaceID, epic)
	case len(parts) == 1 && r.Method == http.MethodPost:
		h.updateEpic(w, r, workspaceID, userID, epic)
	case len(parts) == 2 && parts[1] == "issue" && r.Method == http.MethodGet:
		h.scopedIssuePage(w, r, workspaceID, userID, epicChildScope, []any{epic.ID})
	case len(parts) == 2 && parts[1] == "issue" && r.Method == http.MethodPost:
		h.moveIssuesToEpic(w, r, workspaceID, userID, epic)
	case len(parts) == 2 && parts[1] == "rank" && r.Method == http.MethodPut:
		h.rankEpic(w, r, workspaceID, userID, epic)
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) updateEpic(w http.ResponseWriter, r *http.Request, workspaceID, userID string, epic *models.Issue) {
	var req struct {
		Name    *string `json:"name"`
		Summary *string `json:"summary"`
		Color   *struct {
			Key string `json:"key"`
		} `json:"color"`
		Done *bool `json:"done"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	if req.Color != nil && !epicColors[req.Color.Key] {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"color": "The color must be one of color_1 to color_14."})
		return
	}
	if req.Name != nil && (strings.TrimSpace(*req.Name) == "" || len(*req.Name) > 255) {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"name": "The epic name is required and must be at most 255 characters."})
		return
	}
	if req.Summary != nil {
		if _, _, err := h.Commands.UpdateIssue(r.Context(), commands.UpdateIssueInput{
			ActorID: userID, WorkspaceID: workspaceID, IssueIDOrKey: epic.ID, Summary: req.Summary,
		}); err != nil {
			if errors.Is(err, commands.ErrPermission) {
				jiraError(w, http.StatusForbidden, err.Error())
				return
			}
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"summary": err.Error()})
			return
		}
	}
	update := store.EpicUpdate{Name: req.Name, Done: req.Done}
	if req.Color != nil {
		update.ColorKey = &req.Color.Key
	}
	if update.Name != nil || update.ColorKey != nil || update.Done != nil {
		if err := h.Commands.UpdateEpicDetails(r.Context(), userID, workspaceID, epic.ID, update); err != nil {
			if errors.Is(err, commands.ErrPermission) {
				jiraError(w, http.StatusForbidden, err.Error())
				return
			}
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	updated, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, epic.ID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.writeEpic(w, r, workspaceID, updated)
}

// decodeIssueRefs reads the {"issues": [...]} list Jira's issue moves take.
func decodeIssueRefs(w http.ResponseWriter, r *http.Request) ([]string, bool) {
	var req struct {
		Issues []string `json:"issues"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Issues) == 0 {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"issues": "At least one issue key or id is required."})
		return nil, false
	}
	if len(req.Issues) > maxIssuesPerMove {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"issues": "At most 50 issues may be moved at once."})
		return nil, false
	}
	return req.Issues, true
}

func (h *Handler) moveIssuesToEpic(w http.ResponseWriter, r *http.Request, workspaceID, userID string, epic *models.Issue) {
	refs, ok := decodeIssueRefs(w, r)
	if !ok {
		return
	}
	issues := make([]*models.Issue, 0, len(refs))
	for _, ref := range refs {
		issue, err := h.visibleIssue(r, workspaceID, userID, ref)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "Issue "+ref+" does not exist or you do not have permission to see it.")
			return
		}
		if issue.IssueType.HierarchyLevel != 0 {
			jiraError(w, http.StatusBadRequest, commands.ErrParentHierarchy.Error())
			return
		}
		issues = append(issues, issue)
	}
	for _, issue := range issues {
		if _, _, err := h.Commands.UpdateIssue(r.Context(), commands.UpdateIssueInput{
			ActorID: userID, WorkspaceID: workspaceID, IssueIDOrKey: issue.ID, ParentIDOrKey: &epic.Key,
		}); err != nil {
			jiraError(w, commandStatus(err), err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) removeIssuesFromEpic(w http.ResponseWriter, r *http.Request, workspaceID, userID string) {
	refs, ok := decodeIssueRefs(w, r)
	if !ok {
		return
	}
	issues := make([]*models.Issue, 0, len(refs))
	parentIDs := []string{}
	for _, ref := range refs {
		issue, err := h.visibleIssue(r, workspaceID, userID, ref)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "Issue "+ref+" does not exist or you do not have permission to see it.")
			return
		}
		issues = append(issues, issue)
		if issue.Parent != nil {
			parentIDs = append(parentIDs, issue.Parent.ID)
		}
	}
	epicIDs, err := h.Store.EpicIDs(r.Context(), parentIDs)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	clear := ""
	for _, issue := range issues {
		if issue.Parent == nil || !epicIDs[issue.Parent.ID] {
			continue
		}
		if _, _, err := h.Commands.UpdateIssue(r.Context(), commands.UpdateIssueInput{
			ActorID: userID, WorkspaceID: workspaceID, IssueIDOrKey: issue.ID, ParentIDOrKey: &clear,
		}); err != nil {
			jiraError(w, commandStatus(err), err.Error())
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// commandStatus is the status Jira Software gives a refused command: 403 for a
// missing permission, 400 otherwise.
func commandStatus(err error) int {
	if errors.Is(err, commands.ErrPermission) {
		return http.StatusForbidden
	}
	return http.StatusBadRequest
}

func validRankField(value *int64) bool {
	return value == nil || *value == rankCustomFieldID
}

func (h *Handler) rankEpic(w http.ResponseWriter, r *http.Request, workspaceID, userID string, epic *models.Issue) {
	var req struct {
		RankBeforeEpic    string `json:"rankBeforeEpic"`
		RankAfterEpic     string `json:"rankAfterEpic"`
		RankCustomFieldID *int64 `json:"rankCustomFieldId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	if (req.RankBeforeEpic == "") == (req.RankAfterEpic == "") {
		jiraError(w, http.StatusBadRequest, "Specify exactly one of rankBeforeEpic or rankAfterEpic.")
		return
	}
	if !validRankField(req.RankCustomFieldID) {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"rankCustomFieldId": "The rank custom field does not exist."})
		return
	}
	ref := req.RankBeforeEpic + req.RankAfterEpic
	reference, ok := h.epicByRef(r, workspaceID, userID, ref)
	if !ok {
		jiraError(w, http.StatusNotFound, "Epic "+ref+" does not exist or you do not have permission to see it.")
		return
	}
	if reference.ID == epic.ID {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	beforeID, afterID := "", ""
	if req.RankBeforeEpic != "" {
		beforeID = reference.ID
	} else {
		afterID = reference.ID
	}
	if err := h.Commands.RankAround(r.Context(), userID, workspaceID, epic.ID, beforeID, afterID); err != nil {
		if errors.Is(err, commands.ErrPermission) {
			jiraError(w, http.StatusForbidden, err.Error())
			return
		}
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) boardEpics(w http.ResponseWriter, r *http.Request, board *models.Board, workspaceID, userID string) {
	startAt, maxResults, ok := pageParams(w, r)
	if !ok {
		return
	}
	done := r.URL.Query().Get("done")
	if done != "" && done != "true" && done != "false" {
		jiraError(w, http.StatusBadRequest, "done must be true or false.")
		return
	}
	epics, err := h.Store.EpicsInProjects(r.Context(), workspaceID, userID, []string{board.ProjectID})
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	epicIDs := make([]string, 0, len(epics))
	for _, epic := range epics {
		epicIDs = append(epicIDs, epic.ID)
	}
	details, err := h.Store.EpicDetailsForIssues(r.Context(), workspaceID, epicIDs)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	values := []map[string]any{}
	for _, epic := range epics {
		if done != "" && strconv.FormatBool(details[epic.ID].Done) != done {
			continue
		}
		values = append(values, h.epicBean(epic.JiraID, epic.Key, epic.Summary, details[epic.ID]))
	}
	total := len(values)
	startAt = min(startAt, total)
	end := min(startAt+maxResults, total)
	writeJSON(w, http.StatusOK, map[string]any{
		"maxResults": maxResults, "startAt": startAt, "total": total, "isLast": end == total, "values": values[startAt:end],
	})
}

func (h *Handler) boardEpicIssues(w http.ResponseWriter, r *http.Request, board *models.Board, workspaceID, userID, epicRef string, scroll bool) {
	if epicRef == "none" {
		scope, args := noEpicScope+" AND i.project_id = {1}", []any{board.ProjectID}
		if scroll {
			h.scopedIssueScroll(w, r, workspaceID, userID, scope, args)
		} else {
			h.scopedIssuePage(w, r, workspaceID, userID, scope, args)
		}
		return
	}
	epic, ok := h.epicByRef(r, workspaceID, userID, epicRef)
	if !ok {
		jiraError(w, http.StatusNotFound, "The epic does not exist.")
		return
	}
	scope, args := epicChildScope+" AND i.project_id = {2}", []any{epic.ID, board.ProjectID}
	if scroll {
		h.scopedIssueScroll(w, r, workspaceID, userID, scope, args)
	} else {
		h.scopedIssuePage(w, r, workspaceID, userID, scope, args)
	}
}

// softwareEpicRoute serves the enhanced /rest/software/1.0 epic issue reads.
func (h *Handler) softwareEpicRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) != 2 || parts[1] != "issue" || r.Method != http.MethodGet {
		jiraError(w, http.StatusNotFound, "No resource found for path "+r.URL.Path)
		return
	}
	workspaceID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	if parts[0] == "none" {
		h.scopedIssueScroll(w, r, workspaceID, userID, noEpicScope, nil)
		return
	}
	epic, ok := h.epicByRef(r, workspaceID, userID, parts[0])
	if !ok {
		jiraError(w, http.StatusNotFound, "Epic "+parts[0]+" does not exist or you do not have permission to see it.")
		return
	}
	h.scopedIssueScroll(w, r, workspaceID, userID, epicChildScope, []any{epic.ID})
}

// ---- ranking and moves ----

type rankRequest struct {
	Issues            []string `json:"issues"`
	RankBeforeIssue   string   `json:"rankBeforeIssue"`
	RankAfterIssue    string   `json:"rankAfterIssue"`
	RankCustomFieldID *int64   `json:"rankCustomFieldId"`
}

type rankEntry struct {
	IssueID  int64    `json:"issueId,omitempty"`
	IssueKey string   `json:"issueKey"`
	Status   int      `json:"status"`
	Errors   []string `json:"errors,omitempty"`
}

func decodeRankRequest(w http.ResponseWriter, r *http.Request, rankRequired bool) (rankRequest, bool) {
	var req rankRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return req, false
	}
	switch {
	case len(req.Issues) == 0:
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"issues": "At least one issue key or id is required."})
	case len(req.Issues) > maxIssuesPerMove:
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"issues": "At most 50 issues may be ranked at once."})
	case rankRequired && req.RankBeforeIssue == "" && req.RankAfterIssue == "":
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"rankBeforeIssue": "Either rankBeforeIssue or rankAfterIssue is required."})
	case req.RankBeforeIssue != "" && req.RankAfterIssue != "":
		jiraError(w, http.StatusBadRequest, "Specify only one of rankBeforeIssue or rankAfterIssue.")
	case !validRankField(req.RankCustomFieldID):
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"rankCustomFieldId": "The rank custom field does not exist."})
	default:
		return req, true
	}
	return req, false
}

// applyIssueMoves runs a move for each issue and then ranks it around the
// reference, keeping the request's order. Every issue gets an entry; the
// response is 204 when all succeed and 207 with the entries otherwise.
func (h *Handler) applyIssueMoves(w http.ResponseWriter, r *http.Request, workspaceID, userID string, req rankRequest, move func(*models.Issue) (int, string)) {
	beforeID, afterID := "", ""
	if ref := req.RankBeforeIssue + req.RankAfterIssue; ref != "" {
		reference, err := h.visibleIssue(r, workspaceID, userID, ref)
		if err != nil {
			jiraError(w, http.StatusNotFound, "Issue "+ref+" does not exist or you do not have permission to see it.")
			return
		}
		if req.RankBeforeIssue != "" {
			beforeID = reference.ID
		} else {
			afterID = reference.ID
		}
	}
	entries := make([]rankEntry, len(req.Issues))
	failed := false
	process := func(index int) {
		ref := req.Issues[index]
		issue, err := h.visibleIssue(r, workspaceID, userID, ref)
		if err != nil {
			entries[index] = rankEntry{IssueKey: ref, Status: http.StatusNotFound, Errors: []string{"Issue does not exist or you do not have permission to see it."}}
			failed = true
			return
		}
		entry := rankEntry{IssueID: issue.JiraID, IssueKey: issue.Key, Status: http.StatusOK}
		if move != nil {
			if status, message := move(issue); status != 0 {
				entry.Status, entry.Errors = status, []string{message}
				entries[index] = entry
				failed = true
				return
			}
		}
		if beforeID != "" || afterID != "" {
			if issue.ID == beforeID || issue.ID == afterID {
				entry.Status, entry.Errors = http.StatusBadRequest, []string{"An issue cannot be ranked relative to itself."}
				entries[index] = entry
				failed = true
				return
			}
			if rankErr := h.Commands.RankAround(r.Context(), userID, workspaceID, issue.ID, beforeID, afterID); errors.Is(rankErr, commands.ErrPermission) {
				entry.Status, entry.Errors = http.StatusForbidden, []string{rankErr.Error()}
				entries[index] = entry
				failed = true
				return
			} else if rankErr != nil {
				entry.Status, entry.Errors = http.StatusInternalServerError, []string{"The issue could not be ranked."}
				entries[index] = entry
				failed = true
				return
			}
			if beforeID != "" {
				beforeID = issue.ID
			} else {
				afterID = issue.ID
			}
		}
		entries[index] = entry
	}
	if beforeID != "" {
		for index := len(req.Issues) - 1; index >= 0; index-- {
			process(index)
		}
	} else {
		for index := range req.Issues {
			process(index)
		}
	}
	if failed {
		writeJSON(w, http.StatusMultiStatus, map[string]any{"entries": entries})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// rankIssues implements PUT /rest/agile/1.0/issue/rank.
func (h *Handler) rankIssues(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	req, ok := decodeRankRequest(w, r, true)
	if !ok {
		return
	}
	h.applyIssueMoves(w, r, workspaceID, userID, req, nil)
}

// moveIssuesToBoard moves backlog issues onto the board: a scrum board shows
// its active sprint, and every kanban board issue is already on the board.
func (h *Handler) moveIssuesToBoard(w http.ResponseWriter, r *http.Request, workspaceID, userID string, board *models.Board) {
	req, ok := decodeRankRequest(w, r, false)
	if !ok {
		return
	}
	var activeSprint *models.Sprint
	if board.Type == "scrum" {
		sprint, err := h.Store.ActiveSprintForBoard(r.Context(), board.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		activeSprint = sprint
	}
	h.applyIssueMoves(w, r, workspaceID, userID, req, func(issue *models.Issue) (int, string) {
		if issue.ProjectID != board.ProjectID {
			return http.StatusBadRequest, "The issue does not belong to the board."
		}
		if board.Type != "scrum" {
			return 0, ""
		}
		if activeSprint == nil {
			return http.StatusBadRequest, "The board has no active sprint."
		}
		if err := h.Commands.PlanIssue(r.Context(), userID, workspaceID, board.ID, issue.ID, activeSprint.ID, "", ""); err != nil {
			return commandStatus(err), err.Error()
		}
		return 0, ""
	})
}

func (h *Handler) moveIssuesToBacklogForBoard(w http.ResponseWriter, r *http.Request, boardRef string) {
	workspaceID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), workspaceID, boardRef)
	if err != nil {
		jiraError(w, http.StatusNotFound, "The board does not exist.")
		return
	}
	req, ok := decodeRankRequest(w, r, false)
	if !ok {
		return
	}
	h.applyIssueMoves(w, r, workspaceID, userID, req, func(issue *models.Issue) (int, string) {
		if issue.ProjectID != board.ProjectID {
			return http.StatusBadRequest, "The issue does not belong to the board."
		}
		if err := h.Commands.MoveIssueToBacklog(r.Context(), userID, workspaceID, issue.ID); err != nil {
			return commandStatus(err), err.Error()
		}
		return 0, ""
	})
}

// ---- issues and estimation ----

func (h *Handler) agileIssueRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	workspaceID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	if len(parts) == 0 || len(parts) > 2 || parts[0] == "" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	issue, err := h.visibleIssue(r, workspaceID, userID, parts[0])
	if err != nil {
		jiraError(w, http.StatusNotFound, "Issue does not exist or you do not have permission to see it.")
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		beans, err := h.agileIssueBeans(r.Context(), []*models.Issue{issue})
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, beans[0])
	case len(parts) == 2 && parts[1] == "estimation" && (r.Method == http.MethodGet || r.Method == http.MethodPut):
		h.issueEstimation(w, r, workspaceID, userID, issue)
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func estimationValue(raw json.RawMessage) any {
	var value float64
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}

func (h *Handler) issueEstimation(w http.ResponseWriter, r *http.Request, workspaceID, userID string, issue *models.Issue) {
	boardRef := r.URL.Query().Get("boardId")
	if boardRef == "" {
		jiraError(w, http.StatusBadRequest, "The boardId query parameter is required.")
		return
	}
	board, err := h.Store.BoardByIDInWorkspace(r.Context(), workspaceID, boardRef)
	if err != nil {
		jiraError(w, http.StatusNotFound, "The board does not exist or you do not have permission to see it.")
		return
	}
	if issue.ProjectID != board.ProjectID {
		jiraError(w, http.StatusNotFound, "The issue does not belong to the board.")
		return
	}
	if board.EstimationFieldID == "" {
		jiraError(w, http.StatusBadRequest, "The board does not estimate issues with a field.")
		return
	}
	fieldID := board.EstimationFieldID
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"fieldId": fieldID, "value": estimationValue(issue.Fields[fieldID])})
		return
	}
	var req struct {
		Value *string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	raw := json.RawMessage("null")
	if req.Value != nil && strings.TrimSpace(*req.Value) != "" {
		number, parseErr := strconv.ParseFloat(strings.TrimSpace(*req.Value), 64)
		if parseErr != nil {
			jiraError(w, http.StatusBadRequest, "The estimation value must be a number.")
			return
		}
		raw = json.RawMessage(strconv.FormatFloat(number, 'f', -1, 64))
	}
	updated, _, err := h.Commands.UpdateIssue(r.Context(), commands.UpdateIssueInput{
		ActorID: userID, WorkspaceID: workspaceID, IssueIDOrKey: issue.ID, Fields: map[string]json.RawMessage{fieldID: raw},
	})
	if err != nil {
		jiraError(w, commandStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fieldId": fieldID, "value": estimationValue(updated.Fields[fieldID])})
}

// ---- boards ----

// filterProject returns the project a filter's query is limited to, when its
// top-level terms name exactly one.
func filterProject(raw string) string {
	query, err := jql.Parse(raw)
	if err != nil {
		return ""
	}
	terms := []jql.Node{query.Root}
	if and, ok := query.Root.(jql.And); ok {
		terms = and.Terms
	}
	for _, term := range terms {
		clause, ok := term.(jql.Clause)
		if ok && strings.EqualFold(clause.Field, "project") && (clause.Op == "=" || clause.Op == "in") && len(clause.Values) == 1 {
			return clause.Values[0]
		}
	}
	return ""
}

func (h *Handler) createBoard(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	var req struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		FilterID *int64 `json:"filterId"`
		Location *struct {
			Type           string `json:"type"`
			ProjectKeyOrID string `json:"projectKeyOrId"`
		} `json:"location"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	fieldErrors := map[string]string{}
	if strings.TrimSpace(req.Name) == "" {
		fieldErrors["name"] = "The board name is required."
	}
	if req.Type != "scrum" && req.Type != "kanban" {
		fieldErrors["type"] = "The board type must be scrum or kanban."
	}
	if req.FilterID == nil {
		fieldErrors["filterId"] = "The filter id is required."
	}
	if len(fieldErrors) > 0 {
		jiraFieldError(w, http.StatusBadRequest, fieldErrors)
		return
	}
	filter, err := h.Store.FilterByID(r.Context(), workspaceID, userID, strconv.FormatInt(*req.FilterID, 10))
	if err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"filterId": "The filter does not exist or you do not have permission to view it."})
		return
	}
	projectRef := ""
	switch {
	case req.Location != nil && req.Location.Type == "project":
		if req.Location.ProjectKeyOrID == "" {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"location": "A project location needs projectKeyOrId."})
			return
		}
		projectRef = req.Location.ProjectKeyOrID
	case req.Location == nil || req.Location.Type == "user" || req.Location.Type == "":
		if req.Location != nil && req.Location.ProjectKeyOrID != "" {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"location": "A user location does not take projectKeyOrId."})
			return
		}
		if projectRef = filterProject(filter.JQL); projectRef == "" {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"location": "Boards are located in a project: give a project location or a filter limited to one project."})
			return
		}
	default:
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"location": "The location type must be project or user."})
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectRef)
	if err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"location": "The project " + projectRef + " does not exist."})
		return
	}
	board, err := h.Store.CreateBoard(r.Context(), userID, workspaceID, store.BoardCreate{
		Name: req.Name, Type: req.Type, ProjectID: project.ID, Filter: filter,
	})
	if errors.Is(err, store.ErrBoardValidation) {
		jiraError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), store.ErrBoardValidation.Error()+": "))
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, h.boardBean(board))
}

func (h *Handler) deleteBoard(w http.ResponseWriter, r *http.Request, workspaceID, userID string, board *models.Board) {
	admin, err := authz.IsWorkspaceAdmin(r.Context(), h.Store, workspaceID, userID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !admin {
		jiraError(w, http.StatusForbidden, "You do not have permission to delete this board.")
		return
	}
	if err := h.Store.DeleteBoard(r.Context(), userID, workspaceID, board.ID); err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) boardsByFilter(w http.ResponseWriter, r *http.Request, filterRef string) {
	workspaceID, _, status, msg := h.authWorkspace(r)
	if status != 0 {
		jiraError(w, status, msg)
		return
	}
	if _, err := strconv.ParseInt(filterRef, 10, 64); err != nil {
		jiraError(w, http.StatusBadRequest, "The filter id must be a number.")
		return
	}
	startAt, maxResults, ok := pageParams(w, r)
	if !ok {
		return
	}
	boards, err := h.Store.BoardsUsingFilter(r.Context(), workspaceID, filterRef)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	total := len(boards)
	startAt = min(startAt, total)
	end := min(startAt+maxResults, total)
	values := make([]map[string]any, 0, end-startAt)
	for _, board := range boards[startAt:end] {
		values = append(values, map[string]any{
			"id": board.JiraID, "self": h.BaseURL + "/rest/agile/1.0/board/" + boardWireID(board), "name": board.Name,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"maxResults": maxResults, "startAt": startAt, "total": total, "isLast": end == total, "values": values,
	})
}
