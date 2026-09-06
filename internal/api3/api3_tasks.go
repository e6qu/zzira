package api3

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/store"
)

func (h *Handler) apiTaskBean(task store.APITask) map[string]any {
	result := task.Result
	if len(result) == 0 {
		result = []byte("null")
	}
	bean := map[string]any{
		"description":    task.Description,
		"elapsedRuntime": task.LastUpdateAt.Sub(task.SubmittedAt).Milliseconds(),
		"id":             task.ID,
		"lastUpdate":     task.LastUpdateAt.UnixMilli(),
		"message":        task.Message,
		"progress":       task.Progress,
		"result":         result,
		"self":           h.BaseURL + "/rest/api/3/task/" + task.ID,
		"status":         task.Status,
		"submitted":      task.SubmittedAt.UnixMilli(),
		"submittedBy":    0,
	}
	if task.StartedAt != nil {
		bean["started"] = task.StartedAt.UnixMilli()
	}
	if task.FinishedAt != nil {
		bean["finished"] = task.FinishedAt.UnixMilli()
		if task.StartedAt != nil {
			bean["elapsedRuntime"] = task.FinishedAt.Sub(*task.StartedAt).Milliseconds()
		}
	}
	return bean
}

func (h *Handler) taskRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/task/"), "/")
	if len(parts) == 0 || parts[0] == "" || len(parts) > 2 {
		jiraError(w, http.StatusNotFound, "The task does not exist.")
		return
	}
	task, err := h.Store.APITaskByID(r.Context(), workspaceID, parts[0])
	if errors.Is(err, pgx.ErrNoRows) {
		jiraError(w, http.StatusNotFound, "The task does not exist.")
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if task.SubmittedBy != userID {
		admin, adminErr := h.Store.IsAdmin(r.Context(), workspaceID, userID)
		if adminErr != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !admin {
			jiraError(w, http.StatusForbidden, "You do not have permission to view this task.")
			return
		}
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, h.apiTaskBean(task))
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		cancelled, err := h.Store.CancelAPITask(r.Context(), workspaceID, task.ID)
		if errors.Is(err, store.ErrAPITaskNotCancellable) {
			jiraError(w, http.StatusBadRequest, "The task has already finished and cannot be cancelled.")
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			jiraError(w, http.StatusNotFound, "The task does not exist.")
			return
		}
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusAccepted, h.apiTaskBean(cancelled))
		return
	}
	jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
}
