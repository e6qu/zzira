package web

import (
	"log"
	"net/http"
	"strconv"
)

// recordWikiView notes that a person opened content in the product. Failing to
// count a view must never stop the person reading what they opened, so an error
// is logged rather than shown.
func (h *Handler) recordWikiView(r *http.Request, ws, actor, contentType, contentID string) {
	if err := h.Store.RecordWikiContentView(r.Context(), ws, actor, contentType, contentID); err != nil {
		log.Print("record wiki view: ", strconv.Quote(err.Error()))
	}
}
