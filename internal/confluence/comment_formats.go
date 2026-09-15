package confluence

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// Comments are written in the same body forms as pages and read in the same
// formats: a single comment in any primary format, collections and versions in
// storage or the document format.

func commentFormat(w http.ResponseWriter, r *http.Request, single bool) bool {
	_, ok := pageBodyFormat(w, r, single)
	return ok
}

func commentBodyIn(body models.WikiBody, format string) models.WikiBody {
	if format == "" || format == "storage" {
		return models.WikiBody{Representation: "storage", Value: body.Value}
	}
	target := format
	if target == "anonymous_export_view" {
		target = "export_view"
	}
	value, err := store.ConvertWikiBody(body.Value, "storage", target)
	if err != nil {
		return models.WikiBody{Representation: "storage", Value: body.Value}
	}
	return models.WikiBody{Representation: format, Value: value}
}

func (h *Handler) footerCommentBeanFormat(comment *models.WikiFooterComment, format string) map[string]any {
	bean := h.footerCommentBean(comment, false)
	if format != "" {
		bean["body"] = map[string]any{format: commentBodyIn(comment.Body, format)}
	}
	return bean
}

func (h *Handler) inlineCommentBeanFormat(comment *models.WikiFooterComment, format string) map[string]any {
	bean := h.inlineCommentBean(comment, false)
	if format != "" {
		bean["body"] = map[string]any{format: commentBodyIn(comment.Body, format)}
	}
	return bean
}

// commentStatusesAllowCurrent reports whether a status filter lets current
// comments through. A comment here is current until it is deleted, so a filter
// naming only other statuses finds nothing.
func commentStatusesAllowCurrent(r *http.Request) bool {
	if _, present := r.URL.Query()["status"]; !present {
		return true
	}
	return queryContains(r, "status", "current")
}

func validCommentStatuses(w http.ResponseWriter, r *http.Request) bool {
	for _, raw := range r.URL.Query()["status"] {
		for _, value := range strings.Split(raw, ",") {
			switch value {
			case "", "current", "archived", "trashed", "deleted", "historical", "draft":
			default:
				failure(w, 400, "Unsupported comment status.")
				return false
			}
		}
	}
	return true
}

func validCommentSort(w http.ResponseWriter, r *http.Request) bool {
	order := r.URL.Query().Get("sort")
	if order != "" && order != "created-date" && order != "-created-date" && order != "modified-date" && order != "-modified-date" {
		failure(w, 400, "Unsupported comment sort order.")
		return false
	}
	return true
}

func commentQuery(w http.ResponseWriter, r *http.Request, status bool) bool {
	allowed := []string{"body-format", "sort", "cursor", "limit"}
	if status {
		allowed = append(allowed, "status")
	}
	return supportedQuery(w, r, allowed...) && commentFormat(w, r, false) && validCommentSort(w, r) && (!status || validCommentStatuses(w, r))
}

func inlineCommentQuery(w http.ResponseWriter, r *http.Request, pageScoped bool) bool {
	allowed := []string{"body-format", "sort", "cursor", "limit"}
	if pageScoped {
		allowed = append(allowed, "status", "resolution-status")
	}
	if !supportedQuery(w, r, allowed...) || !commentFormat(w, r, false) || !validCommentSort(w, r) {
		return false
	}
	if pageScoped {
		if !validCommentStatuses(w, r) {
			return false
		}
		for _, raw := range r.URL.Query()["resolution-status"] {
			for _, status := range strings.Split(raw, ",") {
				if status != "" && status != "open" && status != "reopened" && status != "resolved" && status != "dangling" {
					failure(w, 400, "Unsupported resolution status.")
					return false
				}
			}
		}
	}
	return true
}

// decodeCommentBody reads a comment body in any form a page body is written
// in and keeps it as storage.
func decodeCommentBody(raw json.RawMessage) (models.WikiBody, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return models.WikiBody{}, errors.New("comment body is required")
	}
	body, problem := decodePageBody(raw)
	if problem != "" {
		return models.WikiBody{}, errors.New(problem)
	}
	return body, nil
}

type commentIncludes struct {
	properties, operations, likes, versions, version bool
}

func parseCommentIncludes(w http.ResponseWriter, r *http.Request) (commentIncludes, bool) {
	includes := commentIncludes{version: true}
	for key, target := range map[string]*bool{"include-properties": &includes.properties, "include-operations": &includes.operations, "include-likes": &includes.likes, "include-versions": &includes.versions} {
		value, ok := queryBool(w, r, key)
		if !ok {
			return includes, false
		}
		*target = value
	}
	if _, present := r.URL.Query()["include-version"]; present {
		value, ok := queryBool(w, r, "include-version")
		if !ok {
			return includes, false
		}
		includes.version = value
	}
	return includes, true
}

// addCommentIncludes fills what a single comment read asked to include.
func (h *Handler) addCommentIncludes(r *http.Request, ws, actor, id, commentType string, bean map[string]any, includes commentIncludes) error {
	if !includes.version {
		delete(bean, "version")
	}
	if includes.properties {
		properties, err := h.Store.WikiCommentProperties(r.Context(), ws, actor, id, "")
		if err != nil {
			return err
		}
		values := make([]any, len(properties))
		for i := range properties {
			values[i] = properties[i]
		}
		bean["properties"] = resultsWrap(values)
	}
	if includes.operations {
		canUpdate, err := h.Store.CanUpdateWikiComment(r.Context(), ws, actor, id, commentType)
		if err != nil {
			return err
		}
		canDelete, err := h.Store.CanDeleteWikiComment(r.Context(), ws, actor, id, commentType)
		if err != nil {
			return err
		}
		bean["operations"] = resultsWrap(commentOperations(canUpdate, canDelete))
	}
	if includes.likes {
		var likes []string
		var err error
		if commentType == "inline" {
			likes, err = h.Store.WikiInlineCommentLikes(r.Context(), ws, actor, id)
		} else {
			likes, err = h.Store.WikiFooterCommentLikes(r.Context(), ws, actor, id)
		}
		if err != nil {
			return err
		}
		values := make([]any, len(likes))
		for i, accountID := range likes {
			values[i] = map[string]string{"accountId": accountID}
		}
		bean["likes"] = resultsWrap(values)
	}
	if includes.versions {
		var versions []models.WikiFooterCommentVersion
		var err error
		if commentType == "inline" {
			versions, err = h.Store.WikiInlineCommentVersions(r.Context(), ws, actor, id)
		} else {
			versions, err = h.Store.WikiFooterCommentVersions(r.Context(), ws, actor, id)
		}
		if err != nil {
			return err
		}
		values := make([]any, len(versions))
		for i := range versions {
			values[i] = versions[i]
		}
		bean["versions"] = resultsWrap(values)
	}
	return nil
}

func (h *Handler) footerComment(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "body-format", "version", "include-properties", "include-operations", "include-likes", "include-versions", "include-version") || !commentFormat(w, r, true) {
		return
	}
	includes, ok := parseCommentIncludes(w, r)
	if !ok {
		return
	}
	comment, err := h.Store.WikiFooterComment(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if rawVersion := r.URL.Query().Get("version"); rawVersion != "" {
		number, parseErr := strconv.Atoi(rawVersion)
		if parseErr != nil || number < 1 {
			failure(w, 400, "Version number must be a positive integer.")
			return
		}
		version, versionErr := h.Store.WikiFooterCommentVersion(r.Context(), ws, actor, id, number)
		if versionErr != nil {
			writeError(w, versionErr)
			return
		}
		comment.Body = version.Body
		comment.Version = version.WikiVersion
	}
	bean := h.footerCommentBeanFormat(comment, r.URL.Query().Get("body-format"))
	if err := h.addCommentIncludes(r, ws, actor, id, "footer", bean, includes); err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, bean)
}

func (h *Handler) inlineComment(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "body-format", "version", "include-properties", "include-operations", "include-likes", "include-versions", "include-version") || !commentFormat(w, r, true) {
		return
	}
	includes, ok := parseCommentIncludes(w, r)
	if !ok {
		return
	}
	comment, err := h.Store.WikiInlineComment(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if raw := r.URL.Query().Get("version"); raw != "" {
		number, parseErr := strconv.Atoi(raw)
		if parseErr != nil || number < 1 {
			failure(w, 400, "Version number must be positive.")
			return
		}
		version, versionErr := h.Store.WikiInlineCommentVersion(r.Context(), ws, actor, id, number)
		if versionErr != nil {
			writeError(w, versionErr)
			return
		}
		comment.Body, comment.Version = version.Body, version.WikiVersion
	}
	bean := h.inlineCommentBeanFormat(comment, r.URL.Query().Get("body-format"))
	if err := h.addCommentIncludes(r, ws, actor, id, "inline", bean, includes); err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, bean)
}
