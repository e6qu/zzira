package automation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// ManualTriggerType is the trigger of rules people run from a work item
// themselves, rather than a rule starting from an event or a schedule.
const ManualTriggerType = "jira.manual.trigger.issue.action"

const manualSearchLimit = 50

// authorizeMember admits anyone who may use the site.
func (h *Handler) authorizeMember(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	userID, err := authn.Identify(r.Context(), h.Service.Store, r)
	if err != nil {
		automationError(w, http.StatusForbidden, "automation.authentication.required", "User is not authenticated", "")
		return "", "", false
	}
	workspaceID, err := h.Service.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		automationError(w, http.StatusInternalServerError, "automation.workspace.not_found", "Workspace is not configured", "")
		return "", "", false
	}
	member, err := authz.CanSeeWorkspace(r.Context(), h.Service.Store, workspaceID, userID)
	if err != nil || !member {
		automationError(w, http.StatusForbidden, "automation.permission.denied", "You do not have permission to use automation on this site", "")
		return "", "", false
	}
	return workspaceID, userID, true
}

// ---- ARIs ----

func projectARI(cloudID, projectID string) string {
	return "ari:cloud:jira:" + cloudID + ":project/" + projectID
}

func siteARI(cloudID string) string {
	return "ari:cloud:jira::site/" + cloudID
}

// parseObjectARI splits a Jira object ARI into its type and id.
func parseObjectARI(cloudID, ari string) (string, string, bool) {
	prefix := "ari:cloud:jira:" + cloudID + ":"
	rest, found := strings.CutPrefix(ari, prefix)
	if !found {
		if strings.HasPrefix(ari, "ari:cloud:jira:") {
			parts := strings.SplitN(strings.TrimPrefix(ari, "ari:cloud:jira:"), ":", 2)
			if len(parts) == 2 {
				kind, id, ok := strings.Cut(parts[1], "/")
				return kind, id, ok && id != ""
			}
		}
		return "", "", false
	}
	kind, id, ok := strings.Cut(rest, "/")
	return kind, id, ok && id != ""
}

// ruleAppliesTo reports whether a rule's scope covers a project.
func ruleAppliesTo(rule *Rule, cloudID, projectID string) bool {
	if len(rule.RuleScopeARIs) == 0 {
		return true
	}
	return contains(rule.RuleScopeARIs, siteARI(cloudID)) || contains(rule.RuleScopeARIs, projectARI(cloudID, projectID))
}

type manualTrigger struct {
	Value struct {
		InputPrompts []map[string]any `json:"inputPrompts"`
	} `json:"value"`
}

func manualInputPrompts(rule *Rule) []map[string]any {
	var payload struct {
		Trigger manualTrigger `json:"trigger"`
	}
	_ = json.Unmarshal(rule.Payload, &payload)
	prompts := []map[string]any{}
	for _, prompt := range payload.Trigger.Value.InputPrompts {
		bean := map[string]any{
			"inputType": prompt["inputType"], "displayName": prompt["displayName"],
			"required": prompt["required"] == true, "variableName": prompt["variableName"],
		}
		if value, ok := prompt["defaultValue"]; ok {
			bean["defaultValue"] = value
		}
		prompts = append(prompts, bean)
	}
	return prompts
}

// ---- manual rule search and invocation ----

type manualCursor struct {
	Objects []string `json:"objects"`
	Offset  int      `json:"offset"`
}

func encodeManualCursor(cursor manualCursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeManualCursor(value string) (manualCursor, error) {
	var cursor manualCursor
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Offset < 0 || len(cursor.Objects) == 0 {
		return cursor, errors.New("invalid cursor")
	}
	return cursor, nil
}

func (h *Handler) manualRuleRoute(w http.ResponseWriter, r *http.Request, workspaceID, userID, cloudID string, tail []string) {
	switch {
	case len(tail) == 1 && tail[0] == "search" && r.Method == http.MethodGet:
		query := r.URL.Query()
		for key := range query {
			if key != "cursor" && key != "limit" {
				automationError(w, http.StatusBadRequest, "automation.search.unsupported", "Only cursor is allowed as a parameter for the GET search", key)
				return
			}
		}
		cursor, err := decodeManualCursor(query.Get("cursor"))
		if err != nil {
			automationError(w, http.StatusBadRequest, "automation.cursor.invalid", "The cursor is invalid or expired", "cursor")
			return
		}
		limit, ok := searchLimit(w, query.Get("limit"))
		if !ok {
			return
		}
		h.searchManualRules(w, r, workspaceID, userID, cloudID, cursor, limit)
	case len(tail) == 1 && tail[0] == "search" && r.Method == http.MethodPost:
		var request struct {
			Objects []string `json:"objects"`
			Cursor  string   `json:"cursor"`
			Limit   *float64 `json:"limit"`
		}
		if !decodeJSON(w, r, &request) {
			return
		}
		if (len(request.Objects) == 0) == (request.Cursor == "") {
			automationError(w, http.StatusBadRequest, "automation.search.invalid", "Either objects or cursor is required, but not both", "objects")
			return
		}
		limit := manualSearchLimit
		if request.Limit != nil {
			if *request.Limit < 1 || *request.Limit > 100 {
				automationError(w, http.StatusBadRequest, "automation.limit.invalid", "limit must be between 1 and 100", "limit")
				return
			}
			limit = int(*request.Limit)
		}
		cursor := manualCursor{Objects: request.Objects}
		if request.Cursor != "" {
			var err error
			if cursor, err = decodeManualCursor(request.Cursor); err != nil {
				automationError(w, http.StatusBadRequest, "automation.cursor.invalid", "The cursor is invalid or expired", "cursor")
				return
			}
		}
		h.searchManualRules(w, r, workspaceID, userID, cloudID, cursor, limit)
	case len(tail) == 2 && tail[1] == "invocation" && r.Method == http.MethodPost:
		h.invokeManualRule(w, r, workspaceID, userID, cloudID, tail[0])
	default:
		automationError(w, http.StatusNotFound, "automation.resource.not_found", "No resource found", "")
	}
}

func searchLimit(w http.ResponseWriter, raw string) (int, bool) {
	if raw == "" {
		return manualSearchLimit, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 100 {
		automationError(w, http.StatusBadRequest, "automation.limit.invalid", "limit must be between 1 and 100", "limit")
		return 0, false
	}
	return limit, true
}

// visibleIssueForARI resolves an issue ARI the user can see.
func (h *Handler) visibleIssueForARI(ctx context.Context, workspaceID, userID, cloudID, ari string) (*models.Issue, string) {
	kind, id, ok := parseObjectARI(cloudID, ari)
	if !ok {
		return nil, "invalid"
	}
	if kind != "issue" {
		return nil, kind
	}
	issue, err := h.Service.Store.IssueByIDOrKey(ctx, workspaceID, id)
	if err != nil {
		return nil, "missing"
	}
	visible, err := authz.CanSeeIssue(ctx, h.Service.Store, workspaceID, issue.ProjectID, userID, issue.ID, issue.SecurityLevelID)
	if err != nil || !visible {
		return nil, "missing"
	}
	return issue, "issue"
}

func (h *Handler) searchManualRules(w http.ResponseWriter, r *http.Request, workspaceID, userID, cloudID string, cursor manualCursor, limit int) {
	kinds := map[string]bool{}
	projectIDs := map[string]bool{}
	for _, ari := range cursor.Objects {
		issue, kind := h.visibleIssueForARI(r.Context(), workspaceID, userID, cloudID, ari)
		switch kind {
		case "invalid":
			automationError(w, http.StatusBadRequest, "automation.object.invalid", "The object "+ari+" is not a valid ARI", "objects")
			return
		case "missing":
			automationError(w, http.StatusForbidden, "automation.permission.denied", "You are not permitted to fetch rules for the given target object", "objects")
			return
		case "issue", "alert":
			kinds[kind] = true
		default:
			automationError(w, http.StatusBadRequest, "automation.object.unsupported", "Only issue and alert objects are supported", "objects")
			return
		}
		if issue != nil {
			projectIDs[issue.ProjectID] = true
		}
	}
	if len(kinds) > 1 {
		automationError(w, http.StatusBadRequest, "automation.object.mixed", "Only one type of object is allowed in a request", "objects")
		return
	}
	page, err := h.Service.Rules(r.Context(), workspaceID, SummaryFilter{States: []string{"ENABLED"}, Triggers: []string{ManualTriggerType}, Limit: maxRulePage})
	if err != nil {
		automationError(w, http.StatusInternalServerError, "automation.search.failed", "Rules could not be searched", "")
		return
	}
	rules := []map[string]any{}
	if kinds["issue"] {
	rules:
		for _, rule := range page.Rules {
			for projectID := range projectIDs {
				if !ruleAppliesTo(rule, cloudID, projectID) {
					continue rules
				}
			}
			rules = append(rules, map[string]any{"id": rule.UUID, "name": rule.Name, "userInputs": manualInputPrompts(rule)})
		}
	}
	offset := min(cursor.Offset, len(rules))
	end := min(offset+limit, len(rules))
	links := map[string]any{"self": "cursor=" + url.QueryEscape(encodeManualCursor(cursor)) + "&limit=" + strconv.Itoa(limit), "next": nil, "prev": nil}
	if end < len(rules) {
		links["next"] = "cursor=" + url.QueryEscape(encodeManualCursor(manualCursor{Objects: cursor.Objects, Offset: end})) + "&limit=" + strconv.Itoa(limit)
	}
	if offset > 0 {
		links["prev"] = "cursor=" + url.QueryEscape(encodeManualCursor(manualCursor{Objects: cursor.Objects, Offset: max(0, offset-limit)})) + "&limit=" + strconv.Itoa(limit)
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": rules[offset:end], "links": links})
}

func (h *Handler) invokeManualRule(w http.ResponseWriter, r *http.Request, workspaceID, userID, cloudID, ruleID string) {
	var request struct {
		Objects    []string `json:"objects"`
		UserInputs map[string]struct {
			InputType string `json:"inputType"`
			Value     any    `json:"value"`
		} `json:"userInputs"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	if len(request.Objects) < 1 || len(request.Objects) > 50 {
		automationError(w, http.StatusBadRequest, "automation.objects.invalid", "Between 1 and 50 objects are required", "objects")
		return
	}
	rule, err := h.Service.Rule(r.Context(), workspaceID, ruleID)
	if err != nil {
		automationError(w, http.StatusNotFound, "automation.rule.not_found", "Rule not found", "ruleId")
		return
	}
	// What was typed travels with the run: the rule's actions read it as
	// {{userInputs.<name>}}, which is the point of asking.
	inputs := map[string]string{}
	for name, input := range request.UserInputs {
		inputs[name] = fmt.Sprint(input.Value)
	}
	for _, prompt := range manualInputPrompts(rule) {
		name, _ := prompt["variableName"].(string)
		if prompt["required"] == true {
			if _, ok := request.UserInputs[name]; !ok {
				automationError(w, http.StatusBadRequest, "automation.input.required", "The input "+name+" is required", "userInputs."+name)
				return
			}
		}
	}
	invocable := rule.State == "ENABLED" && triggerType(rule.Payload) == ManualTriggerType
	components, componentErr := ruleComponents(rule.Payload)
	results := map[string]string{}
	runner := &Runner{Service: h.Service}
	for _, ari := range request.Objects {
		issue, kind := h.visibleIssueForARI(r.Context(), workspaceID, userID, cloudID, ari)
		switch {
		case kind == "invalid" || kind == "missing" || (kind != "issue" && kind != "alert"):
			results[ari] = "INVALID_TARGET_OBJECT"
			continue
		case !invocable || kind != "issue" || componentErr != nil:
			results[ari] = "INVALID_RULE_OR_OBJECT"
			continue
		case !ruleAppliesTo(rule, cloudID, issue.ProjectID):
			results[ari] = "INVALID_TARGET_SCOPE"
			continue
		}
		run := &claimedRun{Run: Run{RuleUUID: rule.UUID}, WorkspaceID: workspaceID, ActorID: rule.ActorID,
			Payload: rule.Payload, InitiatorID: userID, RuleName: rule.Name, UserInputs: inputs}
		changed, executionErr := runner.runComponents(store.WithAutomationRule(r.Context(), rule.UUID), run, issue, components)
		if err := h.Service.recordManualRun(r.Context(), rule.UUID, changed, executionErr); err != nil {
			automationError(w, http.StatusInternalServerError, "automation.run.failed", "The rule run could not be recorded", "")
			return
		}
		results[ari] = "SUCCESS"
	}
	writeJSON(w, http.StatusOK, results)
}

// ManualInput is one answer to a rule's prompt, in the shape the run reads.
type ManualInput struct {
	Name  string
	Value string
}

// RunManualRule runs one manual rule for one work item as the rule's actor,
// with what the person running it typed, and records the run. It is what both
// the Automation API and the work item's own Run automation control do, so a
// rule behaves the same whichever asked for it.
func (s *Service) RunManualRule(ctx context.Context, workspaceID, initiatorID string, rule *Rule, issue *models.Issue, inputs map[string]string) error {
	components, err := ruleComponents(rule.Payload)
	if err != nil {
		return err
	}
	runner := &Runner{Service: s}
	run := &claimedRun{Run: Run{RuleUUID: rule.UUID}, WorkspaceID: workspaceID, ActorID: rule.ActorID,
		Payload: rule.Payload, InitiatorID: initiatorID, RuleName: rule.Name, UserInputs: inputs}
	changed, executionErr := runner.runComponents(store.WithAutomationRule(ctx, rule.UUID), run, issue, components)
	if err := s.recordManualRun(ctx, rule.UUID, changed, executionErr); err != nil {
		return err
	}
	return executionErr
}

// MissingManualInput names a prompt the rule requires that was not answered,
// or is empty when every required prompt has one.
func MissingManualInput(rule *Rule, inputs map[string]string) string {
	for _, prompt := range manualInputPrompts(rule) {
		name, _ := prompt["variableName"].(string)
		if prompt["required"] != true {
			continue
		}
		if value, ok := inputs[name]; !ok || strings.TrimSpace(value) == "" {
			// The question as it was asked, because this is read by whoever
			// was asked it; the variable name only if the rule has no question.
			if display, _ := prompt["displayName"].(string); strings.TrimSpace(display) != "" {
				return display
			}
			return name
		}
	}
	return ""
}

// ManualPrompts are a rule's prompts, for a page that asks them.
func (s *Service) ManualPrompts(rule *Rule) []map[string]any { return manualInputPrompts(rule) }

// IsManualRule reports a rule people run themselves.
func IsManualRule(rule *Rule) bool {
	return rule != nil && rule.State == "ENABLED" && triggerType(rule.Payload) == ManualTriggerType
}

// AppliesToProject reports whether a rule's scope covers a project.
func (s *Service) AppliesToProject(ctx context.Context, workspaceID string, rule *Rule, projectID string) (bool, error) {
	cloudID, err := s.WorkspaceCloudID(ctx, workspaceID)
	if err != nil {
		return false, err
	}
	return ruleAppliesTo(rule, cloudID, projectID), nil
}

// recordManualRun keeps a completed run in the rule's audit history.
func (s *Service) recordManualRun(ctx context.Context, ruleUUID string, changed bool, executionErr error) error {
	id, err := NewUUIDv7()
	if err != nil {
		return err
	}
	state, detail, changedCount := "NO_ACTIONS", "Manually triggered", 0
	if changed {
		state, changedCount = "SUCCESS", 1
	}
	if executionErr != nil {
		state, detail = "FAILED", executionErr.Error()
	}
	now := time.Now().UTC()
	_, err = s.Store.Pool.Exec(ctx, `
		INSERT INTO automation_runs(id,rule_uuid,scheduled_for,state,attempts,available_at,started_at,completed_at,matched_count,changed_count,detail)
		VALUES($1,$2,$3,$4,1,$3,$3,$3,1,$5,$6)`, id, ruleUUID, now, state, changedCount, detail)
	return err
}

// ---- templates ----

type templateParameter struct {
	Type     string `json:"type"`
	Key      string `json:"key"`
	Required bool   `json:"required"`
}

type ruleTemplate struct {
	ID           string
	Name         string
	Description  string
	Categories   []string
	Parameters   []templateParameter
	TriggerIcons []string
	ActionIcons  []string
	Build        func(values map[string]any) (map[string]any, []map[string]any)
}

var templateCategoryNames = map[string]string{
	"jira-software.software": "Software",
	"popular":                "Popular",
	"issue-management":       "Work item management",
	"scheduled":              "Scheduled",
	"manual":                 "Manually triggered",
}

func textValue(values map[string]any, key, fallback string) string {
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func numberValue(values map[string]any, key string, fallback int) int {
	if value, ok := values[key].(float64); ok && value > 0 {
		return int(value)
	}
	return fallback
}

func actionComponent(kind string, value map[string]any) map[string]any {
	return map[string]any{"component": "ACTION", "type": kind, "schemaVersion": 1, "value": value}
}

var ruleTemplates = []ruleTemplate{
	{
		ID: "scheduled-label-stale-work", Name: "Label stale work",
		Description:  "Every day, label open work items that have not been updated for a number of days.",
		Categories:   []string{"popular", "scheduled", "issue-management"},
		Parameters:   []templateParameter{{Type: "TEXT", Key: "label", Required: false}, {Type: "NUMBER", Key: "days", Required: false}},
		TriggerIcons: []string{"jira.jql.scheduled"}, ActionIcons: []string{"jira.issue.add-label"},
		Build: func(values map[string]any) (map[string]any, []map[string]any) {
			jql := fmt.Sprintf("updated <= -%dd AND statusCategory != Done", numberValue(values, "days", 30))
			return map[string]any{"component": "TRIGGER", "type": "jira.jql.scheduled", "schemaVersion": 1, "value": map[string]any{"intervalMinutes": 1440, "timezone": "UTC", "jql": jql}},
				[]map[string]any{actionComponent("jira.issue.add-label", map[string]any{"label": textValue(values, "label", "stale")})}
		},
	},
	{
		ID: "scheduled-assign-unassigned", Name: "Assign unassigned work",
		Description:  "Every hour, assign open work items that have no assignee to a chosen person.",
		Categories:   []string{"jira-software.software", "scheduled", "issue-management"},
		Parameters:   []templateParameter{{Type: "TEXT", Key: "assigneeAccountId", Required: true}},
		TriggerIcons: []string{"jira.jql.scheduled"}, ActionIcons: []string{"jira.issue.assign"},
		Build: func(values map[string]any) (map[string]any, []map[string]any) {
			return map[string]any{"component": "TRIGGER", "type": "jira.jql.scheduled", "schemaVersion": 1, "value": map[string]any{"intervalMinutes": 60, "timezone": "UTC", "jql": "assignee is EMPTY AND statusCategory != Done"}},
				[]map[string]any{actionComponent("jira.issue.assign", map[string]any{"accountId": textValue(values, "assigneeAccountId", "")})}
		},
	},
	{
		ID: "manual-assign-to-me", Name: "Assign to me",
		Description:  "From a work item, assign it to the rule's actor.",
		Categories:   []string{"popular", "manual", "issue-management"},
		Parameters:   []templateParameter{},
		TriggerIcons: []string{ManualTriggerType}, ActionIcons: []string{"jira.issue.assign"},
		Build: func(values map[string]any) (map[string]any, []map[string]any) {
			return map[string]any{"component": "TRIGGER", "type": ManualTriggerType, "schemaVersion": 1, "value": map[string]any{"inputPrompts": []any{}}},
				[]map[string]any{actionComponent("jira.issue.assign", map[string]any{"accountId": "ACTOR"})}
		},
	},
	{
		ID: "manual-transition", Name: "Move to a status",
		Description:  "From a work item, move it to a chosen status.",
		Categories:   []string{"jira-software.software", "manual", "issue-management"},
		Parameters:   []templateParameter{{Type: "TEXT", Key: "statusId", Required: true}},
		TriggerIcons: []string{ManualTriggerType}, ActionIcons: []string{"jira.issue.transition"},
		Build: func(values map[string]any) (map[string]any, []map[string]any) {
			return map[string]any{"component": "TRIGGER", "type": ManualTriggerType, "schemaVersion": 1, "value": map[string]any{"inputPrompts": []any{}}},
				[]map[string]any{actionComponent("jira.issue.transition", map[string]any{"statusId": textValue(values, "statusId", "")})}
		},
	},
}

func templateBean(template ruleTemplate) map[string]any {
	categories := make([]map[string]string, 0, len(template.Categories))
	for _, key := range template.Categories {
		categories = append(categories, map[string]string{"key": key, "displayName": templateCategoryNames[key]})
	}
	return map[string]any{
		"id": template.ID, "description": template.Description, "categories": categories, "parameters": template.Parameters,
		"displayMetadata": map[string]any{"triggerIcons": template.TriggerIcons, "actionIcons": template.ActionIcons},
	}
}

func templateByID(id string) (ruleTemplate, bool) {
	for _, template := range ruleTemplates {
		if template.ID == id {
			return template, true
		}
	}
	return ruleTemplate{}, false
}

type templateCursor struct {
	Categories []string `json:"categories,omitempty"`
	RuleHome   string   `json:"ruleHome,omitempty"`
	Offset     int      `json:"offset"`
}

// validRuleHome accepts the site or a project in it.
func validRuleHome(cloudID, ari string) bool {
	if ari == siteARI(cloudID) {
		return true
	}
	kind, _, ok := parseObjectARI(cloudID, ari)
	return ok && kind == "project" && strings.HasPrefix(ari, "ari:cloud:jira:"+cloudID+":")
}

func (h *Handler) templateRoute(w http.ResponseWriter, r *http.Request, workspaceID, userID, cloudID string, tail []string) {
	switch {
	case len(tail) == 1 && tail[0] == "search" && r.Method == http.MethodGet:
		query := r.URL.Query()
		filters := len(query["categories"]) > 0 || query.Get("ruleHome") != ""
		if query.Get("cursor") != "" && filters {
			automationError(w, http.StatusBadRequest, "automation.search.invalid", "Either a cursor or filter parameters are permitted, but not both", "cursor")
			return
		}
		limit, ok := searchLimit(w, query.Get("limit"))
		if !ok {
			return
		}
		cursor := templateCursor{Categories: query["categories"], RuleHome: query.Get("ruleHome")}
		if raw := query.Get("cursor"); raw != "" {
			decoded, err := base64.RawURLEncoding.DecodeString(raw)
			if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Offset < 0 {
				automationError(w, http.StatusBadRequest, "automation.cursor.invalid", "The cursor is invalid or expired", "cursor")
				return
			}
		}
		h.searchTemplates(w, cloudID, cursor, limit)
	case len(tail) == 1 && tail[0] == "search" && r.Method == http.MethodPost:
		var request struct {
			Categories []string `json:"categories"`
			RuleHome   string   `json:"ruleHome"`
			Cursor     string   `json:"cursor"`
			Limit      *float64 `json:"limit"`
		}
		if !decodeJSON(w, r, &request) {
			return
		}
		if request.Cursor != "" && (len(request.Categories) > 0 || request.RuleHome != "") {
			automationError(w, http.StatusBadRequest, "automation.search.invalid", "Either a cursor or filters are permitted, but not both", "cursor")
			return
		}
		if len(request.Categories) > 50 {
			automationError(w, http.StatusBadRequest, "automation.categories.invalid", "At most 50 categories are allowed", "categories")
			return
		}
		limit := manualSearchLimit
		if request.Limit != nil {
			if *request.Limit < 1 || *request.Limit > 100 {
				automationError(w, http.StatusBadRequest, "automation.limit.invalid", "limit must be between 1 and 100", "limit")
				return
			}
			limit = int(*request.Limit)
		}
		cursor := templateCursor{Categories: request.Categories, RuleHome: request.RuleHome}
		if request.Cursor != "" {
			decoded, err := base64.RawURLEncoding.DecodeString(request.Cursor)
			if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.Offset < 0 {
				automationError(w, http.StatusBadRequest, "automation.cursor.invalid", "The cursor is invalid or expired", "cursor")
				return
			}
		}
		h.searchTemplates(w, cloudID, cursor, limit)
	case len(tail) == 1 && tail[0] == "create" && r.Method == http.MethodPost:
		h.createRuleFromTemplate(w, r, workspaceID, userID, cloudID)
	case len(tail) == 1 && r.Method == http.MethodGet:
		template, ok := templateByID(tail[0])
		if !ok {
			automationError(w, http.StatusNotFound, "automation.template.not_found", "Template not found", "templateId")
			return
		}
		writeJSON(w, http.StatusOK, templateBean(template))
	default:
		automationError(w, http.StatusNotFound, "automation.resource.not_found", "No resource found", "")
	}
}

func (h *Handler) searchTemplates(w http.ResponseWriter, cloudID string, cursor templateCursor, limit int) {
	if cursor.RuleHome != "" && !validRuleHome(cloudID, cursor.RuleHome) {
		automationError(w, http.StatusForbidden, "automation.permission.denied", "You are not permitted to fetch templates for the given rule home", "ruleHome")
		return
	}
	matched := []map[string]any{}
	for _, template := range ruleTemplates {
		if len(cursor.Categories) > 0 {
			found := false
			for _, category := range cursor.Categories {
				if contains(template.Categories, category) {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		matched = append(matched, templateBean(template))
	}
	offset := min(cursor.Offset, len(matched))
	end := min(offset+limit, len(matched))
	encode := func(offset int) string {
		next := cursor
		next.Offset = offset
		raw, _ := json.Marshal(next)
		return "cursor=" + url.QueryEscape(base64.RawURLEncoding.EncodeToString(raw)) + "&limit=" + strconv.Itoa(limit)
	}
	links := map[string]any{"self": encode(offset), "next": nil, "prev": nil}
	if end < len(matched) {
		links["next"] = encode(end)
	}
	if offset > 0 {
		links["prev"] = encode(max(0, offset-limit))
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": matched[offset:end], "links": links})
}

func (h *Handler) createRuleFromTemplate(w http.ResponseWriter, r *http.Request, workspaceID, userID, cloudID string) {
	var request struct {
		TemplateID string `json:"templateId"`
		RuleHome   string `json:"ruleHome"`
		Parameters map[string]struct {
			Type  string `json:"type"`
			Value any    `json:"value"`
		} `json:"parameters"`
		State string `json:"state"`
	}
	if !decodeJSON(w, r, &request) {
		return
	}
	parameters := map[string]TemplateValue{}
	for key, supplied := range request.Parameters {
		parameters[key] = TemplateValue{Type: supplied.Type, Value: supplied.Value}
	}
	body, templateErr := BuildTemplateRule(cloudID, request.TemplateID, request.RuleHome, request.State, "", parameters)
	if templateErr != nil {
		automationError(w, templateErr.Status, templateErr.Code, templateErr.Title, templateErr.Field)
		return
	}
	uuid, err := h.Service.CreateRule(r.Context(), workspaceID, userID, body)
	if err != nil {
		ruleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ruleUuid": uuid})
}
