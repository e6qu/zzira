// Package confluence implements the delivered Confluence Cloud v2 contract.
package confluence

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Handler struct {
	Store                  *store.Store
	Commands               *commands.Service
	Blobs                  attachments.Store
	WorkspaceSlug, BaseURL string
}

func respond(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		log.Printf("confluence response: %v", err)
	}
}
func failure(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]any{"errors": []any{map[string]any{"status": status, "code": http.StatusText(status), "title": message}}})
}
func writeError(w http.ResponseWriter, err error) {
	var pgerr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		failure(w, 404, "Content does not exist or you do not have permission to view it.")
	case errors.Is(err, store.ErrProjectPermission):
		failure(w, 403, err.Error())
	case errors.Is(err, store.ErrWikiConflict):
		failure(w, 409, err.Error())
	case errors.Is(err, store.ErrWikiContentConflict):
		failure(w, 409, err.Error())
	case errors.Is(err, store.ErrWikiBlogPostConflict):
		failure(w, 409, err.Error())
	case errors.Is(err, store.ErrWikiCommentConflict):
		failure(w, 409, err.Error())
	case errors.Is(err, store.ErrWikiPropertyConflict):
		failure(w, 409, err.Error())
	case errors.Is(err, store.ErrWikiPermissionValidation):
		failure(w, 400, err.Error())
	case errors.Is(err, store.ErrWikiUserValidation):
		failure(w, 400, err.Error())
	case errors.Is(err, store.ErrWikiMoveValidation):
		failure(w, 400, err.Error())
	case errors.Is(err, store.ErrWikiContentStateValidation):
		failure(w, 400, err.Error())
	case errors.Is(err, store.ErrWikiValidation):
		failure(w, 400, err.Error())
	case errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "wiki_attachment_properties_attachment_id_key_key":
		failure(w, 400, "An attachment property with this key already exists.")
	case errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "wiki_content_properties_content_id_key_key":
		failure(w, 400, "A content property with this key already exists.")
	case errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "wiki_blog_post_properties_blog_post_id_key_key":
		failure(w, 400, "A blog post property with this key already exists.")
	case errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "wiki_page_properties_page_id_key_key":
		failure(w, 400, "A page property with this key already exists.")
	case errors.As(err, &pgerr) && pgerr.Code == "23505":
		failure(w, 400, "A space with this key or published content with this title already exists.")
	default:
		log.Print("confluence operation: ", strconv.Quote(err.Error()))
		failure(w, 500, "Could not complete the wiki operation.")
	}
}
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		failure(w, 400, "Invalid request: "+err.Error())
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		failure(w, 400, "Expected one JSON object.")
		return false
	}
	return true
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	actor, err := authn.Identify(r.Context(), h.Store, r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="zzira"`)
		failure(w, 401, "Authentication required.")
		return
	}
	ws, err := h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		writeError(w, err)
		return
	}
	member, err := h.Store.IsMember(r.Context(), ws, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	if !member {
		failure(w, 403, "Workspace membership required.")
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/wiki/api/v2/"), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "space-roles" && r.Method == "GET":
		h.spaceRoles(w, r, ws, actor)
	case len(parts) == 1 && parts[0] == "space-roles" && r.Method == "POST":
		h.createSpaceRole(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "space-roles" && r.Method == "GET":
		h.spaceRole(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "space-roles" && r.Method == "PUT":
		h.updateSpaceRole(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "space-roles" && r.Method == "DELETE":
		h.deleteSpaceRole(w, r, ws, actor, parts[1])
	case len(parts) == 1 && parts[0] == "classification-levels" && r.Method == "GET":
		h.classificationLevels(w, r)
	case len(parts) == 1 && parts[0] == "spaces" && r.Method == "GET":
		h.spaces(w, r, ws, actor)
	case len(parts) == 1 && parts[0] == "spaces" && r.Method == "POST":
		h.createSpace(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "spaces" && r.Method == "GET":
		if !validPageID(w, parts[1]) || !supportedQuery(w, r, "description-format", "include-icon", "include-operations", "include-properties", "include-permissions", "include-role-assignments", "include-labels") {
			return
		}
		format := r.URL.Query().Get("description-format")
		if format != "" && format != "plain" && format != "view" {
			failure(w, 400, "Unsupported space description format.")
			return
		}
		flags := map[string]bool{}
		for _, key := range []string{"include-icon", "include-operations", "include-properties", "include-permissions", "include-role-assignments", "include-labels"} {
			value, ok := queryBool(w, r, key)
			if !ok {
				return
			}
			flags[key] = value
		}
		space, err := h.Store.WikiSpace(r.Context(), ws, actor, parts[1])
		if err != nil {
			writeError(w, err)
			return
		}
		bean := h.spaceBean(space, format, flags["include-icon"])
		if flags["include-operations"] {
			operations, operationErr := h.spaceOperationValues(r, ws, actor, space.ID)
			if operationErr != nil {
				writeError(w, operationErr)
				return
			}
			bean["operations"] = map[string]any{"results": operations, "meta": map[string]any{"hasMore": false}, "_links": map[string]any{}}
		}
		if flags["include-labels"] {
			labels, labelErr := h.Store.WikiSpaceLabels(r.Context(), ws, actor, space.ID, false)
			if labelErr != nil {
				writeError(w, labelErr)
				return
			}
			values := make([]any, 0, len(labels))
			for _, label := range labels {
				values = append(values, label)
			}
			bean["labels"] = map[string]any{"results": values, "meta": map[string]any{"hasMore": false}, "_links": map[string]any{}}
		}
		if flags["include-properties"] {
			properties, propertyErr := h.Store.WikiSpaceProperties(r.Context(), ws, actor, space.ID, "")
			if propertyErr != nil {
				writeError(w, propertyErr)
				return
			}
			values := make([]any, len(properties))
			for i := range properties {
				values[i] = properties[i]
			}
			bean["properties"] = map[string]any{"results": values, "meta": map[string]any{"hasMore": false}, "_links": map[string]any{}}
		}
		if flags["include-permissions"] {
			bean["permissions"] = map[string]any{"results": spacePermissionValues(space), "meta": map[string]any{"hasMore": false}, "_links": map[string]any{}}
		}
		if flags["include-role-assignments"] {
			assignments, assignmentErr := h.Store.WikiSpaceRoleAssignments(r.Context(), ws, actor, space.ID)
			if assignmentErr != nil {
				writeError(w, assignmentErr)
				return
			}
			values := make([]any, len(assignments))
			for i := range assignments {
				values[i] = roleAssignmentBean(assignments[i])
			}
			bean["roleAssignments"] = map[string]any{"results": values, "meta": map[string]any{"hasMore": false}, "_links": map[string]any{}}
		}
		respond(w, 200, bean)
	case len(parts) == 3 && parts[0] == "spaces" && parts[2] == "operations" && r.Method == "GET":
		h.spaceOperations(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "spaces" && parts[2] == "permissions" && r.Method == "GET":
		h.spacePermissions(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "spaces" && parts[2] == "role-assignments" && r.Method == "GET":
		h.spaceRoleAssignments(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "spaces" && parts[2] == "role-assignments" && r.Method == "POST":
		h.setSpaceRoleAssignments(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "spaces" && parts[2] == "classification-level" && parts[3] == "default" && r.Method == "GET":
		h.spaceDefaultClassification(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "spaces" && parts[2] == "classification-level" && parts[3] == "default" && r.Method == "PUT":
		h.setSpaceDefaultClassification(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "spaces" && parts[2] == "classification-level" && parts[3] == "default" && r.Method == "DELETE":
		h.deleteSpaceDefaultClassification(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "spaces" && parts[2] == "properties" && r.Method == "GET":
		h.spaceProperties(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "spaces" && parts[2] == "properties" && r.Method == "POST":
		h.createSpaceProperty(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "spaces" && parts[2] == "properties" && r.Method == "GET":
		h.spaceProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "spaces" && parts[2] == "properties" && r.Method == "PUT":
		h.updateSpaceProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "spaces" && parts[2] == "properties" && r.Method == "DELETE":
		h.deleteSpaceProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "spaces" && parts[2] == "pages" && r.Method == "GET":
		if _, err := h.Store.WikiSpace(r.Context(), ws, actor, parts[1]); err != nil {
			writeError(w, err)
			return
		}
		h.pages(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "spaces" && parts[2] == "blogposts" && r.Method == "GET":
		h.blogPosts(w, r, ws, actor, parts[1])
	case len(parts) == 1 && parts[0] == "blogposts" && r.Method == "GET":
		h.blogPosts(w, r, ws, actor, "")
	case len(parts) == 1 && parts[0] == "blogposts" && r.Method == "POST":
		h.saveBlogPost(w, r, ws, actor, "")
	case len(parts) == 2 && parts[0] == "blogposts" && r.Method == "GET":
		h.blogPost(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "blogposts" && r.Method == "PUT":
		h.saveBlogPost(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "blogposts" && r.Method == "DELETE":
		h.deleteBlogPost(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "blogposts" && parts[2] == "versions" && r.Method == "GET":
		h.blogPostVersions(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "blogposts" && parts[2] == "versions" && r.Method == "GET":
		h.blogPostVersion(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "blogposts" && parts[2] == "labels" && r.Method == "GET":
		h.blogPostLabels(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "blogposts" && parts[2] == "likes" && parts[3] == "count" && r.Method == "GET":
		h.blogPostLikeCount(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "blogposts" && parts[2] == "likes" && parts[3] == "users" && r.Method == "GET":
		h.blogPostLikeUsers(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "blogposts" && parts[2] == "properties" && r.Method == "GET":
		h.blogPostProperties(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "blogposts" && parts[2] == "properties" && r.Method == "POST":
		h.createBlogPostProperty(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "blogposts" && parts[2] == "properties" && r.Method == "GET":
		h.blogPostProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "blogposts" && parts[2] == "properties" && r.Method == "PUT":
		h.updateBlogPostProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "blogposts" && parts[2] == "properties" && r.Method == "DELETE":
		h.deleteBlogPostProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "blogposts" && parts[2] == "operations" && r.Method == "GET":
		h.blogPostOperations(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "blogposts" && parts[2] == "classification-level" && r.Method == "GET":
		h.blogPostClassification(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "blogposts" && parts[2] == "classification-level" && r.Method == "PUT":
		h.setBlogPostClassification(w, r, ws, actor, parts[1], false)
	case len(parts) == 4 && parts[0] == "blogposts" && parts[2] == "classification-level" && parts[3] == "reset" && r.Method == "POST":
		h.setBlogPostClassification(w, r, ws, actor, parts[1], true)
	case len(parts) == 3 && parts[0] == "blogposts" && parts[2] == "custom-content" && r.Method == "GET":
		h.blogPostCustomContent(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "blogposts" && parts[2] == "attachments" && r.Method == "GET":
		h.blogAttachments(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "blogposts" && parts[2] == "footer-comments" && r.Method == "GET":
		h.blogFooterComments(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "blogposts" && parts[2] == "inline-comments" && r.Method == "GET":
		h.blogInlineComments(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "blogposts" && parts[2] == "redact" && r.Method == "POST":
		h.redactBlogPost(w, r, ws, actor, parts[1])
	case len(parts) == 1 && parts[0] == "pages" && r.Method == "GET":
		h.pages(w, r, ws, actor, "")
	case len(parts) == 1 && parts[0] == "pages" && r.Method == "POST":
		h.savePage(w, r, ws, actor, "")
	case len(parts) == 2 && parts[0] == "pages" && r.Method == "PUT":
		h.savePage(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "pages" && r.Method == "GET":
		h.pageByID(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "pages" && r.Method == "DELETE":
		if !supportedQuery(w, r) {
			return
		}
		page, err := h.Store.WikiPage(r.Context(), ws, actor, parts[1])
		if err != nil {
			writeError(w, err)
			return
		}
		if page.Status == "trashed" {
			failure(w, 400, "Permanent deletion is not implemented.")
			return
		}
		page.Status = "trashed"
		page.Version.Number++
		page.Version.Message = "Moved to trash"
		if _, err := h.Commands.SaveWikiPage(r.Context(), ws, actor, *page); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(204)
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "versions" && r.Method == "GET":
		if !supportedQuery(w, r, "limit", "cursor", "sort", "body-format") || !storageFormat(w, r) {
			return
		}
		versions, err := h.Store.WikiVersionsSorted(r.Context(), ws, actor, parts[1], r.URL.Query().Get("sort"))
		if err != nil {
			writeError(w, err)
			return
		}
		values := make([]any, 0, len(versions))
		for _, v := range versions {
			values = append(values, v)
		}
		h.list(w, r, values)
	case len(parts) == 4 && parts[0] == "pages" && parts[2] == "versions" && r.Method == "GET":
		h.pageVersion(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "title" && r.Method == "PUT":
		h.updatePageTitle(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "children" && r.Method == "GET":
		h.pageChildren(w, r, ws, actor, parts[1], false)
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "direct-children" && r.Method == "GET":
		h.pageChildren(w, r, ws, actor, parts[1], true)
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "ancestors" && r.Method == "GET":
		h.pageAncestors(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "descendants" && r.Method == "GET":
		h.pageDescendants(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "footer-comments" && r.Method == "GET":
		h.footerComments(w, r, ws, actor, parts[1], "")
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "inline-comments" && r.Method == "GET":
		h.inlineComments(w, r, ws, actor, parts[1], "")
	case len(parts) == 4 && parts[0] == "pages" && parts[2] == "likes" && parts[3] == "count" && r.Method == "GET":
		h.pageLikeCount(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "pages" && parts[2] == "likes" && parts[3] == "users" && r.Method == "GET":
		h.pageLikeUsers(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "operations" && r.Method == "GET":
		h.pageOperations(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "classification-level" && r.Method == "GET":
		h.pageClassification(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "classification-level" && r.Method == "PUT":
		h.setPageClassification(w, r, ws, actor, parts[1], false)
	case len(parts) == 4 && parts[0] == "pages" && parts[2] == "classification-level" && parts[3] == "reset" && r.Method == "POST":
		h.setPageClassification(w, r, ws, actor, parts[1], true)
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "custom-content" && r.Method == "GET":
		h.pageCustomContent(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "properties" && r.Method == "GET":
		h.pageProperties(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "properties" && r.Method == "POST":
		h.createPageProperty(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "pages" && parts[2] == "properties" && r.Method == "GET":
		h.pageProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "pages" && parts[2] == "properties" && r.Method == "PUT":
		h.updatePageProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "pages" && parts[2] == "properties" && r.Method == "DELETE":
		h.deletePageProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "redact" && r.Method == "POST":
		h.redactPage(w, r, ws, actor, parts[1])
	case len(parts) == 1 && parts[0] == "inline-comments" && r.Method == "GET":
		h.inlineComments(w, r, ws, actor, "", "")
	case len(parts) == 1 && parts[0] == "inline-comments" && r.Method == "POST":
		h.createInlineComment(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "inline-comments" && r.Method == "GET":
		h.inlineComment(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "inline-comments" && r.Method == "PUT":
		h.updateInlineComment(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "inline-comments" && r.Method == "DELETE":
		h.deleteInlineComment(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "inline-comments" && parts[2] == "children" && r.Method == "GET":
		h.inlineComments(w, r, ws, actor, "", parts[1])
	case len(parts) == 3 && parts[0] == "inline-comments" && parts[2] == "versions" && r.Method == "GET":
		h.inlineCommentVersions(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "inline-comments" && parts[2] == "versions" && r.Method == "GET":
		h.inlineCommentVersion(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "inline-comments" && parts[2] == "operations" && r.Method == "GET":
		h.inlineCommentOperations(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "inline-comments" && parts[2] == "likes" && parts[3] == "count" && r.Method == "GET":
		h.inlineCommentLikeCount(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "inline-comments" && parts[2] == "likes" && parts[3] == "users" && r.Method == "GET":
		h.inlineCommentLikeUsers(w, r, ws, actor, parts[1])
	case len(parts) == 1 && parts[0] == "tasks" && r.Method == "GET":
		h.tasks(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "tasks" && r.Method == "GET":
		h.task(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "tasks" && r.Method == "PUT":
		h.updateTask(w, r, ws, actor, parts[1])
	case len(parts) == 1 && parts[0] == "footer-comments" && r.Method == "GET":
		h.footerComments(w, r, ws, actor, "", "")
	case len(parts) == 1 && parts[0] == "footer-comments" && r.Method == "POST":
		h.createFooterComment(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "footer-comments" && r.Method == "GET":
		h.footerComment(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "footer-comments" && r.Method == "PUT":
		h.updateFooterComment(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "footer-comments" && r.Method == "DELETE":
		h.deleteFooterComment(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "footer-comments" && parts[2] == "children" && r.Method == "GET":
		h.footerComments(w, r, ws, actor, "", parts[1])
	case len(parts) == 3 && parts[0] == "footer-comments" && parts[2] == "versions" && r.Method == "GET":
		h.footerCommentVersions(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "footer-comments" && parts[2] == "versions" && r.Method == "GET":
		h.footerCommentVersion(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "footer-comments" && parts[2] == "operations" && r.Method == "GET":
		h.footerCommentOperations(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "footer-comments" && parts[2] == "likes" && parts[3] == "count" && r.Method == "GET":
		h.footerCommentLikeCount(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "footer-comments" && parts[2] == "likes" && parts[3] == "users" && r.Method == "GET":
		h.footerCommentLikeUsers(w, r, ws, actor, parts[1])
	case len(parts) == 1 && parts[0] == "labels" && r.Method == "GET":
		h.labels(w, r, ws, actor)
	case len(parts) == 3 && parts[0] == "labels" && parts[2] == "pages" && r.Method == "GET":
		h.labelPages(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "labels" && parts[2] == "blogposts" && r.Method == "GET":
		h.labelBlogPosts(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "labels" && parts[2] == "attachments" && r.Method == "GET":
		h.labelAttachments(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "labels" && r.Method == "GET":
		h.contentLabels(w, r, ws, actor, "page", parts[1])
	case len(parts) == 3 && parts[0] == "spaces" && parts[2] == "labels" && r.Method == "GET":
		h.contentLabels(w, r, ws, actor, "space", parts[1])
	case len(parts) == 4 && parts[0] == "spaces" && parts[2] == "content" && parts[3] == "labels" && r.Method == "GET":
		h.contentLabels(w, r, ws, actor, "space-content", parts[1])
	case len(parts) == 1 && parts[0] == "attachments" && r.Method == "GET":
		h.attachments(w, r, ws, actor, "")
	case len(parts) == 2 && parts[0] == "attachments" && r.Method == "GET":
		h.attachment(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "attachments" && r.Method == "DELETE":
		h.deleteAttachment(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "attachments" && parts[2] == "operations" && r.Method == "GET":
		h.attachmentOperations(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "attachments" && parts[2] == "labels" && r.Method == "GET":
		h.attachmentLabels(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "attachments" && parts[2] == "footer-comments" && r.Method == "GET":
		h.attachmentFooterComments(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "attachments" && parts[2] == "thumbnail" && parts[3] == "download" && r.Method == "GET":
		h.attachmentThumbnail(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "attachments" && parts[2] == "properties" && r.Method == "GET":
		h.attachmentProperties(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "attachments" && parts[2] == "properties" && r.Method == "POST":
		h.createAttachmentProperty(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "attachments" && parts[2] == "properties" && r.Method == "GET":
		h.attachmentProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "attachments" && parts[2] == "properties" && r.Method == "PUT":
		h.updateAttachmentProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "attachments" && parts[2] == "properties" && r.Method == "DELETE":
		h.deleteAttachmentProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "attachments" && parts[2] == "versions" && r.Method == "GET":
		h.attachmentVersions(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "attachments" && parts[2] == "versions" && r.Method == "GET":
		h.attachmentVersion(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "pages" && parts[2] == "attachments" && r.Method == "GET":
		h.attachments(w, r, ws, actor, parts[1])
	case len(parts) == 1 && parts[0] == "space-permissions" && r.Method == "GET":
		h.spacePermissionsCatalogue(w, r, ws, actor)
	case len(parts) == 1 && parts[0] == "space-role-mode" && r.Method == "GET":
		h.spaceRoleMode(w, r, ws, actor)
	case len(parts) == 1 && parts[0] == "users-bulk" && r.Method == "POST":
		h.bulkUsersV2(w, r, ws, actor)
	case len(parts) == 1 && parts[0] == "custom-content" && r.Method == "GET":
		h.customContents(w, r, ws, actor, "")
	case len(parts) == 1 && parts[0] == "custom-content" && r.Method == "POST":
		h.createCustomContent(w, r, ws, actor)
	case len(parts) == 3 && parts[0] == "spaces" && parts[2] == "custom-content" && r.Method == "GET":
		h.customContents(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "custom-content" && r.Method == "GET":
		h.customContent(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "custom-content" && r.Method == "PUT":
		h.updateCustomContent(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "custom-content" && r.Method == "DELETE":
		if !validPageID(w, parts[1]) || !supportedQuery(w, r, "purge") {
			return
		}
		if err := h.Commands.DeleteWikiContent(r.Context(), ws, actor, parts[1], "custom"); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(204)
	case len(parts) == 3 && parts[0] == "custom-content" && parts[2] == "versions" && r.Method == "GET":
		h.customContentVersions(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "custom-content" && parts[2] == "versions" && r.Method == "GET":
		h.customContentVersion(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "custom-content" && parts[2] == "labels" && r.Method == "GET":
		h.customContentLabels(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "custom-content" && parts[2] == "operations" && r.Method == "GET":
		h.contentOperations(w, r, ws, actor, parts[1], "custom")
	case len(parts) == 3 && parts[0] == "custom-content" && parts[2] == "children" && r.Method == "GET":
		h.contentDescendants(w, r, ws, actor, parts[1], "custom", true)
	case len(parts) == 3 && parts[0] == "custom-content" && parts[2] == "attachments" && r.Method == "GET":
		h.customContentAttachments(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "custom-content" && parts[2] == "footer-comments" && r.Method == "GET":
		h.customContentFooterComments(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "custom-content" && parts[2] == "properties" && r.Method == "GET":
		h.contentProperties(w, r, ws, actor, parts[1], "custom")
	case len(parts) == 3 && parts[0] == "custom-content" && parts[2] == "properties" && r.Method == "POST":
		h.createContentProperty(w, r, ws, actor, parts[1], "custom")
	case len(parts) == 4 && parts[0] == "custom-content" && parts[2] == "properties" && r.Method == "GET":
		h.contentProperty(w, r, ws, actor, parts[1], parts[3], "custom")
	case len(parts) == 4 && parts[0] == "custom-content" && parts[2] == "properties" && r.Method == "PUT":
		h.updateContentProperty(w, r, ws, actor, parts[1], parts[3], "custom")
	case len(parts) == 4 && parts[0] == "custom-content" && parts[2] == "properties" && r.Method == "DELETE":
		h.deleteContentProperty(w, r, ws, actor, parts[1], parts[3], "custom")
	case len(parts) == 1 && parts[0] == "folders" && r.Method == "POST":
		h.createFolder(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "folders" && r.Method == "GET":
		h.folder(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "folders" && r.Method == "DELETE":
		h.deleteFolder(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "folders" && parts[2] == "ancestors" && r.Method == "GET":
		h.folderAncestors(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "folders" && parts[2] == "descendants" && r.Method == "GET":
		h.folderDescendants(w, r, ws, actor, parts[1], false)
	case len(parts) == 3 && parts[0] == "folders" && parts[2] == "direct-children" && r.Method == "GET":
		h.folderDescendants(w, r, ws, actor, parts[1], true)
	case len(parts) == 3 && parts[0] == "folders" && parts[2] == "operations" && r.Method == "GET":
		h.folderOperations(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "folders" && parts[2] == "properties" && r.Method == "GET":
		h.folderProperties(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "folders" && parts[2] == "properties" && r.Method == "POST":
		h.createFolderProperty(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "folders" && parts[2] == "properties" && r.Method == "GET":
		h.folderProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "folders" && parts[2] == "properties" && r.Method == "PUT":
		h.updateFolderProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "folders" && parts[2] == "properties" && r.Method == "DELETE":
		h.deleteFolderProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 1 && parts[0] == "embeds" && r.Method == "POST":
		h.createSmartLink(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "embeds" && r.Method == "GET":
		h.smartLink(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "embeds" && r.Method == "DELETE":
		h.deleteSmartLink(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "embeds" && parts[2] == "ancestors" && r.Method == "GET":
		h.smartLinkAncestors(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "embeds" && parts[2] == "descendants" && r.Method == "GET":
		h.smartLinkDescendants(w, r, ws, actor, parts[1], false)
	case len(parts) == 3 && parts[0] == "embeds" && parts[2] == "direct-children" && r.Method == "GET":
		h.smartLinkDescendants(w, r, ws, actor, parts[1], true)
	case len(parts) == 3 && parts[0] == "embeds" && parts[2] == "operations" && r.Method == "GET":
		h.smartLinkOperations(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "embeds" && parts[2] == "properties" && r.Method == "GET":
		h.smartLinkProperties(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "embeds" && parts[2] == "properties" && r.Method == "POST":
		h.createSmartLinkProperty(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "embeds" && parts[2] == "properties" && r.Method == "GET":
		h.smartLinkProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "embeds" && parts[2] == "properties" && r.Method == "PUT":
		h.updateSmartLinkProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "embeds" && parts[2] == "properties" && r.Method == "DELETE":
		h.deleteSmartLinkProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 1 && parts[0] == "databases" && r.Method == "POST":
		h.createDatabase(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "databases" && r.Method == "GET":
		h.database(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "databases" && r.Method == "DELETE":
		h.deleteDatabase(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "databases" && parts[2] == "ancestors" && r.Method == "GET":
		h.databaseAncestors(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "databases" && parts[2] == "descendants" && r.Method == "GET":
		h.databaseDescendants(w, r, ws, actor, parts[1], false)
	case len(parts) == 3 && parts[0] == "databases" && parts[2] == "direct-children" && r.Method == "GET":
		h.databaseDescendants(w, r, ws, actor, parts[1], true)
	case len(parts) == 3 && parts[0] == "databases" && parts[2] == "operations" && r.Method == "GET":
		h.databaseOperations(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "databases" && parts[2] == "classification-level" && r.Method == "GET":
		h.contentClassification(w, r, ws, actor, parts[1], "database")
	case len(parts) == 3 && parts[0] == "databases" && parts[2] == "classification-level" && r.Method == "PUT":
		h.setContentClassification(w, r, ws, actor, parts[1], "database", false)
	case len(parts) == 4 && parts[0] == "databases" && parts[2] == "classification-level" && parts[3] == "reset" && r.Method == "POST":
		h.setContentClassification(w, r, ws, actor, parts[1], "database", true)
	case len(parts) == 3 && parts[0] == "databases" && parts[2] == "properties" && r.Method == "GET":
		h.databaseProperties(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "databases" && parts[2] == "properties" && r.Method == "POST":
		h.createDatabaseProperty(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "databases" && parts[2] == "properties" && r.Method == "GET":
		h.databaseProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "databases" && parts[2] == "properties" && r.Method == "PUT":
		h.updateDatabaseProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "databases" && parts[2] == "properties" && r.Method == "DELETE":
		h.deleteDatabaseProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 1 && parts[0] == "whiteboards" && r.Method == "POST":
		h.createWhiteboard(w, r, ws, actor)
	case len(parts) == 2 && parts[0] == "whiteboards" && r.Method == "GET":
		h.whiteboard(w, r, ws, actor, parts[1])
	case len(parts) == 2 && parts[0] == "whiteboards" && r.Method == "DELETE":
		h.deleteWhiteboard(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "whiteboards" && parts[2] == "ancestors" && r.Method == "GET":
		h.whiteboardAncestors(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "whiteboards" && parts[2] == "descendants" && r.Method == "GET":
		h.whiteboardDescendants(w, r, ws, actor, parts[1], false)
	case len(parts) == 3 && parts[0] == "whiteboards" && parts[2] == "direct-children" && r.Method == "GET":
		h.whiteboardDescendants(w, r, ws, actor, parts[1], true)
	case len(parts) == 3 && parts[0] == "whiteboards" && parts[2] == "operations" && r.Method == "GET":
		h.whiteboardOperations(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "whiteboards" && parts[2] == "classification-level" && r.Method == "GET":
		h.contentClassification(w, r, ws, actor, parts[1], "whiteboard")
	case len(parts) == 3 && parts[0] == "whiteboards" && parts[2] == "classification-level" && r.Method == "PUT":
		h.setContentClassification(w, r, ws, actor, parts[1], "whiteboard", false)
	case len(parts) == 4 && parts[0] == "whiteboards" && parts[2] == "classification-level" && parts[3] == "reset" && r.Method == "POST":
		h.setContentClassification(w, r, ws, actor, parts[1], "whiteboard", true)
	case len(parts) == 3 && parts[0] == "whiteboards" && parts[2] == "properties" && r.Method == "GET":
		h.whiteboardProperties(w, r, ws, actor, parts[1])
	case len(parts) == 3 && parts[0] == "whiteboards" && parts[2] == "properties" && r.Method == "POST":
		h.createWhiteboardProperty(w, r, ws, actor, parts[1])
	case len(parts) == 4 && parts[0] == "whiteboards" && parts[2] == "properties" && r.Method == "GET":
		h.whiteboardProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "whiteboards" && parts[2] == "properties" && r.Method == "PUT":
		h.updateWhiteboardProperty(w, r, ws, actor, parts[1], parts[3])
	case len(parts) == 4 && parts[0] == "whiteboards" && parts[2] == "properties" && r.Method == "DELETE":
		h.deleteWhiteboardProperty(w, r, ws, actor, parts[1], parts[3])
	default:
		failure(w, 404, "This Confluence resource is not implemented.")
	}
}

type footerCommentWrite struct {
	PageID          string          `json:"pageId"`
	ParentCommentID string          `json:"parentCommentId"`
	BlogPostID      string          `json:"blogPostId"`
	AttachmentID    string          `json:"attachmentId"`
	CustomContentID string          `json:"customContentId"`
	Body            json.RawMessage `json:"body"`
}

type footerCommentUpdate struct {
	Body    json.RawMessage `json:"body"`
	Links   json.RawMessage `json:"_links"`
	Version struct {
		Number  int    `json:"number"`
		Message string `json:"message"`
	} `json:"version"`
}

func decodeCommentBody(raw json.RawMessage) (models.WikiBody, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return models.WikiBody{}, fmt.Errorf("comment body is required")
	}
	var flat models.WikiBody
	if err := json.Unmarshal(raw, &flat); err == nil && flat.Representation != "" {
		return flat, nil
	}
	var nested map[string]models.WikiBody
	if err := json.Unmarshal(raw, &nested); err != nil {
		return models.WikiBody{}, fmt.Errorf("invalid comment body")
	}
	if len(nested) != 1 {
		return models.WikiBody{}, fmt.Errorf("comment body must contain one representation")
	}
	body, ok := nested["storage"]
	if !ok {
		return models.WikiBody{}, fmt.Errorf("only the storage comment representation is currently supported")
	}
	if body.Representation == "" {
		body.Representation = "storage"
	}
	if body.Representation != "storage" {
		return models.WikiBody{}, fmt.Errorf("the nested representation must match storage")
	}
	return body, nil
}

func (h *Handler) footerCommentBean(comment *models.WikiFooterComment, body bool) map[string]any {
	parentType, parentID := "pages", comment.PageID
	if comment.BlogPostID != "" {
		parentType, parentID = "blogposts", comment.BlogPostID
	}
	bean := map[string]any{
		"id": comment.ID, "status": "current", "title": "",
		"createdAt": comment.CreatedAt, "version": comment.Version,
		"_links": map[string]string{"webui": "/spaces/" + comment.SpaceID + "/" + parentType + "/" + parentID + "#comment-" + comment.ID, "base": h.BaseURL + "/wiki"},
	}
	if comment.AttachmentID != "" {
		bean["attachmentId"] = comment.AttachmentID
	} else if comment.BlogPostID != "" {
		bean["blogPostId"] = comment.BlogPostID
	} else {
		bean["pageId"] = comment.PageID
	}
	if comment.ParentCommentID != "" {
		bean["parentCommentId"] = comment.ParentCommentID
	}
	if body {
		bean["body"] = map[string]any{"storage": comment.Body}
	}
	return bean
}

func (h *Handler) attachmentFooterComments(w http.ResponseWriter, r *http.Request, ws, actor, attachmentID string) {
	if !supportedQuery(w, r, "body-format", "sort", "cursor", "limit", "version") || !storageFormat(w, r) {
		return
	}
	if order := r.URL.Query().Get("sort"); order != "" && order != "created-date" && order != "-created-date" && order != "modified-date" && order != "-modified-date" {
		failure(w, 400, "Unsupported comment sort order.")
		return
	}
	if raw := r.URL.Query().Get("version"); raw != "" {
		version, err := strconv.Atoi(raw)
		if err != nil || version < 1 {
			failure(w, 400, "version must be positive.")
			return
		}
		if _, err = h.Store.WikiAttachmentVersion(r.Context(), ws, actor, attachmentID, version); err != nil {
			writeError(w, err)
			return
		}
	}
	comments, err := h.Store.WikiAttachmentFooterComments(r.Context(), ws, actor, attachmentID)
	if err != nil {
		writeError(w, err)
		return
	}
	sortFooterComments(comments, r.URL.Query().Get("sort"))
	values := make([]any, len(comments))
	for i, comment := range comments {
		values[i] = h.footerCommentBean(comment, r.URL.Query().Get("body-format") != "")
	}
	h.list(w, r, values)
}

func commentQuery(w http.ResponseWriter, r *http.Request, status bool) bool {
	allowed := []string{"body-format", "sort", "cursor", "limit"}
	if status {
		allowed = append(allowed, "status")
	}
	if !supportedQuery(w, r, allowed...) || !storageFormat(w, r) {
		return false
	}
	order := r.URL.Query().Get("sort")
	if order != "" && order != "created-date" && order != "-created-date" && order != "modified-date" && order != "-modified-date" {
		failure(w, 400, "Unsupported comment sort order.")
		return false
	}
	if status {
		for _, raw := range r.URL.Query()["status"] {
			for _, value := range strings.Split(raw, ",") {
				if value != "" && value != "current" {
					failure(w, 400, "Only current footer comments are supported.")
					return false
				}
			}
		}
	}
	return true
}

func sortFooterComments(comments []*models.WikiFooterComment, order string) {
	if order == "" || order == "created-date" {
		return
	}
	modified := strings.Contains(order, "modified")
	desc := strings.HasPrefix(order, "-")
	sort.SliceStable(comments, func(i, j int) bool {
		left, right := comments[i].CreatedAt, comments[j].CreatedAt
		if modified {
			left, right = comments[i].UpdatedAt, comments[j].UpdatedAt
		}
		if left == right {
			leftID, leftErr := strconv.ParseInt(comments[i].ID, 10, 64)
			rightID, rightErr := strconv.ParseInt(comments[j].ID, 10, 64)
			if leftErr == nil && rightErr == nil {
				if desc {
					return leftID > rightID
				}
				return leftID < rightID
			}
			left, right = comments[i].ID, comments[j].ID
		}
		if desc {
			return left > right
		}
		return left < right
	})
}

func (h *Handler) footerComments(w http.ResponseWriter, r *http.Request, ws, actor, pageID, parentID string) {
	if !commentQuery(w, r, pageID != "") {
		return
	}
	var comments []*models.WikiFooterComment
	var err error
	if parentID != "" {
		comments, err = h.Store.WikiFooterCommentChildren(r.Context(), ws, actor, parentID)
	} else {
		if pageID != "" {
			page, pageErr := h.Store.WikiPage(r.Context(), ws, actor, pageID)
			if pageErr != nil {
				writeError(w, pageErr)
				return
			}
			if page.Status != "current" {
				failure(w, 404, "Page not found with current status.")
				return
			}
		}
		comments, err = h.Store.WikiFooterComments(r.Context(), ws, actor, pageID)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	sortFooterComments(comments, r.URL.Query().Get("sort"))
	values := make([]any, 0, len(comments))
	for _, comment := range comments {
		values = append(values, h.footerCommentBean(comment, r.URL.Query().Get("body-format") != ""))
	}
	h.list(w, r, values)
}

func (h *Handler) blogFooterComments(w http.ResponseWriter, r *http.Request, ws, actor, blogPostID string) {
	if !commentQuery(w, r, true) {
		return
	}
	comments, err := h.Store.WikiBlogFooterComments(r.Context(), ws, actor, blogPostID)
	if err != nil {
		writeError(w, err)
		return
	}
	sortFooterComments(comments, r.URL.Query().Get("sort"))
	values := make([]any, 0, len(comments))
	for _, comment := range comments {
		values = append(values, h.footerCommentBean(comment, r.URL.Query().Get("body-format") != ""))
	}
	h.list(w, r, values)
}

func (h *Handler) footerComment(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "body-format", "version", "include-properties", "include-operations", "include-likes", "include-versions", "include-version") || !storageFormat(w, r) {
		return
	}
	for _, key := range []string{"include-properties", "include-operations", "include-likes", "include-versions", "include-version"} {
		if r.URL.Query().Get(key) != "" {
			failure(w, 400, key+" is not yet supported for footer comments.")
			return
		}
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
	respond(w, 200, h.footerCommentBean(comment, r.URL.Query().Get("body-format") != ""))
}

func footerCommentVersionBean(id string, version models.WikiFooterCommentVersion, body bool) map[string]any {
	comment := map[string]any{"id": id, "title": ""}
	if body {
		comment["body"] = map[string]any{"storage": version.Body}
	}
	return map[string]any{"number": version.Number, "message": version.Message, "minorEdit": version.MinorEdit, "authorId": version.AuthorID, "createdAt": version.CreatedAt, "comment": comment}
}

func (h *Handler) footerCommentVersions(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "body-format", "cursor", "limit", "sort") || !storageFormat(w, r) {
		return
	}
	order := r.URL.Query().Get("sort")
	if order != "" && order != "modified-date" && order != "-modified-date" {
		failure(w, 400, "Unsupported version sort order.")
		return
	}
	versions, err := h.Store.WikiFooterCommentVersions(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if order == "-modified-date" {
		slices.Reverse(versions)
	}
	values := make([]any, 0, len(versions))
	for _, version := range versions {
		values = append(values, footerCommentVersionBean(id, version, r.URL.Query().Get("body-format") != ""))
	}
	h.list(w, r, values)
}

func (h *Handler) footerCommentVersion(w http.ResponseWriter, r *http.Request, ws, actor, id, rawVersion string) {
	if !supportedQuery(w, r) {
		return
	}
	number, err := strconv.Atoi(rawVersion)
	if err != nil || number < 1 {
		failure(w, 400, "Version number must be a positive integer.")
		return
	}
	version, err := h.Store.WikiFooterCommentVersion(r.Context(), ws, actor, id, number)
	if err != nil {
		writeError(w, err)
		return
	}
	bean := map[string]any{"number": version.Number, "message": version.Message, "minorEdit": version.MinorEdit, "authorId": version.AuthorID, "createdAt": version.CreatedAt, "contentTypeModified": false, "collaborators": []string{}}
	if number > 1 {
		bean["prevVersion"] = number - 1
	}
	current, err := h.Store.WikiFooterComment(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if number < current.Version.Number {
		bean["nextVersion"] = number + 1
	}
	respond(w, 200, bean)
}

func commentOperations(canUpdate, canDelete bool) []any {
	operations := []any{map[string]string{"operation": "read", "targetType": "comment"}}
	if canUpdate {
		operations = append(operations, map[string]string{"operation": "update", "targetType": "comment"})
	}
	if canDelete {
		operations = append(operations, map[string]string{"operation": "delete", "targetType": "comment"})
	}
	return operations
}

func (h *Handler) footerCommentOperations(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r) {
		return
	}
	_, err := h.Store.WikiFooterComment(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	canUpdate, err := h.Store.CanUpdateWikiComment(r.Context(), ws, actor, id, "footer")
	if err != nil {
		writeError(w, err)
		return
	}
	canDelete, err := h.Store.CanDeleteWikiComment(r.Context(), ws, actor, id, "footer")
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{"operations": commentOperations(canUpdate, canDelete)})
}

func (h *Handler) footerCommentLikeCount(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r) {
		return
	}
	likes, err := h.Store.WikiFooterCommentLikes(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]int{"count": len(likes)})
}

func (h *Handler) footerCommentLikeUsers(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "cursor", "limit") {
		return
	}
	likes, err := h.Store.WikiFooterCommentLikes(r.Context(), ws, actor, id)
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

func validLabelPrefix(prefix string) bool {
	return prefix == "global" || prefix == "my" || prefix == "team" || prefix == "system"
}

func labelQuery(w http.ResponseWriter, r *http.Request, global bool) bool {
	allowed := []string{"prefix", "sort", "cursor", "limit"}
	if global {
		allowed = append(allowed, "label-id")
	}
	if !supportedQuery(w, r, allowed...) {
		return false
	}
	for _, raw := range r.URL.Query()["prefix"] {
		for _, prefix := range strings.Split(raw, ",") {
			if prefix != "" && !validLabelPrefix(prefix) {
				failure(w, 400, "Unsupported label prefix.")
				return false
			}
		}
	}
	order := r.URL.Query().Get("sort")
	if order != "" && order != "created-date" && order != "-created-date" && order != "id" && order != "-id" && order != "name" && order != "-name" {
		failure(w, 400, "Unsupported label sort order.")
		return false
	}
	return true
}

func sortWikiLabels(labels []models.WikiLabel, order string) {
	if order == "" || order == "created-date" {
		return
	}
	desc := strings.HasPrefix(order, "-")
	field := strings.TrimPrefix(order, "-")
	sort.SliceStable(labels, func(i, j int) bool {
		left, right := labels[i].CreatedAt, labels[j].CreatedAt
		switch field {
		case "id":
			left, right = labels[i].ID, labels[j].ID
		case "name":
			left, right = labels[i].Name, labels[j].Name
		}
		if desc {
			return left > right
		}
		return left < right
	})
}

func (h *Handler) labels(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !labelQuery(w, r, true) {
		return
	}
	labels, err := h.Store.WikiLabels(r.Context(), ws, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	filtered := make([]models.WikiLabel, 0, len(labels))
	for _, label := range labels {
		if queryContains(r, "label-id", label.ID) && queryContains(r, "prefix", label.Prefix) {
			filtered = append(filtered, label)
		}
	}
	sortWikiLabels(filtered, r.URL.Query().Get("sort"))
	values := make([]any, 0, len(filtered))
	for _, label := range filtered {
		values = append(values, label)
	}
	h.list(w, r, values)
}

func (h *Handler) contentLabels(w http.ResponseWriter, r *http.Request, ws, actor, kind, id string) {
	if !labelQuery(w, r, false) {
		return
	}
	var labels []models.WikiLabel
	var err error
	switch kind {
	case "page":
		labels, err = h.Store.WikiPageLabels(r.Context(), ws, actor, id)
	case "space":
		labels, err = h.Store.WikiSpaceLabels(r.Context(), ws, actor, id, false)
	case "space-content":
		labels, err = h.Store.WikiSpaceLabels(r.Context(), ws, actor, id, true)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	filtered := labels[:0]
	prefix := r.URL.Query().Get("prefix")
	if (kind == "space" || kind == "space-content") && prefix != "" && prefix != "my" && prefix != "team" {
		failure(w, 400, "Space labels only support my or team prefixes.")
		return
	}
	if prefix != "" {
		for _, label := range labels {
			if label.Prefix == prefix {
				filtered = append(filtered, label)
			}
		}
	} else if kind == "space" || kind == "space-content" {
		for _, label := range labels {
			if label.Prefix == "my" || label.Prefix == "team" {
				filtered = append(filtered, label)
			}
		}
	} else {
		filtered = labels
	}
	sortWikiLabels(filtered, r.URL.Query().Get("sort"))
	values := make([]any, 0, len(filtered))
	for _, label := range filtered {
		values = append(values, label)
	}
	h.list(w, r, values)
}

func sortLabelPages(pages []*models.WikiPage, order string) bool {
	allowed := map[string]bool{"": true, "id": true, "-id": true, "created-date": true, "-created-date": true, "modified-date": true, "-modified-date": true, "title": true, "-title": true}
	if !allowed[order] {
		return false
	}
	if order == "" || order == "id" {
		return true
	}
	desc := strings.HasPrefix(order, "-")
	field := strings.TrimPrefix(order, "-")
	sort.SliceStable(pages, func(i, j int) bool {
		left, right := pages[i].ID, pages[j].ID
		switch field {
		case "created-date":
			left, right = pages[i].CreatedAt, pages[j].CreatedAt
		case "modified-date":
			left, right = pages[i].Version.CreatedAt, pages[j].Version.CreatedAt
		case "title":
			left, right = pages[i].Title, pages[j].Title
		}
		if desc {
			return left > right
		}
		return left < right
	})
	return true
}

func (h *Handler) labelPages(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "space-id", "body-format", "sort", "cursor", "limit") || !storageFormat(w, r) {
		return
	}
	pages, err := h.Store.WikiPagesByLabel(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	filtered := pages[:0]
	for _, page := range pages {
		if queryContains(r, "space-id", page.SpaceID) {
			filtered = append(filtered, page)
		}
	}
	if !sortLabelPages(filtered, r.URL.Query().Get("sort")) {
		failure(w, 400, "Unsupported page sort order.")
		return
	}
	values := make([]any, 0, len(filtered))
	for _, page := range filtered {
		values = append(values, h.pageBean(page, r.URL.Query().Get("body-format") != ""))
	}
	h.list(w, r, values)
}

func unsupportedCommentTarget(in footerCommentWrite) bool {
	return in.CustomContentID != ""
}

func (h *Handler) createFooterComment(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	var in footerCommentWrite
	if !decode(w, r, &in) {
		return
	}
	if unsupportedCommentTarget(in) {
		failure(w, 400, "Custom-content footer comments are not currently supported.")
		return
	}
	body, err := decodeCommentBody(in.Body)
	if err != nil {
		failure(w, 400, err.Error())
		return
	}
	comment, err := h.Commands.CreateWikiFooterComment(r.Context(), ws, actor, models.WikiFooterComment{PageID: in.PageID, BlogPostID: in.BlogPostID, AttachmentID: in.AttachmentID, ParentCommentID: in.ParentCommentID, Body: body})
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Location", h.BaseURL+"/wiki/api/v2/footer-comments/"+comment.ID)
	respond(w, 201, h.footerCommentBean(comment, true))
}

func (h *Handler) updateFooterComment(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r) {
		return
	}
	var in footerCommentUpdate
	if !decode(w, r, &in) {
		return
	}
	body, err := decodeCommentBody(in.Body)
	if err != nil {
		failure(w, 400, err.Error())
		return
	}
	comment, err := h.Commands.UpdateWikiFooterComment(r.Context(), ws, actor, models.WikiFooterComment{ID: id, Body: body, Version: models.WikiVersion{Number: in.Version.Number, Message: in.Version.Message}})
	if err != nil {
		if errors.Is(err, store.ErrProjectPermission) {
			failure(w, 404, "Content does not exist or you do not have permission to update it.")
			return
		}
		writeError(w, err)
		return
	}
	respond(w, 200, h.footerCommentBean(comment, true))
}

func (h *Handler) deleteFooterComment(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r) {
		return
	}
	if err := h.Store.DeleteWikiFooterComment(r.Context(), ws, actor, id); err != nil {
		if errors.Is(err, store.ErrProjectPermission) {
			failure(w, 404, "Content does not exist or you do not have permission to delete it.")
			return
		}
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func supportedQuery(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	for key := range r.URL.Query() {
		found := false
		for _, a := range allowed {
			if a == key {
				found = true
				break
			}
		}
		if !found {
			failure(w, 400, "Unsupported query parameter: "+key)
			return false
		}
	}
	return true
}
func storageFormat(w http.ResponseWriter, r *http.Request) bool {
	format := r.URL.Query().Get("body-format")
	if format != "" && format != "storage" {
		failure(w, 400, "Only the storage body format is currently supported.")
		return false
	}
	return true
}
func (h *Handler) spaceBean(s *models.WikiSpace, descriptionFormat string, includeIcon bool) map[string]any {
	if descriptionFormat == "" {
		descriptionFormat = "plain"
	}
	description := models.WikiBody{Representation: descriptionFormat, Value: s.Description}
	if descriptionFormat == "view" {
		description.Value = "<p>" + strings.ReplaceAll(html.EscapeString(s.Description), "\n", "<br>") + "</p>"
	}
	bean := map[string]any{"id": s.ID, "key": s.Key, "name": s.Name, "type": "global", "status": "current", "authorId": s.AuthorID, "spaceOwnerId": s.AuthorID, "currentActiveAlias": s.Key, "createdAt": s.CreatedAt, "description": map[string]any{descriptionFormat: description}, "_links": map[string]string{"webui": "/spaces/" + s.ID, "base": h.BaseURL + "/wiki"}}
	if includeIcon {
		bean["icon"] = map[string]string{"path": "/static/img/space-default.svg", "apiDownloadLink": "/static/img/space-default.svg"}
	}
	return bean
}
func (h *Handler) pageBean(p *models.WikiPage, body bool) map[string]any {
	bean := map[string]any{"id": p.ID, "status": p.Status, "title": p.Title, "spaceId": p.SpaceID, "authorId": p.AuthorID, "ownerId": p.AuthorID, "lastOwnerId": p.AuthorID, "createdAt": p.CreatedAt, "version": p.Version, "_links": map[string]string{"webui": "/spaces/" + p.SpaceID + "/pages/" + p.ID, "base": h.BaseURL + "/wiki"}}
	if p.ParentID != "" {
		bean["parentId"] = p.ParentID
		bean["parentType"] = "page"
	}
	if body {
		bean["body"] = map[string]any{"storage": p.Body}
	}
	return bean
}

func (h *Handler) createSpace(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	var in struct {
		Name, Key, Alias             string
		Description                  models.WikiBody
		CreatePrivateSpace           bool
		RoleAssignments              []json.RawMessage
		CopySpaceAccessConfiguration *int64
		TemplateKey                  string
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Key == "" {
		in.Key = in.Alias
	} else if in.Alias != "" && in.Key != in.Alias {
		failure(w, 400, "Separate space aliases are not supported.")
		return
	}
	if in.Description.Representation != "" && in.Description.Representation != "plain" {
		failure(w, 400, "Space description must use plain representation.")
		return
	}
	if len(in.RoleAssignments) > 0 || in.CopySpaceAccessConfiguration != nil || in.TemplateKey != "" {
		failure(w, 400, "Space role assignments, copied access and templates are not yet supported.")
		return
	}
	s, err := h.Commands.CreateWikiSpace(r.Context(), ws, actor, in.Key, in.Name, in.Description.Value, in.CreatePrivateSpace)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 201, h.spaceBean(s, "plain", false))
}

func (h *Handler) spaces(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "limit", "cursor", "keys", "ids", "type", "status", "labels", "favorited-by", "not-favorited-by", "sort", "description-format", "include-icon") {
		return
	}
	q := r.URL.Query()
	format := q.Get("description-format")
	if format != "" && format != "plain" && format != "view" {
		failure(w, 400, "Unsupported space description format.")
		return
	}
	includeIcon, ok := queryBool(w, r, "include-icon")
	if !ok {
		return
	}
	if q.Get("favorited-by") != "" || q.Get("not-favorited-by") != "" {
		failure(w, 400, "Space favorite filters are not yet supported.")
		return
	}
	if kind := q.Get("type"); kind != "" && kind != "global" {
		failure(w, 400, "Only global spaces are supported.")
		return
	}
	if status := q.Get("status"); status != "" && status != "current" {
		failure(w, 400, "Only current spaces are supported.")
		return
	}
	items, err := h.Store.WikiSpaces(r.Context(), ws, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	filtered := make([]*models.WikiSpace, 0, len(items))
	for _, s := range items {
		if !queryContains(r, "keys", s.Key) || !queryContains(r, "ids", s.ID) {
			continue
		}
		matchesLabels := true
		if _, present := q["labels"]; present {
			labels, labelErr := h.Store.WikiSpaceLabels(r.Context(), ws, actor, s.ID, false)
			if labelErr != nil {
				writeError(w, labelErr)
				return
			}
			for _, raw := range q["labels"] {
				for _, wanted := range strings.Split(raw, ",") {
					found := false
					for _, label := range labels {
						if label.Name == wanted {
							found = true
							break
						}
					}
					matchesLabels = matchesLabels && found
				}
			}
		}
		if matchesLabels {
			filtered = append(filtered, s)
		}
	}
	order := q.Get("sort")
	if order != "" && order != "id" && order != "-id" && order != "key" && order != "-key" && order != "name" && order != "-name" {
		failure(w, 400, "Unsupported space sort order.")
		return
	}
	desc := strings.HasPrefix(order, "-")
	field := strings.TrimPrefix(order, "-")
	if field == "" {
		field = "id"
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		left, right := filtered[i].ID, filtered[j].ID
		if field == "key" {
			left, right = filtered[i].Key, filtered[j].Key
		} else if field == "name" {
			left, right = filtered[i].Name, filtered[j].Name
		}
		if field == "id" {
			leftID, leftErr := strconv.ParseInt(left, 10, 64)
			rightID, rightErr := strconv.ParseInt(right, 10, 64)
			if leftErr == nil && rightErr == nil {
				if desc {
					return leftID > rightID
				}
				return leftID < rightID
			}
		}
		if desc {
			return left > right
		}
		return left < right
	})
	values := make([]any, 0, len(filtered))
	for _, s := range filtered {
		values = append(values, h.spaceBean(s, format, includeIcon))
	}
	h.list(w, r, values)
}
func queryContains(r *http.Request, key, value string) bool {
	raw, ok := r.URL.Query()[key]
	if !ok {
		return true
	}
	for _, chunk := range raw {
		for _, v := range strings.Split(chunk, ",") {
			if v == value {
				return true
			}
		}
	}
	return false
}

func (h *Handler) pages(w http.ResponseWriter, r *http.Request, ws, actor, space string) {
	allowed := []string{"limit", "cursor", "space-id", "id", "status", "title", "body-format", "sort", "subtype"}
	if space != "" {
		allowed = append(allowed, "depth")
	}
	if !supportedQuery(w, r, allowed...) {
		return
	}
	if !storageFormat(w, r) {
		return
	}
	q := r.URL.Query()
	if depth := q.Get("depth"); depth != "" && depth != "all" && depth != "root" {
		failure(w, 400, "depth must be all or root.")
		return
	}
	if subtype := q.Get("subtype"); subtype != "" && subtype != "page" {
		failure(w, 400, "Only standard pages are available.")
		return
	}
	statuses := []string{"current"}
	if _, present := q["status"]; present {
		statuses = nil
		allowedStatus := map[string]bool{"current": true, "draft": space == "", "trashed": true}
		for _, raw := range q["status"] {
			for _, candidate := range strings.Split(raw, ",") {
				if !allowedStatus[candidate] {
					failure(w, 400, "Unsupported page status.")
					return
				}
			}
		}
		for _, candidate := range []string{"current", "draft", "trashed"} {
			if queryContains(r, "status", candidate) {
				statuses = append(statuses, candidate)
			}
		}
		if len(statuses) == 0 {
			failure(w, 400, "Unsupported page status.")
			return
		}
	}
	pages := []*models.WikiPage{}
	for _, status := range statuses {
		found, err := h.Store.WikiPages(r.Context(), ws, actor, space, status, q.Get("title"))
		if err != nil {
			writeError(w, err)
			return
		}
		pages = append(pages, found...)
	}
	if !sortPages(pages, q.Get("sort")) {
		failure(w, 400, "Unsupported page sort order.")
		return
	}
	values := []any{}
	for _, p := range pages {
		if q.Get("depth") == "root" && p.ParentID != "" {
			continue
		}
		if queryContains(r, "space-id", p.SpaceID) && queryContains(r, "id", p.ID) {
			values = append(values, h.pageBean(p, q.Get("body-format") != ""))
		}
	}
	h.list(w, r, values)
}

func (h *Handler) savePage(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "root-level", "embedded", "private") {
		return
	}
	var in struct {
		ID, SpaceID, Title, Status string
		ParentID                   *string
		Body                       models.WikiBody
		Version                    models.WikiVersion
		Subtype                    string
	}
	if !decode(w, r, &in) {
		return
	}
	if id != "" && in.ID != id {
		failure(w, 400, "The body id must match the page URL.")
		return
	}
	if id == "" && in.ID != "" {
		failure(w, 400, "New page IDs are assigned by the server.")
		return
	}
	for _, key := range []string{"root-level", "embedded", "private"} {
		if _, ok := queryBool(w, r, key); !ok {
			return
		}
	}
	if embedded, _ := queryBool(w, r, "embedded"); embedded {
		failure(w, 400, "Embedded pages are not available.")
		return
	}
	if private, _ := queryBool(w, r, "private"); private {
		failure(w, 400, "Use page restrictions for private page access.")
		return
	}
	if in.Subtype != "" {
		failure(w, 400, "Live documents are not available through the page endpoint.")
		return
	}
	if root := r.URL.Query().Get("root-level"); root == "true" && in.ParentID != nil {
		failure(w, 400, "A root page cannot have a parentId.")
		return
	}
	if id != "" {
		old, err := h.Store.WikiPage(r.Context(), ws, actor, id)
		if err != nil {
			writeError(w, err)
			return
		}
		if in.SpaceID == "" {
			in.SpaceID = old.SpaceID
		}
		if in.ParentID == nil {
			in.ParentID = &old.ParentID
		}
	}
	if in.Status == "trashed" {
		failure(w, 400, "Use DELETE to move a page to trash.")
		return
	}
	parentID := ""
	if in.ParentID != nil {
		parentID = *in.ParentID
	}
	p, err := h.Commands.SaveWikiPage(r.Context(), ws, actor, models.WikiPage{ID: id, SpaceID: in.SpaceID, ParentID: parentID, Title: in.Title, Status: in.Status, Body: in.Body, Version: in.Version})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.pageBean(p, true))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, values []any) {
	start, limit := 0, 25
	if raw, ok := r.URL.Query()["limit"]; ok {
		n, err := strconv.Atoi(raw[0])
		if err != nil || n < 1 || n > 250 {
			failure(w, 400, "limit must be between 1 and 250.")
			return
		}
		limit = n
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		b, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			failure(w, 400, "Invalid cursor.")
			return
		}
		n, err := strconv.Atoi(string(b))
		if err != nil || n < 0 {
			failure(w, 400, "Invalid cursor.")
			return
		}
		start = n
	}
	start = min(start, len(values))
	end := start + min(limit, len(values)-start)
	links := map[string]string{"base": h.BaseURL + "/wiki"}
	if end < len(values) {
		q := r.URL.Query()
		q.Set("cursor", base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end))))
		next := r.URL.Path + "?" + q.Encode()
		links["next"] = next
		w.Header().Set("Link", fmt.Sprintf("<%s%s>; rel=\"next\"", h.BaseURL, next))
	}
	respond(w, 200, map[string]any{"results": values[start:end], "_links": links})
}
