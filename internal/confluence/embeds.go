package confluence

import (
	"net/http"

	"github.com/e6qu/zzira/internal/models"
)

type smartLinkWrite struct {
	SpaceID  string `json:"spaceId"`
	Title    string `json:"title"`
	ParentID string `json:"parentId"`
	EmbedURL string `json:"embedUrl"`
}

func (h *Handler) createSmartLink(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	var input smartLinkWrite
	if !decode(w, r, &input) {
		return
	}
	content, err := h.Commands.CreateWikiContent(r.Context(), ws, actor, models.WikiContent{Type: "embed", SpaceID: input.SpaceID, Title: input.Title, ParentID: input.ParentID, EmbedURL: input.EmbedURL})
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.contentBean(content))
}

func (h *Handler) smartLink(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.hierarchicalContent(w, r, ws, actor, id, "embed")
}
func (h *Handler) deleteSmartLink(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.deleteHierarchicalContent(w, r, ws, actor, id, "embed")
}
func (h *Handler) smartLinkAncestors(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.contentAncestors(w, r, ws, actor, id, "embed")
}
func (h *Handler) smartLinkDescendants(w http.ResponseWriter, r *http.Request, ws, actor, id string, direct bool) {
	h.contentDescendants(w, r, ws, actor, id, "embed", direct)
}
func (h *Handler) smartLinkOperations(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.contentOperations(w, r, ws, actor, id, "embed")
}
func (h *Handler) smartLinkProperties(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.contentProperties(w, r, ws, actor, id, "embed")
}
func (h *Handler) smartLinkProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	h.contentProperty(w, r, ws, actor, id, propertyID, "embed")
}
func (h *Handler) createSmartLinkProperty(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	h.createContentProperty(w, r, ws, actor, id, "embed")
}
func (h *Handler) updateSmartLinkProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	h.updateContentProperty(w, r, ws, actor, id, propertyID, "embed")
}
func (h *Handler) deleteSmartLinkProperty(w http.ResponseWriter, r *http.Request, ws, actor, id, propertyID string) {
	h.deleteContentProperty(w, r, ws, actor, id, propertyID, "embed")
}
