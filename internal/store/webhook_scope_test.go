package store

import (
	"context"
	"os"
	"testing"
)

func TestWebhookClaimsAreWorkspaceScopedAndBounded(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}

	const workspaceID = "ws_default"
	wh, err := st.CreateWebhook(ctx, workspaceID, "https://example.invalid/hook", []string{"jira:issue_created"}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := st.Pool.Exec(ctx, `DELETE FROM webhook_deliveries WHERE webhook_id=$1`, wh.ID); err != nil {
			t.Errorf("cleanup deliveries: %v", err)
		}
		if _, err := st.Pool.Exec(ctx, `DELETE FROM webhooks WHERE id=$1`, wh.ID); err != nil {
			t.Errorf("cleanup webhook: %v", err)
		}
	}()

	if webhooks, err := st.Webhooks(ctx, "other-workspace"); err != nil || len(webhooks) != 0 {
		t.Fatalf("other workspace webhooks=%d err=%v, want none", len(webhooks), err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO webhook_deliveries (webhook_id, seq, state) VALUES ($1,1,'pending'),($1,2,'pending')`, wh.ID); err != nil {
		t.Fatal(err)
	}
	claimed, seqs, ok, err := st.ClaimPendingWebhookBatch(ctx, workspaceID, 1)
	if err != nil || !ok || claimed.ID != wh.ID || len(seqs) != 1 || seqs[0] != 1 {
		t.Fatalf("first claim webhook=%v seqs=%v ok=%v err=%v", claimed, seqs, ok, err)
	}
	_, seqs, ok, err = st.ClaimPendingWebhookBatch(ctx, workspaceID, 1)
	if err != nil || !ok || len(seqs) != 1 || seqs[0] != 2 {
		t.Fatalf("second claim seqs=%v ok=%v err=%v", seqs, ok, err)
	}
	if _, err := st.Pool.Exec(ctx, `
		INSERT INTO webhook_deliveries (webhook_id, seq, state, claimed_at)
		VALUES ($1,3,'delivering',now() - interval '3 minutes')`, wh.ID); err != nil {
		t.Fatal(err)
	}
	_, seqs, ok, err = st.ClaimPendingWebhookBatch(ctx, workspaceID, 1)
	if err != nil || !ok || len(seqs) != 1 || seqs[0] != 3 {
		t.Fatalf("stale claim seqs=%v ok=%v err=%v", seqs, ok, err)
	}
	if _, err := st.Pool.Exec(ctx, `
		INSERT INTO webhook_deliveries (webhook_id, seq, state, attempts)
		VALUES ($1,4,'delivering',100)`, wh.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkWebhookDelivery(ctx, wh.ID, 4, false, "still unavailable", `{"webhookEvent":"jira:issue_updated"}`); err != nil {
		t.Fatalf("high-attempt retry overflowed: %v", err)
	}
	// A delivery that exhausts its retries is abandoned with the body it would
	// have sent, rather than retried forever.
	var attempts int
	var state, body string
	var scheduled, failed bool
	if err := st.Pool.QueryRow(ctx, `
		SELECT attempts, state, COALESCE(body,''), next_attempt_at IS NOT NULL, failed_at IS NOT NULL
		FROM webhook_deliveries WHERE webhook_id=$1 AND seq=4`, wh.ID).Scan(&attempts, &state, &body, &scheduled, &failed); err != nil {
		t.Fatal(err)
	}
	if attempts != 101 || state != "abandoned" || scheduled || !failed || body == "" {
		t.Fatalf("exhausted delivery attempts=%d state=%s scheduled=%v failed=%v body=%q", attempts, state, scheduled, failed, body)
	}
	if err := st.Pool.QueryRow(ctx, `
		INSERT INTO webhook_deliveries (webhook_id, seq, state, attempts) VALUES ($1,5,'delivering',1)
		RETURNING seq`, wh.ID).Scan(new(int64)); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkWebhookDelivery(ctx, wh.ID, 5, false, "unavailable", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT state, next_attempt_at IS NOT NULL FROM webhook_deliveries WHERE webhook_id=$1 AND seq=5`, wh.ID).Scan(&state, &scheduled); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || !scheduled {
		t.Fatalf("early failure state=%s scheduled=%v, want a scheduled retry", state, scheduled)
	}
}

// A webhook's JQL filter, and a Connect app's event filter with it, is
// evaluated through the same search the product uses, so the filter's clause
// must arrive numbered from $2 — the search owns $1 for the workspace. A
// clause numbered from $1 shifted every placeholder, the statement failed
// with "could not determine data type of parameter $3", and the delivery was
// silently marked as filtered out.
func TestWebhookJQLMatchRunsTheFilterThroughSearch(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}

	workspaceID, actorID := NewID("ws"), NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Webhook filters')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Webhook admin')`, actorID, actorID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actorID)
	t.Cleanup(func() {
		drop := func(query string, args ...any) { _, _ = st.Pool.Exec(ctx, query, args...) }
		drop(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		drop(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		drop(`DELETE FROM users WHERE id=$1`, actorID)
	})

	// The shape the webhook dispatcher builds: the rule's own filter with the
	// issue's key forced onto it.
	match, err := st.WebhookJQLMatch(ctx, workspaceID, `(project = WH AND status != Done) AND key = "WH-1"`)
	if err != nil {
		t.Fatalf("evaluate webhook filter: %v", err)
	}
	if match {
		t.Fatal("filter matched an issue in a workspace that has none")
	}
}
