package apps

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

// TestMain provisions the administrator required by app installation tests.
// Production migrations intentionally contain no users, so a fresh CI database
// cannot rely on the demo seed command having run.
func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		os.Exit(m.Run())
	}
	st, err := store.Open(context.Background(), dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open test database:", err)
		os.Exit(1)
	}
	if err := store.Migrate(context.Background(), st.Pool); err != nil {
		fmt.Fprintln(os.Stderr, "migrate test database:", err)
		os.Exit(1)
	}
	if err := st.EnsureBootstrapAdmin(context.Background(), "app-tests@zzira.invalid", "App test administrator", "!test-only!", "admin"); err != nil {
		fmt.Fprintln(os.Stderr, "bootstrap test administrator:", err)
		os.Exit(1)
	}
	code := m.Run()
	st.Close()
	os.Exit(code)
}
