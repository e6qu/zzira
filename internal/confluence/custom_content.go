package confluence

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// splitQueryValues reads Confluence's repeated-or-comma-separated id filters.
func splitQueryValues(r *http.Request, name string) []string {
	values := []string{}
	for _, raw := range r.URL.Query()[name] {
		for _, part := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				values = append(values, trimmed)
			}
		}
	}
	return values
}

type customContentWrite struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Status     string `json:"status"`
	SpaceID    string `json:"spaceId"`
	PageID     string `json:"pageId"`
	BlogPostID string `json:"blogPostId"`
	CustomID   string `json:"customContentId"`
	Title      string `json:"title"`
	Body       struct {
		Storage *struct {
			Value          string `json:"value"`
			Representation string `json:"representation"`
		} `json:"storage"`
		Raw *struct {
			Value          string `json:"value"`
			Representation string `json:"representation"`
		} `json:"raw"`
	} `json:"body"`
	Version struct {
		Number  int    `json:"number"`
		Message string `json:"message"`
	} `json:"version"`
}

// parent reports which of Confluence's three parents the write named. Naming
// more than one is a contradiction rather than a precedence question, so it is
// refused instead of resolved.
func (input customContentWrite) parent() (id, kind string, err bool) {
	named := 0
	for _, candidate := range []struct{ id, kind string }{
		{input.PageID, "page"}, {input.BlogPostID, "blogpost"}, {input.CustomID, "custom"},
	} {
		if candidate.id != "" {
			named++
			id, kind = candidate.id, candidate.kind
		}
	}
	return id, kind, named > 1
}

func (h *Handler) customContentBean(content *models.WikiContent, format string) map[string]any {
	bean := h.contentBean(content)
	// Confluence reports the app-defined type here, not the storage kind.
	bean["type"] = content.CustomType
	delete(bean, "parentId")
	delete(bean, "parentType")
	switch content.ParentType {
	case "page":
		bean["pageId"] = content.ParentID
	case "blogpost":
		bean["blogPostId"] = content.ParentID
	case "custom":
		bean["customContentId"] = content.ParentID
	}
	if format != "" {
		representation := content.BodyRepresentation
		bean["body"] = map[string]any{
			format: map[string]any{"value": content.Body, "representation": representation},
		}
	}
	return bean
}

// customContentBodyFormat validates Confluence's body-format query. A custom
// content type declares the representation its bodies use, so asking for the
// other one is a request the type cannot answer.
func customContentBodyFormat(w http.ResponseWriter, r *http.Request) (string, bool) {
	format := r.URL.Query().Get("body-format")
	switch format {
	case "", "storage", "raw":
		return format, true
	default:
		failure(w, 400, "Only storage or raw custom-content bodies are supported.")
		return "", false
	}
}

func (h *Handler) customContents(w http.ResponseWriter, r *http.Request, ws, actor, spaceID string) {
	if !supportedQuery(w, r, "type", "id", "space-id", "sort", "cursor", "limit", "body-format", "status") {
		return
	}
	format, ok := customContentBodyFormat(w, r)
	if !ok {
		return
	}
	query := store.WikiCustomContentQuery{
		CustomType: strings.TrimSpace(r.URL.Query().Get("type")),
		IDs:        splitQueryValues(r, "id"),
		Sort:       r.URL.Query().Get("sort"),
		Status:     r.URL.Query().Get("status"),
	}
	if spaceID != "" {
		query.SpaceIDs = []string{spaceID}
	} else {
		query.SpaceIDs = splitQueryValues(r, "space-id")
	}
	// Confluence requires a type on the global listing; without one the answer
	// would be every app's content mixed together.
	if spaceID == "" && query.CustomType == "" && len(query.IDs) == 0 {
		failure(w, 400, "Custom content type is required.")
		return
	}
	if query.CustomType != "" {
		if _, err := h.Store.WikiCustomContentTypeRepresentation(r.Context(), query.CustomType); err != nil {
			writeError(w, err)
			return
		}
	}
	contents, err := h.Store.WikiCustomContents(r.Context(), ws, actor, query)
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, 0, len(contents))
	for _, content := range contents {
		values = append(values, h.customContentBean(content, format))
	}
	h.list(w, r, values)
}

func (h *Handler) createCustomContent(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	var input customContentWrite
	if !decode(w, r, &input) {
		return
	}
	parentID, parentType, conflicting := input.parent()
	if conflicting {
		failure(w, 400, "Name at most one of pageId, blogPostId or customContentId.")
		return
	}
	if parentID == "" && input.SpaceID == "" {
		failure(w, 400, "A spaceId or a parent is required.")
		return
	}
	body, ok := customContentBodyValue(w, input)
	if !ok {
		return
	}
	content, err := h.Store.CreateWikiCustomContent(r.Context(), ws, actor, models.WikiContent{
		CustomType: strings.TrimSpace(input.Type), SpaceID: input.SpaceID, Title: input.Title,
		ParentID: parentID, ParentType: parentType, Body: body,
		Version: models.WikiVersion{Message: input.Version.Message},
	})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.customContentBean(content, representationOf(input)))
}

// customContentBodyValue takes the body from whichever representation was
// supplied. Supplying both is a contradiction, so it is refused.
func customContentBodyValue(w http.ResponseWriter, input customContentWrite) (string, bool) {
	if input.Body.Storage != nil && input.Body.Raw != nil {
		failure(w, 400, "Supply the body in one representation.")
		return "", false
	}
	if input.Body.Storage != nil {
		return input.Body.Storage.Value, true
	}
	if input.Body.Raw != nil {
		return input.Body.Raw.Value, true
	}
	return "", true
}

func representationOf(input customContentWrite) string {
	if input.Body.Raw != nil {
		return "raw"
	}
	return "storage"
}

func (h *Handler) customContent(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "body-format", "version", "include-labels", "include-properties", "include-operations", "include-versions", "include-version", "include-collaborators") {
		return
	}
	format, ok := customContentBodyFormat(w, r)
	if !ok {
		return
	}
	content, err := h.Store.WikiContent(r.Context(), ws, actor, id, "custom")
	if err != nil {
		writeError(w, err)
		return
	}
	if format == "" {
		format = content.BodyRepresentation
	}
	bean := h.customContentBean(content, format)
	if r.URL.Query().Get("include-labels") == "true" {
		labels, labelErr := h.Store.WikiContentLabels(r.Context(), ws, actor, id)
		if labelErr != nil {
			writeError(w, labelErr)
			return
		}
		bean["labels"] = map[string]any{"results": labels, "meta": map[string]any{"hasMore": false}, "_links": map[string]any{}}
	}
	if r.URL.Query().Get("include-properties") == "true" {
		properties, propErr := h.Store.WikiContentProperties(r.Context(), ws, actor, id, "custom", "")
		if propErr != nil {
			writeError(w, propErr)
			return
		}
		bean["properties"] = map[string]any{"results": properties}
	}
	if r.URL.Query().Get("include-operations") == "true" {
		operations, opErr := h.contentOperationList(r, ws, actor, id, "custom")
		if opErr != nil {
			writeError(w, opErr)
			return
		}
		bean["operations"] = map[string]any{"results": operations}
	}
	respond(w, 200, bean)
}

func (h *Handler) updateCustomContent(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	var input customContentWrite
	if !decode(w, r, &input) {
		return
	}
	if input.Version.Number < 1 {
		failure(w, 400, "A version number is required.")
		return
	}
	body, ok := customContentBodyValue(w, input)
	if !ok {
		return
	}
	content, err := h.Store.UpdateWikiCustomContent(r.Context(), ws, actor, id, models.WikiContent{
		CustomType: strings.TrimSpace(input.Type), Title: input.Title, Body: body,
		Version: models.WikiVersion{Message: input.Version.Message},
	}, input.Version.Number)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.customContentBean(content, representationOf(input)))
}

func (h *Handler) customContentVersions(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "limit", "cursor", "sort", "body-format") {
		return
	}
	if _, ok := customContentBodyFormat(w, r); !ok {
		return
	}
	versions, err := h.Store.WikiCustomContentVersions(r.Context(), ws, actor, id, r.URL.Query().Get("sort"))
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

func (h *Handler) customContentVersion(w http.ResponseWriter, r *http.Request, ws, actor, id, number string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "body-format") {
		return
	}
	format, ok := customContentBodyFormat(w, r)
	if !ok {
		return
	}
	parsed, err := strconv.Atoi(number)
	if err != nil || parsed < 1 {
		failure(w, 400, "The version number must be a positive integer.")
		return
	}
	version, body, err := h.Store.WikiCustomContentVersion(r.Context(), ws, actor, id, parsed)
	if err != nil {
		writeError(w, err)
		return
	}
	bean := map[string]any{
		"number": version.Number, "message": version.Message, "minorEdit": false,
		"authorId": version.AuthorID, "createdAt": version.CreatedAt,
	}
	if format != "" {
		bean["body"] = map[string]any{format: map[string]any{"value": body, "representation": format}}
	}
	respond(w, 200, bean)
}

func (h *Handler) customContentLabels(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !labelQuery(w, r, false) {
		return
	}
	labels, err := h.Store.WikiContentLabels(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	prefix := r.URL.Query().Get("prefix")
	values := make([]any, 0, len(labels))
	for _, label := range labels {
		if prefix != "" && label.Prefix != prefix {
			continue
		}
		values = append(values, label)
	}
	h.list(w, r, values)
}

// customContentAttachments and customContentFooterComments report what hangs
// off custom content. Neither can be created against custom content yet, so
// both answer an empty page rather than a 404: the content exists and has none.
func (h *Handler) customContentAttachments(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "sort", "cursor", "limit", "status", "mediaType", "filename") {
		return
	}
	if _, err := h.Store.WikiContent(r.Context(), ws, actor, id, "custom"); err != nil {
		writeError(w, err)
		return
	}
	h.list(w, r, []any{})
}

func (h *Handler) customContentFooterComments(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !validPageID(w, id) || !supportedQuery(w, r, "body-format", "sort", "cursor", "limit") {
		return
	}
	if _, err := h.Store.WikiContent(r.Context(), ws, actor, id, "custom"); err != nil {
		writeError(w, err)
		return
	}
	h.list(w, r, []any{})
}
