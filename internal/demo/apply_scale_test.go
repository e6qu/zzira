package demo_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/e6qu/zzira/internal/demo"
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
		// A test is about what the applier does with a history, not about
		// how much of one the company ships: three projects' worth of a few
		// sprints exercises every path the whole three years does.
		if projects, ok := plan["projects"].([]any); ok && len(projects) > 2 {
			plan["projects"] = projects[:2]
		}
		if service, ok := plan["service"].(map[string]any); ok {
			service["requestsPerWeek"] = 3
		}
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
