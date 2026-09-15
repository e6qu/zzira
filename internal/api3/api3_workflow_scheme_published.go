package api3

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/workflow"
)

// saveWorkflowSchemeChange applies an edit through Store.UpdateWorkflowScheme
// and returns the scheme as it now reads: its draft when the draft changed.
func (h *Handler) saveWorkflowSchemeChange(w http.ResponseWriter, r *http.Request, workspaceID, userID string, scheme workflow.Scheme, updateDraftIfNeeded bool) (workflow.Scheme, bool) {
	draft, err := h.Store.UpdateWorkflowScheme(r.Context(), workspaceID, userID, scheme, updateDraftIfNeeded)
	if err != nil {
		workflowSchemeAPIError(w, err)
		return workflow.Scheme{}, false
	}
	saved, err := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, scheme.ID, draft)
	if err != nil {
		workflowSchemeAPIError(w, err)
		return workflow.Scheme{}, false
	}
	return saved, true
}

// schemeForChange loads what an edit starts from: the draft of an active
// scheme when the edit may go to its draft, otherwise the published scheme.
func (h *Handler) schemeForChange(w http.ResponseWriter, r *http.Request, workspaceID, schemeID string, updateDraftIfNeeded bool) (workflow.Scheme, bool) {
	scheme, err := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, schemeID, updateDraftIfNeeded)
	if err != nil {
		jiraError(w, http.StatusNotFound, "The workflow scheme does not exist.")
		return workflow.Scheme{}, false
	}
	return scheme, true
}

func queryUpdateDraftIfNeeded(r *http.Request) bool {
	return strings.EqualFold(r.URL.Query().Get("updateDraftIfNeeded"), "true")
}

func (h *Handler) workflowSchemeProjectUsages(w http.ResponseWriter, r *http.Request, workspaceID, schemeID string) {
	if r.Method != http.MethodGet {
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	usageScheme, err := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, schemeID, false)
	if err != nil {
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
	writeJSON(w, http.StatusOK, map[string]any{"workflowSchemeId": strconv.FormatInt(usageScheme.JiraID, 10), "projects": page})
}

func (h *Handler) workflowSchemePublishedSubresourceRoute(w http.ResponseWriter, r *http.Request, workspaceID, userID string, parts []string) bool {
	if len(parts) < 2 {
		return false
	}
	schemeID := h.Store.WorkflowSchemeIDByRef(r.Context(), workspaceID, parts[0])
	if len(parts) == 2 && parts[1] == "projectUsages" {
		h.workflowSchemeProjectUsages(w, r, workspaceID, schemeID)
		return true
	}
	if len(parts) == 2 && parts[1] == "default" {
		workflows, err := h.Store.ListGlobalWorkflows(r.Context(), workspaceID)
		if err != nil {
			workflowSchemeAPIError(w, err)
			return true
		}
		switch r.Method {
		case http.MethodGet:
			scheme, ok := h.schemeForChange(w, r, workspaceID, schemeID, r.URL.Query().Get("returnDraftIfExists") == "true")
			if ok {
				writeJSON(w, http.StatusOK, map[string]any{"workflow": workflowNameForID(workflows, scheme.DefaultWorkflowID)})
			}
		case http.MethodPut:
			var request struct {
				Workflow            string `json:"workflow"`
				UpdateDraftIfNeeded bool   `json:"updateDraftIfNeeded"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				jiraError(w, http.StatusBadRequest, "Invalid default workflow request.")
				return true
			}
			scheme, ok := h.schemeForChange(w, r, workspaceID, schemeID, request.UpdateDraftIfNeeded)
			if !ok {
				return true
			}
			if scheme.DefaultWorkflowID = workflowIDForName(workflows, request.Workflow); scheme.DefaultWorkflowID == "" {
				jiraError(w, http.StatusBadRequest, "The workflow does not exist.")
				return true
			}
			if saved, ok := h.saveWorkflowSchemeChange(w, r, workspaceID, userID, scheme, request.UpdateDraftIfNeeded); ok {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, saved))
			}
		case http.MethodDelete:
			updateDraft := queryUpdateDraftIfNeeded(r)
			scheme, ok := h.schemeForChange(w, r, workspaceID, schemeID, updateDraft)
			if !ok {
				return true
			}
			scheme.DefaultWorkflowID = workflow.Default().ID
			if saved, ok := h.saveWorkflowSchemeChange(w, r, workspaceID, userID, scheme, updateDraft); ok {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, saved))
			}
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return true
	}
	if len(parts) == 3 && parts[1] == "issuetype" {
		ids := h.issueTypeIDsFor(r, workspaceID)
		issueTypeID := ids.toInternal(parts[2])
		workflows, err := h.Store.ListGlobalWorkflows(r.Context(), workspaceID)
		if err != nil {
			workflowSchemeAPIError(w, err)
			return true
		}
		switch r.Method {
		case http.MethodGet:
			scheme, ok := h.schemeForChange(w, r, workspaceID, schemeID, r.URL.Query().Get("returnDraftIfExists") == "true")
			if !ok {
				return true
			}
			workflowID := scheme.IssueTypeMappings[issueTypeID]
			if workflowID == "" {
				jiraError(w, http.StatusNotFound, "The issue type mapping does not exist.")
				return true
			}
			writeJSON(w, http.StatusOK, map[string]any{"issueType": ids.toWire(issueTypeID), "workflow": workflowNameForID(workflows, workflowID)})
		case http.MethodPut:
			var request struct {
				Workflow            string `json:"workflow"`
				UpdateDraftIfNeeded bool   `json:"updateDraftIfNeeded"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				jiraError(w, http.StatusBadRequest, "Invalid issue type workflow request.")
				return true
			}
			scheme, ok := h.schemeForChange(w, r, workspaceID, schemeID, request.UpdateDraftIfNeeded)
			if !ok {
				return true
			}
			workflowID := workflowIDForName(workflows, request.Workflow)
			if workflowID == "" {
				jiraError(w, http.StatusBadRequest, "The workflow does not exist.")
				return true
			}
			scheme.IssueTypeMappings = cloneSchemeMappings(scheme.IssueTypeMappings)
			scheme.IssueTypeMappings[issueTypeID] = workflowID
			if saved, ok := h.saveWorkflowSchemeChange(w, r, workspaceID, userID, scheme, request.UpdateDraftIfNeeded); ok {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, saved))
			}
		case http.MethodDelete:
			updateDraft := queryUpdateDraftIfNeeded(r)
			scheme, ok := h.schemeForChange(w, r, workspaceID, schemeID, updateDraft)
			if !ok {
				return true
			}
			scheme.IssueTypeMappings = cloneSchemeMappings(scheme.IssueTypeMappings)
			if _, exists := scheme.IssueTypeMappings[issueTypeID]; !exists {
				jiraError(w, http.StatusNotFound, "The issue type mapping does not exist.")
				return true
			}
			delete(scheme.IssueTypeMappings, issueTypeID)
			if saved, ok := h.saveWorkflowSchemeChange(w, r, workspaceID, userID, scheme, updateDraft); ok {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, saved))
			}
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return true
	}
	if len(parts) == 2 && parts[1] == "workflow" {
		workflows, err := h.Store.ListGlobalWorkflows(r.Context(), workspaceID)
		if err != nil {
			workflowSchemeAPIError(w, err)
			return true
		}
		ids := h.issueTypeIDsFor(r, workspaceID)
		workflowName := r.URL.Query().Get("workflowName")
		workflowID := workflowIDForName(workflows, workflowName)
		if workflowName != "" && workflowID == "" || workflowName == "" && r.Method != http.MethodGet {
			jiraError(w, http.StatusBadRequest, "workflowName must identify a workflow.")
			return true
		}
		switch r.Method {
		case http.MethodGet:
			scheme, ok := h.schemeForChange(w, r, workspaceID, schemeID, r.URL.Query().Get("returnDraftIfExists") == "true")
			if !ok {
				return true
			}
			if workflowName == "" {
				writeJSON(w, http.StatusOK, h.workflowMappingBeans(r, workspaceID, workflows, scheme))
				return true
			}
			issueTypes := make([]string, 0)
			for issueTypeID, mappedWorkflowID := range scheme.IssueTypeMappings {
				if mappedWorkflowID == workflowID {
					issueTypes = append(issueTypes, ids.toWire(issueTypeID))
				}
			}
			sort.Strings(issueTypes)
			writeJSON(w, http.StatusOK, map[string]any{"workflow": workflowNameForID(workflows, workflowID), "issueTypes": issueTypes, "defaultMapping": scheme.DefaultWorkflowID == workflowID})
		case http.MethodPut:
			var request struct {
				Workflow            string   `json:"workflow"`
				IssueTypes          []string `json:"issueTypes"`
				DefaultMapping      bool     `json:"defaultMapping"`
				UpdateDraftIfNeeded bool     `json:"updateDraftIfNeeded"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				jiraError(w, http.StatusBadRequest, "Invalid workflow mapping request.")
				return true
			}
			scheme, ok := h.schemeForChange(w, r, workspaceID, schemeID, request.UpdateDraftIfNeeded)
			if !ok {
				return true
			}
			targetWorkflowID := workflowID
			if request.Workflow != "" {
				if targetWorkflowID = workflowIDForName(workflows, request.Workflow); targetWorkflowID == "" {
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
				scheme.IssueTypeMappings[ids.toInternal(issueTypeID)] = targetWorkflowID
			}
			if request.DefaultMapping {
				scheme.DefaultWorkflowID = targetWorkflowID
			}
			if saved, ok := h.saveWorkflowSchemeChange(w, r, workspaceID, userID, scheme, request.UpdateDraftIfNeeded); ok {
				writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, saved))
			}
		case http.MethodDelete:
			updateDraft := queryUpdateDraftIfNeeded(r)
			scheme, ok := h.schemeForChange(w, r, workspaceID, schemeID, updateDraft)
			if !ok {
				return true
			}
			scheme.IssueTypeMappings = cloneSchemeMappings(scheme.IssueTypeMappings)
			for issueTypeID, mappedWorkflowID := range scheme.IssueTypeMappings {
				if mappedWorkflowID == workflowID {
					delete(scheme.IssueTypeMappings, issueTypeID)
				}
			}
			if scheme.DefaultWorkflowID == workflowID {
				scheme.DefaultWorkflowID = workflow.Default().ID
			}
			if _, ok := h.saveWorkflowSchemeChange(w, r, workspaceID, userID, scheme, updateDraft); ok {
				w.WriteHeader(http.StatusNoContent)
			}
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return true
	}
	return false
}
