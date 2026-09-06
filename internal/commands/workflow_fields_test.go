package commands

import (
	"encoding/json"
	"testing"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestChangedTransitionFieldsComparesStoredValues(t *testing.T) {
	summary, priority, assignee := "Ready", "prio_high", "usr_owner"
	description := adf.ParagraphDoc("Reviewed")
	labels := []string{"release"}
	issue := &models.Issue{
		Summary: summary, Description: description, Priority: &models.Priority{ID: priority},
		Assignee: &models.User{ID: assignee}, Labels: labels,
		Fields: map[string]json.RawMessage{"customfield_1": json.RawMessage(`{"a":1,"b":2}`)},
	}
	same := store.IssueUpdate{
		Summary: &summary, Description: adf.ParagraphDoc("Reviewed"), PriorityID: &priority,
		AssigneeID: &assignee, Labels: &labels,
		Fields: map[string]json.RawMessage{"customfield_1": json.RawMessage(`{"b":2,"a":1}`)},
	}
	if changed := changedTransitionFields(issue, same); len(changed) != 0 {
		t.Fatalf("equal values reported changed: %v", changed)
	}

	newSummary, newAssignee := "Released", ""
	newLabels := []string{"release", "verified"}
	changed := changedTransitionFields(issue, store.IssueUpdate{
		Summary: &newSummary, AssigneeID: &newAssignee, Labels: &newLabels,
		Fields: map[string]json.RawMessage{"customfield_1": json.RawMessage(`{"a":2,"b":2}`)},
	})
	for _, field := range []string{"summary", "assignee", "labels", "customfield_1"} {
		if !changed[field] {
			t.Fatalf("%s was not reported changed: %v", field, changed)
		}
	}
}
