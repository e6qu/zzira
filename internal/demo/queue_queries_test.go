package demo_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/e6qu/zzira/internal/demo"
	"github.com/e6qu/zzira/internal/jql"
)

// A queue is a saved query an agent opens. The scenario checks a queue's query
// parses; this checks the shipped company's queues compile as well, because a
// query that parses and does not compile is a queue that fails when it is
// opened rather than when the site is built.
func TestShippedCompanyQueuesCompile(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "demo", "company.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scenario, err := demo.Read(file)
	if err != nil {
		t.Fatalf("read the shipped company: %v", err)
	}
	queues := 0
	for _, project := range scenario.Projects {
		if project.ServiceDesk == nil {
			continue
		}
		for _, queue := range project.ServiceDesk.Queues {
			queues++
			parsed, err := jql.Parse(queue.JQL)
			if err != nil {
				t.Fatalf("queue %q: %v", queue.Name, err)
			}
			if compiled := jql.Compile(parsed, "usr_agent", jql.DefaultResolver()); compiled.Err != nil {
				t.Fatalf("queue %q: %v", queue.Name, compiled.Err)
			}
		}
	}
	if queues == 0 {
		t.Fatal("the company's desk works from the three queues every desk is given and no others")
	}
}
