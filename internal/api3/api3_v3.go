package api3

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

// ---- worklogs ----

func (h *Handler) worklogBean(w *models.Worklog) map[string]any {
	author := map[string]any{"accountId": w.AuthorID, "displayName": w.AuthorName, "active": true, "accountType": "atlassian"}
	body := map[string]any{
		"id":               w.ID,
		"self":             h.BaseURL + "/rest/api/3/issue/worklog/" + w.ID,
		"author":           author,
		"updateAuthor":     author,
		"created":          w.Created,
		"updated":          w.Created,
		"timeSpent":        models.TimeSpentLabel(w.TimeSpentSeconds),
		"timeSpentSeconds": w.TimeSpentSeconds,
		"startsAt":         w.Created,
	}
	if len(w.Comment) > 0 {
		body["comment"] = w.Comment
	} else {
		body["comment"] = adf.Doc(adf.Paragraph())
	}
	return body
}

func (h *Handler) issueWorklogRoute(w http.ResponseWriter, r *http.Request, idOrKey string, sub []string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	switch {
	case len(sub) == 0 && r.Method == http.MethodGet:
		worklogs, err := h.Store.WorklogsByIssue(r.Context(), issue.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		beans := make([]map[string]any, 0, len(worklogs))
		for _, wl := range worklogs {
			beans = append(beans, h.worklogBean(wl))
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"startAt": 0, "maxResults": 5000, "total": len(beans), "worklogs": beans,
		})
	case len(sub) == 0 && r.Method == http.MethodPost:
		var req struct {
			TimeSpentSeconds int             `json:"timeSpentSeconds"`
			Comment          json.RawMessage `json:"comment"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TimeSpentSeconds <= 0 {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"timeSpentSeconds": "A positive timeSpentSeconds is required."})
			return
		}
		wl, _, err := h.Commands.AddWorklog(r.Context(), userID, wsID, issue.ID, req.Comment, req.TimeSpentSeconds)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, h.worklogBean(wl))
	case len(sub) == 1 && r.Method == http.MethodGet:
		wl, err := h.Store.WorklogByID(r.Context(), wsID, sub[0])
		if err != nil || wl.IssueID != issue.ID {
			jiraError(w, http.StatusNotFound, "Worklog does not exist.")
			return
		}
		writeJSON(w, http.StatusOK, h.worklogBean(wl))
	case len(sub) == 1 && r.Method == http.MethodDelete:
		wl, err := h.Store.WorklogByID(r.Context(), wsID, sub[0])
		if err != nil || wl.IssueID != issue.ID {
			jiraError(w, http.StatusNotFound, "Worklog does not exist.")
			return
		}
		if _, err := h.Commands.DeleteWorklog(r.Context(), userID, wsID, sub[0]); err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(sub) == 1 && r.Method == http.MethodPut:
		wl, err := h.Store.WorklogByID(r.Context(), wsID, sub[0])
		if err != nil || wl.IssueID != issue.ID {
			jiraError(w, http.StatusNotFound, "Worklog does not exist.")
			return
		}
		var request struct {
			TimeSpentSeconds *int            `json:"timeSpentSeconds"`
			Comment          json.RawMessage `json:"comment"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil {
			jiraError(w, http.StatusBadRequest, "Invalid request payload.")
			return
		}
		updated, _, err := h.Store.UpdateWorklog(r.Context(), userID, wsID, sub[0], request.Comment, request.TimeSpentSeconds)
		if err != nil {
			worklogError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.worklogBean(updated))
	case len(sub) == 0 && r.Method == http.MethodDelete:
		// Jira's bulk delete removes every worklog on the work item.
		worklogs, err := h.Store.WorklogsByIssue(r.Context(), issue.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		for _, wl := range worklogs {
			if _, err := h.Commands.DeleteWorklog(r.Context(), userID, wsID, wl.ID); err != nil {
				jiraError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	case len(sub) >= 2 && sub[1] == "properties":
		wl, err := h.Store.WorklogByID(r.Context(), wsID, sub[0])
		if err != nil || wl.IssueID != issue.ID {
			jiraError(w, http.StatusNotFound, "Worklog does not exist.")
			return
		}
		h.worklogProperties(w, r, issue.Key, wl.ID, sub[2:])
	case len(sub) == 1 && sub[0] == "move" && r.Method == http.MethodPost:
		var request struct {
			IDs          []string `json:"ids"`
			IssueIDOrKey string   `json:"issueIdOrKey"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil {
			jiraError(w, http.StatusBadRequest, "Invalid request payload.")
			return
		}
		if len(request.IDs) == 0 || len(request.IDs) > 1000 {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"ids": "Between 1 and 1000 worklog ids are required."})
			return
		}
		target, e := h.resolveIssue(r, wsID, request.IssueIDOrKey)
		if e != nil {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"issueIdOrKey": "The destination work item does not exist."})
			return
		}
		if err := h.Store.MoveWorklogs(r.Context(), userID, wsID, issue.ID, target.ID, request.IDs); err != nil {
			worklogError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

// worklogProperties serves the entity properties hung off one worklog, with
// Jira's 201-on-create and 200-on-replace split.
func (h *Handler) worklogProperties(w http.ResponseWriter, r *http.Request, issueKey, worklogID string, rest []string) {
	self := h.BaseURL + "/rest/api/3/issue/" + issueKey + "/worklog/" + worklogID + "/properties/"
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		keys, err := h.Store.WorklogPropertyKeys(r.Context(), worklogID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		values := make([]map[string]any, 0, len(keys))
		for _, key := range keys {
			values = append(values, map[string]any{"key": key, "self": self + key})
		}
		writeJSON(w, http.StatusOK, map[string]any{"keys": values})
	case len(rest) == 1 && r.Method == http.MethodGet:
		value, err := h.Store.WorklogProperty(r.Context(), worklogID, rest[0])
		if err != nil {
			jiraError(w, http.StatusNotFound, "The worklog property does not exist.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": rest[0], "value": value, "self": self + rest[0]})
	case len(rest) == 1 && r.Method == http.MethodPut:
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The property value is invalid.")
			return
		}
		created, err := h.Store.SetWorklogProperty(r.Context(), worklogID, rest[0], raw)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The property key or value is invalid.")
			return
		}
		if created {
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusOK)
	case len(rest) == 1 && r.Method == http.MethodDelete:
		if err := h.Store.DeleteWorklogProperty(r.Context(), worklogID, rest[0]); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				jiraError(w, http.StatusNotFound, "The worklog property does not exist.")
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

func worklogError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrWorklogValidation):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		jiraError(w, http.StatusNotFound, "Worklog does not exist.")
	default:
		jiraError(w, http.StatusInternalServerError, "Could not complete the worklog operation.")
	}
}

// worklogFeedRoute serves Jira's workspace-wide worklog endpoints: the updated
// and deleted feeds, and the bulk fetch by id.
func (h *Handler) worklogFeedRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	switch {
	case path == "/worklog/updated" || path == "/worklog/deleted":
		if r.Method != http.MethodGet {
			jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		since := time.Time{}
		if raw := r.URL.Query().Get("since"); raw != "" {
			millis, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || millis < 0 {
				jiraError(w, http.StatusBadRequest, "since must be a millisecond timestamp.")
				return
			}
			since = time.UnixMilli(millis).UTC()
		}
		const limit = 1000
		changes, err := h.Store.WorklogsChangedSince(r.Context(), workspaceID, userID, since, path == "/worklog/deleted", limit)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		values := make([]map[string]any, 0, len(changes))
		latest := since
		for _, change := range changes {
			values = append(values, map[string]any{
				"worklogId": wireNumericID(change.ID), "updatedTime": change.UpdatedTime.UnixMilli(),
				"properties": []any{},
			})
			if change.UpdatedTime.After(latest) {
				latest = change.UpdatedTime
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"values": values, "since": since.UnixMilli(), "until": latest.UnixMilli(),
			"self": h.BaseURL + "/rest/api/3" + path, "lastPage": len(values) < limit,
		})
	case path == "/worklog/list":
		if r.Method != http.MethodPost {
			jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		var request struct {
			IDs []json.RawMessage `json:"ids"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&request); err != nil {
			jiraError(w, http.StatusBadRequest, "Invalid request payload.")
			return
		}
		if len(request.IDs) == 0 || len(request.IDs) > 1000 {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"ids": "Between 1 and 1000 worklog ids are required."})
			return
		}
		ids := make([]string, 0, len(request.IDs))
		for _, raw := range request.IDs {
			ids = append(ids, strings.Trim(string(raw), `"`))
		}
		worklogs, err := h.Store.WorklogsByIDs(r.Context(), workspaceID, userID, ids)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		beans := make([]map[string]any, 0, len(worklogs))
		for _, wl := range worklogs {
			beans = append(beans, h.worklogBean(wl))
		}
		writeJSON(w, http.StatusOK, beans)
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

// ---- attachments ----

func (h *Handler) attachmentSettings(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	configuration, err := h.Store.JiraSiteConfiguration(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load attachment settings.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": h.Blobs != nil && configuration.AttachmentsEnabled, "uploadLimit": 32 << 20})
}

func (h *Handler) attachmentBean(a *models.Attachment) map[string]any {
	bean := map[string]any{
		"id":       a.ID,
		"self":     h.BaseURL + "/rest/api/3/attachment/" + a.ID,
		"filename": a.Filename,
		"mimeType": a.MimeType,
		"size":     a.Size,
		"created":  a.Created,
		"author":   map[string]any{"accountId": a.AuthorID, "displayName": a.AuthorName, "active": true, "accountType": "atlassian"},
		"content":  h.BaseURL + "/rest/api/3/attachment/content/" + a.ID,
	}
	if strings.HasPrefix(a.MimeType, "image/") {
		bean["thumbnail"] = h.BaseURL + "/rest/api/3/attachment/thumbnail/" + a.ID
	}
	return bean
}

// uploadAttachments implements POST /issue/{id}/attachments with Jira's CSRF
// semantics: multipart/form-data plus the X-Atlassian-Token: no-check header.
func (h *Handler) uploadAttachments(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if r.Header.Get("X-Atlassian-Token") != "no-check" {
		w.Header().Set("X-Atlassian-Token", "no-check")
		jiraError(w, http.StatusForbidden, "XSRF check failed")
		return
	}
	issue, e := h.resolveIssue(r, wsID, idOrKey)
	if e != nil {
		writeJerr(w, e)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)        // bounded: 32MB max upload
	if err := r.ParseMultipartForm(32 << 20); err != nil { // #nosec G120 -- body capped by MaxBytesReader above
		jiraError(w, http.StatusBadRequest, "multipart/form-data body required")
		return
	}
	defer cleanupMultipart(r)
	beans := []map[string]any{}
	for _, files := range r.MultipartForm.File {
		for _, fh := range files {
			f, err := fh.Open()
			if err != nil {
				log.Printf("attachment open: %v", err)
				continue
			}
			att, _, err := h.Commands.AddAttachment(r.Context(), userID, wsID, issue.ID, fh.Filename, fh.Header.Get("Content-Type"), f)
			if closeErr := f.Close(); closeErr != nil {
				log.Printf("attachment close: %v", closeErr)
			}
			if err != nil {
				log.Printf("attachment store: %v", err)
				continue
			}
			beans = append(beans, h.attachmentBean(att))
		}
	}
	if len(beans) == 0 {
		jiraError(w, http.StatusBadRequest, "No attachments were uploaded.")
		return
	}
	writeJSON(w, http.StatusOK, beans)
}

// putAssignee implements PUT /issue/{idOrKey}/assignee.
func (h *Handler) putAssignee(w http.ResponseWriter, r *http.Request, idOrKey string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var req struct {
		AccountID *string `json:"accountId"`
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"accountId": "Invalid request payload."})
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"accountId": "Invalid request payload."})
		return
	}
	if req.AccountID == nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"accountId": "accountId is required (null to unassign via PUT /issue)."})
		return
	}
	if _, _, err := h.Commands.UpdateIssue(r.Context(), commands.UpdateIssueInput{
		ActorID: userID, WorkspaceID: wsID, IssueIDOrKey: idOrKey,
		AssigneeID: req.AccountID,
	}); err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cleanupMultipart removes request temp files; failures are logged, never silent.
func cleanupMultipart(r *http.Request) {
	if r.MultipartForm == nil {
		return
	}
	if err := r.MultipartForm.RemoveAll(); err != nil {
		log.Printf("multipart cleanup: %v", err)
	}
}

func (h *Handler) attachmentMeta(w http.ResponseWriter, r *http.Request, id string) {
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	att, e := h.attachmentForUser(r, wsID, userID, id)
	if e != nil {
		writeJerr(w, e)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.attachmentBean(att))
	case http.MethodDelete:
		if att.AuthorID != userID {
			jiraError(w, http.StatusForbidden, "Only the author may delete an attachment.")
			return
		}
		if _, err := h.Commands.DeleteAttachment(r.Context(), userID, wsID, id); err != nil {
			// The command error can contain request-derived identifiers. Keep the
			// failure observable without allowing forged log lines.
			log.Print("attachment delete failed after authorization")
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, DELETE")
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) attachmentContent(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if _, e := h.attachmentForUser(r, wsID, userID, id); e != nil {
		writeJerr(w, e)
		return
	}
	blobRef, filename, mimeType, err := h.Store.AttachmentBlobRef(r.Context(), wsID, id)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Attachment does not exist.")
		return
	}
	rc, size, err := h.Blobs.Get(r.Context(), blobRef)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Attachment does not exist.")
		return
	}
	defer func() {
		if err := rc.Close(); err != nil {
			log.Printf("attachment content close: %v", err)
		}
	}()
	if r.Header.Get("Range") != "" {
		contents, readErr := io.ReadAll(io.LimitReader(rc, (32<<20)+1))
		if readErr != nil || len(contents) > 32<<20 {
			jiraError(w, http.StatusInternalServerError, "Attachment content could not be read.")
			return
		}
		w.Header().Set("Content-Type", mimeType)
		if disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); disposition != "" {
			w.Header().Set("Content-Disposition", disposition)
		}
		http.ServeContent(w, r, filename, time.Time{}, bytes.NewReader(contents))
		return
	}
	w.Header().Set("Content-Type", mimeType)
	if disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	if _, err := io.Copy(w, rc); err != nil {
		log.Printf("attachment content stream: %v", err)
	}
}

func (h *Handler) attachmentThumbnail(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	att, e := h.attachmentForUser(r, wsID, userID, id)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if !strings.HasPrefix(att.MimeType, "image/") && r.URL.Query().Get("fallbackToDefault") == "false" {
		jiraError(w, http.StatusNotFound, "Attachment does not have a thumbnail.")
		return
	}
	blobRef, _, mimeType, err := h.Store.AttachmentBlobRef(r.Context(), wsID, id)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Attachment does not exist.")
		return
	}
	rc, size, err := h.Blobs.Get(r.Context(), blobRef)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Attachment does not exist.")
		return
	}
	defer func() {
		if closeErr := rc.Close(); closeErr != nil {
			log.Printf("attachment thumbnail close: %v", closeErr)
		}
	}()
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", "inline")
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	if _, err := io.Copy(w, rc); err != nil {
		log.Printf("attachment thumbnail stream: %v", err)
	}
}

func (h *Handler) attachmentArchive(w http.ResponseWriter, r *http.Request, id, representation string) {
	if representation != "human" && representation != "raw" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	wsID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	att, e := h.attachmentForUser(r, wsID, userID, id)
	if e != nil {
		writeJerr(w, e)
		return
	}
	blobRef, _, _, err := h.Store.AttachmentBlobRef(r.Context(), wsID, id)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Attachment does not exist.")
		return
	}
	rc, _, err := h.Blobs.Get(r.Context(), blobRef)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Attachment does not exist.")
		return
	}
	contents, readErr := io.ReadAll(io.LimitReader(rc, (32<<20)+1))
	closeErr := rc.Close()
	if readErr != nil || closeErr != nil || len(contents) > 32<<20 {
		jiraError(w, http.StatusInternalServerError, "Attachment archive could not be read.")
		return
	}
	archive, err := zip.NewReader(bytes.NewReader(contents), int64(len(contents)))
	if err != nil {
		if strings.EqualFold(filepath.Ext(att.Filename), ".zip") || att.MimeType == "application/zip" {
			jiraError(w, http.StatusConflict, "Attachment archive is corrupt or unsupported.")
			return
		}
		if representation == "human" {
			writeJSON(w, http.StatusOK, map[string]any{"id": att.ID, "name": att.Filename, "mediaType": att.MimeType, "entries": []any{}, "totalEntryCount": 0})
		} else {
			writeJSON(w, http.StatusOK, map[string]any{"entries": []any{}, "totalEntryCount": 0})
		}
		return
	}
	entries := make([]map[string]any, 0, len(archive.File))
	for index, file := range archive.File {
		mediaType := mime.TypeByExtension(filepath.Ext(file.Name))
		if mediaType == "" {
			mediaType = "application/octet-stream"
		}
		if representation == "human" {
			entries = append(entries, map[string]any{"index": index, "label": file.Name, "path": file.Name, "mediaType": mediaType, "size": humanAttachmentSize(file.UncompressedSize64)})
		} else {
			entries = append(entries, map[string]any{"entryIndex": index, "name": file.Name, "mediaType": mediaType, "size": file.UncompressedSize64})
		}
	}
	if representation == "human" {
		writeJSON(w, http.StatusOK, map[string]any{"id": att.ID, "name": att.Filename, "mediaType": att.MimeType, "entries": entries, "totalEntryCount": len(entries)})
	} else {
		writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "totalEntryCount": len(entries)})
	}
}

func humanAttachmentSize(size uint64) string {
	if size < 1000 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1_000_000 {
		return fmt.Sprintf("%.1f kB", float64(size)/1000)
	}
	return fmt.Sprintf("%.2f MB", float64(size)/1_000_000)
}

// attachmentForUser keeps attachment metadata and bytes behind the same
// workspace and issue-security checks as the issue itself.
func (h *Handler) attachmentForUser(r *http.Request, workspaceID, userID, attachmentID string) (*models.Attachment, *jerr) {
	att, err := h.Store.AttachmentByID(r.Context(), workspaceID, attachmentID)
	if err != nil {
		return nil, &jerr{status: http.StatusNotFound, message: "Attachment does not exist."}
	}
	issue, e := h.resolveIssue(r, workspaceID, att.IssueID)
	if e != nil {
		return nil, &jerr{status: http.StatusNotFound, message: "Attachment does not exist."}
	}
	visible, err := authz.CanSeeIssue(r.Context(), h.Store, workspaceID, issue.ProjectID, userID, issue.ID, issue.SecurityLevelID)
	if err != nil {
		return nil, &jerr{status: http.StatusInternalServerError, message: "internal error"}
	}
	if !visible {
		return nil, &jerr{status: http.StatusNotFound, message: "Attachment does not exist."}
	}
	isService, public, err := h.Store.ServiceAttachmentIsPublic(r.Context(), attachmentID)
	if err != nil {
		return nil, &jerr{status: http.StatusInternalServerError, message: "internal error"}
	}
	if isService && !public {
		canManage, err := h.Store.CanManageServiceRequest(r.Context(), workspaceID, userID, att.IssueID)
		if err != nil {
			return nil, &jerr{status: http.StatusInternalServerError, message: "internal error"}
		}
		if !canManage {
			return nil, &jerr{status: http.StatusNotFound, message: "Attachment does not exist."}
		}
	}
	return att, nil
}
