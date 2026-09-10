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

type securityMemberRequest struct {
	Type      string `json:"type"`
	Parameter string `json:"parameter"`
}

type securityLevelRequest struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Default     bool                    `json:"isDefault"`
	Members     []securityMemberRequest `json:"members"`
}

// UnmarshalJSON retains zzira's pre-v3 extension, where members were account ID
// strings, while accepting Jira Cloud's holder objects on the same endpoint.
func (request *securityLevelRequest) UnmarshalJSON(data []byte) error {
	var wire struct {
		ID          string            `json:"id"`
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Default     bool              `json:"isDefault"`
		Members     []json.RawMessage `json:"members"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	request.ID, request.Name, request.Description, request.Default = wire.ID, wire.Name, wire.Description, wire.Default
	request.Members = nil
	for _, raw := range wire.Members {
		var member securityMemberRequest
		if err := json.Unmarshal(raw, &member); err == nil && member.Type != "" {
			request.Members = append(request.Members, member)
			continue
		}
		var accountID string
		if err := json.Unmarshal(raw, &accountID); err != nil {
			return err
		}
		request.Members = append(request.Members, securityMemberRequest{Type: "user", Parameter: accountID})
	}
	return nil
}

func isIssueSecurityPath(path string) bool {
	return path == "/issuesecurityschemes" || strings.HasPrefix(path, "/issuesecurityschemes/") ||
		strings.HasPrefix(path, "/securitylevel/") ||
		(strings.HasPrefix(path, "/project/") && (strings.HasSuffix(path, "/issuesecuritylevelscheme") || strings.HasSuffix(path, "/securitylevel")))
}

func issueSecurityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrProjectPermission):
		jiraError(w, http.StatusForbidden, "Administrator privileges are required.")
	case errors.Is(err, store.ErrIssueSecurityTaskConflict):
		jiraError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrIssueSecurityValidation), errors.Is(err, store.ErrIssueSecurityConflict):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrIssueSecurityNotFound):
		jiraError(w, http.StatusNotFound, "The issue security scheme, level, member, or project does not exist.")
	default:
		jiraError(w, http.StatusInternalServerError, "Could not complete the issue security operation.")
	}
}

func securityInputs(requests []securityLevelRequest) []store.SecurityLevelInput {
	levels := make([]store.SecurityLevelInput, 0, len(requests))
	for _, request := range requests {
		level := store.SecurityLevelInput{Name: request.Name, Description: request.Description, Default: request.Default}
		for _, member := range request.Members {
			level.Members = append(level.Members, store.SecurityLevelMemberInput{Type: member.Type, Parameter: member.Parameter})
		}
		levels = append(levels, level)
	}
	return levels
}

func wireNumericID(value string) any {
	if number, err := strconv.ParseInt(value, 10, 64); err == nil {
		return number
	}
	return value
}

func (h *Handler) securityLevelBean(schemeID string, level models.SecurityLevel) map[string]any {
	return map[string]any{
		"id": level.ID, "name": level.Name, "description": level.Description,
		"isDefault": level.IsDefault, "issueSecuritySchemeId": schemeID,
		"self": h.BaseURL + "/rest/api/3/securitylevel/" + level.ID,
	}
}

func (h *Handler) securityMemberBean(member models.SecurityLevelMember) map[string]any {
	holder := map[string]any{"type": member.HolderType}
	if member.HolderParameter != "" {
		holder["parameter"] = member.HolderParameter
	}
	if member.HolderValue != "" {
		holder["value"] = member.HolderValue
	}
	return map[string]any{
		"id": strconv.FormatInt(member.ID, 10), "issueSecuritySchemeId": member.SchemeID,
		"issueSecurityLevelId": member.LevelID, "holder": holder, "managed": member.Managed,
	}
}

func (h *Handler) securitySchemeBean(scheme *models.SecurityScheme, includeLevels bool) map[string]any {
	bean := map[string]any{
		"id": wireNumericID(scheme.ID), "name": scheme.Name, "description": scheme.Description,
		"self": h.BaseURL + "/rest/api/3/issuesecurityschemes/" + scheme.ID,
	}
	if scheme.DefaultLevelID != "" {
		bean["defaultSecurityLevelId"] = wireNumericID(scheme.DefaultLevelID)
	}
	if includeLevels {
		levels := make([]map[string]any, 0, len(scheme.Levels))
		for _, level := range scheme.Levels {
			levels = append(levels, h.securityLevelBean(scheme.ID, level))
		}
		bean["levels"] = levels
	}
	return bean
}

func securityQueryValues(r *http.Request, name string) []string {
	result := []string{}
	for _, value := range r.URL.Query()[name] {
		for _, part := range strings.Split(value, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				result = append(result, trimmed)
			}
		}
	}
	return result
}

func securityPage(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	startAt, maxResults, err := notificationPage(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
		return 0, 0, false
	}
	return startAt, maxResults, true
}

func (h *Handler) securityPageBean(r *http.Request, values []map[string]any, total, startAt, maxResults int) map[string]any {
	bean := map[string]any{
		"self": h.BaseURL + r.URL.RequestURI(), "startAt": startAt, "maxResults": maxResults,
		"total": total, "isLast": startAt+len(values) >= total, "values": values,
	}
	if startAt+len(values) < total {
		nextURL := *r.URL
		query := nextURL.Query()
		query.Set("startAt", strconv.Itoa(startAt+len(values)))
		nextURL.RawQuery = query.Encode()
		bean["nextPage"] = h.BaseURL + nextURL.RequestURI()
	}
	return bean
}

func nullableSecurityID(raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 {
		return "", false, nil
	}
	if string(raw) == "null" {
		return "", true, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, store.ErrIssueSecurityValidation
	}
	return value, true, nil
}

func (h *Handler) securitySchemeRoute(w http.ResponseWriter, r *http.Request, path string) {
	if strings.HasPrefix(path, "/project/") {
		h.projectSecurityRoute(w, r, path)
		return
	}
	if strings.HasPrefix(path, "/securitylevel/") {
		h.securityLevelResource(w, r, strings.TrimPrefix(path, "/securitylevel/"))
		return
	}
	switch path {
	case "/issuesecurityschemes":
		h.securitySchemeCollection(w, r)
		return
	case "/issuesecurityschemes/level":
		h.securityLevelSearch(w, r)
		return
	case "/issuesecurityschemes/level/default":
		h.securityLevelDefaults(w, r)
		return
	case "/issuesecurityschemes/level/member":
		h.securityMemberSearch(w, r, "")
		return
	case "/issuesecurityschemes/project":
		h.securityProjectMappings(w, r)
		return
	case "/issuesecurityschemes/search":
		h.securitySchemeSearch(w, r)
		return
	}
	if strings.HasPrefix(path, "/issuesecurityschemes/project/") {
		h.legacyProjectSecurityRoute(w, r, strings.TrimPrefix(path, "/issuesecurityschemes/project/"))
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/issuesecurityschemes/"), "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	schemeID := parts[0]
	switch {
	case len(parts) == 1:
		h.securitySchemeResource(w, r, schemeID)
	case len(parts) == 2 && parts[1] == "members":
		h.securityMemberSearch(w, r, schemeID)
	case len(parts) == 2 && parts[1] == "level":
		h.securityLevelCollection(w, r, schemeID)
	case len(parts) == 3 && parts[1] == "level":
		h.securityLevelMutation(w, r, schemeID, parts[2])
	case len(parts) == 4 && parts[1] == "level" && parts[3] == "member":
		h.securityMemberCollection(w, r, schemeID, parts[2])
	case len(parts) == 5 && parts[1] == "level" && parts[3] == "member":
		h.securityMemberResource(w, r, schemeID, parts[2], parts[4])
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) securitySchemeCollection(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		schemes, err := h.Store.IssueSecuritySchemes(r.Context(), workspaceID, true)
		if err != nil {
			issueSecurityError(w, err)
			return
		}
		values := make([]map[string]any, 0, len(schemes))
		for _, scheme := range schemes {
			values = append(values, h.securitySchemeBean(scheme, true))
		}
		writeJSON(w, http.StatusOK, map[string]any{"issueSecuritySchemes": values})
	case http.MethodPost:
		var request struct {
			ID          string                 `json:"id"`
			Name        string                 `json:"name"`
			Description string                 `json:"description"`
			Levels      []securityLevelRequest `json:"levels"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		if request.ID != "" {
			legacy := models.SecurityScheme{ID: request.ID, WorkspaceID: workspaceID, Name: request.Name, Description: request.Description}
			for _, requestedLevel := range request.Levels {
				level := models.SecurityLevel{ID: requestedLevel.ID, Name: requestedLevel.Name, Description: requestedLevel.Description, IsDefault: requestedLevel.Default}
				if level.ID == "" {
					issueSecurityError(w, store.ErrIssueSecurityValidation)
					return
				}
				for _, member := range requestedLevel.Members {
					if member.Type != "user" || member.Parameter == "" {
						issueSecurityError(w, store.ErrIssueSecurityValidation)
						return
					}
					level.Members = append(level.Members, member.Parameter)
				}
				if level.IsDefault {
					legacy.DefaultLevelID = level.ID
				}
				legacy.Levels = append(legacy.Levels, level)
			}
			if err := h.Store.CreateSecurityScheme(r.Context(), legacy); err != nil {
				issueSecurityError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, map[string]any{"id": legacy.ID})
			return
		}
		scheme, err := h.Store.CreateIssueSecurityScheme(r.Context(), workspaceID, actorID, request.Name, request.Description, securityInputs(request.Levels))
		if err != nil {
			issueSecurityError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": scheme.ID})
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) securitySchemeResource(w http.ResponseWriter, r *http.Request, schemeID string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		scheme, err := h.Store.IssueSecurityScheme(r.Context(), workspaceID, schemeID, true)
		if err != nil {
			issueSecurityError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.securitySchemeBean(scheme, true))
	case http.MethodPut:
		var request struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		if err := h.Store.UpdateIssueSecurityScheme(r.Context(), workspaceID, actorID, schemeID, request.Name, request.Description); err != nil {
			issueSecurityError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := h.Store.DeleteIssueSecurityScheme(r.Context(), workspaceID, actorID, schemeID); err != nil {
			issueSecurityError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) securityLevelSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	startAt, maxResults, ok := securityPage(w, r)
	if !ok {
		return
	}
	onlyDefault := false
	if raw := r.URL.Query().Get("onlyDefault"); raw != "" {
		var err error
		onlyDefault, err = strconv.ParseBool(raw)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "onlyDefault must be a boolean.")
			return
		}
	}
	levels, err := h.Store.IssueSecurityLevels(r.Context(), workspaceID, store.SecurityLevelFilter{IDs: securityQueryValues(r, "id"), SchemeIDs: securityQueryValues(r, "schemeId"), OnlyDefault: onlyDefault})
	if err != nil {
		issueSecurityError(w, err)
		return
	}
	schemes, err := h.Store.IssueSecuritySchemes(r.Context(), workspaceID, false)
	if err != nil {
		issueSecurityError(w, err)
		return
	}
	owner := map[string]string{}
	for _, scheme := range schemes {
		for _, level := range scheme.Levels {
			owner[level.ID] = scheme.ID
		}
	}
	page := pageSlice(levels, startAt, maxResults)
	values := make([]map[string]any, 0, len(page))
	for _, level := range page {
		values = append(values, h.securityLevelBean(owner[level.ID], level))
	}
	writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(levels), startAt, maxResults))
}

func (h *Handler) securityLevelDefaults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		DefaultValues []struct {
			SchemeID string          `json:"issueSecuritySchemeId"`
			LevelID  json.RawMessage `json:"defaultLevelId"`
		} `json:"defaultValues"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	defaults := map[string]string{}
	for _, value := range request.DefaultValues {
		levelID, present, err := nullableSecurityID(value.LevelID)
		if value.SchemeID == "" || !present || err != nil {
			issueSecurityError(w, store.ErrIssueSecurityValidation)
			return
		}
		defaults[value.SchemeID] = levelID
	}
	if err := h.Store.SetIssueSecurityDefaults(r.Context(), workspaceID, actorID, defaults); err != nil {
		issueSecurityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) securityMemberSearch(w http.ResponseWriter, r *http.Request, schemeID string) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	startAt, maxResults, ok := securityPage(w, r)
	if !ok {
		return
	}
	schemeIDs, levelIDs := securityQueryValues(r, "schemeId"), securityQueryValues(r, "levelId")
	if schemeID != "" {
		schemeIDs, levelIDs = []string{schemeID}, securityQueryValues(r, "issueSecurityLevelId")
	}
	members, err := h.Store.IssueSecurityLevelMembers(r.Context(), workspaceID, store.SecurityMemberFilter{IDs: securityQueryValues(r, "id"), SchemeIDs: schemeIDs, LevelIDs: levelIDs})
	if err != nil {
		issueSecurityError(w, err)
		return
	}
	page := pageSlice(members, startAt, maxResults)
	values := make([]map[string]any, 0, len(page))
	for _, member := range page {
		values = append(values, h.securityMemberBean(member))
	}
	writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(members), startAt, maxResults))
}

func (h *Handler) securityProjectMappings(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if r.Method == http.MethodGet {
		startAt, maxResults, ok := securityPage(w, r)
		if !ok {
			return
		}
		mappings, err := h.Store.IssueSecuritySchemeMappings(r.Context(), workspaceID)
		if err != nil {
			issueSecurityError(w, err)
			return
		}
		schemes, projects := stringQuerySet(securityQueryValues(r, "issueSecuritySchemeId")), stringQuerySet(securityQueryValues(r, "projectId"))
		filtered := []store.IssueSecuritySchemeMapping{}
		for _, mapping := range mappings {
			if len(schemes) > 0 && !schemes[mapping.SchemeID] || len(projects) > 0 && !projects[mapping.ProjectID] {
				continue
			}
			filtered = append(filtered, mapping)
		}
		page := pageSlice(filtered, startAt, maxResults)
		values := make([]map[string]any, 0, len(page))
		for _, mapping := range page {
			values = append(values, map[string]any{"issueSecuritySchemeId": mapping.SchemeID, "projectId": mapping.ProjectID})
		}
		writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(filtered), startAt, maxResults))
		return
	}
	if r.Method != http.MethodPut {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var request struct {
		ProjectID string          `json:"projectId"`
		SchemeID  json.RawMessage `json:"schemeId"`
		Mappings  []struct {
			Old json.RawMessage `json:"oldLevelId"`
			New json.RawMessage `json:"newLevelId"`
		} `json:"oldToNewSecurityLevelMappings"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	if request.ProjectID == "" || len(request.SchemeID) == 0 {
		issueSecurityError(w, store.ErrIssueSecurityValidation)
		return
	}
	target := ""
	if string(request.SchemeID) != "null" {
		if err := json.Unmarshal(request.SchemeID, &target); err != nil || target == "" {
			issueSecurityError(w, store.ErrIssueSecurityValidation)
			return
		}
	}
	mappings := []store.IssueSecurityLevelMapping{}
	for _, mapping := range request.Mappings {
		oldLevelID, oldPresent, oldErr := nullableSecurityID(mapping.Old)
		newLevelID, newPresent, newErr := nullableSecurityID(mapping.New)
		if !oldPresent || !newPresent || oldErr != nil || newErr != nil {
			issueSecurityError(w, store.ErrIssueSecurityValidation)
			return
		}
		mappings = append(mappings, store.IssueSecurityLevelMapping{OldLevelID: oldLevelID, NewLevelID: newLevelID, OldUnsecured: oldLevelID == ""})
	}
	task, err := h.Store.EnqueueAssignIssueSecurityScheme(r.Context(), workspaceID, actorID, request.ProjectID, target, mappings)
	if err != nil {
		issueSecurityError(w, err)
		return
	}
	w.Header().Set("Location", h.BaseURL+"/rest/api/3/task/"+task.ID)
	writeJSON(w, http.StatusSeeOther, h.apiTaskBean(task))
}

func (h *Handler) securitySchemeSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	startAt, maxResults, ok := securityPage(w, r)
	if !ok {
		return
	}
	schemes, err := h.Store.IssueSecuritySchemes(r.Context(), workspaceID, false)
	if err != nil {
		issueSecurityError(w, err)
		return
	}
	mappings, err := h.Store.IssueSecuritySchemeMappings(r.Context(), workspaceID)
	if err != nil {
		issueSecurityError(w, err)
		return
	}
	byScheme := map[string][]string{}
	for _, mapping := range mappings {
		byScheme[mapping.SchemeID] = append(byScheme[mapping.SchemeID], mapping.ProjectID)
	}
	ids, projects := stringQuerySet(securityQueryValues(r, "id")), stringQuerySet(securityQueryValues(r, "projectId"))
	filtered := []*models.SecurityScheme{}
	for _, scheme := range schemes {
		if len(ids) > 0 && !ids[scheme.ID] {
			continue
		}
		if len(projects) > 0 {
			matched := false
			for _, project := range byScheme[scheme.ID] {
				if projects[project] {
					matched = true
				}
			}
			if !matched {
				continue
			}
		}
		scheme.ProjectIDs = byScheme[scheme.ID]
		filtered = append(filtered, scheme)
	}
	page := pageSlice(filtered, startAt, maxResults)
	values := make([]map[string]any, 0, len(page))
	for _, scheme := range page {
		bean := h.securitySchemeBean(scheme, false)
		if value, exists := bean["defaultSecurityLevelId"]; exists {
			bean["defaultLevel"] = value
		}
		bean["projectIds"] = scheme.ProjectIDs
		values = append(values, bean)
	}
	writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(filtered), startAt, maxResults))
}

func (h *Handler) securityLevelCollection(w http.ResponseWriter, r *http.Request, schemeID string) {
	if r.Method != http.MethodPut {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		Levels []securityLevelRequest `json:"levels"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	if err := h.Store.AddIssueSecurityLevels(r.Context(), workspaceID, actorID, schemeID, securityInputs(request.Levels)); err != nil {
		issueSecurityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) securityLevelMutation(w http.ResponseWriter, r *http.Request, schemeID, levelID string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if r.Method == http.MethodPut {
		var request struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		if err := h.Store.UpdateIssueSecurityLevel(r.Context(), workspaceID, actorID, schemeID, levelID, request.Name, request.Description); err != nil {
			issueSecurityError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodDelete {
		task, err := h.Store.EnqueueRemoveIssueSecurityLevel(r.Context(), workspaceID, actorID, schemeID, levelID, r.URL.Query().Get("replaceWith"))
		if err != nil {
			issueSecurityError(w, err)
			return
		}
		w.Header().Set("Location", h.BaseURL+"/rest/api/3/task/"+task.ID)
		writeJSON(w, http.StatusSeeOther, h.apiTaskBean(task))
		return
	}
	jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
}

func (h *Handler) securityMemberCollection(w http.ResponseWriter, r *http.Request, schemeID, levelID string) {
	if r.Method != http.MethodPut {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		Members []securityMemberRequest `json:"members"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	inputs := []store.SecurityLevelMemberInput{}
	for _, member := range request.Members {
		inputs = append(inputs, store.SecurityLevelMemberInput{Type: member.Type, Parameter: member.Parameter})
	}
	if err := h.Store.AddIssueSecurityMembers(r.Context(), workspaceID, actorID, schemeID, levelID, inputs); err != nil {
		issueSecurityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) securityMemberResource(w http.ResponseWriter, r *http.Request, schemeID, levelID, rawMemberID string) {
	if r.Method != http.MethodDelete {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	memberID, err := strconv.ParseInt(rawMemberID, 10, 64)
	if err != nil || memberID <= 0 {
		issueSecurityError(w, store.ErrIssueSecurityValidation)
		return
	}
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if err = h.Store.DeleteIssueSecurityMember(r.Context(), workspaceID, actorID, schemeID, levelID, memberID); err != nil {
		issueSecurityError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) securityLevelResource(w http.ResponseWriter, r *http.Request, levelID string) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	level, schemeID, err := h.Store.IssueSecurityLevel(r.Context(), workspaceID, levelID)
	if err != nil {
		issueSecurityError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.securityLevelBean(schemeID, *level))
}

func (h *Handler) projectSecurityRoute(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/project/"), "/")
	if len(parts) != 2 {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	workspaceID, userID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	scheme, project, err := h.Store.AssignedIssueSecurityScheme(r.Context(), workspaceID, parts[0], true)
	if err != nil {
		issueSecurityError(w, err)
		return
	}
	permission := "BROWSE_PROJECTS"
	if parts[1] == "issuesecuritylevelscheme" {
		permission = "ADMINISTER_PROJECTS"
	}
	allowed, err := h.Store.HasProjectPermission(r.Context(), workspaceID, userID, project.ID, "", permission)
	if err != nil {
		issueSecurityError(w, err)
		return
	}
	if !allowed {
		jiraError(w, http.StatusForbidden, "You do not have permission to view this project security configuration.")
		return
	}
	if parts[1] == "issuesecuritylevelscheme" {
		if scheme == nil {
			issueSecurityError(w, store.ErrIssueSecurityNotFound)
			return
		}
		writeJSON(w, http.StatusOK, h.securitySchemeBean(scheme, true))
		return
	}
	levels := []map[string]any{}
	if scheme != nil {
		for _, level := range scheme.Levels {
			visible, visibilityErr := h.Store.CanUseIssueSecurityLevel(r.Context(), workspaceID, project.ID, "", userID, level.ID)
			if visibilityErr != nil {
				issueSecurityError(w, visibilityErr)
				return
			}
			if visible {
				levels = append(levels, h.securityLevelBean(scheme.ID, level))
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"levels": levels})
}

// legacyProjectSecurityRoute preserves zzira's original project-key mapping
// extension for existing clients. Jira Cloud clients use the paginated
// /issuesecurityschemes/project endpoint above.
func (h *Handler) legacyProjectSecurityRoute(w http.ResponseWriter, r *http.Request, projectIDOrKey string) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectIDOrKey)
	if err != nil {
		issueSecurityError(w, store.ErrIssueSecurityNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		scheme, _, err := h.Store.AssignedIssueSecurityScheme(r.Context(), workspaceID, project.ID, true)
		if err != nil {
			issueSecurityError(w, err)
			return
		}
		if scheme == nil {
			writeJSON(w, http.StatusOK, map[string]any{"id": nil})
			return
		}
		writeJSON(w, http.StatusOK, h.securitySchemeBean(scheme, true))
	case http.MethodPut:
		var request struct {
			ID string `json:"id"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		if request.ID == "" {
			issueSecurityError(w, store.ErrIssueSecurityValidation)
			return
		}
		if err := h.Store.AssignSecurityScheme(r.Context(), project.ID, request.ID); err != nil {
			issueSecurityError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
