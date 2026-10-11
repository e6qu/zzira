package web

import (
	"net/http"
	"time"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/models"
)

type anonymousIssueData struct {
	Issue        *models.Issue
	Project      *models.Project
	Comments     []anonymousCommentView
	Attachments  []*models.Attachment
	DueDateLabel string
}

type anonymousCommentView struct {
	*models.Comment
	When string
}

// Public issue pages use the anonymous permission subject, never a workspace
// member standing in for the visitor. No navigation, editor or history writer
// is invoked for this read.
func (h *Handler) serveAnonymousIssue(w http.ResponseWriter, r *http.Request, key string) {
	w.Header().Set("Cache-Control", "private, no-store")
	r = r.WithContext(authn.WithAnonymous(r.Context()))
	workspaceID, err := h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	issue, err := h.issueForUser(r, &models.User{}, workspaceID, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, issue.ProjectID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	look := h.siteLook(r, workspaceID)
	data := anonymousIssueData{Issue: issue, Project: project, DueDateLabel: issue.DueDate}
	if date, err := time.Parse("2006-01-02", issue.DueDate); err == nil {
		data.DueDateLabel = date.Format(look.DateDay)
	}
	comments, err := h.Store.CommentsByIssue(r.Context(), issue.ID)
	if err != nil {
		http.Error(w, "Could not load comments.", 500)
		return
	}
	for _, comment := range comments {
		visible, err := h.Store.CommentVisibleTo(r.Context(), workspaceID, issue.ProjectID, "", comment)
		if err != nil {
			http.Error(w, "Could not load comments.", 500)
			return
		}
		if visible {
			when := comment.Created
			if date, err := time.Parse(time.RFC3339, comment.Created); err == nil {
				when = date.UTC().Format(look.DateComplete) + " UTC"
			}
			data.Comments = append(data.Comments, anonymousCommentView{Comment: comment, When: when})
		}
	}
	attachments, err := h.Store.AttachmentsByIssue(r.Context(), issue.ID)
	if err != nil {
		http.Error(w, "Could not load attachments.", 500)
		return
	}
	for _, attachment := range attachments {
		isService, public, err := h.Store.ServiceAttachmentIsPublic(r.Context(), attachment.ID)
		if err != nil {
			http.Error(w, "Could not load attachments.", 500)
			return
		}
		if !isService || public {
			data.Attachments = append(data.Attachments, attachment)
		}
	}
	behaviour, err := h.Store.ResolveFieldBehaviour(r.Context(), workspaceID, issue.ProjectID, issue.IssueType.ID)
	if err != nil {
		http.Error(w, "Could not load field rules.", 500)
		return
	}
	issue.FieldRenderers = map[string]string{}
	for field, rule := range behaviour {
		issue.FieldRenderers[field] = rule.Renderer
	}
	// Public access can be revoked immediately. Neither browser nor shared
	// proxies may retain a previously accessible work item page.
	writePage(w, "page_anonymous_issue", pageData{Data: data, Site: &look})
}
