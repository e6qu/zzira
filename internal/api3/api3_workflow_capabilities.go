package api3

import (
	"net/http"
)

func workflowCapabilitiesResponse() map[string]any {
	return map[string]any{
		"editorScope":  "GLOBAL",
		"projectTypes": []string{"software", "business"},
		"systemRules": []map[string]any{
			{
				"description": "Restrict a transition to selected users, the assignee, or the reporter.", "incompatibleRuleKeys": []string{},
				"isAvailableForInitialTransition": false, "isVisible": true, "name": "Restrict issue transition",
				"ruleKey": "system:restrict-issue-transition", "ruleType": "Condition",
			},
			{
				"description": "Require a field to have a value before an issue can transition.", "incompatibleRuleKeys": []string{},
				"isAvailableForInitialTransition": false, "isVisible": true, "name": "Validate a field value",
				"ruleKey": "system:validate-field-value", "ruleType": "Validator",
			},
			{
				"description": "Automatically assign an issue after moving it using a transition.", "incompatibleRuleKeys": []string{},
				"isAvailableForInitialTransition": true, "isVisible": true, "name": "Assign an issue",
				"ruleKey": "system:change-assignee", "ruleType": "Function",
			},
		},
		"connectRules": []any{},
		"forgeRules":   []any{},
		"triggerRules": []any{},
	}
}

func (h *Handler) workflowCapabilities(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	workflowID := r.URL.Query().Get("workflowId")
	projectID := r.URL.Query().Get("projectId")
	issueTypeID := r.URL.Query().Get("issueTypeId")
	if workflowID != "" {
		if projectID != "" || issueTypeID != "" {
			jiraError(w, http.StatusBadRequest, "workflowId cannot be combined with projectId or issueTypeId")
			return
		}
		if _, err := h.Store.WorkflowByID(r.Context(), workspaceID, workflowID); err != nil {
			jiraError(w, http.StatusBadRequest, "workflowId is invalid")
			return
		}
		writeJSON(w, http.StatusOK, workflowCapabilitiesResponse())
		return
	}
	if projectID == "" || issueTypeID == "" {
		jiraError(w, http.StatusBadRequest, "workflowId or projectId with issueTypeId is required")
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectID)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "projectId is invalid")
		return
	}
	issueTypes, err := h.Store.IssueTypes(r.Context())
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	issueTypeExists := false
	for _, issueType := range issueTypes {
		if issueType.ID == issueTypeID {
			issueTypeExists = true
			break
		}
	}
	if !issueTypeExists {
		jiraError(w, http.StatusBadRequest, "issueTypeId is invalid")
		return
	}
	if _, err := h.Store.WorkflowForProjectAndIssueType(r.Context(), project.ID, issueTypeID); err != nil {
		jiraError(w, http.StatusBadRequest, "the project workflow could not be resolved")
		return
	}
	response := workflowCapabilitiesResponse()
	writeJSON(w, http.StatusOK, response)
}
