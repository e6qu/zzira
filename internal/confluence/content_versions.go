package confluence

import (
	"net/http"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// A version list answers each version's metadata and, when a body format is
// asked for, what the page or blog post said at that version.
func (h *Handler) contentVersions(w http.ResponseWriter, r *http.Request, ws, actor, contentType, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "limit", "cursor", "sort", "body-format") {
		return
	}
	format, ok := pageBodyFormat(w, r, false)
	if !ok {
		return
	}
	order := r.URL.Query().Get("sort")
	if order != "" && order != "modified-date" && order != "-modified-date" {
		failure(w, 400, "Unsupported version sort order.")
		return
	}
	var versions []models.WikiVersion
	var bodies map[int]store.WikiVersionBody
	var err error
	field := "page"
	if contentType == "page" {
		versions, err = h.Store.WikiVersionsSorted(r.Context(), ws, actor, id, order)
		if err == nil && format != "" {
			bodies, err = h.Store.WikiPageVersionBodies(r.Context(), ws, actor, id)
		}
	} else {
		field = "blogpost"
		versions, err = h.Store.WikiBlogPostVersions(r.Context(), ws, actor, id, order)
		if err == nil && format != "" {
			bodies, err = h.Store.WikiBlogPostVersionBodies(r.Context(), ws, actor, id)
		}
	}
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, 0, len(versions))
	for _, version := range versions {
		bean := map[string]any{
			"createdAt": version.CreatedAt, "message": version.Message, "number": version.Number,
			"minorEdit": version.MinorEdit, "authorId": version.AuthorID,
		}
		content := map[string]any{"id": id}
		if body, found := bodies[version.Number]; found {
			value := body.Body
			if format != "storage" {
				if value, err = store.ConvertWikiBody(body.Body, "storage", format); err != nil {
					writeError(w, err)
					return
				}
			}
			content["title"] = body.Title
			content["body"] = map[string]any{format: models.WikiBody{Representation: format, Value: value}}
		}
		bean[field] = content
		values = append(values, bean)
	}
	h.list(w, r, values)
}
