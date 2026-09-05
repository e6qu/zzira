package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type adminGroupRow struct {
	Group         *models.Group
	MemberIDs     map[string]bool
	ProductAccess map[string]bool
}

type adminPageData struct {
	Organization     *models.Organization
	Site             *models.Site
	Products         []*models.Product
	Directory        *models.Directory
	Groups           []adminGroupRow
	Users            []*models.User
	Audit            []*models.OrganizationAuditEvent
	Error            string
	Saved            string
	GroupName        string
	GroupDescription string
}

func (h *Handler) adminData(r *http.Request, workspaceID, message string) (adminPageData, error) {
	organization, err := h.Store.OrganizationByWorkspace(r.Context(), workspaceID)
	if err != nil {
		return adminPageData{}, err
	}
	site, err := h.Store.SiteByWorkspace(r.Context(), workspaceID)
	if err != nil {
		return adminPageData{}, err
	}
	products, err := h.Store.ProductsBySite(r.Context(), site.ID)
	if err != nil {
		return adminPageData{}, err
	}
	directories, err := h.Store.DirectoriesByOrganization(r.Context(), organization.ID)
	if err != nil {
		return adminPageData{}, err
	}
	data := adminPageData{
		Organization: organization,
		Site:         site,
		Products:     products,
		Groups:       []adminGroupRow{},
		Users:        []*models.User{},
		Audit:        []*models.OrganizationAuditEvent{},
		Error:        message,
		Saved:        r.URL.Query().Get("saved"),
	}
	if len(directories) == 0 {
		return data, nil
	}
	data.Directory = directories[0]
	data.Users, err = h.Store.DirectoryUsers(r.Context(), data.Directory.ID)
	if err != nil {
		return adminPageData{}, err
	}
	groups, err := h.Store.GroupsByDirectory(r.Context(), data.Directory.ID)
	if err != nil {
		return adminPageData{}, err
	}
	for _, group := range groups {
		memberIDs, err := h.Store.GroupMemberIDs(r.Context(), group.ID)
		if err != nil {
			return adminPageData{}, err
		}
		membership := make(map[string]bool, len(memberIDs))
		for _, userID := range memberIDs {
			membership[userID] = true
		}
		bindings, err := h.Store.RoleBindingsForPrincipal(r.Context(), workspaceID, "group", group.ID)
		if err != nil {
			return adminPageData{}, err
		}
		productAccess := make(map[string]bool)
		for _, binding := range bindings {
			if binding.ScopeType == "product" && binding.RoleKey == "atlassian/user" {
				productAccess[binding.ScopeID] = true
			}
		}
		data.Groups = append(data.Groups, adminGroupRow{Group: group, MemberIDs: membership, ProductAccess: productAccess})
	}
	data.Audit, err = h.Store.OrganizationAuditEvents(r.Context(), organization.ID, 20)
	if err != nil {
		return adminPageData{}, err
	}
	return data, nil
}

func (h *Handler) AdminPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	data, err := h.adminData(r, workspaceID, "")
	if err != nil {
		http.Error(w, "load administration", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_admin", user, workspaceID, data, "admin", "")
}

func (h *Handler) CreateAdminGroup(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	data, err := h.adminData(r, workspaceID, "")
	if err != nil {
		http.Error(w, "load administration", http.StatusInternalServerError)
		return
	}
	if data.Directory == nil {
		http.Error(w, "organization has no active directory", http.StatusConflict)
		return
	}
	_, err = h.Store.CreateDirectoryGroup(r.Context(), workspaceID, user.ID, data.Directory.ID, r.FormValue("name"), r.FormValue("description"))
	if err != nil {
		status := http.StatusInternalServerError
		message := "Group creation failed."
		if errors.Is(err, store.ErrAdminValidation) {
			status = http.StatusBadRequest
			message = strings.TrimSpace(strings.TrimPrefix(err.Error(), store.ErrAdminValidation.Error()+":"))
		} else if errors.Is(err, store.ErrAdminConflict) {
			status = http.StatusConflict
			message = strings.TrimSpace(strings.TrimPrefix(err.Error(), store.ErrAdminConflict.Error()+":"))
		}
		data, dataErr := h.adminData(r, workspaceID, message)
		if dataErr != nil {
			http.Error(w, "load administration", http.StatusInternalServerError)
			return
		}
		data.GroupName = r.FormValue("name")
		data.GroupDescription = r.FormValue("description")
		h.writeWorkspacePageStatus(w, r, "page_admin", user, workspaceID, data, "admin", "", status)
		return
	}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape("Group created"), http.StatusSeeOther)
}

func (h *Handler) UpdateAdminGroupMember(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	data, err := h.adminData(r, workspaceID, "")
	if err != nil {
		http.Error(w, "load administration", http.StatusInternalServerError)
		return
	}
	if data.Directory == nil {
		http.Error(w, "organization has no active directory", http.StatusConflict)
		return
	}
	action := r.FormValue("action")
	if action != "add" && action != "remove" {
		http.Error(w, "action must be add or remove", http.StatusBadRequest)
		return
	}
	err = h.Store.SetGroupMember(r.Context(), workspaceID, user.ID, data.Directory.ID, r.PathValue("groupId"), r.FormValue("accountId"), action == "add")
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, store.ErrAdminValidation):
			status = http.StatusBadRequest
		case errors.Is(err, store.ErrAdminConflict):
			status = http.StatusConflict
		case errors.Is(err, store.ErrAdminNotFound), errors.Is(err, pgx.ErrNoRows):
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	message := "Member added"
	if action == "remove" {
		message = "Member removed"
	}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape(message), http.StatusSeeOther)
}

func (h *Handler) UpdateAdminGroupRole(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	action := r.FormValue("action")
	if action != "assign" && action != "revoke" {
		http.Error(w, "action must be assign or revoke", http.StatusBadRequest)
		return
	}
	err := h.Store.SetRoleBinding(r.Context(), workspaceID, user.ID, "group", r.PathValue("groupId"), "product", r.FormValue("productId"), "atlassian/user", action == "assign")
	if err != nil {
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, store.ErrAdminValidation):
			status = http.StatusBadRequest
		case errors.Is(err, store.ErrAdminNotFound), errors.Is(err, pgx.ErrNoRows):
			status = http.StatusNotFound
		}
		http.Error(w, err.Error(), status)
		return
	}
	message := "Product access granted"
	if action == "revoke" {
		message = "Product access revoked"
	}
	http.Redirect(w, r, "/admin?saved="+url.QueryEscape(message), http.StatusSeeOther)
}
