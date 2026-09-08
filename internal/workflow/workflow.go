// Package workflow is the ZZIRA workflow engine: statuses, transitions, and
// validation. Instance-based — a project may run its own workflow (stored as
// JSON in the workflows table) while Default covers everything unassigned.
// Pure — shared by server and wasm client.
package workflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	RuleRestrictIssueTransition  = "system:restrict-issue-transition"
	RuleRestrictFromAllUsers     = "system:restrict-from-all-users"
	RuleCheckFieldValue          = "system:check-field-value"
	RulePreviousStatusCondition  = "system:previous-status-condition"
	RulePreviousStatusValidator  = "system:previous-status-validator"
	RuleSeparationOfDuties       = "system:separation-of-duties"
	RuleParentChildCondition     = "system:parent-or-child-blocking-condition"
	RuleParentChildValidator     = "system:parent-or-child-blocking-validator"
	RuleFormsAttachedValidator   = "system:proforma-forms-attached"
	RuleFormsSubmittedValidator  = "system:proforma-forms-submitted"
	RuleCheckPermissionValidator = "system:check-permission-validator"
	RuleValidateFieldValue       = "system:validate-field-value"
	RuleChangeAssignee           = "system:change-assignee"
	RuleUpdateField              = "system:update-field"
	RuleCopyFieldValue           = "system:copy-value-from-other-field"
	RuleTriggerWebhook           = "system:trigger-webhook"
	RuleDevelopmentTrigger       = "system:development-triggers"
	DevelopmentBranchCreated     = "com.atlassian.jira.plugins.jira-development-integration-plugin:branch-created-trigger"
	RuleTransitionScreen         = "system:transition-screen"
)

// Rule is the Jira Cloud workflow rule wire shape. Parameters remain strings
// because Jira's workflow APIs encode even booleans and lists as strings.
type Rule struct {
	ID         string            `json:"id,omitempty"`
	RuleKey    string            `json:"ruleKey"`
	Parameters map[string]string `json:"parameters"`
}

// ConditionGroup combines conditions recursively using ALL or ANY semantics.
type ConditionGroup struct {
	Operation       string           `json:"operation"`
	Conditions      []Rule           `json:"conditions"`
	ConditionGroups []ConditionGroup `json:"conditionGroups"`
}

// EvaluationContext contains the issue and actor facts workflow rules may use.
type EvaluationContext struct {
	ActorID        string
	AssigneeID     string
	ReporterID     string
	FieldPresent   map[string]bool
	ChangedFields  map[string]bool
	FieldValues    map[string]json.RawMessage
	StatusHistory  []string
	CurrentStatus  string
	Transitions    []TransitionHistory
	Permissions    map[string]bool
	ParentStatus   string
	ChildStatuses  []string
	FormsAttached  int
	FormsSubmitted bool
	IsAPI          bool
}

type TransitionHistory struct {
	FromStatusID string
	ToStatusID   string
	ActorID      string
}

// Layout is a workflow designer coordinate in CSS pixels. Jira Cloud exposes
// the same x/y pair in modern workflow create, update, search, and preview.
type Layout struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// StatusLayout keeps a workflow's status membership and designer placement.
type StatusLayout struct {
	StatusReference string            `json:"statusReference"`
	Layout          *Layout           `json:"layout,omitempty"`
	Properties      map[string]string `json:"properties"`
}

// Transition is one workflow edge.
type Transition struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	From       []string        `json:"from"`
	To         string          `json:"to"`
	Actions    []Rule          `json:"actions,omitempty"`
	Validators []Rule          `json:"validators,omitempty"`
	Triggers   []Rule          `json:"triggers,omitempty"`
	Conditions *ConditionGroup `json:"conditions,omitempty"`
	Screen     *Rule           `json:"transitionScreen,omitempty"`
}

// Workflow is a named set of transitions over its visible status registry.
type Workflow struct {
	ID                              string         `json:"id"`
	Name                            string         `json:"name"`
	ProjectID                       string         `json:"-"`
	Description                     string         `json:"description,omitempty"`
	StartPointLayout                *Layout        `json:"startPointLayout,omitempty"`
	LoopedTransitionContainerLayout *Layout        `json:"loopedTransitionContainerLayout,omitempty"`
	Statuses                        []StatusLayout `json:"statuses,omitempty"`
	Transitions                     []Transition   `json:"transitions"`
	HasDraft                        bool           `json:"-"`
	Version                         int            `json:"-"`
}

type Scheme struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Description       string            `json:"description"`
	DefaultWorkflowID string            `json:"defaultWorkflowId"`
	IssueTypeMappings map[string]string `json:"issueTypeMappings"`
	HasDraft          bool              `json:"-"`
	Version           int               `json:"-"`
}

// Default is the built-in workflow: To Do ↔ In Progress → Done, Done → To Do.
// Transition ids match the Jira-style string ids clients expect.
func Default() Workflow {
	return Workflow{
		ID:   "wf_default",
		Name: "Default",
		Transitions: []Transition{
			{ID: "11", Name: "To Do", From: []string{"st_inprogress", "st_done"}, To: "st_todo"},
			{ID: "21", Name: "In Progress", From: []string{"st_todo", "st_done"}, To: "st_inprogress"},
			{ID: "31", Name: "Done", From: []string{"st_todo", "st_inprogress"}, To: "st_done"},
		},
	}
}

// Available returns the transitions legal from the given status.
func (w Workflow) Available(statusID string) []Transition {
	var out []Transition
	for _, t := range w.Transitions {
		for _, from := range t.From {
			if from == statusID {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// AvailableFor returns legal transitions whose conditions allow the actor.
func (w Workflow) AvailableFor(statusID string, context EvaluationContext) []Transition {
	available := w.Available(statusID)
	out := make([]Transition, 0, len(available))
	for _, transition := range available {
		if transition.ConditionsAllow(context) {
			out = append(out, transition)
		}
	}
	return out
}

// Find returns the transition with the given id, or nil.
func (w Workflow) Find(transitionID string) *Transition {
	for i := range w.Transitions {
		if w.Transitions[i].ID == transitionID {
			return &w.Transitions[i]
		}
	}
	return nil
}

// Validate reports whether transition id is legal from the given status.
func (w Workflow) Validate(transitionID, currentStatusID string) (*Transition, bool) {
	for i := range w.Transitions {
		t := &w.Transitions[i]
		if t.ID != transitionID {
			continue
		}
		for _, from := range t.From {
			if from == currentStatusID {
				return t, true
			}
		}
		return t, false
	}
	return nil, false
}

// ConditionsAllow evaluates a transition's nested Jira condition groups.
func (t Transition) ConditionsAllow(context EvaluationContext) bool {
	return t.Conditions == nil || evaluateConditionGroup(*t.Conditions, context)
}

func evaluateConditionGroup(group ConditionGroup, context EvaluationContext) bool {
	results := make([]bool, 0, len(group.Conditions)+len(group.ConditionGroups))
	for _, condition := range group.Conditions {
		results = append(results, evaluateCondition(condition, context))
	}
	for _, child := range group.ConditionGroups {
		results = append(results, evaluateConditionGroup(child, context))
	}
	if len(results) == 0 {
		return true
	}
	if strings.EqualFold(group.Operation, "ANY") {
		for _, result := range results {
			if result {
				return true
			}
		}
		return false
	}
	for _, result := range results {
		if !result {
			return false
		}
	}
	return true
}

func evaluateCondition(rule Rule, context EvaluationContext) bool {
	switch rule.RuleKey {
	case RuleCheckFieldValue:
		return checkFieldValue(rule.Parameters, context)
	case RulePreviousStatusCondition:
		matched := previousStatusMatches(rule.Parameters, context)
		if rule.Parameters["not"] == "true" {
			return !matched
		}
		return matched
	case RuleSeparationOfDuties:
		for _, transition := range context.Transitions {
			if transition.ActorID == context.ActorID && transition.FromStatusID == rule.Parameters["fromStatusId"] && transition.ToStatusID == rule.Parameters["toStatusId"] {
				return false
			}
		}
		return true
	case RuleParentChildCondition:
		blocked := stringSet(rule.Parameters["statusIds"])
		for _, statusID := range context.ChildStatuses {
			if blocked[statusID] {
				return false
			}
		}
		return true
	case RuleRestrictFromAllUsers:
		return rule.Parameters["restrictMode"] == "users" && context.IsAPI
	case RuleRestrictIssueTransition:
	default:
		return false
	}
	for _, value := range commaValues(rule.Parameters["accountIds"]) {
		switch value {
		case "allow-assignee":
			if context.ActorID != "" && context.ActorID == context.AssigneeID {
				return true
			}
		case "allow-reporter":
			if context.ActorID != "" && context.ActorID == context.ReporterID {
				return true
			}
		default:
			if context.ActorID != "" && context.ActorID == value {
				return true
			}
		}
	}
	return false
}

// ValidateRules evaluates validators after a transition is selected.
func (t Transition) ValidateRules(context EvaluationContext) error {
	for _, validator := range t.Validators {
		switch validator.RuleKey {
		case RuleValidateFieldValue:
			switch validator.Parameters["ruleType"] {
			case "fieldRequired":
				for _, field := range commaValues(validator.Parameters["fieldsRequired"]) {
					if context.FieldPresent[field] {
						continue
					}
					message := strings.TrimSpace(validator.Parameters["errorMessage"])
					if message == "" {
						message = fmt.Sprintf("%s is required", field)
					}
					return fmt.Errorf("%s", message)
				}
			case "fieldChanged":
				field := validator.Parameters["fieldKey"]
				if !context.ChangedFields[field] {
					message := strings.TrimSpace(validator.Parameters["errorMessage"])
					if message == "" {
						message = fmt.Sprintf("%s must be changed during the transition", field)
					}
					return fmt.Errorf("%s", message)
				}
			case "fieldMatchesRegularExpression":
				field := validator.Parameters["fieldKey"]
				pattern, err := regexp.Compile(validator.Parameters["regexp"])
				if err != nil || !workflowFieldMatches(pattern, context.FieldValues[field]) {
					message := strings.TrimSpace(validator.Parameters["errorMessage"])
					if message == "" {
						message = fmt.Sprintf("%s does not match the required pattern", field)
					}
					return fmt.Errorf("%s", message)
				}
			case "fieldHasSingleValue":
				field := validator.Parameters["fieldKey"]
				if !workflowFieldHasSingleValue(context.FieldValues[field]) {
					return fmt.Errorf("%s must contain exactly one value", field)
				}
			case "dateFieldComparison":
				comparisonType := "DATE_WITHOUT_TIME"
				if validator.Parameters["includeTime"] == "true" {
					comparisonType = "DATE"
				}
				if !workflowFieldsCompare(context.FieldValues[validator.Parameters["date1FieldKey"]], context.FieldValues[validator.Parameters["date2FieldKey"]], validator.Parameters["conditionSelected"], comparisonType) {
					return fmt.Errorf("workflow date fields do not satisfy %s", validator.Parameters["conditionSelected"])
				}
			case "windowDateComparison":
				days, _ := strconv.Atoi(validator.Parameters["numberOfDays"])
				if !workflowFieldsWithinDays(context.FieldValues[validator.Parameters["date1FieldKey"]], context.FieldValues[validator.Parameters["date2FieldKey"]], days) {
					return fmt.Errorf("workflow date field exceeds the %d-day window", days)
				}
			default:
				return fmt.Errorf("unsupported workflow validator %q", validator.RuleKey)
			}
		case RulePreviousStatusValidator:
			if !previousStatusMatches(validator.Parameters, context) {
				return fmt.Errorf("issue has not passed through the required status")
			}
		case RuleCheckPermissionValidator:
			permissionKey := validator.Parameters["permissionKey"]
			if !context.Permissions[permissionKey] {
				return fmt.Errorf("permission %s is required to perform this transition", permissionKey)
			}
		case RuleParentChildValidator:
			if stringSet(validator.Parameters["statusIds"])[context.ParentStatus] {
				return fmt.Errorf("parent status blocks this transition")
			}
		case RuleFormsAttachedValidator:
			if context.FormsAttached == 0 {
				return fmt.Errorf("at least one form must be attached before this transition")
			}
		case RuleFormsSubmittedValidator:
			if context.FormsAttached == 0 || !context.FormsSubmitted {
				return fmt.Errorf("all attached forms must be submitted before this transition")
			}
		default:
			return fmt.Errorf("unsupported workflow validator %q", validator.RuleKey)
		}
	}
	return nil
}

func workflowFieldMatches(pattern *regexp.Regexp, raw json.RawMessage) bool {
	for _, value := range comparableJSONValues(raw) {
		if pattern.MatchString(workflowScalar(value, "STRING")) {
			return true
		}
	}
	return false
}

func workflowFieldHasSingleValue(raw json.RawMessage) bool {
	if !jsonValuePresent(raw) {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	if values, ok := value.([]any); ok {
		return len(values) == 1
	}
	return true
}

func workflowFieldsCompare(leftRaw, rightRaw json.RawMessage, comparator, comparisonType string) bool {
	for _, left := range comparableJSONValues(leftRaw) {
		for _, right := range comparableJSONValues(rightRaw) {
			if compareWorkflowValues(left, right, comparator, comparisonType) {
				return true
			}
		}
	}
	return false
}

func workflowFieldsWithinDays(dateRaw, referenceRaw json.RawMessage, days int) bool {
	for _, dateValue := range comparableJSONValues(dateRaw) {
		date, dateErr := parseWorkflowDate(workflowScalar(dateValue, "DATE_WITHOUT_TIME"), false)
		if dateErr != nil {
			continue
		}
		for _, referenceValue := range comparableJSONValues(referenceRaw) {
			reference, referenceErr := parseWorkflowDate(workflowScalar(referenceValue, "DATE_WITHOUT_TIME"), false)
			if referenceErr == nil && !date.After(reference.AddDate(0, 0, days)) {
				return true
			}
		}
	}
	return false
}

func previousStatusMatches(parameters map[string]string, context EvaluationContext) bool {
	wanted := make(map[string]bool)
	for _, statusID := range commaValues(parameters["previousStatusIds"]) {
		wanted[statusID] = true
	}
	history := append([]string(nil), context.StatusHistory...)
	if parameters["includeCurrentStatus"] == "true" && context.CurrentStatus != "" {
		history = append(history, context.CurrentStatus)
	}
	if len(history) == 0 {
		return false
	}
	if parameters["mostRecentStatusOnly"] == "true" {
		return wanted[history[len(history)-1]]
	}
	for _, statusID := range history {
		if wanted[statusID] {
			return true
		}
	}
	return false
}

// AssigneeEffect returns the final assignee change produced by post-functions.
func (t Transition) AssigneeEffect(context EvaluationContext) (string, bool, error) {
	var assigneeID string
	changed := false
	for _, action := range t.Actions {
		if action.RuleKey == RuleUpdateField || action.RuleKey == RuleCopyFieldValue || action.RuleKey == RuleTriggerWebhook {
			continue
		}
		if action.RuleKey != RuleChangeAssignee {
			return "", false, fmt.Errorf("unsupported workflow post-function %q", action.RuleKey)
		}
		switch action.Parameters["type"] {
		case "to-current-user":
			assigneeID, changed = context.ActorID, true
		case "to-unassigned":
			assigneeID, changed = "", true
		case "to-selected-user":
			assigneeID, changed = action.Parameters["accountId"], true
		default:
			return "", false, fmt.Errorf("unsupported change-assignee type %q", action.Parameters["type"])
		}
	}
	return assigneeID, changed, nil
}

type FieldUpdateEffect struct {
	Field       string
	SourceField string
	IssueSource string
	Value       string
	Mode        string
}

// FieldUpdateEffects returns configured update-field post-functions in their
// workflow order. Validation guarantees every returned effect is executable.
func (t Transition) FieldUpdateEffects() ([]FieldUpdateEffect, error) {
	effects := make([]FieldUpdateEffect, 0)
	for _, action := range t.Actions {
		switch action.RuleKey {
		case RuleChangeAssignee, RuleTriggerWebhook:
			continue
		case RuleUpdateField:
			field := action.Parameters["field"]
			if !executableUpdateField(field) {
				return nil, fmt.Errorf("unsupported update-field target %q", action.Parameters["field"])
			}
			mode := action.Parameters["mode"]
			if mode != "append" && mode != "replace" {
				return nil, fmt.Errorf("unsupported update-field mode %q", mode)
			}
			if mode == "append" && field == "priority" {
				return nil, fmt.Errorf("update-field target %q cannot be appended", field)
			}
			effects = append(effects, FieldUpdateEffect{Field: field, Value: action.Parameters["value"], Mode: mode})
		case RuleCopyFieldValue:
			source, target := action.Parameters["sourceFieldKey"], action.Parameters["targetFieldKey"]
			if !readableWorkflowField(source) || !executableUpdateField(target) {
				return nil, fmt.Errorf("unsupported copy-field source %q or target %q", source, target)
			}
			issueSource := action.Parameters["issueSource"]
			if issueSource != "" && issueSource != "SAME" && issueSource != "PARENT" {
				return nil, fmt.Errorf("unsupported copy-field issue source %q", issueSource)
			}
			if issueSource == "" {
				issueSource = "SAME"
			}
			effects = append(effects, FieldUpdateEffect{Field: target, SourceField: source, IssueSource: issueSource, Mode: "replace"})
		default:
			return nil, fmt.Errorf("unsupported workflow post-function %q", action.RuleKey)
		}
	}
	return effects, nil
}

// TriggerWebhookIDs returns the webhook registrations explicitly invoked by
// post-functions, preserving workflow order while removing duplicates.
func (t Transition) TriggerWebhookIDs() ([]string, error) {
	ids := make([]string, 0)
	seen := make(map[string]bool)
	for _, action := range t.Actions {
		switch action.RuleKey {
		case RuleChangeAssignee, RuleUpdateField, RuleCopyFieldValue:
			continue
		case RuleTriggerWebhook:
			id := strings.TrimSpace(action.Parameters["webhookId"])
			if id == "" {
				return nil, fmt.Errorf("trigger-webhook registration is required")
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		default:
			return nil, fmt.Errorf("unsupported workflow post-function %q", action.RuleKey)
		}
	}
	return ids, nil
}

func readableWorkflowField(field string) bool {
	switch field {
	case "summary", "description", "labels", "assignee", "reporter", "priority", "status":
		return true
	}
	return executableUpdateField(field)
}

func executableUpdateField(field string) bool {
	switch field {
	case "summary", "description", "labels", "priority":
		return true
	}
	if !strings.HasPrefix(field, "customfield_") || len(field) == len("customfield_") {
		return false
	}
	for _, char := range strings.TrimPrefix(field, "customfield_") {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func commaValues(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// ValidateTransitionRules rejects configurations the runtime cannot execute.
// This keeps the capabilities response honest and prevents rules being stored
// as inert metadata.
func ValidateTransitionRules(transition Transition) error {
	seen := make(map[string]bool)
	validateRuleID := func(rule Rule) error {
		if strings.TrimSpace(rule.ID) == "" {
			return fmt.Errorf("workflow rule id is required")
		}
		if seen[rule.ID] {
			return fmt.Errorf("workflow rule id %q is duplicated", rule.ID)
		}
		seen[rule.ID] = true
		return nil
	}
	if transition.Screen != nil {
		if err := validateRuleID(*transition.Screen); err != nil {
			return err
		}
		if transition.Screen.RuleKey != RuleTransitionScreen || len(commaValues(transition.Screen.Parameters["fields"])) == 0 {
			return fmt.Errorf("workflow transition screen is unsupported or incomplete")
		}
	}
	for _, validator := range transition.Validators {
		if err := validateRuleID(validator); err != nil {
			return err
		}
		switch validator.RuleKey {
		case RuleValidateFieldValue:
			switch validator.Parameters["ruleType"] {
			case "fieldRequired":
				if len(commaValues(validator.Parameters["fieldsRequired"])) == 0 {
					return fmt.Errorf("workflow validator %q is unsupported or incomplete", validator.RuleKey)
				}
			case "fieldChanged":
				if strings.TrimSpace(validator.Parameters["fieldKey"]) == "" || strings.TrimSpace(validator.Parameters["groupsExemptFromValidation"]) != "" {
					return fmt.Errorf("workflow validator %q is unsupported or incomplete", validator.RuleKey)
				}
			case "fieldMatchesRegularExpression":
				if strings.TrimSpace(validator.Parameters["fieldKey"]) == "" || strings.TrimSpace(validator.Parameters["regexp"]) == "" {
					return fmt.Errorf("workflow validator %q is unsupported or incomplete", validator.RuleKey)
				}
				if _, err := regexp.Compile(validator.Parameters["regexp"]); err != nil {
					return fmt.Errorf("workflow validator regular expression is invalid")
				}
			case "fieldHasSingleValue":
				if strings.TrimSpace(validator.Parameters["fieldKey"]) == "" {
					return fmt.Errorf("workflow validator %q is unsupported or incomplete", validator.RuleKey)
				}
				if value := validator.Parameters["excludeSubtasks"]; value != "true" && value != "false" {
					return fmt.Errorf("workflow validator excludeSubtasks must be true or false")
				}
			case "dateFieldComparison":
				if strings.TrimSpace(validator.Parameters["date1FieldKey"]) == "" || strings.TrimSpace(validator.Parameters["date2FieldKey"]) == "" {
					return fmt.Errorf("workflow date validator requires two field keys")
				}
				if value := validator.Parameters["includeTime"]; value != "true" && value != "false" {
					return fmt.Errorf("workflow date validator includeTime must be true or false")
				}
				switch validator.Parameters["conditionSelected"] {
				case ">", ">=", "=", "<=", "<", "!=":
				default:
					return fmt.Errorf("workflow date validator condition is unsupported")
				}
			case "windowDateComparison":
				if strings.TrimSpace(validator.Parameters["date1FieldKey"]) == "" || strings.TrimSpace(validator.Parameters["date2FieldKey"]) == "" {
					return fmt.Errorf("workflow date-window validator requires two field keys")
				}
				days, err := strconv.Atoi(validator.Parameters["numberOfDays"])
				if err != nil || days < 0 {
					return fmt.Errorf("workflow date-window validator requires nonnegative numberOfDays")
				}
			default:
				return fmt.Errorf("workflow validator %q is unsupported or incomplete", validator.RuleKey)
			}
		case RulePreviousStatusValidator:
			if err := validatePreviousStatusRule(validator.Parameters, false); err != nil {
				return err
			}
		case RuleCheckPermissionValidator:
			if !jiraPermissionKeys[validator.Parameters["permissionKey"]] {
				return fmt.Errorf("workflow permission validator has an unknown permission key")
			}
		case RuleParentChildValidator:
			if validator.Parameters["blocker"] != "PARENT" || len(commaValues(validator.Parameters["statusIds"])) == 0 {
				return fmt.Errorf("parent blocking validator requires blocker PARENT and statusIds")
			}
		case RuleFormsAttachedValidator, RuleFormsSubmittedValidator:
			if len(validator.Parameters) != 0 {
				return fmt.Errorf("form workflow validators do not accept parameters")
			}
		default:
			return fmt.Errorf("workflow validator %q is unsupported or incomplete", validator.RuleKey)
		}
	}
	for _, action := range transition.Actions {
		if err := validateRuleID(action); err != nil {
			return err
		}
		switch action.RuleKey {
		case RuleChangeAssignee:
			switch action.Parameters["type"] {
			case "to-current-user", "to-unassigned":
			case "to-selected-user":
				if strings.TrimSpace(action.Parameters["accountId"]) == "" {
					return fmt.Errorf("change-assignee selected user is required")
				}
			default:
				return fmt.Errorf("change-assignee type %q is unsupported", action.Parameters["type"])
			}
		case RuleUpdateField:
			field := action.Parameters["field"]
			if !executableUpdateField(field) {
				return fmt.Errorf("update-field target %q is not executable", action.Parameters["field"])
			}
			if mode := action.Parameters["mode"]; mode != "append" && mode != "replace" {
				return fmt.Errorf("update-field mode %q is unsupported", mode)
			} else if mode == "append" && field == "priority" {
				return fmt.Errorf("update-field target %q cannot be appended", field)
			}
		case RuleCopyFieldValue:
			source, target := action.Parameters["sourceFieldKey"], action.Parameters["targetFieldKey"]
			if !readableWorkflowField(source) || !executableUpdateField(target) {
				return fmt.Errorf("copy-field source %q or target %q is not executable", source, target)
			}
			if issueSource := action.Parameters["issueSource"]; issueSource != "" && issueSource != "SAME" && issueSource != "PARENT" {
				return fmt.Errorf("copy-field issue source %q is unsupported", issueSource)
			}
		case RuleTriggerWebhook:
			if strings.TrimSpace(action.Parameters["webhookId"]) == "" {
				return fmt.Errorf("trigger-webhook registration is required")
			}
		default:
			return fmt.Errorf("workflow post-function %q is unsupported", action.RuleKey)
		}
	}
	for _, trigger := range transition.Triggers {
		if err := validateRuleID(trigger); err != nil {
			return err
		}
		if trigger.RuleKey != RuleDevelopmentTrigger {
			return fmt.Errorf("workflow trigger %q is unsupported", trigger.RuleKey)
		}
		if trigger.Parameters["triggerType"] != DevelopmentBranchCreated || len(trigger.Parameters) != 1 {
			return fmt.Errorf("development trigger type is unsupported or incomplete")
		}
	}
	if transition.Conditions != nil {
		if err := validateConditionConfiguration(*transition.Conditions, seen); err != nil {
			return err
		}
	}
	return nil
}

// HasDevelopmentTrigger reports whether a transition is subscribed to a
// specific Jira Software development event type.
func (t Transition) HasDevelopmentTrigger(triggerType string) bool {
	for _, trigger := range t.Triggers {
		if trigger.RuleKey == RuleDevelopmentTrigger && trigger.Parameters["triggerType"] == triggerType {
			return true
		}
	}
	return false
}

var jiraPermissionKeys = map[string]bool{
	"ADMINISTER_PROJECTS": true, "EDIT_WORKFLOW": true, "EDIT_ISSUE_LAYOUT": true,
	"BROWSE_PROJECTS": true, "MANAGE_SPRINTS_PERMISSION": true, "SERVICEDESK_AGENT": true,
	"VIEW_DEV_TOOLS": true, "VIEW_READONLY_WORKFLOW": true,
	"ASSIGNABLE_USER": true, "ASSIGN_ISSUES": true, "CLOSE_ISSUES": true,
	"CREATE_ISSUES": true, "DELETE_ISSUES": true, "EDIT_ISSUES": true,
	"LINK_ISSUES": true, "MODIFY_REPORTER": true, "MOVE_ISSUES": true,
	"RESOLVE_ISSUES": true, "SCHEDULE_ISSUES": true, "SET_ISSUE_SECURITY": true,
	"TRANSITION_ISSUES": true, "MANAGE_WATCHERS": true, "VIEW_VOTERS_AND_WATCHERS": true,
	"ADD_COMMENTS": true, "DELETE_ALL_COMMENTS": true, "DELETE_OWN_COMMENTS": true,
	"EDIT_ALL_COMMENTS": true, "EDIT_OWN_COMMENTS": true,
	"CREATE_ATTACHMENTS": true, "DELETE_ALL_ATTACHMENTS": true, "DELETE_OWN_ATTACHMENTS": true,
	"DELETE_ALL_WORKLOGS": true, "DELETE_OWN_WORKLOGS": true, "EDIT_ALL_WORKLOGS": true,
	"EDIT_OWN_WORKLOGS": true, "WORK_ON_ISSUES": true,
}

func (t Transition) ScreenFields() []string {
	if t.Screen == nil {
		return nil
	}
	return commaValues(t.Screen.Parameters["fields"])
}

func (t Transition) RequiredFields() map[string]bool {
	required := make(map[string]bool)
	for _, validator := range t.Validators {
		if validator.RuleKey == RuleValidateFieldValue && validator.Parameters["ruleType"] == "fieldRequired" {
			for _, field := range commaValues(validator.Parameters["fieldsRequired"]) {
				required[field] = true
			}
		}
	}
	return required
}

func validateConditionConfiguration(group ConditionGroup, seen map[string]bool) error {
	if group.Operation != "ALL" && group.Operation != "ANY" {
		return fmt.Errorf("workflow condition operation must be ALL or ANY")
	}
	if len(group.Conditions)+len(group.ConditionGroups) == 0 {
		return fmt.Errorf("workflow condition group cannot be empty")
	}
	for _, condition := range group.Conditions {
		if strings.TrimSpace(condition.ID) == "" {
			return fmt.Errorf("workflow rule id is required")
		}
		if seen[condition.ID] {
			return fmt.Errorf("workflow rule id %q is duplicated", condition.ID)
		}
		seen[condition.ID] = true
		switch condition.RuleKey {
		case RuleRestrictIssueTransition:
			if len(commaValues(condition.Parameters["accountIds"])) == 0 {
				return fmt.Errorf("workflow condition %q is unsupported or incomplete", condition.RuleKey)
			}
			for _, parameter := range []string{"roleIds", "groupIds", "permissionKeys", "groupCustomFields", "allowUserCustomFields", "denyUserCustomFields"} {
				if strings.TrimSpace(condition.Parameters[parameter]) != "" {
					return fmt.Errorf("workflow condition parameter %q is not executable", parameter)
				}
			}
		case RuleRestrictFromAllUsers:
			if mode := condition.Parameters["restrictMode"]; mode != "users" && mode != "usersAndAPI" {
				return fmt.Errorf("restrict-from-all-users mode %q is unsupported", mode)
			}
		case RuleCheckFieldValue:
			if strings.TrimSpace(condition.Parameters["fieldId"]) == "" {
				return fmt.Errorf("check-field-value fieldId is required")
			}
			if _, ok := comparisonValues(condition.Parameters["fieldValue"]); !ok {
				return fmt.Errorf("check-field-value fieldValue must be a non-empty JSON array")
			}
			switch condition.Parameters["comparator"] {
			case ">", ">=", "=", "<=", "<", "!=":
			default:
				return fmt.Errorf("check-field-value comparator %q is unsupported", condition.Parameters["comparator"])
			}
			switch condition.Parameters["comparisonType"] {
			case "STRING", "NUMBER", "DATE", "DATE_WITHOUT_TIME", "OPTIONID":
			default:
				return fmt.Errorf("check-field-value comparison type %q is unsupported", condition.Parameters["comparisonType"])
			}
		case RulePreviousStatusCondition:
			if err := validatePreviousStatusRule(condition.Parameters, true); err != nil {
				return err
			}
		case RuleSeparationOfDuties:
			if strings.TrimSpace(condition.Parameters["fromStatusId"]) == "" || strings.TrimSpace(condition.Parameters["toStatusId"]) == "" {
				return fmt.Errorf("separation-of-duties requires from and to status ids")
			}
		case RuleParentChildCondition:
			if condition.Parameters["blocker"] != "CHILD" || len(commaValues(condition.Parameters["statusIds"])) == 0 {
				return fmt.Errorf("child blocking condition requires blocker CHILD and statusIds")
			}
		default:
			return fmt.Errorf("workflow condition %q is unsupported or incomplete", condition.RuleKey)
		}
	}
	for _, child := range group.ConditionGroups {
		if err := validateConditionConfiguration(child, seen); err != nil {
			return err
		}
	}
	return nil
}

func stringSet(values string) map[string]bool {
	set := make(map[string]bool)
	for _, value := range commaValues(values) {
		set[value] = true
	}
	return set
}

func validatePreviousStatusRule(parameters map[string]string, condition bool) error {
	if len(commaValues(parameters["previousStatusIds"])) != 1 {
		return fmt.Errorf("previous-status rule requires exactly one status id")
	}
	booleanParameters := []string{"mostRecentStatusOnly"}
	if condition {
		booleanParameters = append(booleanParameters, "not", "includeCurrentStatus", "ignoreLoopTransitions")
	}
	for _, parameter := range booleanParameters {
		if value := parameters[parameter]; value != "" && value != "true" && value != "false" {
			return fmt.Errorf("previous-status parameter %q must be true or false", parameter)
		}
	}
	return nil
}
