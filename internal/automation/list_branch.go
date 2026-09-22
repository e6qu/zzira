package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// Jira calls it advanced branching: a rule runs the same actions once for
// each item in a list it already has -- what a web request answered, the
// labels on the work item, a variable it built itself -- rather than once for
// each related work item.

// listBranchType is the relatedType of a branch over a list.
const listBranchType = "smart-values"

// listItem is one item of what a branch is running over: the text the actions
// read as {{item}}, and, when the item is an object or a list of its own, the
// JSON behind it so {{item.name}} reads that too.
type listItem struct {
	Text string
	Data json.RawMessage
}

// branchList renders what a branch runs over and reads it as a list. A JSON
// array is its elements; anything else is read as Jira reads a rendered list,
// which is comma-separated -- {{issue.labels}} and the rest render that way.
func branchListItems(rendered string) []listItem {
	rendered = strings.TrimSpace(rendered)
	if rendered == "" {
		return nil
	}
	var array []json.RawMessage
	if strings.HasPrefix(rendered, "[") && json.Unmarshal([]byte(rendered), &array) == nil {
		items := make([]listItem, 0, len(array))
		for _, element := range array {
			items = append(items, listItemOf(element))
		}
		return items
	}
	items := []listItem{}
	for _, part := range strings.Split(rendered, ",") {
		if part = strings.TrimSpace(part); part != "" {
			items = append(items, listItem{Text: part})
		}
	}
	return items
}

// listItemOf reads one element of a JSON array: a string is its text, an
// object or an array keeps its JSON so paths into it can be read, and a
// number or a boolean is written as it appears.
func listItemOf(element json.RawMessage) listItem {
	var value any
	if json.Unmarshal(element, &value) != nil {
		return listItem{Text: strings.TrimSpace(string(element))}
	}
	switch typed := value.(type) {
	case string:
		return listItem{Text: typed}
	case float64:
		return listItem{Text: strconv.FormatFloat(typed, 'f', -1, 64)}
	case bool:
		return listItem{Text: strconv.FormatBool(typed)}
	case nil:
		return listItem{Text: ""}
	default:
		return listItem{Text: strings.TrimSpace(string(element)), Data: element}
	}
}

// validateListBranch checks a branch over a list can work before it runs: it
// needs something to run over and a name to call each item.
func validateListBranch(value branchValue) error {
	if strings.TrimSpace(value.SmartValue) == "" {
		return errors.New("a branch over a list needs the smart value holding it")
	}
	name := strings.TrimSpace(value.VariableName)
	if !variableNamePattern.MatchString(name) {
		return errors.New("a branch over a list names each item like item or release: a letter, then letters, digits, _ or -")
	}
	if reservedVariableNames[name] {
		return fmt.Errorf("%s is a smart value the rule already has, so an item cannot be called that", name)
	}
	return nil
}

// runListBranch runs a branch's children once for each item in the list,
// with the item named as the branch asked. The work item does not change:
// this branch is over values, not over work.
func (r *Runner) runListBranch(ctx context.Context, run *claimedRun, issue *models.Issue, value branchValue, children []component) (bool, error) {
	items, err := r.branchList(ctx, run, issue, value.SmartValue)
	if err != nil {
		return false, err
	}
	if len(items) > maxBranchIssues {
		items = items[:maxBranchIssues]
	}
	name := strings.TrimSpace(value.VariableName)
	changed := false
	// What the branch names belongs to the branch, as it does for a branch
	// over work: each item starts from what the rule had named outside it.
	outer, outerData := run.Variables, run.VariableData
	defer func() { run.Variables, run.VariableData = outer, outerData }()
	for _, item := range items {
		run.Variables, run.VariableData = cloneVariables(outer), cloneVariableData(outerData)
		if run.Variables == nil {
			run.Variables = map[string]string{}
		}
		run.Variables[name] = item.Text
		if len(item.Data) > 0 {
			if run.VariableData == nil {
				run.VariableData = map[string]json.RawMessage{}
			}
			run.VariableData[name] = item.Data
		}
		// An item that names a work item the rule actor can see becomes the
		// work item the branch runs for: branching over what a lookup found
		// is how a rule acts on each of them, and {{issue.key}} inside the
		// branch is that work item. An item that names no work item leaves
		// the work item as it was, because the branch is over values.
		branchIssue := issue
		if found, err := r.listItemIssue(ctx, run, item); err != nil {
			return changed, err
		} else if found != nil {
			branchIssue = found
		}
		didChange, err := r.runComponents(ctx, run, branchIssue, children)
		if err != nil {
			return changed, err
		}
		changed = changed || didChange
	}
	return changed, nil
}

// branchList is what a branch runs over. A branch over a variable that holds
// a list reads that list itself, so what the rule kept -- a lookup's work
// items, a web response's array -- is read as the objects it is rather than
// as the text it renders to.
func (r *Runner) branchList(ctx context.Context, run *claimedRun, issue *models.Issue, smartValue string) ([]listItem, error) {
	if name, found := wholeSmartValue(smartValue); found {
		if data, held := run.VariableData[name]; held {
			return branchListItems(string(data)), nil
		}
	}
	rendered, err := r.renderSmartValues(ctx, run, issue, smartValue)
	if err != nil {
		return nil, err
	}
	return branchListItems(rendered), nil
}

// wholeSmartValue reads a smart value that is one name and nothing else, as
// {{lookupIssues}} is, and answers what it names.
func wholeSmartValue(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{{") || !strings.HasSuffix(trimmed, "}}") {
		return "", false
	}
	name := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
	if name == "" || strings.Contains(name, "{{") || strings.ContainsAny(name, " \t") {
		return "", false
	}
	return name, true
}

// listItemIssue reads the work item a list item names, when it names one the
// rule actor can see. A list of anything else -- labels, names, numbers --
// names no work item and answers nothing.
func (r *Runner) listItemIssue(ctx context.Context, run *claimedRun, item listItem) (*models.Issue, error) {
	if len(item.Data) == 0 {
		return nil, nil
	}
	var named struct {
		Key string `json:"key"`
		ID  string `json:"id"`
	}
	if json.Unmarshal(item.Data, &named) != nil {
		return nil, nil
	}
	reference := named.ID
	if reference == "" {
		reference = named.Key
	}
	if reference == "" {
		return nil, nil
	}
	issue, err := r.Service.Store.IssueByIDOrKey(ctx, run.WorkspaceID, reference)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// The list may have come from anywhere -- a web response, a variable the
	// rule built -- so what it names is checked against the actor, not
	// trusted because it was in the list.
	visible, err := authz.CanSeeIssue(ctx, r.Service.Store, run.WorkspaceID, issue.ProjectID, run.ActorID, issue.ID, issue.SecurityLevelID)
	if err != nil || !visible {
		return nil, err
	}
	return issue, nil
}

// cloneVariableData copies the JSON behind a run's variables, for a branch to
// add to without the next item inheriting it.
func cloneVariableData(data map[string]json.RawMessage) map[string]json.RawMessage {
	if data == nil {
		return nil
	}
	copied := make(map[string]json.RawMessage, len(data))
	for name, value := range data {
		copied[name] = value
	}
	return copied
}
