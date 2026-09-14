package api3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// Issue comments and comment properties.

// commentInput is a comment body with its optional visibility and properties.
type commentInput struct {
	Body              json.RawMessage
	VisibilityPresent bool
	Visibility        store.CommentVisibility
	Properties        []entityPropertyInput
}

type entityPropertyInput struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// parseCommentInput reads a Jira comment request. On create the body is
// required; visibility names a group or role by identifier or value.
func (h *Handler) parseCommentInput(r *http.Request, workspaceID string, raw json.RawMessage, requireBody bool) (commentInput, error) {
	var request struct {
		Body       json.RawMessage       `json:"body"`
		Visibility *json.RawMessage      `json:"visibility"`
		Properties []entityPropertyInput `json:"properties"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		return commentInput{}, errors.New("The comment is not valid JSON.")
	}
	input := commentInput{Body: request.Body, Properties: request.Properties}
	if len(input.Body) == 0 || string(input.Body) == "null" {
		if requireBody {
			return commentInput{}, errors.New("Comment body can not be empty!")
		}
		input.Body = nil
	} else {
		var doc struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(input.Body, &doc) != nil || doc.Type != "doc" {
			return commentInput{}, errors.New("Comment body must be an Atlassian Document Format document.")
		}
	}
	if request.Visibility != nil {
		input.VisibilityPresent = true
		if string(*request.Visibility) != "null" {
			var visibility struct {
				Type       string `json:"type"`
				Value      string `json:"value"`
				Identifier string `json:"identifier"`
			}
			if err := json.Unmarshal(*request.Visibility, &visibility); err != nil {
				return commentInput{}, errors.New("The comment visibility is not valid.")
			}
			resolved, _, err := h.Store.ResolveCommentVisibility(r.Context(), workspaceID, visibility.Type, visibility.Identifier, visibility.Value)
			if err != nil {
				return commentInput{}, errors.New(trimErrorPrefix(err))
			}
			input.Visibility = resolved
		}
	}
	for _, property := range input.Properties {
		if strings.TrimSpace(property.Key) == "" || len(property.Value) == 0 {
			return commentInput{}, errors.New("Each comment property needs a key and a value.")
		}
	}
	return input, nil
}

// createCommentOn adds a comment to an issue, applying Jira's Add comments
// permission and per-issue limit, then stores its properties.
func (h *Handler) createCommentOn(r *http.Request, workspaceID, actorID string, issue *models.Issue, input commentInput) (*models.Comment, *jerr) {
	if allowed, err := h.Store.HasProjectPermission(r.Context(), workspaceID, actorID, issue.ProjectID, issue.ID, "ADD_COMMENTS"); err != nil || !allowed {
		return nil, &jerr{http.StatusBadRequest, "You do not have the permission to comment on this issue.", nil}
	}
	if count, err := h.Store.CommentCount(r.Context(), issue.ID); err != nil || count >= store.IssueEntityLimits["comment"] {
		return nil, &jerr{http.StatusRequestEntityTooLarge, "The per-issue limit for comments has been breached.", nil}
	}
	comment, _, err := h.Commands.AddComment(r.Context(), commands.AddCommentInput{
		ActorID: actorID, WorkspaceID: workspaceID, IssueIDOrKey: issue.ID, Body: input.Body, Visibility: input.Visibility,
	})
	if err != nil {
		if errors.Is(err, commands.ErrIssueArchived) {
			return nil, &jerr{http.StatusBadRequest, "The issue is archived and can't be changed.", nil}
		}
		return nil, &jerr{http.StatusBadRequest, err.Error(), nil}
	}
	for _, property := range input.Properties {
		if _, err = h.Store.SetCommentProperty(r.Context(), comment.ID, property.Key, property.Value); err != nil {
			return nil, &jerr{http.StatusBadRequest, trimErrorPrefix(err), nil}
		}
	}
	return comment, nil
}

// commentBeans renders comments for one reader, sharing user lookups.
type commentRenderer struct {
	h           *Handler
	ctx         context.Context
	workspaceID string
	readerID    string
	readerAdmin bool
	expand      string
	users       map[string]*models.User
}

func (h *Handler) newCommentRenderer(r *http.Request, workspaceID, readerID string) *commentRenderer {
	return h.newCommentRendererContext(r.Context(), workspaceID, readerID, r.URL.Query().Get("expand"))
}

func (h *Handler) newCommentRendererContext(ctx context.Context, workspaceID, readerID, expand string) *commentRenderer {
	admin, _ := h.Store.IsAdmin(ctx, workspaceID, readerID)
	return &commentRenderer{h: h, ctx: ctx, workspaceID: workspaceID, readerID: readerID, readerAdmin: admin, expand: expand, users: map[string]*models.User{}}
}

func (cr *commentRenderer) user(id, fallbackName string) map[string]any {
	u, ok := cr.users[id]
	if !ok {
		found, err := cr.h.Store.SiteUser(cr.ctx, cr.workspaceID, id)
		if err != nil {
			found = &models.User{ID: id, DisplayName: fallbackName, Active: false, AccountType: "atlassian"}
		}
		cr.users[id], u = found, found
	}
	return cr.h.fullUserBean(u, cr.readerAdmin || id == cr.readerID)
}

func (cr *commentRenderer) bean(issue *models.Issue, c *models.Comment) map[string]any {
	id := strconv.FormatInt(c.JiraID, 10)
	bean := map[string]any{
		"id":           id,
		"self":         cr.h.BaseURL + "/rest/api/3/issue/" + jiraIssueID(issue) + "/comment/" + id,
		"author":       cr.user(c.AuthorID, c.AuthorName),
		"updateAuthor": cr.user(c.UpdateAuthorID, c.UpdateAuthorName),
		"body":         c.Body,
		"created":      c.Created,
		"updated":      c.Updated,
		"jsdPublic":    c.VisibilityType == "",
	}
	if c.VisibilityType != "" {
		bean["visibility"] = map[string]any{"type": c.VisibilityType, "value": cr.h.Store.CommentVisibilityName(cr.ctx, cr.workspaceID, c), "identifier": c.VisibilityValue}
	}
	for _, option := range strings.Split(cr.expand, ",") {
		switch strings.TrimSpace(option) {
		case "renderedBody":
			bean["renderedBody"] = adf.ToHTML(c.Body)
		case "properties":
			properties, err := cr.h.Store.CommentProperties(cr.ctx, c.ID)
			values := []map[string]any{}
			if err == nil {
				keys := make([]string, 0, len(properties))
				for key := range properties {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					values = append(values, map[string]any{"key": key, "value": properties[key]})
				}
			}
			bean["properties"] = values
		}
	}
	return bean
}

// visibleComment finds a comment on an issue that the reader may see.
func (h *Handler) visibleComment(r *http.Request, workspaceID, readerID string, issue *models.Issue, ref string) (*models.Comment, *jerr) {
	c, err := h.Store.CommentByRef(r.Context(), workspaceID, ref)
	if err != nil || c.IssueID != issue.ID {
		return nil, &jerr{http.StatusNotFound, "Can not find a comment for the id: " + ref + ".", nil}
	}
	if visible, visErr := h.Store.CommentVisibleTo(r.Context(), workspaceID, issue.ProjectID, readerID, c); visErr != nil || !visible {
		return nil, &jerr{http.StatusNotFound, "Can not find a comment for the id: " + ref + ".", nil}
	}
	return c, nil
}

// issueCommentRoute serves /issue/{key}/comment and /issue/{key}/comment/{id}.
func (h *Handler) issueCommentRoute(w http.ResponseWriter, r *http.Request, idOrKey, commentRef string) {
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
	renderer := h.newCommentRenderer(r, workspaceID, actorID)
	if commentRef == "" {
		switch r.Method {
		case http.MethodGet:
			h.listIssueComments(w, r, workspaceID, actorID, issue, renderer)
		case http.MethodPost:
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
			if err != nil {
				jiraError(w, http.StatusBadRequest, "The comment is too large.")
				return
			}
			input, err := h.parseCommentInput(r, workspaceID, body, true)
			if err != nil {
				jiraFieldError(w, http.StatusBadRequest, map[string]string{"comment": err.Error()})
				return
			}
			comment, e := h.createCommentOn(r, workspaceID, actorID, issue, input)
			if e != nil {
				writeJerr(w, e)
				return
			}
			writeJSON(w, http.StatusCreated, renderer.bean(issue, comment))
		default:
			methodNotAllowed(w)
		}
		return
	}
	comment, e := h.visibleComment(r, workspaceID, actorID, issue, commentRef)
	if e != nil {
		writeJerr(w, e)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, renderer.bean(issue, comment))
	case http.MethodPut:
		ctx, e := h.requestOverrides(r, workspaceID, actorID, "overrideEditableFlag")
		if e != nil {
			writeJerr(w, e)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The comment is too large.")
			return
		}
		input, err := h.parseCommentInput(r, workspaceID, body, false)
		if err != nil {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"comment": err.Error()})
			return
		}
		updated, _, err := h.Commands.UpdateComment(ctx, commands.UpdateCommentInput{
			ActorID: actorID, WorkspaceID: workspaceID, CommentID: comment.ID, Body: input.Body,
			SetVisibility: input.VisibilityPresent, Visibility: input.Visibility,
		})
		if err != nil {
			issueCommandError(w, err)
			return
		}
		for _, property := range input.Properties {
			if _, err = h.Store.SetCommentProperty(r.Context(), updated.ID, property.Key, property.Value); err != nil {
				jiraError(w, http.StatusBadRequest, trimErrorPrefix(err))
				return
			}
		}
		writeJSON(w, http.StatusOK, renderer.bean(issue, updated))
	case http.MethodDelete:
		if _, err := h.Commands.DeleteComment(r.Context(), actorID, workspaceID, comment.ID); err != nil {
			issueCommandError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}

func (h *Handler) listIssueComments(w http.ResponseWriter, r *http.Request, workspaceID, readerID string, issue *models.Issue, renderer *commentRenderer) {
	startAt, maxResults, ok := pageParams(w, r, 100)
	if !ok {
		return
	}
	if maxResults > 5000 {
		maxResults = 5000
	}
	newestFirst := false
	switch r.URL.Query().Get("orderBy") {
	case "", "created", "+created":
	case "-created":
		newestFirst = true
	default:
		jiraError(w, http.StatusBadRequest, "orderBy must be created, +created or -created.")
		return
	}
	comments, err := h.Store.CommentsByIssue(r.Context(), issue.ID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	visible := []*models.Comment{}
	for _, c := range comments {
		if ok, visErr := h.Store.CommentVisibleTo(r.Context(), workspaceID, issue.ProjectID, readerID, c); visErr == nil && ok {
			visible = append(visible, c)
		}
	}
	if newestFirst {
		for i, j := 0, len(visible)-1; i < j; i, j = i+1, j-1 {
			visible[i], visible[j] = visible[j], visible[i]
		}
	}
	page := pageSlice(visible, startAt, maxResults)
	beans := make([]map[string]any, 0, len(page))
	for _, c := range page {
		beans = append(beans, renderer.bean(issue, c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"comments": beans, "startAt": startAt, "maxResults": maxResults, "total": len(visible)})
}

// commentsByIDs serves POST /comment/list.
func (h *Handler) commentsByIDs(w http.ResponseWriter, r *http.Request) {
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
		IDs []int64 `json:"ids"`
	}
	if !decodeMetadataRequest(w, r, &request) {
		return
	}
	if len(request.IDs) == 0 || len(request.IDs) > 1000 {
		jiraError(w, http.StatusBadRequest, "Between 1 and 1000 comment IDs are required.")
		return
	}
	comments, err := h.Store.CommentsByJiraIDs(r.Context(), workspaceID, request.IDs)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	renderer := h.newCommentRenderer(r, workspaceID, readerID)
	issues := map[string]*models.Issue{}
	values := []map[string]any{}
	for _, c := range comments {
		issue, cached := issues[c.IssueID]
		if !cached {
			if resolved, e := h.resolveIssue(r, workspaceID, c.IssueID); e == nil {
				issue = resolved
			}
			issues[c.IssueID] = issue
		}
		if issue == nil {
			continue
		}
		if visible, visErr := h.Store.CommentVisibleTo(r.Context(), workspaceID, issue.ProjectID, readerID, c); visErr != nil || !visible {
			continue
		}
		values = append(values, renderer.bean(issue, c))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"self": h.BaseURL + "/rest/api/3/comment/list", "isLast": true, "maxResults": 1000, "startAt": 0, "total": len(values), "values": values,
	})
}

// commentPropertyRoute serves /comment/{id}/properties and /comment/{id}/properties/{key}.
func (h *Handler) commentPropertyRoute(w http.ResponseWriter, r *http.Request, commentRef, key string) {
	workspaceID, actorID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if !numericID(commentRef) {
		jiraError(w, http.StatusBadRequest, "The comment ID is invalid.")
		return
	}
	c, err := h.Store.CommentByRef(r.Context(), workspaceID, commentRef)
	if err != nil {
		jiraError(w, http.StatusNotFound, "The comment was not found.")
		return
	}
	issue, e := h.resolveIssue(r, workspaceID, c.IssueID)
	if e != nil {
		jiraError(w, http.StatusNotFound, "The comment was not found.")
		return
	}
	if visible, visErr := h.Store.CommentVisibleTo(r.Context(), workspaceID, issue.ProjectID, actorID, c); visErr != nil || !visible {
		jiraError(w, http.StatusNotFound, "The comment was not found.")
		return
	}
	canEdit := func() bool {
		permissions := []string{"EDIT_ALL_COMMENTS"}
		if c.AuthorID == actorID {
			permissions = append(permissions, "EDIT_OWN_COMMENTS")
		}
		for _, permission := range permissions {
			if allowed, permErr := h.Store.HasProjectPermission(r.Context(), workspaceID, actorID, issue.ProjectID, issue.ID, permission); permErr == nil && allowed {
				return true
			}
		}
		jiraError(w, http.StatusForbidden, "You do not have the permission to edit this comment.")
		return false
	}
	if key == "" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		keys, err := h.Store.CommentPropertyKeys(r.Context(), c.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		values := make([]map[string]string, 0, len(keys))
		for _, k := range keys {
			values = append(values, map[string]string{"key": k, "self": fmt.Sprintf("%s/rest/api/3/comment/%d/properties/%s", h.BaseURL, c.JiraID, k)})
		}
		writeJSON(w, http.StatusOK, map[string]any{"keys": values})
		return
	}
	switch r.Method {
	case http.MethodGet:
		value, err := h.Store.CommentProperty(r.Context(), c.ID, key)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The property "+key+" was not found.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": value})
	case http.MethodPut:
		if !canEdit() {
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The property value is too large.")
			return
		}
		created, err := h.Store.SetCommentProperty(r.Context(), c.ID, key, body)
		if err != nil {
			jiraError(w, http.StatusBadRequest, trimErrorPrefix(err))
			return
		}
		if created {
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		if !canEdit() {
			return
		}
		if err := h.Store.DeleteCommentProperty(r.Context(), c.ID, key); err != nil {
			jiraError(w, http.StatusNotFound, "The property "+key+" was not found.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		methodNotAllowed(w)
	}
}
