package demo_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/e6qu/zzira/internal/demo"
)

// The shipped company is the one a reader explores, so its size is worth
// saying out loud: a change that halves it, or multiplies it tenfold, should
// be a decision rather than a surprise.
func TestShippedCompanyHasYearsOfHistory(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "demo", "company.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scenario, err := demo.Read(file)
	if err != nil {
		t.Fatalf("read the shipped company: %v", err)
	}
	sprints, versions, events := 0, 0, 0
	for _, project := range scenario.Projects {
		versions += len(project.Versions)
		if project.Board != nil {
			sprints += len(project.Board.Sprints)
		}
	}
	for _, item := range scenario.WorkItems {
		events += len(item.Events)
	}
	t.Logf("people=%d projects=%d sprints=%d versions=%d work=%d events=%d deployments=%d",
		len(scenario.People), len(scenario.Projects), sprints, versions, len(scenario.WorkItems), events, len(scenario.Deployments))
	if len(scenario.WorkItems) < 1000 {
		t.Fatalf("the shipped company has %d work items, which is not years of a company", len(scenario.WorkItems))
	}
}
