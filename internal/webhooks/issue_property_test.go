package webhooks

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestIssuePropertyEvents(t *testing.T) {
	set := &models.Action{EntityType: models.EntityIssueProperty, Op: models.OpUpsert, Payload: json.RawMessage(`{"issueId":"iss_1","issueKey":"OPS-7","key":"flag","value":true}`)}
	deleted := &models.Action{EntityType: models.EntityIssueProperty, Op: models.OpDelete, Payload: json.RawMessage(`{"issueId":"iss_1","issueKey":"OPS-7","key":"flag"}`)}
	if event, ok := EventFor(set); !ok || event != "issue_property_set" {
		t.Fatalf("set event = %q, %v", event, ok)
	}
	if event, ok := EventFor(deleted); !ok || event != "issue_property_deleted" {
		t.Fatalf("delete event = %q, %v", event, ok)
	}
	if actionKey(set) != "OPS-7" || propertyKey(set) != "flag" {
		t.Fatalf("key = %q, property = %q", actionKey(set), propertyKey(set))
	}
}

func TestDeliverIssuePropertyEventHonorsKeyFilterAndJQL(t *testing.T) {
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
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Property webhooks')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM webhook_deliveries WHERE webhook_id IN (SELECT id FROM webhooks WHERE workspace_id=$1)`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM webhooks WHERE workspace_id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, workspaceID)
	})

	received := make(chan *http.Request, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	webhook, err := st.CreateWebhook(ctx, workspaceID, server.URL, []string{"issue_property_set"}, `project = OPS`)
	if err != nil {
		t.Fatal(err)
	}
	webhook.PropertyKeys = []string{"flag"}
	if _, err := st.Pool.Exec(ctx, `UPDATE workspaces SET seq=2 WHERE id=$1`, workspaceID); err != nil {
		t.Fatal(err)
	}
	for seq, key := range map[int]string{1: "other", 2: "flag"} {
		payload := `{"issueId":"iss_prop","issueKey":"OPS-7","key":"` + key + `","value":1}`
		if _, err := st.Pool.Exec(ctx, `INSERT INTO actions(workspace_id,seq,entity_type,entity_id,op,schema_v,payload,actor_id) VALUES($1,$2,'issue_property',$3,'upsert',$4,$5,'usr_actor')`,
			workspaceID, seq, "iss_prop/"+key, models.SchemaVersion, payload); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Pool.Exec(ctx, `INSERT INTO webhook_deliveries(webhook_id,seq,state) VALUES($1,$2,'pending')`, webhook.ID, seq); err != nil {
			t.Fatal(err)
		}
	}
	queries := []string{}
	dispatcher := Dispatcher{Store: st, Client: server.Client(), Checker: &JQLChecker{Search: func(_ context.Context, _ string, query string) (bool, error) {
		queries = append(queries, query)
		return true, nil
	}}}
	for _, seq := range []int64{1, 2} {
		if err := dispatcher.deliver(ctx, workspaceID, webhook, seq); err != nil {
			t.Fatal(err)
		}
	}
	if len(received) != 1 {
		t.Fatalf("deliveries = %d, want only the filtered key", len(received))
	}
	if request := <-received; request.Header.Get("X-ZZIRA-Event") != "issue_property_set" {
		t.Fatalf("event header = %q", request.Header.Get("X-ZZIRA-Event"))
	}
	if len(queries) != 2 || !strings.Contains(queries[1], `key = "OPS-7"`) {
		t.Fatalf("JQL checks = %v", queries)
	}
	var states string
	if err := st.Pool.QueryRow(ctx, `SELECT string_agg(state,',' ORDER BY seq) FROM webhook_deliveries WHERE webhook_id=$1`, webhook.ID).Scan(&states); err != nil || states != "delivered,delivered" {
		t.Fatalf("states = %q, %v", states, err)
	}
}
