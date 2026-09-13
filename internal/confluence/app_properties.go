package confluence

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

// Forge app properties and the data policy metadata are only for apps: a
// request made by a person — even an administrator — did not originate from the
// app, and is refused with 401 as Confluence refuses it.

func appCaller(w http.ResponseWriter, r *http.Request) (*models.AppInstallation, bool) {
	installation, ok := apps.InstallationFromContext(r.Context())
	if !ok {
		failure(w, 401, "The request did not originate from the Forge app.")
		return nil, false
	}
	return installation, true
}

// appProperties lists the calling app's properties, 50 at a time unless asked
// for another number up to 250, with a cursor to the next page.
func (h *Handler) appProperties(w http.ResponseWriter, r *http.Request) {
	if !supportedQuery(w, r, "cursor", "limit") {
		return
	}
	installation, ok := appCaller(w, r)
	if !ok {
		return
	}
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 250 {
			failure(w, 400, "limit must be between 1 and 250.")
			return
		}
		limit = parsed
	}
	after := ""
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || len(decoded) == 0 {
			failure(w, 400, "cursor is not one this listing issued.")
			return
		}
		after = string(decoded)
	}
	// One more than the page is read, so the listing knows whether to offer a
	// next page without a second query.
	properties, err := h.Store.WikiAppProperties(r.Context(), installation.ID, after, limit+1)
	if err != nil {
		writeError(w, err)
		return
	}
	links := map[string]string{"base": h.BaseURL + "/wiki"}
	if len(properties) > limit {
		properties = properties[:limit]
		links["next"] = "/wiki/api/v2/app/properties?limit=" + strconv.Itoa(limit) +
			"&cursor=" + base64.RawURLEncoding.EncodeToString([]byte(properties[len(properties)-1].Key))
	}
	respond(w, 200, map[string]any{"results": properties, "_links": links})
}

func (h *Handler) appProperty(w http.ResponseWriter, r *http.Request, key string) {
	if !supportedQuery(w, r) {
		return
	}
	installation, ok := appCaller(w, r)
	if !ok {
		return
	}
	if err := store.ValidWikiAppPropertyKey(key); err != nil {
		failure(w, 400, "Property key longer than 127 characters.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		property, err := h.Store.WikiAppProperty(r.Context(), installation.ID, key)
		if errors.Is(err, pgx.ErrNoRows) {
			failure(w, 404, "App property not found.")
			return
		}
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, property)
	case http.MethodPut:
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil || !json.Valid(raw) || strings.TrimSpace(string(raw)) == "" {
			failure(w, 400, "The request body must be a valid JSON value.")
			return
		}
		created, err := h.Store.PutWikiAppProperty(r.Context(), installation.ID, key, raw)
		if err != nil {
			writeError(w, err)
			return
		}
		status := 200
		if created {
			status = 201
		}
		w.WriteHeader(status)
	case http.MethodDelete:
		if err := h.Store.DeleteWikiAppProperty(r.Context(), installation.ID, key); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		failure(w, 405, "App properties support GET, PUT and DELETE.")
	}
}

// dataPolicyMetadata reports whether any content in the workspace is blocked
// for the calling app. No data policy restricts content here, so it never is —
// the answer, not an omission, matching the per-space data policy read.
func (h *Handler) dataPolicyMetadata(w http.ResponseWriter, r *http.Request) {
	if !supportedQuery(w, r) {
		return
	}
	if _, ok := appCaller(w, r); !ok {
		return
	}
	respond(w, 200, map[string]bool{"anyContentBlocked": false})
}
