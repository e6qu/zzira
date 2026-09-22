package demo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

// companyScenario reads the demo company the repository ships, with its
// generated history cut to a few sprints. The whole three years is what an
// operator builds with `make demo`; a test that applied all of it would spend
// minutes writing history to check something that one sprint shows just as
// well. TestShippedCompanyHasYearsOfHistory is what holds the shipped size.
func companyScenario(t *testing.T) *demo.Scenario {
	t.Helper()
	return shippedCompanyOver(t, 14)
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
	result, err := demo.Apply(ctx, st, cmds, scenario, demo.NewClock(today), scenario.Site.Slug)
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

	// Time tracking: an estimate the scenario declared is on the work, logging
	// work moved the remaining estimate down from it, and some work is due on
	// a day. Without these the time tracking, user workload, version workload
	// and calendar surfaces are empty whatever else the company holds.
	tracked := get(jira, "/rest/api/3/search/jql?jql="+
		"project%20%3D%20PAY&maxResults=100&fields=timeoriginalestimate,timeestimate,timespent,duedate")
	trackedIssues, _ := tracked["issues"].([]any)
	estimated, logged, due, overran := 0, 0, 0, 0
	for _, raw := range trackedIssues {
		item, _ := raw.(map[string]any)
		itemFields, _ := item["fields"].(map[string]any)
		original, hasOriginal := itemFields["timeoriginalestimate"].(float64)
		if hasOriginal && original > 0 {
			estimated++
		}
		if itemFields["duedate"] != nil {
			due++
		}
		spent, hasSpent := itemFields["timespent"].(float64)
		if !hasSpent || spent <= 0 {
			continue
		}
		logged++
		remaining, hasRemaining := itemFields["timeestimate"].(float64)
		if !hasOriginal || !hasRemaining {
			t.Fatalf("%v logged %v seconds with no estimate to move", item["key"], spent)
		}
		// Work that logs more than it estimated has nothing left rather than
		// a negative estimate, which is what over-run reads as.
		left := original - spent
		if left < 0 {
			left = 0
		}
		if remaining > left+1 {
			t.Fatalf("%v logged %v seconds against an estimate of %v and has %v left, so logging work did not move the estimate",
				item["key"], spent, original, remaining)
		}
		if left == 0 {
			overran++
		}
	}
	if estimated != len(trackedIssues) {
		t.Fatalf("%d of %d work items in PAY carry an original estimate", estimated, len(trackedIssues))
	}
	if logged == 0 {
		t.Fatal("nobody logged work in PAY, so the time tracking report has nothing to show")
	}
	if due == 0 {
		t.Fatal("no work in PAY is due on a day, so the calendar gadget has nothing to show")
	}
	if overran == 0 {
		t.Fatal("no work in PAY cost more than it was estimated at, so the time tracking report's accuracy column is all one colour")
	}

	// Jira Software: the board and its sprints, including closed ones.
	boards := get(software, "/rest/agile/1.0/board")
	boardValues, _ := boards["values"].([]any)
	if len(boardValues) == 0 {
		t.Fatal("no board")
	}
	// The company has a board for each delivery team, so the one to read is
	// the first project's own, not whichever the site lists first.
	boardID := ""
	for _, raw := range boardValues {
		board, _ := raw.(map[string]any)
		location, _ := board["location"].(map[string]any)
		if location != nil && location["projectKey"] == scenario.Projects[0].Key {
			boardID = fmt.Sprintf("%v", board["id"])
			break
		}
	}
	if boardID == "" {
		t.Fatalf("no board for %s among %d boards", scenario.Projects[0].Key, len(boardValues))
	}
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

	// Jira Service Management: the requests customers raised. The desk's
	// queue is years long, so the page is read rather than the whole of it.
	requests := get(jira, "/rest/servicedeskapi/request?requestOwnership=ALL_REQUESTS&limit=50")
	requestValues, _ := requests["values"].([]any)
	wanted := min(len(scenario.Service.Requests), 50)
	if len(requestValues) != wanted {
		t.Fatalf("the desk has %d requests on the first page, want %d of %d", len(requestValues), wanted, len(scenario.Service.Requests))
	}

	// Nothing a history writes happens after the day the site was built. The
	// action log is where a report reads a history from, and a scenario whose
	// writes ran past today filled it with work that has not happened yet.
	// Setting the site up -- its projects, fields and request types -- is done
	// at the wall clock, as an administrator would; what the history writes is
	// dated by the history.
	var ahead int
	var latest *time.Time
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE created_at > $2), max(created_at) FILTER (WHERE created_at > $2)
		FROM actions WHERE workspace_id=$1 AND entity_type IN
		  ('issue','comment','worklog','watcher','issue_link','wiki_page','wiki_blogpost','sprint_issue','notification')`,
		result.WorkspaceID, today).Scan(&ahead, &latest); err != nil {
		t.Fatalf("read the action log: %v", err)
	}
	if ahead > 0 {
		t.Fatalf("%d of the actions the history wrote are after %s, the latest at %s",
			ahead, today.Format(time.DateOnly), latest.Format(time.RFC3339))
	}

	// The desk's inventory, and the incidents connected to it: an asset
	// nothing is ever about is a topology nobody reads.
	adminID, _, _, err := st.UserByEmail(ctx, admin)
	if err != nil {
		t.Fatalf("read the administrator: %v", err)
	}
	desks, err := st.ServiceDesks(ctx, result.WorkspaceID)
	if err != nil || len(desks) == 0 {
		t.Fatalf("read the service desks: %v (%d desks)", err, len(desks))
	}
	inventory, err := st.ServiceAssetInventory(ctx, result.WorkspaceID, adminID, desks[0].ID)
	if err != nil {
		t.Fatalf("read the asset inventory: %v", err)
	}
	declaredObjects := 0
	for _, schema := range scenario.Service.Assets.Schemas {
		declaredObjects += len(schema.Objects)
	}
	if len(inventory.Objects) != declaredObjects {
		t.Fatalf("the inventory holds %d objects, the scenario declared %d", len(inventory.Objects), declaredObjects)
	}
	if len(inventory.Relationships) != len(scenario.Service.Assets.Relationships) {
		t.Fatalf("the inventory holds %d relationships, the scenario declared %d",
			len(inventory.Relationships), len(scenario.Service.Assets.Relationships))
	}
	// What the topology says depends on what, so the impact the site reports
	// can be checked against it rather than taken on trust.
	dependents := map[string][]string{}
	for _, relation := range scenario.Service.Assets.Relationships {
		dependents[relation.To] = append(dependents[relation.To], relation.From)
	}
	labels := map[string]string{}
	for _, schema := range scenario.Service.Assets.Schemas {
		for _, object := range schema.Objects {
			labels[object.ID] = object.Label
		}
	}
	reach := func(from string) map[string]bool {
		seen, queue := map[string]bool{}, []string{from}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, next := range dependents[current] {
				if next == from || seen[next] {
					continue
				}
				seen[next] = true
				queue = append(queue, next)
			}
		}
		return seen
	}
	// Whatever the site says a request is about, everything the topology says
	// depends on that asset is reached from it -- checked against the site's
	// own requests rather than by matching summaries, which repeat.
	byLabel := map[string]string{}
	for id, label := range labels {
		byLabel[label] = id
	}
	// Scoped to the site this test built: a test database keeps what earlier
	// runs left behind, and a request from one of those has no desk here.
	rows, err := st.Pool.Query(ctx, `SELECT DISTINCT a.request_issue_id FROM service_request_assets a
		JOIN issues i ON i.id=a.request_issue_id WHERE i.workspace_id=$1`, result.WorkspaceID)
	if err != nil {
		t.Fatalf("read the connected requests: %v", err)
	}
	connected := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		connected = append(connected, id)
	}
	rows.Close()
	if len(connected) == 0 {
		t.Fatal("no request is about an asset, so the topology is connected to nothing")
	}
	reached := 0
	for _, issueID := range connected {
		impact, err := st.ServiceRequestAssetImpact(ctx, result.WorkspaceID, adminID, issueID)
		if err != nil {
			t.Fatalf("read the impact of %s: %v", issueID, err)
		}
		want, got := map[string]bool{}, map[string]bool{}
		for _, entry := range impact {
			if entry.Direct {
				for dependent := range reach(byLabel[entry.Object.Label]) {
					want[labels[dependent]] = true
				}
				continue
			}
			got[entry.Object.Label] = true
			reached++
		}
		for label := range want {
			if !got[label] {
				t.Fatalf("a request reaches %v through the topology, and %q is missing", got, label)
			}
		}
	}
	if reached == 0 {
		t.Fatal("no request reaches an asset through the topology, so the relationships carry nothing")
	}

	// A desk's forms ask what the scenario says they ask, and an Assets
	// object field offers only what its filter selects.
	for _, project := range scenario.Projects {
		if project.ServiceDesk == nil {
			continue
		}
		deskID := ""
		for _, desk := range desks {
			if desk.ProjectID == "" {
				continue
			}
			found, err := st.ProjectByIDOrKey(ctx, result.WorkspaceID, desk.ProjectID)
			if err == nil && found != nil && found.Key == project.Key {
				deskID = desk.ID
			}
		}
		if deskID == "" {
			t.Fatalf("the %s desk is not on the site", project.Key)
		}
		types, err := st.ServiceRequestTypes(ctx, result.WorkspaceID, deskID, "")
		if err != nil {
			t.Fatalf("read the request types of %s: %v", project.Key, err)
		}
		byName := map[string]string{}
		for _, requestType := range types {
			byName[requestType.Name] = requestType.ID
		}
		for _, declared := range project.ServiceDesk.RequestTypes {
			if len(declared.Fields) == 0 {
				continue
			}
			fields, err := st.ServiceRequestTypeFields(ctx, result.WorkspaceID, deskID, byName[declared.Name])
			if err != nil {
				t.Fatalf("read the %s form: %v", declared.Name, err)
			}
			// Summary and description, then whatever the scenario named.
			if len(fields) != len(declared.Fields)+2 {
				t.Fatalf("the %s form asks %d things, the scenario named %d", declared.Name, len(fields), len(declared.Fields)+2)
			}
			for _, asked := range fields {
				if asked.AssetFilter == "" {
					continue
				}
				offered, err := st.ServicePortalFilteredAssetObjects(ctx, result.WorkspaceID, deskID, asked.AssetSchemaID, asked.AssetFilter)
				if err != nil {
					t.Fatalf("read what %q offers: %v", asked.Name, err)
				}
				if len(offered) == 0 {
					t.Fatalf("%q is filtered by %q and offers nothing", asked.Name, asked.AssetFilter)
				}
				all, err := st.ServicePortalAssetObjects(ctx, result.WorkspaceID, deskID, asked.AssetSchemaID)
				if err != nil {
					t.Fatal(err)
				}
				if len(offered) >= len(all) {
					t.Fatalf("%q offers %d of %d objects, so its filter selects everything", asked.Name, len(offered), len(all))
				}
			}
		}
	}

	// The words on a work item in the other languages the company reads.
	spanish, err := st.IssueMetadataNamesInLocale(ctx, result.WorkspaceID, "es")
	if err != nil {
		t.Fatalf("read the Spanish names: %v", err)
	}
	for _, translation := range scenario.Translations {
		if translation.Locale != "es" {
			continue
		}
		found := false
		for key, named := range spanish {
			if strings.HasPrefix(key, translation.Kind+":") && named.Name == translation.Translated {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("a reader of Spanish does not call any %s %q", translation.Kind, translation.Translated)
		}
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
		`DELETE FROM sessions WHERE user_id IN (SELECT user_id FROM memberships WHERE workspace_id=$1)`,
		`DELETE FROM dashboards WHERE workspace_id=$1`,
		`DELETE FROM filters WHERE workspace_id=$1`,
		// The knowledge base points at both the site and the people who wrote
		// it, so leaving it behind held every one of them alive and the next
		// run of this suite inherited them. Its own rows go first, innermost
		// out, because none of these foreign keys cascades.
		`DELETE FROM wiki_footer_comment_versions WHERE comment_id IN (SELECT id FROM wiki_footer_comments WHERE page_id IN (SELECT id FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)) OR blog_post_id IN (SELECT id FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)))`,
		`DELETE FROM wiki_footer_comments WHERE page_id IN (SELECT id FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)) OR blog_post_id IN (SELECT id FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1))`,
		`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT id FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1))`,
		`DELETE FROM wiki_pages WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
		`DELETE FROM wiki_blog_post_versions WHERE blog_post_id IN (SELECT id FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1))`,
		`DELETE FROM wiki_blog_posts WHERE space_id IN (SELECT id FROM wiki_spaces WHERE workspace_id=$1)`,
		`DELETE FROM wiki_spaces WHERE workspace_id=$1`,
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
		// The scenario's people have fixed emails, so two sites built from it
		// share their accounts; an account still on another site stays.
		if _, err := st.Pool.Exec(ctx,
			`DELETE FROM users WHERE id = ANY($1)
			 AND NOT EXISTS(SELECT 1 FROM memberships m WHERE m.user_id=users.id)`, people); err != nil {
			t.Logf("clean up people: %v", err)
		}
	}
}

// openStore is the test database, migrated.
func openStore(t *testing.T) (context.Context, *store.Store) {
	t.Helper()
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
	return ctx, st
}

// A deployment serves exactly one workspace, the one WORKSPACE_SLUG names, so
// the demo has to build into that one. Seeding it with the scenario's own
// slug built a company in a workspace the server would never show, and said
// nothing about it.
func TestApplyBuildsIntoTheWorkspaceItIsGiven(t *testing.T) {
	ctx, st := openStore(t)
	scenario := companyScenario(t)
	scenario.Site.Slug = "scenario-" + store.NewID("x")[2:10]
	served := "served-" + store.NewID("x")[2:10]
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmds := &commands.Service{Store: st, Blobs: blobs}
	result, err := demo.Apply(ctx, st, cmds, scenario, demo.NewClock(time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)), served)
	if err != nil {
		t.Fatalf("apply into %s: %v", served, err)
	}
	t.Cleanup(func() { cleanWorkspace(t, ctx, st, result.WorkspaceID) })

	if result.Slug != served {
		t.Fatalf("the demo reported the site %q, applied to %q", result.Slug, served)
	}
	if _, err := st.WorkspaceBySlug(ctx, scenario.Site.Slug); err == nil {
		t.Fatalf("a second workspace was built under the scenario's own slug %q", scenario.Site.Slug)
	}
	var slug, name string
	if err := st.Pool.QueryRow(ctx, `SELECT slug,name FROM workspaces WHERE id=$1`, result.WorkspaceID).Scan(&slug, &name); err != nil {
		t.Fatal(err)
	}
	if slug != served {
		t.Fatalf("the site's slug is %q, want %q", slug, served)
	}
	// Only the slug is the deployment's; the company is still the scenario's.
	if name != scenario.Site.Name {
		t.Fatalf("the site is called %q, want the scenario's name %q", name, scenario.Site.Name)
	}
	project, err := st.ProjectByKey(ctx, result.WorkspaceID, scenario.Projects[0].Key)
	if err != nil || project == nil {
		t.Fatalf("the served workspace has no %s project: %v", scenario.Projects[0].Key, err)
	}

	// Naming no workspace at all is refused rather than guessed at.
	if _, err := demo.Apply(ctx, st, cmds, scenario, demo.NewClock(time.Now().UTC()), ""); err == nil {
		t.Fatal("applying a scenario with no workspace was accepted")
	}
}

// The README invites re-running the mode after editing the scenario. A second
// run keeps the company it built -- one project per key, one sprint per
// sprint, one copy of the work and its history -- and raises only what the
// scenario has gained.
func TestReapplyingAScenarioRaisesOnlyWhatIsNew(t *testing.T) {
	ctx, st := openStore(t)
	scenario := companyScenario(t)
	slug := "rerun-" + store.NewID("x")[2:10]
	scenario.Site.Slug = slug
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmds := &commands.Service{Store: st, Blobs: blobs}
	today := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	first, err := demo.Apply(ctx, st, cmds, scenario, demo.NewClock(today), slug)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	t.Cleanup(func() { cleanWorkspace(t, ctx, st, first.WorkspaceID) })

	count := func(what, query string) int {
		t.Helper()
		var n int
		if err := st.Pool.QueryRow(ctx, query, first.WorkspaceID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", what, err)
		}
		return n
	}
	const (
		projects   = `SELECT count(*) FROM projects WHERE workspace_id=$1`
		issues     = `SELECT count(*) FROM issues WHERE workspace_id=$1`
		components = `SELECT count(*) FROM project_components c JOIN projects p ON p.id=c.project_id WHERE p.workspace_id=$1`
		versions   = `SELECT count(*) FROM project_versions v JOIN projects p ON p.id=v.project_id WHERE p.workspace_id=$1`
		boards     = `SELECT count(*) FROM boards b JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1`
		sprints    = `SELECT count(*) FROM sprints s JOIN boards b ON b.id=s.board_id JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1`
		comments   = `SELECT count(*) FROM comments c JOIN issues i ON i.id=c.issue_id WHERE i.workspace_id=$1`
		filters    = `SELECT count(*) FROM filters WHERE workspace_id=$1`
		dashboards = `SELECT count(*) FROM dashboards WHERE workspace_id=$1`
		pages      = `SELECT count(*) FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1`
		spaces     = `SELECT count(*) FROM wiki_spaces WHERE workspace_id=$1`
		fields     = `SELECT count(*) FROM custom_fields WHERE workspace_id=$1`
		groups     = `SELECT count(*) FROM groups g JOIN directories d ON d.id=g.directory_id JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1`
		levels     = `SELECT count(*) FROM issue_type_hierarchy_levels WHERE workspace_id=$1`
		requests   = `SELECT count(*) FROM service_request_types t JOIN service_desks d ON d.id=t.service_desk_id WHERE d.workspace_id=$1`
		customers  = `SELECT count(*) FROM service_organizations WHERE workspace_id=$1`
		deliveries = `SELECT count(*) FROM software_deployments WHERE workspace_id=$1`
	)
	before := map[string]int{"issues": count("issues", issues)}
	if before["issues"] == 0 {
		t.Fatal("the first run raised no work")
	}
	kept := map[string]string{
		"projects": projects, "components": components, "versions": versions,
		"boards": boards, "sprints": sprints, "comments": comments, "filters": filters,
		"dashboards": dashboards, "pages": pages, "spaces": spaces, "custom fields": fields,
		"groups": groups, "hierarchy levels": levels, "request types": requests,
		"customer organizations": customers, "deliveries": deliveries,
	}
	for what, query := range kept {
		before[what] = count(what, query)
		if before[what] == 0 {
			t.Fatalf("the first run built no %s", what)
		}
	}

	// The scenario gains one work item, which is the edit the README invites.
	added := scenario.WorkItems[0]
	added.ID = "added"
	added.Summary = "Work the second run added"
	added.Parent = ""
	added.Sprint = ""
	added.Events = nil
	scenario.WorkItems = append(scenario.WorkItems, added)

	second, err := demo.Apply(ctx, st, cmds, scenario, demo.NewClock(today), slug)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.WorkspaceID != first.WorkspaceID {
		t.Fatalf("the second run built another workspace: %q then %q", first.WorkspaceID, second.WorkspaceID)
	}
	for what, query := range kept {
		if got := count(what, query); got != before[what] {
			t.Errorf("the second run left %d %s, the first left %d", got, what, before[what])
		}
	}
	if got := count("issues", issues); got != before["issues"]+1 {
		t.Errorf("the second run left %d work items, want the first run's %d plus the one added", got, before["issues"])
	}
	var raised int
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM issues WHERE workspace_id=$1 AND summary=$2`, first.WorkspaceID, added.Summary).Scan(&raised); err != nil {
		t.Fatal(err)
	}
	if raised != 1 {
		t.Fatalf("the work item the edit added was raised %d times", raised)
	}
	// A person keeps the account they already had, and the run still reports
	// how to sign in as them.
	if second.Passwords[scenario.People[0].Email] == "" || second.Tokens[scenario.People[0].Email] == "" {
		t.Fatal("the second run reported no credentials for a person who already had an account")
	}
}
