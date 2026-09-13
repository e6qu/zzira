package confluence

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/store"
)

// Content analytics report two numbers Confluence keeps for every piece of
// content: how many times it was viewed, and how many distinct people viewed
// it. Both count from fromDate when one is given.

// recordView notes that a person read content through the API. Failing to count
// a view must never fail the read itself, so an error is logged, not returned.
func (h *Handler) recordView(r *http.Request, ws, actor, contentType, contentID string) {
	if err := h.Store.RecordWikiContentView(r.Context(), ws, actor, contentType, contentID); err != nil {
		log.Print("record wiki view: ", strconv.Quote(err.Error()))
	}
}

// analyticsFromDate reads fromDate. Confluence accepts a date or a full
// timestamp; a date means the start of that day.
func analyticsFromDate(w http.ResponseWriter, r *http.Request) (time.Time, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("fromDate"))
	if raw == "" {
		return time.Time{}, true
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z0700", "2006-01-02T15:04:05Z0700", "2006-01-02"} {
		if moment, err := time.Parse(layout, raw); err == nil {
			return moment.UTC(), true
		}
	}
	failure(w, 400, "fromDate must be a date such as 2026-09-01 or a timestamp such as 2026-09-01T00:00:00Z.")
	return time.Time{}, false
}

func (h *V1Handler) v1ContentAnalytics(w http.ResponseWriter, r *http.Request, ws, actor, contentID string, distinct bool) {
	if !supportedQuery(w, r, "fromDate") {
		return
	}
	// A content id is a number; anything else names no content.
	number, err := strconv.ParseInt(contentID, 10, 64)
	if err != nil || number < 1 {
		failure(w, 404, "There is no content with the given id.")
		return
	}
	from, ok := analyticsFromDate(w, r)
	if !ok {
		return
	}
	var result store.WikiContentAnalytics
	if distinct {
		result, err = h.Store.WikiContentViewers(r.Context(), ws, actor, contentID, from)
	} else {
		result, err = h.Store.WikiContentViews(r.Context(), ws, actor, contentID, from)
	}
	if err != nil {
		writeError(w, err)
		return
	}
	// The id is reported as the number it is, which is how Confluence's
	// analytics shape declares it.
	respond(w, 200, map[string]any{"id": number, "count": result.Count})
}
