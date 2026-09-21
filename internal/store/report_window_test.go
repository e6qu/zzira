package store

import (
	"context"
	"os"
	"testing"
	"time"
)

// The action log is timestamped by the database. A report whose window ends
// at this process's clock therefore loses whatever was recorded in the gap
// between the two clocks -- which on a machine whose database runs a moment
// ahead is everything that just happened.
func TestReportWindowEndsAtTheDatabaseClock(t *testing.T) {
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
	var database time.Time
	if err := st.Pool.QueryRow(ctx, `SELECT now()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	// A window that means "now" reaches at least as far as the database's own
	// clock, whichever of the two is ahead.
	end := st.windowEnd(ctx, time.Now())
	if end.Before(database.UTC()) {
		t.Fatalf("window ends at %s, before the database's %s", end, database.UTC())
	}
	// A window that deliberately ends in the past is left where it was.
	past := time.Now().UTC().Add(-2 * time.Hour)
	if got := st.windowEnd(ctx, past); !got.Equal(past) {
		t.Fatalf("a past window moved to %s, want %s", got, past)
	}
}
