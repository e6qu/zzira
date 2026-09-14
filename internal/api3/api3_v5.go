package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/e6qu/zzira/internal/models"
)

var customFieldIDPattern = regexp.MustCompile(`^customfield_[0-9]+$`)
var appCustomFieldKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}__[a-zA-Z][a-zA-Z0-9._-]{0,63}$`)
var customFieldInMessagePattern = regexp.MustCompile(`customfield_[0-9]+`)

// customFieldsFromBody extracts custom fields and structured system-field references from the raw
// request body; create and update share this extraction path.
func customFieldsFromBody(body []byte) map[string]json.RawMessage {
	var req struct {
		Fields map[string]json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil
	}
	var out map[string]json.RawMessage
	for k, v := range req.Fields {
		if !customFieldIDPattern.MatchString(k) && !appCustomFieldKeyPattern.MatchString(k) && k != "fixVersions" && k != "versions" && k != "components" {
			continue
		}
		if out == nil {
			out = map[string]json.RawMessage{}
		}
		out[k] = v
	}
	return out
}

func (h *Handler) resolveCustomFieldAliases(ctx context.Context, workspaceID string, values map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	if len(values) == 0 {
		return values, nil
	}
	fields, err := h.Store.CustomFieldsForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	aliases := make(map[string]string)
	for _, field := range fields {
		if field.AppKey != "" {
			aliases[field.AppKey+"__"+field.AppModuleKey] = field.ID
		}
	}
	resolved := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		if id := aliases[key]; id != "" {
			key = id
		}
		resolved[key] = value
	}
	return resolved, nil
}

// ---- custom fields ----

func (h *Handler) fieldRoute(w http.ResponseWriter, r *http.Request, parts []string) {
	if len(parts) == 0 {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		h.getCustomField(w, r, parts[0])
	case len(parts) == 1 && r.Method == http.MethodPut:
		h.updateField(w, r, parts[0])
	case len(parts) == 1 && r.Method == http.MethodDelete:
		h.deleteField(w, r, parts[0])
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) getCustomField(w http.ResponseWriter, r *http.Request, id string) {
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	fields, err := h.Store.CustomFieldsForWorkspace(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	for _, f := range fields {
		if f.ID == id || (f.AppKey != "" && f.AppKey+"__"+f.AppModuleKey == id) {
			writeJSON(w, http.StatusOK, h.customFieldBean(f))
			return
		}
	}
	jiraError(w, http.StatusNotFound, "The field does not exist.")
}

func (h *Handler) customFieldBean(f *models.CustomField) map[string]any {
	schema := customFieldSchema(f)
	bean := map[string]any{
		"id":          f.ID,
		"key":         f.ID,
		"name":        f.Name,
		"custom":      true,
		"orderable":   true,
		"navigable":   true,
		"searchable":  true,
		"clauseNames": []string{f.ID, f.Name},
		"schema":      schema,
		"description": f.Description,
		"self":        h.BaseURL + "/rest/api/3/field/" + f.ID,
		// zzira serves one locale, so the translations are the field's own text.
		"translatedName":        f.Name,
		"translatedDescription": f.Description,
	}
	if f.AppKey != "" {
		key := f.AppKey + "__" + f.AppModuleKey
		bean["key"] = key
		bean["clauseNames"] = []string{f.ID, key, f.Name}
	}
	return bean
}

func (h *Handler) listFields(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	fields, err := h.Store.CustomFieldsForWorkspace(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// The system fields come from the one definition the search also resolves
	// through, so field discovery cannot advertise a field the search does not
	// know or omit one it does.
	out := []map[string]any{}
	for _, definition := range searchFieldDefinitions(nil) {
		out = append(out, map[string]any{
			"id": definition.ID, "key": definition.Key, "name": definition.Name,
			"custom": false, "orderable": true, "navigable": true, "searchable": true,
			"clauseNames": []string{definition.ID}, "schema": definition.Schema,
		})
	}
	for _, f := range fields {
		out = append(out, h.customFieldBean(f))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) createField(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var req struct {
		Name        string `json:"name"`
		Type        string `json:"type"`
		Description string `json:"description"`
		SearcherKey string `json:"searcherKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"name": "A field name is required."})
		return
	}
	fieldType := req.Type
	if fieldType == "" {
		fieldType = models.CustomFieldText
	}
	resolved, ok := resolveFieldType(fieldType)
	if !ok {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{
			"type": "type must be a supported custom field type or its Jira type key"})
		return
	}
	typeKey := models.CustomFieldTypeKeys[resolved]
	if _, jiraKey := jiraFieldTypeKeys[fieldType]; jiraKey {
		typeKey = fieldType
	}
	fieldType = resolved
	seq, err := h.Store.NextCustomFieldNumber(r.Context())
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	id := fmt.Sprintf("customfield_%d", seq)
	field, err := h.Store.CreateWorkspaceCustomFieldOfKind(r.Context(), workspaceID, id, req.Name, fieldType, typeKey, req.Description)
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, h.customFieldBean(field))
}

// ---- webhooks (Atlassian registration shape) ----
