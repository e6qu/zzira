package confluence

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// A v1 content id may name a page, a blog post or an attachment, and its
// labels are that content's labels; the label lookup lists every kind of
// content carrying a label, or one kind when asked.

func splitV1LabelName(raw string) (string, string) {
	if prefix, name, found := strings.Cut(raw, ":"); found && (prefix == "global" || prefix == "my" || prefix == "team") {
		return prefix, name
	}
	return "global", raw
}

func (h *V1Handler) addContentLabels(w http.ResponseWriter, r *http.Request, ws, actor, contentID string) {
	if !supportedQuery(w, r) {
		return
	}
	labels, ok := decodeV1Labels(w, r)
	if !ok {
		return
	}
	kind, err := h.Store.WikiContentKindByID(r.Context(), ws, contentID)
	if err != nil {
		writeError(w, err)
		return
	}
	switch kind.Type {
	case "page":
		labels, err = h.Commands.AddWikiPageLabels(r.Context(), ws, actor, contentID, labels)
	case "blogpost":
		labels, err = h.Commands.AddWikiBlogPostLabels(r.Context(), ws, actor, contentID, labels)
	case "attachment":
		labels, err = h.Commands.AddWikiAttachmentLabels(r.Context(), ws, actor, contentID, labels)
	default:
		failure(w, 400, "Labels can be added to pages, blog posts and attachments.")
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	h.v1LabelList(w, r, labels)
}

func (h *V1Handler) removeContentLabel(w http.ResponseWriter, r *http.Request, ws, actor, contentID, raw string) {
	if raw == "" {
		failure(w, 400, "Label name is required.")
		return
	}
	prefix, name := splitV1LabelName(raw)
	kind, err := h.Store.WikiContentKindByID(r.Context(), ws, contentID)
	if err != nil {
		writeError(w, err)
		return
	}
	switch kind.Type {
	case "page":
		err = h.Commands.RemoveWikiPageLabel(r.Context(), ws, actor, contentID, prefix, name)
	case "blogpost":
		err = h.Commands.RemoveWikiBlogPostLabel(r.Context(), ws, actor, contentID, prefix, name)
	case "attachment":
		err = h.Commands.RemoveWikiAttachmentLabel(r.Context(), ws, actor, contentID, prefix, name)
	default:
		failure(w, 400, "Labels can be removed from pages, blog posts and attachments.")
		return
	}
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *V1Handler) labelContent(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "name", "type", "start", "limit") {
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("name")))
	if name == "" {
		failure(w, 400, "Label name is required.")
		return
	}
	contentType := r.URL.Query().Get("type")
	switch contentType {
	case "", "page", "blogpost", "attachment", "page_template":
	default:
		failure(w, 400, "type must be page, blogpost, attachment or page_template.")
		return
	}
	labels, err := h.Store.WikiLabels(r.Context(), ws, actor)
	if err != nil {
		writeError(w, err)
		return
	}
	var label *models.WikiLabel
	for i := range labels {
		if labels[i].Name == name && (label == nil || labels[i].Prefix == "global") {
			copied := labels[i]
			label = &copied
		}
	}
	if label == nil {
		failure(w, 404, "Label not found.")
		return
	}
	results := []any{}
	if contentType == "" || contentType == "page" {
		pages, err := h.Store.WikiPagesByLabel(r.Context(), ws, actor, label.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		for _, page := range pages {
			results = append(results, map[string]any{"id": page.ID, "type": "page", "status": page.Status, "title": page.Title, "_links": map[string]string{"webui": "/spaces/" + page.SpaceID + "/pages/" + page.ID}})
		}
	}
	if contentType == "" || contentType == "blogpost" {
		posts, err := h.Store.WikiBlogPostsByLabel(r.Context(), ws, actor, label.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		for _, post := range posts {
			results = append(results, map[string]any{"id": post.ID, "type": "blogpost", "status": post.Status, "title": post.Title, "_links": map[string]string{"webui": "/spaces/" + post.SpaceID + "/blogposts/" + post.ID}})
		}
	}
	if contentType == "" || contentType == "attachment" {
		attachments, err := h.Store.WikiAttachmentsByLabel(r.Context(), ws, actor, label.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		for _, attachment := range attachments {
			results = append(results, h.v1AttachmentBean(attachment))
		}
	}
	// Page templates carry no labels on this site, so a page_template lookup
	// finds nothing rather than being refused.
	h.v1LabelAssociations(w, r, *label, results)
}

func (h *V1Handler) v1LabelAssociations(w http.ResponseWriter, r *http.Request, label models.WikiLabel, results []any) {
	start, limit := 0, 200
	if raw := r.URL.Query().Get("start"); raw != "" {
		value, ok := parseNonNegative(raw)
		if !ok {
			failure(w, 400, "start must be zero or greater.")
			return
		}
		start = value
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, ok := parseNonNegative(raw)
		if !ok {
			failure(w, 400, "limit must be zero or greater.")
			return
		}
		limit = value
	}
	start = min(start, len(results))
	end := min(start+limit, len(results))
	respond(w, 200, map[string]any{"label": v1LabelBean(label), "associatedContents": map[string]any{"results": results[start:end], "start": start, "limit": limit, "size": end - start}})
}

func parseNonNegative(raw string) (int, bool) {
	value := 0
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			return 0, false
		}
		value = value*10 + int(digit-'0')
		if value > 1_000_000 {
			return 0, false
		}
	}
	return value, raw != ""
}
