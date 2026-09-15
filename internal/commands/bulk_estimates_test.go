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
