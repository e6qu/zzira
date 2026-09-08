package confluence

import (
	"net/http"

	"github.com/e6qu/zzira/internal/models"
)

type whiteboardWrite struct {
	SpaceID     string `json:"spaceId"`
	Title       string `json:"title"`
	ParentID    string `json:"parentId"`
	TemplateKey string `json:"templateKey"`
	Locale      string `json:"locale"`
}

func (h *Handler) createWhiteboard(w http.ResponseWriter, r *http.Request, ws, actor string) {
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
	var input whiteboardWrite
	if !decode(w, r, &input) {
		return
	}
	content, err := h.Commands.CreateWikiContent(r.Context(), ws, actor, models.WikiContent{
		Type: "whiteboard", SpaceID: input.SpaceID, Title: input.Title, ParentID: input.ParentID,
		Private: private, TemplateKey: input.TemplateKey, Locale: input.Locale,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.contentBean(content))
}

func (h *Handler) whiteboard(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.hierarchicalContent(w, r, ws, actor, id, "whiteboard")
}
func (h *Handler) deleteWhiteboard(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.deleteHierarchicalContent(w, r, ws, actor, id, "whiteboard")
}
func (h *Handler) whiteboardAncestors(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.contentAncestors(w, r, ws, actor, id, "whiteboard")
}
func (h *Handler) whiteboardDescendants(w http.ResponseWriter, r *http.Request, ws, actor, id string, direct bool) {
	h.contentDescendants(w, r, ws, actor, id, "whiteboard", direct)
}
func (h *Handler) whiteboardOperations(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.contentOperations(w, r, ws, actor, id, "whiteboard")
}
func (h *Handler) whiteboardProperties(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.contentProperties(w, r, ws, actor, id, "whiteboard")
}
func (h *Handler) whiteboardProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	h.contentProperty(w, r, ws, actor, id, propertyID, "whiteboard")
}
func (h *Handler) createWhiteboardProperty(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.createContentProperty(w, r, ws, actor, id, "whiteboard")
}
func (h *Handler) updateWhiteboardProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	h.updateContentProperty(w, r, ws, actor, id, propertyID, "whiteboard")
}
func (h *Handler) deleteWhiteboardProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	h.deleteContentProperty(w, r, ws, actor, id, propertyID, "whiteboard")
}
