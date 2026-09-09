package api3

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/store"
)

func (h *Handler) bulkIssueRoute(w http.ResponseWriter, r *http.Request, path string) {
	switch {
	case (path == "issues/watch" || path == "issues/unwatch") && r.Method == http.MethodPost:
		h.submitBulkWatch(w, r, path == "issues/watch")
	case strings.HasPrefix(path, "queue/") && r.Method == http.MethodGet:
		h.bulkOperationProgress(w, r, strings.TrimPrefix(path, "queue/"))
	default:
		jiraError(w, http.StatusNotFound, "The bulk operation does not exist.")
	}
}

func bulkOperationError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"errors": []map[string]string{{"message": message}}})
}

func decodeBulkOperationBody(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		bulkOperationError(w, http.StatusBadRequest, "Invalid bulk operation request: "+err.Error())
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		bulkOperationError(w, http.StatusBadRequest, "Invalid bulk operation request: expected one JSON object")
		return false
	}
	return true
}

func (h *Handler) submitBulkWatch(w http.ResponseWriter, r *http.Request, watch bool) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		SelectedIssueIDsOrKeys []string `json:"selectedIssueIdsOrKeys"`
	}
	if !decodeBulkOperationBody(w, r, &request) {
		return
	}
	if len(request.SelectedIssueIDsOrKeys) < 1 || len(request.SelectedIssueIDsOrKeys) > 1000 {
		bulkOperationError(w, http.StatusBadRequest, "selectedIssueIdsOrKeys must contain between 1 and 1000 issues")
		return
	}
	seen := make(map[string]bool, len(request.SelectedIssueIDsOrKeys))
	issues := make([]store.BulkIssueTaskItem, 0, len(request.SelectedIssueIDsOrKeys))
	for _, idOrKey := range request.SelectedIssueIDsOrKeys {
		idOrKey = strings.TrimSpace(idOrKey)
		if idOrKey == "" || seen[strings.ToLower(idOrKey)] {
			bulkOperationError(w, http.StatusBadRequest, "selectedIssueIdsOrKeys must contain unique non-empty issue IDs or keys")
			return
		}
		seen[strings.ToLower(idOrKey)] = true
		issue, issueErr := h.resolveIssue(r, workspaceID, idOrKey)
		if issueErr != nil {
			bulkOperationError(w, http.StatusBadRequest, "Some of the issues in selectedIssueIdsOrKeys are invalid or inaccessible")
			return
		}
		issues = append(issues, store.BulkIssueTaskItem{ID: issue.ID, JiraID: issue.JiraID})
	}
	task, err := h.Store.EnqueueBulkWatchTask(r.Context(), workspaceID, actorID, issues, watch)
	if errors.Is(err, store.ErrBulkTaskLimit) {
		bulkOperationError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not submit the bulk operation.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"taskId": task.ID})
}

func (h *Handler) bulkOperationProgress(w http.ResponseWriter, r *http.Request, taskID string) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if taskID == "" || strings.Contains(taskID, "/") {
		bulkOperationError(w, http.StatusBadRequest, "The task associated with this taskId is not a bulk operation task")
		return
	}
	task, err := h.Store.APITaskByID(r.Context(), workspaceID, taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		bulkOperationError(w, http.StatusBadRequest, "The task associated with this taskId is not a bulk operation task")
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not read the bulk operation.")
		return
	}
	if !task.IsBulkIssueOperation() {
		bulkOperationError(w, http.StatusBadRequest, "The task associated with this taskId is not a bulk operation task")
		return
	}
	bean := map[string]any{
		"taskId": task.ID, "status": task.Status, "progressPercent": task.Progress,
		"submittedBy": map[string]string{"accountId": task.SubmittedBy},
		"created":     task.SubmittedAt.UnixMilli(), "updated": task.LastUpdateAt.UnixMilli(),
	}
	if task.StartedAt != nil {
		bean["started"] = task.StartedAt.UnixMilli()
	}
	if len(task.Result) > 0 && string(task.Result) != "null" {
		var result map[string]any
		if json.Unmarshal(task.Result, &result) == nil {
			for key, value := range result {
				bean[key] = value
			}
		}
	}
	writeJSON(w, http.StatusOK, bean)
}
