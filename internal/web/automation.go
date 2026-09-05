package web

import (
	"encoding/json"
	"fmt"
	"net/http"
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

type automationRunView struct {
	Run      automation.Run
	When     string
	Duration string
}

type automationEditorData struct {
	Rule       *automation.Rule
	Actions    []automationActionView
	Runs       []automationRunView
	Members    []*models.User
	Statuses   []models.Status
	CloudID    string
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
	for _, rule := range page.Rules {
		card := automationRuleCard{Rule: rule, Trigger: "Imported trigger"}
		if rule.IntervalMinutes != nil {
			card.Trigger = "Scheduled"
			card.Schedule = intervalLabel(*rule.IntervalMinutes)
		}
		if rule.NextRunAt != nil && rule.State == "ENABLED" {
			card.NextRun = rule.NextRunAt.In(time.Local).Format("2 Jan 2006, 15:04")
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
	http.Redirect(w, r, "/settings/automation/"+uuid, http.StatusSeeOther)
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
	http.Redirect(w, r, "/settings/automation/"+uuid, http.StatusSeeOther)
}

func (h *Handler) automationEditorData(r *http.Request, workspaceID string, rule *automation.Rule) (automationEditorData, error) {
	members, err := h.Store.MembersByWorkspace(r.Context(), workspaceID)
	if err != nil {
		return automationEditorData{}, err
	}
	statuses, err := h.Store.AllStatuses(r.Context())
	if err != nil {
		return automationEditorData{}, err
	}
	cloudID, err := h.Automation.WorkspaceCloudID(r.Context(), workspaceID)
	if err != nil {
		return automationEditorData{}, err
	}
	data := automationEditorData{Rule: rule, Members: members, Statuses: statuses, CloudID: cloudID}
	if rule == nil {
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
	for _, run := range runs {
		view := automationRunView{Run: run, When: run.ScheduledFor.In(time.Local).Format("2 Jan 2006, 15:04:05")}
		if run.StartedAt != nil && run.CompletedAt != nil {
			view.Duration = run.CompletedAt.Sub(*run.StartedAt).Round(time.Millisecond).String()
		}
		data.Runs = append(data.Runs, view)
	}
	data.Actions = parseAutomationActions(rule.Payload)
	return data, nil
}

func automationPayload(r *http.Request) (json.RawMessage, error) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" || len(name) > 255 {
		return nil, fmt.Errorf("rule name is required and must be at most 255 characters")
	}
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
	actionTypes := r.PostForm["action_type"]
	actionValues := r.PostForm["action_value"]
	components := make([]map[string]any, 0, len(actionTypes))
	for index, actionType := range actionTypes {
		actionType = strings.TrimSpace(actionType)
		if actionType == "" {
			continue
		}
		value := ""
		if index < len(actionValues) {
			value = strings.TrimSpace(actionValues[index])
		}
		var actionValue map[string]string
		switch actionType {
		case "jira.issue.add-label":
			actionValue = map[string]string{"label": value}
		case "jira.issue.assign":
			actionValue = map[string]string{"accountId": value}
		case "jira.issue.transition":
			actionValue = map[string]string{"statusId": value}
		default:
			return nil, fmt.Errorf("unsupported action type")
		}
		if value == "" {
			return nil, fmt.Errorf("every action needs a value")
		}
		components = append(components, map[string]any{
			"component": "ACTION", "schemaVersion": 1, "type": actionType, "value": actionValue,
		})
	}
	if len(components) == 0 {
		return nil, fmt.Errorf("add at least one action")
	}
	scope := splitLines(r.PostFormValue("scope_aris"))
	rule := map[string]any{
		"actor":               map[string]string{"actor": r.PostFormValue("actor_id"), "type": "ACCOUNT_ID"},
		"canOtherRuleTrigger": false, "collaborators": []string{}, "components": components,
		"description": strings.TrimSpace(r.PostFormValue("description")), "labels": []string{},
		"name": name, "notifyOnError": "FIRSTERROR", "ruleScopeARIs": scope,
		"state": strings.ToUpper(r.PostFormValue("state")), "writeAccessType": "OWNER_ONLY",
		"trigger": map[string]any{
			"component": "TRIGGER", "schemaVersion": 1, "type": "jira.jql.scheduled",
			"value": map[string]any{"intervalMinutes": interval, "timezone": timezone, "jql": strings.TrimSpace(r.PostFormValue("jql"))},
		},
	}
	return json.Marshal(map[string]any{"rule": rule, "connections": []any{}})
}

func parseAutomationActions(payload json.RawMessage) []automationActionView {
	var value struct {
		Components []struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		} `json:"components"`
	}
	_ = json.Unmarshal(payload, &value)
	actions := make([]automationActionView, 0, len(value.Components))
	for _, component := range value.Components {
		var fields map[string]string
		_ = json.Unmarshal(component.Value, &fields)
		selected := fields["label"]
		if component.Type == "jira.issue.assign" {
			selected = fields["accountId"]
		} else if component.Type == "jira.issue.transition" {
			selected = fields["statusId"]
		}
		actions = append(actions, automationActionView{Type: component.Type, Value: selected})
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
