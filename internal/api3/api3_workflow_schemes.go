package api3

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

func (h *Handler) workflowSchemeBean(r *http.Request, workspaceID string, scheme workflow.Scheme) map[string]any {
	workflows, _ := h.Store.ListWorkflows(r.Context(), workspaceID)
	names := make(map[string]string, len(workflows))
	for _, item := range workflows {
		names[item.ID] = item.Name
	}
	mappings := make(map[string]string, len(scheme.IssueTypeMappings))
	for issueTypeID, workflowID := range scheme.IssueTypeMappings {
		mappings[issueTypeID] = names[workflowID]
	}
	return map[string]any{
		"id": scheme.ID, "name": scheme.Name, "description": scheme.Description,
		"defaultWorkflow": names[scheme.DefaultWorkflowID], "issueTypeMappings": mappings,
		"draft": scheme.HasDraft, "self": h.BaseURL + "/rest/api/3/workflowscheme/" + scheme.ID,
	}
}

func resolveSchemeWorkflowNames(workflows []workflow.Workflow, defaultName string, mappings map[string]string) (string, map[string]string, bool) {
	ids := make(map[string]string, len(workflows)*2)
	for _, item := range workflows {
		ids[item.ID], ids[strings.ToLower(item.Name)] = item.ID, item.ID
	}
	defaultID := ids[defaultName]
	if defaultID == "" {
		defaultID = ids[strings.ToLower(defaultName)]
	}
	if defaultID == "" && defaultName == "" {
		defaultID = workflow.Default().ID
	}
	resolved := make(map[string]string, len(mappings))
	for issueTypeID, name := range mappings {
		id := ids[name]
		if id == "" {
			id = ids[strings.ToLower(name)]
		}
		if id == "" {
			return "", nil, false
		}
		resolved[issueTypeID] = id
	}
	return defaultID, resolved, defaultID != ""
}

func workflowSchemeAPIError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrAdminNotFound) {
		jiraError(w, http.StatusNotFound, "The workflow scheme does not exist.")
	} else if errors.Is(err, store.ErrAdminConflict) {
		jiraError(w, http.StatusConflict, err.Error())
	} else if errors.Is(err, store.ErrAdminValidation) {
		jiraError(w, http.StatusBadRequest, err.Error())
	} else {
		jiraError(w, http.StatusInternalServerError, "internal error")
	}
}

func (h *Handler) workflowSchemeRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, userID, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if path == "/workflowscheme" {
		switch r.Method {
		case http.MethodGet:
			schemes, err := h.Store.ListWorkflowSchemes(r.Context(), workspaceID)
			if err != nil {
				workflowSchemeAPIError(w, err)
				return
			}
			values := make([]map[string]any, 0, len(schemes))
			for _, scheme := range schemes {
				values = append(values, h.workflowSchemeBean(r, workspaceID, scheme))
			}
			writeJSON(w, http.StatusOK, map[string]any{"startAt": 0, "maxResults": len(values), "total": len(values), "isLast": true, "values": values})
		case http.MethodPost:
			var request struct {
				Name, Description, DefaultWorkflow string
				IssueTypeMappings                  map[string]string `json:"issueTypeMappings"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				jiraError(w, http.StatusBadRequest, "Invalid workflow scheme request.")
				return
			}
			workflows, err := h.Store.ListWorkflows(r.Context(), workspaceID)
			if err != nil {
				workflowSchemeAPIError(w, err)
				return
			}
			defaultID, mappings, ok := resolveSchemeWorkflowNames(workflows, request.DefaultWorkflow, request.IssueTypeMappings)
			if !ok {
				jiraError(w, http.StatusBadRequest, "A mapped workflow does not exist.")
				return
			}
			scheme, err := h.Store.CreateWorkflowScheme(r.Context(), workspaceID, userID, workflow.Scheme{Name: request.Name, Description: request.Description, DefaultWorkflowID: defaultID, IssueTypeMappings: mappings})
			if err != nil {
				workflowSchemeAPIError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, h.workflowSchemeBean(r, workspaceID, scheme))
		default:
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if path == "/workflowscheme/project/switch" {
		if r.Method != http.MethodPost {
			jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var request struct {
			ProjectID                   string `json:"projectId"`
			TargetSchemeID              string `json:"targetSchemeId"`
			MappingsByIssueTypeOverride []struct {
				IssueTypeID    string `json:"issueTypeId"`
				StatusMappings []struct {
					OldStatusID string `json:"oldStatusId"`
					NewStatusID string `json:"newStatusId"`
				} `json:"statusMappings"`
			} `json:"mappingsByIssueTypeOverride"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ProjectID == "" || request.TargetSchemeID == "" {
			jiraError(w, http.StatusBadRequest, "projectId and targetSchemeId are required.")
			return
		}
		project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, request.ProjectID)
		if err != nil {
			jiraError(w, http.StatusNotFound, "The project does not exist.")
			return
		}
		var mappings []store.WorkflowStatusMapping
		for _, override := range request.MappingsByIssueTypeOverride {
			for _, mapping := range override.StatusMappings {
				mappings = append(mappings, store.WorkflowStatusMapping{
					IssueTypeID: override.IssueTypeID,
					OldStatusID: mapping.OldStatusID,
					NewStatusID: mapping.NewStatusID,
				})
			}
		}
		task, err := h.Store.SwitchWorkflowSchemeTask(r.Context(), workspaceID, userID, project.ID, request.TargetSchemeID, mappings)
		if err != nil {
			workflowSchemeAPIError(w, err)
			return
		}
		self := h.BaseURL + "/rest/api/3/task/" + task.ID
		w.Header().Set("Location", self)
		writeJSON(w, http.StatusSeeOther, h.apiTaskBean(task))
		return
	}
	if path == "/workflowscheme/project" {
		if r.Method == http.MethodGet {
			projectIDs := r.URL.Query()["projectId"]
			values := make([]map[string]any, 0, len(projectIDs))
			for _, projectID := range projectIDs {
				project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectID)
				if err != nil {
					continue
				}
				scheme, err := h.Store.WorkflowSchemeForProject(r.Context(), workspaceID, project.ID)
				if err == nil {
					values = append(values, map[string]any{"projectId": project.ID, "workflowScheme": h.workflowSchemeBean(r, workspaceID, scheme)})
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"values": values})
			return
		}
		if r.Method == http.MethodPut {
			var request struct{ ProjectID, WorkflowSchemeID string }
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ProjectID == "" || request.WorkflowSchemeID == "" {
				jiraError(w, http.StatusBadRequest, "projectId and workflowSchemeId are required.")
				return
			}
			project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, request.ProjectID)
			if err != nil {
				jiraError(w, http.StatusNotFound, "The project does not exist.")
				return
			}
			if err := h.Store.AssignWorkflowScheme(r.Context(), workspaceID, userID, project.ID, request.WorkflowSchemeID); err != nil {
				workflowSchemeAPIError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(path, "/workflowscheme/")
	if id == path || strings.Contains(id, "/") || id == "" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		useDraft := r.URL.Query().Get("returnDraftIfExists") == "true"
		scheme, err := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, id, useDraft)
		if err != nil {
			workflowSchemeAPIError(w, store.ErrAdminNotFound)
			return
		}
		writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, scheme))
	case http.MethodPut:
		var request struct {
			Name, Description, DefaultWorkflow string
			IssueTypeMappings                  map[string]string `json:"issueTypeMappings"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			jiraError(w, http.StatusBadRequest, "Invalid workflow scheme request.")
			return
		}
		workflows, _ := h.Store.ListWorkflows(r.Context(), workspaceID)
		defaultID, mappings, ok := resolveSchemeWorkflowNames(workflows, request.DefaultWorkflow, request.IssueTypeMappings)
		if !ok {
			jiraError(w, http.StatusBadRequest, "A mapped workflow does not exist.")
			return
		}
		scheme := workflow.Scheme{ID: id, Name: request.Name, Description: request.Description, DefaultWorkflowID: defaultID, IssueTypeMappings: mappings}
		if err := h.Store.SaveWorkflowSchemeDraft(r.Context(), workspaceID, userID, scheme); err != nil {
			workflowSchemeAPIError(w, err)
			return
		}
		updated, _ := h.Store.WorkflowSchemeByID(r.Context(), workspaceID, id, true)
		writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, updated))
	case http.MethodDelete:
		if err := h.Store.DeleteWorkflowScheme(r.Context(), workspaceID, userID, id); err != nil {
			workflowSchemeAPIError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
