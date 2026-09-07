package confluence

import (
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
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
