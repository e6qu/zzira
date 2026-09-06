package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/models"
)

// ContextForIssue builds the shared rule input used by REST, SSR, automation,
// and command execution so transition visibility and execution cannot diverge.
func ContextForIssue(actorID string, issue *models.Issue) EvaluationContext {
	context := EvaluationContext{ActorID: actorID, FieldPresent: make(map[string]bool), FieldValues: make(map[string]json.RawMessage)}
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
	setFieldString(context.FieldValues, "summary", issue.Summary)
	setFieldString(context.FieldValues, "description", adf.PlainText(issue.Description))
	setFieldString(context.FieldValues, "status", issue.Status.ID)
	if issue.Assignee != nil {
		setFieldString(context.FieldValues, "assignee", issue.Assignee.ID)
	}
	if issue.Reporter != nil {
		setFieldString(context.FieldValues, "reporter", issue.Reporter.ID)
	}
	if issue.Priority != nil {
		setFieldString(context.FieldValues, "priority", issue.Priority.ID)
	}
	if encoded, err := json.Marshal(issue.Labels); err == nil {
		context.FieldValues["labels"] = encoded
	}
	for field, value := range issue.Fields {
		context.FieldPresent[field] = jsonValuePresent(value)
		context.FieldValues[field] = append(json.RawMessage(nil), value...)
	}
	return context
}

func setFieldString(values map[string]json.RawMessage, field, value string) {
	if encoded, err := json.Marshal(value); err == nil {
		values[field] = encoded
	}
}

func jsonValuePresent(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte(`""`)) || bytes.Equal(trimmed, []byte("[]")) || bytes.Equal(trimmed, []byte("{}")) {
		return false
	}
	return true
}

func FieldValuePresent(value json.RawMessage) bool {
	return jsonValuePresent(value)
}

func comparisonValues(value string) ([]any, bool) {
	var values []any
	if err := json.Unmarshal([]byte(value), &values); err != nil || len(values) == 0 {
		return nil, false
	}
	return values, true
}

func checkFieldValue(parameters map[string]string, context EvaluationContext) bool {
	actual, exists := context.FieldValues[parameters["fieldId"]]
	if !exists || !jsonValuePresent(actual) {
		return false
	}
	expected, ok := comparisonValues(parameters["fieldValue"])
	if !ok {
		return false
	}
	actualValues := comparableJSONValues(actual)
	if len(actualValues) == 0 {
		return false
	}
	if parameters["comparator"] == "!=" {
		for _, left := range actualValues {
			for _, right := range expected {
				if compareWorkflowValues(left, right, "=", parameters["comparisonType"]) {
					return false
				}
			}
		}
		return true
	}
	for _, left := range actualValues {
		for _, right := range expected {
			if compareWorkflowValues(left, right, parameters["comparator"], parameters["comparisonType"]) {
				return true
			}
		}
	}
	return false
}

func comparableJSONValues(raw json.RawMessage) []any {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	if values, ok := value.([]any); ok {
		return values
	}
	return []any{value}
}

func workflowScalar(value any, comparisonType string) string {
	if object, ok := value.(map[string]any); ok {
		keys := []string{"value", "name", "key", "accountId", "id"}
		if comparisonType == "OPTIONID" {
			keys = []string{"id", "value", "key", "accountId", "name"}
		}
		for _, key := range keys {
			if candidate, exists := object[key]; exists {
				return workflowScalar(candidate, comparisonType)
			}
		}
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func compareWorkflowValues(left, right any, comparator, comparisonType string) bool {
	leftValue, rightValue := workflowScalar(left, comparisonType), workflowScalar(right, comparisonType)
	var comparison int
	switch comparisonType {
	case "NUMBER":
		leftNumber, leftErr := strconv.ParseFloat(leftValue, 64)
		rightNumber, rightErr := strconv.ParseFloat(rightValue, 64)
		if leftErr != nil || rightErr != nil {
			return false
		}
		switch {
		case leftNumber < rightNumber:
			comparison = -1
		case leftNumber > rightNumber:
			comparison = 1
		}
	case "DATE", "DATE_WITHOUT_TIME":
		layout := time.RFC3339
		if comparisonType == "DATE_WITHOUT_TIME" {
			layout = "2006-01-02"
		}
		leftDate, leftErr := time.Parse(layout, leftValue)
		rightDate, rightErr := time.Parse(layout, rightValue)
		if leftErr != nil || rightErr != nil {
			return false
		}
		switch {
		case leftDate.Before(rightDate):
			comparison = -1
		case leftDate.After(rightDate):
			comparison = 1
		}
	default:
		comparison = strings.Compare(leftValue, rightValue)
	}
	switch comparator {
	case "=":
		return comparison == 0
	case "!=":
		return comparison != 0
	case ">":
		return comparison > 0
	case ">=":
		return comparison >= 0
	case "<":
		return comparison < 0
	case "<=":
		return comparison <= 0
	}
	return false
}
