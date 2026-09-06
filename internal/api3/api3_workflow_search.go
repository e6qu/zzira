package api3

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/workflow"
)

func parseWorkflowSearchPage(r *http.Request) (int, int, error) {
	startAt, maxResults := 0, 50
	var err error
	if value := r.URL.Query().Get("startAt"); value != "" {
		startAt, err = strconv.Atoi(value)
		if err != nil || startAt < 0 {
			return 0, 0, errWorkflowSearchValidation
		}
	}
	if value := r.URL.Query().Get("maxResults"); value != "" {
		maxResults, err = strconv.Atoi(value)
		if err != nil || maxResults < 1 || maxResults > 200 {
			return 0, 0, errWorkflowSearchValidation
		}
	}
	return startAt, maxResults, nil
}

var errWorkflowSearchValidation = &workflowSearchError{}

type workflowSearchError struct{}

func (*workflowSearchError) Error() string { return "invalid workflow search parameters" }

func containsWorkflowUsage(ids []string, projectID string) bool {
	for _, id := range ids {
		if id == projectID {
			return true
		}
	}
	return false
}

func workflowStatusReferences(wf workflow.Workflow) map[string]bool {
	ids := make(map[string]bool)
	for _, status := range wf.Statuses {
		ids[status.StatusReference] = true
	}
	for _, transition := range wf.Transitions {
		ids[transition.To] = true
		for _, from := range transition.From {
			ids[from] = true
		}
	}
	return ids
}

func workflowReferenceStatuses(wf workflow.Workflow) []map[string]any {
	statusIDs := workflowStatusReferences(wf)
	layouts := make(map[string]workflow.StatusLayout, len(wf.Statuses))
	for _, status := range wf.Statuses {
		layouts[status.StatusReference] = status
	}
	statuses := make([]map[string]any, 0, len(statusIDs))
	for id := range statusIDs {
		status, exists := layouts[id]
		properties := status.Properties
		if properties == nil {
			properties = map[string]string{}
		}
		bean := map[string]any{"statusReference": id, "deprecated": false, "properties": properties}
		if exists && status.Layout != nil {
			bean["layout"] = status.Layout
		}
		statuses = append(statuses, bean)
	}
	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i]["statusReference"].(string) < statuses[j]["statusReference"].(string)
	})
	return statuses
}

func workflowStatusCategory(category string) string {
	switch category {
	case "done":
		return "DONE"
	case "indeterminate":
		return "IN_PROGRESS"
	default:
		return "TODO"
	}
}

func workflowTransitionBean(transition workflow.Transition) map[string]any {
	links := make([]map[string]any, 0, len(transition.From))
	for _, from := range transition.From {
		links = append(links, map[string]any{"fromStatusReference": from})
	}
	actions := transition.Actions
	if actions == nil {
		actions = []workflow.Rule{}
	}
	validators := transition.Validators
	if validators == nil {
		validators = []workflow.Rule{}
	}
	triggers := transition.Triggers
	if triggers == nil {
		triggers = []workflow.Rule{}
	}
	bean := map[string]any{
		"id": transition.ID, "name": transition.Name, "description": "",
		"type": "DIRECTED", "toStatusReference": transition.To, "links": links,
		"properties": map[string]string{}, "actions": actions, "validators": validators, "triggers": triggers,
	}
	if transition.Conditions != nil {
		bean["conditions"] = transition.Conditions
	}
	if transition.Screen != nil {
		bean["transitionScreen"] = transition.Screen
	}
	return bean
}

func workflowSearchBean(wf workflow.Workflow, expandTransitions bool) map[string]any {
	bean := map[string]any{
		"id": wf.ID, "name": wf.Name, "description": wf.Description, "isEditable": wf.ID != workflow.Default().ID,
		"scope": jiraWorkflowScope(wf), "statuses": workflowReferenceStatuses(wf),
		"version": map[string]any{"id": wf.ID, "versionNumber": wf.Version},
	}
	if wf.StartPointLayout != nil {
		bean["startPointLayout"] = wf.StartPointLayout
	}
	if wf.LoopedTransitionContainerLayout != nil {
		bean["loopedTransitionContainerLayout"] = wf.LoopedTransitionContainerLayout
	}
	if expandTransitions {
		transitions := make([]map[string]any, 0, len(wf.Transitions))
		for _, transition := range wf.Transitions {
			transitions = append(transitions, workflowTransitionBean(transition))
		}
		bean["transitions"] = transitions
	}
	return bean
}

func jiraWorkflowScope(wf workflow.Workflow) map[string]any {
	if wf.ProjectID != "" {
		return map[string]any{"type": "PROJECT", "project": map[string]string{"id": wf.ProjectID}}
	}
	return map[string]any{"type": "GLOBAL"}
}

func workflowSearchStatusBean(status models.Status) map[string]any {
	return map[string]any{
		"id": status.ID, "name": status.Name, "description": status.Description,
		"scope": jiraStatusScope(status), "statusCategory": workflowStatusCategory(status.Category),
		"statusReference": status.ID,
	}
}

func workflowSearchNextPage(baseURL string, r *http.Request, startAt int) string {
	query := make(url.Values, len(r.URL.Query()))
	for key, values := range r.URL.Query() {
		query[key] = append([]string(nil), values...)
	}
	query.Set("startAt", strconv.Itoa(startAt))
	return baseURL + r.URL.Path + "?" + query.Encode()
}

func (h *Handler) workflowSearch(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, authErr := h.authWorkspaceAdmin(r)
	if authErr != nil {
		writeJerr(w, authErr)
		return
	}
	startAt, maxResults, err := parseWorkflowSearchPage(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, err.Error())
		return
	}
	scope := strings.ToUpper(r.URL.Query().Get("scope"))
	if scope != "" && scope != "GLOBAL" && scope != "PROJECT" {
		jiraError(w, http.StatusBadRequest, "scope is invalid")
		return
	}
	projectID := r.URL.Query().Get("projectId")
	if projectID != "" {
		if _, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectID); err != nil {
			jiraError(w, http.StatusBadRequest, "projectId is invalid")
			return
		}
	}
	var activeFilter *bool
	if value := r.URL.Query().Get("isActive"); value != "" {
		parsed, parseErr := strconv.ParseBool(value)
		if parseErr != nil {
			jiraError(w, http.StatusBadRequest, "isActive is invalid")
			return
		}
		activeFilter = &parsed
	}
	expandTransitions := false
	if expand := r.URL.Query().Get("expand"); expand != "" {
		for _, value := range strings.Split(expand, ",") {
			if strings.TrimSpace(value) != "values.transitions" {
				jiraError(w, http.StatusBadRequest, "expand is invalid")
				return
			}
			expandTransitions = true
		}
	}
	workflows, err := h.Store.ListWorkflows(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	query := strings.ToLower(r.URL.Query().Get("queryString"))
	filtered := make([]workflow.Workflow, 0, len(workflows))
	for _, item := range workflows {
		if (scope == "PROJECT" && item.ProjectID == "") || (scope == "GLOBAL" && item.ProjectID != "") ||
			(query != "" && !strings.Contains(strings.ToLower(item.Name), query)) {
			continue
		}
		projects, err := h.Store.WorkflowProjectUsages(r.Context(), workspaceID, item.ID)
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		active := len(projects) > 0
		if activeFilter != nil && active != *activeFilter {
			continue
		}
		if projectID != "" && item.ProjectID != projectID && !containsWorkflowUsage(projects, projectID) {
			continue
		}
		filtered = append(filtered, item)
	}
	orderBy := r.URL.Query().Get("orderBy")
	if orderBy != "" && orderBy != "name" && orderBy != "+name" && orderBy != "-name" {
		jiraError(w, http.StatusBadRequest, "orderBy is invalid")
		return
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		left, right := strings.ToLower(filtered[i].Name), strings.ToLower(filtered[j].Name)
		if orderBy == "-name" {
			return left > right
		}
		return left < right
	})
	if startAt > len(filtered) {
		startAt = len(filtered)
	}
	end := startAt + maxResults
	if end > len(filtered) {
		end = len(filtered)
	}
	values := make([]map[string]any, 0, end-startAt)
	referencedStatuses := make(map[string]bool)
	for _, item := range filtered[startAt:end] {
		values = append(values, workflowSearchBean(item, expandTransitions))
		for id := range workflowStatusReferences(item) {
			referencedStatuses[id] = true
		}
	}
	statuses, err := h.Store.StatusesForAdministration(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	statusValues := make([]map[string]any, 0, len(referencedStatuses))
	for _, status := range statuses {
		if referencedStatuses[status.ID] {
			statusValues = append(statusValues, workflowSearchStatusBean(status))
		}
	}
	response := map[string]any{
		"self": h.BaseURL + r.URL.RequestURI(), "startAt": startAt, "maxResults": maxResults,
		"total": len(filtered), "isLast": end == len(filtered), "values": values, "statuses": statusValues,
	}
	if end < len(filtered) {
		response["nextPage"] = workflowSearchNextPage(h.BaseURL, r, end)
	}
	writeJSON(w, http.StatusOK, response)
}
