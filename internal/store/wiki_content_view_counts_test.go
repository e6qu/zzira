package store

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestWikiContentViewCountsWindowAndIDs(t *testing.T) {
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
	if err = Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, first, second := NewID("ws"), NewID("usr"), NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Wiki views')`, workspaceID)
	for _, id := range []string{first, second} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, id, id+"@example.test")
	}
	t.Cleanup(func() {
		exec(`DELETE FROM wiki_content_views WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{first, second} {
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	now := time.Now().UTC()
	for _, view := range []struct {
		contentType, contentID, userID string
		at                             time.Time
	}{
		{"page", "101", first, now.AddDate(0, 0, -40)},
		{"page", "101", first, now.AddDate(0, 0, -2)},
		{"page", "101", second, now.AddDate(0, 0, -1)},
		{"page", "102", first, now},
		{"blogpost", "101", second, now},
	} {
		exec(`INSERT INTO wiki_content_views(workspace_id,content_type,content_id,user_id,viewed_at) VALUES($1,$2,$3::bigint,$4,$5)`, workspaceID, view.contentType, view.contentID, view.userID, view.at)
	}
	recent, err := st.WikiContentViewCounts(ctx, workspaceID, "page", []string{"101"}, now.AddDate(0, 0, -30))
	if err != nil || len(recent) != 1 || recent["101"] != (WikiViewCount{Views: 2, Viewers: 2}) {
		t.Fatalf("recent page counts = %+v, %v", recent, err)
	}
	ever, err := st.WikiContentViewCounts(ctx, workspaceID, "page", []string{"101", "102", "999"}, time.Time{})
	if err != nil || ever["101"] != (WikiViewCount{Views: 3, Viewers: 2}) || ever["102"] != (WikiViewCount{Views: 1, Viewers: 1}) || len(ever) != 2 {
		t.Fatalf("all-time page counts = %+v, %v", ever, err)
	}
	if none, err := st.WikiContentViewCounts(ctx, workspaceID, "page", nil, time.Time{}); err != nil || len(none) != 0 {
		t.Fatalf("counts without ids = %+v, %v", none, err)
	}
}
