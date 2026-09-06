// Package workflow is the ZZIRA workflow engine: statuses, transitions, and
// validation. Instance-based — a project may run its own workflow (stored as
// JSON in the workflows table) while Default covers everything unassigned.
// Pure — shared by server and wasm client.
package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	RuleRestrictIssueTransition = "system:restrict-issue-transition"
	RuleRestrictFromAllUsers    = "system:restrict-from-all-users"
	RuleCheckFieldValue         = "system:check-field-value"
	RuleValidateFieldValue      = "system:validate-field-value"
	RuleChangeAssignee          = "system:change-assignee"
	RuleUpdateField             = "system:update-field"
	RuleCopyFieldValue          = "system:copy-value-from-other-field"
	RuleTransitionScreen        = "system:transition-screen"
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
	ActorID      string
	AssigneeID   string
	ReporterID   string
	FieldPresent map[string]bool
	FieldValues  map[string]json.RawMessage
	IsAPI        bool
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
		if validator.RuleKey != RuleValidateFieldValue || validator.Parameters["ruleType"] != "fieldRequired" {
			return fmt.Errorf("unsupported workflow validator %q", validator.RuleKey)
		}
		for _, field := range commaValues(validator.Parameters["fieldsRequired"]) {
			if !context.FieldPresent[field] {
				message := strings.TrimSpace(validator.Parameters["errorMessage"])
				if message == "" {
					message = fmt.Sprintf("%s is required", field)
				}
				return fmt.Errorf("%s", message)
			}
		}
	}
	return nil
}

// AssigneeEffect returns the final assignee change produced by post-functions.
func (t Transition) AssigneeEffect(context EvaluationContext) (string, bool, error) {
	var assigneeID string
	changed := false
	for _, action := range t.Actions {
		if action.RuleKey == RuleUpdateField || action.RuleKey == RuleCopyFieldValue {
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
	Value       string
	Mode        string
}

// FieldUpdateEffects returns configured update-field post-functions in their
// workflow order. Validation guarantees every returned effect is executable.
func (t Transition) FieldUpdateEffects() ([]FieldUpdateEffect, error) {
	effects := make([]FieldUpdateEffect, 0)
	for _, action := range t.Actions {
		switch action.RuleKey {
		case RuleChangeAssignee:
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
			if issueSource := action.Parameters["issueSource"]; issueSource != "" && issueSource != "SAME" {
				return nil, fmt.Errorf("unsupported copy-field issue source %q", issueSource)
			}
			effects = append(effects, FieldUpdateEffect{Field: target, SourceField: source, Mode: "replace"})
		default:
			return nil, fmt.Errorf("unsupported workflow post-function %q", action.RuleKey)
		}
	}
	return effects, nil
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
		if validator.RuleKey != RuleValidateFieldValue || validator.Parameters["ruleType"] != "fieldRequired" || len(commaValues(validator.Parameters["fieldsRequired"])) == 0 {
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
			if issueSource := action.Parameters["issueSource"]; issueSource != "" && issueSource != "SAME" {
				return fmt.Errorf("copy-field issue source %q is unsupported", issueSource)
			}
		default:
			return fmt.Errorf("workflow post-function %q is unsupported", action.RuleKey)
		}
	}
	if transition.Conditions != nil {
		if err := validateConditionConfiguration(*transition.Conditions, seen); err != nil {
			return err
		}
	}
	return nil
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
