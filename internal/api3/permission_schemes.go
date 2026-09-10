package api3

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type permissionHolderRequest struct {
	Type      string `json:"type"`
	Parameter string `json:"parameter"`
	Value     string `json:"value"`
}

type permissionGrantRequest struct {
	Permission string                  `json:"permission"`
	Holder     permissionHolderRequest `json:"holder"`
}

type permissionSchemeRequest struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	Permissions *[]permissionGrantRequest `json:"permissions"`
}

func isPermissionSchemePath(path string) bool {
	return path == "/permissionscheme" || strings.HasPrefix(path, "/permissionscheme/") ||
		(strings.HasPrefix(path, "/project/") && strings.HasSuffix(path, "/permissionscheme"))
}

func permissionSchemeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrProjectPermission):
		jiraError(w, http.StatusForbidden, err.Error())
	case errors.Is(err, store.ErrPermissionSchemeValidation), errors.Is(err, store.ErrPermissionSchemeConflict):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrPermissionSchemeNotFound):
		jiraError(w, http.StatusNotFound, "The permission scheme, grant, or project does not exist.")
	default:
		jiraError(w, http.StatusInternalServerError, "Could not complete the permission scheme operation.")
	}
}

func parsePermissionSchemeID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, store.ErrPermissionSchemeValidation
	}
	return id, nil
}

func permissionGrantInput(request permissionGrantRequest) store.PermissionGrantInput {
	return store.PermissionGrantInput{
		Permission: request.Permission, HolderType: request.Holder.Type,
		HolderParameter: request.Holder.Parameter, HolderValue: request.Holder.Value,
	}
}

func permissionGrantInputs(requests []permissionGrantRequest) []store.PermissionGrantInput {
	inputs := make([]store.PermissionGrantInput, 0, len(requests))
	for _, request := range requests {
		inputs = append(inputs, permissionGrantInput(request))
	}
	return inputs
}

func permissionExpand(r *http.Request) (bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("expand"))
	if raw == "" {
		return false, nil
	}
	expanded := false
	for _, value := range strings.Split(raw, ",") {
		switch strings.TrimSpace(value) {
		case "permissions", "user", "group", "projectRole", "field", "all":
			expanded = true
		case "":
		default:
			return false, store.ErrPermissionSchemeValidation
		}
	}
	return expanded, nil
}

func (h *Handler) permissionGrantBean(schemeID int64, grant models.PermissionGrant) map[string]any {
	holder := map[string]any{"type": grant.HolderType}
	if grant.HolderParameter != "" {
		holder["parameter"] = grant.HolderParameter
	}
	if grant.HolderValue != "" {
		holder["value"] = grant.HolderValue
	}
	return map[string]any{
		"id": grant.ID, "self": h.BaseURL + "/rest/api/3/permissionscheme/" + strconv.FormatInt(schemeID, 10) + "/permission/" + strconv.FormatInt(grant.ID, 10),
		"permission": grant.Permission, "holder": holder,
	}
}

func (h *Handler) permissionSchemeBean(scheme *models.PermissionScheme, expanded bool) map[string]any {
	bean := map[string]any{
		"id": scheme.ID, "self": h.BaseURL + "/rest/api/3/permissionscheme/" + strconv.FormatInt(scheme.ID, 10),
		"name": scheme.Name, "description": scheme.Description,
		"scope": map[string]any{"type": "PROJECT"},
	}
	if expanded {
		permissions := make([]map[string]any, 0, len(scheme.Grants))
		for _, grant := range scheme.Grants {
			permissions = append(permissions, h.permissionGrantBean(scheme.ID, grant))
		}
		bean["permissions"] = permissions
		bean["expand"] = "permissions"
	}
	return bean
}

func (h *Handler) permissionSchemeRoute(w http.ResponseWriter, r *http.Request, path string) {
	if strings.HasPrefix(path, "/project/") {
		h.projectPermissionSchemeRoute(w, r, path)
		return
	}
	expanded, err := permissionExpand(r)
	if err != nil {
		permissionSchemeError(w, err)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/permissionscheme"), "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		if r.Method == http.MethodGet {
			workspaceID, _, authErr := h.authWorkspace(r)
			if authErr != nil {
				writeJerr(w, authErr)
				return
			}
			schemes, listErr := h.Store.PermissionSchemes(r.Context(), workspaceID, expanded)
			if listErr != nil {
				permissionSchemeError(w, listErr)
				return
			}
			values := make([]map[string]any, 0, len(schemes))
			for _, scheme := range schemes {
				values = append(values, h.permissionSchemeBean(scheme, expanded))
			}
			writeJSON(w, http.StatusOK, map[string]any{"permissionSchemes": values})
			return
		}
		if r.Method == http.MethodPost {
			workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
			if authErr != nil {
				writeJerr(w, authErr)
				return
			}
			var request permissionSchemeRequest
			if !decodeProjectRequest(w, r, &request) {
				return
			}
			var grants []store.PermissionGrantInput
			if request.Permissions != nil {
				grants = permissionGrantInputs(*request.Permissions)
			}
			scheme, createErr := h.Store.CreatePermissionScheme(r.Context(), workspaceID, actorID, request.Name, request.Description, grants)
			if createErr != nil {
				permissionSchemeError(w, createErr)
				return
			}
			writeJSON(w, http.StatusCreated, h.permissionSchemeBean(scheme, expanded))
			return
		}
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if len(parts) < 1 || len(parts) > 3 || parts[0] == "" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	schemeID, err := parsePermissionSchemeID(parts[0])
	if err != nil {
		permissionSchemeError(w, err)
		return
	}
	if len(parts) >= 2 {
		if parts[1] != "permission" {
			jiraError(w, http.StatusNotFound, "No resource found")
			return
		}
		h.permissionGrantRoute(w, r, schemeID, parts[2:], expanded)
		return
	}
	switch r.Method {
	case http.MethodGet:
		workspaceID, _, authErr := h.authWorkspace(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		scheme, getErr := h.Store.PermissionScheme(r.Context(), workspaceID, schemeID, expanded)
		if getErr != nil {
			permissionSchemeError(w, getErr)
			return
		}
		writeJSON(w, http.StatusOK, h.permissionSchemeBean(scheme, expanded))
	case http.MethodPut:
		workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		var request permissionSchemeRequest
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		var grants *[]store.PermissionGrantInput
		if request.Permissions != nil {
			values := permissionGrantInputs(*request.Permissions)
			grants = &values
		}
		scheme, updateErr := h.Store.UpdatePermissionScheme(r.Context(), workspaceID, actorID, schemeID, request.Name, request.Description, grants)
		if updateErr != nil {
			permissionSchemeError(w, updateErr)
			return
		}
		writeJSON(w, http.StatusOK, h.permissionSchemeBean(scheme, expanded))
	case http.MethodDelete:
		workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		if deleteErr := h.Store.DeletePermissionScheme(r.Context(), workspaceID, actorID, schemeID); deleteErr != nil {
			permissionSchemeError(w, deleteErr)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) permissionGrantRoute(w http.ResponseWriter, r *http.Request, schemeID int64, rest []string, expanded bool) {
	if len(rest) == 0 {
		workspaceID, actorID, authErr := h.authWorkspace(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		switch r.Method {
		case http.MethodGet:
			grants, err := h.Store.PermissionSchemeGrants(r.Context(), workspaceID, schemeID)
			if err != nil {
				permissionSchemeError(w, err)
				return
			}
			values := make([]map[string]any, 0, len(grants))
			for _, grant := range grants {
				values = append(values, h.permissionGrantBean(schemeID, grant))
			}
			writeJSON(w, http.StatusOK, map[string]any{"permissions": values, "expand": "permissions"})
		case http.MethodPost:
			if _, _, adminErr := h.authWorkspaceAdmin(r); adminErr != nil {
				writeJerr(w, adminErr)
				return
			}
			var request permissionGrantRequest
			if !decodeProjectRequest(w, r, &request) {
				return
			}
			grant, err := h.Store.CreatePermissionGrant(r.Context(), workspaceID, actorID, schemeID, permissionGrantInput(request))
			if err != nil {
				permissionSchemeError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, h.permissionGrantBean(schemeID, grant))
		default:
			jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}
	if len(rest) != 1 {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	grantID, err := parsePermissionSchemeID(rest[0])
	if err != nil {
		permissionSchemeError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		workspaceID, _, authErr := h.authWorkspace(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		grant, getErr := h.Store.PermissionSchemeGrant(r.Context(), workspaceID, schemeID, grantID)
		if getErr != nil {
			permissionSchemeError(w, getErr)
			return
		}
		writeJSON(w, http.StatusOK, h.permissionGrantBean(schemeID, grant))
		return
	}
	if r.Method == http.MethodDelete {
		workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		if deleteErr := h.Store.DeletePermissionGrant(r.Context(), workspaceID, actorID, schemeID, grantID); deleteErr != nil {
			permissionSchemeError(w, deleteErr)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_ = expanded
	jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
}

func (h *Handler) projectPermissionSchemeRoute(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(strings.TrimPrefix(path, "/project/"), "/")
	if len(parts) != 2 || parts[1] != "permissionscheme" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	expanded, err := permissionExpand(r)
	if err != nil {
		permissionSchemeError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		workspaceID, actorID, authErr := h.authWorkspace(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		scheme, project, getErr := h.Store.AssignedPermissionScheme(r.Context(), workspaceID, parts[0], expanded)
		if getErr != nil {
			permissionSchemeError(w, getErr)
			return
		}
		allowed, permissionErr := h.Store.HasProjectPermission(r.Context(), workspaceID, actorID, project.ID, "", "ADMINISTER_PROJECTS")
		if permissionErr != nil {
			permissionSchemeError(w, permissionErr)
			return
		}
		if !allowed {
			jiraError(w, http.StatusForbidden, "Administer Jira or Administer projects permission is required.")
			return
		}
		writeJSON(w, http.StatusOK, h.permissionSchemeBean(scheme, expanded))
		return
	}
	if r.Method == http.MethodPut {
		workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		var request struct {
			ID int64 `json:"id"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		if request.ID <= 0 {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"id": "A permission scheme id is required."})
			return
		}
		scheme, _, assignErr := h.Store.AssignPermissionScheme(r.Context(), workspaceID, actorID, parts[0], request.ID)
		if assignErr != nil {
			permissionSchemeError(w, assignErr)
			return
		}
		writeJSON(w, http.StatusOK, h.permissionSchemeBean(scheme, expanded))
		return
	}
	jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
}

func (h *Handler) allPermissions(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	permissions := map[string]any{}
	for _, definition := range store.PermissionDefinitions() {
		have := false
		var err error
		if definition.Type == "GLOBAL" {
			have, err = h.Store.HasGlobalPermission(r.Context(), workspaceID, userID, definition.Key)
		} else {
			projects, projectErr := h.Store.ProjectsWithPermissions(r.Context(), workspaceID, userID, []string{definition.Key})
			err, have = projectErr, len(projects) > 0
		}
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not evaluate permissions.")
			return
		}
		permissions[definition.Key] = map[string]any{
			"id": definition.Key, "key": definition.Key, "name": definition.Name,
			"description": definition.Description, "type": definition.Type, "havePermission": have,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": permissions})
}

type bulkPermissionsRequest struct {
	AccountID          string   `json:"accountId"`
	GlobalPermissions  []string `json:"globalPermissions"`
	ProjectPermissions []struct {
		Permissions []string `json:"permissions"`
		Projects    []int64  `json:"projects"`
		Issues      []int64  `json:"issues"`
	} `json:"projectPermissions"`
}

func (h *Handler) bulkPermissions(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request bulkPermissionsRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		jiraError(w, http.StatusBadRequest, "The permissions request is invalid.")
		return
	}
	userID := request.AccountID
	if userID == "" {
		userID = actorID
	} else if userID != actorID {
		admin, err := h.Store.HasGlobalPermission(r.Context(), workspaceID, actorID, "ADMINISTER")
		if err != nil || !admin {
			jiraError(w, http.StatusForbidden, "Administer Jira permission is required to inspect another user.")
			return
		}
	}
	if len(request.ProjectPermissions) > 1000 {
		jiraError(w, http.StatusBadRequest, "No more than 1000 project permission entries can be checked.")
		return
	}
	global := []string{}
	for _, permission := range request.GlobalPermissions {
		if allowed, _ := h.Store.HasGlobalPermission(r.Context(), workspaceID, userID, permission); allowed {
			global = append(global, permission)
		}
	}
	projectGrants := []map[string]any{}
	for _, entry := range request.ProjectPermissions {
		if len(entry.Projects) > 1000 || len(entry.Issues) > 1000 {
			jiraError(w, http.StatusBadRequest, "No more than 1000 projects and 1000 issues can be checked.")
			return
		}
		for _, permission := range entry.Permissions {
			if permission == "" {
				continue
			}
			projects, issues := []int64{}, []int64{}
			for _, projectID := range entry.Projects {
				if allowed, _ := h.Store.HasProjectPermission(r.Context(), workspaceID, userID, strconv.FormatInt(projectID, 10), "", permission); allowed {
					projects = append(projects, projectID)
				}
			}
			for _, issueID := range entry.Issues {
				issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, strconv.FormatInt(issueID, 10))
				if err == nil {
					if allowed, _ := h.Store.HasProjectPermission(r.Context(), workspaceID, userID, issue.ProjectID, strconv.FormatInt(issueID, 10), permission); allowed {
						issues = append(issues, issueID)
					}
				}
			}
			projectGrants = append(projectGrants, map[string]any{"permission": permission, "projects": projects, "issues": issues})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"globalPermissions": global, "projectPermissions": projectGrants})
}

func (h *Handler) permittedProjects(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		Permissions []string `json:"permissions"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	if len(request.Permissions) == 0 {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"permissions": "At least one project permission is required."})
		return
	}
	projects, err := h.Store.ProjectsWithPermissions(r.Context(), workspaceID, userID, request.Permissions)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not evaluate project permissions.")
		return
	}
	values := make([]map[string]any, 0, len(projects))
	for _, project := range projects {
		values = append(values, map[string]any{"id": project.ID, "key": project.Key})
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": values})
}

func requestedPermissionKeys(raw string) []string {
	seen := map[string]bool{}
	keys := []string{}
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			keys = append(keys, value)
		}
	}
	return keys
}

func (h *Handler) permissionContext(r *http.Request, workspaceID string) (projectID, issueID string, failure *jerr) {
	values := r.URL.Query()
	projectKey, projectIDQuery := values.Get("projectKey"), values.Get("projectId")
	issueKey, issueIDQuery := values.Get("issueKey"), values.Get("issueId")
	if projectKey != "" && projectIDQuery != "" {
		return "", "", &jerr{status: http.StatusBadRequest, message: "projectKey and projectId cannot be used together."}
	}
	if issueKey != "" && issueIDQuery != "" {
		return "", "", &jerr{status: http.StatusBadRequest, message: "issueKey and issueId cannot be used together."}
	}
	issueID = issueKey
	if issueID == "" {
		issueID = issueIDQuery
	}
	if issueID != "" {
		issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, issueID)
		if err != nil {
			return "", "", &jerr{status: http.StatusNotFound, message: "The issue does not exist."}
		}
		if projectKey != "" || projectIDQuery != "" {
			requestedProject := projectKey
			if requestedProject == "" {
				requestedProject = projectIDQuery
			}
			project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, requestedProject)
			if err != nil || project.ID != issue.ProjectID {
				return "", "", &jerr{status: http.StatusBadRequest, message: "The project and issue contexts do not match."}
			}
		}
		return issue.ProjectID, issueID, nil
	}
	projectID = projectKey
	if projectID == "" {
		projectID = projectIDQuery
	}
	if commentID := values.Get("commentId"); commentID != "" {
		keys := requestedPermissionKeys(values.Get("permissions"))
		if len(keys) != 1 || keys[0] != "BROWSE_PROJECTS" {
			return "", "", &jerr{status: http.StatusBadRequest, message: "commentId only supports the BROWSE_PROJECTS permission."}
		}
		comment, err := h.Store.CommentByID(r.Context(), workspaceID, commentID)
		if err != nil {
			return "", "", &jerr{status: http.StatusNotFound, message: "The comment does not exist."}
		}
		issue, err := h.Store.IssueByIDOrKey(r.Context(), workspaceID, comment.IssueID)
		if err != nil {
			return "", "", &jerr{status: http.StatusNotFound, message: "The comment does not exist."}
		}
		return issue.ProjectID, issue.ID, nil
	}
	if projectID != "" {
		project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectID)
		if err != nil {
			return "", "", &jerr{status: http.StatusNotFound, message: "The project does not exist."}
		}
		return project.ID, "", nil
	}
	return "", "", nil
}

func (h *Handler) myPermissions(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	projectID, issueID, contextErr := h.permissionContext(r, workspaceID)
	if contextErr != nil {
		writeJerr(w, contextErr)
		return
	}
	keys := requestedPermissionKeys(r.URL.Query().Get("permissions"))
	if len(keys) == 0 {
		for _, definition := range store.PermissionDefinitions() {
			keys = append(keys, definition.Key)
		}
	}
	permissions := map[string]any{}
	for _, key := range keys {
		definition, known := store.PermissionDefinitionByKey(key)
		if !known {
			continue
		}
		have := false
		var err error
		if definition.Type == "GLOBAL" {
			have, err = h.Store.HasGlobalPermission(r.Context(), workspaceID, userID, key)
		} else if projectID != "" {
			have, err = h.Store.HasProjectPermission(r.Context(), workspaceID, userID, projectID, issueID, key)
		} else {
			var projects []*models.Project
			projects, err = h.Store.ProjectsWithPermissions(r.Context(), workspaceID, userID, []string{key})
			have = len(projects) > 0
		}
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not evaluate permissions.")
			return
		}
		permissions[key] = map[string]any{
			"id": key, "key": key, "name": definition.Name, "description": definition.Description,
			"type": definition.Type, "havePermission": have,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"permissions": permissions})
}

func (h *Handler) usersWithPermissions(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	permissions := requestedPermissionKeys(r.URL.Query().Get("permissions"))
	if len(permissions) == 0 {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"permissions": "At least one permission is required."})
		return
	}
	projectID, issueID, contextErr := h.permissionContext(r, workspaceID)
	if contextErr != nil {
		writeJerr(w, contextErr)
		return
	}
	if projectID == "" {
		admin, err := h.Store.HasGlobalPermission(r.Context(), workspaceID, actorID, "ADMINISTER")
		if err != nil || !admin {
			jiraError(w, http.StatusForbidden, "A project context or Administer Jira permission is required.")
			return
		}
	} else {
		admin, err := h.Store.HasProjectPermission(r.Context(), workspaceID, actorID, projectID, issueID, "ADMINISTER_PROJECTS")
		if err != nil || !admin {
			jiraError(w, http.StatusForbidden, "Administer Jira or Administer projects permission is required.")
			return
		}
	}
	startAt, maxResults := 0, 50
	if raw := r.URL.Query().Get("startAt"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			jiraError(w, http.StatusBadRequest, "startAt must be a non-negative integer.")
			return
		}
		startAt = value
	}
	if raw := r.URL.Query().Get("maxResults"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 1000 {
			jiraError(w, http.StatusBadRequest, "maxResults must be between 1 and 1000.")
			return
		}
		maxResults = value
	}
	members, err := h.Store.MembersByWorkspace(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not search users.")
		return
	}
	if startAt > 1000 || startAt >= len(members) {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	end := min(len(members), min(1000, startAt+maxResults))
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("query")))
	accountID := r.URL.Query().Get("accountId")
	results := []map[string]any{}
	for _, member := range members[startAt:end] {
		if !member.Active || (accountID != "" && member.ID != accountID) ||
			(query != "" && !strings.Contains(strings.ToLower(member.DisplayName+" "+member.Email), query)) {
			continue
		}
		allowed := true
		for _, permission := range permissions {
			definition, known := store.PermissionDefinitionByKey(permission)
			if !known {
				allowed = false
				break
			}
			var granted bool
			if definition.Type == "GLOBAL" {
				granted, err = h.Store.HasGlobalPermission(r.Context(), workspaceID, member.ID, permission)
			} else if projectID != "" {
				granted, err = h.Store.HasProjectPermission(r.Context(), workspaceID, member.ID, projectID, issueID, permission)
			} else {
				var projects []*models.Project
				projects, err = h.Store.ProjectsWithPermissions(r.Context(), workspaceID, member.ID, []string{permission})
				granted = len(projects) > 0
			}
			if err != nil || !granted {
				allowed = false
				break
			}
		}
		if allowed {
			results = append(results, h.userBean(member))
		}
	}
	writeJSON(w, http.StatusOK, results)
}
