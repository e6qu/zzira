package api3

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

type workflowSchemeAssociationRequest struct {
	IssueTypeIDs []string `json:"issueTypeIds"`
	WorkflowID   string   `json:"workflowId"`
}

type workflowAssociationStatusMappingRequest struct {
	OldStatusID string `json:"oldStatusId"`
	NewStatusID string `json:"newStatusId"`
}

type mappingsByIssueTypeOverrideRequest struct {
	IssueTypeID    string                                    `json:"issueTypeId"`
	StatusMappings []workflowAssociationStatusMappingRequest `json:"statusMappings"`
}

type mappingsByWorkflowRequest struct {
	OldWorkflowID  string                                    `json:"oldWorkflowId"`
	NewWorkflowID  string                                    `json:"newWorkflowId"`
	StatusMappings []workflowAssociationStatusMappingRequest `json:"statusMappings"`
}

func (h *Handler) workflowStatusMappings(r *http.Request, workspaceID string, current, candidate workflow.Scheme, byIssueType []mappingsByIssueTypeOverrideRequest, byWorkflow []mappingsByWorkflowRequest) ([]store.WorkflowStatusMapping, error) {
	issueTypes, err := h.Store.IssueTypes(r.Context(), workspaceID)
	if err != nil {
		return nil, err
	}
	ids := h.issueTypeIDsFor(r, workspaceID)
	statusIDs := h.statusIDsFor(r, workspaceID)
	workflowIDs := h.workflowIDsFor(r, workspaceID)
	var mappings []store.WorkflowStatusMapping
	for _, workflowMapping := range byWorkflow {
		oldWorkflow, newWorkflow := workflowIDs.toInternal(workflowMapping.OldWorkflowID), workflowIDs.toInternal(workflowMapping.NewWorkflowID)
		for _, issueType := range issueTypes {
			if schemeWorkflowIDAPI(current, issueType.ID) != oldWorkflow || schemeWorkflowIDAPI(candidate, issueType.ID) != newWorkflow {
				continue
			}
			for _, mapping := range workflowMapping.StatusMappings {
				mappings = append(mappings, store.WorkflowStatusMapping{IssueTypeID: issueType.ID, OldStatusID: statusIDs.toInternal(mapping.OldStatusID), NewStatusID: statusIDs.toInternal(mapping.NewStatusID)})
			}
		}
	}
	for _, override := range byIssueType {
		for _, mapping := range override.StatusMappings {
			mappings = append(mappings, store.WorkflowStatusMapping{IssueTypeID: ids.toInternal(override.IssueTypeID), OldStatusID: statusIDs.toInternal(mapping.OldStatusID), NewStatusID: statusIDs.toInternal(mapping.NewStatusID)})
		}
	}
	return mappings, nil
}

func workflowSchemeFromAssociations(ids issueTypeIDs, current workflow.Scheme, defaultWorkflowID string, associations []workflowSchemeAssociationRequest) workflow.Scheme {
	current.DefaultWorkflowID = defaultWorkflowID
	if current.DefaultWorkflowID == "" {
		current.DefaultWorkflowID = workflow.Default().ID
	}
	current.IssueTypeMappings = make(map[string]string)
	for _, association := range associations {
		for _, issueTypeID := range association.IssueTypeIDs {
			current.IssueTypeMappings[ids.toInternal(issueTypeID)] = association.WorkflowID
		}
	}
	return current
}

func workflowMetadataBean(item workflow.Workflow) map[string]any {
	version := item.Version
	if version < 1 {
		version = 1
	}
	return map[string]any{
		"id": workflowWireID(item), "name": item.Name, "description": "",
		"version": map[string]any{"id": workflowWireID(item) + ":" + strconv.Itoa(version), "versionNumber": version},
	}
}

func (h *Handler) workflowSchemeReadBean(r *http.Request, workspaceID string, scheme workflow.Scheme) (map[string]any, error) {
	workflows, err := h.Store.ListGlobalWorkflows(r.Context(), workspaceID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]workflow.Workflow, len(workflows))
	for _, item := range workflows {
		byID[item.ID] = item
	}
	issueTypesByWorkflow := make(map[string][]string)
	for issueTypeID, workflowID := range scheme.IssueTypeMappings {
		issueTypesByWorkflow[workflowID] = append(issueTypesByWorkflow[workflowID], issueTypeID)
	}
	ids := h.issueTypeIDsFor(r, workspaceID)
	mappings := make([]map[string]any, 0, len(issueTypesByWorkflow))
	for workflowID, issueTypeIDs := range issueTypesByWorkflow {
		wired := ids.allToWire(issueTypeIDs)
		sort.Strings(wired)
		mappings = append(mappings, map[string]any{"workflow": workflowMetadataBean(byID[workflowID]), "issueTypeIds": wired})
	}
	sort.Slice(mappings, func(i, j int) bool {
		return mappings[i]["workflow"].(map[string]any)["id"].(string) < mappings[j]["workflow"].(map[string]any)["id"].(string)
	})
	return map[string]any{
		"id": scheme.ID, "name": scheme.Name, "description": scheme.Description,
		"scope":                  map[string]any{"type": "GLOBAL"},
		"version":                map[string]any{"id": scheme.ID + ":" + strconv.Itoa(scheme.Version), "versionNumber": scheme.Version},
		"defaultWorkflow":        workflowMetadataBean(byID[scheme.DefaultWorkflowID]),
		"workflowsForIssueTypes": mappings,
	}, nil
}

func (h *Handler) workflowSchemeBulkRoute(w http.ResponseWriter, r *http.Request, workspaceID, userID, path string) bool {
	if path == "/workflowscheme/read" {
		if r.Method != http.MethodPost {
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
			return true
		}
		var request struct {
			ProjectIDs        []string `json:"projectIds"`
			WorkflowSchemeIDs []string `json:"workflowSchemeIds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			jiraError(w, http.StatusBadRequest, "Invalid workflow scheme read request.")
			return true
		}
		ids := make(map[string]bool)
		for _, id := range request.WorkflowSchemeIDs {
			ids[id] = true
		}
		for _, projectID := range request.ProjectIDs {
			project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectID)
			if err != nil {
				continue
			}
			scheme, err := h.Store.WorkflowSchemeForProject(r.Context(), workspaceID, project.ID)
			if err == nil {
				ids[scheme.ID] = true
			}
		}
		values := make([]map[string]any, 0, len(ids))
		for id := range ids {
			scheme, err := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, id, false)
			if err == nil {
				bean, beanErr := h.workflowSchemeReadBean(r, workspaceID, scheme)
				if beanErr != nil {
					workflowSchemeAPIError(w, beanErr)
					return true
				}
				values = append(values, bean)
			}
		}
		sort.Slice(values, func(i, j int) bool { return values[i]["id"].(string) < values[j]["id"].(string) })
		writeJSON(w, http.StatusOK, values)
		return true
	}
	if path == "/workflowscheme/update/mappings" {
		if r.Method != http.MethodPost {
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
			return true
		}
		var request struct {
			ID                     string                             `json:"id"`
			DefaultWorkflowID      string                             `json:"defaultWorkflowId"`
			WorkflowsForIssueTypes []workflowSchemeAssociationRequest `json:"workflowsForIssueTypes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ID == "" {
			jiraError(w, http.StatusBadRequest, "id and workflowsForIssueTypes are required.")
			return true
		}
		current, err := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, request.ID, false)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The workflow scheme does not exist.")
			return true
		}
		schemeWorkflows := h.workflowIDsFor(r, workspaceID)
		for index := range request.WorkflowsForIssueTypes {
			request.WorkflowsForIssueTypes[index].WorkflowID = schemeWorkflows.toInternal(request.WorkflowsForIssueTypes[index].WorkflowID)
		}
		candidate := workflowSchemeFromAssociations(h.issueTypeIDsFor(r, workspaceID), current, schemeWorkflows.toInternal(request.DefaultWorkflowID), request.WorkflowsForIssueTypes)
		if err := h.Store.ValidateWorkflowSchemeDefinition(r.Context(), workspaceID, candidate); err != nil {
			workflowSchemeAPIError(w, err)
			return true
		}
		projects, err := h.Store.ProjectsForWorkflowScheme(r.Context(), workspaceID, request.ID)
		if err != nil {
			workflowSchemeAPIError(w, err)
			return true
		}
		type impactKey struct{ issueTypeID, statusID, sourceWorkflowID, targetWorkflowID string }
		impactsByKey := make(map[impactKey]models.Status)
		for _, project := range projects {
			impacts, impactErr := h.Store.WorkflowSchemeDefinitionImpact(r.Context(), workspaceID, project.ID, candidate)
			if impactErr != nil {
				workflowSchemeAPIError(w, impactErr)
				return true
			}
			for _, impact := range impacts {
				key := impactKey{impact.IssueTypeID, impact.Status.ID, schemeWorkflowIDAPI(current, impact.IssueTypeID), schemeWorkflowIDAPI(candidate, impact.IssueTypeID)}
				impactsByKey[key] = impact.Status
			}
		}
		byIssueType := make(map[string]map[string]bool)
		byWorkflow := make(map[[2]string]map[string]bool)
		statuses := make(map[string]models.Status)
		for key, status := range impactsByKey {
			if byIssueType[key.issueTypeID] == nil {
				byIssueType[key.issueTypeID] = make(map[string]bool)
			}
			byIssueType[key.issueTypeID][key.statusID] = true
			pair := [2]string{key.sourceWorkflowID, key.targetWorkflowID}
			if byWorkflow[pair] == nil {
				byWorkflow[pair] = make(map[string]bool)
			}
			byWorkflow[pair][key.statusID] = true
			statuses[key.statusID] = status
		}
		wireIDs := h.issueTypeIDsFor(r, workspaceID)
		wireStatuses := h.statusIDsFor(r, workspaceID)
		wireWorkflows := h.workflowIDsFor(r, workspaceID)
		issueTypeMappings := make([]map[string]any, 0, len(byIssueType))
		for issueTypeID, statusSet := range byIssueType {
			issueTypeMappings = append(issueTypeMappings, map[string]any{"issueTypeId": wireIDs.toWire(issueTypeID), "statusIds": wireStatuses.allToWire(sortedSet(statusSet))})
		}
		sort.Slice(issueTypeMappings, func(i, j int) bool {
			return issueTypeMappings[i]["issueTypeId"].(string) < issueTypeMappings[j]["issueTypeId"].(string)
		})
		workflowMappings := make([]map[string]any, 0, len(byWorkflow))
		for pair, statusSet := range byWorkflow {
			workflowMappings = append(workflowMappings, map[string]any{"sourceWorkflowId": wireWorkflows.toWire(pair[0]), "targetWorkflowId": wireWorkflows.toWire(pair[1]), "statusIds": wireStatuses.allToWire(sortedSet(statusSet))})
		}
		sort.Slice(workflowMappings, func(i, j int) bool {
			left, right := workflowMappings[i], workflowMappings[j]
			return left["sourceWorkflowId"].(string)+"\x00"+left["targetWorkflowId"].(string) < right["sourceWorkflowId"].(string)+"\x00"+right["targetWorkflowId"].(string)
		})
		statusValues := make([]map[string]any, 0, len(statuses))
		for _, status := range statuses {
			statusValues = append(statusValues, map[string]any{"id": statusWireID(status), "name": status.Name, "category": status.Category})
		}
		sort.Slice(statusValues, func(i, j int) bool { return statusValues[i]["id"].(string) < statusValues[j]["id"].(string) })
		writeJSON(w, http.StatusOK, map[string]any{"statusMappingsByIssueTypes": issueTypeMappings, "statusMappingsByWorkflows": workflowMappings, "statuses": statusValues, "statusesPerWorkflow": []any{}})
		return true
	}
	if path == "/workflowscheme/update" {
		if r.Method != http.MethodPost {
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
			return true
		}
		var request struct {
			ID                string `json:"id"`
			Name              string `json:"name"`
			Description       string `json:"description"`
			DefaultWorkflowID string `json:"defaultWorkflowId"`
			Version           struct {
				VersionNumber int `json:"versionNumber"`
			} `json:"version"`
			WorkflowsForIssueTypes            []workflowSchemeAssociationRequest   `json:"workflowsForIssueTypes"`
			StatusMappingsByIssueTypeOverride []mappingsByIssueTypeOverrideRequest `json:"statusMappingsByIssueTypeOverride"`
			StatusMappingsByWorkflows         []mappingsByWorkflowRequest          `json:"statusMappingsByWorkflows"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ID == "" || request.Name == "" || request.Version.VersionNumber < 1 {
			jiraError(w, http.StatusBadRequest, "id, name, description, and version are required.")
			return true
		}
		current, err := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, request.ID, false)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "The workflow scheme does not exist.")
			return true
		}
		schemeWorkflows := h.workflowIDsFor(r, workspaceID)
		for index := range request.WorkflowsForIssueTypes {
			request.WorkflowsForIssueTypes[index].WorkflowID = schemeWorkflows.toInternal(request.WorkflowsForIssueTypes[index].WorkflowID)
		}
		candidate := workflowSchemeFromAssociations(h.issueTypeIDsFor(r, workspaceID), current, schemeWorkflows.toInternal(request.DefaultWorkflowID), request.WorkflowsForIssueTypes)
		candidate.Name, candidate.Description = request.Name, request.Description
		statusMappings, err := h.workflowStatusMappings(r, workspaceID, current, candidate, request.StatusMappingsByIssueTypeOverride, request.StatusMappingsByWorkflows)
		if err != nil {
			workflowSchemeAPIError(w, err)
			return true
		}
		task, err := h.Store.UpdatePublishedWorkflowSchemeTask(r.Context(), workspaceID, userID, candidate, statusMappings, request.Version.VersionNumber)
		if err != nil {
			workflowSchemeAPIError(w, err)
			return true
		}
		location := h.BaseURL + "/rest/api/3/task/" + task.ID
		w.Header().Set("Location", location)
		writeJSON(w, http.StatusSeeOther, h.apiTaskBean(task))
		return true
	}
	return false
}

func schemeWorkflowIDAPI(scheme workflow.Scheme, issueTypeID string) string {
	if id := scheme.IssueTypeMappings[issueTypeID]; id != "" {
		return id
	}
	return scheme.DefaultWorkflowID
}

func sortedSet(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
