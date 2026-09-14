package confluence

import (
	"context"
	"net/http"

	"github.com/e6qu/zzira/internal/models"
)

// Confluence reports what a caller may do with a space or a piece of content
// as a list of operations. Beyond reading, editing and deleting, the list
// covers exporting, copying, moving, archiving, restricting and purging, and
// creating the things that live on it — comments and attachments on content,
// every kind of content in a space. Each is decided by the permissions the
// caller holds in the space and by the content's own restrictions.

func operation(name, target string) map[string]string {
	return map[string]string{"operation": name, "targetType": target}
}

// spaceAbilities are the space permissions the caller holds and whether they
// administer the space.
func (h *Handler) spaceAbilities(ctx context.Context, ws, actor, spaceID string) (map[string]bool, bool, error) {
	held, err := h.Store.WikiSpaceHeldPermissions(ctx, ws, actor, spaceID)
	if err != nil {
		return nil, false, err
	}
	admin, err := h.Store.CanAdministerWikiSpace(ctx, ws, actor, spaceID)
	return held, admin, err
}

func (h *Handler) pageOperationValues(ctx context.Context, ws, actor string, page *models.WikiPage) ([]any, error) {
	canUpdate, err := h.Store.CanUpdateWikiPage(ctx, ws, actor, page.ID)
	if err != nil {
		return nil, err
	}
	canDelete, err := h.Store.CanDeleteWikiPage(ctx, ws, actor, page.ID)
	if err != nil {
		return nil, err
	}
	canRestrict, err := h.Store.CanRestrictWikiPage(ctx, ws, actor, page.ID)
	if err != nil {
		return nil, err
	}
	held, admin, err := h.spaceAbilities(ctx, ws, actor, page.SpaceID)
	if err != nil {
		return nil, err
	}
	operations := []any{operation("read", "page"), operation("export", "page")}
	if canUpdate {
		operations = append(operations, operation("update", "page"), operation("archive", "page"))
	}
	if canDelete {
		operations = append(operations, operation("delete", "page"))
	}
	if held["create/page"] {
		operations = append(operations, operation("copy", "page"))
		if canUpdate {
			operations = append(operations, operation("move", "page"))
		}
	}
	if canRestrict {
		operations = append(operations, operation("restrict_content", "page"))
	}
	if admin && canDelete {
		operations = append(operations, operation("purge", "page"), operation("purge_version", "page"))
	}
	if held["create/comment"] {
		operations = append(operations, operation("create", "comment"))
	}
	if canUpdate && held["create/attachment"] {
		operations = append(operations, operation("create", "attachment"))
	}
	return operations, nil
}

func (h *Handler) blogPostOperationValues(ctx context.Context, ws, actor string, post *models.WikiBlogPost) ([]any, error) {
	canUpdate, err := h.Store.CanUpdateWikiBlogPost(ctx, ws, actor, post.ID)
	if err != nil {
		return nil, err
	}
	canDelete, err := h.Store.CanDeleteWikiBlogPost(ctx, ws, actor, post.ID)
	if err != nil {
		return nil, err
	}
	held, admin, err := h.spaceAbilities(ctx, ws, actor, post.SpaceID)
	if err != nil {
		return nil, err
	}
	operations := []any{operation("read", "blogpost"), operation("export", "blogpost")}
	if canUpdate {
		operations = append(operations, operation("update", "blogpost"))
	}
	if canDelete {
		operations = append(operations, operation("delete", "blogpost"))
	}
	if held["create/blogpost"] {
		operations = append(operations, operation("copy", "blogpost"))
	}
	if admin && canDelete {
		operations = append(operations, operation("purge", "blogpost"), operation("purge_version", "blogpost"))
	}
	if held["create/comment"] {
		operations = append(operations, operation("create", "comment"))
	}
	if canUpdate && held["create/attachment"] {
		operations = append(operations, operation("create", "attachment"))
	}
	return operations, nil
}

// contentOperationList covers folders, databases, whiteboards, Smart Links
// and custom content.
func (h *Handler) contentOperationList(r *http.Request, ws, actor, id, contentType string) ([]any, error) {
	ctx := r.Context()
	content, err := h.Store.WikiContent(ctx, ws, actor, id, contentType)
	if err != nil {
		return nil, err
	}
	canUpdate, err := h.Store.CanUpdateWikiContent(ctx, ws, actor, id, contentType)
	if err != nil {
		return nil, err
	}
	canDelete, err := h.Store.CanDeleteWikiContent(ctx, ws, actor, id, contentType)
	if err != nil {
		return nil, err
	}
	held, admin, err := h.spaceAbilities(ctx, ws, actor, content.SpaceID)
	if err != nil {
		return nil, err
	}
	operations := []any{operation("read", contentType)}
	if contentType == "database" || contentType == "whiteboard" {
		operations = append(operations, operation("export", contentType))
	}
	if canUpdate {
		operations = append(operations, operation("update", contentType))
	}
	if canDelete {
		operations = append(operations, operation("delete", contentType))
	}
	if held["create/"+contentType] {
		operations = append(operations, operation("copy", contentType))
		if canUpdate {
			operations = append(operations, operation("move", contentType))
		}
	}
	if admin && canDelete {
		operations = append(operations, operation("purge", contentType))
	}
	if contentType == "custom" && held["create/comment"] {
		operations = append(operations, operation("create", "comment"))
	}
	return operations, nil
}

func (h *Handler) attachmentOperationValues(r *http.Request, ws, actor string, a *models.WikiAttachment) []any {
	operations := []any{operation("read", "attachment")}
	canUpdate, _ := h.Store.CanUpdateWikiAttachment(r.Context(), ws, actor, a.ID)
	canDelete, _ := h.Store.CanDeleteWikiAttachment(r.Context(), ws, actor, a.ID)
	if canUpdate {
		operations = append(operations, operation("update", "attachment"))
	}
	if canDelete {
		operations = append(operations, operation("delete", "attachment"))
		if admin, err := h.Store.CanAdministerWikiSpace(r.Context(), ws, actor, a.SpaceID); err == nil && admin {
			operations = append(operations, operation("purge", "attachment"))
		}
	}
	return operations
}

// spaceOperationValues lists what the caller may do in a space: read and
// export it, create each kind of content they hold permission to, and, as its
// administrator, update, archive, delete, restrict and administer it.
func (h *Handler) spaceOperationValues(r *http.Request, ws, actor, id string) ([]any, error) {
	held, admin, err := h.spaceAbilities(r.Context(), ws, actor, id)
	if err != nil {
		return nil, err
	}
	operations := []any{operation("read", "space"), operation("export", "space")}
	for _, target := range []string{"page", "blogpost", "comment", "attachment", "folder", "embed", "database", "whiteboard", "custom"} {
		if held["create/"+target] {
			operations = append(operations, operation("create", target))
		}
	}
	if admin {
		operations = append(operations, operation("update", "space"), operation("archive", "space"), operation("delete", "space"), operation("restrict_content", "space"), operation("administer", "space"))
	}
	return operations, nil
}
