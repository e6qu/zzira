package api3

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

// jiraFieldTypeKeys maps Jira's canonical custom field type keys onto the types
// this product has. A client configured against Jira sends the long key, so
// accepting only the short name would reject every real request.
var jiraFieldTypeKeys = map[string]string{
	"com.atlassian.jira.plugin.system.customfieldtypes:textfield":     models.CustomFieldText,
	"com.atlassian.jira.plugin.system.customfieldtypes:textarea":      models.CustomFieldText,
	"com.atlassian.jira.plugin.system.customfieldtypes:readonlyfield": models.CustomFieldText,
	"com.atlassian.jira.plugin.system.customfieldtypes:url":           models.CustomFieldText,
	"com.atlassian.jira.plugin.system.customfieldtypes:float":         models.CustomFieldNumber,
	"com.atlassian.jira.plugin.system.customfieldtypes:importid":      models.CustomFieldNumber,
	"com.atlassian.jira.plugin.system.customfieldtypes:datetime":      models.CustomFieldDatetime,
	"com.atlassian.jira.plugin.system.customfieldtypes:datepicker":    models.CustomFieldDatetime,
	"com.atlassian.jira.plugin.system.customfieldtypes:select":        models.CustomFieldSelect,
	"com.atlassian.jira.plugin.system.customfieldtypes:radiobuttons":  models.CustomFieldSelect,
}

// resolveFieldType accepts either this product's short type name or Jira's
// canonical key, and reports whether the type is one this product can serve.
func resolveFieldType(requested string) (string, bool) {
	switch requested {
	case models.CustomFieldText, models.CustomFieldNumber, models.CustomFieldDatetime, models.CustomFieldSelect:
		return requested, true
	}
	if mapped, ok := jiraFieldTypeKeys[requested]; ok {
		return mapped, true
	}
	return "", false
}

func fieldError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrFieldValidation):
		jiraError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		jiraError(w, http.StatusNotFound, "The field does not exist.")
	default:
		jiraError(w, http.StatusInternalServerError, "Could not complete the field operation.")
	}
}

// searchFieldBean is the richer field shape Jira's paginated searches return,
// which carries where the field is used as well as what it is.
func (h *Handler) searchFieldBean(field *models.CustomField, usage store.FieldUsage) map[string]any {
	bean := h.customFieldBean(field)
	bean["isLocked"] = field.AppKey != ""
	bean["contextsCount"] = usage.Contexts
	bean["projectsCount"] = usage.Projects
	bean["screensCount"] = usage.Screens
	if field.SearcherKey != "" {
		bean["searcherKey"] = field.SearcherKey
	}
	return bean
}

// fieldSearchRoute serves Jira's two paginated field searches. They differ only
// in which state they look at, so one handler serves both.
func (h *Handler) fieldSearchRoute(w http.ResponseWriter, r *http.Request, trashed bool) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	startAt, maxResults, err := notificationPage(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
		return
	}
	search := store.FieldSearch{
		Query:      strings.TrimSpace(r.URL.Query().Get("query")),
		IDs:        securityQueryValues(r, "id"),
		OrderBy:    r.URL.Query().Get("orderBy"),
		Trashed:    trashed,
		StartAt:    startAt,
		MaxResults: maxResults,
	}
	if !trashed {
		// Jira's type filter names the long key, so it is resolved the same way
		// a create is; an unknown type matches nothing rather than everything.
		for _, requested := range securityQueryValues(r, "type") {
			resolved, ok := resolveFieldType(requested)
			if !ok {
				resolved = requested
			}
			search.Types = append(search.Types, resolved)
		}
		search.ProjectIDs = securityQueryValues(r, "projectIds")
	}
	page, err := h.Store.SearchCustomFields(r.Context(), workspaceID, search)
	if err != nil {
		fieldError(w, err)
		return
	}
	ids := make([]string, 0, len(page.Fields))
	for _, field := range page.Fields {
		ids = append(ids, field.ID)
	}
	usage, err := h.Store.FieldUsageCounts(r.Context(), workspaceID, ids)
	if err != nil {
		fieldError(w, err)
		return
	}
	values := make([]map[string]any, 0, len(page.Fields))
	for _, field := range page.Fields {
		values = append(values, h.searchFieldBean(field, usage[field.ID]))
	}
	writeJSON(w, http.StatusOK, h.securityPageBean(r, values, page.Total, startAt, maxResults))
}

// updateField renames or re-describes a custom field.
func (h *Handler) updateField(w http.ResponseWriter, r *http.Request, fieldID string) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		SearcherKey *string `json:"searcherKey"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&request); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	if _, err := h.Store.UpdateCustomField(r.Context(), workspaceID, fieldID,
		request.Name, request.Description, request.SearcherKey); err != nil {
		fieldError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// fieldTrashRoute moves a field into or out of the trash, and deletes one that
// is already there.
func (h *Handler) fieldTrashRoute(w http.ResponseWriter, r *http.Request, fieldID string, trashed bool) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if err := h.Store.SetCustomFieldTrashed(r.Context(), workspaceID, fieldID, trashed); err != nil {
		fieldError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func (h *Handler) deleteField(w http.ResponseWriter, r *http.Request, fieldID string) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if err := h.Store.DeleteCustomField(r.Context(), workspaceID, fieldID); err != nil {
		fieldError(w, err)
		return
	}
	// Jira answers 303 with a task descriptor because the removal runs in the
	// background there. It completes here before the response, so the task is
	// reported finished rather than pending.
	self := h.BaseURL + "/rest/api/3/task/field-" + fieldID
	w.Header().Set("Location", self)
	writeJSON(w, http.StatusSeeOther, map[string]any{
		"self": self, "id": "field-" + fieldID, "status": "COMPLETE", "progress": 100,
	})
}

// fieldProjectAssociations reports the projects a field's contexts reach.
func (h *Handler) fieldProjectAssociations(w http.ResponseWriter, r *http.Request, fieldID string) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if _, err := h.Store.CustomFieldByID(r.Context(), workspaceID, fieldID); err != nil {
		fieldError(w, err)
		return
	}
	startAt, maxResults, err := notificationPage(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
		return
	}
	projectIDs, err := h.Store.FieldProjectIDs(r.Context(), workspaceID, fieldID)
	if err != nil {
		fieldError(w, err)
		return
	}
	page := pageSlice(projectIDs, startAt, maxResults)
	values := make([]map[string]any, 0, len(page))
	for _, projectID := range page {
		values = append(values, map[string]any{"projectId": wireNumericID(projectID)})
	}
	writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(projectIDs), startAt, maxResults))
}

// projectsFieldsRoute serves `GET /projects/fields`: which fields apply to
// which project and work type, and whether the form requires them.
func (h *Handler) projectsFieldsRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	startAt, maxResults, err := notificationPage(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
		return
	}
	associations, err := h.Store.ProjectFieldAssociations(r.Context(), workspaceID,
		securityQueryValues(r, "projectId"), securityQueryValues(r, "workTypeId"), securityQueryValues(r, "fieldId"))
	if err != nil {
		fieldError(w, err)
		return
	}
	page := pageSlice(associations, startAt, maxResults)
	values := make([]map[string]any, 0, len(page))
	for _, row := range page {
		values = append(values, map[string]any{
			"fieldId": row.FieldID, "description": row.Description, "isRequired": row.Required,
			"projectId": wireNumericID(row.ProjectID), "workTypeId": wireNumericID(row.WorkTypeID),
		})
	}
	writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(associations), startAt, maxResults))
}

// fieldContextsForField serves `GET /field/{fieldId}/contexts`, which is a
// different operation from `/field/{fieldId}/context`: it reports each context
// with its scope rather than its full definition.
func (h *Handler) fieldContextsForField(w http.ResponseWriter, r *http.Request, fieldID string) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	startAt, maxResults, err := notificationPage(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "startAt and maxResults are invalid.")
		return
	}
	contexts, err := h.Store.CustomFieldContexts(r.Context(), workspaceID, fieldID, nil)
	if err != nil {
		fieldContextError(w, err)
		return
	}
	page := pageSlice(contexts, startAt, maxResults)
	values := make([]map[string]any, 0, len(page))
	for _, found := range page {
		scope := map[string]any{"type": "GLOBAL"}
		if !found.AllProjects {
			// A project-scoped context reports the project it is bound to,
			// which is what tells a client the context is not global.
			projects := make([]map[string]any, 0, len(found.ProjectIDs))
			for _, projectID := range found.ProjectIDs {
				projects = append(projects, map[string]any{"id": wireNumericID(projectID), "type": "PROJECT"})
			}
			scope = map[string]any{"type": "PROJECT", "projects": projects}
		}
		values = append(values, map[string]any{
			"id": wireNumericID(found.ID), "name": found.Name, "scope": scope,
		})
	}
	writeJSON(w, http.StatusOK, h.securityPageBean(r, values, len(contexts), startAt, maxResults))
}
