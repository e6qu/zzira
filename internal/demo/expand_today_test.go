package demo_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/e6qu/zzira/internal/demo"
)

// A scenario's day zero is the day the site is built. Expanding the shipped
// company must not write anything after it: work, comments, worklogs and SLA
// clocks dated tomorrow are what a reader sees as a site that has run ahead of
// itself.
func TestGeneratedHistoryStopsToday(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "demo", "company.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scenario, err := demo.Read(file)
	if err != nil {
		t.Fatalf("read the shipped company: %v", err)
	}
	for _, item := range scenario.WorkItems {
		if item.CreatedDay > 0 {
			t.Fatalf("work item %s is raised on day %d", item.ID, item.CreatedDay)
		}
		for _, event := range item.Events {
			if event.Day > 0 {
				t.Fatalf("work item %s has a %s on day %d", item.ID, event.Kind, event.Day)
			}
		}
	}
	if scenario.Service == nil {
		return
	}
	for _, request := range scenario.Service.Requests {
		if request.CreatedDay > 0 {
			t.Fatalf("request %s is raised on day %d", request.ID, request.CreatedDay)
		}
		for _, event := range request.Events {
			if event.Day > 0 {
				t.Fatalf("request %s has a %s on day %d", request.ID, event.Kind, event.Day)
			}
		}
	}
}
