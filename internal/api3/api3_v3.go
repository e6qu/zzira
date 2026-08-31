package api3

import (
	"encoding/json"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
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
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

// ---- attachments ----

func (h *Handler) attachmentBean(a *models.Attachment) map[string]any {
	return map[string]any{
		"id":       a.ID,
		"self":     h.BaseURL + "/rest/api/3/attachment/" + a.ID,
		"filename": a.Filename,
		"mimeType": a.MimeType,
		"size":     a.Size,
		"created":  a.Created,
		"author":   map[string]any{"accountId": a.AuthorID, "displayName": a.AuthorName, "active": true, "accountType": "atlassian"},
		"content":  h.BaseURL + "/rest/api/3/attachment/content/" + a.ID,
	}
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
				continue
			}
			att, _, err := h.Commands.AddAttachment(r.Context(), userID, wsID, issue.ID, fh.Filename, fh.Header.Get("Content-Type"), f)
			if closeErr := f.Close(); closeErr != nil {
				log.Printf("attachment close: %v", closeErr)
			}
			if err != nil {
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
	writeJSON(w, http.StatusOK, h.attachmentBean(att))
}

func (h *Handler) attachmentContent(w http.ResponseWriter, r *http.Request, id string) {
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
	defer rc.Close()
	w.Header().Set("Content-Type", mimeType)
	if disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename}); disposition != "" {
		w.Header().Set("Content-Disposition", disposition)
	}
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	_, _ = io.Copy(w, rc)
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
	visible, err := authz.CanSeeIssue(r.Context(), h.Store, workspaceID, issue.ProjectID, userID, issue.SecurityLevelID)
	if err != nil {
		return nil, &jerr{status: http.StatusInternalServerError, message: "internal error"}
	}
	if !visible {
		return nil, &jerr{status: http.StatusNotFound, message: "Attachment does not exist."}
	}
	return att, nil
}
