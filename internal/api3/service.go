package api3

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/workflow"
)

func (h *Handler) serviceDeskRoute(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/rest/servicedeskapi"), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "customer" && r.Method == http.MethodPost:
		_, actorID, authErr := h.authWorkspace(r)
		if authErr != nil {
			writeJerr(w, authErr)
			return
		}
		agent, err := h.Store.IsAnyServiceAgent(r.Context(), workspaceID, actorID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not authorize service access.")
			return
		}
		if !agent {
			jiraError(w, http.StatusForbidden, "Service agent access is required.")
			return
		}
		var input struct {
			Email       string `json:"email"`
			DisplayName string `json:"displayName"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
			jiraError(w, http.StatusBadRequest, "Request body is invalid.")
			return
		}
		customer, err := h.Commands.CreateServiceCustomer(r.Context(), actorID, workspaceID, input.Email, input.DisplayName)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, h.serviceUserBean(customer))
	case len(parts) == 1 && parts[0] == "request" && r.Method == http.MethodGet:
		h.listServiceRequests(w, r, workspaceID)
	case len(parts) == 1 && parts[0] == "request" && r.Method == http.MethodPost:
		h.createServiceRequest(w, r, workspaceID, false)
	case len(parts) == 2 && parts[0] == "request" && parts[1] == "validate" && r.Method == http.MethodPost:
		h.createServiceRequest(w, r, workspaceID, true)
	case len(parts) == 2 && parts[0] == "request" && r.Method == http.MethodGet:
		h.getServiceRequest(w, r, workspaceID, parts[1])
	case len(parts) == 3 && parts[0] == "request" && parts[2] == "comment" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		h.serviceRequestComments(w, r, workspaceID, parts[1], "")
	case len(parts) == 5 && parts[0] == "request" && parts[2] == "comment" && parts[4] == "attachment" && r.Method == http.MethodGet:
		h.serviceCommentAttachments(w, r, workspaceID, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "request" && parts[2] == "approval" && r.Method == http.MethodGet:
		h.serviceRequestApprovals(w, r, workspaceID, parts[1], "")
	case len(parts) == 4 && parts[0] == "request" && parts[2] == "approval" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		h.serviceRequestApprovals(w, r, workspaceID, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "request" && parts[2] == "attachment" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		h.serviceRequestAttachments(w, r, workspaceID, parts[1])
	case len(parts) == 3 && parts[0] == "request" && parts[2] == "notification" && (r.Method == http.MethodGet || r.Method == http.MethodPut || r.Method == http.MethodDelete):
		h.serviceRequestNotification(w, r, workspaceID, parts[1])
	case len(parts) == 3 && parts[0] == "request" && parts[2] == "feedback" && (r.Method == http.MethodGet || r.Method == http.MethodPost || r.Method == http.MethodDelete):
		h.serviceRequestFeedback(w, r, workspaceID, parts[1])
	case len(parts) == 4 && parts[0] == "request" && parts[2] == "attachment" && r.Method == http.MethodGet:
		h.serviceRequestAttachmentContent(w, r, workspaceID, parts[1], parts[3], false)
	case len(parts) == 5 && parts[0] == "request" && parts[2] == "attachment" && parts[4] == "thumbnail" && r.Method == http.MethodGet:
		h.serviceRequestAttachmentContent(w, r, workspaceID, parts[1], parts[3], true)
	case len(parts) == 3 && parts[0] == "request" && parts[2] == "participant" && (r.Method == http.MethodGet || r.Method == http.MethodPost || r.Method == http.MethodDelete):
		h.serviceRequestParticipants(w, r, workspaceID, parts[1])
	case len(parts) == 4 && parts[0] == "request" && parts[2] == "comment" && r.Method == http.MethodGet:
		h.serviceRequestComments(w, r, workspaceID, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "request" && parts[2] == "status" && r.Method == http.MethodGet:
		h.serviceRequestStatus(w, r, workspaceID, parts[1])
	case len(parts) == 3 && parts[0] == "request" && parts[2] == "sla" && r.Method == http.MethodGet:
		h.serviceRequestSLA(w, r, workspaceID, parts[1], "")
	case len(parts) == 4 && parts[0] == "request" && parts[2] == "sla" && r.Method == http.MethodGet:
		h.serviceRequestSLA(w, r, workspaceID, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "request" && parts[2] == "transition" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		h.serviceRequestTransition(w, r, workspaceID, parts[1])
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
	case len(parts) == 3 && parts[0] == "servicedesk" && parts[2] == "attachTemporaryFile" && r.Method == http.MethodPost:
		h.attachServiceTemporaryFiles(w, r, workspaceID, parts[1])
	case len(parts) == 3 && parts[0] == "servicedesk" && parts[2] == "queue" && r.Method == http.MethodGet:
		h.listServiceQueues(w, r, workspaceID, parts[1])
	case len(parts) == 4 && parts[0] == "servicedesk" && parts[2] == "queue" && r.Method == http.MethodGet:
		h.getServiceQueue(w, r, workspaceID, parts[1], parts[3], false)
	case len(parts) == 5 && parts[0] == "servicedesk" && parts[2] == "queue" && parts[4] == "issue" && r.Method == http.MethodGet:
		h.getServiceQueue(w, r, workspaceID, parts[1], parts[3], true)
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

func (h *Handler) serviceQueueBean(queue models.ServiceQueue, includeCount bool) map[string]any {
	bean := map[string]any{"id": queue.ID, "name": queue.Name, "jql": queue.JQL, "fields": queue.Fields, "_links": map[string]string{"self": h.BaseURL + "/rest/servicedeskapi/servicedesk/" + queue.ServiceDeskID + "/queue/" + queue.ID}}
	if includeCount {
		bean["issueCount"] = queue.IssueCount
	}
	return bean
}

func (h *Handler) listServiceQueues(w http.ResponseWriter, r *http.Request, workspaceID, serviceDeskID string) {
	_, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	agent, err := h.Store.IsServiceAgent(r.Context(), workspaceID, serviceDeskID, actorID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not authorize service desk access.")
		return
	}
	if !agent {
		jiraError(w, http.StatusForbidden, "Service agent access is required.")
		return
	}
	queues, err := h.Store.ServiceQueues(r.Context(), workspaceID, serviceDeskID)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Service desk was not found.")
		return
	}
	includeCount := r.URL.Query().Get("includeCount") == "true"
	beans := make([]map[string]any, 0, len(queues))
	for _, queue := range queues {
		if includeCount {
			counted, _, err := h.Store.ServiceQueueRequests(r.Context(), workspaceID, actorID, serviceDeskID, queue.ID)
			if err != nil {
				jiraError(w, http.StatusInternalServerError, "Could not count queue requests.")
				return
			}
			queue.IssueCount = counted.IssueCount
		}
		beans = append(beans, h.serviceQueueBean(queue, includeCount))
	}
	h.writeServicePage(w, r, beans)
}

func (h *Handler) getServiceQueue(w http.ResponseWriter, r *http.Request, workspaceID, serviceDeskID, queueID string, includeIssues bool) {
	_, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	agent, err := h.Store.IsServiceAgent(r.Context(), workspaceID, serviceDeskID, actorID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not authorize service desk access.")
		return
	}
	if !agent {
		jiraError(w, http.StatusForbidden, "Service agent access is required.")
		return
	}
	queue, requests, err := h.Store.ServiceQueueRequests(r.Context(), workspaceID, actorID, serviceDeskID, queueID)
	if err != nil {
		jiraError(w, http.StatusNotFound, "Queue was not found.")
		return
	}
	if !includeIssues {
		writeJSON(w, http.StatusOK, h.serviceQueueBean(*queue, r.URL.Query().Get("includeCount") == "true"))
		return
	}
	beans := make([]map[string]any, 0, len(requests))
	for _, request := range requests {
		fields := map[string]any{"summary": request.Issue.Summary, "issuetype": request.Issue.IssueType, "created": request.CreatedAt.UTC().Format("2006-01-02T15:04:05.000-0700"), "reporter": h.serviceUserBean(request.Customer), "status": request.Issue.Status}
		if request.Issue.Assignee != nil {
			fields["assignee"] = h.serviceUserBean(request.Issue.Assignee)
		} else {
			fields["assignee"] = nil
		}
		beans = append(beans, map[string]any{"id": request.Issue.ID, "key": request.Issue.Key, "self": h.BaseURL + "/rest/api/3/issue/" + request.Issue.ID, "fields": fields})
	}
	h.writeServicePage(w, r, beans)
}

type createServiceRequestBody struct {
	Channel             string                     `json:"channel"`
	Form                json.RawMessage            `json:"form"`
	IsADFRequest        *bool                      `json:"isAdfRequest"`
	RaiseOnBehalfOf     string                     `json:"raiseOnBehalfOf"`
	RequestFieldValues  map[string]json.RawMessage `json:"requestFieldValues"`
	RequestParticipants []string                   `json:"requestParticipants"`
	RequestTypeID       string                     `json:"requestTypeId"`
	ServiceDeskID       string                     `json:"serviceDeskId"`
}

func decodeServiceText(raw json.RawMessage) (string, bool) {
	var value string
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func serviceADFText(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	var parts []string
	var walk func(any)
	walk = func(node any) {
		switch typed := node.(type) {
		case map[string]any:
			if text, ok := typed["text"].(string); ok {
				parts = append(parts, text)
			}
			if children, ok := typed["content"].([]any); ok {
				for _, child := range children {
					walk(child)
				}
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	return strings.Join(parts, "")
}

func (h *Handler) validateServiceRequestBody(r *http.Request, workspaceID string, input createServiceRequestBody) map[string]string {
	errors := map[string]string{}
	if len(input.Form) > 0 && string(input.Form) != "null" {
		errors["form"] = "Forms are not configured for this request type."
	}
	for _, participant := range input.RequestParticipants {
		if _, err := h.Store.ServiceCustomer(r.Context(), workspaceID, participant); err != nil {
			errors["requestParticipants"] = "Every request participant must be an active service customer."
			break
		}
	}
	for fieldID := range input.RequestFieldValues {
		if fieldID != "summary" && fieldID != "description" {
			errors[fieldID] = "This field is not configured for the request type."
		}
	}
	if input.ServiceDeskID == "" {
		errors["serviceDeskId"] = "A service desk is required."
	}
	if input.RequestTypeID == "" {
		errors["requestTypeId"] = "A request type is required."
	}
	if input.ServiceDeskID != "" && input.RequestTypeID != "" {
		if _, err := h.Store.ServiceRequestType(r.Context(), workspaceID, input.ServiceDeskID, input.RequestTypeID); err != nil {
			errors["requestTypeId"] = "The request type does not belong to this service desk."
		}
	}
	summary, ok := decodeServiceText(input.RequestFieldValues["summary"])
	if !ok || strings.TrimSpace(summary) == "" || len(strings.TrimSpace(summary)) > 255 {
		errors["summary"] = "Summary is required and accepts at most 255 characters."
	}
	if description := input.RequestFieldValues["description"]; len(description) > 1<<20 {
		errors["description"] = "Description accepts at most 1 MiB."
	} else if len(description) > 0 {
		if _, text := decodeServiceText(description); !text {
			var doc struct {
				Type    string `json:"type"`
				Version int    `json:"version"`
			}
			if json.Unmarshal(description, &doc) != nil || doc.Type != "doc" || doc.Version != 1 {
				errors["description"] = "Description must be text or an Atlassian document format value."
			}
		}
	}
	return errors
}

func (h *Handler) createServiceRequest(w http.ResponseWriter, r *http.Request, workspaceID string, validateOnly bool) {
	_, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var input createServiceRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&input); err != nil {
		jiraError(w, http.StatusBadRequest, "Request body is invalid.")
		return
	}
	fieldErrors := h.validateServiceRequestBody(r, workspaceID, input)
	if validateOnly {
		messages := []string{}
		for _, message := range fieldErrors {
			messages = append(messages, message)
		}
		writeJSON(w, http.StatusOK, map[string]any{"valid": len(fieldErrors) == 0, "fieldErrors": fieldErrors, "formErrors": []any{}, "errorMessages": messages, "errorMessage": "", "reasonKey": ""})
		return
	}
	if len(fieldErrors) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"errorMessage": "Request validation failed.", "errorMessages": []string{"Request validation failed."}, "fieldErrors": fieldErrors})
		return
	}
	customerID := actorID
	if input.RaiseOnBehalfOf != "" {
		customer, err := h.Store.ServiceCustomer(r.Context(), workspaceID, input.RaiseOnBehalfOf)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The requested customer does not exist.")
			return
		}
		customerID = customer.ID
	}
	summary, _ := decodeServiceText(input.RequestFieldValues["summary"])
	participantIDs := make([]string, 0, len(input.RequestParticipants))
	for _, participant := range input.RequestParticipants {
		customer, err := h.Store.ServiceCustomer(r.Context(), workspaceID, participant)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "A request participant does not exist.")
			return
		}
		participantIDs = append(participantIDs, customer.ID)
	}
	description, descriptionADF := "", json.RawMessage(nil)
	if raw := input.RequestFieldValues["description"]; len(raw) > 0 {
		if text, ok := decodeServiceText(raw); ok {
			description = text
		} else {
			descriptionADF = raw
		}
	}
	request, err := h.Commands.CreateServiceRequest(r.Context(), commands.CreateServiceRequestInput{
		ActorID: actorID, WorkspaceID: workspaceID, CustomerID: customerID, Channel: input.Channel,
		ServiceDeskID: input.ServiceDeskID, RequestTypeID: input.RequestTypeID,
		Summary: summary, Description: description, DescriptionADF: descriptionADF, ParticipantIDs: participantIDs,
	})
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	bean, err := h.serviceRequestBean(r, workspaceID, actorID, request)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load the created request.")
		return
	}
	writeJSON(w, http.StatusCreated, bean)
}

func (h *Handler) serviceRequestAccess(r *http.Request, workspaceID, issueIDOrKey string) (*models.ServiceRequest, bool, string, *jerr) {
	_, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		return nil, false, "", authErr
	}
	canManage, err := h.Store.CanManageServiceRequest(r.Context(), workspaceID, actorID, issueIDOrKey)
	if err != nil {
		return nil, false, "", &jerr{status: http.StatusInternalServerError, message: "internal error"}
	}
	request, err := h.Store.ServiceRequest(r.Context(), workspaceID, actorID, issueIDOrKey, canManage)
	if err != nil {
		return nil, canManage, actorID, &jerr{status: http.StatusNotFound, message: "Customer request does not exist or you do not have permission to view it."}
	}
	return request, canManage, actorID, nil
}

func (h *Handler) listServiceRequests(w http.ResponseWriter, r *http.Request, workspaceID string) {
	_, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	admin, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load customer requests.")
		return
	}
	allRequests := strings.EqualFold(r.URL.Query().Get("requestOwnership"), "ALL_REQUESTS")
	var requests []*models.ServiceRequest
	if allRequests && admin {
		requests, err = h.Store.ServiceRequests(r.Context(), workspaceID, actorID, r.URL.Query().Get("serviceDeskId"), r.URL.Query().Get("requestTypeId"), true)
	} else if allRequests {
		agent, accessErr := h.Store.IsAnyServiceAgent(r.Context(), workspaceID, actorID)
		if accessErr != nil {
			jiraError(w, http.StatusInternalServerError, "Could not authorize service access.")
			return
		}
		if !agent {
			jiraError(w, http.StatusForbidden, "Service agent access is required for all requests.")
			return
		}
		requests, err = h.Store.ServiceRequestsForAgent(r.Context(), workspaceID, actorID, r.URL.Query().Get("serviceDeskId"), r.URL.Query().Get("requestTypeId"))
	} else {
		requests, err = h.Store.ServiceRequests(r.Context(), workspaceID, actorID, r.URL.Query().Get("serviceDeskId"), r.URL.Query().Get("requestTypeId"), false)
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load customer requests.")
		return
	}
	beans := make([]map[string]any, 0, len(requests))
	for _, request := range requests {
		bean, err := h.serviceRequestBean(r, workspaceID, actorID, request)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load customer requests.")
			return
		}
		beans = append(beans, bean)
	}
	h.writeServicePage(w, r, beans)
}

func (h *Handler) getServiceRequest(w http.ResponseWriter, r *http.Request, workspaceID, issueIDOrKey string) {
	request, _, actorID, accessErr := h.serviceRequestAccess(r, workspaceID, issueIDOrKey)
	if accessErr != nil {
		writeJerr(w, accessErr)
		return
	}
	bean, err := h.serviceRequestBean(r, workspaceID, actorID, request)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load customer request.")
		return
	}
	writeJSON(w, http.StatusOK, bean)
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

func serviceDate(value time.Time) map[string]any {
	value = value.UTC()
	return map[string]any{
		"iso8601":     value.Format("2006-01-02T15:04:05-0700"),
		"jira":        value.Format("2006-01-02T15:04:05.000-0700"),
		"friendly":    value.Format("02/Jan/06 3:04 PM"),
		"epochMillis": value.UnixMilli(),
	}
}

func parseServiceDate(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Unix(0, 0).UTC()
	}
	return parsed
}

func (h *Handler) serviceUserBean(user *models.User) map[string]any {
	if user == nil {
		return nil
	}
	bean := h.userBean(user)
	bean["_links"] = map[string]string{"jiraRest": h.BaseURL + "/rest/api/3/user?accountId=" + user.ID}
	return bean
}

func (h *Handler) serviceStatusBean(status models.Status, changed string) map[string]any {
	return map[string]any{
		"status":         status.Name,
		"statusCategory": map[string]string{"key": status.Category, "name": status.Category},
		"statusDate":     serviceDate(parseServiceDate(changed)),
	}
}

func (h *Handler) serviceRequestBean(r *http.Request, workspaceID, viewerID string, request *models.ServiceRequest) (map[string]any, error) {
	canManage, err := h.Store.CanManageServiceRequest(r.Context(), workspaceID, viewerID, request.Issue.ID)
	if err != nil {
		return nil, err
	}
	comments, err := h.Store.ServiceRequestComments(r.Context(), request.Issue.ID, canManage)
	if err != nil {
		return nil, err
	}
	commentBeans := make([]map[string]any, 0, len(comments))
	for _, comment := range comments {
		attachments, err := h.Store.ServiceCommentAttachments(r.Context(), request.Issue.ID, comment.Comment.ID, canManage)
		if err != nil {
			return nil, err
		}
		for _, attachment := range attachments {
			comment.Attachments = append(comment.Attachments, attachment.Attachment)
		}
		commentBeans = append(commentBeans, h.serviceCommentBean(request, comment))
	}
	attachments, err := h.Store.ServiceRequestAttachments(r.Context(), request.Issue.ID, canManage)
	if err != nil {
		return nil, err
	}
	attachmentBeans := make([]map[string]any, 0, len(attachments))
	for _, attachment := range attachments {
		attachmentBeans = append(attachmentBeans, h.serviceAttachmentBean(request, attachment.Attachment))
	}
	participants, err := h.Store.ServiceRequestParticipants(r.Context(), request.Issue.ID)
	if err != nil {
		return nil, err
	}
	participantBeans := make([]map[string]any, 0, len(participants))
	for _, participant := range participants {
		participantBeans = append(participantBeans, h.serviceUserBean(participant))
	}
	slaBeans := make([]map[string]any, 0)
	if canManage {
		slas, err := h.Store.ServiceSLAs(r.Context(), workspaceID, request.Issue.ID, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		for _, sla := range slas {
			slaBeans = append(slaBeans, h.serviceSLABean(request, sla))
		}
	}
	fields := []map[string]any{
		{"fieldId": "summary", "label": "Summary", "value": request.Issue.Summary, "renderedValue": request.Issue.Summary},
		{"fieldId": "description", "label": "Description", "value": request.Issue.Description, "renderedValue": adf.ToHTML(request.Issue.Description)},
	}
	status := h.serviceStatusBean(request.Issue.Status, request.Issue.UpdatedAt)
	return map[string]any{
		"issueId": request.Issue.ID, "issueKey": request.Issue.Key, "summary": request.Issue.Summary,
		"serviceDeskId": request.ServiceDesk.ID, "requestTypeId": request.RequestType.ID,
		"serviceDesk": serviceDeskBean(h.BaseURL, request.ServiceDesk),
		"requestType": serviceRequestTypeBean(h.BaseURL, request.RequestType),
		"reporter":    h.serviceUserBean(request.Customer), "participants": participantBeans,
		"requestFieldValues": fields, "currentStatus": status, "status": status,
		"createdDate": serviceDate(request.CreatedAt), "channel": request.Channel,
		"comments":    map[string]any{"start": 0, "limit": 50, "size": len(commentBeans), "isLastPage": true, "values": commentBeans},
		"attachments": map[string]any{"start": 0, "limit": 50, "size": len(attachmentBeans), "isLastPage": true, "values": attachmentBeans}, "sla": slaBeans, "actions": []any{}, "_expands": []string{"serviceDesk", "requestType", "currentStatus"},
		"_links": map[string]string{
			"self": h.BaseURL + "/rest/servicedeskapi/request/" + request.Issue.Key,
			"web":  h.BaseURL + "/service/requests/" + request.Issue.Key,
		},
	}, nil
}

func (h *Handler) writeServiceParticipants(w http.ResponseWriter, r *http.Request, users []*models.User) {
	beans := make([]map[string]any, 0, len(users))
	for _, user := range users {
		beans = append(beans, h.serviceUserBean(user))
	}
	h.writeServicePage(w, r, beans)
}

func (h *Handler) serviceRequestParticipants(w http.ResponseWriter, r *http.Request, workspaceID, issueIDOrKey string) {
	request, _, actorID, accessErr := h.serviceRequestAccess(r, workspaceID, issueIDOrKey)
	if accessErr != nil {
		writeJerr(w, accessErr)
		return
	}
	if r.Method == http.MethodGet {
		users, err := h.Store.ServiceRequestParticipants(r.Context(), request.Issue.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load request participants.")
			return
		}
		h.writeServiceParticipants(w, r, users)
		return
	}
	var input struct {
		AccountIDs []string `json:"accountIds"`
		Usernames  []string `json:"usernames"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		jiraError(w, http.StatusBadRequest, "Request body is invalid.")
		return
	}
	identifiers := append(input.AccountIDs, input.Usernames...)
	userIDs := make([]string, 0, len(identifiers))
	for _, identifier := range identifiers {
		customer, err := h.Store.ServiceCustomer(r.Context(), workspaceID, identifier)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "A request participant does not exist.")
			return
		}
		userIDs = append(userIDs, customer.ID)
	}
	users, err := h.Commands.UpdateServiceRequestParticipants(r.Context(), actorID, workspaceID, request.Issue.ID, userIDs, r.Method == http.MethodDelete)
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.writeServiceParticipants(w, r, users)
}

func (h *Handler) serviceCommentBean(request *models.ServiceRequest, comment models.ServiceRequestComment) map[string]any {
	attachments := make([]map[string]any, 0, len(comment.Attachments))
	for _, attachment := range comment.Attachments {
		attachments = append(attachments, h.serviceAttachmentBean(request, attachment))
	}
	return map[string]any{
		"id": comment.Comment.ID, "body": serviceADFText(comment.Comment.Body), "renderedBody": adf.ToHTML(comment.Comment.Body),
		"public": comment.Public, "author": h.serviceUserBean(&models.User{ID: comment.Comment.AuthorID, DisplayName: comment.Comment.AuthorName, Active: true, AccountType: "atlassian"}),
		"created": serviceDate(parseServiceDate(comment.Comment.Created)), "attachments": map[string]any{"start": 0, "limit": 50, "size": len(attachments), "isLastPage": true, "values": attachments}, "_expands": []any{},
		"_links": map[string]string{"self": h.BaseURL + "/rest/servicedeskapi/request/" + request.Issue.Key + "/comment/" + comment.Comment.ID},
	}
}

func (h *Handler) serviceRequestComments(w http.ResponseWriter, r *http.Request, workspaceID, issueIDOrKey, commentID string) {
	request, canManage, actorID, accessErr := h.serviceRequestAccess(r, workspaceID, issueIDOrKey)
	if accessErr != nil {
		writeJerr(w, accessErr)
		return
	}
	if r.Method == http.MethodPost {
		var input struct {
			Body   json.RawMessage `json:"body"`
			Public *bool           `json:"public"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
			jiraError(w, http.StatusBadRequest, "Request body is invalid.")
			return
		}
		public := true
		if input.Public != nil {
			public = *input.Public
		}
		plainText, isText := decodeServiceText(input.Body)
		body := json.RawMessage(nil)
		if !isText {
			body = input.Body
		}
		if strings.TrimSpace(plainText) == "" && len(body) == 0 {
			jiraError(w, http.StatusBadRequest, "Comment body is required.")
			return
		}
		comment, err := h.Commands.AddServiceRequestComment(r.Context(), actorID, workspaceID, request.Issue.ID, body, plainText, public)
		if err != nil {
			jiraError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, h.serviceCommentBean(request, *comment))
		return
	}
	if commentID != "" {
		comment, err := h.Store.ServiceRequestComment(r.Context(), request.Issue.ID, commentID, canManage)
		if err != nil {
			jiraError(w, http.StatusNotFound, "Comment does not exist or is not visible.")
			return
		}
		attachments, err := h.Store.ServiceCommentAttachments(r.Context(), request.Issue.ID, comment.Comment.ID, canManage)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load comment attachments.")
			return
		}
		for _, attachment := range attachments {
			comment.Attachments = append(comment.Attachments, attachment.Attachment)
		}
		writeJSON(w, http.StatusOK, h.serviceCommentBean(request, *comment))
		return
	}
	comments, err := h.Store.ServiceRequestComments(r.Context(), request.Issue.ID, canManage)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load comments.")
		return
	}
	beans := make([]map[string]any, 0, len(comments))
	for _, comment := range comments {
		attachments, err := h.Store.ServiceCommentAttachments(r.Context(), request.Issue.ID, comment.Comment.ID, canManage)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load comment attachments.")
			return
		}
		for _, attachment := range attachments {
			comment.Attachments = append(comment.Attachments, attachment.Attachment)
		}
		beans = append(beans, h.serviceCommentBean(request, comment))
	}
	h.writeServicePage(w, r, beans)
}

func (h *Handler) serviceRequestStatus(w http.ResponseWriter, r *http.Request, workspaceID, issueIDOrKey string) {
	request, _, _, accessErr := h.serviceRequestAccess(r, workspaceID, issueIDOrKey)
	if accessErr != nil {
		writeJerr(w, accessErr)
		return
	}
	h.writeServicePage(w, r, []map[string]any{h.serviceStatusBean(request.Issue.Status, request.Issue.UpdatedAt)})
}

func serviceDuration(millis int64, friendly string) map[string]any {
	return map[string]any{"millis": millis, "friendly": friendly}
}

func serviceSLACycleBean(cycle models.ServiceSLACycle, ongoing bool) map[string]any {
	bean := map[string]any{
		"startTime": serviceDate(cycle.StartTime), "breachTime": serviceDate(cycle.BreachTime),
		"breached": cycle.Breached, "goalDuration": serviceDuration(cycle.GoalMillis, cycle.GoalLabel),
		"elapsedTime":   serviceDuration(cycle.ElapsedMillis, cycle.ElapsedLabel),
		"remainingTime": serviceDuration(cycle.RemainingMillis, cycle.RemainingLabel),
	}
	if ongoing {
		bean["paused"] = cycle.Paused
		bean["withinCalendarHours"] = cycle.WithinCalendarHours
	} else if cycle.StopTime != nil {
		bean["stopTime"] = serviceDate(*cycle.StopTime)
	}
	return bean
}

func (h *Handler) serviceSLABean(request *models.ServiceRequest, sla models.ServiceSLA) map[string]any {
	completed := make([]map[string]any, 0, len(sla.CompletedCycles))
	for _, cycle := range sla.CompletedCycles {
		completed = append(completed, serviceSLACycleBean(cycle, false))
	}
	bean := map[string]any{
		"id": sla.ID, "name": sla.Name, "completedCycles": completed, "slaDisplayFormat": "NEW_SLA_FORMAT",
		"_links": map[string]string{"self": h.BaseURL + "/rest/servicedeskapi/request/" + request.Issue.Key + "/sla/" + sla.ID},
	}
	if sla.OngoingCycle != nil {
		bean["ongoingCycle"] = serviceSLACycleBean(*sla.OngoingCycle, true)
	}
	return bean
}

func (h *Handler) serviceRequestSLA(w http.ResponseWriter, r *http.Request, workspaceID, issueIDOrKey, metricID string) {
	request, canManage, _, accessErr := h.serviceRequestAccess(r, workspaceID, issueIDOrKey)
	if accessErr != nil {
		writeJerr(w, accessErr)
		return
	}
	if !canManage {
		jiraError(w, http.StatusForbidden, "Service agent access is required to view SLA information.")
		return
	}
	slas, err := h.Store.ServiceSLAs(r.Context(), workspaceID, request.Issue.ID, time.Now().UTC())
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load SLA information.")
		return
	}
	beans := make([]map[string]any, 0, len(slas))
	for _, sla := range slas {
		if metricID != "" && sla.ID != metricID {
			continue
		}
		beans = append(beans, h.serviceSLABean(request, sla))
	}
	if metricID != "" {
		if len(beans) == 0 {
			jiraError(w, http.StatusNotFound, "SLA metric was not found.")
			return
		}
		writeJSON(w, http.StatusOK, beans[0])
		return
	}
	h.writeServicePage(w, r, beans)
}

func (h *Handler) availableServiceTransitions(r *http.Request, workspaceID, actorID string, request *models.ServiceRequest) ([]map[string]any, error) {
	wf, err := h.Store.WorkflowForProjectAndIssueType(r.Context(), request.Issue.ProjectID, request.Issue.IssueType.ID)
	if err != nil {
		return nil, err
	}
	evaluation := workflow.ContextForIssue(actorID, request.Issue)
	evaluation.IsAPI = true
	evaluation.StatusHistory, err = h.Store.IssueStatusHistory(r.Context(), workspaceID, request.Issue.ID)
	if err != nil {
		return nil, err
	}
	evaluation.Transitions, err = h.Store.IssueTransitionHistory(r.Context(), workspaceID, request.Issue.ID)
	if err != nil {
		return nil, err
	}
	evaluation.ParentStatus, evaluation.ChildStatuses, err = h.Store.IssueHierarchyStatuses(r.Context(), workspaceID, request.Issue.ID)
	if err != nil {
		return nil, err
	}
	evaluation.FormsAttached, evaluation.FormsSubmitted, err = h.Store.IssueFormState(r.Context(), workspaceID, request.Issue.ID)
	if err != nil {
		return nil, err
	}
	beans := make([]map[string]any, 0)
	for _, transition := range wf.AvailableFor(request.Issue.Status.ID, evaluation) {
		status, err := h.Store.StatusByIDForProject(r.Context(), transition.To, request.Issue.ProjectID)
		if err != nil {
			return nil, err
		}
		beans = append(beans, map[string]any{"id": transition.ID, "name": transition.Name, "to": map[string]any{"id": status.ID, "name": status.Name, "statusCategory": status.Category}})
	}
	return beans, nil
}

func (h *Handler) serviceRequestTransition(w http.ResponseWriter, r *http.Request, workspaceID, issueIDOrKey string) {
	request, canManage, actorID, accessErr := h.serviceRequestAccess(r, workspaceID, issueIDOrKey)
	if accessErr != nil {
		writeJerr(w, accessErr)
		return
	}
	if r.Method == http.MethodGet {
		beans, err := h.availableServiceTransitions(r, workspaceID, actorID, request)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load transitions.")
			return
		}
		h.writeServicePage(w, r, beans)
		return
	}
	var input struct {
		ID                string `json:"id"`
		AdditionalComment *struct {
			Body   string `json:"body"`
			Public *bool  `json:"public"`
		} `json:"additionalComment"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil || input.ID == "" {
		jiraError(w, http.StatusBadRequest, "A transition id is required.")
		return
	}
	if input.AdditionalComment != nil && input.AdditionalComment.Public != nil && !*input.AdditionalComment.Public && !canManage {
		jiraError(w, http.StatusBadRequest, "Customers may only add public comments.")
		return
	}
	updated, err := h.Commands.TransitionServiceRequest(r.Context(), actorID, workspaceID, request.Issue.ID, input.ID)
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.AdditionalComment != nil && strings.TrimSpace(input.AdditionalComment.Body) != "" {
		public := true
		if input.AdditionalComment.Public != nil {
			public = *input.AdditionalComment.Public
		}
		if _, err := h.Commands.AddServiceRequestComment(r.Context(), actorID, workspaceID, updated.Issue.ID, nil, input.AdditionalComment.Body, public); err != nil {
			jiraError(w, http.StatusInternalServerError, fmt.Sprintf("Request transitioned, but the additional comment could not be saved: %v", err))
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
