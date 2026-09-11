package confluence

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

// longTaskBean renders a background page operation the way Confluence's long
// task reads do.
func (h *V1Handler) longTaskBean(task store.APITask) map[string]any {
	percentage := task.Progress
	if task.Status == "COMPLETE" {
		percentage = 100
	}
	messages := []any{}
	if task.Message != "" {
		messages = append(messages, map[string]any{"translation": task.Message, "args": []any{}})
	}
	return map[string]any{
		"id": task.ID, "name": map[string]any{"key": task.Kind}, "elapsedTime": 0,
		"percentageComplete": percentage, "successful": task.Status == "COMPLETE",
		"finished": task.Status == "COMPLETE" || task.Status == "FAILED",
		"messages": messages,
		"status":   task.Status,
		"_links":   map[string]string{"self": h.BaseURL + "/wiki/rest/api/longtask/" + task.ID},
	}
}

func (h *V1Handler) v1LongTasks(w http.ResponseWriter, r *http.Request, ws, actor, taskID string) {
	if !supportedQuery(w, r, "start", "limit", "expand") {
		return
	}
	tasks, err := h.Store.WikiLongTasks(r.Context(), ws, actor, taskID)
	if err != nil {
		writeError(w, err)
		return
	}
	if taskID != "" {
		respond(w, 200, h.longTaskBean(tasks[0]))
		return
	}
	results := make([]any, 0, len(tasks))
	for _, task := range tasks {
		results = append(results, h.longTaskBean(task))
	}
	respond(w, 200, map[string]any{
		"results": results, "start": 0, "limit": len(results), "size": len(results),
		"_links": map[string]string{"base": h.BaseURL + "/wiki"},
	})
}

type copyPageRequest struct {
	CopyAttachments    bool   `json:"copyAttachments"`
	CopyPermissions    bool   `json:"copyPermissions"`
	CopyProperties     bool   `json:"copyProperties"`
	CopyLabels         bool   `json:"copyLabels"`
	CopyCustomContents bool   `json:"copyCustomContents"`
	CopyDescendants    bool   `json:"copyDescendants"`
	PageTitle          string `json:"pageTitle"`
	DestinationPageID  string `json:"destinationPageId"`
	Destination        *struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"destination"`
	TitleOptions *struct {
		Prefix  string `json:"prefix"`
		Replace string `json:"replace"`
		Search  string `json:"search"`
	} `json:"titleOptions"`
	Body *struct {
		Storage *struct {
			Value          string `json:"value"`
			Representation string `json:"representation"`
		} `json:"storage"`
		Editor2 *struct {
			Value          string `json:"value"`
			Representation string `json:"representation"`
		} `json:"editor2"`
	} `json:"body"`
}

func (h *V1Handler) v1CopyPage(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r, "expand") {
		return
	}
	var input copyPageRequest
	if !decode(w, r, &input) {
		return
	}
	options := store.WikiPageCopyOptions{
		Title:           input.PageTitle,
		CopyAttachments: input.CopyAttachments,
		CopyProperties:  input.CopyProperties,
		CopyLabels:      input.CopyLabels,
	}
	if input.Destination != nil {
		options.DestinationType, options.DestinationID = input.Destination.Type, input.Destination.Value
	}
	if input.Body != nil && input.Body.Storage != nil {
		options.Body = input.Body.Storage.Value
	}
	copyID, err := h.Store.CopyWikiPage(r.Context(), ws, actor, id, options)
	if err != nil {
		writeError(w, err)
		return
	}
	page, err := h.Store.WikiPage(r.Context(), ws, actor, copyID)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{
		"id": page.ID, "type": "page", "status": page.Status, "title": page.Title,
		"_links": map[string]string{"base": h.BaseURL + "/wiki", "webui": "/wiki/pages/" + page.ID},
	})
}

// v1CopyPageHierarchy queues the copy and answers with the task that reports
// it, because a tree can be large enough that a caller should not wait.
func (h *V1Handler) v1CopyPageHierarchy(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r) {
		return
	}
	var input copyPageRequest
	if !decode(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.DestinationPageID) == "" {
		failure(w, 400, "destinationPageId is required.")
		return
	}
	payload := store.WikiCopyHierarchyRequest{
		PageID: id, DestinationPageID: input.DestinationPageID,
		CopyAttachments: input.CopyAttachments, CopyProperties: input.CopyProperties,
		CopyLabels: input.CopyLabels, CopyDescendants: input.CopyDescendants,
	}
	if input.TitleOptions != nil {
		payload.TitlePrefix = input.TitleOptions.Prefix
		payload.TitleSearch = input.TitleOptions.Search
		payload.TitleReplace = input.TitleOptions.Replace
	}
	task, err := h.Store.EnqueueWikiCopyHierarchy(r.Context(), ws, actor, payload)
	if err != nil {
		writeError(w, err)
		return
	}
	h.respondWithTask(w, task)
}

func (h *V1Handler) respondWithTask(w http.ResponseWriter, task store.APITask) {
	self := h.BaseURL + "/wiki/rest/api/longtask/" + task.ID
	w.Header().Set("Location", self)
	respond(w, 202, map[string]any{"id": task.ID, "links": map[string]string{"status": self}})
}

func (h *V1Handler) v1ArchivePages(w http.ResponseWriter, r *http.Request, ws, actor string) {
	if !supportedQuery(w, r) {
		return
	}
	var input struct {
		Pages []struct {
			ID any `json:"id"`
		} `json:"pages"`
	}
	if !decode(w, r, &input) {
		return
	}
	if len(input.Pages) == 0 || len(input.Pages) > 200 {
		failure(w, 400, "Between 1 and 200 pages are required.")
		return
	}
	ids := make([]string, 0, len(input.Pages))
	for _, page := range input.Pages {
		id := contentStateID(page.ID)
		if id == "" {
			failure(w, 400, "Every page needs an id.")
			return
		}
		ids = append(ids, id)
	}
	task, err := h.Store.EnqueueWikiArchivePages(r.Context(), ws, actor, ids)
	if err != nil {
		writeError(w, err)
		return
	}
	h.respondWithTask(w, task)
}

func (h *V1Handler) v1TrashPageTree(w http.ResponseWriter, r *http.Request, ws, actor, id string) {
	if !supportedQuery(w, r) {
		return
	}
	// Confluence supports this only for a current page, so a page in any other
	// status is refused rather than half-handled.
	page, err := h.Store.WikiPage(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	if page.Status != "current" {
		failure(w, 400, "Only a current page tree can be trashed.")
		return
	}
	task, err := h.Store.EnqueueWikiTrashPageTree(r.Context(), ws, actor, id)
	if err != nil {
		writeError(w, err)
		return
	}
	h.respondWithTask(w, task)
}

func (h *V1Handler) v1MovePage(w http.ResponseWriter, r *http.Request, ws, actor, pageID, position, targetID string) {
	if !supportedQuery(w, r) {
		return
	}
	movedID, err := h.Store.MoveWikiPage(r.Context(), ws, actor, pageID, position, targetID)
	if err != nil {
		writeError(w, err)
		return
	}
	respond(w, 200, map[string]any{"pageId": movedID})
}
