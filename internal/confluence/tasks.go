package confluence

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func taskQueryValues(w http.ResponseWriter, r *http.Request, key string, numeric bool) ([]string, bool) {
	var values []string
	for _, raw := range r.URL.Query()[key] {
		for _, value := range strings.Split(raw, ",") {
			value = strings.TrimSpace(value)
			if value == "" {
				failure(w, 400, key+" must not contain an empty value.")
				return nil, false
			}
			if numeric {
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil || n < 1 {
					failure(w, 400, key+" values must be positive integers.")
					return nil, false
				}
			}
			values = append(values, value)
		}
	}
	if len(values) > 250 {
		failure(w, 400, key+" accepts at most 250 values.")
		return nil, false
	}
	return values, true
}

func taskQueryTime(w http.ResponseWriter, r *http.Request, key string) (*time.Time, bool) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil, true
	}
	millis, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || millis < 0 {
		failure(w, 400, key+" must be epoch milliseconds.")
		return nil, false
	}
	value := time.UnixMilli(millis).UTC()
	return &value, true
}

func (h *Handler) taskBean(task *models.WikiTask, body bool) map[string]any {
	bean := map[string]any{
		"id": task.ID, "localId": task.LocalID, "spaceId": task.SpaceID,
		"pageId": task.PageID, "status": task.Status, "createdBy": task.CreatedBy,
		"createdAt": task.CreatedAt, "updatedAt": task.UpdatedAt,
	}
	if task.AssignedTo != "" {
		bean["assignedTo"] = task.AssignedTo
	}
	if task.CompletedBy != "" {
		bean["completedBy"] = task.CompletedBy
	}
	if task.DueAt != "" {
		bean["dueAt"] = task.DueAt
	}
	if task.CompletedAt != "" {
		bean["completedAt"] = task.CompletedAt
	}
	if body {
		bean["body"] = map[string]any{"storage": task.Body}
	}
	return bean
}

func (h *Handler) tasks(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r, "body-format", "include-blank-tasks", "status", "task-id", "space-id", "page-id", "blogpost-id", "created-by", "assigned-to", "completed-by", "created-at-from", "created-at-to", "due-at-from", "due-at-to", "completed-at-from", "completed-at-to", "cursor", "limit") || !storageFormat(w, r) {
		return
	}
	filter := store.WikiTaskFilter{IncludeBlank: true, Status: r.URL.Query().Get("status")}
	if filter.Status != "" && filter.Status != "complete" && filter.Status != "incomplete" {
		failure(w, 400, "status must be complete or incomplete.")
		return
	}
	if _, present := r.URL.Query()["include-blank-tasks"]; present {
		value, ok := queryBool(w, r, "include-blank-tasks")
		if !ok {
			return
		}
		filter.IncludeBlank = value
	}
	var ok bool
	if filter.TaskIDs, ok = taskQueryValues(w, r, "task-id", true); !ok {
		return
	}
	if filter.SpaceIDs, ok = taskQueryValues(w, r, "space-id", true); !ok {
		return
	}
	if filter.PageIDs, ok = taskQueryValues(w, r, "page-id", true); !ok {
		return
	}
	blogPostIDs, ok := taskQueryValues(w, r, "blogpost-id", true)
	if !ok {
		return
	}
	if filter.CreatedBy, ok = taskQueryValues(w, r, "created-by", false); !ok {
		return
	}
	if filter.AssignedTo, ok = taskQueryValues(w, r, "assigned-to", false); !ok {
		return
	}
	if filter.CompletedBy, ok = taskQueryValues(w, r, "completed-by", false); !ok {
		return
	}
	for key, target := range map[string]**time.Time{
		"created-at-from": &filter.CreatedFrom, "created-at-to": &filter.CreatedTo,
		"due-at-from": &filter.DueFrom, "due-at-to": &filter.DueTo,
		"completed-at-from": &filter.CompletedFrom, "completed-at-to": &filter.CompletedTo,
	} {
		if *target, ok = taskQueryTime(w, r, key); !ok {
			return
		}
	}
	if len(blogPostIDs) > 0 && len(filter.PageIDs) == 0 {
		h.list(w, r, []any{})
		return
	}
	tasks, err := h.Store.WikiTasks(r.Context(), ws, actor, filter)
	if err != nil {
		writeError(w, err)
		return
	}
	values := make([]any, 0, len(tasks))
	for _, task := range tasks {
		values = append(values, h.taskBean(task, r.URL.Query().Get("body-format") != ""))
	}
	h.list(w, r, values)
}

func (h *Handler) task(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "body-format") || !storageFormat(w, r) {
		return
	}
	if value, err := strconv.ParseInt(id, 10, 64); err != nil || value < 1 {
		failure(w, 400, "Task id must be a positive integer.")
		return
	}
	task, err := h.Store.WikiTask(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.taskBean(task, r.URL.Query().Get("body-format") != ""))
}

type taskUpdateRequest struct {
	ID, LocalID, SpaceID, PageID, BlogPostID string
	Status                                   string `json:"status"`
	CreatedBy, AssignedTo, CompletedBy       string
	CreatedAt, UpdatedAt, DueAt, CompletedAt string
}

func (h *Handler) updateTask(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "body-format") || !storageFormat(w, r) {
		return
	}
	if value, err := strconv.ParseInt(id, 10, 64); err != nil || value < 1 {
		failure(w, 400, "Task id must be a positive integer.")
		return
	}
	var input taskUpdateRequest
	if !decode(w, r, &input) {
		return
	}
	task, err := h.Commands.UpdateWikiTask(r.Context(), ws, actor, id, input.Status)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, h.taskBean(task, r.URL.Query().Get("body-format") != ""))
}
