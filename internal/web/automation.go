package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/automation"
	"github.com/e6qu/zzira/internal/cron"
	"github.com/e6qu/zzira/internal/models"
)

type automationRuleCard struct {
	Rule     *automation.Rule
	Schedule string
	NextRun  string
	Trigger  string
}

type automationRulesData struct {
	Rules   []automationRuleCard
	CloudID string
}

type automationActionView struct {
	Type  string
	Value string
}

// automationOption is a choice in the rule editor.
type automationOption struct{ Value, Name string }

// automationTriggerView is the rule's trigger as the editor shows it.
type automationTriggerView struct {
	Type, FromStatus, ToStatus, Fields string
	AllowRules                         bool
}

// automationConditionView is a work item fields or JQL condition in the
// editor; a JQL condition has the field "jql".
type automationConditionView struct{ Field, Operator, Value string }

// automationBranchView is a related work items branch and its actions.
type automationBranchView struct {
	RelatedType, LinkTypes string
	Actions                []automationActionView
}

var (
	automationTriggers = []automationOption{
		{"jira.jql.scheduled", "Scheduled"}, {"jira.issue.event.trigger:created", "Work item created"},
		{"jira.issue.event.trigger:transitioned", "Work item transitioned"}, {"jira.issue.field.changed", "Field value changed"},
		{"jira.issue.event.trigger:commented", "Work item commented"},
	}
	automationActionTypes = []automationOption{
		{"jira.issue.add-label", "Add label"}, {"jira.issue.assign", "Assign work item"}, {"jira.issue.transition", "Transition work item"},
		{"jira.issue.comment", "Comment on work item"}, {"jira.issue.edit:summary", "Edit summary"}, {"jira.issue.edit:duedate", "Set due date"},
	}
	automationRelatedTypes    = []automationOption{{"sub-tasks", "Sub-tasks"}, {"parent", "Parent"}, {"linked", "Linked work items"}}
	automationConditionFields = []automationOption{
		{"jql", "Matches JQL"}, {"status", "Status"}, {"priority", "Priority"}, {"issuetype", "Work type"}, {"assignee", "Assignee"},
		{"reporter", "Reporter"}, {"labels", "Labels"}, {"summary", "Summary"}, {"duedate", "Due date"},
	}
	automationConditionOperators = []automationOption{
		{"EQUALS", "equals"}, {"NOT_EQUALS", "does not equal"}, {"CONTAINS", "contains"}, {"IS_EMPTY", "is empty"}, {"IS_NOT_EMPTY", "is not empty"},
	}
)

func automationOptionName(options []automationOption, value string) string {
	for _, option := range options {
		if option.Value == value {
			return option.Name
		}
	}
	return ""
}

type automationRunView struct {
	Run      automation.Run
	When     string
	Duration string
}

type automationEditorData struct {
	Rule       *automation.Rule
	Trigger    automationTriggerView
	Conditions []automationConditionView
	Actions    []automationActionView
	// Triggers, ActionTypes, ConditionFields and ConditionOperators are the
	// editor's choices.
	Triggers, ActionTypes, ConditionFields, ConditionOperators []automationOption
	// Unsupported names what the editor cannot show in the rule, which turns
	// saving there off so it is not lost.
	Unsupported string
	// Branch is the rule's related work items branch, and RelatedTypes the
	// work a branch can run for.
	Branch       automationBranchView
	RelatedTypes []automationOption
	Runs         []automationRunView
	Members      []*models.User
	Statuses     []models.Status
	CloudID      string
	IsNew        bool
	FormAction   string
	Error        string
}

func (h *Handler) AutomationRules(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	page, err := h.Automation.Rules(r.Context(), workspaceID, automation.SummaryFilter{Limit: 100})
	if err != nil {
		http.Error(w, "could not load automation rules", http.StatusInternalServerError)
		return
	}
	cloudID, err := h.Automation.WorkspaceCloudID(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "could not load cloud ID", http.StatusInternalServerError)
		return
	}
	data := automationRulesData{CloudID: cloudID, Rules: make([]automationRuleCard, 0, len(page.Rules))}
	look := h.siteLook(r, workspaceID)
	for _, rule := range page.Rules {
		card := automationRuleCard{Rule: rule, Trigger: "Imported trigger"}
		if rule.EventTrigger != "" {
			card.Trigger = automationOptionName(automationTriggers, parseAutomationTrigger(rule.Payload).Type)
		}
		if rule.IntervalMinutes != nil {
			card.Trigger = "Scheduled"
			card.Schedule = intervalLabel(*rule.IntervalMinutes)
		}
		if rule.CronExpression != "" {
			card.Trigger = "Scheduled"
			card.Schedule = "Cron " + rule.CronExpression + " in " + rule.ScheduleTimezone
		}
		if rule.NextRunAt != nil && rule.State == "ENABLED" {
			card.NextRun = rule.NextRunAt.In(time.Local).Format(look.DateComplete)
		}
		data.Rules = append(data.Rules, card)
	}
	h.writeWorkspacePage(w, r, "page_automation_rules", user, workspaceID, data, "automation", "")
}

func (h *Handler) AutomationNew(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	data, err := h.automationEditorData(r, workspaceID, nil)
	if err != nil {
		http.Error(w, "could not load rule editor", http.StatusInternalServerError)
		return
	}
	data.IsNew = true
	data.FormAction = "/settings/automation"
	h.writeWorkspacePage(w, r, "page_automation_rule", user, workspaceID, data, "automation", "")
}

func (h *Handler) AutomationCreate(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	body, err := automationPayload(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	user := h.currentUser(r)
	uuid, err := h.Automation.CreateRule(r.Context(), workspaceID, user.ID, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectAutomationRule(w, uuid)
}

func (h *Handler) AutomationRule(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	rule, err := h.Automation.Rule(r.Context(), workspaceID, r.PathValue("uuid"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, err := h.automationEditorData(r, workspaceID, rule)
	if err != nil {
		http.Error(w, "could not load rule editor", http.StatusInternalServerError)
		return
	}
	data.FormAction = "/settings/automation/" + rule.UUID
	h.writeWorkspacePage(w, r, "page_automation_rule", user, workspaceID, data, "automation", "")
}

func (h *Handler) AutomationUpdate(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	uuid := r.PathValue("uuid")
	action := r.PostFormValue("operation")
	var err error
	switch action {
	case "run":
		err = h.Automation.EnqueueNow(r.Context(), workspaceID, uuid)
	case "enable":
		err = h.Automation.SetState(r.Context(), workspaceID, uuid, "ENABLED")
	case "disable":
		err = h.Automation.SetState(r.Context(), workspaceID, uuid, "DISABLED")
	case "delete":
		err = h.Automation.DeleteRule(r.Context(), workspaceID, uuid)
		if err == nil {
			http.Redirect(w, r, "/settings/automation", http.StatusSeeOther)
			return
		}
	case "save":
		var body json.RawMessage
		if existing, loadErr := h.Automation.Rule(r.Context(), workspaceID, uuid); loadErr != nil {
			err = loadErr
		} else if missing := automationEditorUnsupported(existing.Payload); missing != "" {
			err = fmt.Errorf("the editor does not show %s; change this rule through the Automation API", missing)
		} else if body, err = automationPayload(r); err == nil {
			err = h.Automation.UpdateRule(r.Context(), workspaceID, h.currentUser(r).ID, uuid, body)
		}
	default:
		err = fmt.Errorf("unknown operation")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectAutomationRule(w, uuid)
}

func redirectAutomationRule(w http.ResponseWriter, uuid string) {
	w.Header().Set("Location", "/settings/automation/"+url.PathEscape(uuid))
	w.WriteHeader(http.StatusSeeOther)
}

func (h *Handler) automationEditorData(r *http.Request, workspaceID string, rule *automation.Rule) (automationEditorData, error) {
	members, err := h.Store.MembersByWorkspace(r.Context(), workspaceID)
	if err != nil {
		return automationEditorData{}, err
	}
	statuses, err := h.Store.StatusesForWorkspace(r.Context(), workspaceID)
	if err != nil {
		return automationEditorData{}, err
	}
	cloudID, err := h.Automation.WorkspaceCloudID(r.Context(), workspaceID)
	if err != nil {
		return automationEditorData{}, err
	}
	data := automationEditorData{Rule: rule, Members: members, Statuses: statuses, CloudID: cloudID,
		Triggers: automationTriggers, ActionTypes: automationActionTypes, ConditionFields: automationConditionFields, ConditionOperators: automationConditionOperators,
		RelatedTypes: automationRelatedTypes}
	if rule == nil {
		data.Trigger = automationTriggerView{Type: "jira.jql.scheduled"}
		data.Conditions = []automationConditionView{{}}
		data.Branch = automationBranchView{Actions: []automationActionView{}}
		data.Rule = &automation.Rule{State: "ENABLED", ActorID: h.currentUser(r).ID, ScheduleTimezone: h.currentUser(r).TimeZone}
		if data.Rule.ScheduleTimezone == "" {
			data.Rule.ScheduleTimezone = "UTC"
		}
		interval := 60
		data.Rule.IntervalMinutes = &interval
		data.Actions = []automationActionView{{Type: "jira.issue.add-label"}}
		return data, nil
	}
	runs, err := h.Automation.Runs(r.Context(), workspaceID, rule.UUID, 25)
	if err != nil {
		return automationEditorData{}, err
	}
	look := h.siteLook(r, workspaceID)
	for _, run := range runs {
		view := automationRunView{Run: run, When: run.ScheduledFor.In(time.Local).Format(look.DateComplete)}
		if run.StartedAt != nil && run.CompletedAt != nil {
			view.Duration = run.CompletedAt.Sub(*run.StartedAt).Round(time.Millisecond).String()
		}
		data.Runs = append(data.Runs, view)
	}
	data.Actions = parseAutomationActions(rule.Payload)
	data.Trigger = parseAutomationTrigger(rule.Payload)
	data.Conditions = append(parseAutomationConditions(rule.Payload), automationConditionView{})
	data.Unsupported = automationEditorUnsupported(rule.Payload)
	data.Branch = parseAutomationBranch(rule.Payload)
	return data, nil
}

func automationPayload(r *http.Request) (json.RawMessage, error) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" || len(name) > 255 {
		return nil, fmt.Errorf("rule name is required and must be at most 255 characters")
	}
	query := strings.TrimSpace(r.PostFormValue("jql"))
	triggerType := r.PostFormValue("trigger_type")
	if triggerType == "" {
		triggerType = "jira.jql.scheduled"
	}
	var triggerValue map[string]any
	switch triggerType {
	case "jira.jql.scheduled":
		timezone := strings.TrimSpace(r.PostFormValue("timezone"))
		if timezone == "" {
			timezone = "UTC"
		}
		if _, err := time.LoadLocation(timezone); err != nil {
			return nil, fmt.Errorf("timezone is not a valid IANA timezone")
		}
		if expression := strings.TrimSpace(r.PostFormValue("cron_expression")); expression != "" {
			if _, err := cron.Parse(expression); err != nil {
				return nil, fmt.Errorf("cron expression: %w", err)
			}
			triggerValue = map[string]any{"timezone": timezone, "jql": query, "schedule": map[string]any{"method": "CRON_EXPRESSION", "cronExpression": expression}}
			break
		}
		interval, err := strconv.Atoi(r.PostFormValue("interval_minutes"))
		if err != nil || interval < 1 || interval > 43200 {
			return nil, fmt.Errorf("schedule must be a cron expression or an interval between 1 minute and 30 days")
		}
		triggerValue = map[string]any{"intervalMinutes": interval, "timezone": timezone, "jql": query}
	case "jira.issue.event.trigger:created", "jira.issue.event.trigger:commented":
		triggerValue = map[string]any{"jql": query}
	case "jira.issue.event.trigger:transitioned":
		triggerValue = map[string]any{"jql": query, "fromStatusIds": nonEmpty(r.PostFormValue("from_status")), "toStatusIds": nonEmpty(r.PostFormValue("to_status"))}
	case "jira.issue.field.changed":
		fields := splitLines(r.PostFormValue("changed_fields"))
		if len(fields) == 0 || len(fields) > 20 {
			return nil, fmt.Errorf("choose between 1 and 20 fields for the field value changed trigger")
		}
		triggerValue = map[string]any{"jql": query, "fields": fields}
	default:
		return nil, fmt.Errorf("unsupported trigger")
	}
	components := []map[string]any{}
	conditionOperators := r.PostForm["condition_operator"]
	conditionValues := r.PostForm["condition_value"]
	for index, field := range r.PostForm["condition_field"] {
		if field = strings.TrimSpace(field); field == "" {
			continue
		}
		operator, value := "", ""
		if index < len(conditionOperators) {
			operator = conditionOperators[index]
		}
		if index < len(conditionValues) {
			value = strings.TrimSpace(conditionValues[index])
		}
		if field == "jql" {
			if value == "" {
				return nil, fmt.Errorf("a JQL condition needs a query")
			}
			components = append(components, map[string]any{
				"component": "CONDITION", "schemaVersion": 1, "type": "jira.jql.condition", "value": map[string]string{"jql": value},
			})
			continue
		}
		if automationOptionName(automationConditionFields, field) == "" || automationOptionName(automationConditionOperators, operator) == "" {
			return nil, fmt.Errorf("unsupported condition")
		}
		if value == "" && operator != "IS_EMPTY" && operator != "IS_NOT_EMPTY" {
			return nil, fmt.Errorf("every condition that compares needs a value")
		}
		components = append(components, map[string]any{
			"component": "CONDITION", "schemaVersion": 1, "type": "jira.issue.condition",
			"value": map[string]string{"field": field, "operator": operator, "value": value},
		})
	}
	actionComponents, err := automationFormActions(r.PostForm["action_type"], r.PostForm["action_value"])
	if err != nil {
		return nil, err
	}
	components = append(components, actionComponents...)
	actions := len(actionComponents)
	if related := strings.TrimSpace(r.PostFormValue("branch_related")); related != "" {
		if automationOptionName(automationRelatedTypes, related) == "" {
			return nil, fmt.Errorf("unsupported related work items")
		}
		children, err := automationFormActions(r.PostForm["branch_action_type"], r.PostForm["branch_action_value"])
		if err != nil {
			return nil, err
		}
		if len(children) == 0 {
			return nil, fmt.Errorf("add at least one action for the related work items")
		}
		value := map[string]any{"relatedType": related}
		if linkTypes := splitLines(r.PostFormValue("branch_link_types")); related == "linked" && len(linkTypes) > 0 {
			value["linkTypes"] = linkTypes
		}
		components = append(components, map[string]any{"component": "BRANCH", "schemaVersion": 1, "type": "jira.issue.related", "value": value, "children": children})
		actions += len(children)
	}
	if actions == 0 {
		return nil, fmt.Errorf("add at least one action")
	}
	scope := splitLines(r.PostFormValue("scope_aris"))
	rule := map[string]any{
		"actor":               map[string]string{"actor": r.PostFormValue("actor_id"), "type": "ACCOUNT_ID"},
		"canOtherRuleTrigger": r.PostFormValue("allow_rule_trigger") == "true", "collaborators": []string{}, "components": components,
		"description": strings.TrimSpace(r.PostFormValue("description")), "labels": []string{},
		"name": name, "notifyOnError": "FIRSTERROR", "ruleScopeARIs": scope,
		"state": strings.ToUpper(r.PostFormValue("state")), "writeAccessType": "OWNER_ONLY",
		"trigger": map[string]any{"component": "TRIGGER", "schemaVersion": 1, "type": triggerType, "value": triggerValue},
	}
	return json.Marshal(map[string]any{"rule": rule, "connections": []any{}})
}

// automationFormActions reads the editor's action rows into rule components.
func automationFormActions(types, values []string) ([]map[string]any, error) {
	components := []map[string]any{}
	for index, actionType := range types {
		actionType = strings.TrimSpace(actionType)
		if actionType == "" {
			continue
		}
		value := ""
		if index < len(values) {
			value = strings.TrimSpace(values[index])
		}
		if value == "" {
			return nil, fmt.Errorf("every action needs a value")
		}
		var actionValue map[string]string
		switch actionType {
		case "jira.issue.add-label":
			actionValue = map[string]string{"label": value}
		case "jira.issue.assign":
			actionValue = map[string]string{"accountId": value}
		case "jira.issue.transition":
			actionValue = map[string]string{"statusId": value}
		case "jira.issue.comment":
			actionValue = map[string]string{"comment": value}
		case "jira.issue.edit:summary", "jira.issue.edit:duedate":
			actionValue = map[string]string{"field": strings.TrimPrefix(actionType, "jira.issue.edit:"), "value": value}
			actionType = "jira.issue.edit"
		default:
			return nil, fmt.Errorf("unsupported action type")
		}
		components = append(components, map[string]any{
			"component": "ACTION", "schemaVersion": 1, "type": actionType, "value": actionValue,
		})
	}
	return components, nil
}

func nonEmpty(values ...string) []string {
	out := []string{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// automationComponentValue decodes a component value that may be a JSON
// object or a string holding one.
func automationComponentValue(raw json.RawMessage, value any) {
	var encoded string
	if json.Unmarshal(raw, &encoded) == nil {
		raw = json.RawMessage(encoded)
	}
	_ = json.Unmarshal(raw, value)
}

// automationEditorUnsupported names what the editor cannot show in a rule, so
// saving there would lose it; it is empty when the editor shows the whole rule.
func automationEditorUnsupported(payload json.RawMessage) string {
	var rule struct {
		Trigger struct {
			Type string `json:"type"`
		} `json:"trigger"`
		Components []automationComponentJSON `json:"components"`
	}
	_ = json.Unmarshal(payload, &rule)
	if automationOptionName(automationTriggers, rule.Trigger.Type) == "" {
		return "its trigger"
	}
	editableAction := func(component automationComponentJSON) bool {
		return (component.Component == "" || component.Component == "ACTION") && (component.Type == "jira.issue.edit" || automationOptionName(automationActionTypes, component.Type) != "")
	}
	for index, component := range rule.Components {
		switch component.Component {
		case "CONDITION":
			if component.Type != "jira.issue.condition" && component.Type != "jira.jql.condition" {
				return "some of its conditions"
			}
		case "BRANCH":
			// The editor shows one related work items branch of actions, last.
			if component.Type != "jira.issue.related" || index != len(rule.Components)-1 {
				return "its branches"
			}
			for _, child := range component.Children {
				if !editableAction(child) {
					return "its branches"
				}
			}
		default:
			if !editableAction(component) {
				return "some of its actions"
			}
		}
	}
	return ""
}

func parseAutomationTrigger(payload json.RawMessage) automationTriggerView {
	var rule struct {
		CanOtherRuleTrigger bool `json:"canOtherRuleTrigger"`
		Trigger             struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"trigger"`
	}
	_ = json.Unmarshal(payload, &rule)
	var value struct {
		FromStatusIDs []string `json:"fromStatusIds"`
		ToStatusIDs   []string `json:"toStatusIds"`
		Fields        []string `json:"fields"`
	}
	automationComponentValue(rule.Trigger.Value, &value)
	view := automationTriggerView{Type: rule.Trigger.Type, Fields: strings.Join(value.Fields, ", "), AllowRules: rule.CanOtherRuleTrigger}
	if len(value.FromStatusIDs) > 0 {
		view.FromStatus = value.FromStatusIDs[0]
	}
	if len(value.ToStatusIDs) > 0 {
		view.ToStatus = value.ToStatusIDs[0]
	}
	return view
}

func parseAutomationConditions(payload json.RawMessage) []automationConditionView {
	var rule struct {
		Components []struct {
			Component string          `json:"component"`
			Type      string          `json:"type"`
			Value     json.RawMessage `json:"value"`
		} `json:"components"`
	}
	_ = json.Unmarshal(payload, &rule)
	conditions := []automationConditionView{}
	for _, component := range rule.Components {
		if component.Component != "CONDITION" {
			continue
		}
		if component.Type == "jira.jql.condition" {
			var value struct {
				JQL string `json:"jql"`
			}
			automationComponentValue(component.Value, &value)
			conditions = append(conditions, automationConditionView{Field: "jql", Value: value.JQL})
			continue
		}
		if component.Type != "jira.issue.condition" {
			continue
		}
		var value struct{ Field, Operator, Value string }
		automationComponentValue(component.Value, &value)
		conditions = append(conditions, automationConditionView{Field: value.Field, Operator: value.Operator, Value: value.Value})
	}
	return conditions
}

func parseAutomationActions(payload json.RawMessage) []automationActionView {
	var value struct {
		Components []automationComponentJSON `json:"components"`
	}
	_ = json.Unmarshal(payload, &value)
	return automationActionViews(value.Components)
}

// automationComponentJSON is a stored rule component as the editor reads it.
type automationComponentJSON struct {
	Component string                    `json:"component"`
	Type      string                    `json:"type"`
	Value     json.RawMessage           `json:"value"`
	Children  []automationComponentJSON `json:"children"`
}

func automationActionViews(components []automationComponentJSON) []automationActionView {
	actions := make([]automationActionView, 0, len(components))
	for _, component := range components {
		if component.Component == "CONDITION" || component.Component == "BRANCH" {
			continue
		}
		var fields map[string]string
		automationComponentValue(component.Value, &fields)
		view := automationActionView{Type: component.Type, Value: fields["label"]}
		switch component.Type {
		case "jira.issue.assign":
			view.Value = fields["accountId"]
		case "jira.issue.transition":
			view.Value = fields["statusId"]
		case "jira.issue.comment":
			view.Value = fields["comment"]
		case "jira.issue.edit":
			view.Type, view.Value = "jira.issue.edit:"+fields["field"], fields["value"]
		}
		actions = append(actions, view)
	}
	return actions
}

// parseAutomationBranch reads the rule's related work items branch for the
// editor.
func parseAutomationBranch(payload json.RawMessage) automationBranchView {
	var rule struct {
		Components []automationComponentJSON `json:"components"`
	}
	_ = json.Unmarshal(payload, &rule)
	for _, component := range rule.Components {
		if component.Component != "BRANCH" {
			continue
		}
		var value struct {
			RelatedType string   `json:"relatedType"`
			LinkTypes   []string `json:"linkTypes"`
		}
		automationComponentValue(component.Value, &value)
		return automationBranchView{RelatedType: value.RelatedType, LinkTypes: strings.Join(value.LinkTypes, ", "), Actions: automationActionViews(component.Children)}
	}
	return automationBranchView{Actions: []automationActionView{}}
}

func splitLines(value string) []string {
	result := []string{}
	for _, line := range strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == ',' }) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func intervalLabel(minutes int) string {
	switch {
	case minutes%10080 == 0:
		return fmt.Sprintf("Every %d week(s)", minutes/10080)
	case minutes%1440 == 0:
		return fmt.Sprintf("Every %d day(s)", minutes/1440)
	case minutes%60 == 0:
		return fmt.Sprintf("Every %d hour(s)", minutes/60)
	default:
		return fmt.Sprintf("Every %d minute(s)", minutes)
	}
}
