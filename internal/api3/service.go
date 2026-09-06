package api3

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

func (h *Handler) serviceDeskRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/rest/servicedeskapi"), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "info" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"version": "5.17.0", "platformVersion": "1001.0.0-SNAPSHOT", "buildChangeSet": "zzira", "buildDate": "2026-09-06T00:00:00Z", "isLicensedForUse": true, "_links": map[string]string{"self": h.BaseURL + "/rest/servicedeskapi/info"}})
	case len(parts) == 1 && parts[0] == "servicedesk" && r.Method == http.MethodGet:
		desks, err := h.Store.ServiceDesks(r.Context(), workspaceID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load service desks.")
			return
		}
		h.writeServicePage(w, r, serviceDeskBeans(h.BaseURL, desks))
	case len(parts) == 2 && parts[0] == "servicedesk" && r.Method == http.MethodGet:
		desk, err := h.Store.ServiceDesk(r.Context(), workspaceID, parts[1])
		if err != nil {
			jiraError(w, http.StatusNotFound, "Service desk was not found.")
			return
		}
		writeJSON(w, http.StatusOK, serviceDeskBean(h.BaseURL, *desk))
	case len(parts) == 1 && parts[0] == "requesttype" && r.Method == http.MethodGet:
		h.listServiceRequestTypes(w, r, workspaceID, "")
	case len(parts) == 3 && parts[0] == "servicedesk" && parts[2] == "requesttype" && r.Method == http.MethodGet:
		h.listServiceRequestTypes(w, r, workspaceID, parts[1])
	case len(parts) == 3 && parts[0] == "servicedesk" && parts[2] == "requesttype" && r.Method == http.MethodPost:
		if _, _, err := h.authWorkspaceAdmin(r); err != nil {
			writeJerr(w, err)
			return
		}
		var input struct{ Name, Description, HelpText, IssueTypeID string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil || strings.TrimSpace(input.Name) == "" || len(input.Name) > 255 || len(input.Description) > 255 || len(input.HelpText) > 255 || input.IssueTypeID == "" {
			jiraError(w, http.StatusBadRequest, "name and issueTypeId are required and text fields accept at most 255 characters.")
			return
		}
		requestType, err := h.Store.CreateServiceRequestType(r.Context(), workspaceID, parts[1], strings.TrimSpace(input.Name), input.Description, input.HelpText, input.IssueTypeID)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "Could not create request type.")
			return
		}
		writeJSON(w, http.StatusOK, serviceRequestTypeBean(h.BaseURL, *requestType))
	case (len(parts) == 4 || len(parts) == 5) && parts[0] == "servicedesk" && parts[2] == "requesttype":
		requestType, err := h.Store.ServiceRequestType(r.Context(), workspaceID, parts[1], parts[3])
		if err != nil {
			jiraError(w, http.StatusNotFound, "Request type was not found.")
			return
		}
		if len(parts) == 5 && parts[4] == "field" && r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, serviceRequestTypeFields(*requestType))
			return
		}
		if len(parts) == 4 && r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, serviceRequestTypeBean(h.BaseURL, *requestType))
			return
		}
		if len(parts) == 4 && r.Method == http.MethodDelete {
			if _, _, err := h.authWorkspaceAdmin(r); err != nil {
				writeJerr(w, err)
				return
			}
			if err := h.Store.DeleteServiceRequestType(r.Context(), workspaceID, parts[1], parts[3]); err != nil {
				jiraError(w, http.StatusInternalServerError, "Could not delete request type.")
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		fallthrough
	default:
		jiraError(w, http.StatusNotFound, "Service management resource does not exist.")
	}
}

func serviceDeskBean(baseURL string, desk models.ServiceDesk) map[string]any {
	return map[string]any{"id": desk.ID, "projectId": desk.ProjectID, "projectKey": desk.ProjectKey, "projectName": desk.ProjectName, "projectTypeKey": desk.ProjectTypeKey, "_links": map[string]string{"self": baseURL + "/rest/servicedeskapi/servicedesk/" + desk.ID}}
}

func serviceDeskBeans(baseURL string, desks []models.ServiceDesk) []map[string]any {
	values := make([]map[string]any, 0, len(desks))
	for _, desk := range desks {
		values = append(values, serviceDeskBean(baseURL, desk))
	}
	return values
}

func (h *Handler) writeServicePage(w http.ResponseWriter, r *http.Request, values []map[string]any) {
	start, limit := 0, 50
	if value, err := strconv.Atoi(r.URL.Query().Get("start")); err == nil && value >= 0 {
		start = value
	}
	if value, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && value > 0 && value <= 100 {
		limit = value
	}
	end := start + limit
	if start > len(values) {
		start = len(values)
	}
	if end > len(values) {
		end = len(values)
	}
	writeJSON(w, http.StatusOK, map[string]any{"start": start, "limit": limit, "size": end - start, "isLastPage": end == len(values), "values": values[start:end], "_expands": []any{}, "_links": map[string]string{"self": h.BaseURL + r.URL.Path}})
}

func (h *Handler) listServiceRequestTypes(w http.ResponseWriter, r *http.Request, workspaceID, serviceDeskID string) {
	values, err := h.Store.ServiceRequestTypes(r.Context(), workspaceID, serviceDeskID, r.URL.Query().Get("searchQuery"))
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load request types.")
		return
	}
	beans := make([]map[string]any, 0, len(values))
	for _, requestType := range values {
		beans = append(beans, serviceRequestTypeBean(h.BaseURL, requestType))
	}
	h.writeServicePage(w, r, beans)
}

func serviceRequestTypeBean(baseURL string, requestType models.ServiceRequestType) map[string]any {
	return map[string]any{"id": requestType.ID, "serviceDeskId": requestType.ServiceDeskID, "portalId": requestType.ServiceDeskID, "name": requestType.Name, "description": requestType.Description, "helpText": requestType.HelpText, "issueTypeId": requestType.IssueTypeID, "groupIds": requestType.GroupIDs, "canCreateRequest": true, "restrictionStatus": "OPEN", "practice": "service_desk", "_expands": []any{}, "_links": map[string]string{"self": baseURL + "/rest/servicedeskapi/servicedesk/" + requestType.ServiceDeskID + "/requesttype/" + requestType.ID}}
}

func serviceRequestTypeFields(requestType models.ServiceRequestType) map[string]any {
	return map[string]any{"canAddRequestParticipants": true, "canRaiseOnBehalfOf": true, "requestTypeFields": []map[string]any{
		{"fieldId": "summary", "name": "Summary", "description": requestType.HelpText, "required": true, "visible": true, "defaultValues": []any{}, "presetValues": []any{}, "validValues": []any{}, "jiraSchema": map[string]string{"type": "string", "system": "summary"}},
		{"fieldId": "description", "name": "Description", "description": "Describe the request.", "required": false, "visible": true, "defaultValues": []any{}, "presetValues": []any{}, "validValues": []any{}, "jiraSchema": map[string]string{"type": "string", "system": "description"}},
	}}
}
