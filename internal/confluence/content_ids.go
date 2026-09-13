package confluence

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// convert-ids-to-types tells a client migrating from v1 what each stored id is
// in v2 terms. v1 called every comment "comment"; v2 distinguishes a footer
// comment from an inline one, and that is the distinction this answers.

func (h *Handler) convertContentIDs(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	var input struct {
		ContentIDs []json.RawMessage `json:"contentIds"`
	}
	if !decode(w, r, &input) {
		return
	}
	if input.ContentIDs == nil {
		failure(w, 400, "contentIds is required.")
		return
	}
	if len(input.ContentIDs) > 100 {
		failure(w, 400, "At most 100 content ids may be converted at once.")
		return
	}
	results := map[string]any{}
	for _, raw := range input.ContentIDs {
		// An id may be sent as a string or as a number; both name the same
		// content, and a duplicate is answered once under one key.
		id, ok := contentIDText(raw)
		if !ok {
			failure(w, 400, "Every content id must be a string or a number.")
			return
		}
		if _, seen := results[id]; seen {
			continue
		}
		contentType, err := h.viewableContentType(r, ws, actor, id)
		if err != nil {
			writeError(w, err)
			return
		}
		// Content the caller may not view, or that does not exist, maps to
		// null — the answer never confirms that hidden content exists.
		if contentType == "" {
			results[id] = nil
		} else {
			results[id] = contentType
		}
	}
	respond(w, 200, map[string]any{"results": results})
}

func contentIDText(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text), true
	}
	var number json.Number
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&number) == nil {
		if _, err := strconv.ParseInt(number.String(), 10, 64); err == nil {
			return number.String(), true
		}
	}
	return "", false
}

// viewableContentType resolves what an id names and then reads that content
// through its own visibility rules. It returns "" for content the caller may
// not view or that does not exist, and an error only when the lookup itself
// failed.
func (h *Handler) viewableContentType(r *http.Request, ws, actor, id string) (string, error) {
	if number, err := strconv.ParseInt(id, 10, 64); err != nil || number < 1 {
		return "", nil
	}
	kind, err := h.Store.WikiContentKindByID(r.Context(), ws, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	ctx := r.Context()
	switch kind.Type {
	case "page":
		_, err = h.Store.WikiPage(ctx, ws, actor, id)
	case "blogpost":
		_, err = h.Store.WikiBlogPost(ctx, ws, actor, id)
	case "footer-comment":
		_, err = h.Store.WikiFooterComment(ctx, ws, actor, id)
	case "inline-comment":
		_, err = h.Store.WikiInlineComment(ctx, ws, actor, id)
	case "attachment":
		_, err = h.Store.WikiAttachment(ctx, ws, actor, id)
	default:
		_, err = h.Store.WikiContent(ctx, ws, actor, id, kind.Type)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	// Custom content is reported by the app-defined type it was created as.
	if kind.Type == "custom" && kind.CustomType != "" {
		return kind.CustomType, nil
	}
	return kind.Type, nil
}
