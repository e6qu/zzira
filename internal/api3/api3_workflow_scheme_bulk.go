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

func (h *Handler) workflowStatusMappings(r *http.Request, current, candidate workflow.Scheme, byIssueType []mappingsByIssueTypeOverrideRequest, byWorkflow []mappingsByWorkflowRequest) ([]store.WorkflowStatusMapping, error) {
	issueTypes, err := h.Store.IssueTypes(r.Context())
	if err != nil {
		return nil, err
	}
	var mappings []store.WorkflowStatusMapping
	for _, workflowMapping := range byWorkflow {
		for _, issueType := range issueTypes {
			if schemeWorkflowIDAPI(current, issueType.ID) != workflowMapping.OldWorkflowID || schemeWorkflowIDAPI(candidate, issueType.ID) != workflowMapping.NewWorkflowID {
				continue
			}
			for _, mapping := range workflowMapping.StatusMappings {
				mappings = append(mappings, store.WorkflowStatusMapping{IssueTypeID: issueType.ID, OldStatusID: mapping.OldStatusID, NewStatusID: mapping.NewStatusID})
			}
		}
	}
	for _, override := range byIssueType {
		for _, mapping := range override.StatusMappings {
			mappings = append(mappings, store.WorkflowStatusMapping{IssueTypeID: override.IssueTypeID, OldStatusID: mapping.OldStatusID, NewStatusID: mapping.NewStatusID})
		}
	}
	return mappings, nil
}

func workflowSchemeFromAssociations(current workflow.Scheme, defaultWorkflowID string, associations []workflowSchemeAssociationRequest) workflow.Scheme {
	current.DefaultWorkflowID = defaultWorkflowID
	if current.DefaultWorkflowID == "" {
		current.DefaultWorkflowID = workflow.Default().ID
	}
	current.IssueTypeMappings = make(map[string]string)
	for _, association := range associations {
		for _, issueTypeID := range association.IssueTypeIDs {
			current.IssueTypeMappings[issueTypeID] = association.WorkflowID
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
		"id": item.ID, "name": item.Name, "description": "",
		"version": map[string]any{"id": item.ID + ":" + strconv.Itoa(version), "versionNumber": version},
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
	mappings := make([]map[string]any, 0, len(issueTypesByWorkflow))
	for workflowID, issueTypeIDs := range issueTypesByWorkflow {
		sort.Strings(issueTypeIDs)
		mappings = append(mappings, map[string]any{"workflow": workflowMetadataBean(byID[workflowID]), "issueTypeIds": issueTypeIDs})
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
		candidate := workflowSchemeFromAssociations(current, request.DefaultWorkflowID, request.WorkflowsForIssueTypes)
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
		issueTypeMappings := make([]map[string]any, 0, len(byIssueType))
		for issueTypeID, statusSet := range byIssueType {
			issueTypeMappings = append(issueTypeMappings, map[string]any{"issueTypeId": issueTypeID, "statusIds": sortedSet(statusSet)})
		}
		sort.Slice(issueTypeMappings, func(i, j int) bool {
			return issueTypeMappings[i]["issueTypeId"].(string) < issueTypeMappings[j]["issueTypeId"].(string)
		})
		workflowMappings := make([]map[string]any, 0, len(byWorkflow))
		for pair, statusSet := range byWorkflow {
			workflowMappings = append(workflowMappings, map[string]any{"sourceWorkflowId": pair[0], "targetWorkflowId": pair[1], "statusIds": sortedSet(statusSet)})
		}
		sort.Slice(workflowMappings, func(i, j int) bool {
			left, right := workflowMappings[i], workflowMappings[j]
			return left["sourceWorkflowId"].(string)+"\x00"+left["targetWorkflowId"].(string) < right["sourceWorkflowId"].(string)+"\x00"+right["targetWorkflowId"].(string)
		})
		statusValues := make([]map[string]any, 0, len(statuses))
		for _, status := range statuses {
			statusValues = append(statusValues, map[string]any{"id": status.ID, "name": status.Name, "category": status.Category})
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
		candidate := workflowSchemeFromAssociations(current, request.DefaultWorkflowID, request.WorkflowsForIssueTypes)
		candidate.Name, candidate.Description = request.Name, request.Description
		statusMappings, err := h.workflowStatusMappings(r, current, candidate, request.StatusMappingsByIssueTypeOverride, request.StatusMappingsByWorkflows)
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
