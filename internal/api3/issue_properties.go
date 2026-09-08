package api3

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

const issuePropertyValueLimit = 32768

func (h *Handler) issueProperties(w http.ResponseWriter, r *http.Request, idOrKey string, propertyKey *string) {
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	issue, issueErr := h.resolveIssue(r, workspaceID, idOrKey)
	if issueErr != nil {
		writeJerr(w, issueErr)
		return
	}

	if propertyKey == nil {
		if r.Method != http.MethodGet {
			jiraError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		properties, err := h.Store.IssueProperties(r.Context(), issue.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load issue properties.")
			return
		}
		keys := make([]map[string]string, 0, len(properties))
		base := h.BaseURL + "/rest/api/3/issue/" + url.PathEscape(idOrKey) + "/properties/"
		for key := range properties {
			keys = append(keys, map[string]string{"key": key, "self": base + url.PathEscape(key)})
		}
		// IssueProperties returns rows in key order. PostgreSQL preserves that
		// order while the map does not, so restore Jira's stable key listing.
		sortPropertyKeys(keys)
		writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
		return
	}

	key := *propertyKey
	if key == "" || !utf8.ValidString(key) || utf8.RuneCountInString(key) > 255 {
		jiraError(w, http.StatusBadRequest, "Property key accepts 1 to 255 characters.")
		return
	}
	switch r.Method {
	case http.MethodGet:
		value, err := h.Store.IssueProperty(r.Context(), issue.ID, key)
		if errors.Is(err, pgx.ErrNoRows) {
			jiraError(w, http.StatusNotFound, "Property does not exist.")
			return
		}
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load issue property.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": value})
	case http.MethodPut:
		value, ok := decodeIssuePropertyValue(w, r)
		if !ok {
			return
		}
		created, err := h.Store.SetIssueProperty(r.Context(), issue.ID, key, value)
		if errors.Is(err, store.ErrIssuePropertyValidation) {
			jiraError(w, http.StatusBadRequest, "A valid JSON property value of at most 32768 bytes is required.")
			return
		}
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not save issue property.")
			return
		}
		if created {
			w.WriteHeader(http.StatusCreated)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	case http.MethodDelete:
		err := h.Store.DeleteIssueProperty(r.Context(), issue.ID, key)
		if errors.Is(err, pgx.ErrNoRows) {
			jiraError(w, http.StatusNotFound, "Property does not exist.")
			return
		}
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not delete issue property.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

func decodeIssuePropertyValue(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, issuePropertyValueLimit+1))
	raw = bytes.TrimSpace(raw)
	if err != nil || len(raw) == 0 || len(raw) > issuePropertyValueLimit || !json.Valid(raw) {
		jiraError(w, http.StatusBadRequest, "A valid JSON property value of at most 32768 bytes is required.")
		return nil, false
	}
	return json.RawMessage(raw), true
}

func sortPropertyKeys(keys []map[string]string) {
	slices.SortFunc(keys, func(a, b map[string]string) int { return strings.Compare(a["key"], b["key"]) })
}
