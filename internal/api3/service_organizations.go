package api3

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

func (h *Handler) serviceOrganizationBean(organization models.ServiceOrganization) map[string]any {
	return map[string]any{
		"id": organization.ID, "name": organization.Name, "scimManaged": false,
		"_links": map[string]string{"self": h.BaseURL + "/rest/servicedeskapi/organization/" + organization.ID},
	}
}

func (h *Handler) serviceAgentAccess(r *http.Request, workspaceID, actorID string) (bool, bool) {
	agent, err := h.Store.IsAnyServiceAgent(r.Context(), workspaceID, actorID)
	return agent, err == nil
}

func (h *Handler) serviceDeskAgentAccess(r *http.Request, workspaceID, serviceDeskID, actorID string) bool {
	agent, err := h.Store.IsServiceAgent(r.Context(), workspaceID, serviceDeskID, actorID)
	return err == nil && agent
}

func (h *Handler) createServiceCustomer(w http.ResponseWriter, r *http.Request, workspaceID, actorID string) {
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not authorize customer administration.")
		return
	}
	if !admin {
		jiraError(w, http.StatusForbidden, "Site administrator access is required.")
		return
	}
	var input struct {
		Email       string `json:"email"`
		DisplayName string `json:"displayName"`
		FullName    string `json:"fullName"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		jiraError(w, http.StatusBadRequest, "Request body is invalid.")
		return
	}
	if input.DisplayName == "" {
		input.DisplayName = input.FullName
	}
	if r.URL.Query().Get("strictConflictStatusCode") == "true" {
		if _, err := h.Store.ServiceCustomer(r.Context(), workspaceID, input.Email); err == nil {
			jiraError(w, http.StatusConflict, "A customer with this email already exists.")
			return
		}
	}
	customer, err := h.Commands.CreateServiceCustomer(r.Context(), actorID, workspaceID, input.Email, input.DisplayName)
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, h.serviceUserBean(customer))
}

func (h *Handler) revokeServiceCustomer(w http.ResponseWriter, r *http.Request, workspaceID, actorID, accountID string) {
	if err := h.Commands.RevokePortalOnlyServiceCustomer(r.Context(), actorID, workspaceID, accountID); err != nil {
		if strings.Contains(err.Error(), "administrator") {
			jiraError(w, http.StatusForbidden, err.Error())
		} else {
			jiraError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) serviceOrganizations(w http.ResponseWriter, r *http.Request, workspaceID, actorID string) {
	agent, authorized := h.serviceAgentAccess(r, workspaceID, actorID)
	if !authorized {
		jiraError(w, http.StatusInternalServerError, "Could not authorize organization access.")
		return
	}
	if r.Method == http.MethodPost {
		if !agent {
			jiraError(w, http.StatusForbidden, "Service agent access is required.")
			return
		}
		var input struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
			jiraError(w, http.StatusBadRequest, "Request body is invalid.")
			return
		}
		organization, err := h.Commands.CreateServiceOrganization(r.Context(), actorID, workspaceID, input.Name)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, h.serviceOrganizationBean(*organization))
		return
	}
	accountID := r.URL.Query().Get("accountId")
	if accountID != "" && !agent {
		jiraError(w, http.StatusForbidden, "Service agent access is required when filtering by accountId.")
		return
	}
	organizations, err := h.Store.ServiceOrganizations(r.Context(), workspaceID, actorID, accountID, agent)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load organizations.")
		return
	}
	beans := make([]map[string]any, 0, len(organizations))
	for _, organization := range organizations {
		beans = append(beans, h.serviceOrganizationBean(organization))
	}
	h.writeServicePage(w, r, beans)
}

func (h *Handler) serviceOrganization(w http.ResponseWriter, r *http.Request, workspaceID, actorID, organizationID string) {
	agent, authorized := h.serviceAgentAccess(r, workspaceID, actorID)
	if !authorized {
		jiraError(w, http.StatusInternalServerError, "Could not authorize organization access.")
		return
	}
	organization, err := h.Store.ServiceOrganization(r.Context(), workspaceID, organizationID, actorID, agent)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Organization was not found.")
		return
	}
	if r.Method == http.MethodDelete {
		if !agent {
			jiraError(w, http.StatusForbidden, "Service agent access is required.")
			return
		}
		if err := h.Commands.DeleteServiceOrganization(r.Context(), actorID, workspaceID, organization.ID); err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, h.serviceOrganizationBean(*organization))
}

func (h *Handler) readableServiceOrganization(w http.ResponseWriter, r *http.Request, workspaceID, actorID, organizationID string) bool {
	agent, authorized := h.serviceAgentAccess(r, workspaceID, actorID)
	if !authorized {
		jiraError(w, http.StatusInternalServerError, "Could not authorize organization access.")
		return false
	}
	if _, err := h.Store.ServiceOrganization(r.Context(), workspaceID, organizationID, actorID, agent); err != nil {
		jiraError(w, http.StatusNotFound, "Organization was not found.")
		return false
	}
	return true
}

func (h *Handler) serviceOrganizationPropertyKeys(w http.ResponseWriter, r *http.Request, workspaceID, actorID, organizationID string) {
	if !h.readableServiceOrganization(w, r, workspaceID, actorID, organizationID) {
		return
	}
	keys, err := h.Store.ServiceOrganizationPropertyKeys(r.Context(), workspaceID, organizationID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load organization properties.")
		return
	}
	beans := make([]map[string]string, 0, len(keys))
	for _, key := range keys {
		beans = append(beans, map[string]string{"key": key, "self": h.BaseURL + "/rest/servicedeskapi/organization/" + organizationID + "/property/" + key})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entityPropertyKeyBeans": beans})
}

func (h *Handler) serviceOrganizationProperty(w http.ResponseWriter, r *http.Request, workspaceID, actorID, organizationID, key string) {
	if !h.readableServiceOrganization(w, r, workspaceID, actorID, organizationID) {
		return
	}
	if r.Method == http.MethodGet {
		value, err := h.Store.ServiceOrganizationProperty(r.Context(), workspaceID, organizationID, key)
		if err != nil {
			jiraError(w, http.StatusNotFound, "Organization property was not found.")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": json.RawMessage(value)})
		return
	}
	agent, _ := h.serviceAgentAccess(r, workspaceID, actorID)
	if !agent {
		jiraError(w, http.StatusForbidden, "Service agent access is required.")
		return
	}
	if r.Method == http.MethodPut {
		var value json.RawMessage
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32769)).Decode(&value); err != nil {
			jiraError(w, http.StatusBadRequest, "A valid JSON property value is required.")
			return
		}
		if err := h.Commands.SetServiceOrganizationProperty(r.Context(), actorID, workspaceID, organizationID, key, value); err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.Commands.DeleteServiceOrganizationProperty(r.Context(), actorID, workspaceID, organizationID, key); err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type serviceUserIDsBody struct {
	AccountIDs []string `json:"accountIds"`
	Usernames  []string `json:"usernames"`
}

func (b serviceUserIDsBody) ids() []string { return append(b.AccountIDs, b.Usernames...) }

func decodeServiceUserIDs(w http.ResponseWriter, r *http.Request) (serviceUserIDsBody, bool) {
	var input serviceUserIDsBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		jiraError(w, http.StatusBadRequest, "Request body is invalid.")
		return input, false
	}
	return input, true
}

func (h *Handler) serviceOrganizationUsers(w http.ResponseWriter, r *http.Request, workspaceID, actorID, organizationID string) {
	agent, authorized := h.serviceAgentAccess(r, workspaceID, actorID)
	if !authorized {
		jiraError(w, http.StatusInternalServerError, "Could not authorize organization access.")
		return
	}
	if !agent {
		jiraError(w, http.StatusForbidden, "Service agent access is required.")
		return
	}
	if _, err := h.Store.ServiceOrganization(r.Context(), workspaceID, organizationID, actorID, true); err != nil {
		jiraError(w, http.StatusNotFound, "Organization was not found.")
		return
	}
	if r.Method == http.MethodGet {
		users, err := h.Store.ServiceOrganizationUsers(r.Context(), workspaceID, organizationID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load organization users.")
			return
		}
		beans := make([]map[string]any, 0, len(users))
		for _, user := range users {
			beans = append(beans, h.serviceUserBean(user))
		}
		h.writeServicePage(w, r, beans)
		return
	}
	input, ok := decodeServiceUserIDs(w, r)
	if !ok {
		return
	}
	if err := h.Commands.SetServiceOrganizationUsers(r.Context(), actorID, workspaceID, organizationID, input.ids(), r.Method == http.MethodPost); err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) serviceDeskCustomers(w http.ResponseWriter, r *http.Request, workspaceID, actorID, serviceDeskID string) {
	if !h.serviceDeskAgentAccess(r, workspaceID, serviceDeskID, actorID) {
		jiraError(w, http.StatusForbidden, "Service agent access is required.")
		return
	}
	if r.Method == http.MethodGet {
		users, err := h.Store.ServiceDeskCustomers(r.Context(), workspaceID, serviceDeskID, r.URL.Query().Get("query"))
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load service desk customers.")
			return
		}
		beans := make([]map[string]any, 0, len(users))
		for _, user := range users {
			beans = append(beans, h.serviceUserBean(user))
		}
		h.writeServicePage(w, r, beans)
		return
	}
	input, ok := decodeServiceUserIDs(w, r)
	if !ok {
		return
	}
	if err := h.Commands.SetServiceDeskCustomers(r.Context(), actorID, workspaceID, serviceDeskID, input.ids(), r.Method == http.MethodPost); err != nil {
		if strings.Contains(err.Error(), "administrator") {
			jiraError(w, http.StatusForbidden, err.Error())
		} else {
			jiraError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) inviteServiceDeskCustomer(w http.ResponseWriter, r *http.Request, workspaceID, actorID, serviceDeskID string) {
	var input struct {
		Email, DisplayName string
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		jiraError(w, http.StatusBadRequest, "Request body is invalid.")
		return
	}
	if r.URL.Query().Get("strictConflictStatusCode") == "true" {
		if _, err := h.Store.ServiceCustomer(r.Context(), workspaceID, input.Email); err == nil {
			jiraError(w, http.StatusConflict, "A customer with this email already exists.")
			return
		}
	}
	customer, err := h.Commands.InviteServiceDeskCustomer(r.Context(), actorID, workspaceID, serviceDeskID, input.Email, input.DisplayName)
	if err != nil {
		if strings.Contains(err.Error(), "administrator") {
			jiraError(w, http.StatusForbidden, err.Error())
		} else {
			jiraError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, h.serviceUserBean(customer))
}

func (h *Handler) serviceDeskOrganizations(w http.ResponseWriter, r *http.Request, workspaceID, actorID, serviceDeskID string) {
	if !h.serviceDeskAgentAccess(r, workspaceID, serviceDeskID, actorID) {
		jiraError(w, http.StatusForbidden, "Service agent access is required.")
		return
	}
	if r.Method == http.MethodGet {
		organizations, err := h.Store.ServiceDeskOrganizations(r.Context(), workspaceID, serviceDeskID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load service desk organizations.")
			return
		}
		beans := make([]map[string]any, 0, len(organizations))
		for _, organization := range organizations {
			beans = append(beans, h.serviceOrganizationBean(organization))
		}
		h.writeServicePage(w, r, beans)
		return
	}
	var input struct {
		OrganizationID json.Number `json:"organizationId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil || input.OrganizationID.String() == "" {
		jiraError(w, http.StatusBadRequest, "organizationId is required.")
		return
	}
	if err := h.Commands.SetServiceDeskOrganization(r.Context(), actorID, workspaceID, serviceDeskID, input.OrganizationID.String(), r.Method == http.MethodPost); err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
