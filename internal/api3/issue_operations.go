package api3

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// Issue operations beyond a single issue's CRUD: bulk reads and writes,
// changelogs, the picker, notifications, events and limit reports, and the
// detail fields an issue shows.

// addIssueDetailFields fills the fields an issue carries that live outside the
// issue row. Each is loaded only when the projection asks for it: always for
// named fields, and for every field when details are requested with *all.
func (h *Handler) addIssueDetailFields(ctx context.Context, workspaceID, readerID string, issue *models.Issue, fields map[string]any, requested []string, details, defaultAll bool) error {
	all, include, exclude := requestedFieldSet(requested, defaultAll)
	want := func(field string) bool {
		return ((details && all) || include[field]) && !exclude[field]
	}
	if want("creator") {
		if creatorID, err := h.Store.IssueCreatorID(ctx, workspaceID, issue.ID); err == nil {
			if creator, userErr := h.Store.SiteUser(ctx, workspaceID, creatorID); userErr == nil {
				fields["creator"] = h.fullUserBean(creator, creatorID == readerID)
			}
		}
	}
	if want("comment") {
		comments, err := h.Store.CommentsByIssue(ctx, issue.ID)
		if err != nil {
			return err
		}
		renderer := h.newCommentRendererContext(ctx, workspaceID, readerID, "")
		beans := []map[string]any{}
		for _, c := range comments {
			if visible, visErr := h.Store.CommentVisibleTo(ctx, workspaceID, issue.ProjectID, readerID, c); visErr == nil && visible {
				beans = append(beans, renderer.bean(issue, c))
			}
		}
		fields["comment"] = map[string]any{"comments": beans, "self": h.BaseURL + "/rest/api/3/issue/" + jiraIssueID(issue) + "/comment", "maxResults": len(beans), "total": len(beans), "startAt": 0}
	}
	visible := func(id string) *models.Issue {
		other, err := h.Store.IssueByIDOrKey(ctx, workspaceID, id)
		if err != nil {
			return nil
		}
		if ok, visErr := authz.CanSeeIssue(ctx, h.Store, workspaceID, other.ProjectID, readerID, other.ID, other.SecurityLevelID); visErr != nil || !ok {
			return nil
		}
		return other
	}
	if want("issuelinks") {
		links, err := h.Store.LinksByIssue(ctx, issue.ID)
		if err != nil {
			return err
		}
		beans := []map[string]any{}
		for _, link := range links {
			otherID := link.InwardID
			if link.InwardID == issue.ID {
				otherID = link.OutwardID
			}
			if other := visible(otherID); other != nil {
				beans = append(beans, h.issueLinkBeanFor(link, issue, other))
			}
		}
		fields["issuelinks"] = beans
	}
	if want("watches") {
		watchers, err := h.Store.WatchersByIssue(ctx, issue.ID)
		if err != nil {
			return err
		}
		fields["watches"] = map[string]any{"self": h.BaseURL + "/rest/api/3/issue/" + issue.Key + "/watchers", "watchCount": len(watchers), "isWatching": containsString(watchers, readerID)}
	}
	if want("votes") {
		voters, err := h.Store.VotersByIssue(ctx, issue.ID)
		if err != nil {
			return err
		}
		fields["votes"] = map[string]any{"self": h.BaseURL + "/rest/api/3/issue/" + issue.Key + "/votes", "votes": len(voters), "hasVoted": containsString(voters, readerID)}
	}
	if want("subtasks") {
		children, err := h.Store.ChildIssues(ctx, workspaceID, issue.ID)
		if err != nil {
			return err
		}
		beans := []map[string]any{}
		for _, child := range children {
			if child.IssueType.Subtask && visible(child.ID) != nil {
				beans = append(beans, h.linkedIssueBean(child))
			}
		}
		fields["subtasks"] = beans
	}
	if want("attachment") {
		attachments, err := h.Store.AttachmentsByIssue(ctx, issue.ID)
		if err != nil {
			return err
		}
		beans := []map[string]any{}
		for _, attachment := range attachments {
			beans = append(beans, h.attachmentBean(attachment))
		}
		fields["attachment"] = beans
	}
	if want("worklog") {
		worklogs, err := h.Store.WorklogsByIssue(ctx, issue.ID)
		if err != nil {
			return err
		}
		beans := []map[string]any{}
		for index, worklog := range worklogs {
			if index == 20 {
				break
			}
			beans = append(beans, h.worklogBean(worklog))
		}
		fields["worklog"] = map[string]any{"startAt": 0, "maxResults": 20, "total": len(worklogs), "worklogs": beans}
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// visibleIssueByRef finds an issue the reader may see, by id or key.
func (h *Handler) visibleIssueByRef(ctx context.Context, workspaceID, readerID, ref string) *models.Issue {
	issue, err := h.Store.IssueByIDOrKey(ctx, workspaceID, strings.TrimSpace(ref))
	if err != nil {
		return nil
	}
	if ok, visErr := authz.CanSeeIssue(ctx, h.Store, workspaceID, issue.ProjectID, readerID, issue.ID, issue.SecurityLevelID); visErr != nil || !ok {
		return nil
	}
	return issue
}

// bulkIsWatching serves POST /issue/watching.
func (h *Handler) bulkIsWatching(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	workspaceID, readerID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var request struct {
		IssueIDs []string `json:"issueIds"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	result := map[string]bool{}
	for _, ref := range request.IssueIDs {
		result[ref] = false
		if issue := h.visibleIssueByRef(r.Context(), workspaceID, readerID, ref); issue != nil {
			watchers, err := h.Store.WatchersByIssue(r.Context(), issue.ID)
			if err != nil {
				jiraError(w, http.StatusInternalServerError, "internal error")
				return
			}
			result[ref] = containsString(watchers, readerID)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"issuesIsWatching": result})
}

// bulkFetchIssues serves POST /issue/bulkfetch.
func (h *Handler) bulkFetchIssues(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	workspaceID, readerID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var request struct {
		Expand         []string `json:"expand"`
		Fields         []string `json:"fields"`
		FieldsByKeys   bool     `json:"fieldsByKeys"`
		IssueIDsOrKeys []string `json:"issueIdsOrKeys"`
		Properties     []string `json:"properties"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	positiveField := false
	for _, field := range request.Fields {
		if !strings.HasPrefix(field, "-") && !strings.HasPrefix(field, "*") {
			positiveField = true
		}
	}
	limit := 100
	if positiveField && len(request.Expand) == 0 && len(request.Properties) == 0 {
		limit = 1000
	}
	switch {
	case len(request.IssueIDsOrKeys) == 0:
		jiraError(w, http.StatusBadRequest, "At least one issue ID or key is required.")
		return
	case len(request.IssueIDsOrKeys) > limit:
		jiraError(w, http.StatusBadRequest, "A maximum of "+strconv.Itoa(limit)+" issue IDs or keys can be requested.")
		return
	case len(request.Properties) > 5:
		jiraError(w, http.StatusBadRequest, "A maximum of 5 issue property keys can be requested.")
		return
	}
	options := searchOptions{Fields: request.Fields, Expand: request.Expand, Properties: request.Properties, FieldsByKeys: request.FieldsByKeys}
	if e := validateSearchOptions(&options); e != nil {
		writeJerr(w, e)
		return
	}
	issues := []*models.Issue{}
	seen := map[string]bool{}
	for _, ref := range request.IssueIDsOrKeys {
		if issue := h.visibleIssueByRef(r.Context(), workspaceID, readerID, ref); issue != nil && !seen[issue.ID] {
			seen[issue.ID] = true
			issues = append(issues, issue)
		}
	}
	customFields, err := h.Store.CustomFieldsForWorkspace(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	options.IssueDetails = true
	beans, err := h.searchIssueBeans(r.Context(), workspaceID, readerID, issues, options, len(request.Fields) == 0, searchFieldDefinitions(customFields))
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"issues": beans, "issueErrors": []any{}})
}

// bufferedResponse captures a handler's answer so a bulk operation can run the
// single-item handler for each element and report per-element results.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: http.Header{}, status: http.StatusOK}
}

func (b *bufferedResponse) Header() http.Header         { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bufferedResponse) WriteHeader(status int)      { b.status = status }

// createIssues serves POST /issue/bulk: up to 50 issues, each created exactly
// as POST /issue would create it.
func (h *Handler) createIssues(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	var request struct {
		IssueUpdates []json.RawMessage `json:"issueUpdates"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	if len(request.IssueUpdates) == 0 || len(request.IssueUpdates) > 50 {
		jiraError(w, http.StatusBadRequest, "Between 1 and 50 issues can be created in one request.")
		return
	}
	issues, failures := []any{}, []any{}
	for index, raw := range request.IssueUpdates {
		element := r.Clone(r.Context())
		element.Body = io.NopCloser(bytes.NewReader(raw))
		element.ContentLength = int64(len(raw))
		response := newBufferedResponse()
		h.createIssue(response, element)
		var decoded map[string]any
		_ = json.Unmarshal(response.body.Bytes(), &decoded)
		if response.status == http.StatusCreated {
			issues = append(issues, decoded)
			continue
		}
		if decoded == nil {
			decoded = map[string]any{"errorMessages": []string{}, "errors": map[string]any{}}
		}
		decoded["status"] = response.status
		failures = append(failures, map[string]any{"status": response.status, "elementErrors": decoded, "failedElementNumber": index})
	}
	status := http.StatusCreated
	if len(issues) == 0 {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]any{"issues": issues, "errors": failures})
}

// issueChangelog serves GET /issue/{key}/changelog, oldest first.
func (h *Handler) issueChangelog(w http.ResponseWriter, r *http.Request, idOrKey string) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, workspaceID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	startAt, maxResults, ok := pageParams(w, r, 100)
	if !ok {
		return
	}
	if maxResults > 100 {
		maxResults = 100
	}
	values, err := h.issueChangelogBeans(r.Context(), workspaceID, issue.ID, false)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	page := pageSlice(values, startAt, maxResults)
	self := h.BaseURL + "/rest/api/3/issue/" + issue.Key + "/changelog"
	body := map[string]any{
		"self":    self + "?maxResults=" + strconv.Itoa(maxResults) + "&startAt=" + strconv.Itoa(startAt),
		"startAt": startAt, "maxResults": maxResults, "total": len(values), "isLast": startAt+len(page) >= len(values), "values": page,
	}
	if startAt+len(page) < len(values) {
		body["nextPage"] = self + "?maxResults=" + strconv.Itoa(maxResults) + "&startAt=" + strconv.Itoa(startAt+len(page))
	}
	writeJSON(w, http.StatusOK, body)
}

// changelogByIDs serves POST /issue/{key}/changelog/list.
func (h *Handler) changelogByIDs(w http.ResponseWriter, r *http.Request, idOrKey string) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, workspaceID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var request struct {
		ChangelogIDs []int64 `json:"changelogIds"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	if len(request.ChangelogIDs) == 0 || len(request.ChangelogIDs) > 1000 {
		jiraError(w, http.StatusBadRequest, "Between 1 and 1000 changelog IDs are required.")
		return
	}
	wanted := map[string]bool{}
	for _, id := range request.ChangelogIDs {
		wanted[strconv.FormatInt(id, 10)] = true
	}
	values, err := h.issueChangelogBeans(r.Context(), workspaceID, issue.ID, false)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	histories := []map[string]any{}
	for _, value := range values {
		if id, _ := value["id"].(string); wanted[id] {
			histories = append(histories, value)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"histories": histories, "startAt": 0, "maxResults": len(histories), "total": len(histories)})
}

// bulkChangelogs serves POST /changelog/bulkfetch: changelogs across issues,
// oldest first and by issue id, optionally only for some fields.
func (h *Handler) bulkChangelogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	workspaceID, readerID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var request struct {
		FieldIDs       []string `json:"fieldIds"`
		IssueIDsOrKeys []string `json:"issueIdsOrKeys"`
		MaxResults     *int     `json:"maxResults"`
		NextPageToken  string   `json:"nextPageToken"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	switch {
	case len(request.IssueIDsOrKeys) == 0 || len(request.IssueIDsOrKeys) > 1000:
		jiraError(w, http.StatusBadRequest, "Between 1 and 1000 issue IDs or keys are required.")
		return
	case len(request.FieldIDs) > 10:
		jiraError(w, http.StatusBadRequest, "A maximum of 10 field IDs can be requested.")
		return
	}
	maxResults := 1000
	if request.MaxResults != nil {
		if *request.MaxResults < 1 || *request.MaxResults > 10000 {
			jiraError(w, http.StatusBadRequest, "maxResults must be between 1 and 10000.")
			return
		}
		maxResults = *request.MaxResults
	}
	offset := 0
	if request.NextPageToken != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(request.NextPageToken)
		if err == nil {
			offset, err = strconv.Atoi(string(decoded))
		}
		if err != nil || offset < 0 {
			jiraError(w, http.StatusBadRequest, "The nextPageToken is invalid.")
			return
		}
	}
	fieldFilter := map[string]bool{}
	for _, field := range request.FieldIDs {
		fieldFilter[field] = true
	}
	type entry struct {
		issue   *models.Issue
		created string
		bean    map[string]any
	}
	entries := []entry{}
	seen := map[string]bool{}
	for _, ref := range request.IssueIDsOrKeys {
		issue := h.visibleIssueByRef(r.Context(), workspaceID, readerID, ref)
		if issue == nil || seen[issue.ID] {
			continue
		}
		seen[issue.ID] = true
		values, err := h.issueChangelogBeans(r.Context(), workspaceID, issue.ID, false)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		for _, value := range values {
			if len(fieldFilter) > 0 {
				items, _ := value["items"].([]map[string]any)
				kept := []map[string]any{}
				for _, item := range items {
					if field, _ := item["fieldId"].(string); fieldFilter[field] {
						kept = append(kept, item)
					}
				}
				if len(kept) == 0 {
					continue
				}
				value["items"] = kept
			}
			created, _ := value["created"].(string)
			entries = append(entries, entry{issue: issue, created: created, bean: value})
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].created != entries[j].created {
			return entries[i].created < entries[j].created
		}
		return entries[i].issue.JiraID < entries[j].issue.JiraID
	})
	page := pageSlice(entries, offset, maxResults)
	grouped := []map[string]any{}
	index := map[string]int{}
	for _, item := range page {
		position, ok := index[item.issue.ID]
		if !ok {
			position = len(grouped)
			index[item.issue.ID] = position
			grouped = append(grouped, map[string]any{"issueId": jiraIssueID(item.issue), "changeHistories": []map[string]any{}})
		}
		grouped[position]["changeHistories"] = append(grouped[position]["changeHistories"].([]map[string]any), item.bean)
	}
	body := map[string]any{"issueChangeLogs": grouped}
	if offset+len(page) < len(entries) {
		body["nextPageToken"] = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + len(page))))
	}
	writeJSON(w, http.StatusOK, body)
}

// pickerBold wraps each occurrence of the query in bold tags, as the issue picker does.
func pickerBold(text, query string) string {
	escaped := html.EscapeString(text)
	query = strings.TrimSpace(query)
	if query == "" {
		return escaped
	}
	needle := html.EscapeString(query)
	lower, lowerNeedle := strings.ToLower(escaped), strings.ToLower(needle)
	var out strings.Builder
	for {
		index := strings.Index(lower, lowerNeedle)
		if index < 0 {
			out.WriteString(escaped)
			return out.String()
		}
		out.WriteString(escaped[:index] + "<b>" + escaped[index:index+len(needle)] + "</b>")
		escaped, lower = escaped[index+len(needle):], lower[index+len(needle):]
	}
}

// issuePicker serves GET /issue/picker: matching issues from the reader's
// history and from the current search.
func (h *Handler) issuePicker(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, readerID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	q := r.URL.Query()
	query := strings.TrimSpace(q.Get("query"))
	showSubTasks := q.Get("showSubTasks") != "false"
	showSubTaskParent := q.Get("showSubTaskParent") != "false"
	currentKey := strings.ToUpper(strings.TrimSpace(q.Get("currentIssueKey")))
	currentProject := strings.TrimSpace(q.Get("currentProjectId"))
	var currentIssue *models.Issue
	if currentKey != "" {
		currentIssue = h.visibleIssueByRef(r.Context(), workspaceID, readerID, currentKey)
	}
	const sectionLimit = 20
	matches := func(issue *models.Issue) bool {
		if issue.ArchivedAt != "" || strings.ToUpper(issue.Key) == currentKey {
			return false
		}
		if !showSubTasks && issue.IssueType.Subtask {
			return false
		}
		if !showSubTaskParent && currentIssue != nil && currentIssue.Parent != nil && currentIssue.Parent.ID == issue.ID {
			return false
		}
		if currentProject != "" && issue.ProjectID != currentProject {
			return false
		}
		if query == "" {
			return true
		}
		lower := strings.ToLower(query)
		return strings.Contains(strings.ToLower(issue.Key), lower) || strings.Contains(strings.ToLower(issue.Summary), lower)
	}
	suggestion := func(issue *models.Issue) map[string]any {
		icon, _ := h.issueTypeBean(issue.IssueType)["iconUrl"].(string)
		return map[string]any{"id": issue.JiraID, "key": issue.Key, "keyHtml": pickerBold(issue.Key, query), "img": icon, "summary": pickerBold(issue.Summary, query), "summaryText": issue.Summary}
	}
	section := func(id, label string, issues []*models.Issue, total int) map[string]any {
		values := []map[string]any{}
		for _, issue := range issues {
			values = append(values, suggestion(issue))
		}
		bean := map[string]any{"id": id, "label": label, "issues": values}
		if total == 0 {
			bean["msg"] = "No matching issues found"
		} else {
			bean["sub"] = "Showing " + strconv.Itoa(len(values)) + " of " + strconv.Itoa(total) + " matching issues"
		}
		return bean
	}
	historyIDs, err := h.Store.RecentlyViewedIssueIDs(r.Context(), workspaceID, readerID, 200)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	history, historyTotal := []*models.Issue{}, 0
	inHistory := map[string]bool{}
	for _, id := range historyIDs {
		issue := h.visibleIssueByRef(r.Context(), workspaceID, readerID, id)
		if issue == nil || !matches(issue) {
			continue
		}
		historyTotal++
		inHistory[issue.ID] = true
		if len(history) < sectionLimit {
			history = append(history, issue)
		}
	}
	compiled, e := h.compileJQL(r.Context(), workspaceID, q.Get("currentJQL"), readerID)
	if e != nil {
		writeJerr(w, e)
		return
	}
	found, _, err := h.Store.Search(r.Context(), workspaceID, readerID, compiled, 1000, 0)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	current, currentTotal := []*models.Issue{}, 0
	for _, issue := range found {
		if inHistory[issue.ID] || !matches(issue) {
			continue
		}
		currentTotal++
		if len(current) < sectionLimit {
			current = append(current, issue)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sections": []any{
		section("hs", "History Search", history, historyTotal),
		section("cs", "Current Search", current, currentTotal),
	}})
}

// notifyIssue serves POST /issue/{key}/notify.
func (h *Handler) notifyIssue(w http.ResponseWriter, r *http.Request, idOrKey string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, workspaceID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	type groupRef struct {
		GroupID string `json:"groupId"`
		Name    string `json:"name"`
	}
	var request struct {
		Subject  string `json:"subject"`
		TextBody string `json:"textBody"`
		HTMLBody string `json:"htmlBody"`
		To       struct {
			Reporter bool `json:"reporter"`
			Assignee bool `json:"assignee"`
			Watchers bool `json:"watchers"`
			Voters   bool `json:"voters"`
			Users    []struct {
				AccountID string `json:"accountId"`
			} `json:"users"`
			Groups   []groupRef `json:"groups"`
			GroupIDs []string   `json:"groupIds"`
		} `json:"to"`
		Restrict struct {
			Groups      []groupRef `json:"groups"`
			GroupIDs    []string   `json:"groupIds"`
			Permissions []struct {
				ID  string `json:"id"`
				Key string `json:"key"`
			} `json:"permissions"`
		} `json:"restrict"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	recipients := map[string]bool{}
	if request.To.Reporter {
		if issue.Reporter == nil {
			jiraError(w, http.StatusBadRequest, "The issue has no reporter to notify.")
			return
		}
		recipients[issue.Reporter.ID] = true
	}
	if request.To.Assignee {
		if issue.Assignee == nil {
			jiraError(w, http.StatusBadRequest, "The issue is unassigned, so the assignee can't be notified.")
			return
		}
		recipients[issue.Assignee.ID] = true
	}
	if request.To.Watchers {
		watchers, err := h.Store.WatchersByIssue(r.Context(), issue.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		for _, id := range watchers {
			recipients[id] = true
		}
	}
	if request.To.Voters {
		voters, err := h.Store.VotersByIssue(r.Context(), issue.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		for _, id := range voters {
			recipients[id] = true
		}
	}
	for _, user := range request.To.Users {
		if _, err := h.Store.SiteUser(r.Context(), workspaceID, user.AccountID); err != nil {
			jiraError(w, http.StatusBadRequest, "The user "+user.AccountID+" does not exist.")
			return
		}
		recipients[user.AccountID] = true
	}
	resolveGroups := func(refs []groupRef, ids []string) ([]store.SiteGroup, bool) {
		groups := []store.SiteGroup{}
		for _, id := range ids {
			refs = append(refs, groupRef{GroupID: id})
		}
		for _, ref := range refs {
			group, err := h.Store.SiteGroupByIDOrName(r.Context(), workspaceID, ref.GroupID, ref.Name)
			if err != nil {
				jiraError(w, http.StatusBadRequest, "The group "+ref.GroupID+ref.Name+" does not exist.")
				return nil, false
			}
			groups = append(groups, group)
		}
		return groups, true
	}
	toGroups, ok := resolveGroups(request.To.Groups, request.To.GroupIDs)
	if !ok {
		return
	}
	for _, group := range toGroups {
		members, err := h.Store.SiteGroupMembers(r.Context(), workspaceID, group.ID, false)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		for _, member := range members {
			recipients[member.ID] = true
		}
	}
	restrictGroups, ok := resolveGroups(request.Restrict.Groups, request.Restrict.GroupIDs)
	if !ok {
		return
	}
	restrictPermissions := []string{}
	for _, permission := range request.Restrict.Permissions {
		key := permission.Key
		if key == "" {
			key = permission.ID
		}
		if _, known := store.PermissionDefinitionByKey(key); !known {
			jiraError(w, http.StatusBadRequest, "The permission "+key+" does not exist.")
			return
		}
		restrictPermissions = append(restrictPermissions, key)
	}
	if len(recipients) == 1 && recipients[actorID] {
		jiraError(w, http.StatusBadRequest, "You can't send this notification only to yourself.")
		return
	}
	delete(recipients, actorID)
	emails := []string{}
	for id := range recipients {
		user, err := h.Store.SiteUser(r.Context(), workspaceID, id)
		if err != nil || !user.Active || user.Email == "" {
			continue
		}
		if ok, visErr := authz.CanSeeIssue(r.Context(), h.Store, workspaceID, issue.ProjectID, id, issue.ID, issue.SecurityLevelID); visErr != nil || !ok {
			continue
		}
		if len(restrictGroups) > 0 {
			member := false
			groups, _ := h.Store.UserGroups(r.Context(), workspaceID, id)
			for _, group := range groups {
				for _, wanted := range restrictGroups {
					member = member || group.ID == wanted.ID
				}
			}
			if !member {
				continue
			}
		}
		permitted := true
		for _, key := range restrictPermissions {
			if allowed, permErr := h.Store.HasProjectPermission(r.Context(), workspaceID, id, issue.ProjectID, issue.ID, key); permErr != nil || !allowed {
				permitted = false
			}
		}
		if permitted {
			emails = append(emails, user.Email)
		}
	}
	sort.Strings(emails)
	subject := strings.TrimSpace(request.Subject)
	if subject == "" {
		subject = issue.Key + ": " + issue.Summary
	}
	body := request.TextBody
	if strings.TrimSpace(body) == "" {
		body = request.HTMLBody
	}
	body = strings.TrimRight(body, "\n") + "\n\n" + issue.Key + " — " + issue.Summary + "\n" + h.BaseURL + "/browse/" + issue.Key
	if err := h.Store.QueueIssueEmail(r.Context(), workspaceID, emails, subject, body); err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireJiraAdmin checks the Administer Jira global permission.
func (h *Handler) requireJiraAdmin(w http.ResponseWriter, r *http.Request, workspaceID, actorID string) bool {
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID)
	if err != nil || !admin {
		jiraError(w, http.StatusForbidden, "You do not have the Administer Jira global permission.")
		return false
	}
	return true
}

// issueEvents serves GET /events.
func (h *Handler) issueEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if !h.requireJiraAdmin(w, r, workspaceID, actorID) {
		return
	}
	events, err := h.Store.IssueEvents(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load issue events.")
		return
	}
	values := []map[string]any{}
	for _, event := range events {
		values = append(values, map[string]any{"id": event.ID, "name": event.Name})
	}
	writeJSON(w, http.StatusOK, values)
}

// issueLimitReport serves GET /issue/limit/report and /issue/limit/adf/report.
func (h *Handler) issueLimitReport(w http.ResponseWriter, r *http.Request, adfReport bool) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if !h.requireJiraAdmin(w, r, workspaceID, actorID) {
		return
	}
	byKey := strings.EqualFold(r.URL.Query().Get("isReturningKeys"), "true")
	var report store.IssueLimitReport
	var err error
	if adfReport {
		fieldTypes := securityQueryValues(r, "fieldType")
		if len(fieldTypes) == 0 {
			fieldTypes = store.IssueADFFieldTypes
		}
		for _, fieldType := range fieldTypes {
			if !containsString(store.IssueADFFieldTypes, fieldType) {
				jiraError(w, http.StatusBadRequest, "The field type "+fieldType+" is not a valid ADF field type.")
				return
			}
		}
		report, err = h.Store.IssueADFSizes(r.Context(), workspaceID, actorID, byKey, fieldTypes)
	} else {
		report, err = h.Store.IssueLimitCounts(r.Context(), workspaceID, actorID, byKey)
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"limits": report.Limits, "issuesBreachingLimit": report.Breaching, "issuesApproachingLimit": report.Approaching, "entitiesBreachingLimit": report.Entities,
	})
}
