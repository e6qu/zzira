package demo_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// Generated names are made by putting words together, and a phrase spliced
// out of two different phrases reads as gibberish -- "Craud checks" for a
// company that works on card payments and fraud checks. Every generated
// version, sprint goal and work item summary has to be made of words the
// scenario actually wrote.
func TestGeneratedNamesUseWholePhrases(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "demo", "company.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scenario, err := demo.Read(file)
	if err != nil {
		t.Fatalf("read the shipped company: %v", err)
	}
	// Every theme the company names, and the same words as a phrase can start
	// a sentence with.
	phrases := map[string]bool{}
	for _, generated := range shippedThemes(t) {
		phrases[generated] = true
		phrases[strings.ToUpper(generated[:1])+generated[1:]] = true
	}
	// Only what the generator wrote: a curated sprint goal or release note is
	// whatever the person who wrote it meant.
	generated := func(id string) bool { return strings.Contains(id, "-g") }
	for _, project := range scenario.Projects {
		for _, version := range project.Versions {
			phrase, _, found := strings.Cut(version.Description, ", released from ")
			if !found || phrase == "" || !generated(version.ID) {
				continue
			}
			if !phrases[phrase] {
				t.Fatalf("version %s of %s is described as %q, which is not something the company works on",
					version.Name, project.ID, phrase)
			}
		}
		if project.Board == nil {
			continue
		}
		for _, sprint := range project.Board.Sprints {
			if sprint.Goal == "" || phrases[sprint.Goal] || !generated(sprint.ID) {
				continue
			}
			t.Fatalf("sprint %s of %s aims at %q, which is not something the company works on",
				sprint.Name, project.ID, sprint.Goal)
		}
	}
}

// shippedThemes are the themes the company's generated projects work on.
func shippedThemes(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "demo", "company.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Generate struct {
			Projects []struct {
				Themes []string `json:"themes"`
			} `json:"projects"`
		} `json:"generate"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	themes := []string{}
	for _, project := range document.Generate.Projects {
		themes = append(themes, project.Themes...)
	}
	return themes
}
