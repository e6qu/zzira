package api3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

func (h *Handler) serviceDeskRoute(w http.ResponseWriter, r *http.Request) {
	// Jira answers the instance information without credentials.
	if r.Method == http.MethodGet && strings.Trim(strings.TrimPrefix(r.URL.Path, "/rest/servicedeskapi"), "/") == "info" {
		writeJSON(w, http.StatusOK, map[string]any{"version": "5.17.0", "platformVersion": "1001.0.0-SNAPSHOT", "buildChangeSet": "zzira", "buildDate": serviceDate(time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)), "isLicensedForUse": true, "_links": map[string]string{"self": h.BaseURL + "/rest/servicedeskapi/info"}})
		return
	}
	workspaceID, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/rest/servicedeskapi"), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "customer" && r.Method == http.MethodPost:
		h.createServiceCustomer(w, r, workspaceID, actorID)
	case len(parts) == 2 && parts[0] == "customer" && parts[1] == "skip-permission-check" && r.Method == http.MethodPost:
		h.createServiceCustomer(w, r, workspaceID, actorID)
	case len(parts) == 4 && parts[0] == "customer" && parts[1] == "user" && parts[3] == "revoke-portal-only-access" && r.Method == http.MethodPut:
		h.revokeServiceCustomer(w, r, workspaceID, actorID, parts[2])
	case len(parts) == 1 && parts[0] == "organization" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		h.serviceOrganizations(w, r, workspaceID, actorID)
	case len(parts) == 2 && parts[0] == "organization" && (r.Method == http.MethodGet || r.Method == http.MethodDelete):
		h.serviceOrganization(w, r, workspaceID, actorID, parts[1])
	case len(parts) == 3 && parts[0] == "organization" && parts[2] == "property" && r.Method == http.MethodGet:
		h.serviceOrganizationPropertyKeys(w, r, workspaceID, actorID, parts[1])
	case len(parts) == 4 && parts[0] == "organization" && parts[2] == "property" && (r.Method == http.MethodGet || r.Method == http.MethodPut || r.Method == http.MethodDelete):
		h.serviceOrganizationProperty(w, r, workspaceID, actorID, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "organization" && parts[2] == "user" && (r.Method == http.MethodGet || r.Method == http.MethodPost || r.Method == http.MethodDelete):
		h.serviceOrganizationUsers(w, r, workspaceID, actorID, parts[1])
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
	case len(parts) == 1 && parts[0] == "servicedesk" && r.Method == http.MethodGet:
		desks, err := h.Store.ServiceDesks(r.Context(), workspaceID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load service desks.")
			return
		}
		// Only the service desks the caller has permission to access.
		accessible := make([]models.ServiceDesk, 0, len(desks))
		for _, desk := range desks {
			allowed, accessErr := h.serviceDeskAccess(r, workspaceID, desk.ID, actorID)
			if accessErr != nil {
				jiraError(w, http.StatusInternalServerError, "Could not authorize service desk access.")
				return
			}
			if allowed {
				accessible = append(accessible, desk)
			}
		}
		h.writeServicePage(w, r, serviceDeskBeans(h.BaseURL, accessible))
	case len(parts) == 2 && parts[0] == "servicedesk" && r.Method == http.MethodGet:
		desk, err := h.Store.ServiceDesk(r.Context(), workspaceID, parts[1])
		if err != nil {
			jiraError(w, http.StatusNotFound, "Service desk was not found.")
			return
		}
		if allowed, accessErr := h.serviceDeskAccess(r, workspaceID, desk.ID, actorID); accessErr != nil || !allowed {
			jiraError(w, http.StatusForbidden, "You do not have permission to access this service desk.")
			return
		}
		writeJSON(w, http.StatusOK, serviceDeskBean(h.BaseURL, *desk))
	case len(parts) == 1 && parts[0] == "requesttype" && r.Method == http.MethodGet:
		h.listServiceRequestTypes(w, r, workspaceID, "")
	case len(parts) == 3 && parts[0] == "servicedesk" && parts[2] == "requesttype" && r.Method == http.MethodGet:
		h.listServiceRequestTypes(w, r, workspaceID, parts[1])
	case len(parts) == 3 && parts[0] == "servicedesk" && parts[2] == "requesttype" && r.Method == http.MethodPost:
		if !h.serviceDeskAdminAccess(r, workspaceID, parts[1], actorID) {
			jiraError(w, http.StatusForbidden, "Service desk administrator access is required.")
			return
		}
		var input struct{ Name, Description, HelpText, IssueTypeID string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil || strings.TrimSpace(input.Name) == "" || len(input.Name) > 255 || len(input.Description) > 255 || len(input.HelpText) > 255 || input.IssueTypeID == "" {
			jiraError(w, http.StatusBadRequest, "name and issueTypeId are required and text fields accept at most 255 characters.")
			return
		}
		ids, idErr := h.issueTypeIDTranslator(r.Context(), workspaceID)
		if idErr != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		requestType, err := h.Store.CreateServiceRequestType(r.Context(), workspaceID, parts[1], strings.TrimSpace(input.Name), input.Description, input.HelpText, ids.toInternal(input.IssueTypeID))
		if err != nil {
			jiraError(w, http.StatusBadRequest, "Could not create request type.")
			return
		}
		writeJSON(w, http.StatusOK, h.serviceRequestTypeBean(r, workspaceID, actorID, h.wireServiceRequestType(r.Context(), workspaceID, *requestType)))
	case len(parts) == 3 && parts[0] == "servicedesk" && parts[2] == "customer" && (r.Method == http.MethodGet || r.Method == http.MethodPost || r.Method == http.MethodDelete):
		h.serviceDeskCustomers(w, r, workspaceID, actorID, parts[1])
	case len(parts) == 4 && parts[0] == "servicedesk" && parts[2] == "customer" && parts[3] == "invite" && r.Method == http.MethodPost:
		h.inviteServiceDeskCustomer(w, r, workspaceID, actorID, parts[1])
	case len(parts) == 4 && parts[0] == "servicedesk" && parts[2] == "customer" && parts[3] == "skip-permission-check" && r.Method == http.MethodPost:
		h.serviceDeskCustomers(w, r, workspaceID, actorID, parts[1])
	case len(parts) == 3 && parts[0] == "servicedesk" && parts[2] == "organization" && (r.Method == http.MethodGet || r.Method == http.MethodPost || r.Method == http.MethodDelete):
		h.serviceDeskOrganizations(w, r, workspaceID, actorID, parts[1])
	case len(parts) == 2 && (parts[0] == "assets" || parts[0] == "insight") && parts[1] == "workspace" && r.Method == http.MethodGet:
		h.serviceAssetsWorkspaces(w, r, workspaceID)
	case len(parts) == 2 && parts[0] == "knowledgebase" && parts[1] == "article" && r.Method == http.MethodGet:
		h.serviceKnowledgeArticles(w, r, workspaceID, actorID, "")
	case len(parts) == 4 && parts[0] == "knowledgebase" && parts[1] == "article" && parts[2] == "view" && r.Method == http.MethodGet:
		h.serviceKnowledgeArticle(w, r, workspaceID, actorID, parts[3])
	case len(parts) == 4 && parts[0] == "servicedesk" && parts[2] == "knowledgebase" && parts[3] == "article" && r.Method == http.MethodGet:
		h.serviceKnowledgeArticles(w, r, workspaceID, actorID, parts[1])
	case len(parts) == 3 && parts[0] == "servicedesk" && parts[2] == "requesttypegroup" && r.Method == http.MethodGet:
		h.serviceRequestTypeGroups(w, r, workspaceID, actorID, parts[1])
	case len(parts) == 5 && parts[0] == "servicedesk" && parts[2] == "requesttype" && parts[3] == "permissions" && parts[4] == "check" && r.Method == http.MethodPost:
		h.serviceRequestTypePermissions(w, r, workspaceID, actorID, parts[1])
	case len(parts) == 5 && parts[0] == "servicedesk" && parts[2] == "requesttype" && parts[4] == "property" && r.Method == http.MethodGet:
		h.serviceRequestTypePropertyKeys(w, r, workspaceID, actorID, parts[1], parts[3])
	case len(parts) == 6 && parts[0] == "servicedesk" && parts[2] == "requesttype" && parts[4] == "property" && (r.Method == http.MethodGet || r.Method == http.MethodPut || r.Method == http.MethodDelete):
		h.serviceRequestTypeProperty(w, r, workspaceID, actorID, parts[1], parts[3], parts[5])
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
			fields, err := h.Store.ServiceRequestTypeFields(r.Context(), workspaceID, parts[1], parts[3])
			if err != nil {
				jiraError(w, http.StatusInternalServerError, "Could not load request type fields.")
				return
			}
			form, err := h.serviceRequestTypeFields(r, workspaceID, actorID, parts[1], fields)
			if err != nil {
				jiraError(w, http.StatusInternalServerError, "Could not load request type fields.")
				return
			}
			writeJSON(w, http.StatusOK, form)
			return
		}
		if len(parts) == 4 && r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, h.serviceRequestTypeBean(r, workspaceID, actorID, h.wireServiceRequestType(r.Context(), workspaceID, *requestType)))
			return
		}
		if len(parts) == 4 && r.Method == http.MethodDelete {
			if !h.serviceDeskAdminAccess(r, workspaceID, parts[1], actorID) {
				jiraError(w, http.StatusForbidden, "Service desk administrator access is required.")
				return
			}
			if err := h.Store.DeleteServiceRequestType(r.Context(), workspaceID, actorID, parts[1], parts[3]); err != nil {
				if errors.Is(err, store.ErrServiceRequestTypeNotFound) {
					jiraError(w, http.StatusNotFound, "The service desk or request type does not exist.")
					return
				}
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
	// Each request carries only the fields the queue is configured to show.
	beans := make([]map[string]any, 0, len(requests))
	for _, request := range requests {
		bean := projectSearchIssue(h.issueBean(request.Issue), queue.Fields, false)
		if _, projected := bean["fields"]; !projected {
			full := h.issueBean(request.Issue)
			bean["key"], bean["self"], bean["fields"] = full["key"], full["self"], map[string]any{}
		}
		beans = append(beans, bean)
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

func (h *Handler) validateServiceRequestBody(r *http.Request, workspaceID string, input createServiceRequestBody) (map[string]string, []models.ServiceRequestTypeField, error) {
	errors := map[string]string{}
	configured := []models.ServiceRequestTypeField{}
	if len(input.Form) > 0 && string(input.Form) != "null" {
		errors["form"] = "Forms are not configured for this request type."
	}
	for _, participant := range input.RequestParticipants {
		if _, err := h.Store.ServiceCustomer(r.Context(), workspaceID, participant); err != nil {
			errors["requestParticipants"] = "Every request participant must be an active service customer."
			break
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
		} else {
			fields, err := h.Store.ServiceRequestTypeFields(r.Context(), workspaceID, input.ServiceDeskID, input.RequestTypeID)
			if err != nil {
				return nil, nil, err
			}
			configured = fields
		}
	}
	allowed := make(map[string]models.ServiceRequestTypeField, len(configured))
	for _, field := range configured {
		// A hidden field takes its preset value, never a submitted one.
		if field.Hidden {
			continue
		}
		allowed[field.ID] = field
		raw, present := input.RequestFieldValues[field.ID]
		if field.Required {
			text, isText := decodeServiceText(raw)
			if !present || len(raw) == 0 || string(raw) == "null" || (isText && strings.TrimSpace(text) == "") {
				errors[field.ID] = field.Name + " is required."
			}
		}
		if field.Custom && present && len(raw) > 0 && string(raw) != "null" {
			if message := serviceRequestFieldValueError(field, raw); message != "" {
				errors[field.ID] = message
			}
		}
	}
	for fieldID := range input.RequestFieldValues {
		if _, ok := allowed[fieldID]; !ok {
			errors[fieldID] = "This field is not configured for the request type."
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
	return errors, configured, nil
}

func serviceRequestFieldValueError(field models.ServiceRequestTypeField, raw json.RawMessage) string {
	if len(raw) > 64<<10 {
		return field.Name + " accepts at most 64 KiB."
	}
	switch field.Type {
	case models.CustomFieldText:
		if _, ok := decodeServiceText(raw); !ok {
			return field.Name + " must be text."
		}
	case models.CustomFieldNumber:
		var number json.Number
		if json.Unmarshal(raw, &number) != nil {
			return field.Name + " must be a number."
		}
		value, err := strconv.ParseFloat(number.String(), 64)
		if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
			return field.Name + " must be a finite number."
		}
	case models.CustomFieldDatetime:
		value, ok := decodeServiceText(raw)
		if !ok {
			return field.Name + " must be a date and time."
		}
		valid := value == ""
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02T15:04:05"} {
			if _, err := time.Parse(layout, value); err == nil {
				valid = true
				break
			}
		}
		if !valid {
			return field.Name + " must be an RFC 3339 or local date-time."
		}
	}
	return ""
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
	fieldErrors, configuredFields, err := h.validateServiceRequestBody(r, workspaceID, input)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not validate request type fields.")
		return
	}
	if validateOnly {
		// Jira's RequestValidationResultDTO: field errors are a list, and the
		// summary and reason key are null for a valid payload.
		fields := make([]string, 0, len(fieldErrors))
		for field := range fieldErrors {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		errors := make([]map[string]string, 0, len(fields))
		for _, field := range fields {
			errors = append(errors, map[string]string{"field": field, "message": fieldErrors[field]})
		}
		result := map[string]any{"valid": len(fieldErrors) == 0, "fieldErrors": errors, "formErrors": []any{}, "errorMessages": []string{}, "errorMessage": nil, "reasonKey": nil}
		if len(fieldErrors) > 0 {
			result["errorMessage"], result["reasonKey"] = "Request validation failed.", "FIELD_VALIDATION_FAILED"
		}
		writeJSON(w, http.StatusOK, result)
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
	customFields := map[string]json.RawMessage{}
	for _, field := range configuredFields {
		value, ok := input.RequestFieldValues[field.ID]
		if field.Hidden {
			value, ok = field.PresetValue, len(field.PresetValue) > 0 && string(field.PresetValue) != "null"
			if ok && field.ID == "description" {
				if input.RequestFieldValues == nil {
					input.RequestFieldValues = map[string]json.RawMessage{}
				}
				input.RequestFieldValues["description"] = value
			}
		}
		if field.Custom && ok {
			customFields[field.ID] = value
		}
	}
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
		Summary: summary, Description: description, DescriptionADF: descriptionADF, ParticipantIDs: participantIDs, Fields: customFields,
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
	query := r.URL.Query()
	filter := store.ServiceRequestListFilter{ViewerID: actorID, ServiceDeskID: query.Get("serviceDeskId"), RequestTypeID: query.Get("requestTypeId"), SearchTerm: query.Get("searchTerm")}
	ownership := []string{}
	for _, value := range query["requestOwnership"] {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				ownership = append(ownership, strings.ToUpper(part))
			}
		}
	}
	// Without an ownership filter Jira lists owned, participated and
	// organization requests.
	if len(ownership) == 0 {
		ownership = []string{"OWNED_REQUESTS", "PARTICIPATED_REQUESTS", "ALL_ORGANIZATIONS"}
	}
	for _, value := range ownership {
		switch value {
		case "OWNED_REQUESTS":
			filter.Owned = true
		case "PARTICIPATED_REQUESTS":
			filter.Participated = true
		case "ORGANIZATION", "ALL_ORGANIZATIONS":
			filter.Organizations = true
		case "APPROVER":
			filter.Approver = true
		case "ALL_REQUESTS":
			filter.AllRequests = true
		default:
			jiraError(w, http.StatusBadRequest, "requestOwnership must be OWNED_REQUESTS, PARTICIPATED_REQUESTS, ORGANIZATION, ALL_ORGANIZATIONS, APPROVER or ALL_REQUESTS.")
			return
		}
	}
	if organizationID := query.Get("organizationId"); organizationID != "" {
		if !slices.Contains(ownership, "ORGANIZATION") {
			jiraError(w, http.StatusBadRequest, "organizationId is valid only with requestOwnership=ORGANIZATION.")
			return
		}
		filter.OrganizationID = organizationID
	} else if slices.Contains(ownership, "ORGANIZATION") && !slices.Contains(ownership, "ALL_ORGANIZATIONS") {
		jiraError(w, http.StatusBadRequest, "requestOwnership=ORGANIZATION needs an organizationId.")
		return
	}
	switch approval := strings.ToUpper(query.Get("approvalStatus")); approval {
	case "":
	case "MY_PENDING_APPROVAL", "MY_HISTORY_APPROVAL":
		if !filter.Approver {
			jiraError(w, http.StatusBadRequest, "approvalStatus is valid only with requestOwnership=APPROVER.")
			return
		}
		filter.ApprovalStatus = approval
	default:
		jiraError(w, http.StatusBadRequest, "approvalStatus must be MY_PENDING_APPROVAL or MY_HISTORY_APPROVAL.")
		return
	}
	switch status := strings.ToUpper(query.Get("requestStatus")); status {
	case "", "ALL_REQUESTS":
	case "OPEN_REQUESTS", "CLOSED_REQUESTS":
		filter.RequestStatus = status
	default:
		jiraError(w, http.StatusBadRequest, "requestStatus must be OPEN_REQUESTS, CLOSED_REQUESTS or ALL_REQUESTS.")
		return
	}
	if filter.RequestTypeID != "" && filter.ServiceDeskID == "" {
		jiraError(w, http.StatusBadRequest, "requestTypeId needs the serviceDeskId of its service desk.")
		return
	}
	if filter.ServiceDeskID != "" {
		if _, err := h.Store.ServiceDesk(r.Context(), workspaceID, filter.ServiceDeskID); err != nil {
			jiraError(w, http.StatusNotFound, "The service desk does not exist.")
			return
		}
		if filter.RequestTypeID != "" {
			if _, err := h.Store.ServiceRequestType(r.Context(), workspaceID, filter.ServiceDeskID, filter.RequestTypeID); err != nil {
				jiraError(w, http.StatusNotFound, "The service desk does not support the request type.")
				return
			}
		}
	}
	if filter.AllRequests {
		admin, err := h.Store.IsAdmin(r.Context(), workspaceID, actorID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load customer requests.")
			return
		}
		filter.SiteAdmin = admin
		if !admin {
			agent, accessErr := h.Store.IsAnyServiceAgent(r.Context(), workspaceID, actorID)
			if accessErr != nil {
				jiraError(w, http.StatusInternalServerError, "Could not authorize service access.")
				return
			}
			if !agent {
				jiraError(w, http.StatusForbidden, "Service agent access is required for all requests.")
				return
			}
		}
	}
	requests, err := h.Store.ServiceRequestList(r.Context(), workspaceID, filter)
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
	writeJSON(w, http.StatusOK, map[string]any{"start": start, "limit": limit, "size": end - start, "isLastPage": end == len(values), "values": values[start:end], "_expands": []any{}, "_links": h.servicePageLinks(r, start, limit, end < len(values))})
}

// servicePageLinks are Jira Service Management's page links: the API base, the
// site's context path (empty at the root), the page itself, and the pages
// before and after it when there are any.
func (h *Handler) servicePageLinks(r *http.Request, start, limit int, more bool) map[string]string {
	page := func(from int) string {
		query := r.URL.Query()
		query.Set("start", strconv.Itoa(from))
		query.Set("limit", strconv.Itoa(limit))
		return h.BaseURL + r.URL.Path + "?" + query.Encode()
	}
	self := h.BaseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		self += "?" + r.URL.RawQuery
	}
	links := map[string]string{"base": h.BaseURL + "/rest/servicedeskapi", "context": "", "self": self}
	if more {
		links["next"] = page(start + limit)
	}
	if start > 0 {
		links["prev"] = page(max(0, start-limit))
	}
	return links
}

func (h *Handler) listServiceRequestTypes(w http.ResponseWriter, r *http.Request, workspaceID, serviceDeskID string) {
	_, actorID, authErr := h.authWorkspace(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	query := r.URL.Query()
	search := query.Get("searchQuery")
	values, err := h.Store.ServiceRequestTypes(r.Context(), workspaceID, serviceDeskID, search)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load request types.")
		return
	}
	includeHidden := false
	if raw := query.Get("includeHiddenRequestTypesInSearch"); raw != "" {
		if includeHidden, err = strconv.ParseBool(raw); err != nil {
			jiraError(w, http.StatusBadRequest, "includeHiddenRequestTypesInSearch must be true or false.")
			return
		}
	}
	restriction := strings.ToUpper(query.Get("restrictionStatus"))
	if restriction != "" && restriction != "OPEN" && restriction != "RESTRICTED" {
		jiraError(w, http.StatusBadRequest, "restrictionStatus must be OPEN or RESTRICTED.")
		return
	}
	desks := map[string]bool{}
	for _, id := range query["serviceDeskId"] {
		for _, part := range strings.Split(id, ",") {
			if part = strings.TrimSpace(part); part != "" {
				desks[part] = true
			}
		}
	}
	groupID := query.Get("groupId")
	filtered := values[:0]
	for _, requestType := range values {
		// A request type in no group is hidden from the portal, and a search
		// leaves hidden types out unless asked for them. Every zzira request
		// type is open to the desk's customers.
		if (search != "" && !includeHidden && len(requestType.GroupIDs) == 0) || restriction == "RESTRICTED" ||
			(len(desks) > 0 && !desks[requestType.ServiceDeskID]) || (groupID != "" && !slices.Contains(requestType.GroupIDs, groupID)) {
			continue
		}
		filtered = append(filtered, requestType)
	}
	values = filtered
	beans := make([]map[string]any, 0, len(values))
	for _, requestType := range values {
		beans = append(beans, h.serviceRequestTypeBean(r, workspaceID, actorID, h.wireServiceRequestType(r.Context(), workspaceID, requestType)))
	}
	h.writeServicePage(w, r, beans)
}

// serviceRequestTypeBean describes a request type to a caller: its headset
// icon, whether they can raise requests with it, and, when expanded, the
// form fields a request of the type takes.
func (h *Handler) serviceRequestTypeBean(r *http.Request, workspaceID, actorID string, requestType models.ServiceRequestType) map[string]any {
	iconID, _ := store.DefaultSystemAvatar("SD_REQTYPE")
	icon := strconv.FormatInt(iconID, 10)
	view := h.BaseURL + "/rest/api/3/universal_avatar/view/type/SD_REQTYPE/avatar/" + icon
	canCreate, err := h.Store.CanCreateServiceRequest(r.Context(), workspaceID, requestType.ServiceDeskID, actorID)
	if err != nil {
		canCreate = false
	}
	bean := map[string]any{
		"id": requestType.ID, "serviceDeskId": requestType.ServiceDeskID, "portalId": requestType.ServiceDeskID, "name": requestType.Name,
		"description": requestType.Description, "helpText": requestType.HelpText, "issueTypeId": requestType.IssueTypeID, "groupIds": requestType.GroupIDs,
		"canCreateRequest": canCreate, "restrictionStatus": "OPEN", "practice": "service_desk",
		"icon": map[string]any{"id": icon, "_links": map[string]any{"iconUrls": map[string]string{
			"48x48": view + "?size=large", "32x32": view + "?size=medium", "24x24": view + "?size=small", "16x16": view + "?size=xsmall",
		}}},
		"_expands": []string{"field"},
		"_links":   map[string]string{"self": h.BaseURL + "/rest/servicedeskapi/servicedesk/" + requestType.ServiceDeskID + "/requesttype/" + requestType.ID},
	}
	expandFields := false
	for _, value := range r.URL.Query()["expand"] {
		for _, part := range strings.Split(value, ",") {
			expandFields = expandFields || strings.TrimSpace(part) == "field"
		}
	}
	if expandFields {
		if fields, err := h.Store.ServiceRequestTypeFields(r.Context(), workspaceID, requestType.ServiceDeskID, requestType.ID); err == nil {
			if form, formErr := h.serviceRequestTypeFields(r, workspaceID, actorID, requestType.ServiceDeskID, fields); formErr == nil {
				bean["fields"] = form
				bean["_expands"] = []string{}
			}
		}
	}
	return bean
}

// serviceRequestFieldSchema describes a request type field's value the way
// Jira's field metadata does.
func serviceRequestFieldSchema(field models.ServiceRequestTypeField) map[string]any {
	if !field.Custom {
		return map[string]any{"type": "string", "system": field.ID}
	}
	types := map[string][2]string{
		models.CustomFieldText: {"string", "textfield"}, models.CustomFieldNumber: {"number", "float"},
		models.CustomFieldDatetime: {"datetime", "datetime"}, models.CustomFieldDate: {"date", "datepicker"},
		models.CustomFieldURL: {"string", "url"}, models.CustomFieldSelect: {"option", "select"},
		models.CustomFieldMultiSelect: {"array", "multiselect"}, models.CustomFieldCascadingSelect: {"option-with-child", "cascadingselect"},
		models.CustomFieldUser: {"user", "userpicker"}, models.CustomFieldMultiUser: {"array", "multiuserpicker"},
		models.CustomFieldGroup: {"group", "grouppicker"}, models.CustomFieldMultiGroup: {"array", "multigrouppicker"},
		models.CustomFieldLabels: {"array", "labels"}, models.CustomFieldProject: {"project", "project"},
		models.CustomFieldVersion: {"version", "version"}, models.CustomFieldMultiVersion: {"array", "multiversion"},
	}
	kind, ok := types[field.Type]
	if !ok {
		kind = [2]string{"string", field.Type}
	}
	schema := map[string]any{"type": kind[0], "custom": "com.atlassian.jira.plugin.system.customfieldtypes:" + kind[1]}
	if id, err := strconv.ParseInt(strings.TrimPrefix(field.ID, "customfield_"), 10, 64); err == nil {
		schema["customId"] = id
	}
	switch field.Type {
	case models.CustomFieldMultiSelect:
		schema["items"] = "option"
	case models.CustomFieldMultiUser:
		schema["items"] = "user"
	case models.CustomFieldMultiGroup:
		schema["items"] = "group"
	case models.CustomFieldLabels:
		schema["items"] = "string"
	case models.CustomFieldMultiVersion:
		schema["items"] = "version"
	}
	return schema
}

// serviceRequestTypeFields describes a request type's form to the caller:
// agents may raise requests for customers and add participants, customers
// may not, and only the desk's administrators asking for hiddenFields see hidden fields
// and their preset values.
func (h *Handler) serviceRequestTypeFields(r *http.Request, workspaceID, actorID, serviceDeskID string, fields []models.ServiceRequestTypeField) (map[string]any, error) {
	agent, err := h.Store.IsServiceAgent(r.Context(), workspaceID, serviceDeskID, actorID)
	if err != nil {
		return nil, err
	}
	admin, err := h.Store.IsServiceDeskAdmin(r.Context(), workspaceID, serviceDeskID, actorID)
	if err != nil {
		return nil, err
	}
	showHidden := false
	for _, value := range r.URL.Query()["expand"] {
		for _, part := range strings.Split(value, ",") {
			showHidden = showHidden || strings.TrimSpace(part) == "hiddenFields"
		}
	}
	showHidden = showHidden && admin
	beans := make([]map[string]any, 0, len(fields))
	for _, field := range fields {
		if field.Hidden && !showHidden {
			continue
		}
		description := field.HelpText
		if description == "" {
			description = field.Description
		}
		validValues := []map[string]any{}
		if field.Type == models.CustomFieldSelect || field.Type == models.CustomFieldMultiSelect || field.Type == models.CustomFieldCascadingSelect {
			options, err := h.Store.ServiceRequestFieldOptions(r.Context(), workspaceID, serviceDeskID, field.ID)
			if err != nil {
				return nil, err
			}
			for _, option := range options {
				children := []map[string]any{}
				for _, child := range option.Children {
					children = append(children, map[string]any{"value": child.ID, "label": child.Value, "children": []any{}})
				}
				validValues = append(validValues, map[string]any{"value": option.ID, "label": option.Value, "children": children})
			}
		}
		presetValues := []string{}
		if field.Hidden && len(field.PresetValue) > 0 {
			var text string
			if json.Unmarshal(field.PresetValue, &text) == nil {
				presetValues = append(presetValues, text)
			} else {
				presetValues = append(presetValues, string(field.PresetValue))
			}
		}
		beans = append(beans, map[string]any{"fieldId": field.ID, "name": field.Name, "description": description, "required": field.Required, "visible": !field.Hidden,
			"defaultValues": []any{}, "presetValues": presetValues, "validValues": validValues, "jiraSchema": serviceRequestFieldSchema(field)})
	}
	return map[string]any{"canAddRequestParticipants": agent, "canRaiseOnBehalfOf": agent, "requestTypeFields": beans}, nil
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

// serviceUserBean is Jira Service Management's UserDTO. The deprecated key and
// name are not sent, and avatars appear only under its links.
func (h *Handler) serviceUserBean(user *models.User) map[string]any {
	if user == nil {
		return nil
	}
	self := h.BaseURL + "/rest/api/3/user?accountId=" + url.QueryEscape(user.ID)
	avatar := h.BaseURL + "/static/img/avatar-default.svg"
	return map[string]any{
		"accountId": user.ID, "emailAddress": user.Email, "displayName": user.DisplayName, "active": user.Active, "timeZone": user.TimeZone,
		"_links": map[string]any{
			"self": self, "jiraRest": self,
			"avatarUrls": map[string]string{"48x48": avatar, "24x24": avatar, "16x16": avatar, "32x32": avatar},
		},
	}
}

func (h *Handler) serviceStatusBean(status models.Status, changed string) map[string]any {
	return serviceStatusEntryBean(status, parseServiceDate(changed))
}

// serviceStatusEntryBean is a status a request attained, with Jira's status
// category key.
func serviceStatusEntryBean(status models.Status, at time.Time) map[string]any {
	category := map[string]string{"new": "NEW", "indeterminate": "INDETERMINATE", "done": "DONE"}[strings.ToLower(status.Category)]
	if category == "" {
		category = "UNDEFINED"
	}
	return map[string]any{"status": status.Name, "statusCategory": category, "statusDate": serviceDate(at)}
}

// servicePagedValues is Jira Service Management's page of every value.
func servicePagedValues[T any](values []T) map[string]any {
	return map[string]any{"start": 0, "limit": len(values), "size": len(values), "isLastPage": true, "values": values, "_expands": []any{}}
}

func (h *Handler) serviceRequestBean(r *http.Request, workspaceID, viewerID string, request *models.ServiceRequest) (map[string]any, error) {
	expand := map[string]bool{}
	for _, value := range r.URL.Query()["expand"] {
		for _, part := range strings.Split(value, ",") {
			expand[strings.TrimSpace(part)] = true
		}
	}
	canManage, err := h.Store.CanManageServiceRequest(r.Context(), workspaceID, viewerID, request.Issue.ID)
	if err != nil {
		return nil, err
	}
	fields := []map[string]any{
		{"fieldId": "summary", "label": "Summary", "value": request.Issue.Summary, "renderedValue": request.Issue.Summary},
		{"fieldId": "description", "label": "Description", "value": request.Issue.Description, "renderedValue": adf.ToHTML(request.Issue.Description)},
	}
	configuredFields, err := h.Store.ServiceRequestTypeFields(r.Context(), workspaceID, request.ServiceDesk.ID, request.RequestType.ID)
	if err != nil {
		return nil, err
	}
	for _, field := range configuredFields {
		// Hidden fields are not part of what the request shows.
		if !field.Custom || field.Hidden {
			continue
		}
		var value any
		if raw, ok := request.Issue.Fields[field.ID]; ok && string(raw) != "null" {
			if err := json.Unmarshal(raw, &value); err != nil {
				return nil, err
			}
		}
		fields = append(fields, map[string]any{"fieldId": field.ID, "label": field.Name, "value": value, "renderedValue": value})
	}
	bean := map[string]any{
		"issueId": jiraIssueID(request.Issue), "issueKey": request.Issue.Key, "summary": request.Issue.Summary,
		"serviceDeskId": request.ServiceDesk.ID, "requestTypeId": request.RequestType.ID,
		"reporter": h.serviceUserBean(request.Customer), "requestFieldValues": fields,
		"currentStatus": h.serviceStatusBean(request.Issue.Status, request.Issue.UpdatedAt),
		"createdDate":   serviceDate(request.CreatedAt),
		"_links": map[string]string{
			"self": h.BaseURL + "/rest/servicedeskapi/request/" + request.Issue.Key,
			"web":  h.BaseURL + "/service/requests/" + request.Issue.Key,
		},
	}
	unexpanded := []string{}
	if expand["serviceDesk"] {
		bean["serviceDesk"] = serviceDeskBean(h.BaseURL, request.ServiceDesk)
	} else {
		unexpanded = append(unexpanded, "serviceDesk")
	}
	if expand["requestType"] {
		bean["requestType"] = h.serviceRequestTypeBean(r, workspaceID, viewerID, h.wireServiceRequestType(r.Context(), workspaceID, request.RequestType))
	} else {
		unexpanded = append(unexpanded, "requestType")
	}
	participants, err := h.Store.ServiceRequestParticipants(r.Context(), request.Issue.ID)
	if err != nil {
		return nil, err
	}
	if expand["participant"] {
		participantBeans := make([]map[string]any, 0, len(participants))
		for _, participant := range participants {
			participantBeans = append(participantBeans, h.serviceUserBean(participant))
		}
		bean["participants"] = servicePagedValues(participantBeans)
	} else {
		unexpanded = append(unexpanded, "participant")
	}
	if expand["sla"] {
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
		bean["sla"] = servicePagedValues(slaBeans)
	} else {
		unexpanded = append(unexpanded, "sla")
	}
	if expand["status"] {
		history, err := h.Store.ServiceRequestStatusHistory(r.Context(), workspaceID, request.Issue.ID)
		if err != nil {
			return nil, err
		}
		statusBeans := make([]map[string]any, 0, len(history))
		for _, entry := range history {
			statusBeans = append(statusBeans, serviceStatusEntryBean(entry.Status, entry.At))
		}
		bean["status"] = servicePagedValues(statusBeans)
	} else {
		unexpanded = append(unexpanded, "status")
	}
	if expand["attachment"] {
		attachments, err := h.Store.ServiceRequestAttachments(r.Context(), request.Issue.ID, canManage)
		if err != nil {
			return nil, err
		}
		attachmentBeans := make([]map[string]any, 0, len(attachments))
		for _, attachment := range attachments {
			attachmentBeans = append(attachmentBeans, h.serviceAttachmentBean(request, attachment.Attachment))
		}
		bean["attachments"] = servicePagedValues(attachmentBeans)
	} else {
		unexpanded = append(unexpanded, "attachment")
	}
	if expand["action"] {
		// People who can see a request comment on it and attach files; its
		// reporter and agents manage who participates.
		reporter := request.Customer != nil && request.Customer.ID == viewerID
		bean["actions"] = map[string]any{
			"addAttachment": map[string]bool{"allowed": true}, "addComment": map[string]bool{"allowed": true},
			"addParticipant": map[string]bool{"allowed": canManage || reporter}, "removeParticipant": map[string]bool{"allowed": canManage || reporter},
		}
	} else {
		unexpanded = append(unexpanded, "action")
	}
	if expand["comment"] {
		comments, err := h.Store.ServiceRequestComments(r.Context(), request.Issue.ID, canManage)
		if err != nil {
			return nil, err
		}
		commentBeans := make([]map[string]any, 0, len(comments))
		for _, comment := range comments {
			if expand["comment.attachment"] {
				attachments, err := h.Store.ServiceCommentAttachments(r.Context(), request.Issue.ID, comment.Comment.ID, canManage)
				if err != nil {
					return nil, err
				}
				for _, attachment := range attachments {
					comment.Attachments = append(comment.Attachments, attachment.Attachment)
				}
			}
			commentBean := h.serviceCommentBean(request, comment)
			if !expand["comment.attachment"] {
				delete(commentBean, "attachments")
			}
			if !expand["comment.renderedBody"] {
				delete(commentBean, "renderedBody")
			}
			commentBeans = append(commentBeans, commentBean)
		}
		bean["comments"] = servicePagedValues(commentBeans)
	} else {
		unexpanded = append(unexpanded, "comment")
	}
	bean["_expands"] = unexpanded
	return bean, nil
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
		"id": strconv.FormatInt(comment.Comment.JiraID, 10), "body": serviceADFText(comment.Comment.Body), "renderedBody": adf.ToHTML(comment.Comment.Body),
		"public": comment.Public, "author": h.serviceUserBean(&models.User{ID: comment.Comment.AuthorID, DisplayName: comment.Comment.AuthorName, Active: true, AccountType: "atlassian"}),
		"created": serviceDate(parseServiceDate(comment.Comment.Created)), "attachments": map[string]any{"start": 0, "limit": 50, "size": len(attachments), "isLastPage": true, "values": attachments}, "_expands": []any{},
		"_links": map[string]string{"self": h.BaseURL + "/rest/servicedeskapi/request/" + request.Issue.Key + "/comment/" + strconv.FormatInt(comment.Comment.JiraID, 10)},
	}
}

// serviceCommentExpansions reads which optional parts of request comments the
// caller asked for: attachment and renderedBody.
func serviceCommentExpansions(r *http.Request) map[string]bool {
	expand := map[string]bool{}
	for _, value := range r.URL.Query()["expand"] {
		for _, part := range strings.Split(value, ",") {
			expand[strings.TrimSpace(part)] = true
		}
	}
	return expand
}

// expandedServiceComment keeps a comment's attachments and rendered body only
// when expanded, listing the others in _expands.
func (h *Handler) expandedServiceComment(r *http.Request, request *models.ServiceRequest, comment models.ServiceRequestComment, expand map[string]bool, canManage bool) (map[string]any, error) {
	if expand["attachment"] {
		attachments, err := h.Store.ServiceCommentAttachments(r.Context(), request.Issue.ID, comment.Comment.ID, canManage)
		if err != nil {
			return nil, err
		}
		for _, attachment := range attachments {
			comment.Attachments = append(comment.Attachments, attachment.Attachment)
		}
	}
	bean := h.serviceCommentBean(request, comment)
	unexpanded := []string{}
	if !expand["attachment"] {
		delete(bean, "attachments")
		unexpanded = append(unexpanded, "attachment")
	}
	if !expand["renderedBody"] {
		delete(bean, "renderedBody")
		unexpanded = append(unexpanded, "renderedBody")
	}
	bean["_expands"] = unexpanded
	return bean, nil
}

func (h *Handler) serviceRequestComments(w http.ResponseWriter, r *http.Request, workspaceID, issueIDOrKey, commentID string) {
	request, canManage, actorID, accessErr := h.serviceRequestAccess(r, workspaceID, issueIDOrKey)
	if accessErr != nil {
		writeJerr(w, accessErr)
		return
	}
	expand := serviceCommentExpansions(r)
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
		bean, err := h.expandedServiceComment(r, request, *comment, expand, canManage)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load the comment.")
			return
		}
		writeJSON(w, http.StatusCreated, bean)
		return
	}
	if commentID != "" {
		comment, err := h.Store.ServiceRequestComment(r.Context(), request.Issue.ID, commentID, canManage)
		if err != nil {
			jiraError(w, http.StatusNotFound, "Comment does not exist or is not visible.")
			return
		}
		bean, err := h.expandedServiceComment(r, request, *comment, expand, canManage)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load comment attachments.")
			return
		}
		writeJSON(w, http.StatusOK, bean)
		return
	}
	// public and internal each default to true; customers only ever see
	// public comments.
	include := map[string]bool{"public": true, "internal": true}
	for name := range include {
		if raw := r.URL.Query().Get(name); raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				jiraError(w, http.StatusBadRequest, name+" must be true or false.")
				return
			}
			include[name] = value
		}
	}
	comments, err := h.Store.ServiceRequestComments(r.Context(), request.Issue.ID, canManage)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load comments.")
		return
	}
	beans := make([]map[string]any, 0, len(comments))
	for _, comment := range comments {
		if (comment.Public && !include["public"]) || (!comment.Public && !include["internal"]) {
			continue
		}
		bean, err := h.expandedServiceComment(r, request, comment, expand, canManage)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "Could not load comment attachments.")
			return
		}
		beans = append(beans, bean)
	}
	h.writeServicePage(w, r, beans)
}

func (h *Handler) serviceRequestStatus(w http.ResponseWriter, r *http.Request, workspaceID, issueIDOrKey string) {
	request, _, _, accessErr := h.serviceRequestAccess(r, workspaceID, issueIDOrKey)
	if accessErr != nil {
		writeJerr(w, accessErr)
		return
	}
	history, err := h.Store.ServiceRequestStatusHistory(r.Context(), workspaceID, request.Issue.ID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "Could not load the request's statuses.")
		return
	}
	// The chronology lists the current status first.
	beans := make([]map[string]any, 0, len(history))
	for index := len(history) - 1; index >= 0; index-- {
		beans = append(beans, serviceStatusEntryBean(history[index].Status, history[index].At))
	}
	h.writeServicePage(w, r, beans)
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
	evaluation.Approvals, err = h.Store.IssueApprovalDecisions(r.Context(), request.Issue.ID)
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
		beans = append(beans, map[string]any{"id": transition.ID, "name": transition.Name, "to": map[string]any{"id": statusWireID(status), "name": status.Name, "statusCategory": status.Category}})
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
	// Everything the comment could be refused for is checked before the
	// request moves, so a refused comment never leaves a transition behind.
	if input.AdditionalComment != nil && utf8.RuneCountInString(input.AdditionalComment.Body) > 32767 {
		jiraError(w, http.StatusBadRequest, "The comment is too long.")
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

// wireServiceRequestType gives a request type the issue type id clients know.
// The request type is a copy, so the stored value is untouched.
func (h *Handler) wireServiceRequestType(ctx context.Context, workspaceID string, requestType models.ServiceRequestType) models.ServiceRequestType {
	if ids, err := h.issueTypeIDTranslator(ctx, workspaceID); err == nil {
		requestType.IssueTypeID = ids.toWire(requestType.IssueTypeID)
	}
	return requestType
}
