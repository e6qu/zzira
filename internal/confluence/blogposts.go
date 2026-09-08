package confluence

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type blogPostBodyWrite struct {
	Representation string           `json:"representation"`
	Value          string           `json:"value"`
	Storage        *models.WikiBody `json:"storage"`
	AtlasDocFormat *models.WikiBody `json:"atlas_doc_format"`
	Wiki           *models.WikiBody `json:"wiki"`
}

func (b blogPostBodyWrite) body() (models.WikiBody, bool) {
	if b.Storage != nil && b.AtlasDocFormat == nil && b.Wiki == nil && b.Representation == "" {
		return *b.Storage, b.Storage.Representation == "storage"
	}
	if b.Storage == nil && b.AtlasDocFormat == nil && b.Wiki == nil && b.Representation == "storage" {
		return models.WikiBody{Representation: b.Representation, Value: b.Value}, true
	}
	return models.WikiBody{}, false
}

type blogPostWrite struct {
	ID, SpaceID, Status, Title, CreatedAt string
	Body                                  blogPostBodyWrite
	Version                               models.WikiVersion
}

func (h *Handler) blogPostBean(post *models.WikiBlogPost, body bool) map[string]any {
	bean := map[string]any{
		"id": post.ID, "status": post.Status, "title": post.Title, "spaceId": post.SpaceID,
		"authorId": post.AuthorID, "ownerId": post.AuthorID, "createdAt": post.CreatedAt,
		"version": post.Version,
		"_links":  map[string]string{"webui": "/spaces/" + post.SpaceID + "/blogposts/" + post.ID, "base": h.BaseURL + "/wiki"},
	}
	if body {
		bean["body"] = map[string]any{"storage": post.Body}
	}
	return bean
}

func (h *Handler) blogPosts(w http.ResponseWriter, r *http.Request, ws, actor, spaceID string) {
	if !supportedQuery(w, r, "id", "space-id", "sort", "status", "title", "body-format", "cursor", "limit") || !storageFormat(w, r) {
		return
	}
	if spaceID != "" {
		if _, err := h.Store.WikiSpace(r.Context(), ws, actor, spaceID); err != nil {
			writeError(w, err)
			return
		}
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "current"
	}
	if status != "current" && status != "draft" && status != "trashed" {
		failure(w, 400, "Unsupported blog post status.")
		return
	}
	posts, err := h.Store.WikiBlogPosts(r.Context(), ws, actor, spaceID, status, r.URL.Query().Get("title"), r.URL.Query().Get("sort"))
	if err != nil {
		writeError(w, err)
		return
	}
	values := []any{}
	for _, post := range posts {
		if queryContains(r, "id", post.ID) && queryContains(r, "space-id", post.SpaceID) {
			values = append(values, h.blogPostBean(post, r.URL.Query().Get("body-format") != ""))
		}
	}
	h.list(w, r, values)
}

func (h *Handler) saveBlogPost(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "private") {
		return
	}
	private := false
	if raw := r.URL.Query().Get("private"); raw != "" {
		if raw != "true" && raw != "false" {
			failure(w, 400, "private must be true or false.")
			return
		}
		private = raw == "true"
	}
	var input blogPostWrite
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
	body, ok := input.Body.body()
	if !ok {
		failure(w, 400, "Only one storage body representation is currently supported.")
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
	if !supportedQuery(w, r, "body-format", "status") || !storageFormat(w, r) {
		return
	}
	post, err := h.Store.WikiBlogPost(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "current"
	}
	if post.Status != status {
		failure(w, 404, "Blog post not found with the requested status.")
		return
	}
	respond(w, 200, h.blogPostBean(post, r.URL.Query().Get("body-format") != ""))
}

func (h *Handler) deleteBlogPost(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "purge") {
		return
	}
	purge := false
	if raw := r.URL.Query().Get("purge"); raw != "" {
		if raw != "true" && raw != "false" {
			failure(w, 400, "purge must be true or false.")
			return
		}
		purge = raw == "true"
	}
	if purge {
		if err := h.Commands.PurgeWikiBlogPost(r.Context(), ws, actor, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(204)
		return
	}
	post, err := h.Store.WikiBlogPost(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if post.Status == "trashed" {
		failure(w, 400, "Use purge=true to permanently delete a trashed blog post.")
		return
	}
	post.Status = "trashed"
	post.Version.Number++
	post.Version.Message = "Moved to trash"
	if _, err := h.Commands.SaveWikiBlogPost(r.Context(), ws, actor, *post); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) blogPostVersions(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "body-format", "cursor", "limit", "sort") || !storageFormat(w, r) {
		return
	}
	versions, err := h.Store.WikiBlogPostVersions(r.Context(), ws, actor, id, r.URL.Query().Get("sort"))
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, 0, len(versions))
	for _, version := range versions {
		values = append(values, version)
	}
	h.list(w, r, values)
}

func (h *Handler) blogPostVersion(w http.ResponseWriter, r *http.Request, ws, actor, id, rawVersion string) {
	if !supportedQuery(w, r) {
		return
	}
	number, err := strconv.Atoi(rawVersion)
	if err != nil || number < 1 {
		failure(w, 400, "Version number must be a positive integer.")
		return
	}
	version, err := h.Store.WikiBlogPostVersion(r.Context(), ws, actor, id, number)
	if err != nil {
		writeError(w, err)
		return
	}
	versions, err := h.Store.WikiBlogPostVersions(r.Context(), ws, actor, id, "modified-date")
	if err != nil {
		writeError(w, err)
		return
	}
	bean := map[string]any{"number": version.Number, "authorId": version.AuthorID, "message": version.Message, "createdAt": version.CreatedAt, "minorEdit": version.MinorEdit, "contentTypeModified": false, "collaborators": []string{}}
	for index, candidate := range versions {
		if candidate.Number != version.Number {
			continue
		}
		if index > 0 {
			bean["prevVersion"] = versions[index-1].Number
		}
		if index+1 < len(versions) {
			bean["nextVersion"] = versions[index+1].Number
		}
	}
	respond(w, 200, bean)
}

func (h *Handler) blogPostLabels(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !labelQuery(w, r, false) {
		return
	}
	labels, err := h.Store.WikiBlogPostLabels(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	prefix := r.URL.Query().Get("prefix")
	filtered := make([]models.WikiLabel, 0, len(labels))
	for _, label := range labels {
		if prefix == "" || label.Prefix == prefix {
			filtered = append(filtered, label)
		}
	}
	sortWikiLabels(filtered, r.URL.Query().Get("sort"))
	values := make([]any, len(filtered))
	for i := range filtered {
		values[i] = filtered[i]
	}
	h.list(w, r, values)
}

func sortBlogPosts(posts []*models.WikiBlogPost, order string) bool {
	allowed := map[string]bool{"": true, "id": true, "-id": true, "created-date": true, "-created-date": true, "modified-date": true, "-modified-date": true}
	if !allowed[order] {
		return false
	}
	if order == "" || order == "id" {
		return true
	}
	desc := strings.HasPrefix(order, "-")
	field := strings.TrimPrefix(order, "-")
	sort.SliceStable(posts, func(i, j int) bool {
		left, right := posts[i].ID, posts[j].ID
		if field == "created-date" {
			left, right = posts[i].CreatedAt, posts[j].CreatedAt
		} else if field == "modified-date" {
			left, right = posts[i].Version.CreatedAt, posts[j].Version.CreatedAt
		}
		if desc {
			return left > right
		}
		return left < right
	})
	return true
}

func (h *Handler) labelBlogPosts(w http.ResponseWriter, r *http.Request, ws, actor, labelID string) {
	if !validPageID(w, labelID) || !supportedQuery(w, r, "space-id", "body-format", "sort", "cursor", "limit") || !storageFormat(w, r) {
		return
	}
	posts, err := h.Store.WikiBlogPostsByLabel(r.Context(), ws, actor, labelID)
	if err != nil {
		writeError(w, err)
		return
	}
	filtered := posts[:0]
	for _, post := range posts {
		if queryContains(r, "space-id", post.SpaceID) {
			filtered = append(filtered, post)
		}
	}
	if !sortBlogPosts(filtered, r.URL.Query().Get("sort")) {
		failure(w, 400, "Unsupported blog post sort order.")
		return
	}
	values := make([]any, len(filtered))
	for i := range filtered {
		values[i] = h.blogPostBean(filtered[i], r.URL.Query().Get("body-format") != "")
	}
	h.list(w, r, values)
}

func (h *Handler) blogPostLikeCount(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	likes, err := h.Store.WikiBlogPostLikes(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]int{"count": len(likes)})
}

func (h *Handler) blogPostLikeUsers(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "cursor", "limit") {
		return
	}
	likes, err := h.Store.WikiBlogPostLikes(r.Context(), ws, actor, id)
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

func (h *Handler) blogPostOperations(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	canUpdate, err := h.Store.CanUpdateWikiBlogPost(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	canDelete, err := h.Store.CanDeleteWikiBlogPost(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	operations := []any{map[string]string{"operation": "read", "targetType": "blogpost"}}
	if canUpdate {
		operations = append(operations, map[string]string{"operation": "update", "targetType": "blogpost"})
	}
	if canDelete {
		operations = append(operations, map[string]string{"operation": "delete", "targetType": "blogpost"})
	}
	respond(w, 200, map[string]any{"operations": operations})
}

func (h *Handler) blogPostProperties(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "key", "sort", "cursor", "limit") {
		return
	}
	properties, err := h.Store.WikiBlogPostProperties(r.Context(), ws, actor, id, r.URL.Query().Get("key"))
	if err != nil {
		writeError(w, err)
		return
	}
	order := r.URL.Query().Get("sort")
	if order != "" && order != "key" && order != "-key" {
		failure(w, 400, "Unsupported content property sort order.")
		return
	}
	if order == "-key" {
		sort.SliceStable(properties, func(i, j int) bool { return properties[i].Key > properties[j].Key })
	}
	values := make([]any, len(properties))
	for i := range properties {
		values[i] = properties[i]
	}
	h.list(w, r, values)
}

func (h *Handler) blogPostProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	if !validPageID(w, id) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	property, err := h.Store.WikiBlogPostProperty(r.Context(), ws, actor, id, propertyID)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) createBlogPostProperty(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	var input attachmentPropertyWrite
	if !decode(w, r, &input) || !validAttachmentProperty(w, input, false) {
		return
	}
	property, err := h.Commands.CreateWikiBlogPostProperty(r.Context(), ws, actor, id, input.Key, input.Value)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) updateBlogPostProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	if !validPageID(w, id) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	var input attachmentPropertyWrite
	if !decode(w, r, &input) || !validAttachmentProperty(w, input, true) {
		return
	}
	property, err := h.Commands.UpdateWikiBlogPostProperty(r.Context(), ws, actor, id, propertyID, input.Key, input.Value, input.Version.Number, input.Version.Message)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, property)
}

func (h *Handler) deleteBlogPostProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	if !validPageID(w, id) || !validPageID(w, propertyID) || !supportedQuery(w, r) {
		return
	}
	if err := h.Commands.DeleteWikiBlogPostProperty(r.Context(), ws, actor, id, propertyID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) blogPostClassification(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "status") {
		return
	}
	if status := r.URL.Query().Get("status"); status != "" && status != "current" {
		failure(w, 400, "Only current blog post classification is supported.")
		return
	}
	blog, err := h.Store.WikiBlogPost(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	level, ok := classificationLevels[blog.ClassificationLevel]
	if !ok {
		failure(w, 404, "Blog post does not have a classification level.")
		return
	}
	respond(w, 200, level)
}

func (h *Handler) setBlogPostClassification(w http.ResponseWriter, r *http.Request, ws, actor, id string, reset bool) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	var input classificationWrite
	if !decode(w, r, &input) {
		return
	}
	if input.Status != "current" || (!reset && classificationLevels[input.ID] == nil) || (reset && input.ID != "") {
		failure(w, 400, "A current, supported classification level is required.")
		return
	}
	levelID := input.ID
	if reset {
		levelID = ""
	}
	if _, err := h.Commands.SetWikiBlogPostClassification(r.Context(), ws, actor, id, levelID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *Handler) blogPostCustomContent(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "type", "sort", "cursor", "limit", "body-format") {
		return
	}
	contentType := strings.TrimSpace(r.URL.Query().Get("type"))
	if contentType == "" {
		failure(w, 400, "Custom content type is required.")
		return
	}
	format := r.URL.Query().Get("body-format")
	if format != "" && format != "storage" && format != "raw" {
		failure(w, 400, "Only storage or raw custom-content bodies are supported.")
		return
	}
	contents, err := h.Store.WikiBlogCustomContent(r.Context(), ws, actor, id, contentType, r.URL.Query().Get("sort"))
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, 0, len(contents))
	for _, content := range contents {
		bean := map[string]any{"id": content.ID, "type": content.Type, "status": content.Status, "title": content.Title, "spaceId": content.SpaceID, "blogPostId": content.BlogPostID, "authorId": content.AuthorID, "createdAt": content.CreatedAt, "version": content.Version, "_links": map[string]string{"base": h.BaseURL + "/wiki", "webui": "/spaces/" + content.SpaceID + "/blogposts/" + content.BlogPostID}}
		if format != "" {
			if format != content.BodyRepresentation {
				failure(w, 400, "The requested body format is unavailable for this custom content type.")
				return
			}
			bean["body"] = map[string]any{format: content.Body}
		}
		values = append(values, bean)
	}
	h.list(w, r, values)
}

type blogRedactionWrite struct {
	CreatedAt     string `json:"createdAt"`
	CleanHistory  bool   `json:"cleanHistory"`
	VersionNumber int    `json:"versionNumber"`
	Title         struct {
		Redactions []models.WikiRedactionPointer `json:"redactions"`
	} `json:"title"`
	Body struct {
		Redactions []models.WikiRedactionPointer `json:"redactions"`
	} `json:"body"`
}

func (h *Handler) redactBlogPost(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	var input blogRedactionWrite
	if !decode(w, r, &input) {
		return
	}
	_, title, body, err := h.Commands.RedactWikiBlogPost(r.Context(), ws, actor, id, input.CreatedAt, input.VersionNumber, input.CleanHistory, input.Title.Redactions, input.Body.Redactions)
	if err != nil {
		if errors.Is(err, store.ErrWikiBlogPostConflict) {
			failure(w, 400, "createdAt or versionNumber is out of date.")
			return
		}
		writeError(w, err)
		return
	}
	respond(w, 202, map[string]any{"title": map[string]any{"redactions": title}, "body": map[string]any{"redactions": body}})
}
