package api3

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/e6qu/zzira/internal/workflow"
)

type workflowPreviewRequest struct {
	ProjectID     string   `json:"projectId"`
	WorkflowIDs   []string `json:"workflowIds"`
	WorkflowNames []string `json:"workflowNames"`
	IssueTypeIDs  []string `json:"issueTypeIds"`
}

type workflowPreviewItem struct {
	Workflow   workflow.Workflow
	IssueTypes []string
}

func workflowPreviewBean(item workflowPreviewItem, projectID string) map[string]any {
	statusIDs := workflowStatusReferences(item.Workflow)
	statuses := make([]map[string]any, 0, len(statusIDs))
	for statusID := range statusIDs {
		statuses = append(statuses, map[string]any{"statusReference": statusID, "deprecated": false})
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i]["statusReference"].(string) < statuses[j]["statusReference"].(string)
	})
	transitions := make([]map[string]any, 0, len(item.Workflow.Transitions))
	for _, transition := range item.Workflow.Transitions {
		links := make([]map[string]any, 0, len(transition.From))
		for _, from := range transition.From {
			links = append(links, map[string]any{"fromStatusReference": from})
		}
		transitions = append(transitions, map[string]any{
			"id": transition.ID, "name": transition.Name, "description": "", "type": "DIRECTED",
			"toStatusReference": transition.To, "links": links, "actions": []any{}, "validators": []any{}, "triggers": []any{},
		})
	}
	queryContext := []map[string]any{}
	if len(item.IssueTypes) > 0 {
		queryContext = append(queryContext, map[string]any{"project": projectID, "issueTypes": item.IssueTypes})
	}
	return map[string]any{
		"id": item.Workflow.ID, "name": item.Workflow.Name, "description": "",
		"scope": map[string]string{"type": "GLOBAL"}, "statuses": statuses, "transitions": transitions,
		"queryContext": queryContext,
		"version":      map[string]any{"id": item.Workflow.ID, "versionNumber": item.Workflow.Version},
	}
}

func (h *Handler) workflowPreview(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	var request workflowPreviewRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		jiraError(w, http.StatusBadRequest, "request body is invalid")
		return
	}
	if request.ProjectID == "" || len(request.WorkflowIDs)+len(request.WorkflowNames)+len(request.IssueTypeIDs) == 0 || len(request.WorkflowIDs) > 25 || len(request.WorkflowNames) > 25 || len(request.IssueTypeIDs) > 25 {
		jiraError(w, http.StatusBadRequest, "projectId and between 1 and 25 lookup values are required")
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, request.ProjectID)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "projectId is invalid")
		return
	}
	all, err := h.Store.ListWorkflows(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	byID := make(map[string]workflow.Workflow, len(all))
	byName := make(map[string]workflow.Workflow, len(all))
	for _, item := range all {
		byID[item.ID], byName[strings.ToLower(item.Name)] = item, item
	}
	items := make([]workflowPreviewItem, 0)
	positions := make(map[string]int)
	add := func(item workflow.Workflow, issueTypeID string) {
		if index, exists := positions[item.ID]; exists {
			if issueTypeID != "" {
				items[index].IssueTypes = append(items[index].IssueTypes, issueTypeID)
			}
			return
		}
		positions[item.ID] = len(items)
		entry := workflowPreviewItem{Workflow: item}
		if issueTypeID != "" {
			entry.IssueTypes = []string{issueTypeID}
		}
		items = append(items, entry)
	}
	ensureProjectUsage := func(item workflow.Workflow) (bool, error) {
		projects, err := h.Store.WorkflowProjectUsages(r.Context(), workspaceID, item.ID)
		return containsWorkflowUsage(projects, project.ID), err
	}
	for _, workflowID := range request.WorkflowIDs {
		item, exists := byID[workflowID]
		used := false
		if exists {
			used, err = ensureProjectUsage(item)
			if err != nil {
				jiraError(w, http.StatusInternalServerError, "internal error")
				return
			}
		}
		if !exists || !used {
			jiraError(w, http.StatusNotFound, "A requested workflow is not associated with the project")
			return
		}
		add(item, "")
	}
	for _, name := range request.WorkflowNames {
		item, exists := byName[strings.ToLower(name)]
		used := false
		if exists {
			used, err = ensureProjectUsage(item)
			if err != nil {
				jiraError(w, http.StatusInternalServerError, "internal error")
				return
			}
		}
		if !exists || !used {
			jiraError(w, http.StatusNotFound, "A requested workflow is not associated with the project")
			return
		}
		add(item, "")
	}
	issueTypes, err := h.Store.IssueTypes(r.Context())
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	knownIssueTypes := make(map[string]bool, len(issueTypes))
	for _, issueType := range issueTypes {
		knownIssueTypes[issueType.ID] = true
	}
	for _, issueTypeID := range request.IssueTypeIDs {
		if !knownIssueTypes[issueTypeID] {
			jiraError(w, http.StatusBadRequest, "issueTypeIds contains an invalid issue type")
			return
		}
		item, err := h.Store.WorkflowForProjectAndIssueType(r.Context(), project.ID, issueTypeID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		add(item, issueTypeID)
	}
	workflowValues := make([]map[string]any, 0, len(items))
	workflows := make([]workflow.Workflow, 0, len(items))
	for _, item := range items {
		workflowValues = append(workflowValues, workflowPreviewBean(item, project.ID))
		workflows = append(workflows, item.Workflow)
	}
	statuses, err := h.workflowResponseStatuses(r, workspaceID, workflows)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	for _, status := range statuses {
		status["rawName"] = status["name"]
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": workflowValues, "statuses": statuses})
}
