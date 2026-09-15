package api3

import (
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/workflow"
)

func (h *Handler) workflowDefaultEditor(w http.ResponseWriter, r *http.Request) {
	if _, _, err := h.authWorkspace(r); err != nil {
		writeJerr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"value": "NEW"})
}

// legacyWorkflowSearch serves GET /rest/api/3/workflow/search: a page of
// published classic workflows, which are the site's global workflows, with
// Jira's expansions of transitions, rules, statuses, schemes and projects.
func (h *Handler) legacyWorkflowSearch(w http.ResponseWriter, r *http.Request) {
	workspaceID, _, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	query := r.URL.Query()
	startAt, maxResults, err := parseWorkflowSearchPage(r)
	if err != nil {
		jiraError(w, http.StatusBadRequest, "startAt must be zero or more and maxResults between 1 and 200.")
		return
	}
	orderBy := query.Get("orderBy")
	descending := strings.HasPrefix(orderBy, "-")
	orderField := strings.TrimLeft(orderBy, "+-")
	if orderBy != "" && orderField != "name" && orderField != "created" && orderField != "updated" {
		jiraError(w, http.StatusBadRequest, "orderBy must be name, created or updated.")
		return
	}
	var activeFilter *bool
	if raw := query.Get("isActive"); raw != "" {
		active, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			jiraError(w, http.StatusBadRequest, "isActive must be true or false.")
			return
		}
		activeFilter = &active
	}
	expand := map[string]bool{}
	for _, name := range strings.Split(query.Get("expand"), ",") {
		expand[strings.TrimSpace(name)] = true
	}
	expandTransitions := expand["transitions"] || expand["transitions.rules"] || expand["transitions.properties"]
	expandStatuses := expand["statuses"] || expand["statuses.properties"]
	names := map[string]bool{}
	for _, name := range query["workflowName"] {
		names[name] = true
	}
	text := strings.ToLower(query.Get("queryString"))

	workflows, err := h.Store.ListWorkflows(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	type candidate struct {
		wf       workflow.Workflow
		projects []string
	}
	matched := []candidate{}
	for _, wf := range workflows {
		// Team-managed workflows are not classic workflows.
		if wf.ProjectID != "" || (len(names) > 0 && !names[wf.Name]) || (text != "" && !strings.Contains(strings.ToLower(wf.Name), text)) {
			continue
		}
		projects, usageErr := h.Store.WorkflowProjectUsages(r.Context(), workspaceID, wf.ID)
		if usageErr != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if activeFilter != nil && *activeFilter != (len(projects) > 0) {
			continue
		}
		matched = append(matched, candidate{wf: wf, projects: projects})
	}
	sort.SliceStable(matched, func(i, j int) bool {
		a, b := matched[i].wf, matched[j].wf
		less, equal := strings.ToLower(a.Name) < strings.ToLower(b.Name), strings.EqualFold(a.Name, b.Name)
		switch orderField {
		case "created":
			less, equal = a.CreatedAt.Before(b.CreatedAt), a.CreatedAt.Equal(b.CreatedAt)
		case "updated":
			less, equal = a.UpdatedAt.Before(b.UpdatedAt), a.UpdatedAt.Equal(b.UpdatedAt)
		}
		if equal {
			return a.Name < b.Name
		}
		return less != descending
	})

	wire := h.statusIDsFor(r, workspaceID)
	statusNames := map[string]string{}
	if statuses, statusErr := h.Store.StatusesForWorkspace(r.Context(), workspaceID); statusErr == nil {
		for _, status := range statuses {
			statusNames[status.ID] = status.Name
		}
	}
	values := []map[string]any{}
	for index := startAt; index < len(matched) && index < startAt+maxResults; index++ {
		wf, projects := matched[index].wf, matched[index].projects
		bean := map[string]any{
			"id":          map[string]any{"name": wf.Name, "entityId": wf.EntityID},
			"description": wf.Description,
			"created":     wf.CreatedAt.UTC().Format("2006-01-02T15:04:05.000-0700"),
			"updated":     wf.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000-0700"),
		}
		if expandTransitions {
			transitions := []map[string]any{}
			for _, transition := range wf.Transitions {
				transitions = append(transitions, legacyWorkflowTransitionBean(transition, wire, expand["transitions.rules"], expand["transitions.properties"]))
			}
			bean["transitions"] = transitions
		}
		if expandStatuses {
			properties := map[string]map[string]string{}
			for _, layout := range wf.Statuses {
				properties[layout.StatusReference] = layout.Properties
			}
			statuses := []map[string]any{}
			for _, id := range wf.StatusIDs() {
				status := map[string]any{"id": wire.toWire(id), "name": statusNames[id]}
				if expand["statuses.properties"] {
					values := properties[id]
					if values == nil {
						values = map[string]string{}
					}
					status["properties"] = values
				}
				statuses = append(statuses, status)
			}
			bean["statuses"] = statuses
		}
		if expand["default"] {
			bean["isDefault"] = wf.ID == workflow.Default().ID
		}
		schemes, schemeErr := h.Store.WorkflowSchemesUsing(r.Context(), workspaceID, wf.ID)
		if schemeErr != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if expand["schemes"] {
			beans := []map[string]any{}
			for _, scheme := range schemes {
				beans = append(beans, map[string]any{"id": scheme.ID, "name": scheme.Name})
			}
			bean["schemes"] = beans
		}
		if expand["projects"] {
			beans := []map[string]any{}
			for _, projectID := range projects {
				if project, projectErr := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, projectID); projectErr == nil {
					beans = append(beans, map[string]any{
						"id": project.ID, "key": project.Key, "name": project.Name, "projectTypeKey": project.ProjectTypeKey,
						"avatarUrls": map[string]string{"48x48": h.BaseURL + "/static/img/avatar-default.svg"},
					})
				}
			}
			bean["projects"] = beans
		}
		if expand["hasDraftWorkflow"] {
			bean["hasDraftWorkflow"] = wf.HasDraft
		}
		if expand["operations"] {
			system := wf.ID == workflow.Default().ID
			bean["operations"] = map[string]bool{"canEdit": !system, "canDelete": !system && len(projects) == 0 && len(schemes) == 0}
		}
		values = append(values, bean)
	}
	self := h.BaseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		self += "?" + r.URL.RawQuery
	}
	page := map[string]any{"self": self, "maxResults": maxResults, "startAt": startAt, "total": len(matched), "isLast": startAt+maxResults >= len(matched), "values": values}
	if startAt+maxResults < len(matched) {
		next := url.Values{}
		for key, value := range query {
			next[key] = value
		}
		next.Set("startAt", strconv.Itoa(startAt+maxResults))
		next.Set("maxResults", strconv.Itoa(maxResults))
		page["nextPage"] = h.BaseURL + r.URL.Path + "?" + next.Encode()
	}
	writeJSON(w, http.StatusOK, page)
}

// legacyWorkflowTransitionBean is a transition in the classic workflow shape:
// lowercase types, source and destination status ids, and rules as a
// conditions tree, validators and post functions.
func legacyWorkflowTransitionBean(transition workflow.Transition, wire statusIDs, withRules, withProperties bool) map[string]any {
	bean := map[string]any{
		"id": transition.ID, "name": transition.Name, "description": transition.Description,
		"from": wire.allToWire(transition.From), "to": wire.toWire(transition.To), "type": strings.ToLower(transition.Kind()),
	}
	if withRules {
		ruleBeans := func(rules []workflow.Rule) []map[string]any {
			beans := []map[string]any{}
			for _, rule := range rules {
				beans = append(beans, map[string]any{"type": rule.RuleKey, "configuration": rule.Parameters})
			}
			return beans
		}
		rules := map[string]any{"validators": ruleBeans(transition.Validators), "postFunctions": ruleBeans(transition.Actions)}
		if transition.Conditions != nil {
			rules["conditionsTree"] = legacyConditionTree(*transition.Conditions)
		}
		bean["rules"] = rules
	}
	if withProperties {
		properties := transition.Properties
		if properties == nil {
			properties = map[string]string{}
		}
		bean["properties"] = properties
	}
	return bean
}

// legacyConditionTree renders a condition group as Jira's compound and simple
// condition nodes.
func legacyConditionTree(group workflow.ConditionGroup) map[string]any {
	conditions := []map[string]any{}
	for _, rule := range group.Conditions {
		conditions = append(conditions, map[string]any{"nodeType": "simple", "type": rule.RuleKey, "configuration": rule.Parameters})
	}
	for _, child := range group.ConditionGroups {
		conditions = append(conditions, legacyConditionTree(child))
	}
	operator := strings.ToUpper(group.Operation)
	if operator != "OR" {
		operator = "AND"
	}
	return map[string]any{"nodeType": "compound", "operator": operator, "conditions": conditions}
}
