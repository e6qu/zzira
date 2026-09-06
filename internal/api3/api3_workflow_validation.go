package api3

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/workflow"
)

type workflowStatusUpdateRequest struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	StatusCategory  string `json:"statusCategory"`
	StatusReference string `json:"statusReference"`
}

type workflowStatusLayoutRequest struct {
	StatusReference string            `json:"statusReference"`
	Properties      map[string]string `json:"properties"`
}

type workflowTransitionLinkRequest struct {
	FromStatusReference string `json:"fromStatusReference"`
}

type workflowTransitionUpdateRequest struct {
	ID                string                          `json:"id"`
	Name              string                          `json:"name"`
	Type              string                          `json:"type"`
	ToStatusReference string                          `json:"toStatusReference"`
	Links             []workflowTransitionLinkRequest `json:"links"`
	Actions           []json.RawMessage               `json:"actions"`
	Validators        []json.RawMessage               `json:"validators"`
	Conditions        json.RawMessage                 `json:"conditions"`
	Triggers          []json.RawMessage               `json:"triggers"`
}

type workflowCreateItemRequest struct {
	Name        string                            `json:"name"`
	Statuses    []workflowStatusLayoutRequest     `json:"statuses"`
	Transitions []workflowTransitionUpdateRequest `json:"transitions"`
}

type workflowVersionRequest struct {
	ID            string `json:"id"`
	VersionNumber int    `json:"versionNumber"`
}

type workflowUpdateItemRequest struct {
	ID          string                            `json:"id"`
	Version     workflowVersionRequest            `json:"version"`
	Statuses    []workflowStatusLayoutRequest     `json:"statuses"`
	Transitions []workflowTransitionUpdateRequest `json:"transitions"`
}

type workflowCreatePayloadRequest struct {
	Scope struct {
		Type string `json:"type"`
	} `json:"scope"`
	Statuses  []workflowStatusUpdateRequest `json:"statuses"`
	Workflows []workflowCreateItemRequest   `json:"workflows"`
}

type workflowUpdatePayloadRequest struct {
	Statuses  []workflowStatusUpdateRequest `json:"statuses"`
	Workflows []workflowUpdateItemRequest   `json:"workflows"`
}

type workflowValidationOptionsRequest struct {
	Levels []string `json:"levels"`
}

type workflowCreateValidationRequest struct {
	Payload           *workflowCreatePayloadRequest    `json:"payload"`
	ValidationOptions workflowValidationOptionsRequest `json:"validationOptions"`
}

type workflowUpdateValidationRequest struct {
	Payload           *workflowUpdatePayloadRequest    `json:"payload"`
	ValidationOptions workflowValidationOptionsRequest `json:"validationOptions"`
}

func workflowValidationError(code, message, kind string, reference map[string]any) map[string]any {
	value := map[string]any{"level": "ERROR", "code": code, "message": message, "type": kind}
	if reference != nil {
		value["elementReference"] = reference
	}
	return value
}

func validateWorkflowLevels(levels []string) error {
	if len(levels) > 2 {
		return fmt.Errorf("validationOptions.levels accepts at most two values")
	}
	for _, level := range levels {
		if level != "ERROR" && level != "WARNING" {
			return fmt.Errorf("validationOptions.levels contains an invalid value")
		}
	}
	return nil
}

func includeWorkflowErrors(levels []string) bool {
	if len(levels) == 0 {
		return true
	}
	for _, level := range levels {
		if level == "ERROR" {
			return true
		}
	}
	return false
}

func (h *Handler) workflowStatusReferences(r *http.Request, workspaceID string, updates []workflowStatusUpdateRequest) (map[string]string, []map[string]any, error) {
	statuses, err := h.Store.StatusesForWorkspace(r.Context(), workspaceID)
	if err != nil {
		return nil, nil, err
	}
	known := make(map[string]bool, len(statuses))
	references := make(map[string]string, len(statuses)+len(updates))
	for _, status := range statuses {
		known[status.ID] = true
		references[status.ID] = status.ID
	}
	errors := make([]map[string]any, 0)
	for _, status := range updates {
		if status.StatusReference == "" {
			errors = append(errors, workflowValidationError("STATUS_REFERENCE_REQUIRED", "A status reference is required.", "STATUS", nil))
			continue
		}
		if strings.TrimSpace(status.Name) == "" || (status.StatusCategory != "TODO" && status.StatusCategory != "IN_PROGRESS" && status.StatusCategory != "DONE") {
			errors = append(errors, workflowValidationError("STATUS_INVALID", "A status name and valid status category are required.", "STATUS", map[string]any{"statusReference": status.StatusReference}))
			continue
		}
		if status.ID == "" {
			errors = append(errors, workflowValidationError("STATUS_CREATE_UNSUPPORTED", "This endpoint currently accepts existing status IDs only.", "STATUS", map[string]any{"statusReference": status.StatusReference}))
			continue
		}
		if !known[status.ID] {
			errors = append(errors, workflowValidationError("STATUS_NOT_FOUND", "The referenced status does not exist.", "STATUS", map[string]any{"statusReference": status.StatusReference}))
			continue
		}
		references[status.StatusReference] = status.ID
	}
	return references, errors, nil
}

func workflowDefinitionFromRequest(id, name string, statuses []workflowStatusLayoutRequest, transitions []workflowTransitionUpdateRequest, references map[string]string) (workflow.Workflow, []map[string]any) {
	errors := make([]map[string]any, 0)
	allowed := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		resolved := references[status.StatusReference]
		if resolved == "" {
			errors = append(errors, workflowValidationError("STATUS_NOT_FOUND", "The workflow status reference does not exist.", "STATUS", map[string]any{"statusReference": status.StatusReference}))
			continue
		}
		allowed[resolved] = true
	}
	wf := workflow.Workflow{ID: id, Name: strings.TrimSpace(name)}
	if wf.Name == "" || len(wf.Name) > 255 {
		errors = append(errors, workflowValidationError("WORKFLOW_NAME_INVALID", "The workflow name is required and must be at most 255 characters.", "WORKFLOW", nil))
	}
	if len(statuses) == 0 {
		errors = append(errors, workflowValidationError("WORKFLOW_STATUSES_REQUIRED", "At least one workflow status is required.", "WORKFLOW", nil))
	}
	if len(transitions) == 0 {
		errors = append(errors, workflowValidationError("WORKFLOW_TRANSITIONS_REQUIRED", "At least one workflow transition is required.", "WORKFLOW", nil))
	}
	transitionIDs := make(map[string]bool)
	for index, item := range transitions {
		transitionID := strings.TrimSpace(item.ID)
		if transitionID == "" {
			transitionID = fmt.Sprintf("new-%d", index+1)
		}
		if transitionIDs[transitionID] {
			errors = append(errors, workflowValidationError("TRANSITION_ID_DUPLICATE", "Transition IDs must be unique.", "TRANSITION", map[string]any{"transitionId": transitionID}))
			continue
		}
		transitionIDs[transitionID] = true
		to := references[item.ToStatusReference]
		if to == "" || !allowed[to] {
			errors = append(errors, workflowValidationError("TRANSITION_DESTINATION_INVALID", "The transition destination must reference a workflow status.", "TRANSITION", map[string]any{"transitionId": transitionID}))
			continue
		}
		if item.Type != "" && item.Type != "DIRECTED" {
			errors = append(errors, workflowValidationError("TRANSITION_TYPE_UNSUPPORTED", "Only directed transitions are currently supported.", "TRANSITION", map[string]any{"transitionId": transitionID}))
			continue
		}
		from := make([]string, 0, len(item.Links))
		for _, link := range item.Links {
			statusID := references[link.FromStatusReference]
			if statusID == "" || !allowed[statusID] {
				errors = append(errors, workflowValidationError("TRANSITION_SOURCE_INVALID", "Every transition source must reference a workflow status.", "TRANSITION", map[string]any{"transitionId": transitionID}))
				continue
			}
			from = append(from, statusID)
		}
		if strings.TrimSpace(item.Name) == "" || len(from) == 0 {
			errors = append(errors, workflowValidationError("TRANSITION_INVALID", "A directed transition requires a name and at least one source.", "TRANSITION", map[string]any{"transitionId": transitionID}))
			continue
		}
		conditions := strings.TrimSpace(string(item.Conditions))
		if len(item.Actions) > 0 || len(item.Validators) > 0 || (conditions != "" && conditions != "null") || len(item.Triggers) > 0 {
			errors = append(errors, workflowValidationError("TRANSITION_RULES_UNSUPPORTED", "Transition rules are not yet supported by this workflow payload.", "RULE", map[string]any{"transitionId": transitionID}))
			continue
		}
		wf.Transitions = append(wf.Transitions, workflow.Transition{ID: transitionID, Name: strings.TrimSpace(item.Name), From: from, To: to})
	}
	return wf, errors
}

func (h *Handler) workflowCreateValidation(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request workflowCreateValidationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Payload == nil {
		jiraError(w, http.StatusBadRequest, "payload is required")
		return
	}
	if err := validateWorkflowLevels(request.ValidationOptions.Levels); err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	errors := make([]map[string]any, 0)
	if request.Payload.Scope.Type != "" && request.Payload.Scope.Type != "GLOBAL" {
		errors = append(errors, workflowValidationError("SCOPE_UNSUPPORTED", "Only global workflows are currently supported.", "SCOPE", nil))
	}
	if len(request.Payload.Workflows) == 0 || len(request.Payload.Workflows) > 20 || len(request.Payload.Statuses) > 1000 {
		errors = append(errors, workflowValidationError("PAYLOAD_SIZE_INVALID", "Provide between 1 and 20 workflows and no more than 1000 statuses.", "WORKFLOW", nil))
	}
	references, statusErrors, err := h.workflowStatusReferences(r, workspaceID, request.Payload.Statuses)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	errors = append(errors, statusErrors...)
	existing, err := h.Store.ListWorkflows(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	names := make(map[string]bool, len(existing))
	for _, item := range existing {
		names[strings.ToLower(item.Name)] = true
	}
	for index, item := range request.Payload.Workflows {
		wf, itemErrors := workflowDefinitionFromRequest(fmt.Sprintf("validation-%d", index+1), item.Name, item.Statuses, item.Transitions, references)
		errors = append(errors, itemErrors...)
		if names[strings.ToLower(wf.Name)] {
			errors = append(errors, workflowValidationError("WORKFLOW_NAME_CONFLICT", "A workflow already uses this name.", "WORKFLOW", nil))
		}
		names[strings.ToLower(wf.Name)] = true
		if len(itemErrors) == 0 {
			if err := h.Store.ValidateWorkflowDefinition(r.Context(), workspaceID, wf); err != nil {
				errors = append(errors, workflowValidationError("WORKFLOW_INVALID", err.Error(), "WORKFLOW", nil))
			}
		}
	}
	if !includeWorkflowErrors(request.ValidationOptions.Levels) {
		errors = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"errors": errors})
}

func (h *Handler) workflowUpdateValidation(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request workflowUpdateValidationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Payload == nil {
		jiraError(w, http.StatusBadRequest, "payload is required")
		return
	}
	if err := validateWorkflowLevels(request.ValidationOptions.Levels); err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	errors := make([]map[string]any, 0)
	if len(request.Payload.Workflows) == 0 || len(request.Payload.Workflows) > 20 || len(request.Payload.Statuses) > 1000 {
		errors = append(errors, workflowValidationError("PAYLOAD_SIZE_INVALID", "Provide between 1 and 20 workflows and no more than 1000 statuses.", "WORKFLOW", nil))
	}
	references, statusErrors, err := h.workflowStatusReferences(r, workspaceID, request.Payload.Statuses)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	errors = append(errors, statusErrors...)
	for _, item := range request.Payload.Workflows {
		published, err := h.Store.WorkflowByID(r.Context(), workspaceID, item.ID)
		if err != nil {
			errors = append(errors, workflowValidationError("WORKFLOW_NOT_FOUND", "The workflow does not exist.", "WORKFLOW", nil))
			continue
		}
		if item.Version.VersionNumber != published.Version || (item.Version.ID != "" && item.Version.ID != published.ID) {
			errors = append(errors, workflowValidationError("WORKFLOW_VERSION_CONFLICT", "The workflow version is stale.", "WORKFLOW", nil))
		}
		wf, itemErrors := workflowDefinitionFromRequest(item.ID, published.Name, item.Statuses, item.Transitions, references)
		errors = append(errors, itemErrors...)
		if len(itemErrors) == 0 {
			if err := h.Store.ValidateWorkflowDefinition(r.Context(), workspaceID, wf); err != nil {
				errors = append(errors, workflowValidationError("WORKFLOW_INVALID", err.Error(), "WORKFLOW", nil))
			}
		}
	}
	if !includeWorkflowErrors(request.ValidationOptions.Levels) {
		errors = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"errors": errors})
}
