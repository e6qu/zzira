package agile

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

func (h *Handler) sprintProperties(w http.ResponseWriter, r *http.Request, sprint *models.Sprint) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	keys, err := h.Store.SprintPropertyKeys(r.Context(), sprint.ID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	values := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		values = append(values, map[string]any{
			"key":  key,
			"self": h.BaseURL + "/rest/agile/1.0/sprint/" + sprint.ID + "/properties/" + key,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": values})
}

func (h *Handler) sprintProperty(w http.ResponseWriter, r *http.Request, sprint *models.Sprint, key string) {
	switch r.Method {
	case http.MethodGet:
		value, err := h.Store.SprintProperty(r.Context(), sprint.ID, key)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The sprint property does not exist.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"key": key, "value": json.RawMessage(value),
			"self": h.BaseURL + "/rest/agile/1.0/sprint/" + sprint.ID + "/properties/" + key,
		})
	case http.MethodPut:
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The property value is invalid.")
			return
		}
		created, err := h.Store.SetSprintProperty(r.Context(), sprint.ID, key, raw)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The property key or value is invalid.")
			return
		}
		if created {
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		if err := h.Store.DeleteSprintProperty(r.Context(), sprint.ID, key); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				jiraError(w, http.StatusNotFound, "The sprint property does not exist.")
				return
			}
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) deleteSprint(w http.ResponseWriter, r *http.Request, workspaceID, userID string, sprint *models.Sprint) {
	err := h.Store.DeleteSprint(r.Context(), userID, workspaceID, sprint.ID)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, store.ErrSprintConflict):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		jiraError(w, http.StatusNotFound, "The sprint does not exist.")
	default:
		jiraError(w, http.StatusInternalServerError, "internal error")
	}
}

func (h *Handler) swapSprint(w http.ResponseWriter, r *http.Request, workspaceID, userID string, sprint *models.Sprint) {
	var request struct {
		SprintToSwapWith string `json:"sprintToSwapWith"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	if request.SprintToSwapWith == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"sprintToSwapWith": "A sprint to swap with is required."})
		return
	}
	err := h.Store.SwapSprints(r.Context(), userID, workspaceID, sprint.ID, request.SprintToSwapWith)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, store.ErrSprintConflict):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		jiraError(w, http.StatusNotFound, "The sprint does not exist.")
	default:
		jiraError(w, http.StatusInternalServerError, "internal error")
	}
}
