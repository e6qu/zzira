package commands

import (
	"encoding/json"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestBulkIssueUpdateSetsEstimates(t *testing.T) {
	issue := &models.Issue{ID: "iss_1", WorkspaceID: "ws_1"}
	update, err := bulkIssueUpdate(issue, "usr_1", []store.BulkIssueEditOperation{
		{FieldID: "timeoriginalestimate", Action: "SET", Value: json.RawMessage(`7200`)},
		{FieldID: "timeestimate", Action: "SET", Value: json.RawMessage(`1800`)},
	})
	if err != nil || update.OriginalEstimate == nil || *update.OriginalEstimate != 7200 || update.RemainingEstimate == nil || *update.RemainingEstimate != 1800 {
		t.Fatalf("update = %+v err=%v", update, err)
	}
	if _, err := bulkIssueUpdate(issue, "usr_1", []store.BulkIssueEditOperation{{FieldID: "timeestimate", Action: "SET", Value: json.RawMessage(`"2h"`)}}); err == nil {
		t.Fatal("an unconverted duration was accepted")
	}
}

func TestBulkIssueUpdateSetsDueDate(t *testing.T) {
	issue := &models.Issue{ID: "iss_1", WorkspaceID: "ws_1", DueDate: "2026-09-01"}
	update, err := bulkIssueUpdate(issue, "usr_1", []store.BulkIssueEditOperation{{FieldID: "duedate", Action: "SET", Value: json.RawMessage(`"2026-12-24"`)}})
	if err != nil || update.DueDate == nil || *update.DueDate != "2026-12-24" || update.Fields != nil {
		t.Fatalf("update = %+v err=%v", update, err)
	}
	cleared, err := bulkIssueUpdate(issue, "usr_1", []store.BulkIssueEditOperation{{FieldID: "duedate", Action: "SET", Value: json.RawMessage(`""`)}})
	if err != nil || cleared.DueDate == nil || *cleared.DueDate != "" {
		t.Fatalf("cleared = %+v err=%v", cleared, err)
	}
	if _, err := normalizeDueDate("24/12/2026"); err == nil {
		t.Fatal("an unformatted due date was accepted")
	}
}
