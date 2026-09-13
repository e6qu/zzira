package confluence

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// adminKey reads, issues and ends the caller's admin key. Only an organization
// or site administrator has one; anyone else is answered 404.
func (h *Handler) adminKey(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		key, err := h.Store.WikiAdminKeyFor(r.Context(), ws, actor)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, adminKeyBean(key.AccountID, key.ExpirationTime.UTC().Format("2006-01-02T15:04:05.000Z")))
	case http.MethodPost:
		// The body is optional: an empty body asks for the default duration.
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<16))
		if err != nil {
			failure(w, 400, "Invalid admin key request.")
			return
		}
		var input struct {
			DurationInMinutes *int `json:"durationInMinutes"`
		}
		if strings.TrimSpace(string(raw)) != "" {
			decoder := json.NewDecoder(strings.NewReader(string(raw)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				failure(w, 400, "Invalid admin key request: "+err.Error())
				return
			}
		}
		minutes := 0
		if input.DurationInMinutes != nil {
			minutes = *input.DurationInMinutes
		}
		key, err := h.Store.EnableWikiAdminKey(r.Context(), ws, actor, minutes)
		if err != nil {
			writeError(w, err)
			return
		}
		respond(w, 200, adminKeyBean(key.AccountID, key.ExpirationTime.UTC().Format("2006-01-02T15:04:05.000Z")))
	case http.MethodDelete:
		if err := h.Store.DisableWikiAdminKey(r.Context(), ws, actor); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(204)
	default:
		failure(w, 405, "The admin key supports GET, POST and DELETE.")
	}
}

func adminKeyBean(accountID, expiration string) map[string]string {
	return map[string]string{"accountId": accountID, "expirationTime": expiration}
}
