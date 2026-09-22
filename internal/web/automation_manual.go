package web

import (
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/automation"
)

// A manual rule is one somebody runs themselves, from the work item they are
// looking at. Jira puts them in the work item's own actions, which is where
// they are here: the rules that cover this project, the questions each asks,
// and what happened when one ran.

// manualRulePageSize is the largest page the rule listing accepts.
const manualRulePageSize = 100

// manualRulesData is the Run automation control's content: the rules that
// apply to one work item, with the questions each asks.
type manualRulesData struct {
	IssueKey string
	Rules    []manualRuleView
	// Ran and Error are what the last run of one of them did, shown where it
	// was started rather than on a page of its own.
	Ran   string
	Error string
}

type manualRuleView struct {
	UUID, Name string
	Prompts    []manualPromptView
}

type manualPromptView struct {
	DisplayName, VariableName, InputType string
	Required                             bool
}

// manualRulesFor reads the manual rules whoever is looking at this work item
// can run on it: enabled, manually triggered, and covering its project.
func (h *Handler) manualRulesFor(r *http.Request, workspaceID, projectID string) ([]manualRuleView, error) {
	if h.Automation == nil {
		return nil, nil
	}
	views := []manualRuleView{}
	// A page is capped at a hundred rules, so read every page: a workspace
	// with more manual rules than that still shows the ones it can run here.
	for cursor := ""; ; {
		page, err := h.Automation.Rules(r.Context(), workspaceID, automation.SummaryFilter{
			States: []string{"ENABLED"}, Triggers: []string{automation.ManualTriggerType}, Limit: manualRulePageSize, Cursor: cursor,
		})
		if err != nil {
			return nil, err
		}
		for _, rule := range page.Rules {
			applies, err := h.Automation.AppliesToProject(r.Context(), workspaceID, rule, projectID)
			if err != nil {
				return nil, err
			}
			if !applies {
				continue
			}
			view := manualRuleView{UUID: rule.UUID, Name: rule.Name}
			for _, prompt := range h.Automation.ManualPrompts(rule) {
				name, _ := prompt["displayName"].(string)
				variable, _ := prompt["variableName"].(string)
				inputType, _ := prompt["inputType"].(string)
				view.Prompts = append(view.Prompts, manualPromptView{
					DisplayName: name, VariableName: variable, InputType: inputType, Required: prompt["required"] == true,
				})
			}
			views = append(views, view)
		}
		if page.NextCursor == "" {
			return views, nil
		}
		cursor = page.NextCursor
	}
}

// ManualRules answers with the Run automation control for one work item. It
// is fetched when the control is opened rather than with the page: a work item
// is read far more often than a rule is run on it.
func (h *Handler) ManualRules(w http.ResponseWriter, r *http.Request, key string) {
	user, workspaceID, ok := h.issueMutationContext(w, r, key)
	if !ok {
		return
	}
	issue, err := h.issueForUser(r, user, workspaceID, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rules, err := h.manualRulesFor(r, workspaceID, issue.ProjectID)
	if err != nil {
		log.Print("automation: manual rule lookup failed: ", strconv.Quote(err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeFragment(w, "manual_rules", manualRulesData{IssueKey: issue.Key, Rules: rules})
}

// RunManualRule runs one manual rule on one work item, as the person looking
// at it asked. The work item is re-rendered afterwards, because the rule has
// just changed it.
func (h *Handler) RunManualRule(w http.ResponseWriter, r *http.Request, key, uuid string) {
	user, workspaceID, ok := h.issueMutationContext(w, r, key)
	if !ok || !parseForm(w, r) {
		return
	}
	issue, err := h.issueForUser(r, user, workspaceID, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rule, err := h.Automation.Rule(r.Context(), workspaceID, uuid)
	if err != nil || !automation.IsManualRule(rule) {
		http.Error(w, "that rule is not one to run from a work item", http.StatusNotFound)
		return
	}
	applies, err := h.Automation.AppliesToProject(r.Context(), workspaceID, rule, issue.ProjectID)
	if err != nil {
		log.Print("automation: manual rule scope lookup failed: ", strconv.Quote(err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !applies {
		http.Error(w, "that rule does not cover this project", http.StatusForbidden)
		return
	}
	inputs := map[string]string{}
	for _, prompt := range h.Automation.ManualPrompts(rule) {
		name, _ := prompt["variableName"].(string)
		inputs[name] = strings.TrimSpace(r.PostFormValue("input_" + name))
	}
	if missing := automation.MissingManualInput(rule, inputs); missing != "" {
		h.manualRulesAnswer(w, r, workspaceID, issue.Key, issue.ProjectID, "", "Answer "+missing+" before running this rule.")
		return
	}
	if err := h.Automation.RunManualRule(r.Context(), workspaceID, user.ID, rule, issue, inputs); err != nil {
		h.manualRulesAnswer(w, r, workspaceID, issue.Key, issue.ProjectID, "", rule.Name+" failed: "+err.Error())
		return
	}
	h.serveIssue(w, r, user, workspaceID, key)
}

// manualRulesAnswer re-renders the control with what happened, so a refused or
// failed run says so where it was started.
func (h *Handler) manualRulesAnswer(w http.ResponseWriter, r *http.Request, workspaceID, key, projectID, ran, failure string) {
	rules, err := h.manualRulesFor(r, workspaceID, projectID)
	if err != nil {
		log.Print("automation: manual rule lookup failed: ", strconv.Quote(err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// The form re-renders the whole work item when a rule runs, because the
	// rule has just changed it. A refused run changed nothing, so it answers
	// the control alone, where the person is looking.
	w.Header().Set("HX-Retarget", ".issue-automation-rules")
	w.Header().Set("HX-Reswap", "innerHTML")
	status := http.StatusOK
	if failure != "" {
		status = http.StatusBadRequest
	}
	writeFragmentStatus(w, "manual_rules", manualRulesData{IssueKey: key, Rules: rules, Ran: ran, Error: failure}, status)
}

// bulkManualRuleLimit is how many work items one run covers. It is the limit
// the Automation API's own bulk invocation takes, and this runs the same rule
// the same way, so it takes the same number.
const bulkManualRuleLimit = 50

// SubmitBulkIssueAutomation runs one manual rule over the work items somebody
// selected in the navigator. Jira offers this beside the other bulk actions;
// here it is the same run as the work item's own Run automation control, one
// item at a time, so a rule that is refused on one item does not stop the rest.
func (h *Handler) SubmitBulkIssueAutomation(w http.ResponseWriter, r *http.Request, projectKey string) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	if h.Automation == nil {
		http.Error(w, "automation is not available on this site", http.StatusNotFound)
		return
	}
	project, err := h.Store.ProjectByKey(r.Context(), workspaceID, projectKey)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	issues, ok := h.bulkSelection(w, r, user, workspaceID, project.ID)
	if !ok {
		return
	}
	if len(issues) > bulkManualRuleLimit {
		http.Error(w, "a rule runs over at most "+strconv.Itoa(bulkManualRuleLimit)+" work items at once", http.StatusBadRequest)
		return
	}
	rule, err := h.Automation.Rule(r.Context(), workspaceID, strings.TrimSpace(r.PostFormValue("rule")))
	if err != nil || !automation.IsManualRule(rule) {
		http.Error(w, "that rule is not one to run from a work item", http.StatusNotFound)
		return
	}
	applies, err := h.Automation.AppliesToProject(r.Context(), workspaceID, rule, project.ID)
	if err != nil {
		log.Print("automation: bulk rule scope lookup failed: ", strconv.Quote(err.Error()))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !applies {
		http.Error(w, "that rule does not cover this project", http.StatusForbidden)
		return
	}
	inputs := map[string]string{}
	for _, prompt := range h.Automation.ManualPrompts(rule) {
		name, _ := prompt["variableName"].(string)
		inputs[name] = strings.TrimSpace(r.PostFormValue("input_" + name))
	}
	if missing := automation.MissingManualInput(rule, inputs); missing != "" {
		http.Error(w, "Answer "+missing+" before running this rule.", http.StatusBadRequest)
		return
	}
	ran, failed := 0, []string{}
	for _, issue := range issues {
		if err := h.Automation.RunManualRule(r.Context(), workspaceID, user.ID, rule, issue, inputs); err != nil {
			failed = append(failed, issue.Key)
			continue
		}
		ran++
	}
	notice := rule.Name + " ran on " + strconv.Itoa(ran) + " of " + strconv.Itoa(len(issues)) + " work items."
	if len(failed) > 0 {
		notice += " It failed on " + strings.Join(failed, ", ") + "."
	}
	redirectLocal(w, r, "/issues/"+url.PathEscape(project.Key)+"?notice="+url.QueryEscape(notice))
}
