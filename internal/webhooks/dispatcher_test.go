package webhooks

import (
	"encoding/json"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestActionKeyUsesPublicIssueKey(t *testing.T) {
	payload, err := json.Marshal(models.IssueUpdatePayload{Issue: models.Issue{ID: "iss_internal", Key: "OPS-42"}})
	if err != nil {
		t.Fatal(err)
	}
	action := &models.Action{EntityType: models.EntityIssue, EntityID: "iss_internal", Payload: payload}
	if got := actionKey(action); got != "OPS-42" {
		t.Fatalf("actionKey()=%q, want OPS-42", got)
	}
}

func TestActionKeyFallsBackForLegacyPayload(t *testing.T) {
	action := &models.Action{EntityType: models.EntityIssue, EntityID: "OPS-7", Payload: json.RawMessage(`{"legacy":true}`)}
	if got := actionKey(action); got != "OPS-7" {
		t.Fatalf("actionKey()=%q, want OPS-7", got)
	}
}
