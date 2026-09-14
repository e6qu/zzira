package confluence

import (
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

// Pages and blog posts share Confluence's draft and deletion lifecycle, and a
// blog post reads the way a page does.

func resultsWrap(values []any) map[string]any {
	return map[string]any{"results": values, "meta": map[string]any{"hasMore": false}, "_links": map[string]any{}}
}

// canSeeDeleted reports whether the reader may see deleted content in a space;
// only its administrators can.
func (h *Handler) canSeeDeleted(r *http.Request, ws, actor, spaceID string, cache map[string]bool) bool {
	if allowed, known := cache[spaceID]; known {
		return allowed
	}
	allowed, err := h.Store.CanAdministerWikiSpace(r.Context(), ws, actor, spaceID)
	cache[spaceID] = err == nil && allowed
	return cache[spaceID]
}

// draftAsPage shows a published page's draft as Confluence reads a draft: the
// page with the draft's title and body, in draft status, at version 1.
func draftAsPage(page *models.WikiPage, draft *store.WikiContentDraft) *models.WikiPage {
	copied := *page
	copied.Status, copied.Title, copied.Body = "draft", draft.Title, draft.Body
	copied.Version = models.WikiVersion{Number: 1, AuthorID: draft.AuthorID, CreatedAt: draft.UpdatedAt}
	return &copied
}

func draftAsBlogPost(post *models.WikiBlogPost, draft *store.WikiContentDraft) *models.WikiBlogPost {
	copied := *post
	copied.Status, copied.Title, copied.Body = "draft", draft.Title, draft.Body
	copied.Version = models.WikiVersion{Number: 1, AuthorID: draft.AuthorID, CreatedAt: draft.UpdatedAt}
	return &copied
}

func (h *Handler) deletePage(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.deleteContent(w, r, ws, actor, "page", id)
}

func (h *Handler) deleteBlogPost(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.deleteContent(w, r, ws, actor, "blogpost", id)
}

// deleteContent is Confluence's delete for pages and blog posts: content goes
// to the trash, `purge=true` takes trashed content out of the trash, and
// `draft=true` discards a draft for good.
func (h *Handler) deleteContent(w http.ResponseWriter, r *http.Request, ws, actor, contentType, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "purge", "draft") {
		return
	}
	purge, ok := queryBool(w, r, "purge")
	if !ok {
		return
	}
	draft, ok := queryBool(w, r, "draft")
	if !ok {
		return
	}
	name := "page"
	var status string
	var trash func() error
	switch contentType {
	case "page":
		page, err := h.Store.WikiPage(r.Context(), ws, actor, id)
		if err != nil {
			writeError(w, err)
			return
		}
		status = page.Status
		trash = func() error {
			page.Status, page.Version.Message = "trashed", "Moved to trash"
			page.Version.Number++
			_, err := h.Commands.SaveWikiPage(r.Context(), ws, actor, *page)
			return err
		}
	default:
		name = "blog post"
		post, err := h.Store.WikiBlogPost(r.Context(), ws, actor, id)
		if err != nil {
			writeError(w, err)
			return
		}
		status = post.Status
		trash = func() error {
			post.Status, post.Version.Message = "trashed", "Moved to trash"
			post.Version.Number++
			_, err := h.Commands.SaveWikiBlogPost(r.Context(), ws, actor, *post)
			return err
		}
	}
	var err error
	switch {
	case purge && draft:
		failure(w, 400, "Choose either purge or draft.")
		return
	case draft:
		if status == "draft" {
			err = h.Commands.DeleteUnpublishedWikiDraft(r.Context(), ws, actor, contentType, id)
		} else {
			err = h.Commands.DiscardWikiContentDraft(r.Context(), ws, actor, contentType, id)
		}
	case purge:
		if status != "trashed" {
			failure(w, 400, "Only a trashed "+name+" can be purged.")
			return
		}
		if contentType == "page" {
			err = h.Commands.PurgeWikiPage(r.Context(), ws, actor, id)
		} else {
			err = h.Commands.PurgeWikiBlogPost(r.Context(), ws, actor, id)
		}
	default:
		switch status {
		case "draft":
			failure(w, 400, "Use draft=true to delete a draft.")
			return
		case "trashed":
			failure(w, 400, "Use purge=true to permanently delete a trashed "+name+".")
			return
		case "deleted":
			writeError(w, pgx.ErrNoRows)
			return
		}
		err = trash()
	}
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

// blogPostBeanWithFormat renders a blog post with its body in a format.
func (h *Handler) blogPostBeanWithFormat(post *models.WikiBlogPost, format string) (map[string]any, error) {
	bean := h.blogPostBean(post, false)
	if format == "" {
		return bean, nil
	}
	value := post.Body.Value
	if format != "storage" {
		target := format
		if target == "anonymous_export_view" {
			target = "export_view"
		}
		converted, err := store.ConvertWikiBody(post.Body.Value, "storage", target)
		if err != nil {
			return nil, err
		}
		value = converted
	}
	bean["body"] = map[string]any{format: models.WikiBody{Representation: format, Value: value}}
	return bean, nil
}

// sortBlogPosts orders blog posts as Confluence's sorts do, comparing ids as
// numbers. It reports false for a sort Confluence does not offer.
func sortBlogPosts(posts []*models.WikiBlogPost, order string) bool {
	switch order {
	case "", "id", "-id", "created-date", "-created-date", "modified-date", "-modified-date":
	default:
		return false
	}
	descending := strings.HasPrefix(order, "-")
	key := strings.TrimPrefix(order, "-")
	sort.SliceStable(posts, func(i, j int) bool {
		left, right := posts[i], posts[j]
		comparison := 0
		switch key {
		case "created-date":
			comparison = cmp.Compare(left.CreatedAt, right.CreatedAt)
		case "modified-date":
			comparison = cmp.Compare(left.Version.CreatedAt, right.Version.CreatedAt)
		}
		if comparison == 0 {
			leftID, _ := strconv.ParseInt(left.ID, 10, 64)
			rightID, _ := strconv.ParseInt(right.ID, 10, 64)
			comparison = cmp.Compare(leftID, rightID)
		}
		if descending {
			return comparison > 0
		}
		return comparison < 0
	})
	return true
}

func (h *Handler) blogPosts(w http.ResponseWriter, r *http.Request, ws, actor, spaceID string) {
	if !supportedQuery(w, r, "id", "space-id", "sort", "status", "title", "body-format", "cursor", "limit") {
		return
	}
	format, ok := pageBodyFormat(w, r, false)
	if !ok {
		return
	}
	if spaceID != "" {
		if _, err := h.Store.WikiSpace(r.Context(), ws, actor, spaceID); err != nil {
			writeError(w, err)
			return
		}
	}
	// Confluence lists current blog posts unless asked for others.
	statuses := []string{"current"}
	if _, present := r.URL.Query()["status"]; present {
		statuses = nil
		for _, raw := range r.URL.Query()["status"] {
			for _, candidate := range strings.Split(raw, ",") {
				if candidate != "current" && candidate != "deleted" && candidate != "trashed" {
					failure(w, 400, "Unsupported blog post status.")
					return
				}
				if !queryListContains(statuses, candidate) {
					statuses = append(statuses, candidate)
				}
			}
		}
	}
	order := r.URL.Query().Get("sort")
	posts := []*models.WikiBlogPost{}
	for _, status := range statuses {
		found, err := h.Store.WikiBlogPosts(r.Context(), ws, actor, spaceID, status, r.URL.Query().Get("title"), order)
		if err != nil {
			writeError(w, err)
			return
		}
		posts = append(posts, found...)
	}
	if len(statuses) > 1 && !sortBlogPosts(posts, order) {
		failure(w, 400, "Unsupported blog post sort order.")
		return
	}
	adminSpaces := map[string]bool{}
	values := []any{}
	for _, post := range posts {
		if !queryContains(r, "id", post.ID) || !queryContains(r, "space-id", post.SpaceID) {
			continue
		}
		if post.Status == "deleted" && !h.canSeeDeleted(r, ws, actor, post.SpaceID, adminSpaces) {
			continue
		}
		bean, err := h.blogPostBeanWithFormat(post, format)
		if err != nil {
			writeError(w, err)
			return
		}
		values = append(values, bean)
	}
	h.list(w, r, values)
}

func queryListContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

type blogPostDraftableWrite struct {
	ID, SpaceID, Status, Title, CreatedAt string
	Body                                  json.RawMessage
	Version                               models.WikiVersion
}

func (h *Handler) saveBlogPost(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "private") {
		return
	}
	private, ok := queryBool(w, r, "private")
	if !ok {
		return
	}
	var input blogPostDraftableWrite
	if !decode(w, r, &input) {
		return
	}
	if id != "" && input.ID != id {
		failure(w, 400, "The body id must match the blog post URL.")
		return
	}
	if id == "" && input.ID != "" {
		failure(w, 400, "New blog post IDs are assigned by the server.")
		return
	}
	body, problem := decodePageBody(input.Body)
	if problem != "" {
		failure(w, 400, problem)
		return
	}
	if id != "" {
		old, err := h.Store.WikiBlogPost(r.Context(), ws, actor, id)
		if err != nil {
			writeError(w, err)
			return
		}
		if input.SpaceID == "" {
			input.SpaceID = old.SpaceID
		}
		private = old.Private
		// Saving a published blog post as a draft keeps the published version
		// and replaces any draft already waiting beside it.
		if input.Status == "draft" && old.Status == "current" {
			if input.Version.Number != 1 {
				failure(w, 400, "A draft of a published blog post is saved at version 1.")
				return
			}
			title := input.Title
			if title == "" {
				title = old.Title
			}
			draft, err := h.Commands.SaveWikiContentDraft(r.Context(), ws, actor, "blogpost", id, title, body)
			if err != nil {
				writeError(w, err)
				return
			}
			respond(w, 200, h.blogPostBean(draftAsBlogPost(old, draft), true))
			return
		}
	}
	if input.Status == "trashed" {
		failure(w, 400, "Use DELETE to move a blog post to trash.")
		return
	}
	post, err := h.Commands.SaveWikiBlogPost(r.Context(), ws, actor, models.WikiBlogPost{ID: id, SpaceID: input.SpaceID, Status: input.Status, Title: input.Title, CreatedAt: input.CreatedAt, Private: private, Body: body, Version: input.Version})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.blogPostBean(post, true))
}

func (h *Handler) blogPost(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "body-format", "get-draft", "status", "version", "include-labels", "include-properties", "include-operations", "include-likes", "include-versions", "include-version", "include-favorited-by-current-user-status", "include-webresources", "include-collaborators") {
		return
	}
	format, ok := pageBodyFormat(w, r, true)
	if !ok {
		return
	}
	flags := map[string]bool{}
	for _, key := range []string{"get-draft", "include-labels", "include-properties", "include-operations", "include-likes", "include-versions", "include-favorited-by-current-user-status", "include-webresources", "include-collaborators"} {
		value, valid := queryBool(w, r, key)
		if !valid {
			return
		}
		flags[key] = value
	}
	includeVersion := true
	if _, present := r.URL.Query()["include-version"]; present {
		var valid bool
		if includeVersion, valid = queryBool(w, r, "include-version"); !valid {
			return
		}
	}
	for _, raw := range r.URL.Query()["status"] {
		for _, candidate := range strings.Split(raw, ",") {
			switch candidate {
			case "current", "trashed", "deleted", "historical", "draft":
			default:
				failure(w, 400, "Unsupported blog post status.")
				return
			}
		}
	}
	post, err := h.Store.WikiBlogPost(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if post.Status == "deleted" && !h.canSeeDeleted(r, ws, actor, post.SpaceID, map[string]bool{}) {
		writeError(w, pgx.ErrNoRows)
		return
	}
	if raw := r.URL.Query().Get("version"); raw != "" {
		number, parseErr := strconv.Atoi(raw)
		if parseErr != nil || number < 1 {
			failure(w, 400, "Version number must be positive.")
			return
		}
		latest := post.Version.Number
		if post, err = h.Store.WikiBlogPostAtVersion(r.Context(), ws, actor, id, number); err != nil {
			writeError(w, err)
			return
		}
		if number < latest {
			post.Status = "historical"
		}
	}
	if flags["get-draft"] && post.Status != "draft" {
		draft, draftErr := h.Store.WikiContentDraft(r.Context(), ws, actor, "blogpost", id)
		if errors.Is(draftErr, pgx.ErrNoRows) {
			failure(w, 404, "Blog post draft not found.")
			return
		}
		if draftErr != nil {
			writeError(w, draftErr)
			return
		}
		post = draftAsBlogPost(post, draft)
	}
	if _, filtered := r.URL.Query()["status"]; filtered && !queryContains(r, "status", post.Status) {
		failure(w, 404, "Blog post not found with the requested status.")
		return
	} else if !filtered && !flags["get-draft"] && post.Status != "current" && post.Status != "historical" {
		failure(w, 404, "Blog post not found with the requested status.")
		return
	}
	bean, err := h.blogPostBeanWithFormat(post, format)
	if err != nil {
		writeError(w, err)
		return
	}
	if !includeVersion {
		delete(bean, "version")
	}
	accountIDs := func(ids []string) []any {
		values := make([]any, len(ids))
		for i, accountID := range ids {
			values[i] = map[string]string{"accountId": accountID}
		}
		return values
	}
	if flags["include-labels"] {
		labels, loadErr := h.Store.WikiBlogPostLabels(r.Context(), ws, actor, id)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		values := make([]any, len(labels))
		for i := range labels {
			values[i] = labels[i]
		}
		bean["labels"] = resultsWrap(values)
	}
	if flags["include-properties"] {
		properties, loadErr := h.Store.WikiBlogPostProperties(r.Context(), ws, actor, id, "")
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		values := make([]any, len(properties))
		for i := range properties {
			values[i] = properties[i]
		}
		bean["properties"] = resultsWrap(values)
	}
	if flags["include-operations"] {
		canUpdate, loadErr := h.Store.CanUpdateWikiBlogPost(r.Context(), ws, actor, id)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		canDelete, loadErr := h.Store.CanDeleteWikiBlogPost(r.Context(), ws, actor, id)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		operations := []any{map[string]string{"operation": "read", "targetType": "blogpost"}}
		if canUpdate {
			operations = append(operations, map[string]string{"operation": "update", "targetType": "blogpost"})
		}
		if canDelete {
			operations = append(operations, map[string]string{"operation": "delete", "targetType": "blogpost"})
		}
		bean["operations"] = resultsWrap(operations)
	}
	if flags["include-likes"] {
		likes, loadErr := h.Store.WikiBlogPostLikes(r.Context(), ws, actor, id)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		bean["likes"] = resultsWrap(accountIDs(likes))
	}
	if flags["include-versions"] {
		versions, loadErr := h.Store.WikiBlogPostVersions(r.Context(), ws, actor, id, "-modified-date")
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		values := make([]any, len(versions))
		for i := range versions {
			values[i] = versions[i]
		}
		bean["versions"] = resultsWrap(values)
	}
	if flags["include-favorited-by-current-user-status"] {
		favourite, loadErr := h.Store.IsWikiBlogPostFavourite(r.Context(), ws, actor, id)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		bean["isFavoritedByCurrentUser"] = favourite
	}
	if flags["include-webresources"] {
		bean["webresources"] = pageWebResources(h.BaseURL)
	}
	if flags["include-collaborators"] {
		collaborators, loadErr := h.Store.WikiBlogPostCollaborators(r.Context(), ws, actor, id)
		if loadErr != nil {
			writeError(w, loadErr)
			return
		}
		bean["collaborators"] = resultsWrap(accountIDs(collaborators))
	}
	if post.Status == "current" {
		h.recordView(r, ws, actor, "blogpost", post.ID)
	}
	respond(w, 200, bean)
}
