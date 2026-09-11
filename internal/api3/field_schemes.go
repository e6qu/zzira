package api3

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

// A field association scheme is served from this product's field configuration
// scheme, so both APIs describe one model rather than two that can drift.

func (h *Handler) fieldSchemeLinks(schemeID string) map[string]any {
	base := h.BaseURL + "/rest/api/3/config/fieldschemes/" + schemeID
	return map[string]any{"associations": base + "/fields", "projects": base + "/projects"}
}

func (h *Handler) fieldSchemeBean(scheme *store.FieldScheme) map[string]any {
	return map[string]any{
		"id": wireNumericID(scheme.ID), "name": scheme.Name, "description": scheme.Description,
		"isDefault": scheme.IsDefault, "fieldsCount": scheme.FieldsCount,
		"links": h.fieldSchemeLinks(scheme.ID),
	}
}

func fieldSchemeParametersBean(parameters store.FieldSchemeParameters, withWorkType bool) map[string]any {
	bean := map[string]any{"isRequired": parameters.IsRequired, "description": parameters.Description}
	if withWorkType {
		bean["workTypeId"] = wireNumericID(parameters.WorkTypeID)
	}
	return bean
}

// fieldSchemeRoute dispatches the `/config/fieldschemes` family.
func (h *Handler) fieldSchemeRoute(w http.ResponseWriter, r *http.Request, path string) {
	rest := strings.Trim(strings.TrimPrefix(path, "/config/fieldschemes"), "/")
	parts := []string{}
	if rest != "" {
		parts = strings.Split(rest, "/")
	}
	switch {
	case len(parts) == 0 && r.Method == http.MethodGet:
		h.listFieldSchemes(w, r)
	case len(parts) == 0 && r.Method == http.MethodPost:
		h.createFieldScheme(w, r)
	case len(parts) == 1 && parts[0] == "fields":
		h.writeFieldSchemeFields(w, r)
	case len(parts) == 2 && parts[0] == "fields" && parts[1] == "parameters":
		h.writeFieldSchemeParameters(w, r)
	case len(parts) == 1 && parts[0] == "projects":
		h.fieldSchemeProjectsCollection(w, r)
	case len(parts) == 1:
		h.fieldSchemeResource(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "clone" && r.Method == http.MethodPost:
		h.cloneFieldScheme(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "fields" && r.Method == http.MethodGet:
		h.listFieldSchemeFields(w, r, parts[0])
	case len(parts) == 2 && parts[1] == "projects" && r.Method == http.MethodGet:
		h.listFieldSchemeProjects(w, r, parts[0])
	case len(parts) == 4 && parts[1] == "fields" && parts[3] == "parameters" && r.Method == http.MethodGet:
		h.fieldSchemeFieldParameters(w, r, parts[0], parts[2])
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) listFieldSchemes(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	startAt, maxResults, err := notificationPage(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
		return
	}
	schemes, err := h.Store.FieldSchemes(r.Context(), workspaceID,
		securityQueryValues(r, "projectId"), strings.TrimSpace(r.URL.Query().Get("query")))
	if err != nil {
		fieldConfigError(w, err)
		return
	}
	page := pageSlice(schemes, startAt, maxResults)
	values := make([]map[string]any, 0, len(page))
	for i := range page {
		values = append(values, h.fieldSchemeBean(&page[i]))
	}
	writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(schemes), startAt, maxResults))
}

func (h *Handler) createFieldScheme(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	scheme, err := h.Store.CreateFieldConfigurationScheme(r.Context(), workspaceID, actorID, request.Name, request.Description)
	if err != nil {
		fieldConfigError(w, err)
		return
	}
	created, err := h.Store.FieldSchemeByID(r.Context(), workspaceID, scheme.ID)
	if err != nil {
		fieldConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.fieldSchemeBean(created))
}

func (h *Handler) fieldSchemeResource(w http.ResponseWriter, r *http.Request, schemeID string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		scheme, err := h.Store.FieldSchemeByID(r.Context(), workspaceID, schemeID)
		if err != nil {
			fieldConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.fieldSchemeBean(scheme))
	case http.MethodPut:
		var request struct {
			Name        *string `json:"name"`
			Description *string `json:"description"`
		}
		if !decodeProjectRequest(w, r, &request) {
			return
		}
		if err := h.Store.UpdateFieldConfigurationScheme(r.Context(), workspaceID, actorID, schemeID, request.Name, request.Description); err != nil {
			fieldConfigError(w, err)
			return
		}
		scheme, err := h.Store.FieldSchemeByID(r.Context(), workspaceID, schemeID)
		if err != nil {
			fieldConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h.fieldSchemeBean(scheme))
	case http.MethodDelete:
		if err := h.Store.DeleteFieldConfigurationScheme(r.Context(), workspaceID, actorID, schemeID); err != nil {
			fieldConfigError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": schemeID, "deleted": true})
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) cloneFieldScheme(w http.ResponseWriter, r *http.Request, schemeID string) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if !decodeProjectRequest(w, r, &request) {
		return
	}
	scheme, err := h.Store.CloneFieldScheme(r.Context(), workspaceID, actorID, schemeID, request.Name, request.Description)
	if err != nil {
		fieldConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h.fieldSchemeBean(scheme))
}

func (h *Handler) listFieldSchemeFields(w http.ResponseWriter, r *http.Request, schemeID string) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	startAt, maxResults, err := notificationPage(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
		return
	}
	if _, err := h.Store.FieldSchemeByID(r.Context(), workspaceID, schemeID); err != nil {
		fieldConfigError(w, err)
		return
	}
	fields, err := h.Store.FieldSchemeFields(r.Context(), workspaceID, schemeID, securityQueryValues(r, "fieldId"))
	if err != nil {
		fieldConfigError(w, err)
		return
	}
	page := pageSlice(fields, startAt, maxResults)
	values := make([]map[string]any, 0, len(page))
	for _, field := range page {
		restricted := make([]any, 0, len(field.RestrictedToWorkTypes))
		for _, workTypeID := range field.RestrictedToWorkTypes {
			restricted = append(restricted, wireNumericID(workTypeID))
		}
		overrides := make([]map[string]any, 0, len(field.WorkTypeParameters))
		for _, parameters := range field.WorkTypeParameters {
			overrides = append(overrides, fieldSchemeParametersBean(parameters, true))
		}
		values = append(values, map[string]any{
			"fieldId": field.FieldID, "parameters": fieldSchemeParametersBean(field.Parameters, false),
			"restrictedToWorkTypes": restricted, "workTypeParameters": overrides,
			// Every associated field can be read and written here; narrower
			// operation sets belong to rules this model does not have.
			"allowedOperations": []string{"READ", "WRITE"},
		})
	}
	writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(fields), startAt, maxResults))
}

func (h *Handler) fieldSchemeFieldParameters(w http.ResponseWriter, r *http.Request, schemeID, fieldID string) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	fields, err := h.Store.FieldSchemeFields(r.Context(), workspaceID, schemeID, []string{fieldID})
	if err != nil {
		fieldConfigError(w, err)
		return
	}
	if len(fields) == 0 {
		jiraError(w, http.StatusNotFound, "The field is not associated with this field association scheme.")
		return
	}
	overrides := make([]map[string]any, 0, len(fields[0].WorkTypeParameters))
	for _, parameters := range fields[0].WorkTypeParameters {
		overrides = append(overrides, fieldSchemeParametersBean(parameters, true))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"fieldId": fieldID, "parameters": fieldSchemeParametersBean(fields[0].Parameters, false),
		"workTypeParameters": overrides,
	})
}

// decodeFieldSchemeItems reads Jira's field-keyed bulk write body.
func decodeFieldSchemeItems(w http.ResponseWriter, r *http.Request) (map[string][]store.FieldSchemeFieldRequest, bool) {
	var wire map[string][]struct {
		SchemeIDs             []json.RawMessage `json:"schemeIds"`
		SchemeID              json.RawMessage   `json:"schemeId"`
		RestrictedToWorkTypes []json.RawMessage `json:"restrictedToWorkTypes"`
		WorkTypeIDs           []json.RawMessage `json:"workTypeIds"`
		Parameters            *struct {
			IsRequired  bool   `json:"isRequired"`
			Description string `json:"description"`
		} `json:"parameters"`
		WorkTypeParameters []struct {
			WorkTypeID  json.RawMessage `json:"workTypeId"`
			IsRequired  bool            `json:"isRequired"`
			Description string          `json:"description"`
		} `json:"workTypeParameters"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&wire); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return nil, false
	}
	if len(wire) == 0 || len(wire) > 100 {
		jiraError(w, http.StatusBadRequest, "Between 1 and 100 fields are required.")
		return nil, false
	}
	request := map[string][]store.FieldSchemeFieldRequest{}
	for fieldID, items := range wire {
		for _, item := range items {
			converted := store.FieldSchemeFieldRequest{
				SchemeIDs:             rawIDs(item.SchemeIDs),
				RestrictedToWorkTypes: rawIDs(item.RestrictedToWorkTypes),
			}
			// The parameter removal names one scheme and the work types whose
			// overrides go, rather than a restriction.
			if len(converted.SchemeIDs) == 0 && len(item.SchemeID) > 0 {
				converted.SchemeIDs = rawIDs([]json.RawMessage{item.SchemeID})
			}
			if len(converted.RestrictedToWorkTypes) == 0 {
				converted.RestrictedToWorkTypes = rawIDs(item.WorkTypeIDs)
			}
			if item.Parameters != nil {
				converted.Parameters = &store.FieldSchemeParameters{
					IsRequired: item.Parameters.IsRequired, Description: item.Parameters.Description,
				}
			}
			for _, override := range item.WorkTypeParameters {
				converted.WorkTypeParameters = append(converted.WorkTypeParameters, store.FieldSchemeParameters{
					WorkTypeID: strings.Trim(string(override.WorkTypeID), `"`),
					IsRequired: override.IsRequired, Description: override.Description,
				})
			}
			request[fieldID] = append(request[fieldID], converted)
		}
	}
	return request, true
}

// rawIDs accepts Jira's numeric ids as well as this product's string ids.
func rawIDs(values []json.RawMessage) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strings.Trim(string(value), `"`))
	}
	return out
}

func (h *Handler) writeFieldSchemeFields(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	request, ok := decodeFieldSchemeItems(w, r)
	if !ok {
		return
	}
	var results []store.FieldSchemeWriteResult
	var err error
	if r.Method == http.MethodPut {
		results, err = h.Store.SetFieldSchemeFields(r.Context(), workspaceID, actorID, request)
	} else {
		results, err = h.Store.RemoveFieldSchemeFields(r.Context(), workspaceID, actorID, request)
	}
	if err != nil {
		fieldConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": fieldSchemeResultBeans(results, r.Method == http.MethodPut)})
}

func (h *Handler) writeFieldSchemeParameters(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	request, ok := decodeFieldSchemeItems(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		// Removing an override returns the named work types to the scheme's
		// fallback rules, which is what an absent override means.
		if err := h.Store.RemoveFieldSchemeWorkTypeParameters(r.Context(), workspaceID, actorID, request); err != nil {
			fieldConfigError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	results, err := h.Store.SetFieldSchemeFields(r.Context(), workspaceID, actorID, request)
	if err != nil {
		fieldConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": fieldSchemeResultBeans(results, true)})
}

func fieldSchemeResultBeans(results []store.FieldSchemeWriteResult, withWorkTypes bool) []map[string]any {
	beans := make([]map[string]any, 0, len(results))
	for _, result := range results {
		bean := map[string]any{
			"fieldId": result.FieldID, "schemeId": wireNumericID(result.SchemeID), "success": result.Success,
		}
		if result.Error != "" {
			bean["error"] = result.Error
		}
		if withWorkTypes {
			ids := make([]any, 0, len(result.WorkTypeIDs))
			for _, workTypeID := range result.WorkTypeIDs {
				ids = append(ids, wireNumericID(workTypeID))
			}
			bean["workTypeIds"] = ids
		}
		beans = append(beans, bean)
	}
	return beans
}

func (h *Handler) fieldSchemeProjectsCollection(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	switch r.Method {
	case http.MethodGet:
		startAt, maxResults, err := notificationPage(r)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
			return
		}
		pairs, err := h.Store.ProjectsWithFieldSchemes(r.Context(), workspaceID, securityQueryValues(r, "projectId"))
		if err != nil {
			fieldConfigError(w, err)
			return
		}
		page := pageSlice(pairs, startAt, maxResults)
		values := make([]map[string]any, 0, len(page))
		for _, pair := range page {
			values = append(values, map[string]any{
				"projectId": wireNumericID(pair.ProjectID), "schemeId": wireNumericID(pair.SchemeID),
			})
		}
		writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(pairs), startAt, maxResults))
	case http.MethodPut:
		var wire map[string]struct {
			ProjectIDs []json.RawMessage `json:"projectIds"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&wire); err != nil {
			jiraError(w, http.StatusBadRequest, "Invalid request payload.")
			return
		}
		if len(wire) == 0 {
			jiraError(w, http.StatusBadRequest, "At least one field association scheme is required.")
			return
		}
		results := []map[string]any{}
		for _, schemeID := range sortedMapKeys(wire) {
			for _, projectID := range rawIDs(wire[schemeID].ProjectIDs) {
				bean := map[string]any{
					"schemeId": wireNumericID(schemeID), "projectId": wireNumericID(projectID), "success": true,
				}
				if err := h.Store.AssignFieldConfigurationScheme(r.Context(), workspaceID, actorID, projectID, schemeID); err != nil {
					bean["success"], bean["error"] = false, err.Error()
				}
				results = append(results, bean)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	default:
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) listFieldSchemeProjects(w http.ResponseWriter, r *http.Request, schemeID string) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	startAt, maxResults, err := notificationPage(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
		return
	}
	if _, err := h.Store.FieldSchemeByID(r.Context(), workspaceID, schemeID); err != nil {
		fieldConfigError(w, err)
		return
	}
	projects, err := h.Store.FieldSchemeProjects(r.Context(), workspaceID, schemeID, securityQueryValues(r, "projectId"))
	if err != nil {
		fieldConfigError(w, err)
		return
	}
	page := pageSlice(projects, startAt, maxResults)
	values := make([]map[string]any, 0, len(page))
	for _, project := range page {
		values = append(values, map[string]any{
			"id": project.ID, "key": project.Key, "name": project.Name,
			"deleted": project.LifecycleState != "ACTIVE", "avatarUrls": map[string]any{},
		})
	}
	writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(projects), startAt, maxResults))
}

// fieldAssociationRoute serves `PUT`/`DELETE /field/association`, which
// associate fields with every work type on a set of projects.
func (h *Handler) fieldAssociationRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if r.Method != http.MethodPut && r.Method != http.MethodDelete {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var request struct {
		AssociationContexts []struct {
			Type       string          `json:"type"`
			Identifier json.RawMessage `json:"identifier"`
		} `json:"associationContexts"`
		Fields []struct {
			Type       string          `json:"type"`
			Identifier json.RawMessage `json:"identifier"`
		} `json:"fields"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&request); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	projectIDs := []string{}
	for _, association := range request.AssociationContexts {
		if association.Type != "PROJECT_ID" {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{
				"associationContexts": "Only PROJECT_ID association contexts are supported."})
			return
		}
		projectIDs = append(projectIDs, strings.Trim(string(association.Identifier), `"`))
	}
	fieldIDs := []string{}
	for _, field := range request.Fields {
		if field.Type != "FIELD_ID" {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{
				"fields": "Only FIELD_ID field identifiers are supported."})
			return
		}
		fieldIDs = append(fieldIDs, strings.Trim(string(field.Identifier), `"`))
	}
	if err := h.Store.AssociateFieldsWithProjects(r.Context(), workspaceID, actorID,
		projectIDs, fieldIDs, r.Method == http.MethodPut); err != nil {
		fieldConfigError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
