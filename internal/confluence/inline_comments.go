package confluence

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type inlineCommentWrite struct {
	PageID, BlogPostID, ParentCommentID string
	Body                                json.RawMessage `json:"body"`
	InlineCommentProperties             struct {
		TextSelection           string `json:"textSelection"`
		TextSelectionMatchCount int    `json:"textSelectionMatchCount"`
		TextSelectionMatchIndex int    `json:"textSelectionMatchIndex"`
	} `json:"inlineCommentProperties"`
}

type inlineCommentUpdate struct {
	Body     json.RawMessage `json:"body"`
	Resolved *bool           `json:"resolved"`
	Version  struct {
		Number  int    `json:"number"`
		Message string `json:"message"`
	} `json:"version"`
}

func (h *Handler) inlineCommentBean(comment *models.WikiFooterComment, body bool) map[string]any {
	parentType, parentID := "pages", comment.PageID
	if comment.BlogPostID != "" {
		parentType, parentID = "blogposts", comment.BlogPostID
	}
	bean := map[string]any{
		"id": comment.ID, "status": "current", "title": "", "version": comment.Version,
		"resolutionStatus": comment.ResolutionStatus,
		"properties":       map[string]any{"inlineMarkerRef": comment.InlineMarkerRef, "inlineOriginalSelection": comment.InlineSelection},
		"_links":           map[string]string{"webui": "/spaces/" + comment.SpaceID + "/" + parentType + "/" + parentID + "#inline-comment-" + comment.ID, "base": h.BaseURL + "/wiki"},
	}
	if comment.ParentCommentID == "" {
		if comment.BlogPostID != "" {
			bean["blogPostId"] = comment.BlogPostID
		} else {
			bean["pageId"] = comment.PageID
		}
	} else {
		bean["parentCommentId"] = comment.ParentCommentID
	}
	if comment.ResolutionModifierID != "" {
		bean["resolutionLastModifierId"] = comment.ResolutionModifierID
		bean["resolutionLastModifiedAt"] = comment.ResolutionModifiedAt
	}
	if body {
		bean["body"] = map[string]any{"storage": comment.Body}
	}
	return bean
}

func (h *Handler) inlineComments(w http.ResponseWriter, r *http.Request, ws, actor, pageID, parentID string) {
	if !inlineCommentQuery(w, r, pageID != "") {
		return
	}
	var comments []*models.WikiFooterComment
	var err error
	if parentID != "" {
		comments, err = h.Store.WikiInlineCommentChildren(r.Context(), ws, actor, parentID)
	} else {
		comments, err = h.Store.WikiInlineComments(r.Context(), ws, actor, pageID)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if pageID != "" {
		filtered := comments[:0]
		for _, comment := range comments {
			if queryContains(r, "resolution-status", comment.ResolutionStatus) {
				filtered = append(filtered, comment)
			}
		}
		comments = filtered
	}
	if !commentStatusesAllowCurrent(r) {
		comments = nil
	}
	sortFooterComments(comments, r.URL.Query().Get("sort"))
	values := make([]any, 0, len(comments))
	for _, comment := range comments {
		values = append(values, h.inlineCommentBeanFormat(comment, r.URL.Query().Get("body-format")))
	}
	h.list(w, r, values)
}

func (h *Handler) blogInlineComments(w http.ResponseWriter, r *http.Request, ws, actor, blogPostID string) {
	if !inlineCommentQuery(w, r, true) {
		return
	}
	comments, err := h.Store.WikiBlogInlineComments(r.Context(), ws, actor, blogPostID)
	if err != nil {
		writeError(w, err)
		return
	}
	filtered := comments[:0]
	for _, comment := range comments {
		if queryContains(r, "resolution-status", comment.ResolutionStatus) {
			filtered = append(filtered, comment)
		}
	}
	sortFooterComments(filtered, r.URL.Query().Get("sort"))
	values := make([]any, 0, len(filtered))
	for _, comment := range filtered {
		values = append(values, h.inlineCommentBeanFormat(comment, r.URL.Query().Get("body-format")))
	}
	h.list(w, r, values)
}

func (h *Handler) createInlineComment(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	var in inlineCommentWrite
	if !decode(w, r, &in) {
		return
	}
	body, err := decodeCommentBody(in.Body)
	if err != nil {
		failure(w, 400, err.Error())
		return
	}
	comment, err := h.Commands.CreateWikiInlineComment(r.Context(), ws, actor, models.WikiFooterComment{PageID: in.PageID, BlogPostID: in.BlogPostID, ParentCommentID: in.ParentCommentID, Body: body, InlineSelection: in.InlineCommentProperties.TextSelection, InlineMatchCount: in.InlineCommentProperties.TextSelectionMatchCount, InlineMatchIndex: in.InlineCommentProperties.TextSelectionMatchIndex})
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", h.BaseURL+"/wiki/api/v2/inline-comments/"+comment.ID)
	respond(w, 201, h.inlineCommentBean(comment, true))
}

func queryBool(w http.ResponseWriter, r *http.Request, name string) (bool, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return false, true
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		failure(w, 400, name+" must be true or false.")
		return false, false
	}
	return value, true
}

func (h *Handler) updateInlineComment(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r) {
		return
	}
	var in inlineCommentUpdate
	if !decode(w, r, &in) {
		return
	}
	current, err := h.Store.WikiInlineComment(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	body := current.Body
	if len(in.Body) > 0 && string(in.Body) != "null" {
		body, err = decodeCommentBody(in.Body)
		if err != nil {
			failure(w, 400, err.Error())
			return
		}
	}
	comment, err := h.Commands.UpdateWikiInlineComment(r.Context(), ws, actor, models.WikiFooterComment{ID: id, Body: body, Version: models.WikiVersion{Number: in.Version.Number, Message: in.Version.Message}}, in.Resolved)
	if err != nil {
		if errors.Is(err, store.ErrProjectPermission) {
			failure(w, 404, "Content does not exist or you do not have permission to update it.")
			return
		}
		writeError(w, err)
		return
	}
	respond(w, 200, h.inlineCommentBean(comment, true))
}

func (h *Handler) deleteInlineComment(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r) {
		return
	}
	if err := h.Store.DeleteWikiInlineComment(r.Context(), ws, actor, id); err != nil {
		if errors.Is(err, store.ErrProjectPermission) {
			failure(w, 404, "Content does not exist or you do not have permission to delete it.")
			return
		}
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) inlineCommentVersions(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "body-format", "cursor", "limit", "sort") || !commentFormat(w, r, false) {
		return
	}
	order := r.URL.Query().Get("sort")
	if order != "" && order != "modified-date" && order != "-modified-date" {
		failure(w, 400, "Unsupported version sort order.")
		return
	}
	versions, err := h.Store.WikiInlineCommentVersions(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if order == "-modified-date" {
		slices.Reverse(versions)
	}
	values := make([]any, 0, len(versions))
	for _, version := range versions {
		values = append(values, footerCommentVersionBean(id, version, r.URL.Query().Get("body-format")))
	}
	h.list(w, r, values)
}

func (h *Handler) inlineCommentVersion(w http.ResponseWriter, r *http.Request, ws, actor, id, raw string) {
	if !supportedQuery(w, r) {
		return
	}
	number, err := strconv.Atoi(raw)
	if err != nil || number < 1 {
		failure(w, 400, "Version number must be positive.")
		return
	}
	version, err := h.Store.WikiInlineCommentVersion(r.Context(), ws, actor, id, number)
	if err != nil {
		writeError(w, err)
		return
	}
	bean := map[string]any{"number": version.Number, "message": version.Message, "minorEdit": version.MinorEdit, "authorId": version.AuthorID, "createdAt": version.CreatedAt, "contentTypeModified": false, "collaborators": []string{}}
	if number > 1 {
		bean["prevVersion"] = number - 1
	}
	current, err := h.Store.WikiInlineComment(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if number < current.Version.Number {
		bean["nextVersion"] = number + 1
	}
	respond(w, 200, bean)
}

func (h *Handler) inlineCommentOperations(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r) {
		return
	}
	_, err := h.Store.WikiInlineComment(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	canUpdate, err := h.Store.CanUpdateWikiComment(r.Context(), ws, actor, id, "inline")
	if err != nil {
		writeError(w, err)
		return
	}
	canDelete, err := h.Store.CanDeleteWikiComment(r.Context(), ws, actor, id, "inline")
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{"operations": commentOperations(canUpdate, canDelete)})
}

func (h *Handler) inlineCommentLikeCount(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r) {
		return
	}
	likes, err := h.Store.WikiInlineCommentLikes(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]int{"count": len(likes)})
}

func (h *Handler) inlineCommentLikeUsers(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "cursor", "limit") {
		return
	}
	likes, err := h.Store.WikiInlineCommentLikes(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, 0, len(likes))
	for _, accountID := range likes {
		values = append(values, map[string]string{"accountId": accountID})
	}
	h.list(w, r, values)
}
