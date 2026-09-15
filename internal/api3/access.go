package api3

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// canBrowseProject reports whether the caller holds Browse Projects for the
// project. An unknown project is simply not browsable.
func (h *Handler) canBrowseProject(r *http.Request, workspaceID, userID, projectIDOrKey string) (bool, error) {
	return h.hasProjectPermission(r.Context(), workspaceID, userID, projectIDOrKey, "", "BROWSE_PROJECTS")
}

func (h *Handler) hasProjectPermission(ctx context.Context, workspaceID, userID, projectIDOrKey, issueID, permission string) (bool, error) {
	allowed, err := h.Store.HasProjectPermission(ctx, workspaceID, userID, projectIDOrKey, issueID, permission)
	if errors.Is(err, store.ErrPermissionSchemeNotFound) {
		return false, nil
	}
	return allowed, err
}

type requestViewerKey struct{}

// requestViewer resolves, once per request, who is reading a response, so user
// beans can apply Jira's default profile visibility: an email address is shown
// to the person themselves and to administrators.
type requestViewer struct {
	once    sync.Once
	resolve func() (workspaceID, userID string, admin bool)
	userID  string
	admin   bool
}

func (h *Handler) withRequestViewer(r *http.Request) *http.Request {
	viewer := &requestViewer{resolve: func() (string, string, bool) {
		userID, err := authn.Identify(r.Context(), h.Store, r)
		if err != nil || userID == "" {
			return "", "", false
		}
		workspaceID, err := h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
		if err != nil {
			return "", userID, false
		}
		admin, err := h.Store.IsAdmin(r.Context(), workspaceID, userID)
		return workspaceID, userID, err == nil && admin
	}}
	return r.WithContext(context.WithValue(r.Context(), requestViewerKey{}, viewer))
}

// userBeanFor is the user bean as the request's reader may see it.
func (h *Handler) userBeanFor(ctx context.Context, u *models.User) map[string]any {
	bean := h.userBean(u)
	viewer, _ := ctx.Value(requestViewerKey{}).(*requestViewer)
	if viewer == nil {
		delete(bean, "emailAddress")
		return bean
	}
	viewer.once.Do(func() { _, viewer.userID, viewer.admin = viewer.resolve() })
	if !viewer.admin && (viewer.userID == "" || viewer.userID != u.ID) {
		delete(bean, "emailAddress")
	}
	return bean
}

// requireIssuePermission answers 403 unless the caller holds the project
// permission for the issue.
func (h *Handler) requireIssuePermission(w http.ResponseWriter, r *http.Request, workspaceID, userID string, issue *models.Issue, permission, message string) bool {
	allowed, err := h.hasProjectPermission(r.Context(), workspaceID, userID, issue.ProjectID, issue.ID, permission)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return false
	}
	if !allowed {
		jiraError(w, http.StatusForbidden, message)
	}
	return allowed
}
