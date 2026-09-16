package agile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestAgileBoardQueries covers Jira's board list filters, sprint state
// filtering and the jql, fields and validateQuery parameters of the Agile
// issue reads, and ranked moves into a sprint.
func TestAgileBoardQueries(t *testing.T) {
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
	if err = store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, actor := store.NewID("ws"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Board queries')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Board lead')`, actor, actor+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actor)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actor, store.HashToken(actor))
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM sprint_issues WHERE sprint_id IN (SELECT s.id FROM sprints s JOIN boards b ON b.id=s.board_id JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1)`,
			`DELETE FROM sprints WHERE board_id IN (SELECT b.id FROM boards b JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1)`,
			`DELETE FROM service_desks WHERE workspace_id=$1`, `DELETE FROM issues WHERE workspace_id=$1`,
			`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, `DELETE FROM projects WHERE workspace_id=$1`,
			`DELETE FROM actions WHERE workspace_id=$1`, `DELETE FROM memberships WHERE workspace_id=$1`, `DELETE FROM workspaces WHERE id=$1`,
		} {
			_, _ = st.Pool.Exec(ctx, query, workspaceID)
		}
		_, _ = st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE user_id=$1`, actor)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, actor)
	})

	service := &commands.Service{Store: st}
	suffix := fmt.Sprintf("%05d", time.Now().UnixNano()%100000)
	createProject := func(key, name, projectType, template string) *models.Project {
		t.Helper()
		project, err := service.CreateProject(ctx, actor, workspaceID, commands.CreateProjectInput{
			Key: key + suffix, Name: name, ProjectTypeKey: projectType, ProjectTemplateKey: template, LeadAccountID: actor,
		})
		if err != nil {
			t.Fatal(err)
		}
		return project
	}
	scrumProject := createProject("SQ", "Mobile delivery", "software", "")
	kanbanProject := createProject("KQ", "Alpha operations", "software", "com.pyxis.greenhopper.jira:gh-simplified-kanban-classic")
	createProject("BQ", "Business planning", "business", "")
	serviceProject := createProject("DQ", "Help desk", "service_desk", "com.atlassian.servicedesk:simplified-it-service-management")
	boards, err := st.BoardsByWorkspace(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	byProject := map[string]*models.Board{}
	for _, board := range boards {
		byProject[board.ProjectID] = board
	}
	scrum, kanban := byProject[scrumProject.ID], byProject[kanbanProject.ID]

	h := &Handler{
		Store: st, Commands: service, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test",
		IssueBean: func(issue *models.Issue) map[string]any {
			return map[string]any{"id": issue.ID, "key": issue.Key, "fields": map[string]any{"summary": issue.Summary, "status": issue.Status.Name, "labels": issue.Labels}}
		},
	}
	call := func(method, path, body string, want int) map[string]any {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(actor+"@example.test", actor)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		if len(response.Body.Bytes()) > 0 && response.Body.Bytes()[0] == '{' {
			if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	names := func(page map[string]any, list string, field string) string {
		values := []string{}
		for _, value := range page[list].([]any) {
			values = append(values, fmt.Sprint(value.(map[string]any)[field]))
		}
		return strings.Join(values, ",")
	}

	// Board list filters.
	all := call(http.MethodGet, "/rest/agile/1.0/board?orderBy=name", "", http.StatusOK)
	if all["total"] != float64(2) || names(all, "values", "name") != kanban.Name+","+scrum.Name {
		t.Fatal(all)
	}
	if desc := call(http.MethodGet, "/rest/agile/1.0/board?orderBy=-name&maxResults=1", "", http.StatusOK); desc["isLast"] != false || names(desc, "values", "name") != scrum.Name {
		t.Fatal(desc)
	}
	if kanbans := call(http.MethodGet, "/rest/agile/1.0/board?type=kanban", "", http.StatusOK); names(kanbans, "values", "name") != kanban.Name {
		t.Fatal(kanbans)
	}
	call(http.MethodGet, "/rest/agile/1.0/board?type=board", "", http.StatusBadRequest)
	if byName := call(http.MethodGet, "/rest/agile/1.0/board?name=MOBILE", "", http.StatusOK); names(byName, "values", "name") != scrum.Name {
		t.Fatal(byName)
	}
	if located := call(http.MethodGet, "/rest/agile/1.0/board?projectKeyOrId="+scrumProject.Key, "", http.StatusOK); names(located, "values", "name") != scrum.Name {
		t.Fatal(located)
	}
	if negated := call(http.MethodGet, "/rest/agile/1.0/board?projectKeyOrId="+scrumProject.Key+"&negateLocationFiltering=true", "", http.StatusOK); names(negated, "values", "name") != kanban.Name {
		t.Fatal(negated)
	}
	call(http.MethodGet, "/rest/agile/1.0/board?projectKeyOrId=NOPE", "", http.StatusBadRequest)
	if people := call(http.MethodGet, "/rest/agile/1.0/board?accountIdLocation="+actor, "", http.StatusOK); people["total"] != float64(0) {
		t.Fatal(people)
	}
	if desks := call(http.MethodGet, "/rest/agile/1.0/board?projectTypeLocation=service_desk", "", http.StatusOK); names(desks, "values", "name") != byProject[serviceProject.ID].Name {
		t.Fatal(desks)
	}
	if filtered := call(http.MethodGet, fmt.Sprintf("/rest/agile/1.0/board?filterId=%d", kanban.FilterJiraID), "", http.StatusOK); names(filtered, "values", "name") != kanban.Name {
		t.Fatal(filtered)
	}
	expanded := call(http.MethodGet, "/rest/agile/1.0/board?name=Mobile&expand=admins,permissions", "", http.StatusOK)
	board := expanded["values"].([]any)[0].(map[string]any)
	if board["canEdit"] != true || len(board["admins"].(map[string]any)["users"].([]any)) != 1 || board["location"].(map[string]any)["projectTypeKey"] != "software" {
		t.Fatal(board)
	}

	// A board that names its own administrators reports them instead of the
	// project lead it falls back to, on the board as well as in the list.
	if _, err := st.AddBoardAdmin(ctx, actor, workspaceID, scrum.ID, store.BoardAdminInput{Type: "user", AccountID: actor}); err != nil {
		t.Fatal(err)
	}
	named := call(http.MethodGet, fmt.Sprintf("/rest/agile/1.0/board/%d?expand=admins", scrum.JiraID), "", http.StatusOK)
	namedUsers := named["admins"].(map[string]any)["users"].([]any)
	if len(namedUsers) != 1 || namedUsers[0].(map[string]any)["accountId"] != actor {
		t.Fatal(named)
	}
	if plain := call(http.MethodGet, fmt.Sprintf("/rest/agile/1.0/board/%d", scrum.JiraID), "", http.StatusOK); plain["admins"] != nil {
		t.Fatalf("a board reports administrators only when asked: %v", plain)
	}

	// Work and sprints.
	newIssue := func(project *models.Project, summary string, labels []string) *models.Issue {
		t.Helper()
		issue, _, err := service.CreateIssue(ctx, commands.CreateIssueInput{ActorID: actor, WorkspaceID: workspaceID, ProjectIDOrKey: project.Key, Summary: summary, IssueTypeID: "it_task", Labels: labels})
		if err != nil {
			t.Fatal(err)
		}
		return issue
	}
	first := newIssue(scrumProject, "First task", []string{"api"})
	second := newIssue(scrumProject, "Second task", nil)
	third := newIssue(scrumProject, "Third task", []string{"api"})
	sprintID := func(name string) string {
		created := call(http.MethodPost, "/rest/agile/1.0/sprint", fmt.Sprintf(`{"name":%q,"originBoardId":%d}`, name, scrum.JiraID), http.StatusCreated)
		return fmt.Sprint(int64(created["id"].(float64)))
	}
	current, next := sprintID("Sprint 1 "+suffix), sprintID("Sprint 2 "+suffix)
	call(http.MethodPost, "/rest/agile/1.0/sprint/"+current, `{"state":"active","startDate":"2026-09-01T00:00:00.000Z","endDate":"2026-09-15T00:00:00.000Z"}`, http.StatusOK)

	sprintPath := "/rest/agile/1.0/sprint/" + current + "/issue"
	call(http.MethodPost, sprintPath, `{"issues":["`+first.Key+`","`+second.Key+`"]}`, http.StatusNoContent)
	call(http.MethodPost, sprintPath, `{"issues":["`+third.Key+`"],"rankBeforeIssue":"`+first.Key+`"}`, http.StatusNoContent)
	call(http.MethodPost, sprintPath, `{"issues":["NOPE-1"]}`, http.StatusNotFound)
	call(http.MethodPost, sprintPath, `{"issues":["`+third.Key+`"],"rankBeforeIssue":"`+first.Key+`","rankAfterIssue":"`+second.Key+`"}`, http.StatusBadRequest)
	other := newIssue(kanbanProject, "Elsewhere", nil)
	call(http.MethodPost, sprintPath, `{"issues":["`+other.Key+`"]}`, http.StatusBadRequest)

	inSprint := call(http.MethodGet, sprintPath+"?jql=labels%3Dapi&fields=summary", "", http.StatusOK)
	if inSprint["total"] != float64(2) || !strings.Contains(names(inSprint, "issues", "key"), third.Key) {
		t.Fatal(inSprint)
	}
	fields := inSprint["issues"].([]any)[0].(map[string]any)["fields"].(map[string]any)
	if _, ok := fields["summary"]; !ok || len(fields) != 1 {
		t.Fatalf("fields = %v", fields)
	}
	if excluded := call(http.MethodGet, sprintPath+"?fields=-labels", "", http.StatusOK); strings.Contains(fmt.Sprint(excluded["issues"]), "labels:") || excluded["total"] != float64(3) {
		t.Fatal(excluded)
	}
	call(http.MethodGet, sprintPath+"?jql=labels%20%3D", "", http.StatusBadRequest)
	lenient := call(http.MethodGet, sprintPath+"?jql=labels%20%3D&validateQuery=false", "", http.StatusOK)
	if lenient["total"] != float64(0) || len(lenient["warningMessages"].([]any)) != 1 {
		t.Fatal(lenient)
	}

	boardPath := fmt.Sprintf("/rest/agile/1.0/board/%d", scrum.JiraID)
	if onBoard := call(http.MethodGet, boardPath+"/issue?jql=summary~%22Second%22", "", http.StatusOK); onBoard["total"] != float64(1) || names(onBoard, "issues", "key") != second.Key {
		t.Fatal(onBoard)
	}
	backlogIssue := newIssue(scrumProject, "Backlog task", []string{"later"})
	if backlog := call(http.MethodGet, boardPath+"/backlog?jql=labels%3Dlater", "", http.StatusOK); backlog["total"] != float64(1) || names(backlog, "issues", "key") != backlogIssue.Key {
		t.Fatal(backlog)
	}
	if onlyBacklog := call(http.MethodGet, boardPath+"/backlog", "", http.StatusOK); strings.Contains(names(onlyBacklog, "issues", "key"), first.Key) {
		t.Fatal(onlyBacklog)
	}
	if boardSprint := call(http.MethodGet, boardPath+"/sprint/"+current+"/issue?jql=labels%3Dapi", "", http.StatusOK); boardSprint["total"] != float64(2) {
		t.Fatal(boardSprint)
	}

	// Sprint states.
	call(http.MethodPost, "/rest/agile/1.0/sprint/"+current, `{"state":"closed"}`, http.StatusOK)
	if closed := call(http.MethodGet, "/rest/agile/1.0/sprint/"+current, "", http.StatusOK); closed["completeDate"] == nil || closed["createdDate"] == nil {
		t.Fatalf("closed sprint dates = %v", closed)
	}
	call(http.MethodPost, sprintPath, `{"issues":["`+backlogIssue.Key+`"]}`, http.StatusBadRequest)
	ordered := call(http.MethodGet, boardPath+"/sprint", "", http.StatusOK)
	if names(ordered, "values", "state") != "closed,future" {
		t.Fatal(ordered)
	}
	if future := call(http.MethodGet, boardPath+"/sprint?state=future,active", "", http.StatusOK); names(future, "values", "id") != next {
		t.Fatal(future)
	}
	call(http.MethodGet, boardPath+"/sprint?state=done", "", http.StatusBadRequest)
}
