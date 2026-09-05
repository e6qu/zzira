package api3

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
)

// ---- projects ----

func (h *Handler) projectBean(p *models.Project) map[string]any {
	return map[string]any{
		"id":             p.ID,
		"key":            p.Key,
		"name":           p.Name,
		"description":    p.Description,
		"url":            p.URL,
		"assigneeType":   p.AssigneeType,
		"self":           h.BaseURL + "/rest/api/3/project/" + p.Key,
		"projectTypeKey": "software",
		"simplified":     true,
		"style":          "classic",
		"avatarUrls": map[string]string{
			"48x48": h.BaseURL + "/static/img/avatar-default.svg",
		},
	}
}

func (h *Handler) listProjects(w http.ResponseWriter, r *http.Request) {
	wsID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	projects, err := h.Store.ProjectsByWorkspace(r.Context(), wsID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(projects))
	for _, p := range projects {
		out = append(out, h.projectBean(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) searchProjects(w http.ResponseWriter, r *http.Request) {
	wsID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	projects, err := h.Store.ProjectsByWorkspace(r.Context(), wsID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	start, limit, e := metadataPage(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	projects, e = filterProjects(r, projects)
	if e != nil {
		writeJerr(w, e)
		return
	}
	total := len(projects)
	end := min(start, total) + min(limit, total-min(start, total))
	values := make([]map[string]any, 0, end-min(start, total))
	for _, p := range projects[min(start, total):end] {
		values = append(values, h.projectBean(p))
	}
	page := map[string]any{
		"self":       h.projectPageURL(r, start, limit),
		"isLast":     end >= total,
		"maxResults": limit,
		"startAt":    start,
		"total":      total,
		"values":     values,
	}
	if end < total && limit > 0 {
		page["nextPage"] = h.projectPageURL(r, end, limit)
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) getProject(w http.ResponseWriter, r *http.Request, keyOrID string) {
	wsID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	projects, err := h.Store.ProjectsByWorkspace(r.Context(), wsID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	for _, p := range projects {
		if p.Key == keyOrID || p.ID == keyOrID {
			h.writeProject(w, r, p)
			return
		}
	}
	jiraError(w, http.StatusNotFound, "No project could be found with key or id "+keyOrID+".")
}

// ---- users ----

func (h *Handler) searchUsers(w http.ResponseWriter, r *http.Request) {
	wsID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	query := r.URL.Query().Get("query")
	members, err := h.Store.SearchMembers(r.Context(), wsID, query)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(members))
	for _, u := range members {
		out = append(out, h.userBean(u))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	wsID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	accountID := r.URL.Query().Get("accountId")
	if accountID == "" {
		jiraError(w, http.StatusBadRequest, "The accountId parameter is required.")
		return
	}
	u, err := h.Store.MemberByID(r.Context(), wsID, accountID)
	if err != nil {
		jiraError(w, http.StatusNotFound, "The user does not exist.")
		return
	}
	writeJSON(w, http.StatusOK, h.userBean(u))
}

// ---- createmeta ----

func (h *Handler) createMeta(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	meta, err := h.Store.IssueCreateMetadata(r.Context(), wsID, userID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	projectFilter := commaQuerySet(r, "projectIds", "projectKeys")
	typeFilter := commaQuerySet(r, "issuetypeIds", "issuetypeNames")
	includeFields := strings.Contains(r.URL.Query().Get("expand"), "fields")
	projectValues := make([]map[string]any, 0, len(meta.Projects))
	for _, project := range meta.Projects {
		if len(projectFilter) > 0 && !querySetContains(projectFilter, project.Project.ID, project.Project.Key) {
			continue
		}
		issueTypes := make([]map[string]any, 0, len(project.IssueTypes))
		for _, issueType := range project.IssueTypes {
			if len(typeFilter) > 0 && !querySetContains(typeFilter, issueType.ID, issueType.Name) {
				continue
			}
			bean := h.createMetaIssueTypeBean(issueType)
			if includeFields {
				fields := make(map[string]any, len(project.Fields))
				for _, field := range project.Fields {
					fields[field.ID] = h.legacyCreateFieldBean(field)
				}
				bean["fields"] = fields
			}
			issueTypes = append(issueTypes, bean)
		}
		projectBean := h.projectBean(&project.Project)
		projectBean["issuetypes"] = issueTypes
		projectValues = append(projectValues, map[string]any{
			"id": projectBean["id"], "key": projectBean["key"], "name": projectBean["name"],
			"self": projectBean["self"], "avatarUrls": projectBean["avatarUrls"], "issuetypes": issueTypes,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"expand":   "projects",
		"projects": projectValues,
	})
}

func (h *Handler) createMetaIssueTypes(w http.ResponseWriter, r *http.Request, projectIDOrKey string) {
	project, e := h.createMetaProject(r, projectIDOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	start, limit, e := metadataPage(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	safeStart := min(start, len(project.IssueTypes))
	end := min(safeStart+limit, len(project.IssueTypes))
	values := make([]map[string]any, 0, end-safeStart)
	for _, issueType := range project.IssueTypes[safeStart:end] {
		values = append(values, h.createMetaIssueTypeBean(issueType))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"startAt": start, "maxResults": limit, "total": len(project.IssueTypes), "issueTypes": values,
	})
}

func (h *Handler) createMetaFields(w http.ResponseWriter, r *http.Request, projectIDOrKey, issueTypeID string) {
	project, e := h.createMetaProject(r, projectIDOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	found := false
	for _, issueType := range project.IssueTypes {
		if issueType.ID == issueTypeID {
			found = true
			break
		}
	}
	if !found {
		jiraError(w, http.StatusBadRequest, "The issue type is not available in this project.")
		return
	}
	start, limit, e := metadataPage(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	safeStart := min(start, len(project.Fields))
	end := min(safeStart+limit, len(project.Fields))
	values := make([]map[string]any, 0, end-safeStart)
	for _, field := range project.Fields[safeStart:end] {
		values = append(values, h.createFieldBean(field))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"startAt": start, "maxResults": limit, "total": len(project.Fields), "fields": values,
	})
}

func (h *Handler) createMetaProject(r *http.Request, projectIDOrKey string) (*models.CreateProjectMeta, *jerr) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		return nil, e
	}
	meta, err := h.Store.IssueCreateMetadata(r.Context(), wsID, userID)
	if err != nil {
		return nil, &jerr{http.StatusInternalServerError, "internal error", nil}
	}
	for i := range meta.Projects {
		project := &meta.Projects[i]
		if project.Project.ID == projectIDOrKey || strings.EqualFold(project.Project.Key, projectIDOrKey) {
			return project, nil
		}
	}
	return nil, &jerr{http.StatusBadRequest, "You cannot create issues in this project.", nil}
}

func (h *Handler) createMetaIssueTypeBean(issueType models.IssueType) map[string]any {
	return map[string]any{
		"id": issueType.ID, "name": issueType.Name, "description": "", "subtask": false,
		"iconUrl": h.BaseURL + "/static/img/issuetype-task.svg",
		"self":    h.BaseURL + "/rest/api/3/issuetype/" + issueType.ID,
	}
}

func (h *Handler) createFieldBean(field models.CreateFieldMeta) map[string]any {
	return map[string]any{
		"fieldId": field.ID, "key": field.ID, "name": field.Name, "required": field.Required,
		"schema": createFieldSchema(field), "allowedValues": createAllowedValues(field.Options),
	}
}

func (h *Handler) legacyCreateFieldBean(field models.CreateFieldMeta) map[string]any {
	bean := h.createFieldBean(field)
	delete(bean, "fieldId")
	bean["hasDefaultValue"] = false
	bean["operations"] = []string{"set"}
	return bean
}

func createFieldSchema(field models.CreateFieldMeta) map[string]any {
	schema := map[string]any{"type": field.Type}
	if field.ID == "description" {
		schema["system"] = field.ID
	} else if field.Custom {
		schema["custom"] = "com.zzira:" + field.Type
		customID := strings.TrimPrefix(field.ID, "customfield_")
		if numericID, err := strconv.Atoi(customID); err == nil {
			schema["customId"] = numericID
		} else {
			schema["customId"] = customID
		}
	} else {
		schema["system"] = field.ID
	}
	if field.Type == "versions" {
		schema["type"] = "array"
		schema["items"] = "version"
	}
	if field.Type == "array" {
		schema["items"] = "string"
	}
	return schema
}

func createAllowedValues(options []models.CreateFieldOption) []map[string]any {
	values := make([]map[string]any, 0, len(options))
	for _, option := range options {
		value := map[string]any{"name": option.Name}
		if option.ID != "" {
			value["id"] = option.ID
		}
		if option.Key != "" {
			value["key"] = option.Key
		}
		values = append(values, value)
	}
	return values
}

func metadataPage(r *http.Request) (int, int, *jerr) {
	start, limit := 0, 50
	for name, target := range map[string]*int{"startAt": &start, "maxResults": &limit} {
		if raw := r.URL.Query().Get(name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 0 || (name == "maxResults" && value > 100) {
				return 0, 0, &jerr{http.StatusBadRequest, name + " must be between 0 and 100.", nil}
			}
			*target = value
		}
	}
	return start, limit, nil
}

func commaQuerySet(r *http.Request, names ...string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, name := range names {
		for _, raw := range r.URL.Query()[name] {
			for _, value := range strings.Split(raw, ",") {
				if value = strings.TrimSpace(value); value != "" {
					out[strings.ToLower(value)] = struct{}{}
				}
			}
		}
	}
	return out
}

func querySetContains(values map[string]struct{}, candidates ...string) bool {
	for _, candidate := range candidates {
		if _, ok := values[strings.ToLower(candidate)]; ok {
			return true
		}
	}
	return false
}

// ---- search ----

func (h *Handler) compileJQL(ctx context.Context, raw, currentUser string) (jql.Compiled, *jerr) {
	if raw == "" {
		raw = "ORDER BY updated DESC"
	}
	q, err := jql.Parse(raw)
	if err != nil {
		return jql.Compiled{}, &jerr{http.StatusBadRequest, "Error in the JQL Query: " + err.Error(), nil}
	}
	resolver := jql.DefaultResolver()
	if customFields, err := h.Store.CustomFields(ctx); err == nil {
		resolver = jql.WithCustomFields(resolver, customFields)
	}
	// offset 2: store.Search reserves $1 for the workspace predicate
	c := jql.CompileAt(q, currentUser, resolver, 2)
	if c.Err != nil {
		return jql.Compiled{}, &jerr{http.StatusBadRequest, "Error in the JQL Query: " + c.Err.Error(), nil}
	}
	return c, nil
}

const defaultSearchPageSize = 50
const maximumSearchPageSize = 100

func validateSearchPage(startAt, maxResults int) *jerr {
	if startAt < 0 {
		return &jerr{http.StatusBadRequest, "startAt must be a non-negative integer.", nil}
	}
	if maxResults < 0 || maxResults > maximumSearchPageSize {
		return &jerr{http.StatusBadRequest, "maxResults must be between 0 and 100.", nil}
	}
	return nil
}

func querySearchPage(r *http.Request) (int, int, *jerr) {
	startAt := 0
	maxResults := defaultSearchPageSize
	if raw := r.URL.Query().Get("startAt"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return 0, 0, &jerr{http.StatusBadRequest, "startAt must be a non-negative integer.", nil}
		}
		startAt = value
	}
	if raw := r.URL.Query().Get("maxResults"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return 0, 0, &jerr{http.StatusBadRequest, "maxResults must be between 0 and 100.", nil}
		}
		maxResults = value
	}
	if err := validateSearchPage(startAt, maxResults); err != nil {
		return 0, 0, err
	}
	return startAt, maxResults, nil
}

func (h *Handler) runSearch(w http.ResponseWriter, r *http.Request, jqlText string, startAt, maxResults int) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if e := validateSearchPage(startAt, maxResults); e != nil {
		writeJerr(w, e)
		return
	}
	c, e := h.compileJQL(r.Context(), jqlText, userID)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issues, total, err := h.Store.Search(r.Context(), wsID, userID, c, maxResults, startAt)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "Error in the JQL Query: "+err.Error())
		return
	}
	beans := make([]map[string]any, 0, len(issues))
	for _, i := range issues {
		beans = append(beans, h.issueBean(i))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"expand":     "schema,names",
		"startAt":    startAt,
		"maxResults": maxResults,
		"total":      total,
		"issues":     beans,
	})
}

type searchRequest struct {
	JQL           string   `json:"jql"`
	StartAt       *int     `json:"startAt"`
	MaxResults    *int     `json:"maxResults"`
	Fields        []string `json:"fields"`
	NextPageToken string   `json:"nextPageToken"`
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	jqlText := r.URL.Query().Get("jql")
	startAt, maxResults, e := querySearchPage(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if r.Method == http.MethodPost {
		var req searchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"jql": "Invalid request payload."})
			return
		}
		jqlText = req.JQL
		startAt = 0
		maxResults = defaultSearchPageSize
		if req.StartAt != nil {
			startAt = *req.StartAt
		}
		if req.MaxResults != nil {
			maxResults = *req.MaxResults
		}
	}
	h.runSearch(w, r, jqlText, startAt, maxResults)
}

func (h *Handler) searchJQL(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		for key := range q {
			switch key {
			case "jql", "fields", "maxResults", "nextPageToken":
			default:
				jiraError(w, 400, "Unsupported enhanced search parameter: "+key)
				return
			}
		}
		req.JQL = q.Get("jql")
		req.NextPageToken = q.Get("nextPageToken")
		for _, raw := range q["fields"] {
			req.Fields = append(req.Fields, strings.Split(raw, ",")...)
		}
		if q.Has("maxResults") {
			n, err := strconv.Atoi(q.Get("maxResults"))
			if err != nil {
				jiraError(w, 400, "maxResults must be an integer.")
				return
			}
			req.MaxResults = &n
		}
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			jiraError(w, 400, "Invalid enhanced search request: "+err.Error())
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			jiraError(w, 400, "Expected one JSON object.")
			return
		}
	}
	if req.StartAt != nil {
		jiraError(w, 400, "Use nextPageToken instead of startAt.")
		return
	}
	maxResults := defaultSearchPageSize
	if req.MaxResults != nil {
		maxResults = *req.MaxResults
	}
	if maxResults < 1 || maxResults > 5000 {
		jiraError(w, 400, "maxResults must be between 1 and 5000.")
		return
	}
	startAt := 0
	if req.NextPageToken != "" {
		raw, err := base64.URLEncoding.DecodeString(req.NextPageToken)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "Invalid nextPageToken.")
			return
		}
		startAt, err = strconv.Atoi(string(raw))
		if err != nil || startAt < 0 {
			jiraError(w, http.StatusBadRequest, "Invalid nextPageToken.")
			return
		}
	}
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	parsed, err := jql.Parse(req.JQL)
	if err != nil {
		jiraError(w, 400, "Error in the JQL Query: "+err.Error())
		return
	}
	if root, ok := parsed.Root.(jql.Text); ok && root.Value == "" {
		jiraError(w, 400, "Enhanced search requires a bounded JQL query.")
		return
	}
	c, e := h.compileJQL(r.Context(), req.JQL, userID)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issues, _, err := h.Store.Search(r.Context(), wsID, userID, c, maxResults+1, startAt)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "Error in the JQL Query: "+err.Error())
		return
	}
	hasMore := len(issues) > maxResults
	if hasMore {
		issues = issues[:maxResults]
	}
	beans := make([]map[string]any, 0, len(issues))
	for _, i := range issues {
		beans = append(beans, enhancedIssueFields(h.issueBean(i), req.Fields))
	}
	resp := map[string]any{"issues": beans, "isLast": !hasMore}
	if hasMore {
		resp["nextPageToken"] = base64.URLEncoding.EncodeToString([]byte(strconv.Itoa(startAt + maxResults)))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) searchCount(w http.ResponseWriter, r *http.Request) {
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"jql": "Invalid request payload."})
		return
	}
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	c, e := h.compileJQL(r.Context(), req.JQL, userID)
	if e != nil {
		writeJerr(w, e)
		return
	}
	_, total, err := h.Store.Search(r.Context(), wsID, userID, c, 1, 0)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "Error in the JQL Query: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": total})
}

// ---- saved filters (read-only in V2) ----

func (h *Handler) filterBean(f *models.Filter) map[string]any {
	owner := map[string]any{}
	if f.OwnerID != "" {
		owner = map[string]any{"accountId": f.OwnerID, "displayName": f.OwnerName, "active": true, "accountType": "atlassian"}
	}
	return map[string]any{
		"id":               f.ID,
		"name":             f.Name,
		"self":             h.BaseURL + "/rest/api/3/filter/" + f.ID,
		"jql":              f.JQL,
		"description":      f.Description,
		"owner":            owner,
		"favourite":        f.Favourite,
		"favouriteCount":   1,
		"sharePermissions": []map[string]any{{"type": "global"}},
	}
}

func (h *Handler) listFilters(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	filters, err := h.Store.ListFilters(r.Context(), wsID, userID)
	if err != nil {
		log.Printf("listFilters: %v", err)
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := make([]map[string]any, 0, len(filters))
	for _, f := range filters {
		out = append(out, h.filterBean(f))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getFilter(w http.ResponseWriter, r *http.Request, id string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	f, err := h.Store.FilterByID(r.Context(), wsID, userID, id)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Filter does not exist.")
		return
	}
	writeJSON(w, http.StatusOK, h.filterBean(f))
}

// ---- bootstrap (custom delta-sync endpoint) ----

// BootstrapHandler serves GET /bootstrap — registered on the top-level mux
// (it lives outside the /rest/api/3 subtree by design).
func (h *Handler) BootstrapHandler(w http.ResponseWriter, r *http.Request) {
	h.bootstrap(w, r)
}

// bootstrap returns sync-payload-shaped data (same shapes as action payloads)
// so the client's apply path materializes it unchanged.
func (h *Handler) bootstrap(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	snap, err := h.Store.BootstrapSnapshot(r.Context(), wsID, userID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "bootstrap failed")
		return
	}
	issues := make([]models.IssueUpsertPayload, 0, len(snap.Issues))
	for i := range snap.Issues {
		issues = append(issues, models.IssueUpsertPayload{Issue: snap.Issues[i]})
	}
	comments := make([]models.CommentUpsertPayload, 0, len(snap.Comments))
	for i := range snap.Comments {
		comments = append(comments, models.CommentUpsertPayload{Comment: snap.Comments[i]})
	}
	attachments := make([]models.AttachmentUpsertPayload, 0, len(snap.Attachments))
	for i := range snap.Attachments {
		attachments = append(attachments, models.AttachmentUpsertPayload{Attachment: snap.Attachments[i]})
	}
	worklogs := make([]models.WorklogUpsertPayload, 0, len(snap.Worklogs))
	for i := range snap.Worklogs {
		worklogs = append(worklogs, models.WorklogUpsertPayload{Worklog: snap.Worklogs[i]})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"seq":         snap.Seq,
		"issues":      issues,
		"comments":    comments,
		"attachments": attachments,
		"worklogs":    worklogs,
	})
}
