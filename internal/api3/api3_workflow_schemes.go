package api3

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

func (h *Handler) workflowSchemeBean(r *http.Request, workspaceID string, scheme workflow.Scheme) map[string]any {
	workflows, _ := h.Store.ListGlobalWorkflows(r.Context(), workspaceID)
	names := make(map[string]string, len(workflows))
	for _, item := range workflows {
		names[item.ID] = item.Name
	}
	ids := h.issueTypeIDsFor(r, workspaceID)
	wireMappings := func(source map[string]string) map[string]string {
		mappings := make(map[string]string, len(source))
		for issueTypeID, workflowID := range source {
			mappings[ids.toWire(issueTypeID)] = names[workflowID]
		}
		return mappings
	}
	bean := map[string]any{
		"name": scheme.Name, "description": scheme.Description,
		"defaultWorkflow": names[scheme.DefaultWorkflowID], "issueTypeMappings": wireMappings(scheme.IssueTypeMappings),
		"draft": scheme.DraftView,
	}
	// The default workflow scheme is not addressed by an id, as in Jira.
	if !scheme.IsDefault {
		bean["id"] = scheme.JiraID
		bean["self"] = h.BaseURL + "/rest/api/3/workflowscheme/" + strconv.FormatInt(scheme.JiraID, 10)
	}
	if scheme.DraftView {
		bean["originalDefaultWorkflow"] = names[scheme.OriginalDefaultWorkflowID]
		bean["originalIssueTypeMappings"] = wireMappings(scheme.OriginalIssueTypeMappings)
		if at, err := time.Parse("2006-01-02T15:04:05.000Z", scheme.DraftModifiedAt); err == nil {
			bean["lastModified"] = at.UTC().Format(jiraDateTime)
		}
		if scheme.DraftModifiedBy != "" {
			if user, err := h.Store.UserByID(r.Context(), scheme.DraftModifiedBy); err == nil {
				bean["lastModifiedUser"] = h.userBeanFor(r.Context(), user)
			}
		}
	}
	return bean
}

// workflowMappingBeans lists every workflow a scheme uses with the work item
// types mapped to it, as the workflow mapping reads return when no workflow is
// named.
func (h *Handler) workflowMappingBeans(r *http.Request, workspaceID string, workflows []workflow.Workflow, scheme workflow.Scheme) []map[string]any {
	ids := h.issueTypeIDsFor(r, workspaceID)
	byWorkflow := map[string][]string{scheme.DefaultWorkflowID: {}}
	for issueTypeID, workflowID := range scheme.IssueTypeMappings {
		byWorkflow[workflowID] = append(byWorkflow[workflowID], ids.toWire(issueTypeID))
	}
	beans := make([]map[string]any, 0, len(byWorkflow))
	for workflowID, issueTypes := range byWorkflow {
		sort.Strings(issueTypes)
		beans = append(beans, map[string]any{"workflow": workflowNameForID(workflows, workflowID), "issueTypes": issueTypes, "defaultMapping": workflowID == scheme.DefaultWorkflowID})
	}
	sort.Slice(beans, func(i, j int) bool { return beans[i]["workflow"].(string) < beans[j]["workflow"].(string) })
	return beans
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
	if h.workflowSchemeBulkRoute(w, r, workspaceID, userID, path) {
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
			start, limit, pageErr := metadataPage(r)
			if pageErr != nil {
				writeJerr(w, pageErr)
				return
			}
			// Drafts and the default scheme, which has no id, are not listed.
			listed := make([]workflow.Scheme, 0, len(schemes))
			for _, scheme := range schemes {
				if !scheme.IsDefault {
					listed = append(listed, scheme)
				}
			}
			total := len(listed)
			from := min(start, total)
			to := min(from+limit, total)
			values := make([]map[string]any, 0, to-from)
			for _, scheme := range listed[from:to] {
				values = append(values, h.workflowSchemeBean(r, workspaceID, scheme))
			}
			page := map[string]any{"startAt": start, "maxResults": limit, "total": total, "isLast": to >= total, "values": values,
				"self": h.BaseURL + "/rest/api/3/workflowscheme?startAt=" + strconv.Itoa(start) + "&maxResults=" + strconv.Itoa(limit)}
			if to < total {
				page["nextPage"] = h.BaseURL + "/rest/api/3/workflowscheme?startAt=" + strconv.Itoa(to) + "&maxResults=" + strconv.Itoa(limit)
			}
			writeJSON(w, http.StatusOK, page)
		case http.MethodPost:
			var request struct {
				Name, Description, DefaultWorkflow string
				IssueTypeMappings                  map[string]string `json:"issueTypeMappings"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				jiraError(w, http.StatusBadRequest, "Invalid workflow scheme request.")
				return
			}
			workflows, err := h.Store.ListGlobalWorkflows(r.Context(), workspaceID)
			if err != nil {
				workflowSchemeAPIError(w, err)
				return
			}
			request.IssueTypeMappings = h.issueTypeIDsFor(r, workspaceID).mapKeysToInternal(request.IssueTypeMappings)
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
		ids := h.issueTypeIDsFor(r, workspaceID)
		statusIDs := h.statusIDsFor(r, workspaceID)
		var mappings []store.WorkflowStatusMapping
		for _, override := range request.MappingsByIssueTypeOverride {
			for _, mapping := range override.StatusMappings {
				mappings = append(mappings, store.WorkflowStatusMapping{
					IssueTypeID: ids.toInternal(override.IssueTypeID),
					OldStatusID: statusIDs.toInternal(mapping.OldStatusID),
					NewStatusID: statusIDs.toInternal(mapping.NewStatusID),
				})
			}
		}
		task, err := h.Store.SwitchWorkflowSchemeTask(r.Context(), workspaceID, userID, project.ID, h.Store.WorkflowSchemeIDByRef(r.Context(), workspaceID, request.TargetSchemeID), mappings)
		if err != nil {
			workflowSchemeAPIError(w, err)
			return
		}
		self := h.BaseURL + "/rest/api/3/task/" + task.WireID()
		w.Header().Set("Location", self)
		writeJSON(w, http.StatusSeeOther, h.apiTaskBean(task))
		return
	}
	if path == "/workflowscheme/project" {
		if r.Method == http.MethodGet {
			// Each scheme is listed once with the requested projects that use it;
			// unknown and team-managed projects are ignored.
			values := []map[string]any{}
			bySchemeID := map[string]map[string]any{}
			for _, projectID := range securityQueryValues(r, "projectId") {
				project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectID)
				if err != nil {
					continue
				}
				scheme, err := h.Store.WorkflowSchemeForProject(r.Context(), workspaceID, project.ID)
				if err != nil {
					continue
				}
				association := bySchemeID[scheme.ID]
				if association == nil {
					association = map[string]any{"projectIds": []string{}, "workflowScheme": h.workflowSchemeBean(r, workspaceID, scheme)}
					bySchemeID[scheme.ID] = association
					values = append(values, association)
				}
				association["projectIds"] = append(association["projectIds"].([]string), project.ID)
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
			if err := h.Store.AssignWorkflowScheme(r.Context(), workspaceID, userID, project.ID, h.Store.WorkflowSchemeIDByRef(r.Context(), workspaceID, request.WorkflowSchemeID)); err != nil {
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
	parts := strings.Split(id, "/")
	if h.workflowSchemeSubresourceRoute(w, r, workspaceID, userID, parts) {
		return
	}
	if h.workflowSchemePublishedSubresourceRoute(w, r, workspaceID, userID, parts) {
		return
	}
	if id == path || strings.Contains(id, "/") || id == "" {
		jiraError(w, http.StatusNotFound, "No resource found")
		return
	}
	// Clients name a scheme by its numeric id.
	id = h.Store.WorkflowSchemeIDByRef(r.Context(), workspaceID, id)
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
			UpdateDraftIfNeeded                bool              `json:"updateDraftIfNeeded"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			jiraError(w, http.StatusBadRequest, "Invalid workflow scheme request.")
			return
		}
		workflows, _ := h.Store.ListGlobalWorkflows(r.Context(), workspaceID)
		request.IssueTypeMappings = h.issueTypeIDsFor(r, workspaceID).mapKeysToInternal(request.IssueTypeMappings)
		defaultID, mappings, ok := resolveSchemeWorkflowNames(workflows, request.DefaultWorkflow, request.IssueTypeMappings)
		if !ok {
			jiraError(w, http.StatusBadRequest, "A mapped workflow does not exist.")
			return
		}
		scheme := workflow.Scheme{ID: id, Name: request.Name, Description: request.Description, DefaultWorkflowID: defaultID, IssueTypeMappings: mappings}
		if updated, saved := h.saveWorkflowSchemeChange(w, r, workspaceID, userID, scheme, request.UpdateDraftIfNeeded); saved {
			writeJSON(w, http.StatusOK, h.workflowSchemeBean(r, workspaceID, updated))
		}
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
