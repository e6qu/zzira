package api3

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
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
	Layout          *workflow.Layout  `json:"layout"`
	Properties      map[string]string `json:"properties"`
}

type workflowTransitionLinkRequest struct {
	FromStatusReference string `json:"fromStatusReference"`
}

type workflowRuleUpdateRequest struct {
	ID         string            `json:"id"`
	RuleKey    string            `json:"ruleKey"`
	Parameters map[string]string `json:"parameters"`
}

type workflowConditionGroupUpdateRequest struct {
	Operation       string                                `json:"operation"`
	Conditions      []workflowRuleUpdateRequest           `json:"conditions"`
	ConditionGroups []workflowConditionGroupUpdateRequest `json:"conditionGroups"`
}

type workflowTransitionUpdateRequest struct {
	ID                string                               `json:"id"`
	Name              string                               `json:"name"`
	Type              string                               `json:"type"`
	ToStatusReference string                               `json:"toStatusReference"`
	Links             []workflowTransitionLinkRequest      `json:"links"`
	Actions           []workflowRuleUpdateRequest          `json:"actions"`
	Validators        []workflowRuleUpdateRequest          `json:"validators"`
	Conditions        *workflowConditionGroupUpdateRequest `json:"conditions"`
	TransitionScreen  *workflowRuleUpdateRequest           `json:"transitionScreen"`
	Triggers          []json.RawMessage                    `json:"triggers"`
}

type workflowCreateItemRequest struct {
	Name                            string                            `json:"name"`
	Description                     string                            `json:"description"`
	StartPointLayout                *workflow.Layout                  `json:"startPointLayout"`
	LoopedTransitionContainerLayout *workflow.Layout                  `json:"loopedTransitionContainerLayout"`
	Statuses                        []workflowStatusLayoutRequest     `json:"statuses"`
	Transitions                     []workflowTransitionUpdateRequest `json:"transitions"`
}

type workflowVersionRequest struct {
	ID            string `json:"id"`
	VersionNumber int    `json:"versionNumber"`
}

type workflowStatusMigrationRequest struct {
	OldStatusReference string `json:"oldStatusReference"`
	NewStatusReference string `json:"newStatusReference"`
}

type workflowScopedStatusMappingRequest struct {
	ProjectID        string                           `json:"projectId"`
	IssueTypeID      string                           `json:"issueTypeId"`
	StatusMigrations []workflowStatusMigrationRequest `json:"statusMigrations"`
}

type workflowUpdateItemRequest struct {
	ID                              string                               `json:"id"`
	Description                     *string                              `json:"description"`
	StartPointLayout                *workflow.Layout                     `json:"startPointLayout"`
	LoopedTransitionContainerLayout *workflow.Layout                     `json:"loopedTransitionContainerLayout"`
	Version                         workflowVersionRequest               `json:"version"`
	Statuses                        []workflowStatusLayoutRequest        `json:"statuses"`
	Transitions                     []workflowTransitionUpdateRequest    `json:"transitions"`
	DefaultStatusMappings           []workflowStatusMigrationRequest     `json:"defaultStatusMappings"`
	StatusMappings                  []workflowScopedStatusMappingRequest `json:"statusMappings"`
}

type workflowCreatePayloadRequest struct {
	Scope struct {
		Type    string `json:"type"`
		Project struct {
			ID string `json:"id"`
		} `json:"project"`
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

func workflowCategory(category string) string {
	switch category {
	case "TODO":
		return "new"
	case "IN_PROGRESS":
		return "indeterminate"
	case "DONE":
		return "done"
	default:
		return ""
	}
}

func (h *Handler) workflowStatusReferences(r *http.Request, workspaceID, projectID string, updates []workflowStatusUpdateRequest, generateIDs bool) (map[string]string, []models.Status, []map[string]any, error) {
	var statuses []models.Status
	var err error
	if projectID == "" {
		statuses, err = h.Store.StatusesForWorkspace(r.Context(), workspaceID)
	} else {
		statuses, err = h.Store.StatusesForProject(r.Context(), workspaceID, projectID, true)
	}
	if err != nil {
		return nil, nil, nil, err
	}
	known := make(map[string]bool, len(statuses))
	names := make(map[string]bool, len(statuses)+len(updates))
	references := make(map[string]string, len(statuses)+len(updates))
	for _, status := range statuses {
		known[status.ID] = true
		if projectID == "" || status.ProjectID == projectID {
			names[strings.ToLower(status.Name)] = true
		}
		references[status.ID] = status.ID
	}
	created := make([]models.Status, 0)
	errors := make([]map[string]any, 0)
	seenReferences := make(map[string]bool, len(updates))
	for index, status := range updates {
		if status.StatusReference == "" {
			errors = append(errors, workflowValidationError("STATUS_REFERENCE_REQUIRED", "A status reference is required.", "STATUS", nil))
			continue
		}
		if seenReferences[status.StatusReference] {
			errors = append(errors, workflowValidationError("STATUS_REFERENCE_CONFLICT", "Status references in the request must be unique.", "STATUS", map[string]any{"statusReference": status.StatusReference}))
			continue
		}
		seenReferences[status.StatusReference] = true
		name := strings.TrimSpace(status.Name)
		category := workflowCategory(status.StatusCategory)
		if name == "" || category == "" {
			errors = append(errors, workflowValidationError("STATUS_INVALID", "A status name and valid status category are required.", "STATUS", map[string]any{"statusReference": status.StatusReference}))
			continue
		}
		if status.ID == "" {
			nameKey := strings.ToLower(name)
			if names[nameKey] {
				errors = append(errors, workflowValidationError("STATUS_NAME_CONFLICT", "A visible status already uses this name.", "STATUS", map[string]any{"statusReference": status.StatusReference}))
				continue
			}
			id := fmt.Sprintf("validation-status-%d", index+1)
			if generateIDs {
				id = store.NewID("status")
			}
			created = append(created, models.Status{ID: id, Name: name, Category: category, ProjectID: projectID})
			names[nameKey], known[id], references[status.StatusReference] = true, true, id
			continue
		}
		if !known[status.ID] {
			errors = append(errors, workflowValidationError("STATUS_NOT_FOUND", "The referenced status does not exist.", "STATUS", map[string]any{"statusReference": status.StatusReference}))
			continue
		}
		references[status.StatusReference] = status.ID
	}
	return references, created, errors, nil
}

func (h *Handler) workflowCreateScope(r *http.Request, workspaceID string, payload *workflowCreatePayloadRequest) (string, error) {
	switch payload.Scope.Type {
	case "", "GLOBAL":
		if payload.Scope.Project.ID != "" {
			return "", fmt.Errorf("GLOBAL scope cannot include a project")
		}
		return "", nil
	case "PROJECT":
		project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, payload.Scope.Project.ID)
		if err != nil {
			return "", fmt.Errorf("PROJECT scope requires a project in this workspace")
		}
		return project.ID, nil
	default:
		return "", fmt.Errorf("scope type must be GLOBAL or PROJECT")
	}
}

func (h *Handler) workflowUpdateScope(r *http.Request, workspaceID string, workflows []workflowUpdateItemRequest) (string, error) {
	projectID := ""
	found := false
	for _, item := range workflows {
		published, err := h.Store.WorkflowByID(r.Context(), workspaceID, item.ID)
		if err != nil {
			continue
		}
		if !found {
			projectID = published.ProjectID
			found = true
		} else if projectID != published.ProjectID {
			return "", fmt.Errorf("workflow updates with new statuses must share one scope")
		}
	}
	return projectID, nil
}

func workflowDefinitionFromRequest(id, name, description string, startPointLayout, loopedTransitionContainerLayout *workflow.Layout, statuses []workflowStatusLayoutRequest, transitions []workflowTransitionUpdateRequest, references map[string]string) (workflow.Workflow, []map[string]any) {
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
	wf := workflow.Workflow{ID: id, Name: strings.TrimSpace(name), Description: strings.TrimSpace(description), StartPointLayout: startPointLayout, LoopedTransitionContainerLayout: loopedTransitionContainerLayout}
	if wf.Name == "" || len(wf.Name) > 255 {
		errors = append(errors, workflowValidationError("WORKFLOW_NAME_INVALID", "The workflow name is required and must be at most 255 characters.", "WORKFLOW", nil))
	}
	if len(wf.Description) > 1000 {
		errors = append(errors, workflowValidationError("WORKFLOW_DESCRIPTION_INVALID", "The workflow description must be at most 1000 characters.", "WORKFLOW", nil))
	}
	if !workflowLayoutValid(startPointLayout) || !workflowLayoutValid(loopedTransitionContainerLayout) {
		errors = append(errors, workflowValidationError("WORKFLOW_LAYOUT_INVALID", "Workflow layout coordinates must be finite values between -10000 and 10000.", "WORKFLOW", nil))
	}
	if len(statuses) == 0 {
		errors = append(errors, workflowValidationError("WORKFLOW_STATUSES_REQUIRED", "At least one workflow status is required.", "WORKFLOW", nil))
	}
	statusLayouts := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		resolved := references[status.StatusReference]
		if resolved == "" {
			continue
		}
		if statusLayouts[resolved] {
			errors = append(errors, workflowValidationError("WORKFLOW_STATUS_DUPLICATE", "A workflow status can only appear once.", "STATUS", map[string]any{"statusReference": status.StatusReference}))
			continue
		}
		statusLayouts[resolved] = true
		if !workflowLayoutValid(status.Layout) {
			errors = append(errors, workflowValidationError("WORKFLOW_STATUS_LAYOUT_INVALID", "Status layout coordinates must be finite values between -10000 and 10000.", "STATUS", map[string]any{"statusReference": status.StatusReference}))
			continue
		}
		properties := status.Properties
		if properties == nil {
			properties = map[string]string{}
		}
		wf.Statuses = append(wf.Statuses, workflow.StatusLayout{StatusReference: resolved, Layout: status.Layout, Properties: properties})
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
		if len(item.Triggers) > 0 {
			errors = append(errors, workflowValidationError("TRANSITION_TRIGGERS_UNSUPPORTED", "Transition triggers are not yet executable.", "RULE", map[string]any{"transitionId": transitionID}))
			continue
		}
		transition := workflow.Transition{ID: transitionID, Name: strings.TrimSpace(item.Name), From: from, To: to}
		if item.TransitionScreen != nil {
			screen := workflowRuleFromRequest(*item.TransitionScreen, transitionID+"-screen")
			transition.Screen = &screen
		}
		for ruleIndex, rule := range item.Actions {
			transition.Actions = append(transition.Actions, workflowRuleFromRequest(rule, fmt.Sprintf("%s-action-%d", transitionID, ruleIndex+1)))
		}
		for ruleIndex, rule := range item.Validators {
			transition.Validators = append(transition.Validators, workflowRuleFromRequest(rule, fmt.Sprintf("%s-validator-%d", transitionID, ruleIndex+1)))
		}
		if item.Conditions != nil {
			conditions := workflowConditionGroupFromRequest(*item.Conditions, transitionID, "condition")
			transition.Conditions = &conditions
		}
		if err := workflow.ValidateTransitionRules(transition); err != nil {
			errors = append(errors, workflowValidationError("TRANSITION_RULE_INVALID", err.Error(), "RULE", map[string]any{"transitionId": transitionID}))
			continue
		}
		wf.Transitions = append(wf.Transitions, transition)
	}
	return wf, errors
}

func workflowLayoutValid(layout *workflow.Layout) bool {
	return layout == nil || (!math.IsNaN(layout.X) && !math.IsInf(layout.X, 0) && !math.IsNaN(layout.Y) && !math.IsInf(layout.Y, 0) && layout.X >= -10000 && layout.X <= 10000 && layout.Y >= -10000 && layout.Y <= 10000)
}

func workflowRuleFromRequest(rule workflowRuleUpdateRequest, fallbackID string) workflow.Rule {
	id := strings.TrimSpace(rule.ID)
	if id == "" {
		id = fallbackID
	}
	parameters := rule.Parameters
	if parameters == nil {
		parameters = map[string]string{}
	}
	return workflow.Rule{ID: id, RuleKey: strings.TrimSpace(rule.RuleKey), Parameters: parameters}
}

func workflowConditionGroupFromRequest(group workflowConditionGroupUpdateRequest, transitionID, path string) workflow.ConditionGroup {
	converted := workflow.ConditionGroup{Operation: group.Operation, Conditions: make([]workflow.Rule, 0, len(group.Conditions)), ConditionGroups: make([]workflow.ConditionGroup, 0, len(group.ConditionGroups))}
	for index, condition := range group.Conditions {
		converted.Conditions = append(converted.Conditions, workflowRuleFromRequest(condition, fmt.Sprintf("%s-%s-%d", transitionID, path, index+1)))
	}
	for index, child := range group.ConditionGroups {
		converted.ConditionGroups = append(converted.ConditionGroups, workflowConditionGroupFromRequest(child, transitionID, fmt.Sprintf("%s-group-%d", path, index+1)))
	}
	return converted
}

func workflowStatusMigrationsFromRequest(item workflowUpdateItemRequest, references map[string]string, wf workflow.Workflow) ([]store.WorkflowStatusMigration, []map[string]any) {
	targetStatuses := workflowStatusReferences(wf)
	migrations := make([]store.WorkflowStatusMigration, 0)
	errors := make([]map[string]any, 0)
	add := func(projectID, issueTypeID string, migration workflowStatusMigrationRequest) {
		oldStatusID, newStatusID := references[migration.OldStatusReference], references[migration.NewStatusReference]
		if oldStatusID == "" || newStatusID == "" {
			errors = append(errors, workflowValidationError("STATUS_MAPPING_REFERENCE_INVALID", "Status mappings must reference known old and new statuses.", "STATUS_MAPPING", map[string]any{"statusMappingReference": map[string]string{"projectId": projectID, "issueTypeId": issueTypeID}}))
			return
		}
		if !targetStatuses[newStatusID] {
			errors = append(errors, workflowValidationError("STATUS_MAPPING_TARGET_INVALID", "A replacement status must belong to the updated workflow.", "STATUS_MAPPING", map[string]any{"statusMappingReference": map[string]string{"projectId": projectID, "issueTypeId": issueTypeID}}))
			return
		}
		migrations = append(migrations, store.WorkflowStatusMigration{ProjectID: projectID, IssueTypeID: issueTypeID, OldStatusID: oldStatusID, NewStatusID: newStatusID})
	}
	for _, migration := range item.DefaultStatusMappings {
		add("", "", migration)
	}
	for _, mapping := range item.StatusMappings {
		if mapping.ProjectID == "" || mapping.IssueTypeID == "" || len(mapping.StatusMigrations) == 0 {
			errors = append(errors, workflowValidationError("STATUS_MAPPING_CONTEXT_INVALID", "Scoped mappings require projectId, issueTypeId, and statusMigrations.", "STATUS_MAPPING", nil))
			continue
		}
		for _, migration := range mapping.StatusMigrations {
			add(mapping.ProjectID, mapping.IssueTypeID, migration)
		}
	}
	return migrations, errors
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
	projectID, scopeErr := h.workflowCreateScope(r, workspaceID, request.Payload)
	if scopeErr != nil {
		errors = append(errors, workflowValidationError("SCOPE_INVALID", scopeErr.Error(), "SCOPE", nil))
	}
	if len(request.Payload.Workflows) == 0 || len(request.Payload.Workflows) > 20 || len(request.Payload.Statuses) > 1000 {
		errors = append(errors, workflowValidationError("PAYLOAD_SIZE_INVALID", "Provide between 1 and 20 workflows and no more than 1000 statuses.", "WORKFLOW", nil))
	}
	references, createdStatuses, statusErrors, err := h.workflowStatusReferences(r, workspaceID, projectID, request.Payload.Statuses, false)
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
		if item.ProjectID == projectID {
			names[strings.ToLower(item.Name)] = true
		}
	}
	for index, item := range request.Payload.Workflows {
		wf, itemErrors := workflowDefinitionFromRequest(fmt.Sprintf("validation-%d", index+1), item.Name, item.Description, item.StartPointLayout, item.LoopedTransitionContainerLayout, item.Statuses, item.Transitions, references)
		wf.ProjectID = projectID
		errors = append(errors, itemErrors...)
		if names[strings.ToLower(wf.Name)] {
			errors = append(errors, workflowValidationError("WORKFLOW_NAME_CONFLICT", "A workflow already uses this name.", "WORKFLOW", nil))
		}
		names[strings.ToLower(wf.Name)] = true
		if len(itemErrors) == 0 && len(createdStatuses) == 0 {
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
	projectID, scopeErr := h.workflowUpdateScope(r, workspaceID, request.Payload.Workflows)
	if scopeErr != nil {
		errors = append(errors, workflowValidationError("SCOPE_INVALID", scopeErr.Error(), "SCOPE", nil))
	}
	references, createdStatuses, statusErrors, err := h.workflowStatusReferences(r, workspaceID, projectID, request.Payload.Statuses, false)
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
		description, startPointLayout, loopedTransitionContainerLayout := workflowUpdateMetadata(item, published)
		wf, itemErrors := workflowDefinitionFromRequest(item.ID, published.Name, description, startPointLayout, loopedTransitionContainerLayout, item.Statuses, item.Transitions, references)
		wf.ProjectID = published.ProjectID
		errors = append(errors, itemErrors...)
		_, mappingErrors := workflowStatusMigrationsFromRequest(item, references, wf)
		errors = append(errors, mappingErrors...)
		if len(itemErrors) == 0 && len(createdStatuses) == 0 {
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

func workflowMutationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrAdminConflict):
		jiraError(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrAdminValidation), errors.Is(err, store.ErrAdminNotFound):
		jiraError(w, http.StatusBadRequest, err.Error())
	default:
		jiraError(w, http.StatusInternalServerError, "internal error")
	}
}

func workflowValidationMessages(values []map[string]any) string {
	messages := make([]string, 0, len(values))
	for _, value := range values {
		if message, ok := value["message"].(string); ok {
			messages = append(messages, message)
		}
	}
	return strings.Join(messages, " ")
}

func workflowValidationHasConflict(values []map[string]any) bool {
	for _, value := range values {
		code, _ := value["code"].(string)
		if strings.HasSuffix(code, "_CONFLICT") {
			return true
		}
	}
	return false
}

func (h *Handler) workflowResponseStatuses(r *http.Request, workspaceID string, workflows []workflow.Workflow) ([]map[string]any, error) {
	referenced := make(map[string]bool)
	for _, item := range workflows {
		for id := range workflowStatusReferences(item) {
			referenced[id] = true
		}
	}
	statuses, err := h.Store.StatusesForAdministration(r.Context(), workspaceID)
	if err != nil {
		return nil, err
	}
	values := make([]map[string]any, 0, len(referenced))
	for _, status := range statuses {
		if referenced[status.ID] {
			values = append(values, workflowSearchStatusBean(status))
		}
	}
	return values, nil
}

func (h *Handler) workflowCreate(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var payload workflowCreatePayloadRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		jiraError(w, http.StatusBadRequest, "request body is invalid")
		return
	}
	validationErrors := make([]map[string]any, 0)
	projectID, scopeErr := h.workflowCreateScope(r, workspaceID, &payload)
	if scopeErr != nil {
		validationErrors = append(validationErrors, workflowValidationError("SCOPE_INVALID", scopeErr.Error(), "SCOPE", nil))
	}
	if len(payload.Workflows) == 0 || len(payload.Workflows) > 20 || len(payload.Statuses) > 1000 {
		validationErrors = append(validationErrors, workflowValidationError("PAYLOAD_SIZE_INVALID", "Provide between 1 and 20 workflows and no more than 1000 statuses.", "WORKFLOW", nil))
	}
	references, createdStatuses, statusErrors, err := h.workflowStatusReferences(r, workspaceID, projectID, payload.Statuses, true)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	validationErrors = append(validationErrors, statusErrors...)
	definitions := make([]workflow.Workflow, 0, len(payload.Workflows))
	names := make(map[string]bool)
	for _, item := range payload.Workflows {
		definition, itemErrors := workflowDefinitionFromRequest(store.NewID("workflow"), item.Name, item.Description, item.StartPointLayout, item.LoopedTransitionContainerLayout, item.Statuses, item.Transitions, references)
		definition.ProjectID = projectID
		validationErrors = append(validationErrors, itemErrors...)
		nameKey := strings.ToLower(definition.Name)
		if names[nameKey] {
			validationErrors = append(validationErrors, workflowValidationError("WORKFLOW_NAME_CONFLICT", "Workflow names in the request must be unique.", "WORKFLOW", nil))
		}
		names[nameKey] = true
		definitions = append(definitions, definition)
	}
	if len(validationErrors) > 0 {
		status := http.StatusBadRequest
		if workflowValidationHasConflict(validationErrors) {
			status = http.StatusConflict
		}
		jiraError(w, status, workflowValidationMessages(validationErrors))
		return
	}
	_, created, err := h.Store.CreateWorkflowBatch(r.Context(), workspaceID, actorID, createdStatuses, definitions)
	if err != nil {
		workflowMutationError(w, err)
		return
	}
	workflowValues := make([]map[string]any, 0, len(created))
	for _, item := range created {
		workflowValues = append(workflowValues, workflowSearchBean(item, true))
	}
	statuses, err := h.workflowResponseStatuses(r, workspaceID, created)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": workflowValues, "statuses": statuses})
}

func (h *Handler) workflowUpdate(w http.ResponseWriter, r *http.Request) {
	workspaceID, actorID, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var payload workflowUpdatePayloadRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		jiraError(w, http.StatusBadRequest, "request body is invalid")
		return
	}
	validationErrors := make([]map[string]any, 0)
	if len(payload.Workflows) == 0 || len(payload.Workflows) > 20 || len(payload.Statuses) > 1000 {
		validationErrors = append(validationErrors, workflowValidationError("PAYLOAD_SIZE_INVALID", "Provide between 1 and 20 workflows and no more than 1000 statuses.", "WORKFLOW", nil))
	}
	projectID, scopeErr := h.workflowUpdateScope(r, workspaceID, payload.Workflows)
	if scopeErr != nil {
		validationErrors = append(validationErrors, workflowValidationError("SCOPE_INVALID", scopeErr.Error(), "SCOPE", nil))
	}
	references, createdStatuses, statusErrors, err := h.workflowStatusReferences(r, workspaceID, projectID, payload.Statuses, true)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	validationErrors = append(validationErrors, statusErrors...)
	updates := make([]store.WorkflowUpdateDefinition, 0, len(payload.Workflows))
	for _, item := range payload.Workflows {
		published, err := h.Store.WorkflowByID(r.Context(), workspaceID, item.ID)
		if err != nil {
			validationErrors = append(validationErrors, workflowValidationError("WORKFLOW_NOT_FOUND", "The workflow does not exist.", "WORKFLOW", nil))
			continue
		}
		if item.Version.ID != "" && item.Version.ID != item.ID {
			validationErrors = append(validationErrors, workflowValidationError("WORKFLOW_VERSION_CONFLICT", "The workflow version ID does not match the workflow.", "WORKFLOW", nil))
		}
		description, startPointLayout, loopedTransitionContainerLayout := workflowUpdateMetadata(item, published)
		definition, itemErrors := workflowDefinitionFromRequest(item.ID, published.Name, description, startPointLayout, loopedTransitionContainerLayout, item.Statuses, item.Transitions, references)
		definition.ProjectID = published.ProjectID
		validationErrors = append(validationErrors, itemErrors...)
		migrations, mappingErrors := workflowStatusMigrationsFromRequest(item, references, definition)
		validationErrors = append(validationErrors, mappingErrors...)
		updates = append(updates, store.WorkflowUpdateDefinition{Workflow: definition, ExpectedVersion: item.Version.VersionNumber, StatusMigrations: migrations})
	}
	if len(validationErrors) > 0 {
		status := http.StatusBadRequest
		if workflowValidationHasConflict(validationErrors) {
			status = http.StatusConflict
		}
		jiraError(w, status, workflowValidationMessages(validationErrors))
		return
	}
	_, updated, err := h.Store.UpdateWorkflowBatch(r.Context(), workspaceID, actorID, createdStatuses, updates)
	if err != nil {
		workflowMutationError(w, err)
		return
	}
	workflowValues := make([]map[string]any, 0, len(updated))
	for _, item := range updated {
		workflowValues = append(workflowValues, workflowSearchBean(item, true))
	}
	statuses, err := h.workflowResponseStatuses(r, workspaceID, updated)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": workflowValues, "statuses": statuses, "taskId": nil})
}

func workflowUpdateMetadata(item workflowUpdateItemRequest, published workflow.Workflow) (string, *workflow.Layout, *workflow.Layout) {
	description := published.Description
	if item.Description != nil {
		description = *item.Description
	}
	startPointLayout := item.StartPointLayout
	if startPointLayout == nil {
		startPointLayout = published.StartPointLayout
	}
	loopedTransitionContainerLayout := item.LoopedTransitionContainerLayout
	if loopedTransitionContainerLayout == nil {
		loopedTransitionContainerLayout = published.LoopedTransitionContainerLayout
	}
	return description, startPointLayout, loopedTransitionContainerLayout
}
