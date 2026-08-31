package api3

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
)

// ---- projects ----

func (h *Handler) projectBean(p *models.Project) map[string]any {
	return map[string]any{
		"id":             p.ID,
		"key":            p.Key,
		"name":           p.Name,
		"self":           h.BaseURL + "/rest/api/3/project/" + p.Key,
		"projectTypeKey": "software",
		"simplified":     true,
		"style":          "next-gen",
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
	values := make([]map[string]any, 0, len(projects))
	for _, p := range projects {
		values = append(values, h.projectBean(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"isLast":     true,
		"maxResults": 50,
		"startAt":    0,
		"total":      len(values),
		"values":     values,
	})
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
			writeJSON(w, http.StatusOK, h.projectBean(p))
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
	issueType, _ := h.Store.FirstIssueType(r.Context())
	customFields, _ := h.Store.CustomFieldsForProject(r.Context(), projectIDOrEmpty(projects))
	projectValues := make([]map[string]any, 0, len(projects))
	for _, p := range projects {
		its := []map[string]any{}
		if issueType != nil {
			its = append(its, map[string]any{
				"id":          issueType.ID,
				"name":        issueType.Name,
				"subtask":     false,
				"description": "",
				"iconUrl":     h.BaseURL + "/static/img/issuetype-task.svg",
			})
		}
		cfs := make([]map[string]any, 0, len(customFields))
		for _, cf := range customFields {
			cfs = append(cfs, map[string]any{
				"key":      cf.ID,
				"name":     cf.Name,
				"required": false,
				"schema":   map[string]any{"type": cf.Type, "custom": cf.ID, "customId": cf.ID},
			})
		}
		projectValues = append(projectValues, map[string]any{
			"id":         p.ID,
			"key":        p.Key,
			"name":       p.Name,
			"avatarUrls": map[string]string{"48x48": h.BaseURL + "/static/img/avatar-default.svg"},
			"issuetypes": its,
			"fields":     cfs,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"expand":   "projects",
		"projects": projectValues,
	})
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

func projectIDOrEmpty(projects []*models.Project) string {
	if len(projects) > 0 {
		return projects[0].ID
	}
	return ""
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"jql": "Invalid request payload."})
		return
	}
	maxResults := defaultSearchPageSize
	if req.MaxResults != nil {
		maxResults = *req.MaxResults
	}
	if e := validateSearchPage(0, maxResults); e != nil {
		writeJerr(w, e)
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
		if err != nil {
			jiraError(w, http.StatusBadRequest, "Invalid nextPageToken.")
			return
		}
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
		beans = append(beans, h.issueBean(i))
	}
	resp := map[string]any{"issues": beans}
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
