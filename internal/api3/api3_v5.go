package api3

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/e6qu/zzira/internal/jql"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

var customFieldIDPattern = regexp.MustCompile(`^customfield_[0-9]+$`)

// customFieldsFromBody extracts customfield_NNNNN entries from the raw
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
		if !customFieldIDPattern.MatchString(k) {
			continue
		}
		if out == nil {
			out = map[string]json.RawMessage{}
		}
		out[k] = v
	}
	return out
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
	fields, err := h.Store.CustomFields(r.Context())
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	for _, f := range fields {
		if f.ID == id {
			writeJSON(w, http.StatusOK, h.customFieldBean(f))
			return
		}
	}
	jiraError(w, http.StatusNotFound, "The field does not exist.")
}

func (h *Handler) customFieldBean(f *models.CustomField) map[string]any {
	return map[string]any{
		"id":          f.ID,
		"name":        f.Name,
		"custom":      true,
		"schema":      map[string]any{"type": f.Type},
		"description": f.Description,
		"self":        h.BaseURL + "/rest/api/3/field/" + f.ID,
	}
}

func (h *Handler) listFields(w http.ResponseWriter, r *http.Request) {
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	fields, err := h.Store.CustomFields(r.Context())
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	out := []map[string]any{
		{"id": "summary", "name": "Summary", "custom": false, "schema": map[string]any{"type": "string"}},
		{"id": "description", "name": "Description", "custom": false, "schema": map[string]any{"type": "doc"}},
	}
	for _, f := range fields {
		out = append(out, h.customFieldBean(f))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) createField(w http.ResponseWriter, r *http.Request) {
	if _, _, e := h.authWorkspace(r); e != nil {
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
	field, err := h.Store.CreateCustomField(r.Context(), id, req.Name, fieldType, req.Description)
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, h.customFieldBean(field))
}

// ---- webhooks (Atlassian registration shape) ----

func (h *Handler) webhookRoute(w http.ResponseWriter, r *http.Request) {
	if _, _, e := h.authWorkspace(r); e != nil {
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
		w.WriteHeader(http.StatusOK)
	default:
		id := strings.TrimPrefix(r.URL.Path, "/rest/api/3/webhook/")
		if r.Method != http.MethodDelete {
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if err := h.Store.DeleteWebhook(r.Context(), id); err != nil {
			jiraError(w, http.StatusNotFound, "The webhook does not exist.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) createWebhook(w http.ResponseWriter, r *http.Request) {
	wsID, _, e := h.authWorkspace(r)
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
	webhooks, err := h.Store.Webhooks(r.Context())
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

// ---- filters CRUD ----

func (h *Handler) createFilter(w http.ResponseWriter, r *http.Request) {
	_, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	var req struct {
		Name        string `json:"name"`
		JQL         string `json:"jql"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"name": "A filter name is required."})
		return
	}
	if _, err := jql.Parse(req.JQL); err != nil {
		jiraError(w, http.StatusBadRequest, "Error in the JQL Query: "+err.Error())
		return
	}
	f, err := h.Store.CreateFilter(r.Context(), store.NewID("flt"), req.Name, req.JQL, req.Description, userID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, h.filterBean(f))
}

func (h *Handler) putFilter(w http.ResponseWriter, r *http.Request, id string) {
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	var req struct {
		Name        string `json:"name"`
		JQL         string `json:"jql"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"name": "A filter name is required."})
		return
	}
	f, err := h.Store.UpdateFilter(r.Context(), id, req.Name, req.JQL, req.Description)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Filter does not exist.")
		return
	}
	writeJSON(w, http.StatusOK, h.filterBean(f))
}

func (h *Handler) deleteFilter(w http.ResponseWriter, r *http.Request, id string) {
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	if err := h.Store.DeleteFilter(r.Context(), id); err != nil {
		jiraError(w, http.StatusNotFound, "Filter does not exist.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) filterCRUD(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(rest, "/")
	id := parts[0]
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		h.getFilter(w, r, id)
	case len(parts) == 1 && r.Method == http.MethodPut:
		h.putFilter(w, r, id)
	case len(parts) == 1 && r.Method == http.MethodDelete:
		h.deleteFilter(w, r, id)
	case len(parts) == 2 && parts[1] == "favourite" && r.Method == http.MethodPost:
		h.setFilterFavourite(w, r, id, true)
	case len(parts) == 2 && parts[1] == "favourite" && r.Method == http.MethodDelete:
		h.setFilterFavourite(w, r, id, false)
	default:
		jiraError(w, http.StatusNotFound, "No resource found")
	}
}

func (h *Handler) setFilterFavourite(w http.ResponseWriter, r *http.Request, id string, favourite bool) {
	if _, _, e := h.authWorkspace(r); e != nil {
		writeJerr(w, e)
		return
	}
	if err := h.Store.SetFilterFavourite(r.Context(), id, favourite); err != nil {
		jiraError(w, http.StatusNotFound, "Filter does not exist.")
		return
	}
	f, err := h.Store.FilterByID(r.Context(), id)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Filter does not exist.")
		return
	}
	writeJSON(w, http.StatusOK, h.filterBean(f))
}
