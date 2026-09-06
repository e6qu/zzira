package api3

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

func workflowIDForName(workflows []workflow.Workflow, name string) string {
	for _, item := range workflows {
		if item.ID == name || strings.EqualFold(item.Name, name) {
			return item.ID
		}
	}
	return ""
}

func workflowNameForID(workflows []workflow.Workflow, id string) string {
	for _, item := range workflows {
		if item.ID == id {
			return item.Name
		}
	}
	return ""
}

func cloneSchemeMappings(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for issueTypeID, workflowID := range source {
		result[issueTypeID] = workflowID
	}
	return result
}

func (h *Handler) workflowSchemeDraft(w http.ResponseWriter, r *http.Request, workspaceID, schemeID string) (workflow.Scheme, bool) {
	scheme, err := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, schemeID, true)
	if err != nil || !scheme.HasDraft {
		jiraError(w, http.StatusNotFound, "The workflow scheme draft does not exist.")
		return workflow.Scheme{}, false
	}
	return scheme, true
}

func (h *Handler) saveWorkflowSchemeDraft(w http.ResponseWriter, r *http.Request, workspaceID, userID string, scheme workflow.Scheme) bool {
	if err := h.Store.SaveWorkflowSchemeDraft(r.Context(), workspaceID, userID, scheme); err != nil {
		workflowSchemeAPIError(w, err)
		return false
	}
	return true
}

func (h *Handler) workflowSchemeSubresourceRoute(w http.ResponseWriter, r *http.Request, workspaceID, userID string, parts []string) bool {
	if len(parts) < 2 {
		return false
	}
	schemeID := parts[0]
	if len(parts) == 2 && parts[1] == "createdraft" {
		if r.Method != http.MethodPost {
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
			return true
		}
		scheme, err := h.Store.CreateWorkflowSchemeDraft(r.Context(), workspaceID, userID, schemeID)
		if err != nil {
			workflowSchemeAPIError(w, err)
			return true
		}
		writeJSON(w, http.StatusCreated, h.workflowSchemeBean(r, workspaceID, scheme))
		return true
	}
	if parts[1] != "draft" {
		return false
	}
	if len(parts) == 2 {
		switch r.Method {
		case http.MethodGet:
			scheme, ok := h.workflowSchemeDraft(w, r, workspaceID, schemeID)
			if ok {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, scheme))
			}
		case http.MethodPut:
			scheme, ok := h.workflowSchemeDraft(w, r, workspaceID, schemeID)
			if !ok {
				return true
			}
			var request struct {
				Name              *string           `json:"name"`
				Description       *string           `json:"description"`
				DefaultWorkflow   *string           `json:"defaultWorkflow"`
				IssueTypeMappings map[string]string `json:"issueTypeMappings"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				jiraError(w, http.StatusBadRequest, "Invalid workflow scheme draft request.")
				return true
			}
			workflows, err := h.Store.ListWorkflows(r.Context(), workspaceID)
			if err != nil {
				workflowSchemeAPIError(w, err)
				return true
			}
			if request.Name != nil {
				scheme.Name = *request.Name
			}
			if request.Description != nil {
				scheme.Description = *request.Description
			}
			if request.DefaultWorkflow != nil {
				scheme.DefaultWorkflowID = workflowIDForName(workflows, *request.DefaultWorkflow)
				if scheme.DefaultWorkflowID == "" {
					jiraError(w, http.StatusBadRequest, "The default workflow does not exist.")
					return true
				}
			}
			if request.IssueTypeMappings != nil {
				scheme.IssueTypeMappings = make(map[string]string, len(request.IssueTypeMappings))
				for issueTypeID, workflowName := range request.IssueTypeMappings {
					workflowID := workflowIDForName(workflows, workflowName)
					if workflowID == "" {
						jiraError(w, http.StatusBadRequest, "A mapped workflow does not exist.")
						return true
					}
					scheme.IssueTypeMappings[issueTypeID] = workflowID
				}
			}
			if h.saveWorkflowSchemeDraft(w, r, workspaceID, userID, scheme) {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, scheme))
			}
		case http.MethodDelete:
			if err := h.Store.DiscardWorkflowSchemeDraft(r.Context(), workspaceID, userID, schemeID); err != nil {
				if errors.Is(err, store.ErrAdminConflict) {
					jiraError(w, http.StatusNotFound, "The workflow scheme draft does not exist.")
				} else {
					workflowSchemeAPIError(w, err)
				}
				return true
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return true
	}
	if len(parts) == 3 && parts[2] == "publish" {
		if r.Method != http.MethodPost {
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
			return true
		}
		var request struct {
			StatusMappings []struct{} `json:"statusMappings"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			jiraError(w, http.StatusBadRequest, "Invalid workflow scheme publish request.")
			return true
		}
		if len(request.StatusMappings) > 0 {
			jiraError(w, http.StatusBadRequest, "Status mappings are only accepted when assigned project statuses require migration.")
			return true
		}
		if r.URL.Query().Get("validateOnly") == "true" {
			projects, err := h.Store.ProjectsForWorkflowScheme(r.Context(), workspaceID, schemeID)
			if err != nil {
				workflowSchemeAPIError(w, err)
				return true
			}
			for _, project := range projects {
				impacts, err := h.Store.WorkflowSchemeImpact(r.Context(), workspaceID, project.ID, schemeID, true)
				if err != nil || len(impacts) > 0 {
					jiraError(w, http.StatusBadRequest, "The draft requires status mappings before it can be published.")
					return true
				}
			}
			w.WriteHeader(http.StatusNoContent)
			return true
		}
		task, err := h.Store.PublishWorkflowSchemeDraftTask(r.Context(), workspaceID, userID, schemeID)
		if err != nil {
			workflowSchemeAPIError(w, err)
			return true
		}
		location := h.BaseURL + "/rest/api/3/task/" + task.ID
		w.Header().Set("Location", location)
		writeJSON(w, http.StatusSeeOther, h.apiTaskBean(task))
		return true
	}
	if len(parts) == 3 && parts[2] == "default" {
		scheme, ok := h.workflowSchemeDraft(w, r, workspaceID, schemeID)
		if !ok {
			return true
		}
		workflows, err := h.Store.ListWorkflows(r.Context(), workspaceID)
		if err != nil {
			workflowSchemeAPIError(w, err)
			return true
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"workflow": workflowNameForID(workflows, scheme.DefaultWorkflowID)})
		case http.MethodPut:
			var request struct {
				Workflow string `json:"workflow"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				jiraError(w, http.StatusBadRequest, "Invalid default workflow request.")
				return true
			}
			scheme.DefaultWorkflowID = workflowIDForName(workflows, request.Workflow)
			if scheme.DefaultWorkflowID == "" {
				jiraError(w, http.StatusBadRequest, "The workflow does not exist.")
				return true
			}
			if h.saveWorkflowSchemeDraft(w, r, workspaceID, userID, scheme) {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, scheme))
			}
		case http.MethodDelete:
			scheme.DefaultWorkflowID = workflow.Default().ID
			if h.saveWorkflowSchemeDraft(w, r, workspaceID, userID, scheme) {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, scheme))
			}
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return true
	}
	if len(parts) == 4 && parts[2] == "issuetype" {
		scheme, ok := h.workflowSchemeDraft(w, r, workspaceID, schemeID)
		if !ok {
			return true
		}
		issueTypeID := parts[3]
		workflows, err := h.Store.ListWorkflows(r.Context(), workspaceID)
		if err != nil {
			workflowSchemeAPIError(w, err)
			return true
		}
		switch r.Method {
		case http.MethodGet:
			workflowID := scheme.IssueTypeMappings[issueTypeID]
			if workflowID == "" {
				jiraError(w, http.StatusNotFound, "The issue type mapping does not exist.")
				return true
			}
			writeJSON(w, http.StatusOK, map[string]any{"issueType": issueTypeID, "workflow": workflowNameForID(workflows, workflowID)})
		case http.MethodPut:
			var request struct {
				Workflow string `json:"workflow"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				jiraError(w, http.StatusBadRequest, "Invalid issue type workflow request.")
				return true
			}
			workflowID := workflowIDForName(workflows, request.Workflow)
			if workflowID == "" {
				jiraError(w, http.StatusBadRequest, "The workflow does not exist.")
				return true
			}
			scheme.IssueTypeMappings = cloneSchemeMappings(scheme.IssueTypeMappings)
			scheme.IssueTypeMappings[issueTypeID] = workflowID
			if h.saveWorkflowSchemeDraft(w, r, workspaceID, userID, scheme) {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, scheme))
			}
		case http.MethodDelete:
			scheme.IssueTypeMappings = cloneSchemeMappings(scheme.IssueTypeMappings)
			if _, exists := scheme.IssueTypeMappings[issueTypeID]; !exists {
				jiraError(w, http.StatusNotFound, "The issue type mapping does not exist.")
				return true
			}
			delete(scheme.IssueTypeMappings, issueTypeID)
			if h.saveWorkflowSchemeDraft(w, r, workspaceID, userID, scheme) {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, scheme))
			}
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return true
	}
	if len(parts) == 3 && parts[2] == "workflow" {
		scheme, ok := h.workflowSchemeDraft(w, r, workspaceID, schemeID)
		if !ok {
			return true
		}
		workflows, err := h.Store.ListWorkflows(r.Context(), workspaceID)
		if err != nil {
			workflowSchemeAPIError(w, err)
			return true
		}
		workflowName := r.URL.Query().Get("workflowName")
		workflowID := workflowIDForName(workflows, workflowName)
		if workflowID == "" {
			jiraError(w, http.StatusBadRequest, "workflowName must identify a workflow.")
			return true
		}
		switch r.Method {
		case http.MethodGet:
			issueTypes := make([]string, 0)
			for issueTypeID, mappedWorkflowID := range scheme.IssueTypeMappings {
				if mappedWorkflowID == workflowID {
					issueTypes = append(issueTypes, issueTypeID)
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"workflow": workflowNameForID(workflows, workflowID), "issueTypes": issueTypes, "defaultMapping": scheme.DefaultWorkflowID == workflowID})
		case http.MethodPut:
			var request struct {
				Workflow       string   `json:"workflow"`
				IssueTypes     []string `json:"issueTypes"`
				DefaultMapping bool     `json:"defaultMapping"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				jiraError(w, http.StatusBadRequest, "Invalid workflow mapping request.")
				return true
			}
			targetWorkflowID := workflowID
			if request.Workflow != "" {
				targetWorkflowID = workflowIDForName(workflows, request.Workflow)
				if targetWorkflowID == "" {
					jiraError(w, http.StatusBadRequest, "The target workflow does not exist.")
					return true
				}
			}
			scheme.IssueTypeMappings = cloneSchemeMappings(scheme.IssueTypeMappings)
			for issueTypeID, mappedWorkflowID := range scheme.IssueTypeMappings {
				if mappedWorkflowID == workflowID {
					delete(scheme.IssueTypeMappings, issueTypeID)
				}
			}
			for _, issueTypeID := range request.IssueTypes {
				scheme.IssueTypeMappings[issueTypeID] = targetWorkflowID
			}
			if request.DefaultMapping {
				scheme.DefaultWorkflowID = targetWorkflowID
			}
			if h.saveWorkflowSchemeDraft(w, r, workspaceID, userID, scheme) {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, scheme))
			}
		case http.MethodDelete:
			scheme.IssueTypeMappings = cloneSchemeMappings(scheme.IssueTypeMappings)
			for issueTypeID, mappedWorkflowID := range scheme.IssueTypeMappings {
				if mappedWorkflowID == workflowID {
					delete(scheme.IssueTypeMappings, issueTypeID)
				}
			}
			if scheme.DefaultWorkflowID == workflowID {
				scheme.DefaultWorkflowID = workflow.Default().ID
			}
			if h.saveWorkflowSchemeDraft(w, r, workspaceID, userID, scheme) {
				w.WriteHeader(http.StatusNoContent)
			}
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return true
	}
	return false
}
