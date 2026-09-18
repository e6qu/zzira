package demo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/agile"
	"github.com/e6qu/zzira/internal/api3"
	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/confluence"
	"github.com/e6qu/zzira/internal/demo"
	"github.com/e6qu/zzira/internal/store"
)

// companyScenario reads the demo company the repository ships.
func companyScenario(t *testing.T) *demo.Scenario {
	t.Helper()
	file, err := os.Open(filepath.Join("..", "..", "demo", "company.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	scenario, err := demo.Read(file)
	if err != nil {
		t.Fatal(err)
	}
	return scenario
}

// A scenario survives being written and read again: what a person edits is
// what the applier sees.
func TestScenarioRoundTrip(t *testing.T) {
	scenario := companyScenario(t)
	var buf bytes.Buffer
	if err := demo.Write(&buf, scenario); err != nil {
		t.Fatal(err)
	}
	again, err := demo.Read(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("reading what we wrote: %v", err)
	}
	var second bytes.Buffer
	if err := demo.Write(&second, again); err != nil {
		t.Fatal(err)
	}
	if buf.String() != second.String() {
		t.Fatal("writing a scenario twice gave two different documents")
	}
	if len(again.Projects) != len(scenario.Projects) || len(again.WorkItems) != len(scenario.WorkItems) {
		t.Fatalf("the scenario lost content: %d projects, %d work items", len(again.Projects), len(again.WorkItems))
	}
}

// A scenario that contradicts itself is refused before anything is written.
func TestScenarioValidation(t *testing.T) {
	for _, broken := range []struct {
		name, want string
		change     func(*demo.Scenario)
	}{
		{"unknown assignee", "unknown person", func(s *demo.Scenario) { s.WorkItems[0].Assignee = "nobody" }},
		{"unknown project", "unknown project", func(s *demo.Scenario) { s.WorkItems[0].Project = "nowhere" }},
		{"event before the work existed", "before it existed", func(s *demo.Scenario) {
			s.WorkItems[0].Events = []demo.Event{{Day: s.WorkItems[0].CreatedDay - 1, Kind: "comment", Body: "too early"}}
		}},
		{"unknown event kind", "unknown event kind", func(s *demo.Scenario) {
			s.WorkItems[0].Events = []demo.Event{{Day: s.WorkItems[0].CreatedDay, Kind: "teleport"}}
		}},
		{"released version with no date", "needs a releaseDay", func(s *demo.Scenario) {
			s.Projects[0].Versions[0].Released = true
			s.Projects[0].Versions[0].ReleaseDay = nil
		}},
	} {
		t.Run(broken.name, func(t *testing.T) {
			scenario := companyScenario(t)
			broken.change(scenario)
			err := scenario.Validate()
			if err == nil {
				t.Fatalf("the scenario was accepted")
			}
			if !contains(err.Error(), broken.want) {
				t.Fatalf("error %q does not mention %q", err, broken.want)
			}
		})
	}
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}

// The demo company applies, and every product reads it back: Jira sees the
// work and its history, Jira Software sees the board and its sprints, Jira
// Service Management sees the requests, and Confluence sees the pages.
func TestApplyDemoCompany(t *testing.T) {
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
	scenario := companyScenario(t)
	scenario.Site.Slug = "demo-" + store.NewID("x")[2:10]
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmds := &commands.Service{Store: st, Blobs: blobs}
	today := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	result, err := demo.Apply(ctx, st, cmds, scenario, demo.NewClock(today))
	if err != nil {
		t.Fatalf("apply the demo company: %v", err)
	}
	t.Cleanup(func() { cleanWorkspace(t, ctx, st, result.WorkspaceID) })

	admin := ""
	for _, person := range scenario.People {
		if person.Role == "admin" {
			admin = person.Email
		}
	}
	token := result.Tokens[admin]
	if token == "" {
		t.Fatal("the administrator got no API token")
	}
	jira := &api3.Handler{Store: st, Commands: cmds, Blobs: blobs, WorkspaceSlug: scenario.Site.Slug, BaseURL: "https://demo.test"}
	software := &agile.Handler{Store: st, Commands: cmds, IssueBean: jira.IssueBean, WorkspaceSlug: scenario.Site.Slug, BaseURL: "https://demo.test"}
	wiki := &confluence.Handler{Store: st, Commands: cmds, Blobs: blobs, WorkspaceSlug: scenario.Site.Slug, BaseURL: "https://demo.test"}
	get := func(handler http.Handler, path string) map[string]any {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.SetBasicAuth(admin, token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s: %d %s", path, response.Code, response.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("GET %s: %v %s", path, err, response.Body.String())
		}
		return body
	}

	// Jira: the work the scenario declared is there, with its history.
	search := get(jira, "/rest/api/3/search/jql?jql="+
		"project%20%3D%20PAY%20ORDER%20BY%20created%20ASC&maxResults=100&fields=summary,status,resolution,created")
	issues, _ := search["issues"].([]any)
	payItems := 0
	for _, item := range scenario.WorkItems {
		if item.Project == "pay" {
			payItems++
		}
	}
	if len(issues) != payItems {
		t.Fatalf("Jira sees %d work items in PAY, the scenario declared %d", len(issues), payItems)
	}
	first, _ := issues[0].(map[string]any)
	fields, _ := first["fields"].(map[string]any)
	created, _ := fields["created"].(string)
	createdAt, err := time.Parse(time.RFC3339, created)
	if err != nil {
		createdAt, err = time.Parse("2006-01-02T15:04:05.000-0700", created)
	}
	if err != nil {
		t.Fatalf("created %q: %v", created, err)
	}
	if !createdAt.Before(today.AddDate(0, 0, -30)) {
		t.Fatalf("the oldest work item was created %s, which is not history", created)
	}

	// The history a report reads is there too: a resolved item has a changelog
	// entry that set its resolution.
	resolved := ""
	for _, raw := range issues {
		item, _ := raw.(map[string]any)
		itemFields, _ := item["fields"].(map[string]any)
		if itemFields["resolution"] != nil {
			resolved, _ = item["key"].(string)
			break
		}
	}
	if resolved == "" {
		t.Fatal("no work item was resolved")
	}
	changelog := get(jira, "/rest/api/3/issue/"+resolved+"/changelog")
	entries, _ := changelog["values"].([]any)
	if len(entries) == 0 {
		t.Fatalf("%s has no changelog", resolved)
	}

	// Jira Software: the board and its sprints, including closed ones.
	boards := get(software, "/rest/agile/1.0/board")
	boardValues, _ := boards["values"].([]any)
	if len(boardValues) == 0 {
		t.Fatal("no board")
	}
	board, _ := boardValues[0].(map[string]any)
	boardID := fmt.Sprintf("%v", board["id"])
	sprints := get(software, "/rest/agile/1.0/board/"+boardID+"/sprint?state=closed,active,future")
	sprintValues, _ := sprints["values"].([]any)
	declaredSprints := len(scenario.Projects[0].Board.Sprints)
	if len(sprintValues) != declaredSprints {
		t.Fatalf("the board has %d sprints, the scenario declared %d", len(sprintValues), declaredSprints)
	}
	closed := 0
	for _, raw := range sprintValues {
		sprint, _ := raw.(map[string]any)
		if sprint["state"] == "closed" {
			closed++
		}
	}
	if closed == 0 {
		t.Fatal("no sprint was closed, so the sprint and velocity reports have nothing to show")
	}

	// The board shows the work a sprint can take. Work above the epic level is
	// planned elsewhere, so it stays off the board and the backlog.
	initiative := ""
	for _, item := range scenario.WorkItems {
		if item.Type == "Initiative" {
			initiative = item.Summary
		}
	}
	if initiative == "" {
		t.Fatal("the scenario declares no work above the epic level")
	}
	for _, path := range []string{
		"/rest/agile/1.0/board/" + boardID + "/issue?maxResults=100&fields=summary",
		"/rest/agile/1.0/board/" + boardID + "/backlog?maxResults=100&fields=summary",
	} {
		listed := get(software, path)
		values, _ := listed["issues"].([]any)
		for _, raw := range values {
			item, _ := raw.(map[string]any)
			itemFields, _ := item["fields"].(map[string]any)
			if itemFields["summary"] == initiative {
				t.Fatalf("%s lists work above the epic level", path)
			}
		}
	}

	// Jira Service Management: the requests customers raised.
	requests := get(jira, "/rest/servicedeskapi/request?requestOwnership=ALL_REQUESTS&limit=50")
	requestValues, _ := requests["values"].([]any)
	if len(requestValues) != len(scenario.Service.Requests) {
		t.Fatalf("the desk has %d requests, the scenario declared %d", len(requestValues), len(scenario.Service.Requests))
	}

	// Confluence: the pages, with their tree.
	pages := get(wiki, "/wiki/api/v2/pages?limit=100")
	pageValues, _ := pages["results"].([]any)
	declaredPages := 0
	var count func([]demo.Page)
	count = func(list []demo.Page) {
		for _, page := range list {
			declaredPages++
			count(page.Children)
		}
	}
	for _, space := range scenario.Wiki.Spaces {
		count(space.Pages)
	}
	if len(pageValues) < declaredPages {
		t.Fatalf("Confluence has %d pages, the scenario declared %d", len(pageValues), declaredPages)
	}
}

// cleanWorkspace removes a demo site and the people it created.
func cleanWorkspace(t *testing.T, ctx context.Context, st *store.Store, workspaceID string) {
	t.Helper()
	var people []string
	rows, err := st.Pool.Query(ctx, `SELECT user_id FROM memberships WHERE workspace_id=$1`, workspaceID)
	if err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				people = append(people, id)
			}
		}
		rows.Close()
	}
	for _, statement := range []string{
		`DELETE FROM api_tokens WHERE user_id IN (SELECT user_id FROM memberships WHERE workspace_id=$1)`,
		`DELETE FROM filters WHERE workspace_id=$1`,
		`DELETE FROM issues WHERE workspace_id=$1`,
		`DELETE FROM projects WHERE workspace_id=$1`,
		`DELETE FROM memberships WHERE workspace_id=$1`,
		`DELETE FROM workspaces WHERE id=$1`,
	} {
		if _, err := st.Pool.Exec(ctx, statement, workspaceID); err != nil {
			t.Logf("clean up: %v", err)
		}
	}
	if len(people) > 0 {
		if _, err := st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE user_id = ANY($1)`, people); err != nil {
			t.Logf("clean up tokens: %v", err)
		}
		if _, err := st.Pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, people); err != nil {
			t.Logf("clean up people: %v", err)
		}
	}
}
