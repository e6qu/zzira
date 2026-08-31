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
}
