package webhooks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
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

func TestWorkflowTriggeredWebhookIsAnIssueUpdateAndTargetsOneRegistration(t *testing.T) {
	payload, err := json.Marshal(models.IssueUpdatePayload{
		Issue: models.Issue{ID: "iss_internal", Key: "OPS-42"}, TriggeredWebhookIDs: []string{"wh_target"},
	})
	if err != nil {
		t.Fatal(err)
	}
	action := &models.Action{EntityType: models.EntityIssue, EntityID: "iss_internal", Op: models.OpUpsert, Payload: payload}
	if event, ok := EventFor(action); !ok || event != "jira:issue_updated" {
		t.Fatalf("event = %q, %t", event, ok)
	}
	if !actionTriggersWebhook(action, "wh_target") {
		t.Fatal("target registration was not recognized")
	}
	if actionTriggersWebhook(action, "wh_other") {
		t.Fatal("unrelated registration was force-triggered")
	}
}

func TestDeliverForcesWorkflowTargetPastSubscriptionFilters(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID := store.NewID("ws")
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Webhook workflow')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM webhook_deliveries WHERE webhook_id IN (SELECT id FROM webhooks WHERE workspace_id=$1)`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM webhooks WHERE workspace_id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, workspaceID)
	})

	received := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	webhook, err := st.CreateWebhook(ctx, workspaceID, server.URL, []string{"jira:issue_created"}, `summary = "does not match"`)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(models.IssueUpdatePayload{
		Issue: models.Issue{ID: "iss_forced", Key: "OPS-99"}, TriggeredWebhookIDs: []string{webhook.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE workspaces SET seq=1 WHERE id=$1`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO actions(workspace_id,seq,entity_type,entity_id,op,schema_v,payload,actor_id) VALUES($1,1,'issue','iss_forced','upsert',$2,$3,'usr_actor')`, workspaceID, models.SchemaVersion, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO webhook_deliveries(webhook_id,seq,state) VALUES($1,1,'pending')`, webhook.ID); err != nil {
		t.Fatal(err)
	}
	checkerCalled := false
	dispatcher := Dispatcher{Store: st, Client: server.Client(), Checker: &JQLChecker{Search: func(context.Context, string, string) (bool, error) {
		checkerCalled = true
		return false, nil
	}}}
	if err := dispatcher.deliver(ctx, workspaceID, webhook, 1); err != nil {
		t.Fatal(err)
	}
	if checkerCalled {
		t.Fatal("forced webhook evaluated its ordinary JQL filter")
	}
	select {
	case request := <-received:
		if request.Header.Get("X-ZZIRA-Event") != "jira:issue_updated" {
			t.Fatalf("event header = %q", request.Header.Get("X-ZZIRA-Event"))
		}
	default:
		t.Fatal("target webhook did not receive the transition event")
	}
	var state string
	if err := st.Pool.QueryRow(ctx, `SELECT state FROM webhook_deliveries WHERE webhook_id=$1 AND seq=1`, webhook.ID).Scan(&state); err != nil || state != "delivered" {
		t.Fatalf("delivery state = %q, %v", state, err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO webhook_deliveries(webhook_id,seq,state) VALUES($1,2,'pending')`, webhook.ID); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.deliver(ctx, workspaceID, webhook, 2); err != nil {
		t.Fatalf("sequence gap should be terminal: %v", err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT state FROM webhook_deliveries WHERE webhook_id=$1 AND seq=2`, webhook.ID).Scan(&state); err != nil || state != "delivered" {
		t.Fatalf("gap delivery state = %q, %v", state, err)
	}
}
