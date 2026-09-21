package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// A rule can name a value of its own and read it back later: Jira calls it a
// created variable, and it is how a rule carries what a web request answered,
// or what a branch found, into the actions that come after.

// VariableActionType is the action that names a value for the rest of the rule.
const VariableActionType = "jira.create.variable"

// variableActionValue is what the action carries: what the variable is called,
// and the text it holds, which is rendered before it is stored so a variable
// can be built out of other smart values.
type variableActionValue struct {
	VariableName  string `json:"variableName"`
	VariableValue string `json:"variableValue"`
}

// variableNamePattern is what a variable may be called. It becomes the smart
// value {{name}}, so it is a plain identifier.
var variableNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

// reservedVariableNames are the smart values a rule already has. A variable
// called one of them would be read as the built-in or shadow it depending on
// where it was set, so the rule is refused instead: a rule that silently means
// something other than what it says is worse than one that will not save.
var reservedVariableNames = map[string]bool{
	"issue": true, "triggerIssue": true, "initiator": true, "rule": true, "now": true,
	"webResponse": true, "webhookData": true, "userInputs": true,
	"version": true, "sprint": true, "deletedIssue": true,
}

// maxVariables is how many variables one run holds. A rule that names more
// than this is building a data structure, which is not what these are for.
const maxVariables = 50

// maxVariableLength bounds one variable, as an action's text is bounded.
const maxVariableLength = 32768

// validateVariableAction reads and checks a create variable action, so a rule
// that cannot work says so when it is written rather than when it runs.
func validateVariableAction(raw json.RawMessage) (variableActionValue, error) {
	var value variableActionValue
	if err := json.Unmarshal(decodeComponentValue(raw), &value); err != nil {
		return value, errors.New("a create variable action takes variableName and variableValue")
	}
	value.VariableName = strings.TrimSpace(value.VariableName)
	if !variableNamePattern.MatchString(value.VariableName) {
		return value, errors.New("a variable is named like reason or release_note: a letter, then letters, digits, _ or -")
	}
	if reservedVariableNames[value.VariableName] {
		return value, fmt.Errorf("%s is a smart value the rule already has, so a variable cannot be called that", value.VariableName)
	}
	return value, nil
}

// setVariable stores what a create variable action named, for the rest of the
// rule to read as {{name}}.
func (r *Runner) setVariable(run *claimedRun, rendered string, value variableActionValue) error {
	if len(rendered) > maxVariableLength {
		return fmt.Errorf("%s is longer than %d characters", value.VariableName, maxVariableLength)
	}
	if run.Variables == nil {
		run.Variables = map[string]string{}
	}
	// Setting a variable again replaces it, as Jira's does, so only a new
	// name counts against the limit.
	if _, held := run.Variables[value.VariableName]; !held && len(run.Variables) >= maxVariables {
		return fmt.Errorf("a rule holds at most %d variables", maxVariables)
	}
	run.Variables[value.VariableName] = rendered
	return nil
}
