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

// automationConditionView is a work item fields condition in the editor.
type automationConditionView struct{ Field, Operator, Value string }

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
	automationConditionFields = []automationOption{
		{"status", "Status"}, {"priority", "Priority"}, {"issuetype", "Work type"}, {"assignee", "Assignee"},
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
	Runs                                                       []automationRunView
	Members                                                    []*models.User
	Statuses                                                   []models.Status
	CloudID                                                    string
	IsNew                                                      bool
	FormAction                                                 string
	Error                                                      string
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
		body, err = automationPayload(r)
		if err == nil {
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
		Triggers: automationTriggers, ActionTypes: automationActionTypes, ConditionFields: automationConditionFields, ConditionOperators: automationConditionOperators}
	if rule == nil {
		data.Trigger = automationTriggerView{Type: "jira.jql.scheduled"}
		data.Conditions = []automationConditionView{{}}
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
		interval, err := strconv.Atoi(r.PostFormValue("interval_minutes"))
		if err != nil || interval < 1 || interval > 43200 {
			return nil, fmt.Errorf("schedule must be between 1 minute and 30 days")
		}
		timezone := strings.TrimSpace(r.PostFormValue("timezone"))
		if timezone == "" {
			timezone = "UTC"
		}
		if _, err := time.LoadLocation(timezone); err != nil {
			return nil, fmt.Errorf("timezone is not a valid IANA timezone")
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
	actionValues := r.PostForm["action_value"]
	actions := 0
	for index, actionType := range r.PostForm["action_type"] {
		actionType = strings.TrimSpace(actionType)
		if actionType == "" {
			continue
		}
		value := ""
		if index < len(actionValues) {
			value = strings.TrimSpace(actionValues[index])
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
		actions++
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
		if component.Component != "CONDITION" || component.Type != "jira.issue.condition" {
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
		Components []struct {
			Component string          `json:"component"`
			Type      string          `json:"type"`
			Value     json.RawMessage `json:"value"`
		} `json:"components"`
	}
	_ = json.Unmarshal(payload, &value)
	actions := make([]automationActionView, 0, len(value.Components))
	for _, component := range value.Components {
		if component.Component == "CONDITION" {
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
