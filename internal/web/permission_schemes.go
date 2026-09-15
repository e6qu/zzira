package web

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type permissionGrantView struct {
	Grant          models.PermissionGrant
	PermissionName string
	HolderLabel    string
}

type permissionSchemeCard struct {
	Scheme   *models.PermissionScheme
	Grants   []permissionGrantView
	Projects []*models.Project
}

type permissionSchemesData struct {
	Schemes     []permissionSchemeCard
	Permissions []store.PermissionDefinition
	Projects    []*models.Project
	Members     []*models.User
	Groups      []*models.Group
	Roles       []*models.ProjectRole
	Notice      string
	Error       string
}

type projectPermissionsData struct {
	Project *models.Project
	Scheme  permissionSchemeCard
}

func permissionSchemeMutationMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrPermissionSchemeValidation), errors.Is(err, store.ErrPermissionSchemeConflict),
		errors.Is(err, store.ErrPermissionSchemeNotFound), errors.Is(err, store.ErrProjectPermission):
		return err.Error()
	default:
		return "Could not update permission schemes."
	}
}

func permissionGrantLabel(grant models.PermissionGrant, members []*models.User, groups []*models.Group, roles []*models.ProjectRole) string {
	switch grant.HolderType {
	case "anyone":
		return "Anyone, including anonymous users"
	case "applicationRole":
		return "Product access: " + grant.HolderValue
	case "assignee":
		return "Current assignee"
	case "projectLead":
		return "Project lead"
	case "reporter":
		return "Work item reporter"
	case "sd.customer.portal.only":
		return "Service project customers (portal only)"
	case "userCustomField":
		return "User selected in " + grant.HolderValue
	case "groupCustomField":
		return "Group selected in " + grant.HolderValue
	case "user":
		for _, member := range members {
			if member.ID == grant.HolderValue {
				return member.DisplayName
			}
		}
	case "group":
		for _, group := range groups {
			if group.ID == grant.HolderValue {
				return group.Name
			}
		}
	case "projectRole":
		for _, role := range roles {
			if strconv.FormatInt(role.ID, 10) == grant.HolderValue {
				return role.Name + " project role"
			}
		}
	}
	if grant.HolderValue != "" {
		return grant.HolderValue
	}
	return grant.HolderType
}

func permissionSchemeCards(definitions []store.PermissionDefinition, schemes []*models.PermissionScheme, members []*models.User, groups []*models.Group, roles []*models.ProjectRole) []permissionSchemeCard {
	names := map[string]string{}
	for _, permission := range definitions {
		names[permission.Key] = permission.Name
	}
	cards := make([]permissionSchemeCard, 0, len(schemes))
	for _, scheme := range schemes {
		card := permissionSchemeCard{Scheme: scheme}
		for _, grant := range scheme.Grants {
			name := names[grant.Permission]
			if name == "" {
				name = grant.Permission
			}
			card.Grants = append(card.Grants, permissionGrantView{Grant: grant, PermissionName: name, HolderLabel: permissionGrantLabel(grant, members, groups, roles)})
		}
		cards = append(cards, card)
	}
	return cards
}

func (h *Handler) loadPermissionSchemesPage(r *http.Request, workspaceID string) (permissionSchemesData, error) {
	data := permissionSchemesData{Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	catalog, err := h.Store.PermissionCatalog(r.Context(), workspaceID)
	if err != nil {
		return data, err
	}
	for _, definition := range catalog {
		if definition.Type == "PROJECT" {
			data.Permissions = append(data.Permissions, definition)
		}
	}
	data.Projects, err = h.Store.ProjectsByWorkspace(r.Context(), workspaceID)
	if err == nil {
		data.Members, err = h.Store.MembersByWorkspace(r.Context(), workspaceID)
	}
	if err == nil {
		data.Groups, err = h.Store.GroupsByWorkspace(r.Context(), workspaceID)
	}
	if err == nil {
		data.Roles, err = h.Store.ProjectRoles(r.Context(), workspaceID)
	}
	var schemes []*models.PermissionScheme
	if err == nil {
		schemes, err = h.Store.PermissionSchemes(r.Context(), workspaceID, true)
	}
	if err != nil {
		return data, err
	}
	data.Schemes = permissionSchemeCards(data.Permissions, schemes, data.Members, data.Groups, data.Roles)
	for index := range data.Schemes {
		ids, lookupErr := h.Store.ProjectIDsForPermissionScheme(r.Context(), workspaceID, data.Schemes[index].Scheme.ID)
		if lookupErr != nil {
			return data, lookupErr
		}
		assigned := map[string]bool{}
		for _, id := range ids {
			assigned[id] = true
		}
		for _, project := range data.Projects {
			if assigned[project.ID] {
				data.Schemes[index].Projects = append(data.Schemes[index].Projects, project)
			}
		}
	}
	return data, nil
}

func (h *Handler) PermissionSchemesPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		_, err := h.Store.CreatePermissionScheme(r.Context(), workspaceID, user.ID, r.PostFormValue("name"), r.PostFormValue("description"), nil)
		if err != nil {
			redirectLocal(w, r, "/settings/permission-schemes?error="+url.QueryEscape(permissionSchemeMutationMessage(err)))
			return
		}
		redirectLocal(w, r, "/settings/permission-schemes?notice="+url.QueryEscape("Permission scheme created."))
		return
	}
	data, err := h.loadPermissionSchemesPage(r, workspaceID)
	if err != nil {
		http.Error(w, "Could not load permission schemes.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_permission_schemes", user, workspaceID, data, "permission-schemes", "")
}

func grantInputFromPermissionForm(r *http.Request) store.PermissionGrantInput {
	holderType := r.PostFormValue("holderType")
	value := r.PostFormValue("holderValue")
	switch holderType {
	case "user":
		value = r.PostFormValue("userValue")
	case "group":
		value = r.PostFormValue("groupValue")
	case "projectRole":
		value = r.PostFormValue("roleValue")
	}
	return store.PermissionGrantInput{Permission: r.PostFormValue("permission"), HolderType: holderType, HolderValue: value}
}

func (h *Handler) PermissionSchemeMutation(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	schemeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || schemeID <= 0 {
		http.NotFound(w, r)
		return
	}
	notice := "Permission scheme updated."
	switch r.PostFormValue("action") {
	case "update":
		_, err = h.Store.UpdatePermissionScheme(r.Context(), workspaceID, user.ID, schemeID, r.PostFormValue("name"), r.PostFormValue("description"), nil)
	case "delete":
		err = h.Store.DeletePermissionScheme(r.Context(), workspaceID, user.ID, schemeID)
		notice = "Permission scheme deleted."
	case "add-grant":
		_, err = h.Store.CreatePermissionGrant(r.Context(), workspaceID, user.ID, schemeID, grantInputFromPermissionForm(r))
		notice = "Permission grant added."
	case "remove-grant":
		var grantID int64
		grantID, err = strconv.ParseInt(r.PostFormValue("grantId"), 10, 64)
		if err == nil {
			err = h.Store.DeletePermissionGrant(r.Context(), workspaceID, user.ID, schemeID, grantID)
		}
		notice = "Permission grant removed."
	case "assign-project":
		_, _, err = h.Store.AssignPermissionScheme(r.Context(), workspaceID, user.ID, r.PostFormValue("project"), schemeID)
		notice = "Project permission scheme assigned."
	default:
		err = store.ErrPermissionSchemeValidation
	}
	target := "/settings/permission-schemes"
	if err != nil {
		redirectLocal(w, r, target+"?error="+url.QueryEscape(permissionSchemeMutationMessage(err)))
		return
	}
	redirectLocal(w, r, target+"?notice="+url.QueryEscape(notice))
}

func (h *Handler) ProjectPermissionsPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, project, ok := h.requireProjectAdminPage(w, r)
	if !ok {
		return
	}
	scheme, _, err := h.Store.AssignedPermissionScheme(r.Context(), workspaceID, project.ID, true)
	if err != nil {
		http.Error(w, "Could not load project permissions.", http.StatusInternalServerError)
		return
	}
	members, err := h.Store.MembersByWorkspace(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load project permissions.", http.StatusInternalServerError)
		return
	}
	groups, err := h.Store.GroupsByWorkspace(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load project permissions.", http.StatusInternalServerError)
		return
	}
	roles, err := h.Store.ProjectRoles(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load project permissions.", http.StatusInternalServerError)
		return
	}
	catalog, err := h.Store.PermissionCatalog(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load project permissions.", http.StatusInternalServerError)
		return
	}
	cards := permissionSchemeCards(catalog, []*models.PermissionScheme{scheme}, members, groups, roles)
	data := projectPermissionsData{Project: project, Scheme: cards[0]}
	h.writeWorkspacePage(w, r, "page_project_permissions", user, workspaceID, data, "project-permissions", project.Key)
}

type permissionHelperResult struct {
	store.PermissionDiagnosis
	Person, IssueKey string
	// Grants names the held grants the way the scheme pages do.
	Grants []string
}

type permissionHelperData struct {
	Members     []*models.User
	Permissions []store.PermissionDefinition
	AccountID   string
	IssueKey    string
	Permission  string
	Error       string
	Result      *permissionHelperResult
}

// PermissionHelperPage is Jira's permission helper: an administrator picks a
// person, a work item and a project permission and learns whether the person
// holds it and which grants give it to them.
func (h *Handler) PermissionHelperPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	ctx, query := r.Context(), r.URL.Query()
	members, err := h.Store.MembersByWorkspace(ctx, workspaceID)
	if err != nil {
		http.Error(w, "Could not load the permission helper.", http.StatusInternalServerError)
		return
	}
	data := permissionHelperData{Members: members, Permissions: store.ProjectPermissionDefinitions(), AccountID: query.Get("accountId"), IssueKey: strings.TrimSpace(query.Get("issueKey")), Permission: query.Get("permission")}
	if data.AccountID != "" || data.IssueKey != "" || data.Permission != "" {
		var person *models.User
		for _, member := range members {
			if member.ID == data.AccountID {
				person = member
			}
		}
		var issue *models.Issue
		if data.IssueKey != "" {
			if issue, err = h.Store.IssueByIDOrKey(ctx, workspaceID, data.IssueKey); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				http.Error(w, "Could not load the permission helper.", http.StatusInternalServerError)
				return
			}
		}
		switch {
		case person == nil:
			data.Error = "Choose a person to check."
		case issue == nil:
			data.Error = "No work item has the key " + data.IssueKey + "."
		default:
			diagnosis, diagnoseErr := h.Store.DiagnoseProjectPermission(ctx, workspaceID, person.ID, issue.ID, data.Permission)
			if errors.Is(diagnoseErr, store.ErrPermissionSchemeValidation) {
				data.Error = "Choose a permission."
				break
			}
			if diagnoseErr != nil {
				http.Error(w, "Could not load the permission helper.", http.StatusInternalServerError)
				return
			}
			result := &permissionHelperResult{PermissionDiagnosis: diagnosis, Person: person.DisplayName, IssueKey: issue.Key}
			if len(diagnosis.Grants) > 0 {
				groups, groupsErr := h.Store.GroupsByWorkspace(ctx, workspaceID)
				roles, rolesErr := h.Store.ProjectRoles(ctx, workspaceID)
				if err = errors.Join(groupsErr, rolesErr); err != nil {
					http.Error(w, "Could not load the permission helper.", http.StatusInternalServerError)
					return
				}
				for _, grant := range diagnosis.Grants {
					result.Grants = append(result.Grants, permissionGrantLabel(grant, members, groups, roles))
				}
			}
			data.Result = result
		}
	}
	h.writeWorkspacePage(w, r, "page_permission_helper", user, workspaceID, data, "admin", "")
}
