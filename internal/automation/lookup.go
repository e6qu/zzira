package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// Jira's lookup issues action: a rule asks a question of the site and keeps
// the answer, which the rest of the rule reads as {{lookupIssues}} -- how
// many there are, and, branched over, each of them.

// LookupActionType is the action that runs a query and keeps what it found.
const LookupActionType = "jira.issue.lookup"

// LookupVariable is what the answer is called, as Jira calls it.
const LookupVariable = "lookupIssues"

// maxLookupIssues bounds what one lookup keeps, as a branch is bounded.
const maxLookupIssues = 100

// lookupActionValue is the query to run and how much of it to keep.
type lookupActionValue struct {
	JQL   string `json:"jql"`
	Limit int    `json:"limit"`
}

// lookedUpIssue is one work item as the rest of the rule reads it: enough to
// name it, act on it in a branch, and say something about it.
type lookedUpIssue struct {
	Key      string `json:"key"`
	ID       string `json:"id"`
	Summary  string `json:"summary"`
	Status   string `json:"status"`
	Assignee string `json:"assignee,omitempty"`
	Project  string `json:"project"`
}

// validateLookupAction reads and checks a lookup action.
func validateLookupAction(raw json.RawMessage) (lookupActionValue, error) {
	var value lookupActionValue
	if err := json.Unmarshal(decodeComponentValue(raw), &value); err != nil {
		return value, errors.New("a lookup work items action takes jql and an optional limit")
	}
	if strings.TrimSpace(value.JQL) == "" {
		return value, errors.New("a lookup work items action needs the query it runs")
	}
	if value.Limit < 0 || value.Limit > maxLookupIssues {
		return value, fmt.Errorf("a lookup keeps between 1 and %d work items", maxLookupIssues)
	}
	return value, nil
}

// lookupIssues runs the action's query as the rule actor and keeps what it
// found for the rest of the rule. Finding nothing is an answer: the variable
// is an empty list rather than missing, so {{lookupIssues.size}} reads 0 and a
// branch over it runs for nothing.
func (r *Runner) lookupIssues(ctx context.Context, run *claimedRun, issue *models.Issue, value lookupActionValue, render func(string) (string, error)) (bool, error) {
	query, err := render(value.JQL)
	if err != nil {
		return false, err
	}
	limit := value.Limit
	if limit == 0 {
		limit = maxLookupIssues
	}
	found, _, err := r.searchIssues(ctx, run, query, limit)
	if err != nil {
		return false, err
	}
	kept := make([]lookedUpIssue, 0, len(found))
	for _, item := range found {
		view := lookedUpIssue{Key: item.Key, ID: item.ID, Summary: item.Summary, Status: item.Status.Name, Project: item.ProjectID}
		if item.Assignee != nil {
			view.Assignee = item.Assignee.DisplayName
		}
		kept = append(kept, view)
	}
	data, err := json.Marshal(kept)
	if err != nil {
		return false, err
	}
	if run.Variables == nil {
		run.Variables = map[string]string{}
	}
	if run.VariableData == nil {
		run.VariableData = map[string]json.RawMessage{}
	}
	// Read as text, a lookup is the work it found, as Jira reads one:
	// "ZZ-3, ZZ-7". The JSON behind it is what a branch runs over and what
	// {{lookupIssues.size}} and {{lookupIssues.0.summary}} read.
	keys := make([]string, 0, len(kept))
	for _, item := range kept {
		keys = append(keys, item.Key)
	}
	run.Variables[LookupVariable] = strings.Join(keys, ", ")
	run.VariableData[LookupVariable] = data
	// Reading changes nothing about the work: a rule whose only action is a
	// lookup did nothing, and its run says so.
	return false, nil
}
