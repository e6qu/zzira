// Package api3 is the REST edge for the Atlassian Jira Cloud REST API v3
// contract. It only translates wire DTOs ⇄ internal models and calls the
// command core. Wire shapes are locked by golden tests (golden_test.go).
package api3

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/build"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type Handler struct {
	Store    *store.Store
	Commands *commands.Service
	Blobs    attachments.Store
	BaseURL  string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/rest/api/3")
	switch {
	case path == "/serverInfo" && r.Method == http.MethodGet:
		h.serverInfo(w, r)
	case path == "/myself" && r.Method == http.MethodGet:
		h.myself(w, r)
	case path == "/issue" && r.Method == http.MethodPost:
		h.createIssue(w, r)
	case path == "/field" && r.Method == http.MethodGet:
		h.listFields(w, r)
	case path == "/field" && r.Method == http.MethodPost:
		h.createField(w, r)
	case strings.HasPrefix(path, "/field/"):
		h.fieldRoute(w, r, strings.Split(strings.TrimPrefix(path, "/field/"), "/"))
	case path == "/issueLinkType" && r.Method == http.MethodGet:
		h.listLinkTypes(w, r)
	case path == "/issueLink" && r.Method == http.MethodPost:
		h.createIssueLink(w, r)
	case strings.HasPrefix(path, "/issueLink/"):
		h.deleteIssueLink(w, r, strings.TrimPrefix(path, "/issueLink/"))
	case path == "/label" && r.Method == http.MethodGet:
		h.labelsEndpoint(w, r)
	case path == "/issuetype" && r.Method == http.MethodGet:
		h.issueTypesEndpoint(w, r)
	case path == "/priority" && r.Method == http.MethodGet:
		h.prioritiesEndpoint(w, r)
	case path == "/status" && r.Method == http.MethodGet:
		h.statusesEndpoint(w, r)
	case path == "/statuscategory" && r.Method == http.MethodGet:
		h.statusCategoryEndpoint(w, r)
	case path == "/resolution" && r.Method == http.MethodGet:
		h.resolutionsEndpoint(w, r)
	case path == "/mypermissions" && r.Method == http.MethodGet:
		h.myPermissions(w, r)
	case path == "/permissions/check" && r.Method == http.MethodPost:
		h.permissionsCheck(w, r)
	case path == "/workflow/search" && r.Method == http.MethodGet:
		h.workflowRoute(w, r)
	case path == "/workflow" && r.Method == http.MethodPost:
		h.workflowRoute(w, r)
	case strings.HasPrefix(path, "/workflow/project/"):
		h.workflowRoute(w, r)
	case path == "/issuesecurityschemes" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		h.securitySchemeRoute(w, r)
	case strings.HasPrefix(path, "/issuesecurityschemes/"):
		h.securitySchemeRoute(w, r)
	case path == "/role" && r.Method == http.MethodGet:
		h.roleRoute(w, r)
	case path == "/webhook" || path == "/webhook/refresh" || strings.HasPrefix(path, "/webhook/"):
		h.webhookRoute(w, r)
	case path == "/filter" && r.Method == http.MethodPost:
		h.createFilter(w, r)
	case path == "/project" && r.Method == http.MethodGet:
		h.listProjects(w, r)
	case path == "/project/search" && r.Method == http.MethodGet:
		h.searchProjects(w, r)
	case strings.HasPrefix(path, "/project/"):
		h.getProject(w, r, strings.TrimPrefix(path, "/project/"))
	case path == "/user/search" && r.Method == http.MethodGet:
		h.searchUsers(w, r)
	case path == "/user" && r.Method == http.MethodGet:
		h.getUser(w, r)
	case path == "/search" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		h.search(w, r)
	case path == "/search/jql" && r.Method == http.MethodPost:
		h.searchJQL(w, r)
	case path == "/search/approximate-count" && r.Method == http.MethodPost:
		h.searchCount(w, r)
	case path == "/filter" && r.Method == http.MethodGet:
		h.listFilters(w, r)
	case strings.HasPrefix(path, "/filter/"):
		h.filterCRUD(w, r, strings.TrimPrefix(path, "/filter/"))
	case path == "/bootstrap" && r.Method == http.MethodGet:
		h.bootstrap(w, r)
	case strings.HasPrefix(path, "/attachment/content/"):
		h.attachmentContent(w, r, strings.TrimPrefix(path, "/attachment/content/"))
	case strings.HasPrefix(path, "/attachment/"):
		h.attachmentMeta(w, r, strings.TrimPrefix(path, "/attachment/"))
	case strings.HasPrefix(path, "/issue/"):
		parts := strings.Split(strings.TrimPrefix(path, "/issue/"), "/")
		h.issueRoute(w, r, parts)
	default:
		jiraError(w, http.StatusNotFound, fmt.Sprintf("No resource found for path %s", r.URL.Path))
	}
}

func (h *Handler) issueRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	idOrKey := parts[0]
	switch {
	case len(parts) == 1 && idOrKey == "createmeta" && r.Method == http.MethodGet:
		h.createMeta(w, r)
		return
	case len(parts) == 1:
		switch r.Method {
		case http.MethodGet:
			h.getIssue(w, r, idOrKey)
		case http.MethodPut:
			h.putIssue(w, r, idOrKey)
		case http.MethodDelete:
			h.deleteIssue(w, r, idOrKey)
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case len(parts) == 2 && parts[1] == "comment" && r.Method == http.MethodGet:
		h.listComments(w, r, idOrKey)
	case len(parts) == 2 && parts[1] == "comment" && r.Method == http.MethodPost:
		h.addComment(w, r, idOrKey)
	case len(parts) == 3 && parts[1] == "comment" && r.Method == http.MethodGet:
		h.getComment(w, r, idOrKey, parts[2])
	case len(parts) == 3 && parts[1] == "comment" && r.Method == http.MethodDelete:
		h.deleteComment(w, r, idOrKey, parts[2])
	case len(parts) == 2 && parts[1] == "transitions" && r.Method == http.MethodGet:
		h.listTransitions(w, r, idOrKey)
	case len(parts) == 2 && parts[1] == "transitions" && r.Method == http.MethodPost:
		h.performTransition(w, r, idOrKey)
	case len(parts) >= 2 && parts[1] == "worklog":
		h.issueWorklogRoute(w, r, idOrKey, parts[2:])
	case len(parts) == 2 && parts[1] == "assignee" && r.Method == http.MethodPut:
		h.putAssignee(w, r, idOrKey)
	case len(parts) == 2 && parts[1] == "attachments" && r.Method == http.MethodPost:
		h.uploadAttachments(w, r, idOrKey)
	case len(parts) == 2 && parts[1] == "changelog" && r.Method == http.MethodGet:
		h.changelog(w, r, idOrKey)
	case len(parts) == 2 && parts[1] == "editmeta" && r.Method == http.MethodGet:
		h.editMeta(w, r, idOrKey)
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

// ---- auth helpers ----

type jerr struct {
	status      int
	message     string
	fieldErrors map[string]string
}

func (h *Handler) authWorkspace(r *http.Request) (wsID, userID string, j *jerr) {
	userID, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		return "", "", &jerr{http.StatusUnauthorized, "You are not authenticated. Authentication required to perform this operation.", nil}
	}
	wsID, _, err = h.Store.DefaultWorkspace(r.Context())
	if err != nil {
		return "", "", &jerr{http.StatusInternalServerError, "no workspace configured", nil}
	}
	ok, err := authz.CanSeeWorkspace(r.Context(), h.Store, wsID, userID)
	if err != nil || !ok {
		return "", "", &jerr{http.StatusForbidden, "You do not have permission to perform this operation.", nil}
	}
	return wsID, userID, nil
}

func (h *Handler) resolveIssue(r *http.Request, wsID, idOrKey string) (*models.Issue, *jerr) {
	issue, err := h.Store.IssueByIDOrKey(r.Context(), wsID, idOrKey)
	if err != nil {
		return nil, &jerr{http.StatusNotFound, "Issue does not exist or you do not have permission to see it.", nil}
	}
	userID, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		return nil, &jerr{http.StatusUnauthorized, "You are not authenticated. Authentication required to perform this operation.", nil}
	}
	visible, err := authz.CanSeeIssue(r.Context(), h.Store, wsID, issue.ProjectID, userID, issue.SecurityLevelID)
	if err != nil {
		return nil, &jerr{http.StatusInternalServerError, "internal error", nil}
	}
	if !visible {
		return nil, &jerr{http.StatusNotFound, "Issue does not exist or you do not have permission to see it.", nil}
	}
	return issue, nil
}

func (h *Handler) storeStatus(r *http.Request, id string) models.Status {
	st, err := h.Store.StatusByID(r.Context(), id)
	if err != nil {
		return models.Status{ID: id}
	}
	return st
}

func (h *Handler) statusBean(s models.Status) map[string]any {
	return map[string]any{
		"self":           h.BaseURL + "/rest/api/3/status/" + s.ID,
		"id":             s.ID,
		"name":           s.Name,
		"statusCategory": statusCategoryBean(s.Category),
	}
}

func writeJerr(w http.ResponseWriter, e *jerr) {
	if e.fieldErrors != nil {
		jiraFieldError(w, e.status, e.fieldErrors)
		return
	}
	jiraError(w, e.status, e.message)
}

// ---- serverInfo / myself ----

func (h *Handler) serverInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"baseUrl":     h.BaseURL,
		"version":     build.Version,
		"buildNumber": 0,
		"scmInfo":     "",
		"product":     build.Product,
		"deploymentType": map[string]any{
			"type":          "Cloud",
			"remoteAddress": r.RemoteAddr,
		},
	})
}

func (h *Handler) myself(w http.ResponseWriter, r *http.Request) {
	userID, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="zzira"`)
		jiraError(w, http.StatusUnauthorized, "You are not authenticated. Authentication required to perform this operation.")
		return
	}
	u, err := h.Store.UserByID(r.Context(), userID)
	if err != nil {
		jiraError(w, http.StatusUnauthorized, "You are not authenticated. Authentication required to perform this operation.")
		return
	}
	writeJSON(w, http.StatusOK, h.userBean(u))
}

func (h *Handler) userBean(u *models.User) map[string]any {
	return map[string]any{
		"accountId":    u.ID,
		"emailAddress": u.Email,
		"displayName":  u.DisplayName,
		"active":       u.Active,
		"timeZone":     u.TimeZone,
		"accountType":  "atlassian",
		"avatarUrls": map[string]string{
			"48x48": h.BaseURL + "/static/img/avatar-default.svg",
			"24x24": h.BaseURL + "/static/img/avatar-default.svg",
		},
	}
}

// ---- issue create/get/update/delete ----

type createIssueRequest struct {
	Update json.RawMessage `json:"update"`
	Fields struct {
		Project *struct {
			Key string `json:"key"`
		} `json:"project"`
		Summary     string          `json:"summary"`
		Description json.RawMessage `json:"description"`
		IssueType   *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"issuetype"`
		Priority *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"priority"`
		Assignee *struct {
			AccountID string `json:"accountId"`
		} `json:"assignee"`
		Security *struct {
			ID string `json:"id"`
		} `json:"security"`
	} `json:"fields"`
}

func (h *Handler) createIssue(w http.ResponseWriter, r *http.Request) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		if e.status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Basic realm="zzira"`)
		}
		writeJerr(w, e)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"fields": "Invalid request payload."})
		return
	}
	var req createIssueRequest
	if err := json.Unmarshal(body, &req); err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"fields": "Invalid request payload."})
		return
	}
	if req.Fields.Project == nil || req.Fields.Project.Key == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"project": "Project key is required."})
		return
	}
	if req.Fields.Summary == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"summary": "You must specify a summary of the issue."})
		return
	}
	if req.Fields.IssueType == nil || (req.Fields.IssueType.ID == "" && req.Fields.IssueType.Name == "") {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"issuetype": "Issue type is required."})
		return
	}
	issueTypeID := req.Fields.IssueType.ID
	if issueTypeID == "" {
		issueTypeID = req.Fields.IssueType.Name
	}
	priorityID := ""
	if req.Fields.Priority != nil {
		priorityID = req.Fields.Priority.ID
		if priorityID == "" {
			priorityID = req.Fields.Priority.Name
		}
	}
	description := ""
	if len(req.Fields.Description) > 0 {
		description = adfToPlainText(req.Fields.Description)
	}
	assigneeID := ""
	if req.Fields.Assignee != nil {
		assigneeID = req.Fields.Assignee.AccountID
	}
	issue, _, err := h.Commands.CreateIssue(r.Context(), commands.CreateIssueInput{
		ActorID:     userID,
		WorkspaceID: wsID,
		ProjectKey:  req.Fields.Project.Key,
		Summary:     req.Fields.Summary,
		Description: description,
		IssueTypeID: issueTypeID,
		PriorityID:  priorityID,
		AssigneeID:  assigneeID,
		Fields:      customFieldsFromBody(body),
	})
	if err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"fields": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":   issue.ID,
		"key":  issue.Key,
		"self": h.BaseURL + "/rest/api/3/issue/" + issue.ID,
	})
}

type putIssueRequest struct {
	Fields struct {
		Summary     *string         `json:"summary"`
		Description json.RawMessage `json:"description"`
		Priority    *struct {
			ID string `json:"id"`
		} `json:"priority"`
		Assignee *json.RawMessage `json:"assignee"`
		Security *struct {
			ID string `json:"id"`
		} `json:"security"`
	} `json:"fields"`
}

func (h *Handler) putIssue(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if _, e := h.resolveIssue(r, wsID, idOrKey); e != nil {
		writeJerr(w, e)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"fields": "Invalid request payload."})
		return
	}
	var req putIssueRequest
	if err := json.Unmarshal(body, &req); err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"fields": "Invalid request payload."})
		return
	}
	up := store.IssueUpdate{Summary: req.Fields.Summary}
	if len(req.Fields.Description) > 0 {
		up.Description = req.Fields.Description
	}
	if req.Fields.Priority != nil {
		p := req.Fields.Priority.ID
		up.PriorityID = &p
	}
	if req.Fields.Assignee != nil {
		var a *struct {
			AccountID string `json:"accountId"`
		}
		if string(*req.Fields.Assignee) == "null" {
			empty := ""
			up.AssigneeID = &empty // explicit null = unassign
		} else if err := json.Unmarshal(*req.Fields.Assignee, &a); err == nil && a != nil {
			up.AssigneeID = &a.AccountID
		}
		_ = a
	}
	fields := customFieldsFromBody(body)
	var securityID *string
	if req.Fields.Security != nil {
		sid := req.Fields.Security.ID
		securityID = &sid
	}
	if _, _, err := h.Commands.UpdateIssue(r.Context(), commands.UpdateIssueInput{
		ActorID: userID, WorkspaceID: wsID, IssueIDOrKey: idOrKey,
		Summary: up.Summary, Description: up.Description,
		PriorityID: up.PriorityID, AssigneeID: up.AssigneeID,
		SecurityLevelID: securityID, Fields: fields,
	}); err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"fields": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteIssue(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if _, err := h.Store.DeleteIssue(r.Context(), userID, wsID, issue.ID, "deleted via API"); err != nil {
		jiraError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getIssue(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, _, e := h.authWorkspace(r)
	if e != nil {
		if e.status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Basic realm="zzira"`)
		}
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	bean := h.issueBean(issue)
	if issue.SecurityLevelID != "" {
		if name := h.Store.SecurityLevelName(r.Context(), issue.ProjectID, issue.SecurityLevelID); name != "" {
			bean["fields"].(map[string]any)["security"] = map[string]any{"id": issue.SecurityLevelID, "name": name}
		}
	}
	if strings.Contains(r.URL.Query().Get("expand"), "renderedFields") {
		bean["rendered"] = map[string]any{
			"description": adf.ToHTML(issue.Description),
		}
	}
	writeJSON(w, http.StatusOK, bean)
}

// issueBean renders the Jira IssueBean (V3 subset). Field keys match the
// published contract; golden tests lock them.
// IssueBean is exported for the Agile edge, which must render identical beans.
func (h *Handler) IssueBean(i *models.Issue) map[string]any { return h.issueBean(i) }

func (h *Handler) issueBean(i *models.Issue) map[string]any {
	fields := map[string]any{
		"summary":     i.Summary,
		"description": i.Description,
		"labels":      []string{},
		"created":     i.UpdatedAt,
		"updated":     i.UpdatedAt,
		"project": map[string]any{
			"id":   i.ProjectID,
			"key":  projectKeyOf(i),
			"name": projectKeyOf(i),
		},
		"status": map[string]any{
			"name":           i.Status.Name,
			"id":             i.Status.ID,
			"statusCategory": statusCategoryBean(i.Status.Category),
		},
		"issuetype": map[string]any{
			"id":      i.IssueType.ID,
			"name":    i.IssueType.Name,
			"iconUrl": h.BaseURL + "/static/img/issuetype-task.svg",
		},
	}
	if i.Priority != nil {
		fields["priority"] = map[string]any{"id": i.Priority.ID, "name": i.Priority.Name}
	}
	if i.Assignee != nil {
		fields["assignee"] = h.userBean(i.Assignee)
	}
	if i.Reporter != nil {
		fields["reporter"] = h.userBean(i.Reporter)
	}
	for k, v := range i.Fields {
		fields[k] = v
	}
	return map[string]any{
		"expand": "renderedFields,names,schema,operations,editmeta,changelog,versionedRepresentations",
		"id":     i.ID,
		"self":   h.BaseURL + "/rest/api/3/issue/" + i.ID,
		"key":    i.Key,
		"fields": fields,
	}
}

func statusCategoryBean(category string) map[string]any {
	name := category
	switch category {
	case "new":
		name = "To Do"
	case "indeterminate":
		name = "In Progress"
	case "done":
		name = "Done"
	}
	return map[string]any{"key": category, "name": name}
}

func projectKeyOf(i *models.Issue) string {
	if idx := strings.IndexByte(i.Key, '-'); idx > 0 {
		return i.Key[:idx]
	}
	return i.Key
}

// ---- comments ----

func (h *Handler) commentBean(c *models.Comment) map[string]any {
	author := map[string]any{"accountId": c.AuthorID, "displayName": c.AuthorName, "active": true, "accountType": "atlassian"}
	return map[string]any{
		"id":        c.ID,
		"self":      h.BaseURL + "/rest/api/3/issue/comment/" + c.ID,
		"author":    author,
		"body":      c.Body,
		"created":   c.Created,
		"updated":   c.Created,
		"jsdPublic": true,
	}
}

func (h *Handler) listComments(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	comments, err := h.Store.CommentsByIssue(r.Context(), issue.ID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	beans := make([]map[string]any, 0, len(comments))
	for _, c := range comments {
		beans = append(beans, h.commentBean(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"comments":   beans,
		"startAt":    0,
		"maxResults": 5000,
		"total":      len(beans),
	})
}

type addCommentRequest struct {
	Body json.RawMessage `json:"body"`
}

func (h *Handler) addComment(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var req addCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Body) == 0 {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"body": "Comment body cannot be empty."})
		return
	}
	comment, _, err := h.Commands.AddComment(r.Context(), commands.AddCommentInput{
		ActorID: userID, WorkspaceID: wsID, IssueIDOrKey: issue.ID, Body: req.Body,
	})
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, h.commentBean(comment))
}

func (h *Handler) getComment(w http.ResponseWriter, r *http.Request, idOrKey, commentID string) {
	wsID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	c, err := h.Store.CommentByID(r.Context(), wsID, commentID)
	if err != nil || c.IssueID != issue.ID {
		jiraError(w, http.StatusNotFound, "Comment does not exist.")
		return
	}
	writeJSON(w, http.StatusOK, h.commentBean(c))
}

func (h *Handler) deleteComment(w http.ResponseWriter, r *http.Request, idOrKey, commentID string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	c, err := h.Store.CommentByID(r.Context(), wsID, commentID)
	if err != nil || c.IssueID != issue.ID {
		jiraError(w, http.StatusNotFound, "Comment does not exist.")
		return
	}
	if _, err := h.Commands.DeleteComment(r.Context(), userID, wsID, commentID); err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- transitions ----

func (h *Handler) listTransitions(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	wf, err := h.Store.WorkflowForProject(r.Context(), issue.ProjectID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	beans := []map[string]any{}
	for _, t := range wf.Available(issue.Status.ID) {
		status := h.storeStatus(r, t.To)
		beans = append(beans, map[string]any{
			"id":            t.ID,
			"name":          t.Name,
			"to":            h.statusBean(status),
			"hasScreen":     false,
			"isGlobal":      false,
			"isInitial":     false,
			"isConditional": false,
			"isAvailable":   true,
			"fields":        map[string]any{},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"expand":      "transitions",
		"transitions": beans,
	})
}

func (h *Handler) performTransition(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var req struct {
		Transition struct {
			ID string `json:"id"`
		} `json:"transition"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Transition.ID == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"transition": "Transition id is required."})
		return
	}
	if _, _, err := h.Commands.TransitionIssue(r.Context(), userID, wsID, idOrKey, req.Transition.ID); err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- changelog (derived view of the log) ----

func (h *Handler) changelog(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	entries, err := h.Store.IssueChangelog(r.Context(), wsID, issue.ID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	values := make([]map[string]any, 0, len(entries))
	for _, en := range entries {
		items := make([]map[string]any, 0, len(en.Items))
		for _, it := range en.Items {
			items = append(items, map[string]any{
				"field":      it.Field,
				"fieldtype":  it.FieldType,
				"from":       it.From,
				"fromString": it.FromString,
				"to":         it.To,
				"toString":   it.ToString,
			})
		}
		values = append(values, map[string]any{
			"id":      fmt.Sprintf("%d", en.Seq),
			"author":  h.userBean(en.Author),
			"created": en.Created,
			"items":   items,
		})
	}
	isLast := true
	writeJSON(w, http.StatusOK, map[string]any{
		"startAt":    0,
		"maxResults": 1000,
		"total":      len(values),
		"isLast":     isLast,
		"values":     values,
	})
}

// ---- editmeta ----

func (h *Handler) editMeta(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if _, e := h.resolveIssue(r, wsID, idOrKey); e != nil {
		writeJerr(w, e)
		return
	}
	field := func(name, typ string, required bool) map[string]any {
		return map[string]any{
			"required":   required,
			"schema":     map[string]any{"type": typ},
			"name":       name,
			"key":        strings.ToLower(name),
			"operations": []string{"set"},
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"fields": map[string]any{
			"summary":     field("Summary", "string", true),
			"description": map[string]any{"required": false, "schema": map[string]any{"type": "doc", "system": "description"}, "name": "Description", "key": "description", "operations": []string{"set"}},
			"assignee":    field("Assignee", "user", false),
			"priority":    field("Priority", "priority", false),
		},
	})
}

// ---- shared helpers ----

// adfToPlainText extracts raw text from an ADF doc (V1 normalizer).
func adfToPlainText(raw json.RawMessage) string {
	type adfNode struct {
		Type    string    `json:"type"`
		Text    string    `json:"text"`
		Content []adfNode `json:"content"`
	}
	var doc struct {
		Content []adfNode `json:"content"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	var b strings.Builder
	var walk func(nodes []adfNode)
	walk = func(nodes []adfNode) {
		for _, n := range nodes {
			if n.Text != "" {
				b.WriteString(n.Text)
			}
			walk(n.Content)
		}
	}
	for _, block := range doc.Content {
		walk(block.Content)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func jiraError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"errorMessages": []string{message}})
}

func jiraFieldError(w http.ResponseWriter, status int, errors map[string]string) {
	writeJSON(w, status, map[string]any{
		"errorMessages": []string{},
		"errors":        errors,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("api3: encode response: %v", err)
	}
}
