package api3

import (
	"encoding/json"
	"net/http"

	"github.com/e6qu/zzira/internal/models"
)

func (h *Handler) serviceRequestNotification(w http.ResponseWriter, r *http.Request, workspaceID, issueIDOrKey string) {
	request, _, actorID, accessErr := h.serviceRequestAccess(r, workspaceID, issueIDOrKey)
	if accessErr != nil {
		writeJerr(w, accessErr)
		return
	}
	if r.Method == http.MethodGet {
		subscribed, err := h.Store.ServiceRequestSubscription(r.Context(), request.Issue.ID, actorID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load notification subscription.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"subscribed": subscribed})
		return
	}
	if err := h.Commands.SetServiceRequestSubscription(r.Context(), actorID, workspaceID, request.Issue.ID, r.Method == http.MethodPut); err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func serviceFeedbackBean(feedback *models.ServiceRequestFeedback) map[string]any {
	return map[string]any{"type": feedback.Type, "rating": feedback.Rating, "comment": map[string]string{"body": feedback.Comment}}
}

func (h *Handler) serviceRequestFeedback(w http.ResponseWriter, r *http.Request, workspaceID, issueIDOrKey string) {
	request, _, actorID, accessErr := h.serviceRequestAccess(r, workspaceID, issueIDOrKey)
	if accessErr != nil {
		writeJerr(w, accessErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		feedback, err := h.Store.ServiceRequestFeedback(r.Context(), request.Issue.ID)
		if err != nil {
			jiraError(w, http.StatusNotFound, "Feedback was not found.")
			return
		}
		writeJSON(w, http.StatusOK, serviceFeedbackBean(feedback))
	case http.MethodPost:
		var input struct {
			Type    string `json:"type"`
			Rating  int    `json:"rating"`
			Comment struct {
				Body string `json:"body"`
			} `json:"comment"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
			jiraError(w, http.StatusBadRequest, "Request body is invalid.")
			return
		}
		feedback, err := h.Commands.PutServiceRequestFeedback(r.Context(), actorID, workspaceID, request.Issue.ID, input.Type, input.Rating, input.Comment.Body)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, serviceFeedbackBean(feedback))
	case http.MethodDelete:
		if err := h.Commands.DeleteServiceRequestFeedback(r.Context(), actorID, workspaceID, request.Issue.ID); err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
