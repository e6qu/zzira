package store

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// TestMain gives every store integration test a schema owned by its own test
// job. GitHub Actions jobs do not share the e2e job's PostgreSQL service, so
// relying on that job's migration created an order-dependent test suite.
func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		os.Exit(m.Run())
	}
	st, err := Open(context.Background(), dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open test database:", err)
		os.Exit(1)
	}
	defer st.Close()
	if err := Migrate(context.Background(), st.Pool); err != nil {
		fmt.Fprintln(os.Stderr, "migrate test database:", err)
		os.Exit(1)
	}
	if err := st.EnsureBootstrapAdmin(context.Background(), "store-tests@zzira.invalid", "Store test administrator", "!test-only!", "admin"); err != nil {
		fmt.Fprintln(os.Stderr, "bootstrap test administrator:", err)
		os.Exit(1)
	}
	code := m.Run()
	st.Close()
	os.Exit(code)
}
