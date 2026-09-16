package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

var smartValuePattern = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z.]*)\s*\}\}`)

// renderSmartValues replaces Jira Automation smart values in an action's text
// with the work item's, the event initiator's and the rule's values. Unknown
// smart values render empty, as in Jira.
func (r *Runner) renderSmartValues(ctx context.Context, run *claimedRun, issue *models.Issue, text string) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	var initiator *models.User
	if run.InitiatorID != "" {
		if member, err := r.Service.Store.MemberByID(ctx, run.WorkspaceID, run.InitiatorID); err == nil {
			initiator = member
		}
	}
	now := time.Now().UTC()
	person := func(user *models.User, field string) string {
		if user == nil {
			return ""
		}
		if field == "accountId" {
			return user.ID
		}
		return user.DisplayName
	}
	values := map[string]string{
		"issue.key": issue.Key, "issue.summary": issue.Summary, "issue.status.name": issue.Status.Name,
		"issue.issueType.name": issue.IssueType.Name, "issue.dueDate": issue.DueDate, "issue.labels": strings.Join(issue.Labels, ", "),
		"issue.assignee.displayName": person(issue.Assignee, "displayName"), "issue.assignee.accountId": person(issue.Assignee, "accountId"),
		"issue.reporter.displayName": person(issue.Reporter, "displayName"), "issue.reporter.accountId": person(issue.Reporter, "accountId"),
		"initiator.displayName": person(initiator, "displayName"), "initiator.accountId": person(initiator, "accountId"),
		"rule.name": run.RuleName, "now": now.Format(time.RFC3339), "now.jiraDate": now.Format("2006-01-02"),
	}
	if issue.Priority != nil {
		values["issue.priority.name"] = issue.Priority.Name
	}
	trigger := run.TriggerIssue
	if trigger == nil {
		trigger = issue
	}
	values["triggerIssue.key"], values["triggerIssue.summary"] = trigger.Key, trigger.Summary
	if len(text) > 32768 {
		return "", errors.New("action text is longer than 32768 characters")
	}
	rendered := smartValuePattern.ReplaceAllStringFunc(text, func(match string) string {
		name := smartValuePattern.FindStringSubmatch(match)[1]
		return values[name]
	})
	return rendered, nil
}

// conditionFields are the work item fields a fields condition compares.
var conditionFields = map[string]bool{"status": true, "priority": true, "issuetype": true, "assignee": true, "reporter": true, "labels": true, "summary": true, "duedate": true,
	"resolution": true, "created": true, "resolved": true, "parent": true, "key": true}

// conditionDateFields hold an ISO day or time, which is what an ordering
// comparison reads. Ordering any other field holds for nothing.
var conditionDateFields = map[string]bool{"duedate": true, "created": true, "resolved": true}

// conditionOperators are the comparisons a fields condition makes.
var conditionOperators = map[string]bool{"EQUALS": true, "NOT_EQUALS": true, "CONTAINS": true, "NOT_CONTAINS": true,
	"STARTS_WITH": true, "ENDS_WITH": true, "IS_ONE_OF": true, "IS_NOT_ONE_OF": true,
	"GREATER_THAN": true, "LESS_THAN": true, "IS_EMPTY": true, "IS_NOT_EMPTY": true}

// condition reports whether a rule's condition holds for a work item.
func (r *Runner) condition(ctx context.Context, run *claimedRun, issue *models.Issue, item component) (bool, error) {
	raw := decodeComponentValue(item.Value)
	switch item.Type {
	case "jira.jql.condition":
		var value struct {
			JQL string `json:"jql"`
		}
		if err := jsonUnmarshal(raw, &value); err != nil || strings.TrimSpace(value.JQL) == "" {
			return false, errors.New("JQL condition requires value.jql")
		}
		return r.matchesJQL(ctx, run, issue, value.JQL)
	case "jira.issue.related.condition":
		var value struct {
			RelatedType string   `json:"relatedType"`
			LinkTypes   []string `json:"linkTypes"`
			JQL         string   `json:"jql"`
		}
		if err := jsonUnmarshal(raw, &value); err != nil || strings.TrimSpace(value.RelatedType) == "" {
			return false, errors.New("related work items condition requires value.relatedType")
		}
		// The branch resolver already finds a work item's related work, and a
		// condition is handed the same run and work item, so it asks the same
		// question without traversing links again.
		related, err := r.relatedIssues(ctx, run, issue, item)
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(value.JQL) == "" {
			return len(related) > 0, nil
		}
		for _, other := range related {
			matches, err := r.matchesJQL(ctx, run, other, value.JQL)
			if err != nil {
				return false, err
			}
			if matches {
				return true, nil
			}
		}
		return false, nil
	case "jira.issue.condition":
		var value struct {
			Field, Operator, Value string
		}
		if err := jsonUnmarshal(raw, &value); err != nil || !conditionFields[value.Field] || !conditionOperators[value.Operator] {
			return false, errors.New("fields condition requires a supported value.field and value.operator")
		}
		expected, err := r.renderSmartValues(ctx, run, issue, value.Value)
		if err != nil {
			return false, err
		}
		return fieldConditionHolds(issue, value.Field, value.Operator, expected), nil
	}
	return false, fmt.Errorf("unsupported condition %q", item.Type)
}

// fieldConditionHolds compares a work item's field with a value, ignoring
// case. People compare by account ID or display name, and labels by any label.
func fieldConditionHolds(issue *models.Issue, field, operator, expected string) bool {
	var actual []string
	switch field {
	case "status":
		actual = []string{issue.Status.Name, issue.Status.ID}
	case "priority":
		if issue.Priority != nil {
			actual = []string{issue.Priority.Name, issue.Priority.ID}
		}
	case "issuetype":
		actual = []string{issue.IssueType.Name, issue.IssueType.ID}
	case "assignee", "reporter":
		user := issue.Assignee
		if field == "reporter" {
			user = issue.Reporter
		}
		if user != nil {
			actual = []string{user.DisplayName, user.ID}
		}
	case "labels":
		actual = issue.Labels
	case "summary":
		actual = []string{issue.Summary}
	case "duedate":
		if issue.DueDate != "" {
			actual = []string{issue.DueDate}
		}
	case "resolution":
		if issue.Resolution != nil {
			actual = []string{issue.Resolution.Name, issue.Resolution.ID}
		}
	case "resolved":
		if issue.ResolvedAt != "" {
			actual = []string{issue.ResolvedAt}
		}
	case "created":
		if issue.CreatedAt != "" {
			actual = []string{issue.CreatedAt}
		}
	case "parent":
		if issue.Parent != nil {
			actual = []string{issue.Parent.Key, issue.Parent.Summary, issue.Parent.ID}
		}
	case "key":
		actual = []string{issue.Key}
	}
	present := len(actual) > 0 && actual[0] != ""
	expected = strings.ToLower(strings.TrimSpace(expected))
	matches := func(compare func(string) bool) bool {
		for _, value := range actual {
			if value != "" && compare(strings.ToLower(value)) {
				return true
			}
		}
		return false
	}
	switch operator {
	case "IS_EMPTY":
		return !present
	case "IS_NOT_EMPTY":
		return present
	case "EQUALS":
		return matches(func(value string) bool { return value == expected })
	case "NOT_EQUALS":
		return !matches(func(value string) bool { return value == expected })
	case "CONTAINS":
		return matches(func(value string) bool { return strings.Contains(value, expected) })
	case "NOT_CONTAINS":
		return !matches(func(value string) bool { return strings.Contains(value, expected) })
	case "STARTS_WITH":
		return matches(func(value string) bool { return strings.HasPrefix(value, expected) })
	case "ENDS_WITH":
		return matches(func(value string) bool { return strings.HasSuffix(value, expected) })
	case "IS_ONE_OF", "IS_NOT_ONE_OF":
		listed := matches(func(value string) bool {
			for _, want := range strings.Split(expected, ",") {
				if want = strings.TrimSpace(want); want != "" && value == want {
					return true
				}
			}
			return false
		})
		return listed == (operator == "IS_ONE_OF")
	case "GREATER_THAN", "LESS_THAN":
		// Ordering reads the ISO day or time a date field holds. Comparing a
		// day with a time compares the days they fall on.
		if !conditionDateFields[field] {
			return false
		}
		return matches(func(value string) bool {
			left, right := value, expected
			if len(right) == 10 && len(left) > 10 {
				left = left[:10]
			}
			if len(left) == 10 && len(right) > 10 {
				right = right[:10]
			}
			if left == "" || right == "" {
				return false
			}
			if operator == "GREATER_THAN" {
				return left > right
			}
			return left < right
		})
	}
	return false
}

func jsonUnmarshal(raw []byte, value any) error {
	return json.Unmarshal(raw, value)
}
