package confluence

import (
	"net/http"

	"github.com/e6qu/zzira/internal/models"
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

var classificationLevels = map[string]map[string]any{
	"public":       {"id": "public", "status": "PUBLISHED", "order": 0, "name": "Public", "description": "Approved for public sharing", "guideline": "May be shared outside the organization.", "color": "GREEN"},
	"internal":     {"id": "internal", "status": "PUBLISHED", "order": 1, "name": "Internal", "description": "For organization members", "guideline": "Share only with authenticated organization members.", "color": "BLUE"},
	"confidential": {"id": "confidential", "status": "PUBLISHED", "order": 2, "name": "Confidential", "description": "Limited business information", "guideline": "Share only with people who need this information.", "color": "ORANGE"},
	"restricted":   {"id": "restricted", "status": "PUBLISHED", "order": 3, "name": "Restricted", "description": "Highly sensitive information", "guideline": "Use explicit access controls and approved handling.", "color": "RED_BOLD"},
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
	level, ok := classificationLevels[content.ClassificationLevel]
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
	if input.Status != "current" || (!reset && classificationLevels[input.ID] == nil) || (reset && input.ID != "") {
		failure(w, 400, "A current, supported classification level is required.")
		return
	}
	levelID := input.ID
	if reset {
		levelID = ""
	}
	if _, err := h.Commands.SetWikiContentClassification(r.Context(), ws, actor, id, contentType, levelID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(204)
}
