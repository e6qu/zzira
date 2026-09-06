package store

import (
	"context"
	"os"
	"testing"
)

func TestMonitoringSnapshotReportsRealIssueCount(t *testing.T) {
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

	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Other packages share TEST_DATABASE_URL and run concurrently. A SHARE lock
	// keeps their issue writes from racing the reference count while still
	// allowing MonitoringSnapshot's separate connection to read the table.
	if _, err := tx.Exec(ctx, `LOCK TABLE issues IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM issues`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	dbHealthy, issueCount, err := st.MonitoringSnapshot(ctx)
	if err != nil {
		t.Fatalf("MonitoringSnapshot: %v", err)
	}
	if !dbHealthy {
		t.Fatal("expected the database to report healthy against a live connection")
	}
	if issueCount != before {
		t.Fatalf("expected the real issue count %d, got %d", before, issueCount)
	}
}
