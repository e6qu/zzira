package api3

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

// workflowDocument is Jira's WorkflowDocumentDTO for a stored workflow version.
func (h *Handler) workflowDocument(r *http.Request, workspaceID string, wf workflow.Workflow) (map[string]any, []map[string]any, error) {
	wire := h.statusIDsFor(r, workspaceID)
	document := workflowSearchBean(wf, true, wire)
	statuses, err := h.Store.StatusesForAdministration(r.Context(), workspaceID)
	if err != nil {
		return nil, nil, err
	}
	references := workflowStatusReferences(wf)
	beans := []map[string]any{}
	for _, status := range statuses {
		if references[status.ID] {
			beans = append(beans, workflowSearchStatusBean(status))
		}
	}
	return document, beans, nil
}

func (h *Handler) workflowHistoryRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, _, e := h.authWorkspaceAdmin(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	if r.Method != http.MethodPost {
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request struct {
		WorkflowID string `json:"workflowId"`
		Version    *int   `json:"version"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || strings.TrimSpace(request.WorkflowID) == "" {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"workflowId": "The workflow id is required."})
		return
	}
	if path == "/workflow/history/list" {
		includeIntermediate := false
		for _, option := range strings.Split(r.URL.Query().Get("expand"), ",") {
			if strings.TrimSpace(option) == "includeIntermediateWorkflows" {
				includeIntermediate = true
			}
		}
		entries, err := h.Store.WorkflowHistory(r.Context(), workspaceID, request.WorkflowID, includeIntermediate)
		if errors.Is(err, store.ErrAdminNotFound) {
			jiraError(w, http.StatusBadRequest, "The workflow does not exist.")
			return
		}
		if err != nil {
			jiraError(w, http.StatusInternalServerError, "internal error")
			return
		}
		beans := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			beans = append(beans, map[string]any{
				"workflowId": entry.WorkflowID, "workflowVersion": entry.Version,
				"writtenAt": entry.WrittenAt.UTC().Format(jiraTimeLayout), "isIntermediate": entry.Intermediate,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": beans})
		return
	}
	if request.Version == nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"version": "The workflow version is required."})
		return
	}
	version, err := h.Store.WorkflowHistoryVersion(r.Context(), workspaceID, request.WorkflowID, *request.Version)
	if errors.Is(err, store.ErrAdminNotFound) {
		jiraError(w, http.StatusBadRequest, "The workflow version does not exist.")
		return
	}
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	document, statuses, err := h.workflowDocument(r, workspaceID, version.Workflow)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	document["updated"] = version.WrittenAt.UTC().Format(jiraTimeLayout)
	document["lastUpdateAuthorAAID"] = version.AuthorID
	document["version"] = map[string]any{"id": version.Workflow.EntityID, "versionNumber": version.Workflow.Version}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": []map[string]any{document}, "statuses": statuses})
}

// readWorkflows implements Jira's bulk POST /rest/api/3/workflows read.
func (h *Handler) readWorkflows(w http.ResponseWriter, r *http.Request) {
	access, e := h.authWorkflowAccess(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	workspaceID := access.workspaceID
	var request struct {
		WorkflowIDs          []string `json:"workflowIds"`
		WorkflowNames        []string `json:"workflowNames"`
		ProjectAndIssueTypes []struct {
			ProjectID   string `json:"projectId"`
			IssueTypeID string `json:"issueTypeId"`
		} `json:"projectAndIssueTypes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
		jiraError(w, http.StatusBadRequest, "Invalid request payload.")
		return
	}
	workflows, err := h.Store.ListWorkflows(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	workflows = access.scoped(workflows)
	wanted := map[string]bool{}
	for _, id := range request.WorkflowIDs {
		wanted["id:"+id] = true
	}
	for _, name := range request.WorkflowNames {
		wanted["name:"+name] = true
	}
	for _, pair := range request.ProjectAndIssueTypes {
		project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, pair.ProjectID)
		if err != nil {
			continue
		}
		issueTypeID := pair.IssueTypeID
		if issueType, err := h.Store.IssueTypeByIDOrName(r.Context(), workspaceID, pair.IssueTypeID); err == nil {
			issueTypeID = issueType.ID
		}
		if flow, err := h.Store.WorkflowForProjectAndIssueType(r.Context(), project.ID, issueTypeID); err == nil {
			wanted["id:"+flow.ID] = true
		}
	}
	all := len(wanted) == 0
	wire := h.statusIDsFor(r, workspaceID)
	beans := []map[string]any{}
	references := map[string]bool{}
	for _, flow := range workflows {
		if !all && !wanted["id:"+flow.ID] && !wanted["id:"+flow.EntityID] && !wanted["name:"+flow.Name] {
			continue
		}
		beans = append(beans, workflowSearchBean(flow, true, wire))
		for id := range workflowStatusReferences(flow) {
			references[id] = true
		}
	}
	statuses, err := h.Store.StatusesForAdministration(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	statusBeans := []map[string]any{}
	for _, status := range statuses {
		if references[status.ID] {
			statusBeans = append(statusBeans, workflowSearchStatusBean(status))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": beans, "statuses": statusBeans})
}

// ---- app transition rules ----

var appRuleTypes = map[string]bool{"postfunction": true, "condition": true, "validator": true}

type appRuleRef struct {
	kind       string
	transition workflow.Transition
	rule       *workflow.Rule
}

// appRules finds the calling app's rules in a workflow, by type.
func appRules(wf *workflow.Workflow, appKey string) []appRuleRef {
	refs := []appRuleRef{}
	var collectConditions func(transition workflow.Transition, group *workflow.ConditionGroup)
	collectConditions = func(transition workflow.Transition, group *workflow.ConditionGroup) {
		if group == nil {
			return
		}
		for index := range group.Conditions {
			rule := &group.Conditions[index]
			if workflow.IsAppRule(rule.RuleKey) && rule.Parameters["appKey"] == appKey {
				refs = append(refs, appRuleRef{kind: "condition", transition: transition, rule: rule})
			}
		}
		for index := range group.ConditionGroups {
			collectConditions(transition, &group.ConditionGroups[index])
		}
	}
	for transitionIndex := range wf.Transitions {
		transition := &wf.Transitions[transitionIndex]
		for index := range transition.Actions {
			if rule := &transition.Actions[index]; workflow.IsAppRule(rule.RuleKey) && rule.Parameters["appKey"] == appKey {
				refs = append(refs, appRuleRef{kind: "postfunction", transition: *transition, rule: rule})
			}
		}
		for index := range transition.Validators {
			if rule := &transition.Validators[index]; workflow.IsAppRule(rule.RuleKey) && rule.Parameters["appKey"] == appKey {
				refs = append(refs, appRuleRef{kind: "validator", transition: *transition, rule: rule})
			}
		}
		collectConditions(*transition, transition.Conditions)
	}
	return refs
}

func appRuleBean(ref appRuleRef, expandTransition bool) map[string]any {
	configuration := map[string]any{"value": ref.rule.Parameters["config"], "disabled": ref.rule.Parameters["disabled"] == "true"}
	if tag := ref.rule.Parameters["tag"]; tag != "" {
		configuration["tag"] = tag
	}
	bean := map[string]any{"id": ref.rule.ID, "key": ref.rule.Parameters["key"], "configuration": configuration}
	if expandTransition {
		id, _ := strconv.Atoi(ref.transition.ID)
		bean["transition"] = map[string]any{"id": id, "name": ref.transition.Name}
	}
	return bean
}

func queryValues(r *http.Request, name string) []string {
	values := []string{}
	for _, raw := range r.URL.Query()[name] {
		for _, value := range strings.Split(raw, ",") {
			if value = strings.TrimSpace(value); value != "" {
				values = append(values, value)
			}
		}
	}
	return values
}

type workflowRef struct {
	Name  string `json:"name"`
	Draft bool   `json:"draft"`
}

func (h *Handler) workflowRuleConfigRoute(w http.ResponseWriter, r *http.Request, path string) {
	workspaceID, userID, e := h.authWorkspace(r)
	if e != nil {
		writeJerr(w, e)
		return
	}
	installation, ok := apps.InstallationFromContext(r.Context())
	if !ok {
		jiraError(w, http.StatusForbidden, "Only Connect or Forge apps can use this operation.")
		return
	}
	switch {
	case path == "/workflow/rule/config" && r.Method == http.MethodGet:
		h.listAppWorkflowRules(w, r, workspaceID, installation)
	case path == "/workflow/rule/config" && r.Method == http.MethodPut:
		h.updateAppWorkflowRules(w, r, workspaceID, userID, installation, false)
	case path == "/workflow/rule/config/delete" && r.Method == http.MethodPut:
		if installation.Format != "connect" {
			jiraError(w, http.StatusForbidden, "Only Connect apps can use this operation.")
			return
		}
		h.updateAppWorkflowRules(w, r, workspaceID, userID, installation, true)
	default:
		jiraError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// loadWorkflowVersion reads a workflow by name, as published or as its draft.
func (h *Handler) loadWorkflowVersion(r *http.Request, workspaceID string, workflows []workflow.Workflow, ref workflowRef) (*workflow.Workflow, bool) {
	for _, flow := range workflows {
		if flow.Name != ref.Name {
			continue
		}
		if !ref.Draft {
			found := flow
			return &found, true
		}
		if !flow.HasDraft {
			return nil, false
		}
		draft, err := h.Store.WorkflowDraftByID(r.Context(), workspaceID, flow.ID)
		if err != nil {
			return nil, false
		}
		return &draft, true
	}
	return nil, false
}

func (h *Handler) listAppWorkflowRules(w http.ResponseWriter, r *http.Request, workspaceID string, installation *models.AppInstallation) {
	types := queryValues(r, "types")
	if len(types) == 0 {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"types": "At least one transition rule type is required."})
		return
	}
	wantedTypes := map[string]bool{}
	for _, kind := range types {
		if !appRuleTypes[kind] {
			jiraFieldError(w, http.StatusBadRequest, map[string]string{"types": "The transition rule type " + kind + " is not supported."})
			return
		}
		wantedTypes[kind] = true
	}
	keys, names, tags := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, key := range queryValues(r, "keys") {
		keys[key] = true
	}
	for _, name := range queryValues(r, "workflowNames") {
		names[name] = true
	}
	for _, tag := range queryValues(r, "withTags") {
		tags[tag] = true
	}
	var draftFilter *bool
	if raw := r.URL.Query().Get("draft"); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			jiraError(w, http.StatusBadRequest, "draft must be true or false.")
			return
		}
		draftFilter = &parsed
	}
	expandTransition := false
	for _, option := range queryValues(r, "expand") {
		if option == "transition" {
			expandTransition = true
		}
	}
	startAt, maxResults, ok := pageRequest(w, r, 10, 50)
	if !ok {
		return
	}
	workflows, err := h.Store.ListWorkflows(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	sort.SliceStable(workflows, func(i, j int) bool { return workflows[i].Name < workflows[j].Name })
	values := []map[string]any{}
	for _, flow := range workflows {
		if len(names) > 0 && !names[flow.Name] {
			continue
		}
		for _, draft := range []bool{false, true} {
			if draftFilter != nil && *draftFilter != draft {
				continue
			}
			version, found := h.loadWorkflowVersion(r, workspaceID, workflows, workflowRef{Name: flow.Name, Draft: draft})
			if !found {
				continue
			}
			entry := map[string]any{"workflowId": map[string]any{"name": flow.Name, "draft": draft}}
			lists := map[string][]map[string]any{}
			for kind := range wantedTypes {
				lists[kind] = []map[string]any{}
			}
			matched := 0
			for _, ref := range appRules(version, installation.Key) {
				if !wantedTypes[ref.kind] || (len(keys) > 0 && !keys[ref.rule.Parameters["key"]]) || (len(tags) > 0 && !tags[ref.rule.Parameters["tag"]]) {
					continue
				}
				lists[ref.kind] = append(lists[ref.kind], appRuleBean(ref, expandTransition))
				matched++
			}
			if matched == 0 {
				continue
			}
			for kind, field := range map[string]string{"postfunction": "postFunctions", "condition": "conditions", "validator": "validators"} {
				if wantedTypes[kind] {
					entry[field] = lists[kind]
				}
			}
			values = append(values, entry)
		}
	}
	start := min(startAt, len(values))
	end := min(start+maxResults, len(values))
	writeJSON(w, http.StatusOK, h.pageBean(r, startAt, maxResults, len(values), values[start:end], end-start))
}

func (h *Handler) updateAppWorkflowRules(w http.ResponseWriter, r *http.Request, workspaceID, userID string, installation *models.AppInstallation, remove bool) {
	type ruleUpdate struct {
		ID            string `json:"id"`
		Key           string `json:"key"`
		Configuration *struct {
			Value    *string `json:"value"`
			Disabled *bool   `json:"disabled"`
			Tag      *string `json:"tag"`
		} `json:"configuration"`
	}
	var request struct {
		Workflows []struct {
			WorkflowID      workflowRef  `json:"workflowId"`
			PostFunctions   []ruleUpdate `json:"postFunctions"`
			Conditions      []ruleUpdate `json:"conditions"`
			Validators      []ruleUpdate `json:"validators"`
			WorkflowRuleIDs []string     `json:"workflowRuleIds"`
		} `json:"workflows"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&request); err != nil || request.Workflows == nil {
		jiraFieldError(w, http.StatusBadRequest, map[string]string{"workflows": "The list of workflows is required."})
		return
	}
	workflows, err := h.Store.ListWorkflows(r.Context(), workspaceID)
	if err != nil {
		jiraError(w, http.StatusInternalServerError, "internal error")
		return
	}
	results := []map[string]any{}
	for _, item := range request.Workflows {
		result := map[string]any{"workflowId": map[string]any{"name": item.WorkflowID.Name, "draft": item.WorkflowID.Draft}, "ruleUpdateErrors": map[string][]string{}, "updateErrors": []string{}}
		ruleErrors := result["ruleUpdateErrors"].(map[string][]string)
		version, found := h.loadWorkflowVersion(r, workspaceID, workflows, item.WorkflowID)
		if !found {
			result["updateErrors"] = []string{"The workflow " + item.WorkflowID.Name + " does not exist."}
			results = append(results, result)
			continue
		}
		refs := appRules(version, installation.Key)
		byID := map[string]appRuleRef{}
		for _, ref := range refs {
			byID[ref.kind+"/"+ref.rule.ID] = ref
			byID["any/"+ref.rule.ID] = ref
		}
		changed := false
		if remove {
			removeIDs := map[string]bool{}
			for _, id := range item.WorkflowRuleIDs {
				if _, ok := byID["any/"+id]; !ok {
					ruleErrors[id] = []string{"The transition rule does not exist or belongs to another app."}
					continue
				}
				removeIDs[id] = true
			}
			if len(removeIDs) > 0 {
				removeAppRules(version, removeIDs)
				changed = true
			}
		} else {
			apply := func(kind string, updates []ruleUpdate) {
				for _, update := range updates {
					ref, ok := byID[kind+"/"+update.ID]
					if !ok {
						ruleErrors[update.ID] = []string{"The transition rule does not exist or belongs to another app."}
						continue
					}
					if update.Configuration == nil || update.Configuration.Value == nil {
						ruleErrors[update.ID] = []string{"The rule configuration value is required."}
						continue
					}
					ref.rule.Parameters["config"] = *update.Configuration.Value
					ref.rule.Parameters["disabled"] = strconv.FormatBool(update.Configuration.Disabled != nil && *update.Configuration.Disabled)
					if update.Configuration.Tag != nil {
						ref.rule.Parameters["tag"] = *update.Configuration.Tag
					}
					changed = true
				}
			}
			apply("postfunction", item.PostFunctions)
			apply("condition", item.Conditions)
			apply("validator", item.Validators)
		}
		if changed {
			if err := h.Store.SaveWorkflowRules(r.Context(), workspaceID, userID, *version, item.WorkflowID.Draft); err != nil {
				result["updateErrors"] = []string{err.Error()}
			}
		}
		results = append(results, result)
	}
	writeJSON(w, http.StatusOK, map[string]any{"updateResults": results})
}

func removeAppRules(wf *workflow.Workflow, ids map[string]bool) {
	keep := func(rules []workflow.Rule) []workflow.Rule {
		kept := rules[:0]
		for _, rule := range rules {
			if !(workflow.IsAppRule(rule.RuleKey) && ids[rule.ID]) {
				kept = append(kept, rule)
			}
		}
		return kept
	}
	var prune func(group *workflow.ConditionGroup)
	prune = func(group *workflow.ConditionGroup) {
		if group == nil {
			return
		}
		group.Conditions = keep(group.Conditions)
		for index := range group.ConditionGroups {
			prune(&group.ConditionGroups[index])
		}
	}
	for index := range wf.Transitions {
		transition := &wf.Transitions[index]
		transition.Actions = keep(transition.Actions)
		transition.Validators = keep(transition.Validators)
		prune(transition.Conditions)
	}
}
