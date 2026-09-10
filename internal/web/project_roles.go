package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type projectRoleCard struct {
	Role   *models.ProjectRole
	Actors []models.ProjectRoleActor
}

type projectRolesAdminData struct {
	Roles   []projectRoleCard
	Members []*models.User
	Groups  []*models.Group
	Notice  string
	Error   string
}

type projectRoleAssignmentsData struct {
	Project *models.Project
	Roles   []projectRoleCard
	Members []*models.User
	Groups  []*models.Group
	Notice  string
	Error   string
}

func roleActorInputFromForm(r *http.Request) store.ProjectRoleActorInput {
	input := store.ProjectRoleActorInput{}
	switch r.PostFormValue("principalType") {
	case "user":
		input.Users = []string{r.PostFormValue("principalId")}
	case "group":
		input.GroupIDs = []string{r.PostFormValue("principalId")}
	}
	return input
}

func roleMutationMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrProjectRoleValidation), errors.Is(err, store.ErrProjectRoleConflict), errors.Is(err, store.ErrProjectRoleNotFound), errors.Is(err, store.ErrProjectPermission):
		return err.Error()
	default:
		return "Could not update project roles."
	}
}

func (h *Handler) ProjectRolesAdminPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		_, err := h.Store.CreateProjectRole(r.Context(), workspaceID, user.ID, r.PostFormValue("name"), r.PostFormValue("description"))
		target := "/settings/project-roles"
		if err != nil {
			redirectLocal(w, r, target+"?error="+url.QueryEscape(roleMutationMessage(err)))
			return
		}
		redirectLocal(w, r, target+"?notice="+url.QueryEscape("Project role created."))
		return
	}
	roles, err := h.Store.ProjectRoles(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load project roles.", http.StatusInternalServerError)
		return
	}
	data := projectRolesAdminData{Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	for _, role := range roles {
		actors, actorErr := h.Store.DefaultProjectRoleActors(r.Context(), workspaceID, role.ID)
		if actorErr != nil {
			http.Error(w, "Could not load default role actors.", http.StatusInternalServerError)
			return
		}
		data.Roles = append(data.Roles, projectRoleCard{Role: role, Actors: actors})
	}
	data.Members, err = h.Store.MembersByWorkspace(r.Context(), workspaceID)
	if err == nil {
		data.Groups, err = h.Store.GroupsByWorkspace(r.Context(), workspaceID)
	}
	if err != nil {
		http.Error(w, "Could not load role principals.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_project_roles_admin", user, workspaceID, data, "project-roles-admin", "")
}

func (h *Handler) ProjectRoleAdminMutation(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	roleID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || roleID <= 0 {
		http.NotFound(w, r)
		return
	}
	action := r.PostFormValue("action")
	notice := "Project role updated."
	switch action {
	case "update":
		name, description := r.PostFormValue("name"), r.PostFormValue("description")
		_, err = h.Store.UpdateProjectRole(r.Context(), workspaceID, user.ID, roleID, &name, &description, true)
	case "delete":
		var swap *int64
		if raw := strings.TrimSpace(r.PostFormValue("swap")); raw != "" {
			value, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil {
				err = store.ErrProjectRoleValidation
			} else {
				swap = &value
			}
		}
		if err == nil {
			err = h.Store.DeleteProjectRole(r.Context(), workspaceID, user.ID, roleID, swap)
		}
		notice = "Project role deleted."
	case "add-default":
		_, err = h.Store.AddDefaultProjectRoleActors(r.Context(), workspaceID, user.ID, roleID, roleActorInputFromForm(r))
		notice = "Default actor added."
	case "remove-default":
		_, err = h.Store.DeleteDefaultProjectRoleActors(r.Context(), workspaceID, user.ID, roleID, roleActorInputFromForm(r))
		notice = "Default actor removed."
	default:
		err = store.ErrProjectRoleValidation
	}
	target := "/settings/project-roles"
	if err != nil {
		redirectLocal(w, r, target+"?error="+url.QueryEscape(roleMutationMessage(err)))
		return
	}
	redirectLocal(w, r, target+"?notice="+url.QueryEscape(notice))
}

func (h *Handler) requireProjectAdminPage(w http.ResponseWriter, r *http.Request) (*models.User, string, *models.Project, bool) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return nil, "", nil, false
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return nil, "", nil, false
	}
	allowed, err := h.Store.CanAdministerProject(r.Context(), workspaceID, user.ID, project.ID)
	if err != nil || !allowed {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil, "", nil, false
	}
	return user, workspaceID, project, true
}

func (h *Handler) ProjectRoleAssignmentsPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, project, ok := h.requireProjectAdminPage(w, r)
	if !ok {
		return
	}
	roles, err := h.Store.ProjectRoles(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load project roles.", http.StatusInternalServerError)
		return
	}
	data := projectRoleAssignmentsData{Project: project, Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	for _, role := range roles {
		actors, actorErr := h.Store.ProjectRoleActors(r.Context(), workspaceID, project.ID, role.ID, false)
		if actorErr != nil {
			http.Error(w, "Could not load role assignments.", http.StatusInternalServerError)
			return
		}
		data.Roles = append(data.Roles, projectRoleCard{Role: role, Actors: actors})
	}
	data.Members, err = h.Store.MembersByWorkspace(r.Context(), workspaceID)
	if err == nil {
		data.Groups, err = h.Store.GroupsByWorkspace(r.Context(), workspaceID)
	}
	if err != nil {
		http.Error(w, "Could not load role principals.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_project_role_assignments", user, workspaceID, data, "project-roles", project.Key)
}

func (h *Handler) ProjectRoleAssignmentMutation(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, project, ok := h.requireProjectAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	roleID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || roleID <= 0 {
		http.NotFound(w, r)
		return
	}
	input := roleActorInputFromForm(r)
	message := "Role actor added."
	switch r.PostFormValue("action") {
	case "add":
		_, err = h.Store.AddProjectRoleActors(r.Context(), workspaceID, user.ID, project.ID, roleID, input)
	case "remove":
		_, err = h.Store.DeleteProjectRoleActors(r.Context(), workspaceID, user.ID, project.ID, roleID, input)
		message = "Role actor removed."
	default:
		err = store.ErrProjectRoleValidation
	}
	target := "/projects/" + url.PathEscape(project.Key) + "/settings/roles"
	if err != nil {
		redirectLocal(w, r, target+"?error="+url.QueryEscape(roleMutationMessage(err)))
		return
	}
	redirectLocal(w, r, target+"?notice="+url.QueryEscape(message))
}
