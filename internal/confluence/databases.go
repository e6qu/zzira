package confluence

import (
	"errors"
	"net/http"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

type databaseWrite struct {
	SpaceID  string `json:"spaceId"`
	Title    string `json:"title"`
	ParentID string `json:"parentId"`
}

type classificationWrite struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// classificationLevelBean is a classification level in Confluence's shape.
func classificationLevelBean(level models.DataClassificationLevel) map[string]any {
	return map[string]any{
		"id": level.ID, "status": level.Status, "order": level.Rank, "name": level.Name,
		"description": level.Description, "guideline": level.Guideline, "color": level.Color,
	}
}

// contentClassificationLevel is the level a piece of content carries: its own,
// or else its space's default. It reports false when there is neither.
func (h *Handler) contentClassificationLevel(r *http.Request, ws, actor, own, spaceID string) (map[string]any, bool, error) {
	levelID := own
	if levelID == "" {
		space, err := h.Store.WikiSpace(r.Context(), ws, actor, spaceID)
		if err != nil {
			return nil, false, err
		}
		levelID = space.DefaultClassificationLevel
	}
	if levelID == "" {
		return nil, false, nil
	}
	level, err := h.Store.DataClassificationLevel(r.Context(), ws, levelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return classificationLevelBean(level), true, nil
}

// assignableClassification checks a classification write: a reset names no
// level, and a set names a published one.
func (h *Handler) assignableClassification(w http.ResponseWriter, r *http.Request, ws string, input classificationWrite, reset bool) bool {
	if input.Status != "current" || reset && input.ID != "" || !reset && input.ID == "" {
		failure(w, 400, "A current, supported classification level is required.")
		return false
	}
	if !reset {
		if err := h.Store.PublishedDataClassificationLevel(r.Context(), ws, input.ID); err != nil {
			failure(w, 400, "A current, supported classification level is required.")
			return false
		}
	}
	return true
}

func (h *Handler) createDatabase(w http.ResponseWriter, r *http.Request, ws, actor string) {
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
	var input databaseWrite
	if !decode(w, r, &input) {
		return
	}
	content, err := h.Commands.CreateWikiContent(r.Context(), ws, actor, models.WikiContent{Type: "database", SpaceID: input.SpaceID, Title: input.Title, ParentID: input.ParentID, Private: private})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.contentBean(content))
}

func (h *Handler) database(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.hierarchicalContent(w, r, ws, actor, id, "database")
}
func (h *Handler) deleteDatabase(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.deleteHierarchicalContent(w, r, ws, actor, id, "database")
}
func (h *Handler) databaseAncestors(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.contentAncestors(w, r, ws, actor, id, "database")
}
func (h *Handler) databaseDescendants(w http.ResponseWriter, r *http.Request, ws, actor, id string, direct bool) {
	h.contentDescendants(w, r, ws, actor, id, "database", direct)
}
func (h *Handler) databaseOperations(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.contentOperations(w, r, ws, actor, id, "database")
}
func (h *Handler) databaseProperties(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.contentProperties(w, r, ws, actor, id, "database")
}
func (h *Handler) databaseProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	h.contentProperty(w, r, ws, actor, id, propertyID, "database")
}
func (h *Handler) createDatabaseProperty(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.createContentProperty(w, r, ws, actor, id, "database")
}
func (h *Handler) updateDatabaseProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	h.updateContentProperty(w, r, ws, actor, id, propertyID, "database")
}
func (h *Handler) deleteDatabaseProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	h.deleteContentProperty(w, r, ws, actor, id, propertyID, "database")
}

func (h *Handler) contentClassification(w http.ResponseWriter, r *http.Request, ws, actor, id, contentType string) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	content, err := h.Store.WikiContent(r.Context(), ws, actor, id, contentType)
	if err != nil {
		writeError(w, err)
		return
	}
	level, ok, err := h.contentClassificationLevel(r, ws, actor, content.ClassificationLevel, content.SpaceID)
	if err != nil {
		writeError(w, err)
		return
	}
	if !ok {
		failure(w, 404, "Content does not have a classification level.")
		return
	}
	respond(w, 200, level)
}

func (h *Handler) setContentClassification(w http.ResponseWriter, r *http.Request, ws, actor, id, contentType string, reset bool) {
	if !validPageID(w, id) || !supportedQuery(w, r) {
		return
	}
	var input classificationWrite
	if !decode(w, r, &input) {
		return
	}
	if !h.assignableClassification(w, r, ws, input, reset) {
		return
	}
	levelID := input.ID
	if _, err := h.Commands.SetWikiContentClassification(r.Context(), ws, actor, id, contentType, levelID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}
