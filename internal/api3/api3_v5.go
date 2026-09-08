package api3

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/e6qu/zzira/internal/jql"

	"github.com/e6qu/zzira/internal/models"
)

var customFieldIDPattern = regexp.MustCompile(`^customfield_[0-9]+$`)
var appCustomFieldKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}__[a-zA-Z][a-zA-Z0-9._-]{0,63}$`)
var customFieldInMessagePattern = regexp.MustCompile(`customfield_[0-9]+`)

// customFieldsFromBody extracts custom fields and version references from the raw
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
		if !customFieldIDPattern.MatchString(k) && !appCustomFieldKeyPattern.MatchString(k) && k != "fixVersions" && k != "versions" {
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
	case len(parts) == 1 && r.Method == http.MethodDelete:
		if _, _, e := h.authWorkspace(r); e != nil {
			writeJerr(w, e)
			return
		}
		jiraError(w, http.StatusMethodNotAllowed, "Deleting fields is not supported.")
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
	schema := map[string]any{"type": f.Type}
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
	}
	if f.AppKey != "" {
		key := f.AppKey + "__" + f.AppModuleKey
		bean["key"] = key
		bean["clauseNames"] = []string{f.ID, key, f.Name}
		schema["custom"] = key
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
	out := []map[string]any{
		{"id": "fixVersions", "name": "Fix versions", "custom": false, "schema": map[string]any{"type": "array", "items": "version", "system": "fixVersions"}},
		{"id": "versions", "name": "Affects versions", "custom": false, "schema": map[string]any{"type": "array", "items": "version", "system": "versions"}},
		{"id": "summary", "name": "Summary", "custom": false, "schema": map[string]any{"type": "string"}},
		{"id": "description", "name": "Description", "custom": false, "schema": map[string]any{"type": "doc"}},
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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"name": "A field name is required."})
		return
	}
	fieldType := req.Type
	if fieldType == "" {
		fieldType = models.CustomFieldText
	}
	switch fieldType {
	case models.CustomFieldText, models.CustomFieldNumber, models.CustomFieldDatetime:
	default:
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"type": "type must be text, number, or datetime"})
		return
	}
	seq, err := h.Store.NextCustomFieldNumber(r.Context())
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	id := fmt.Sprintf("customfield_%d", 10000+seq)
	field, err := h.Store.CreateWorkspaceCustomField(r.Context(), workspaceID, id, req.Name, fieldType, req.Description)
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, h.customFieldBean(field))
}

// ---- webhooks (Atlassian registration shape) ----

func (h *Handler) webhookRoute(w http.ResponseWriter, r *http.Request) {
	wsID, _, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	switch r.URL.Path {
	case "/rest/api/3/webhook":
		switch r.Method {
		case http.MethodPost:
			h.createWebhook(w, r)
		case http.MethodGet:
			h.listWebhooks(w, r)
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case "/rest/api/3/webhook/refresh":
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			w.Header().Set("Allow", "PUT, POST")
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		w.WriteHeader(http.StatusOK)
	default:
		id := strings.TrimPrefix(r.URL.Path, "/rest/api/3/webhook/")
		if r.Method != http.MethodDelete {
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if err := h.Store.DeleteWebhook(r.Context(), wsID, id); err != nil {
			jiraError(w, http.StatusNotFound, "The webhook does not exist.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) createWebhook(w http.ResponseWriter, r *http.Request) {
	wsID, _, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var req struct {
		URL      string `json:"url"`
		Webhooks []struct {
			JQL    string   `json:"jqlFilter"`
			Events []string `json:"events"`
		} `json:"webhooks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" || len(req.Webhooks) == 0 {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"url": "A webhook URL and at least one webhook spec are required."})
		return
	}
	for _, spec := range req.Webhooks {
		if spec.JQL == "" {
			continue
		}
		if _, err := jql.Parse(spec.JQL); err != nil {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"jqlFilter": "Error in the JQL Query: " + err.Error()})
			return
		}
	}
	statuses := []map[string]any{}
	for _, spec := range req.Webhooks {
		wh, err := h.Store.CreateWebhook(r.Context(), wsID, req.URL, spec.Events, spec.JQL)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		statuses = append(statuses, map[string]any{
			"createdWebhookId": wh.ID,
		})
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"webhookRegistrationStatus": statuses,
	})
}

func (h *Handler) listWebhooks(w http.ResponseWriter, r *http.Request) {
	wsID, _, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	webhooks, err := h.Store.Webhooks(r.Context(), wsID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	values := make([]map[string]any, 0, len(webhooks))
	for _, w := range webhooks {
		values = append(values, map[string]any{
			"id":          w.ID,
			"url":         w.URL,
			"jqlFilter":   w.JQL,
			"events":      w.Events,
			"active":      w.Active,
			"lastUpdated": "",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"values": values})
}
