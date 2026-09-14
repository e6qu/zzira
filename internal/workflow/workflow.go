// Package workflow is the ZZIRA workflow engine: statuses, transitions, and
// validation. Instance-based — a project may run its own workflow (stored as
// JSON in the workflows table) while Default covers everything unassigned.
// Pure — shared by server and wasm client.
package workflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
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
	// Approval conditions: block while an approval is pending, or until one is
	// approved or rejected.
	RuleBlockInProgressApproval     = "system:block-in-progress-approval"
	RuleApprovalsBlockUntilApproved = "system:jsd-approvals-block-until-approved"
	RuleApprovalsBlockUntilRejected = "system:jsd-approvals-block-until-rejected"
	// RuleRemindToUpdateFields is a screen rule prompting people to update
	// fields during the transition.
	RuleRemindToUpdateFields = "system:remind-people-to-update-fields"
	// RuleTriggerAgent is a post function requesting an agent run after the
	// transition.
	RuleTriggerAgent = "system:trigger-agent"
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
	// Approvals holds the final decision of each approval on the work item:
	// pending, approved or declined.
	Approvals []string
}

// approvalState summarizes the approvals on a work item.
func approvalState(decisions []string) (pending, approved, declined int) {
	for _, decision := range decisions {
		switch decision {
		case "pending":
			pending++
		case "approved":
			approved++
		case "declined":
			declined++
		}
	}
	return pending, approved, declined
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
	StatusReference       string                 `json:"statusReference"`
	Layout                *Layout                `json:"layout,omitempty"`
	Properties            map[string]string      `json:"properties"`
	ApprovalConfiguration *ApprovalConfiguration `json:"approvalConfiguration,omitempty"`
}

// ApprovalConfiguration is a status's Jira Service Management approval: the
// user picker field naming its approvers, how many approvals it needs, and the
// transitions that run once it is approved or declined.
type ApprovalConfiguration struct {
	Active              string   `json:"active"`
	ConditionType       string   `json:"conditionType"`
	ConditionValue      string   `json:"conditionValue"`
	Exclude             []string `json:"exclude,omitempty"`
	FieldID             string   `json:"fieldId"`
	PrePopulatedFieldID string   `json:"prePopulatedFieldId,omitempty"`
	TransitionApproved  string   `json:"transitionApproved"`
	TransitionRejected  string   `json:"transitionRejected"`
}

// StatusApproval returns the active approval configured on a status, if any.
func (w Workflow) StatusApproval(statusID string) *ApprovalConfiguration {
	for _, status := range w.Statuses {
		if status.StatusReference == statusID && status.ApprovalConfiguration != nil && status.ApprovalConfiguration.Active == "true" {
			configuration := *status.ApprovalConfiguration
			return &configuration
		}
	}
	return nil
}

// ValidateApprovalConfiguration checks a status's approval configuration
// against Jira's documented limits and the workflow's transitions.
func ValidateApprovalConfiguration(statusID string, configuration ApprovalConfiguration, transitions []Transition) error {
	if configuration.Active != "true" && configuration.Active != "false" {
		return fmt.Errorf("approval configuration active must be true or false")
	}
	limit := 20
	switch configuration.ConditionType {
	case "number", "numberPerPrincipal":
	case "percent":
		limit = 100
	default:
		return fmt.Errorf("approval condition type must be number, percent or numberPerPrincipal")
	}
	if value, err := strconv.Atoi(configuration.ConditionValue); err != nil || value < 1 || value > limit {
		return fmt.Errorf("approval condition value must be a whole number from 1 to %d", limit)
	}
	for _, role := range configuration.Exclude {
		if role != "assignee" && role != "reporter" {
			return fmt.Errorf("approval exclusions must be assignee or reporter")
		}
	}
	if !strings.HasPrefix(configuration.FieldID, "customfield_") {
		return fmt.Errorf("approval configuration fieldId must name the approvers custom field")
	}
	if configuration.PrePopulatedFieldID != "" && !strings.HasPrefix(configuration.PrePopulatedFieldID, "customfield_") {
		return fmt.Errorf("approval configuration prePopulatedFieldId must name a custom field")
	}
	for _, id := range []string{configuration.TransitionApproved, configuration.TransitionRejected} {
		if !transitionLeaves(transitions, id, statusID) {
			return fmt.Errorf("approval transition %q must be a transition out of the status", id)
		}
	}
	return nil
}

// transitionLeaves reports whether the transition runs from the status.
func transitionLeaves(transitions []Transition, id, statusID string) bool {
	for _, transition := range transitions {
		if id == "" || transition.ID != id {
			continue
		}
		if transition.Kind() == TransitionGlobal {
			return true
		}
		for _, from := range transition.From {
			if from == statusID {
				return true
			}
		}
	}
	return false
}

// Transition is one workflow edge.
// Transition types. A directed transition runs from the statuses it links;
// a global transition runs from every status in the workflow; the initial
// transition creates work items in its destination status.
const (
	TransitionDirected = "DIRECTED"
	TransitionGlobal   = "GLOBAL"
	TransitionInitial  = "INITIAL"
)

// LinkPorts are the designer ports a transition link starts and ends on.
type LinkPorts struct {
	FromPort *int `json:"fromPort,omitempty"`
	ToPort   *int `json:"toPort,omitempty"`
}

type Transition struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type,omitempty"`
	Description string            `json:"description,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
	From        []string          `json:"from"`
	// Ports holds each link's designer ports, keyed by the source status; a
	// global or initial transition keys its single link by "".
	Ports      map[string]LinkPorts `json:"ports,omitempty"`
	To         string               `json:"to"`
	Actions    []Rule               `json:"actions,omitempty"`
	Validators []Rule               `json:"validators,omitempty"`
	Triggers   []Rule               `json:"triggers,omitempty"`
	Conditions *ConditionGroup      `json:"conditions,omitempty"`
	Screen     *Rule                `json:"transitionScreen,omitempty"`
	// CustomIssueEventID is the event the transition fires instead of the
	// one Jira derives from the status change.
	CustomIssueEventID string `json:"customIssueEventId,omitempty"`
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
	// EntityID is the UUID clients identify the workflow by.
	EntityID string `json:"-"`
	// CreatedAt and UpdatedAt are when the workflow was created and last
	// published.
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

type Scheme struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Description       string            `json:"description"`
	DefaultWorkflowID string            `json:"defaultWorkflowId"`
	IssueTypeMappings map[string]string `json:"issueTypeMappings"`
	HasDraft          bool              `json:"-"`
	Version           int               `json:"-"`
	// JiraID is the id clients see.
	JiraID int64 `json:"-"`
	// IsDefault marks the site's default workflow scheme.
	IsDefault bool `json:"-"`
	// DraftView is set when the scheme carries its draft's mappings; the
	// published mappings are then kept in the Original fields.
	DraftView                 bool              `json:"-"`
	OriginalDefaultWorkflowID string            `json:"-"`
	OriginalIssueTypeMappings map[string]string `json:"-"`
	DraftModifiedAt           string            `json:"-"`
	DraftModifiedBy           string            `json:"-"`
}

// Default is the built-in workflow: To Do ↔ In Progress → Done, Done → To Do.
// Transition ids match the Jira-style string ids clients expect.
func Default() Workflow {
	return Workflow{
		ID:   "wf_default",
		Name: "Default",
		Transitions: []Transition{
			{ID: "1", Name: "Create", Type: TransitionInitial, To: "st_todo"},
			{ID: "11", Name: "To Do", From: []string{"st_inprogress", "st_done"}, To: "st_todo"},
			{ID: "21", Name: "In Progress", From: []string{"st_todo", "st_done"}, To: "st_inprogress"},
			{ID: "31", Name: "Done", From: []string{"st_todo", "st_inprogress"}, To: "st_done"},
		},
	}
}

// Kind is the transition's type; a stored transition without one is directed.
func (t Transition) Kind() string {
	if t.Type == "" {
		return TransitionDirected
	}
	return t.Type
}

// runsFrom reports whether the transition can start from the status. A global
// transition runs from every status of its workflow, including its own
// destination, as in Jira; the initial transition only runs on creation.
func (w Workflow) runsFrom(t Transition, statusID string) bool {
	switch t.Kind() {
	case TransitionInitial:
		return false
	case TransitionGlobal:
		return slices.Contains(w.StatusIDs(), statusID)
	}
	return slices.Contains(t.From, statusID)
}

// Available returns the transitions legal from the given status.
func (w Workflow) Available(statusID string) []Transition {
	var out []Transition
	for _, t := range w.Transitions {
		if w.runsFrom(t, statusID) {
			out = append(out, t)
		}
	}
	return out
}

// StatusIDs lists the workflow's statuses: its designer statuses first, then
// every status its transitions reach or leave, each once.
func (w Workflow) StatusIDs() []string {
	seen := make(map[string]bool)
	ids := make([]string, 0, len(w.Statuses))
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, status := range w.Statuses {
		add(status.StatusReference)
	}
	for _, t := range w.Transitions {
		for _, from := range t.From {
			add(from)
		}
		add(t.To)
	}
	return ids
}

// Initial returns the workflow's initial transition, or nil.
func (w Workflow) Initial() *Transition {
	for i := range w.Transitions {
		if w.Transitions[i].Kind() == TransitionInitial {
			return &w.Transitions[i]
		}
	}
	return nil
}

// InitialStatus is the status new work items start in: the initial
// transition's destination. A workflow stored before it had one starts in To
// Do when it uses that status, and otherwise in its first status.
func (w Workflow) InitialStatus() string {
	if initial := w.Initial(); initial != nil {
		return initial.To
	}
	ids := w.StatusIDs()
	if len(ids) == 0 || slices.Contains(ids, "st_todo") {
		return "st_todo"
	}
	return ids[0]
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
		return t, w.runsFrom(*t, currentStatusID)
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

// IsAppRule reports a workflow rule provided by a Connect or Forge app. Jira
// runs such rules in the app; here they keep their configuration and never
// block a transition.
func IsAppRule(ruleKey string) bool {
	return strings.HasPrefix(ruleKey, "connect:") || strings.HasPrefix(ruleKey, "forge:")
}

func validateAppRule(rule Rule) error {
	if strings.TrimSpace(rule.Parameters["appKey"]) == "" || strings.TrimSpace(rule.Parameters["key"]) == "" {
		return fmt.Errorf("app workflow rule %q requires appKey and key", rule.RuleKey)
	}
	if disabled := rule.Parameters["disabled"]; disabled != "" && disabled != "true" && disabled != "false" {
		return fmt.Errorf("app workflow rule disabled must be true or false")
	}
	return nil
}

func evaluateCondition(rule Rule, context EvaluationContext) bool {
	if IsAppRule(rule.RuleKey) {
		return true
	}
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
	case RuleBlockInProgressApproval:
		pending, _, _ := approvalState(context.Approvals)
		return pending == 0
	case RuleApprovalsBlockUntilApproved:
		pending, approved, _ := approvalState(context.Approvals)
		return pending == 0 && approved > 0
	case RuleApprovalsBlockUntilRejected:
		pending, _, declined := approvalState(context.Approvals)
		return pending == 0 && declined > 0
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
		if IsAppRule(validator.RuleKey) {
			continue
		}
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
		if action.RuleKey == RuleUpdateField || action.RuleKey == RuleCopyFieldValue || action.RuleKey == RuleTriggerWebhook || action.RuleKey == RuleTriggerAgent || IsAppRule(action.RuleKey) {
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
		if IsAppRule(action.RuleKey) {
			continue
		}
		switch action.RuleKey {
		case RuleChangeAssignee, RuleTriggerWebhook, RuleTriggerAgent:
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
		if IsAppRule(action.RuleKey) {
			continue
		}
		switch action.RuleKey {
		case RuleChangeAssignee, RuleUpdateField, RuleCopyFieldValue, RuleTriggerAgent:
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

// AgentTrigger is an agent run a transition's post function requests.
type AgentTrigger struct {
	AgentID, Prompt string
}

// AgentTriggers returns the agent runs a transition requests, in workflow
// order.
func (t Transition) AgentTriggers() []AgentTrigger {
	triggers := []AgentTrigger{}
	for _, action := range t.Actions {
		if action.RuleKey == RuleTriggerAgent {
			triggers = append(triggers, AgentTrigger{AgentID: strings.TrimSpace(action.Parameters["agentId"]), Prompt: action.Parameters["promptValue"]})
		}
	}
	return triggers
}

// NextTransitionID numbers a new transition the way Jira does: the next
// multiple of ten plus one above the workflow's highest numeric id.
func NextTransitionID(transitions []Transition) string {
	highest := 1
	for _, transition := range transitions {
		if id, err := strconv.Atoi(transition.ID); err == nil && id > highest {
			highest = id
		}
	}
	return strconv.Itoa((highest/10+1)*10 + 1)
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
		switch transition.Screen.RuleKey {
		case RuleTransitionScreen:
			if len(commaValues(transition.Screen.Parameters["fields"])) == 0 {
				return fmt.Errorf("workflow transition screen is unsupported or incomplete")
			}
		case RuleRemindToUpdateFields:
			if len(commaValues(transition.Screen.Parameters["remindingFieldIds"])) == 0 {
				return fmt.Errorf("remind-people-to-update-fields requires remindingFieldIds")
			}
			if always := transition.Screen.Parameters["remindingAlwaysAsk"]; always != "" && always != "true" && always != "false" {
				return fmt.Errorf("remind-people-to-update-fields remindingAlwaysAsk must be true or false")
			}
		default:
			return fmt.Errorf("workflow transition screen is unsupported or incomplete")
		}
	}
	for _, validator := range transition.Validators {
		if err := validateRuleID(validator); err != nil {
			return err
		}
		if IsAppRule(validator.RuleKey) {
			if err := validateAppRule(validator); err != nil {
				return err
			}
			continue
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
		if IsAppRule(action.RuleKey) {
			if err := validateAppRule(action); err != nil {
				return err
			}
			continue
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
		case RuleTriggerAgent:
			if strings.TrimSpace(action.Parameters["agentId"]) == "" {
				return fmt.Errorf("trigger-agent requires the agent's account id")
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
	if t.Screen.RuleKey == RuleRemindToUpdateFields {
		return commaValues(t.Screen.Parameters["remindingFieldIds"])
	}
	return commaValues(t.Screen.Parameters["fields"])
}

// ScreenReminder returns the message a remind-people-to-update-fields screen
// shows and whether it always asks.
func (t Transition) ScreenReminder() (message string, alwaysAsk, ok bool) {
	if t.Screen == nil || t.Screen.RuleKey != RuleRemindToUpdateFields {
		return "", false, false
	}
	return t.Screen.Parameters["remindingMessage"], t.Screen.Parameters["remindingAlwaysAsk"] == "true", true
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
		if IsAppRule(condition.RuleKey) {
			if err := validateAppRule(condition); err != nil {
				return err
			}
			continue
		}
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
		case RuleBlockInProgressApproval:
			if len(condition.Parameters) != 0 {
				return fmt.Errorf("block-in-progress-approval does not accept parameters")
			}
		case RuleApprovalsBlockUntilApproved, RuleApprovalsBlockUntilRejected:
			raw := strings.TrimSpace(condition.Parameters["approvalConfigurationJson"])
			var configuration map[string]any
			if raw == "" || json.Unmarshal([]byte(raw), &configuration) != nil {
				return fmt.Errorf("%s requires approvalConfigurationJson holding a JSON object", condition.RuleKey)
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

// Jira's status property keys marking whether work items in a status can be
// edited; issueEditable is the deprecated spelling.
const (
	PropertyIssueEditable       = "jira.issue.editable"
	PropertyIssueEditableLegacy = "issueEditable"
)

// StatusEditable reports whether work items in the status can be edited: a
// status is editable unless its workflow properties set jira.issue.editable,
// or the deprecated issueEditable, to false.
func (w Workflow) StatusEditable(statusID string) bool {
	for _, status := range w.Statuses {
		if status.StatusReference != statusID {
			continue
		}
		for _, key := range []string{PropertyIssueEditable, PropertyIssueEditableLegacy} {
			if value, ok := status.Properties[key]; ok {
				return !strings.EqualFold(strings.TrimSpace(value), "false")
			}
		}
	}
	return true
}
