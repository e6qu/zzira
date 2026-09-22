package demo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/demo"
	"github.com/e6qu/zzira/internal/store"
)

// shippedCompanyOver reads the shipped company with its generated history cut
// to the given number of days, so a test can apply a real scenario without
// building three years of it.
func shippedCompanyOver(t *testing.T, days int) *demo.Scenario {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "demo", "company.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if plan, ok := document["generate"].(map[string]any); ok {
		plan["days"] = days
	}
	trimmed, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := demo.Read(bytes.NewReader(trimmed))
	if err != nil {
		t.Fatalf("read the shipped company over %d days: %v", days, err)
	}
	return scenario
}

// How long the shipped company takes to build is a fact worth knowing: it is
// what somebody waits for after `make demo`, and it grows with every piece of
// history the scenario declares.
func TestApplyingGeneratedHistoryIsWorthTheWait(t *testing.T) {
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
	scenario := shippedCompanyOver(t, 140)
	scenario.Site.Slug = "scale-" + store.NewID("x")[2:10]
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmds := &commands.Service{Store: st, Blobs: blobs}
	events := 0
	for _, item := range scenario.WorkItems {
		events += len(item.Events)
	}
	started := time.Now()
	result, err := demo.Apply(ctx, st, cmds, scenario, demo.NewClock(time.Now()), scenario.Site.Slug)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	spent := time.Since(started)
	t.Cleanup(func() { cleanWorkspace(t, ctx, st, result.WorkspaceID) })
	writes := len(scenario.WorkItems) + events
	t.Logf("%d work items and %d events in %s (%.0f ms a write)", len(scenario.WorkItems), events, spent.Round(time.Millisecond),
		float64(spent.Milliseconds())/float64(max(writes, 1)))
}
