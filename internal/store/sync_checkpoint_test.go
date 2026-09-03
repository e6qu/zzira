package store

import (
	"context"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestActionPageAdvancesAcrossFilteredActions(t *testing.T) {
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

	workspaceID := NewID("ws")
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspaces (id, slug, name) VALUES ($1,$1,'Sync test')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := st.Pool.Exec(ctx, `DELETE FROM actions WHERE workspace_id=$1`, workspaceID); err != nil {
			t.Errorf("cleanup actions: %v", err)
		}
		if _, err := st.Pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, workspaceID); err != nil {
			t.Errorf("cleanup workspace: %v", err)
		}
	}()
	const head int64 = 0
	targetUser := NewID("usr")
	otherUser := NewID("usr")
	for i, userID := range []string{otherUser, targetUser} {
		if _, err := st.Pool.Exec(ctx, `UPDATE workspaces SET seq=seq+1 WHERE id=$1`, workspaceID); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Pool.Exec(ctx, `
			INSERT INTO actions (workspace_id, seq, entity_type, entity_id, op, schema_v, payload, actor_id)
			VALUES ($1, $2, $3, $4, 'upsert', $5,
			        jsonb_build_object('notification', jsonb_build_object('userId', $6::text)), $6::text)`,
			workspaceID, head+int64(i)+1, models.EntityNotification, NewID("ntf"), models.SchemaVersion, userID); err != nil {
			t.Fatal(err)
		}
	}
	actions, to, err := st.ActionPageSince(ctx, workspaceID, targetUser, head, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 0 || to != head+1 {
		t.Fatalf("first page actions=%v to=%d, want empty page ending %d", actions, to, head+1)
	}
	actions, to, err = st.ActionPageSince(ctx, workspaceID, targetUser, to, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || to != head+2 {
		t.Fatalf("second page len=%d to=%d, want one action ending %d", len(actions), to, head+2)
	}
}
