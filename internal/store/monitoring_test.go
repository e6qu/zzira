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

	var before int64
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM issues`).Scan(&before); err != nil {
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
