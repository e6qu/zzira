package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
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
	// ProjectID is where a create action raises its work, when the rule says
	// so rather than taking the triggering work item's project.
	ProjectID string
	// Body and Headers are what a web request sends besides its address:
	// the body the receiver asked for, and the headers it is read with, one
	// "Name: value" to a line.
	Body    string
	Headers string
	// Variable is what a create variable action names its value, which the
	// rest of the rule reads as {{name}}.
	Variable string
	// Page is the wiki page a comment or label action writes on, when it
	// names one rather than answering the page the rule ran for.
	Page string
	// Limit is how many work items a lookup keeps.
	Limit string
}

// automationFormPrompts reads the questions a manual rule asks. A row with
// nothing in it is no question; a row with anything in it needs a name and a
// variable, because the rule reads the answer by that variable.
func automationFormPrompts(names, variables, types, required []string) ([]map[string]any, error) {
	prompts := []map[string]any{}
	at := func(list []string, index int) string {
		if index < len(list) {
			return strings.TrimSpace(list[index])
		}
		return ""
	}
	for index := range names {
		name, variable := at(names, index), at(variables, index)
		inputType, want := at(types, index), at(required, index)
		if name == "" && variable == "" {
			continue
		}
		if name == "" {
			return nil, fmt.Errorf("every question needs what to call it")
		}
		if !manualPromptName.MatchString(variable) {
			return nil, fmt.Errorf("the variable for %q is a name such as reason or release_note", name)
		}
		if automationOptionName(automationPromptTypes, inputType) == "" {
			return nil, fmt.Errorf("choose what kind of answer %q takes", name)
		}
		for _, held := range prompts {
			if held["variableName"] == variable {
				return nil, fmt.Errorf("two questions cannot share the variable %q", variable)
			}
		}
		prompts = append(prompts, map[string]any{
			"displayName": name, "variableName": variable, "inputType": inputType, "required": want == "required",
		})
		if len(prompts) > maxManualPrompts {
			return nil, fmt.Errorf("a rule asks at most %d questions", maxManualPrompts)
		}
	}
	return prompts, nil
}

// automationFormHeaders reads the headers a web request is sent with, one
// "Name: value" to a line, as the editor takes them.
func automationFormHeaders(lines string) (map[string]string, error) {
	headers := map[string]string{}
	for _, line := range strings.Split(lines, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !found || name == "" {
			return nil, fmt.Errorf("a request header is written as Name: value, not %q", line)
		}
		headers[name] = strings.TrimSpace(value)
	}
	return headers, nil
}

// automationHeaderLines writes a saved action's headers back as the editor
// shows them, in a settled order so the same rule reads the same way twice.
func automationHeaderLines(raw json.RawMessage) string {
	var value struct {
		Headers map[string]string `json:"headers"`
	}
	automationComponentValue(raw, &value)
	if len(value.Headers) == 0 {
		return ""
	}
	names := make([]string, 0, len(value.Headers))
	for name := range value.Headers {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, name+": "+value.Headers[name])
	}
	return strings.Join(lines, "\n")
}

// automationOption is a choice in the rule editor.
type automationOption struct{ Value, Name string }

// automationTriggerView is the rule's trigger as the editor shows it.
type automationTriggerView struct {
	Type, FromStatus, ToStatus, Fields string
	AllowRules                         bool
	// WebhookIssues is whether an incoming webhook rule runs for the work
	// items the request names, rather than for none.
	WebhookIssues bool
	// Prompts are what a manual rule asks whoever runs it, in order, with a
	// blank row at the end to add another.
	Prompts []automationPromptView
}

// automationPromptView is one question a manual rule asks before it runs.
type automationPromptView struct {
	DisplayName, VariableName, InputType string
	Required                             bool
}

// automationPromptTypes are the kinds of answer a prompt takes, as Jira's
// manual rules offer them.
var automationPromptTypes = []automationOption{
	{"TEXT", "Text"}, {"TEXT_AREA", "Paragraph"}, {"NUMBER", "Number"}, {"DATE", "Date"}, {"CHECKBOX", "Yes or no"},
}

// manualPromptName is what a prompt's variable may be called: the smart value
// {{userInputs.<name>}} has to name it, so it is a plain identifier.
var manualPromptName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

// maxManualPrompts is how many questions one rule asks. Jira's own editor
// stops well before a form nobody would fill in.
const maxManualPrompts = 10

// automationConditionView is a work item fields or JQL condition in the
// editor; a JQL condition has the field "jql".
type automationConditionView struct{ Field, Operator, Value string }

// automationBranchView is a related work items branch and its actions.
type automationBranchView struct {
	RelatedType, LinkTypes string
	// JQL is what a branch over matching work runs for.
	JQL string
	// SmartValue is the list a branch over values runs over, and Variable
	// what each item is called inside it.
	SmartValue, Variable string
	Actions              []automationActionView
	// Conditions choose which related work items the branch acts on.
	Conditions []automationConditionView
}

var (
	automationTriggers = []automationOption{
		{"jira.jql.scheduled", "Scheduled"}, {"jira.issue.event.trigger:created", "Work item created"},
		{"jira.issue.event.trigger:transitioned", "Work item transitioned"}, {"jira.issue.field.changed", "Field value changed"},
		{"jira.issue.event.trigger:commented", "Work item commented"}, {"jira.issue.event.trigger:linked", "Work item linked"},
		{"jira.issue.event.trigger:assigned", "Work item assigned"}, {"jira.issue.attachment.added", "Attachment added"},
		{"jira.issue.event.trigger:moved", "Work item moved"}, {"jira.issue.event.trigger:deleted", "Work item deleted"},
		{"jira.version.event.trigger:created", "Version created"}, {"jira.version.event.trigger:updated", "Version updated"},
		{"jira.version.event.trigger:released", "Version released"},
		{"jira.sprint.event.trigger:started", "Sprint started"}, {"jira.sprint.event.trigger:completed", "Sprint completed"},
		{automation.WebhookTriggerType, "Incoming webhook"},
		{"confluence.page.created", "Page created"}, {"confluence.page.updated", "Page updated"},
		{"confluence.page.commented", "Page commented"}, {"confluence.page.labelled", "Page labelled"},
		{"confluence.blogpost.created", "Blog post created"},
		{automation.ManualTriggerType, "Run manually from a work item"},
	}
	automationActionTypes = []automationOption{
		{automation.VariableActionType, "Create variable"},
		{automation.WikiCommentActionType, "Comment on a page"}, {automation.WikiLabelActionType, "Label a page"},
		{automation.WikiAppendActionType, "Write at the end of a page"}, {automation.WikiArchiveActionType, "Archive a page"},
		{automation.LookupActionType, "Look up work items"},
		{"jira.issue.add-label", "Add label"}, {"jira.issue.remove-label", "Remove label"}, {"jira.issue.assign", "Assign work item"},
		{"jira.issue.assign:round-robin", "Assign work item (round-robin)"}, {"jira.issue.assign:balanced", "Assign work item (balanced workload)"},
		{"jira.issue.assign:random", "Assign work item (random)"}, {"jira.issue.transition", "Transition work item"},
		{"jira.issue.comment", "Comment on work item"}, {"jira.issue.edit:summary", "Edit summary"}, {"jira.issue.edit:duedate", "Set due date"},
		{"jira.issue.edit:priority", "Set priority"}, {"jira.issue.edit:description", "Set description"},
		{"jira.issue.edit:labels", "Set labels"}, {"jira.issue.edit:assignee", "Set assignee"},
		{"jira.issue.edit:resolution", "Set resolution"},
		{"jira.issue.edit:originalestimate", "Set original estimate"},
		{"jira.issue.edit:remainingestimate", "Set remaining estimate"},
		{"jira.issue.log-work", "Log work"}, {"jira.issue.delete", "Delete work item"}, {"jira.issue.create-subtask", "Create sub-task"},
		{"jira.issue.clone", "Clone work item"},
		{"jira.issue.watchers:add", "Add watcher"}, {"jira.issue.watchers:remove", "Remove watcher"},
		{automation.WikiPageActionType, "Create page in space"},
		{"jira.issue.email:assignee", "Email the assignee"}, {"jira.issue.email:reporter", "Email the reporter"},
		{"jira.issue.email:watchers", "Email the watchers"},
		{automation.WebRequestActionType + ":POST", "Send web request (POST)"},
		{automation.WebRequestActionType + ":PUT", "Send web request (PUT)"},
		{automation.WebRequestActionType + ":GET", "Send web request (GET)"},
		{automation.WebRequestActionType + ":DELETE", "Send web request (DELETE)"},
	}
	automationRelatedTypes    = []automationOption{{"sub-tasks", "Sub-tasks"}, {"parent", "Parent"}, {"linked", "Linked work items"}, {"jql", "Work matching JQL"}, {"created", "Work this rule created"}, {"smart-values", "Each item in a list"}}
	automationConditionFields = []automationOption{
		{"jql", "Matches JQL"}, {"related:sub-tasks", "Sub-tasks match JQL"}, {"related:parent", "Parent matches JQL"},
		{"related:linked", "Linked work matches JQL"}, {"status", "Status"}, {"priority", "Priority"}, {"issuetype", "Work type"}, {"assignee", "Assignee"},
		{"reporter", "Reporter"}, {"labels", "Labels"}, {"summary", "Summary"}, {"duedate", "Due date"},
		{"resolution", "Resolution"}, {"created", "Created"}, {"resolved", "Resolved"}, {"parent", "Parent"}, {"key", "Work item key"},
	}
	automationConditionOperators = []automationOption{
		{"EQUALS", "equals"}, {"NOT_EQUALS", "does not equal"}, {"CONTAINS", "contains"}, {"NOT_CONTAINS", "does not contain"},
		{"STARTS_WITH", "starts with"}, {"ENDS_WITH", "ends with"}, {"IS_ONE_OF", "is one of"}, {"IS_NOT_ONE_OF", "is not one of"},
		{"GREATER_THAN", "is after"}, {"LESS_THAN", "is before"}, {"IS_EMPTY", "is empty"}, {"IS_NOT_EMPTY", "is not empty"},
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
	// PromptTypes are the kinds of answer a manual rule's question takes.
	PromptTypes []automationOption
	// Unsupported names what the editor cannot show in the rule, which turns
	// saving there off so it is not lost.
	Unsupported string
	// Branches are the rule's branches, in the order it runs them, with a
	// blank one at the end to add another; RelatedTypes is the work a branch
	// can run for.
	Branches     []automationBranchView
	RelatedTypes []automationOption
	Runs         []automationRunView
	Members      []*models.User
	Statuses     []models.Status
	// Projects are where a create action can raise work when the trigger
	// brings no work item to take a project from.
	Projects []*models.Project
	CloudID  string
	// WebhookURL is the address an incoming webhook rule is called at.
	WebhookURL string
	IsNew      bool
	FormAction string
	Error      string
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
		// The card says what starts the rule, whatever kind of trigger that
		// is: a webhook and a rule run by hand have no event trigger either,
		// and both used to read as an imported rule nobody here could name.
		card := automationRuleCard{Rule: rule, Trigger: "Imported trigger"}
		if name := automationOptionName(automationTriggers, parseAutomationTrigger(rule.Payload).Type); name != "" {
			card.Trigger = name
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
	if err == nil {
		err = h.validateAutomationReferences(r, workspaceID, body)
	}
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
			if err = h.validateAutomationReferences(r, workspaceID, body); err == nil {
				err = h.Automation.UpdateRule(r.Context(), workspaceID, h.currentUser(r).ID, uuid, body)
			}
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
	linkTypes, err := h.Store.LinkTypes(r.Context(), workspaceID)
	if err != nil {
		return automationEditorData{}, err
	}
	issueTypes, err := h.Store.IssueTypesForWorkspace(r.Context(), workspaceID)
	if err != nil {
		return automationEditorData{}, err
	}
	actionTypes := append([]automationOption{}, automationActionTypes...)
	for _, linkType := range linkTypes {
		actionTypes = append(actionTypes, automationOption{"jira.issue.link:" + linkType.ID, "Link: this work item " + linkType.Outward})
	}
	// Sub-tasks keep their own action, because they are raised under the work
	// item the rule runs for rather than beside it.
	for _, issueType := range issueTypes {
		if !issueType.Subtask {
			actionTypes = append(actionTypes, automationOption{"jira.issue.create:" + issueType.ID, "Create: " + issueType.Name})
		}
	}
	projects, err := h.Store.ProjectsByWorkspace(r.Context(), workspaceID)
	if err != nil {
		return automationEditorData{}, err
	}
	data := automationEditorData{Rule: rule, Members: members, Statuses: statuses, CloudID: cloudID, Projects: projects,
		Triggers: automationTriggers, ActionTypes: actionTypes, ConditionFields: automationConditionFields, ConditionOperators: automationConditionOperators,
		RelatedTypes: automationRelatedTypes, PromptTypes: automationPromptTypes}
	if rule != nil && rule.WebhookToken != "" {
		data.WebhookURL = strings.TrimSuffix(h.BaseURL, "/") + "/pro/hooks/" + url.PathEscape(rule.WebhookToken)
	}
	if rule == nil {
		data.Trigger = automationTriggerView{Type: "jira.jql.scheduled", Prompts: []automationPromptView{{InputType: "TEXT"}}}
		data.Conditions = []automationConditionView{{}}
		data.Branches = []automationBranchView{blankAutomationBranch()}
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
	data.Branches = parseAutomationBranches(rule.Payload)
	// A blank branch at the end is how another one is added, as a blank row
	// is how another action is.
	data.Branches = append(data.Branches, blankAutomationBranch())
	return data, nil
}

// validateAutomationReferences refuses a rule naming a link type or work type
// the site does not hold, so an action is checked when the rule is saved rather
// than when it next runs.
func (h *Handler) validateAutomationReferences(r *http.Request, workspaceID string, body json.RawMessage) error {
	var rule struct {
		Components []automationComponentJSON `json:"components"`
	}
	if json.Unmarshal(body, &rule) != nil {
		return nil
	}
	named, types := map[string]bool{}, map[string]bool{}
	var collect func(components []automationComponentJSON)
	collect = func(components []automationComponentJSON) {
		for _, component := range components {
			var fields map[string]string
			switch component.Type {
			case "jira.issue.link":
				automationComponentValue(component.Value, &fields)
				named[fields["linkTypeId"]] = true
			case "jira.issue.create":
				automationComponentValue(component.Value, &fields)
				types[fields["issueTypeId"]] = true
			}
			collect(component.Children)
		}
	}
	collect(rule.Components)
	if len(named) > 0 {
		linkTypes, err := h.Store.LinkTypes(r.Context(), workspaceID)
		if err != nil {
			return err
		}
		held := map[string]bool{}
		for _, linkType := range linkTypes {
			held[linkType.ID] = true
		}
		for id := range named {
			if !held[id] {
				return fmt.Errorf("a link action names a link type this site does not hold")
			}
		}
	}
	if len(types) > 0 {
		issueTypes, err := h.Store.IssueTypesForWorkspace(r.Context(), workspaceID)
		if err != nil {
			return err
		}
		held := map[string]bool{}
		for _, issueType := range issueTypes {
			held[issueType.ID] = !issueType.Subtask
		}
		for id := range types {
			if !held[id] {
				return fmt.Errorf("a create action names a work type this site does not offer")
			}
		}
	}
	return nil
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
	case "jira.issue.event.trigger:created", "jira.issue.event.trigger:commented", "jira.issue.event.trigger:linked",
		"jira.issue.event.trigger:assigned", "jira.issue.attachment.added", "jira.issue.event.trigger:moved":
		triggerValue = map[string]any{"jql": query}
	case "jira.issue.event.trigger:deleted", "jira.version.event.trigger:created", "jira.version.event.trigger:updated",
		"jira.version.event.trigger:released", "jira.sprint.event.trigger:started", "jira.sprint.event.trigger:completed",
		"confluence.page.created", "confluence.page.updated", "confluence.page.commented",
		"confluence.page.labelled", "confluence.blogpost.created":
		// These happen to something a rule cannot match work against: a work
		// item that has gone, a version, a sprint, or something in the wiki.
		if query != "" {
			return nil, fmt.Errorf("that trigger takes no JQL: there is no work item to match")
		}
		triggerValue = map[string]any{}
	case "jira.issue.event.trigger:transitioned":
		triggerValue = map[string]any{"jql": query, "fromStatusIds": nonEmpty(r.PostFormValue("from_status")), "toStatusIds": nonEmpty(r.PostFormValue("to_status"))}
	case "jira.issue.field.changed":
		fields := splitLines(r.PostFormValue("changed_fields"))
		if len(fields) == 0 || len(fields) > 20 {
			return nil, fmt.Errorf("choose between 1 and 20 fields for the field value changed trigger")
		}
		triggerValue = map[string]any{"jql": query, "fields": fields}
	case automation.WebhookTriggerType:
		// Jira's incoming webhook runs either for the work items the request
		// names or for none at all, and the rule says which it expects.
		issues := r.PostFormValue("webhook_issues") == "true"
		triggerValue = map[string]any{"jql": query, "issuesFromWebhook": issues}
	case automation.ManualTriggerType:
		prompts, err := automationFormPrompts(r.PostForm["prompt_name"], r.PostForm["prompt_variable"],
			r.PostForm["prompt_type"], r.PostForm["prompt_required"])
		if err != nil {
			return nil, err
		}
		// A manual rule runs for the work item it was run from, so its scope
		// is that work item rather than a query.
		triggerValue = map[string]any{"inputPrompts": prompts}
	default:
		return nil, fmt.Errorf("unsupported trigger")
	}
	components, err := automationFormConditions(r.PostForm["condition_field"], r.PostForm["condition_operator"], r.PostForm["condition_value"])
	if err != nil {
		return nil, err
	}
	actionComponents, err := automationFormActions(automationActionRows{
		Types: r.PostForm["action_type"], Values: r.PostForm["action_value"], Projects: r.PostForm["action_project"],
		Bodies: r.PostForm["action_body"], HeaderLines: r.PostForm["action_headers"], Variables: r.PostForm["action_variable"],
		Pages: r.PostForm["action_page"], Limits: r.PostForm["action_limit"],
	})
	if err != nil {
		return nil, err
	}
	components = append(components, actionComponents...)
	actions := len(actionComponents)
	// Every branch section posts one of each of its own fields, so they line
	// up by position, and each of a branch's rows carries the ordinal of the
	// branch it belongs to. A rule can hold several branches, which the
	// runner has always allowed.
	rowsFor := func(ordinal int, indexes []string, columns ...[]string) [][]string {
		picked := make([][]string, len(columns))
		for row, index := range indexes {
			if strings.TrimSpace(index) != strconv.Itoa(ordinal) {
				continue
			}
			for column := range columns {
				picked[column] = append(picked[column], formValueAt(columns[column], row))
			}
		}
		return picked
	}
	for ordinal, related := range r.PostForm["branch_related"] {
		related = strings.TrimSpace(related)
		if related == "" {
			continue
		}
		if automationOptionName(automationRelatedTypes, related) == "" {
			return nil, fmt.Errorf("unsupported related work items")
		}
		conditionRows := rowsFor(ordinal, r.PostForm["branch_condition_index"],
			r.PostForm["branch_condition_field"], r.PostForm["branch_condition_operator"], r.PostForm["branch_condition_value"])
		// The branch's conditions choose which related work items its actions
		// run for, so they come first.
		children, err := automationFormConditions(conditionRows[0], conditionRows[1], conditionRows[2])
		if err != nil {
			return nil, err
		}
		actionRows := rowsFor(ordinal, r.PostForm["branch_action_index"],
			r.PostForm["branch_action_type"], r.PostForm["branch_action_value"], r.PostForm["branch_action_variable"], r.PostForm["branch_action_page"])
		branchActions, err := automationFormActions(automationActionRows{
			Types: actionRows[0], Values: actionRows[1], Variables: actionRows[2], Pages: actionRows[3],
		})
		if err != nil {
			return nil, err
		}
		if len(branchActions) == 0 {
			return nil, fmt.Errorf("add at least one action for the related work items")
		}
		children = append(children, branchActions...)
		value := map[string]any{"relatedType": related}
		if linkTypes := splitLines(formValueAt(r.PostForm["branch_link_types"], ordinal)); related == "linked" && len(linkTypes) > 0 {
			value["linkTypes"] = linkTypes
		}
		if related == "jql" {
			query := strings.TrimSpace(formValueAt(r.PostForm["branch_jql"], ordinal))
			if query == "" {
				return nil, fmt.Errorf("a branch over work matching JQL needs the query")
			}
			value["jql"] = query
		}
		if related == "smart-values" {
			list := strings.TrimSpace(formValueAt(r.PostForm["branch_smart_value"], ordinal))
			item := strings.TrimSpace(formValueAt(r.PostForm["branch_variable"], ordinal))
			if list == "" {
				return nil, fmt.Errorf("a branch over a list needs the smart value holding it")
			}
			if !manualPromptName.MatchString(item) {
				return nil, fmt.Errorf("name each item like item or release: a letter, then letters, digits, _ or -")
			}
			value["smartValue"], value["variableName"] = list, item
		}
		components = append(components, map[string]any{"component": "BRANCH", "schemaVersion": 1, "type": "jira.issue.related", "value": value, "children": children})
		actions += len(branchActions)
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

// automationLookupLimit reads how many a lookup keeps, which is a number in
// the component and a string in the form.
func automationLookupLimit(raw json.RawMessage) string {
	var value struct {
		Limit int `json:"limit"`
	}
	automationComponentValue(raw, &value)
	if value.Limit <= 0 {
		return ""
	}
	return strconv.Itoa(value.Limit)
}

// formValueAt reads one row of a form column, trimmed, answering nothing for
// a row the form did not post.
func formValueAt(column []string, row int) string {
	if row < 0 || row >= len(column) {
		return ""
	}
	return strings.TrimSpace(column[row])
}

// automationActionRows are the editor's action columns as the form posts
// them, one entry per row: an action reads the columns it uses.
type automationActionRows struct {
	Types, Values, Projects, Bodies, HeaderLines, Variables, Pages, Limits []string
}

// automationFormActions reads the editor's action rows into rule components.
// automationFormConditions turns the editor's condition rows into condition
// components: a JQL condition, or a field compared with a value. Rows without
// a field are skipped.
func automationFormConditions(fields, operators, values []string) ([]map[string]any, error) {
	components := []map[string]any{}
	for index, field := range fields {
		if field = strings.TrimSpace(field); field == "" {
			continue
		}
		operator, value := "", ""
		if index < len(operators) {
			operator = operators[index]
		}
		if index < len(values) {
			value = strings.TrimSpace(values[index])
		}
		if related, ok := strings.CutPrefix(field, "related:"); ok {
			if automationOptionName(automationRelatedTypes, related) == "" {
				return nil, fmt.Errorf("unsupported related work items")
			}
			condition := map[string]any{"relatedType": related, "jql": value}
			if related == "linked" {
				condition["linkTypes"] = []string{}
			}
			components = append(components, map[string]any{
				"component": "CONDITION", "schemaVersion": 1, "type": "jira.issue.related.condition", "value": condition,
			})
			continue
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
	return components, nil
}

// automationFormActions reads the rule's actions. projects is the project
// each create action raises its work in, which matters when the trigger
// brings no work item to take one from; it is empty for every other action.
func automationFormActions(rows automationActionRows) ([]map[string]any, error) {
	types, values, projects, bodies, headerLines, variables := rows.Types, rows.Values, rows.Projects, rows.Bodies, rows.HeaderLines, rows.Variables
	pages, limits := rows.Pages, rows.Limits
	components := []map[string]any{}
	at := formValueAt
	for index, actionType := range types {
		actionType = strings.TrimSpace(actionType)
		if actionType == "" {
			continue
		}
		value, project := "", ""
		if index < len(values) {
			value = strings.TrimSpace(values[index])
		}
		if index < len(projects) {
			project = strings.TrimSpace(projects[index])
		}
		// A create variable action names its value in its own column, and
		// what the variable holds is the row's value, which may be empty: a
		// rule can name what a web request answered even when it answered
		// nothing.
		if actionType == automation.VariableActionType {
			components = append(components, map[string]any{
				"component": "ACTION", "schemaVersion": 1, "type": automation.VariableActionType,
				"value": map[string]string{"variableName": at(variables, index), "variableValue": value},
			})
			continue
		}
		// A lookup asks the site a question and keeps the answer: the query
		// is the row's value, and how many to keep is beside it.
		if actionType == automation.LookupActionType {
			if value == "" {
				return nil, fmt.Errorf("a lookup work items action needs the query it runs")
			}
			lookup := map[string]any{"jql": value}
			if kept := at(limits, index); kept != "" {
				count, err := strconv.Atoi(kept)
				if err != nil || count < 1 || count > 100 {
					return nil, fmt.Errorf("a lookup keeps between 1 and 100 work items")
				}
				lookup["limit"] = count
			}
			components = append(components, map[string]any{
				"component": "ACTION", "schemaVersion": 1, "type": automation.LookupActionType, "value": lookup,
			})
			continue
		}
		// What a rule writes in the wiki: the page it writes on is the one
		// the rule ran for unless the action names another.
		if actionType == automation.WikiCommentActionType || actionType == automation.WikiLabelActionType ||
			actionType == automation.WikiAppendActionType || actionType == automation.WikiArchiveActionType {
			// Archiving takes no value: the page is the whole instruction.
			if value == "" && actionType != automation.WikiArchiveActionType {
				return nil, fmt.Errorf("every action needs a value")
			}
			wiki := map[string]string{"pageId": at(pages, index)}
			switch actionType {
			case automation.WikiCommentActionType:
				wiki["comment"] = value
			case automation.WikiAppendActionType:
				wiki["body"] = value
			case automation.WikiLabelActionType:
				wiki["label"] = value
			}
			components = append(components, map[string]any{
				"component": "ACTION", "schemaVersion": 1, "type": actionType, "value": wiki,
			})
			continue
		}
		// Deleting takes no value, as it takes none in Jira.
		if actionType == "jira.issue.delete" {
			components = append(components, map[string]any{
				"component": "ACTION", "schemaVersion": 1, "type": "jira.issue.delete", "value": map[string]string{},
			})
			continue
		}
		// A copy takes a summary or the one Jira writes, so an empty box is
		// an instruction rather than a mistake.
		if actionType == "jira.issue.clone" {
			components = append(components, map[string]any{
				"component": "ACTION", "schemaVersion": 1, "type": "jira.issue.clone", "value": map[string]string{"summary": value},
			})
			continue
		}
		// Clearing a field is an instruction rather than a mistake: an edit
		// that sets a resolution, an estimate or a due date empties it when
		// the box is left empty, as Jira's own edit does.
		clearing := map[string]bool{
			"jira.issue.edit:resolution": true, "jira.issue.edit:originalestimate": true,
			"jira.issue.edit:remainingestimate": true, "jira.issue.edit:duedate": true,
		}
		if value == "" && !clearing[actionType] {
			return nil, fmt.Errorf("every action needs a value")
		}
		var actionValue map[string]string
		// A link action names its link type in the action, as an edit names
		// the field it sets.
		if linkTypeID := strings.TrimPrefix(actionType, "jira.issue.link:"); linkTypeID != actionType && strings.TrimSpace(linkTypeID) != "" {
			components = append(components, map[string]any{
				"component": "ACTION", "schemaVersion": 1, "type": "jira.issue.link",
				"value": map[string]string{"linkTypeId": linkTypeID, "issueKey": value},
			})
			continue
		}
		// A create action names its work type in the action, as a link names
		// its link type.
		if issueTypeID := strings.TrimPrefix(actionType, "jira.issue.create:"); issueTypeID != actionType && strings.TrimSpace(issueTypeID) != "" {
			create := map[string]string{"issueTypeId": issueTypeID, "summary": value}
			if project != "" {
				create["projectId"] = project
			}
			components = append(components, map[string]any{
				"component": "ACTION", "schemaVersion": 1, "type": "jira.issue.create", "value": create,
			})
			continue
		}
		// A web request names its method in the action, as a link names its
		// link type, and carries the address it is sent to.
		if method := strings.TrimPrefix(actionType, automation.WebRequestActionType+":"); method != actionType && strings.TrimSpace(method) != "" {
			request := map[string]any{"method": method, "url": value}
			if body := at(bodies, index); body != "" {
				request["body"] = body
			}
			headers, err := automationFormHeaders(at(headerLines, index))
			if err != nil {
				return nil, err
			}
			if len(headers) > 0 {
				request["headers"] = headers
			}
			components = append(components, map[string]any{
				"component": "ACTION", "schemaVersion": 1, "type": automation.WebRequestActionType, "value": request,
			})
			continue
		}
		// An email action names its recipients in the action, as a link names
		// its link type.
		if recipient := strings.TrimPrefix(actionType, "jira.issue.email:"); recipient != actionType && strings.TrimSpace(recipient) != "" {
			components = append(components, map[string]any{
				"component": "ACTION", "schemaVersion": 1, "type": "jira.issue.email",
				"value": map[string]string{"recipient": recipient, "body": value},
			})
			continue
		}
		switch actionType {
		case "jira.issue.add-label", "jira.issue.remove-label":
			actionValue = map[string]string{"label": value}
		case "jira.issue.assign":
			actionValue = map[string]string{"accountId": value}
		case "jira.issue.assign:round-robin", "jira.issue.assign:balanced", "jira.issue.assign:random":
			actionValue = map[string]string{"method": strings.TrimPrefix(actionType, "jira.issue.assign:")}
			actionType = "jira.issue.assign"
		case "jira.issue.transition":
			actionValue = map[string]string{"statusId": value}
		case "jira.issue.comment":
			actionValue = map[string]string{"comment": value}
		case "jira.issue.create-subtask":
			actionValue = map[string]string{"summary": value}
		case "jira.issue.log-work":
			actionValue = map[string]string{"duration": value}
		case "jira.issue.watchers:add", "jira.issue.watchers:remove":
			actionValue = map[string]string{"action": strings.TrimPrefix(actionType, "jira.issue.watchers:"), "accountId": value}
			actionType = "jira.issue.watchers"
		case automation.WikiPageActionType:
			actionValue = map[string]string{"spaceKey": value}

		case "jira.issue.edit:summary", "jira.issue.edit:duedate", "jira.issue.edit:priority", "jira.issue.edit:description", "jira.issue.edit:labels",
			"jira.issue.edit:assignee", "jira.issue.edit:resolution", "jira.issue.edit:originalestimate", "jira.issue.edit:remainingestimate":
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
		return (component.Component == "" || component.Component == "ACTION") && (component.Type == "jira.issue.edit" || component.Type == "jira.issue.link" || component.Type == "jira.issue.email" || component.Type == "jira.issue.create" || component.Type == "jira.issue.watchers" || component.Type == automation.WebRequestActionType || automationOptionName(automationActionTypes, component.Type) != "")
	}
	for index, component := range rule.Components {
		switch component.Component {
		case "CONDITION":
			if component.Type != "jira.issue.condition" && component.Type != "jira.jql.condition" && component.Type != "jira.issue.related.condition" {
				return "some of its conditions"
			}
		case "BRANCH":
			// The editor shows the rule's branches after everything else,
			// each one its conditions and then its actions. A branch with
			// something after it, or one that mixes the order, would change
			// meaning when saved, so such a rule stays as it is.
			if component.Type != "jira.issue.related" {
				return "its branches"
			}
			for _, later := range rule.Components[index+1:] {
				if later.Component != "BRANCH" {
					return "its branches"
				}
			}
			acting := false
			for _, child := range component.Children {
				switch {
				case child.Component == "CONDITION" && (child.Type == "jira.issue.condition" || child.Type == "jira.jql.condition" || child.Type == "jira.issue.related.condition") && !acting:
				case editableAction(child):
					acting = true
				default:
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
		FromStatusIDs     []string `json:"fromStatusIds"`
		ToStatusIDs       []string `json:"toStatusIds"`
		Fields            []string `json:"fields"`
		IssuesFromWebhook bool     `json:"issuesFromWebhook"`
		InputPrompts      []struct {
			DisplayName  string `json:"displayName"`
			VariableName string `json:"variableName"`
			InputType    string `json:"inputType"`
			Required     bool   `json:"required"`
		} `json:"inputPrompts"`
	}
	automationComponentValue(rule.Trigger.Value, &value)
	view := automationTriggerView{Type: rule.Trigger.Type, Fields: strings.Join(value.Fields, ", "), AllowRules: rule.CanOtherRuleTrigger,
		WebhookIssues: value.IssuesFromWebhook}
	for _, prompt := range value.InputPrompts {
		view.Prompts = append(view.Prompts, automationPromptView{
			DisplayName: prompt.DisplayName, VariableName: prompt.VariableName,
			InputType: prompt.InputType, Required: prompt.Required,
		})
	}
	// One blank row so another question can be added without a trip through
	// the API.
	view.Prompts = append(view.Prompts, automationPromptView{InputType: "TEXT"})
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
		Components []automationComponentJSON `json:"components"`
	}
	_ = json.Unmarshal(payload, &rule)
	return automationConditionViews(rule.Components)
}

// automationConditionViews lists the work item field and JQL conditions among
// components, the rule's own or a branch's, for the editor.
func automationConditionViews(components []automationComponentJSON) []automationConditionView {
	conditions := []automationConditionView{}
	for _, component := range components {
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
		if component.Type == "jira.issue.related.condition" {
			var value struct {
				RelatedType string `json:"relatedType"`
				JQL         string `json:"jql"`
			}
			automationComponentValue(component.Value, &value)
			conditions = append(conditions, automationConditionView{Field: "related:" + value.RelatedType, Value: value.JQL})
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
			if method := fields["method"]; method != "" {
				view.Type, view.Value = "jira.issue.assign:"+method, method
			}
		case "jira.issue.transition":
			view.Value = fields["statusId"]
		case "jira.issue.comment":
			view.Value = fields["comment"]
		case "jira.issue.create-subtask":
			view.Value = fields["summary"]
		case "jira.issue.log-work":
			view.Value = fields["duration"]
		case "jira.issue.clone":
			view.Value = fields["summary"]
		case "jira.issue.watchers":
			// The editor offers adding and removing as two actions, as Jira's
			// own rule builder does, and the rule stores one with a choice.
			view.Type, view.Value = "jira.issue.watchers:"+fields["action"], fields["accountId"]
		case automation.WikiPageActionType:
			view.Value = fields["spaceKey"]
		case "jira.issue.delete":
			view.Value = ""
		case automation.VariableActionType:
			view.Value, view.Variable = fields["variableValue"], fields["variableName"]
		case automation.WikiCommentActionType:
			view.Value, view.Page = fields["comment"], fields["pageId"]
		case automation.WikiLabelActionType:
			view.Value, view.Page = fields["label"], fields["pageId"]
		case automation.WikiAppendActionType:
			view.Value, view.Page = fields["body"], fields["pageId"]
		case automation.WikiArchiveActionType:
			view.Page = fields["pageId"]
		case automation.LookupActionType:
			view.Value = fields["jql"]
			view.Limit = automationLookupLimit(component.Value)
		case "jira.issue.edit":
			view.Type, view.Value = "jira.issue.edit:"+fields["field"], fields["value"]
		case "jira.issue.link":
			view.Type, view.Value = "jira.issue.link:"+fields["linkTypeId"], fields["issueKey"]
		case "jira.issue.email":
			view.Type, view.Value = "jira.issue.email:"+fields["recipient"], fields["body"]
		case "jira.issue.create":
			view.Type, view.Value = "jira.issue.create:"+fields["issueTypeId"], fields["summary"]
			view.ProjectID = fields["projectId"]
		case automation.WebRequestActionType:
			view.Type, view.Value = automation.WebRequestActionType+":"+fields["method"], fields["url"]
			view.Body = fields["body"]
			view.Headers = automationHeaderLines(component.Value)
		}
		actions = append(actions, view)
	}
	return actions
}

// blankAutomationBranch is the empty branch the editor offers for adding one.
func blankAutomationBranch() automationBranchView {
	return automationBranchView{Actions: []automationActionView{}, Conditions: []automationConditionView{{}}}
}

// parseAutomationBranches reads every branch the rule holds, in order, each
// with a blank condition row to add another.
func parseAutomationBranches(payload json.RawMessage) []automationBranchView {
	var rule struct {
		Components []automationComponentJSON `json:"components"`
	}
	_ = json.Unmarshal(payload, &rule)
	branches := []automationBranchView{}
	for _, component := range rule.Components {
		if component.Component != "BRANCH" {
			continue
		}
		branch := automationBranchOf(component)
		branch.Conditions = append(branch.Conditions, automationConditionView{})
		branches = append(branches, branch)
	}
	return branches
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
		return automationBranchOf(component)
	}
	return automationBranchView{Actions: []automationActionView{}, Conditions: []automationConditionView{}}
}

// automationBranchOf reads one branch component as the editor shows it.
func automationBranchOf(component automationComponentJSON) automationBranchView {
	var value struct {
		RelatedType  string   `json:"relatedType"`
		LinkTypes    []string `json:"linkTypes"`
		JQL          string   `json:"jql"`
		SmartValue   string   `json:"smartValue"`
		VariableName string   `json:"variableName"`
	}
	automationComponentValue(component.Value, &value)
	return automationBranchView{RelatedType: value.RelatedType, LinkTypes: strings.Join(value.LinkTypes, ", "), JQL: value.JQL,
		SmartValue: value.SmartValue, Variable: value.VariableName,
		Actions: automationActionViews(component.Children), Conditions: automationConditionViews(component.Children)}
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
