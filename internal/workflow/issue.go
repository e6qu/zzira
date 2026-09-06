package workflow

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/models"
)

// ContextForIssue builds the shared rule input used by REST, SSR, automation,
// and command execution so transition visibility and execution cannot diverge.
func ContextForIssue(actorID string, issue *models.Issue) EvaluationContext {
	context := EvaluationContext{ActorID: actorID, FieldPresent: make(map[string]bool)}
	if issue == nil {
		return context
	}
	if issue.Assignee != nil {
		context.AssigneeID = issue.Assignee.ID
	}
	if issue.Reporter != nil {
		context.ReporterID = issue.Reporter.ID
	}
	context.FieldPresent["summary"] = strings.TrimSpace(issue.Summary) != ""
	context.FieldPresent["description"] = strings.TrimSpace(adf.PlainText(issue.Description)) != ""
	context.FieldPresent["assignee"] = issue.Assignee != nil
	context.FieldPresent["reporter"] = issue.Reporter != nil
	context.FieldPresent["priority"] = issue.Priority != nil
	context.FieldPresent["labels"] = len(issue.Labels) > 0
	for field, value := range issue.Fields {
		context.FieldPresent[field] = jsonValuePresent(value)
	}
	return context
}

func jsonValuePresent(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte(`""`)) || bytes.Equal(trimmed, []byte("[]")) || bytes.Equal(trimmed, []byte("{}")) {
		return false
	}
	return true
}
