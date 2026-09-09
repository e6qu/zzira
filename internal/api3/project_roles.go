package api3

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type roleWriteRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

type roleActorsRequest struct {
	User    []string `json:"user"`
	Group   []string `json:"group"`
	GroupID []string `json:"groupId"`
}

type roleActorsSetRequest struct {
	CategorisedActors map[string][]string `json:"categorisedActors"`
}

func isProjectRolePath(path string) bool {
	if !strings.HasPrefix(path, "/project/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/project/"), "/")
	return len(parts) >= 2 && (parts[1] == "role" || parts[1] == "roledetails")
}

func projectRoleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrProjectPermission):
		jiraError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, store.ErrProjectRoleValidation):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrProjectRoleConflict):
		jiraError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrProjectRoleNotFound):
		jiraError(w, http.StatusNotFound, err.Error())
	default:
		jiraError(w, http.StatusInternalServerError, "Could not update project roles.")
	}
}

func parseRoleID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, store.ErrProjectRoleValidation
	}
	return id, nil
}

func queryBool(r *http.Request, key string, fallback bool) (bool, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}
	return strconv.ParseBool(raw)
}

func (h *Handler) roleActorBean(actor models.ProjectRoleActor) map[string]any {
	bean := map[string]any{
		"id": actor.ID, "displayName": actor.DisplayName,
		"avatarUrl": h.BaseURL + "/static/img/avatar-default.svg",
	}
	if actor.PrincipalType == "group" {
		bean["type"] = "atlassian-group-role-actor"
		bean["name"] = actor.DisplayName
		bean["actorGroup"] = map[string]any{"name": actor.DisplayName, "displayName": actor.DisplayName, "groupId": actor.PrincipalID}
	} else {
		bean["type"] = "atlassian-user-role-actor"
		bean["actorUser"] = map[string]any{"accountId": actor.PrincipalID}
	}
	return bean
}

func (h *Handler) roleBean(role *models.ProjectRole, actors []models.ProjectRoleActor, self string, current bool) map[string]any {
	actorBeans := make([]map[string]any, 0, len(actors))
	for _, actor := range actors {
		actorBeans = append(actorBeans, h.roleActorBean(actor))
	}
	return map[string]any{
		"self": self, "name": role.Name, "id": role.ID, "description": role.Description,
		"actors": actorBeans, "admin": role.Admin, "default": role.Default,
		"roleConfigurable": true, "translatedName": role.Name, "currentUserRole": current,
	}
}

func (h *Handler) roleDetailsBean(role *models.ProjectRole, self string) map[string]any {
	return map[string]any{
		"self": self, "name": role.Name, "id": role.ID, "description": role.Description,
		"admin": role.Admin, "default": role.Default, "roleConfigurable": true,
		"translatedName": role.Name, "type": "DEFAULT",
	}
}

func roleInput(request roleActorsRequest) store.ProjectRoleActorInput {
	return store.ProjectRoleActorInput{Users: request.User, GroupIDs: request.GroupID, GroupNames: request.Group}
}

func roleSetInput(request roleActorsSetRequest) (store.ProjectRoleActorInput, error) {
	input := store.ProjectRoleActorInput{}
	for key, values := range request.CategorisedActors {
		switch key {
		case "atlassian-user-role-actor":
			input.Users = append(input.Users, values...)
		case "atlassian-group-role-actor-id":
			input.GroupIDs = append(input.GroupIDs, values...)
		case "atlassian-group-role-actor":
			input.GroupNames = append(input.GroupNames, values...)
		default:
			return input, store.ErrProjectRoleValidation
		}
	}
	return input, nil
}

func roleDeleteInput(values url.Values) store.ProjectRoleActorInput {
	return store.ProjectRoleActorInput{Users: values["user"], GroupIDs: values["groupId"], GroupNames: values["group"]}
}

func (h *Handler) globalProjectRoleRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/role"), "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		switch r.Method {
		case http.MethodGet:
			roles, err := h.Store.ProjectRoles(r.Context(), workspaceID)
			if err != nil {
				projectRoleError(w, err)
				return
			}
			values := make([]map[string]any, 0, len(roles))
			for _, role := range roles {
				actors, actorErr := h.Store.DefaultProjectRoleActors(r.Context(), workspaceID, role.ID)
				if actorErr != nil {
					projectRoleError(w, actorErr)
					return
				}
				values = append(values, h.roleBean(role, actors, h.BaseURL+"/rest/api/3/role/"+strconv.FormatInt(role.ID, 10), false))
			}
			writeJSON(w, http.StatusOK, values)
		case http.MethodPost:
			var request roleWriteRequest
			if !decodeProjectRequest(w, r, &request) {
				return
			}
			if request.Name == nil {
				jiraFieldError(w, http.StatusBadRequest, map[string]string{"name": "A role name is required."})
				return
			}
			description := ""
			if request.Description != nil {
				description = *request.Description
			}
			role, err := h.Store.CreateProjectRole(r.Context(), workspaceID, actorID, *request.Name, description)
			if err != nil {
				projectRoleError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, h.roleBean(role, []models.ProjectRoleActor{}, h.BaseURL+"/rest/api/3/role/"+strconv.FormatInt(role.ID, 10), false))
		default:
			jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}
	if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	roleID, err := parseRoleID(parts[0])
	if err != nil {
		projectRoleError(w, err)
		return
	}
	if len(parts) == 2 {
		if parts[1] != "actors" {
			jiraError(w, http.StatusNotFound, "No resource found")
			return
		}
		h.defaultProjectRoleActors(w, r, workspaceID, actorID, roleID)
		return
	}
	switch r.Method {
	case http.MethodGet:
		role, roleErr := h.Store.ProjectRole(r.Context(), workspaceID, roleID)
		if roleErr != nil {
			projectRoleError(w, roleErr)
			return
		}
		actors, actorErr := h.Store.DefaultProjectRoleActors(r.Context(), workspaceID, roleID)
		if actorErr != nil {
			projectRoleError(w, actorErr)
			return
		}
		writeJSON(w, http.StatusOK, h.roleBean(role, actors, h.BaseURL+"/rest/api/3/role/"+strconv.FormatInt(role.ID, 10), false))
	case http.MethodPost, http.MethodPut:
		var request roleWriteRequest
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		// Jira's partial update gives name precedence when both fields are sent.
		if r.Method == http.MethodPost && request.Name != nil {
			request.Description = nil
		}
		role, updateErr := h.Store.UpdateProjectRole(r.Context(), workspaceID, actorID, roleID, request.Name, request.Description, r.Method == http.MethodPut)
		if updateErr != nil {
			projectRoleError(w, updateErr)
			return
		}
		actors, actorErr := h.Store.DefaultProjectRoleActors(r.Context(), workspaceID, roleID)
		if actorErr != nil {
			projectRoleError(w, actorErr)
			return
		}
		writeJSON(w, http.StatusOK, h.roleBean(role, actors, h.BaseURL+"/rest/api/3/role/"+strconv.FormatInt(role.ID, 10), false))
	case http.MethodDelete:
		var swap *int64
		if raw := r.URL.Query().Get("swap"); raw != "" {
			value, parseErr := parseRoleID(raw)
			if parseErr != nil {
				projectRoleError(w, parseErr)
				return
			}
			swap = &value
		}
		if deleteErr := h.Store.DeleteProjectRole(r.Context(), workspaceID, actorID, roleID, swap); deleteErr != nil {
			projectRoleError(w, deleteErr)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) defaultProjectRoleActors(w http.ResponseWriter, r *http.Request, workspaceID, actorID string, roleID int64) {
	var actors []models.ProjectRoleActor
	var err error
	switch r.Method {
	case http.MethodGet:
		actors, err = h.Store.DefaultProjectRoleActors(r.Context(), workspaceID, roleID)
	case http.MethodPost:
		var request roleActorsRequest
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		actors, err = h.Store.AddDefaultProjectRoleActors(r.Context(), workspaceID, actorID, roleID, roleInput(request))
	case http.MethodDelete:
		actors, err = h.Store.DeleteDefaultProjectRoleActors(r.Context(), workspaceID, actorID, roleID, roleDeleteInput(r.URL.Query()))
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if err != nil {
		projectRoleError(w, err)
		return
	}
	role, err := h.Store.ProjectRole(r.Context(), workspaceID, roleID)
	if err != nil {
		projectRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.roleBean(role, actors, h.BaseURL+"/rest/api/3/role/"+strconv.FormatInt(role.ID, 10), false))
}

func (h *Handler) projectRoleRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/project/"), "/")
	if len(parts) < 2 || len(parts) > 3 {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, parts[0])
	if err != nil {
		jiraError(w, http.StatusNotFound, "Project does not exist or you do not have permission to administer it.")
		return
	}
	allowed, err := h.Store.CanAdministerProject(r.Context(), workspaceID, actorID, project.ID)
	if err != nil {
		projectRoleError(w, err)
		return
	}
	if !allowed {
		jiraError(w, http.StatusNotFound, "Project does not exist or you do not have permission to administer it.")
		return
	}
	roles, err := h.Store.ProjectRoles(r.Context(), workspaceID)
	if err != nil {
		projectRoleError(w, err)
		return
	}
	if parts[1] == "roledetails" {
		if len(parts) != 2 || r.Method != http.MethodGet {
			jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		currentOnly, parseErr := queryBool(r, "currentMember", false)
		if parseErr != nil {
			jiraError(w, http.StatusBadRequest, "currentMember must be true or false.")
			return
		}
		for _, parameter := range []string{"excludeConnectAddons", "excludeOtherServiceRoles"} {
			if _, parseErr = queryBool(r, parameter, false); parseErr != nil {
				jiraError(w, http.StatusBadRequest, parameter+" must be true or false.")
				return
			}
		}
		values := []map[string]any{}
		for _, role := range roles {
			current, memberErr := h.Store.UserInProjectRole(r.Context(), workspaceID, project.ID, actorID, role.ID)
			if memberErr != nil {
				projectRoleError(w, memberErr)
				return
			}
			if !currentOnly || current {
				values = append(values, h.roleDetailsBean(role, h.BaseURL+"/rest/api/3/project/"+url.PathEscape(project.Key)+"/role/"+strconv.FormatInt(role.ID, 10)))
			}
		}
		writeJSON(w, http.StatusOK, values)
		return
	}
	if parts[1] != "role" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	if len(parts) == 2 {
		if r.Method != http.MethodGet {
			jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		values := map[string]string{}
		for _, role := range roles {
			values[role.Name] = h.BaseURL + "/rest/api/3/project/" + url.PathEscape(project.Key) + "/role/" + strconv.FormatInt(role.ID, 10)
		}
		writeJSON(w, http.StatusOK, values)
		return
	}
	roleID, parseErr := parseRoleID(parts[2])
	if parseErr != nil {
		projectRoleError(w, parseErr)
		return
	}
	role, err := h.Store.ProjectRole(r.Context(), workspaceID, roleID)
	if err != nil {
		projectRoleError(w, err)
		return
	}
	excludeInactive, parseErr := queryBool(r, "excludeInactiveUsers", false)
	if parseErr != nil {
		jiraError(w, http.StatusBadRequest, "excludeInactiveUsers must be true or false.")
		return
	}
	var actors []models.ProjectRoleActor
	switch r.Method {
	case http.MethodGet:
		actors, err = h.Store.ProjectRoleActors(r.Context(), workspaceID, project.ID, roleID, excludeInactive)
	case http.MethodPost:
		var request roleActorsRequest
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		actors, err = h.Store.AddProjectRoleActors(r.Context(), workspaceID, actorID, project.ID, roleID, roleInput(request))
	case http.MethodPut:
		var request roleActorsSetRequest
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		input, inputErr := roleSetInput(request)
		if inputErr != nil {
			projectRoleError(w, inputErr)
			return
		}
		actors, err = h.Store.SetProjectRoleActors(r.Context(), workspaceID, actorID, project.ID, roleID, input)
	case http.MethodDelete:
		actors, err = h.Store.DeleteProjectRoleActors(r.Context(), workspaceID, actorID, project.ID, roleID, roleDeleteInput(r.URL.Query()))
		if err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if err != nil {
		projectRoleError(w, err)
		return
	}
	current, err := h.Store.UserInProjectRole(r.Context(), workspaceID, project.ID, actorID, roleID)
	if err != nil {
		projectRoleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.roleBean(role, actors, h.BaseURL+"/rest/api/3/project/"+url.PathEscape(project.Key)+"/role/"+strconv.FormatInt(role.ID, 10), current))
}
