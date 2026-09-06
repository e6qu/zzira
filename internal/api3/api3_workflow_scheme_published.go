package api3

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/workflow"
)

func (h *Handler) savePublishedWorkflowScheme(w http.ResponseWriter, r *http.Request, workspaceID, userID string, scheme workflow.Scheme) bool {
	if err := h.Store.SavePublishedWorkflowScheme(r.Context(), workspaceID, userID, scheme); err != nil {
		workflowSchemeAPIError(w, err)
		return false
	}
	return true
}

func (h *Handler) workflowSchemeProjectUsages(w http.ResponseWriter, r *http.Request, workspaceID, schemeID string) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, err := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, schemeID, false); err != nil {
		jiraError(w, http.StatusNotFound, "The workflow scheme does not exist.")
		return
	}
	projects, err := h.Store.ProjectsForWorkflowScheme(r.Context(), workspaceID, schemeID)
	if err != nil {
		workflowSchemeAPIError(w, err)
		return
	}
	maxResults := 50
	if value := r.URL.Query().Get("maxResults"); value != "" {
		maxResults, err = strconv.Atoi(value)
		if err != nil || maxResults < 1 || maxResults > 200 {
			jiraError(w, http.StatusBadRequest, "maxResults must be between 1 and 200.")
			return
		}
	}
	start := 0
	if token := r.URL.Query().Get("nextPageToken"); token != "" {
		raw, decodeErr := base64.RawURLEncoding.DecodeString(token)
		if decodeErr != nil {
			jiraError(w, http.StatusBadRequest, "nextPageToken is invalid.")
			return
		}
		start, err = strconv.Atoi(string(raw))
		if err != nil || start < 0 || start > len(projects) {
			jiraError(w, http.StatusBadRequest, "nextPageToken is invalid.")
			return
		}
	}
	end := start + maxResults
	if end > len(projects) {
		end = len(projects)
	}
	values := make([]map[string]any, 0, end-start)
	for _, project := range projects[start:end] {
		values = append(values, map[string]any{"id": project.ID})
	}
	page := map[string]any{"values": values}
	if end < len(projects) {
		page["nextPageToken"] = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflowSchemeId": schemeID, "projects": page})
}

func (h *Handler) workflowSchemePublishedSubresourceRoute(w http.ResponseWriter, r *http.Request, workspaceID, userID string, parts []string) bool {
	if len(parts) < 2 {
		return false
	}
	schemeID := parts[0]
	if len(parts) == 2 && parts[1] == "projectUsages" {
		h.workflowSchemeProjectUsages(w, r, workspaceID, schemeID)
		return true
	}
	if len(parts) == 2 && parts[1] == "default" {
		scheme, err := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, schemeID, false)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The workflow scheme does not exist.")
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
			if h.savePublishedWorkflowScheme(w, r, workspaceID, userID, scheme) {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, scheme))
			}
		case http.MethodDelete:
			scheme.DefaultWorkflowID = workflow.Default().ID
			if h.savePublishedWorkflowScheme(w, r, workspaceID, userID, scheme) {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, scheme))
			}
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return true
	}
	if len(parts) == 3 && parts[1] == "issuetype" {
		scheme, err := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, schemeID, false)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The workflow scheme does not exist.")
			return true
		}
		issueTypeID := parts[2]
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
			if h.savePublishedWorkflowScheme(w, r, workspaceID, userID, scheme) {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, scheme))
			}
		case http.MethodDelete:
			scheme.IssueTypeMappings = cloneSchemeMappings(scheme.IssueTypeMappings)
			if _, exists := scheme.IssueTypeMappings[issueTypeID]; !exists {
				jiraError(w, http.StatusNotFound, "The issue type mapping does not exist.")
				return true
			}
			delete(scheme.IssueTypeMappings, issueTypeID)
			if h.savePublishedWorkflowScheme(w, r, workspaceID, userID, scheme) {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, scheme))
			}
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return true
	}
	if len(parts) == 2 && parts[1] == "workflow" {
		scheme, err := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, schemeID, false)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The workflow scheme does not exist.")
			return true
		}
		workflows, err := h.Store.ListWorkflows(r.Context(), workspaceID)
		if err != nil {
			workflowSchemeAPIError(w, err)
			return true
		}
		workflowID := workflowIDForName(workflows, r.URL.Query().Get("workflowName"))
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
			if h.savePublishedWorkflowScheme(w, r, workspaceID, userID, scheme) {
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
			if h.savePublishedWorkflowScheme(w, r, workspaceID, userID, scheme) {
				w.WriteHeader(http.StatusNoContent)
			}
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return true
	}
	return false
}
