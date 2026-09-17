package agile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// TestJiraSoftwareContract covers epics, ranking, board administration,
// estimation and the board and backlog moves as a Jira Software client sees
// them.
func TestJiraSoftwareContract(t *testing.T) {
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
	projectKey := fmt.Sprintf("S%08d", time.Now().UnixNano()%100000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Software contract')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Software actor')`, actor, actor+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actor)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actor, store.HashToken(actor))
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, actor)
		exec(`DELETE FROM users WHERE id=$1`, actor)
	})
	service := &commands.Service{Store: st}
	project, err := service.CreateProject(ctx, actor, workspaceID, commands.CreateProjectInput{
		Key: projectKey, Name: "Software contract", ProjectTypeKey: "software", LeadAccountID: actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	boards, err := st.BoardsByWorkspace(ctx, workspaceID)
	if err != nil || len(boards) == 0 {
		t.Fatalf("boards=%d err=%v", len(boards), err)
	}
	board := boards[0]

	issueIDs := map[string]string{}
	insertIssue := func(number int, summary, typeID, parentID, rank string) string {
		t.Helper()
		key := projectKey + "-" + strconv.Itoa(number)
		id := store.NewID("iss")
		issueIDs[key] = id
		exec(`INSERT INTO issues(id,workspace_id,project_id,key,summary,status_id,issuetype_id,reporter_id,parent_id,rank,updated_seq) VALUES($1,$2,$3,$4,$5,'st_todo',$6,$7,NULLIF($8,''),$9,0)`,
			id, workspaceID, project.ID, key, summary, typeID, actor, parentID, rank)
		return key
	}
	epic := insertIssue(1, "Launch the app", "it_epic", "", "m")
	story := insertIssue(2, "Sign-up flow", "it_story", "", "n")
	task := insertIssue(3, "Store listing", "it_task", "", "o")
	secondEpic := insertIssue(4, "Grow the audience", "it_epic", "", "p")
	exec(`UPDATE issues SET parent_id=$1 WHERE id=$2`, issueIDs[epic], issueIDs[story])
	exec(`UPDATE projects SET issue_seq=4 WHERE id=$1`, project.ID)

	h := &Handler{
		Store: st, Commands: service, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test",
		IssueBean: func(issue *models.Issue) map[string]any {
			return map[string]any{"id": strconv.FormatInt(issue.JiraID, 10), "key": issue.Key}
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
	total := func(page map[string]any) string { return fmt.Sprint(page["total"]) }
	agile := "/rest/agile/1.0"
	boardPath := agile + "/board/" + strconv.FormatInt(board.JiraID, 10)

	// An epic carries Jira Software's name, color and done state.
	read := call(http.MethodGet, agile+"/epic/"+epic, "", http.StatusOK)
	if read["key"] != epic || read["name"] != "Launch the app" || read["summary"] != "Launch the app" || read["done"] != false ||
		!strings.HasPrefix(fmt.Sprint(read["color"].(map[string]any)["key"]), "color_") {
		t.Fatal(read)
	}
	updated := call(http.MethodPost, agile+"/epic/"+epic, `{"name":"Launch","color":{"key":"color_9"},"done":true}`, http.StatusOK)
	if updated["name"] != "Launch" || updated["color"].(map[string]any)["key"] != "color_9" || updated["done"] != true || updated["summary"] != "Launch the app" {
		t.Fatal(updated)
	}
	call(http.MethodPost, agile+"/epic/"+epic, `{"color":{"key":"color_15"}}`, http.StatusBadRequest)
	call(http.MethodPost, agile+"/epic/"+epic, `{"colour":"red"}`, http.StatusBadRequest)
	call(http.MethodGet, agile+"/epic/"+story, "", http.StatusNotFound)

	// Standard issues move in and out of epics.
	if children := call(http.MethodGet, agile+"/epic/"+epic+"/issue", "", http.StatusOK); total(children) != "1" {
		t.Fatal(children)
	}
	call(http.MethodPost, agile+"/epic/"+epic+"/issue", `{"issues":["`+task+`"]}`, http.StatusNoContent)
	call(http.MethodPost, agile+"/epic/"+epic+"/issue", `{"issues":["`+secondEpic+`"]}`, http.StatusBadRequest)
	children := call(http.MethodGet, agile+"/epic/"+epic+"/issue?jql=summary~Store", "", http.StatusOK)
	if total(children) != "1" || children["issues"].([]any)[0].(map[string]any)["key"] != task {
		t.Fatal(children)
	}
	noEpic := agile + "/epic/none/issue?jql=" + "project%3D" + projectKey
	if orphans := call(http.MethodGet, noEpic, "", http.StatusOK); total(orphans) != "0" {
		t.Fatal(orphans)
	}
	call(http.MethodPost, agile+"/epic/none/issue", `{"issues":["`+task+`"]}`, http.StatusNoContent)
	if orphans := call(http.MethodGet, noEpic, "", http.StatusOK); total(orphans) != "1" {
		t.Fatal(orphans)
	}

	// The Agile issue view carries the epic, sprint and flag.
	agileIssue := call(http.MethodGet, agile+"/issue/"+story, "", http.StatusOK)
	fields := agileIssue["fields"].(map[string]any)
	if fields["epic"].(map[string]any)["key"] != epic || fields["sprint"] != nil || fields["flagged"] != false {
		t.Fatal(agileIssue)
	}

	// Board epics and their issues.
	if epics := call(http.MethodGet, boardPath+"/epic", "", http.StatusOK); total(epics) != "2" {
		t.Fatal(epics)
	}
	if done := call(http.MethodGet, boardPath+"/epic?done=true", "", http.StatusOK); total(done) != "1" || done["values"].([]any)[0].(map[string]any)["name"] != "Launch" {
		t.Fatal(done)
	}
	call(http.MethodGet, boardPath+"/epic?done=maybe", "", http.StatusBadRequest)
	epicJiraID := fmt.Sprint(read["id"])
	if boardEpicIssues := call(http.MethodGet, boardPath+"/epic/"+epicJiraID+"/issue", "", http.StatusOK); total(boardEpicIssues) != "1" {
		t.Fatal(boardEpicIssues)
	}
	call(http.MethodGet, boardPath+"/epic/"+story+"/issue", "", http.StatusNotFound)

	// The enhanced reads page with tokens.
	call(http.MethodPost, agile+"/epic/"+epic+"/issue", `{"issues":["`+task+`"]}`, http.StatusNoContent)
	first := call(http.MethodGet, "/rest/software/1.0/epic/"+epic+"/issue?maxResults=1", "", http.StatusOK)
	token, _ := first["nextPageToken"].(string)
	if len(first["issues"].([]any)) != 1 || first["isLast"] != false || token == "" {
		t.Fatal(first)
	}
	second := call(http.MethodGet, "/rest/software/1.0/epic/"+epic+"/issue?maxResults=1&nextPageToken="+token, "", http.StatusOK)
	if len(second["issues"].([]any)) != 1 || second["isLast"] != true {
		t.Fatal(second)
	}
	call(http.MethodGet, "/rest/software/1.0/epic/none/issue?nextPageToken=bogus", "", http.StatusBadRequest)

	// Ranking is site-wide and reports each issue.
	rankOf := func(key string) string {
		var rank string
		if err := st.Pool.QueryRow(ctx, `SELECT rank FROM issues WHERE id=$1`, issueIDs[key]).Scan(&rank); err != nil {
			t.Fatal(err)
		}
		return rank
	}
	call(http.MethodPut, agile+"/issue/rank", `{"issues":["`+task+`"],"rankBeforeIssue":"`+epic+`"}`, http.StatusNoContent)
	if rankOf(task) >= rankOf(epic) {
		t.Fatalf("rank %s=%s %s=%s", task, rankOf(task), epic, rankOf(epic))
	}
	partial := call(http.MethodPut, agile+"/issue/rank", `{"issues":["`+story+`","NOPE-1"],"rankAfterIssue":"`+secondEpic+`"}`, http.StatusMultiStatus)
	entries := partial["entries"].([]any)
	if len(entries) != 2 || fmt.Sprint(entries[0].(map[string]any)["status"]) != "200" || fmt.Sprint(entries[1].(map[string]any)["status"]) != "404" {
		t.Fatal(partial)
	}
	call(http.MethodPut, agile+"/issue/rank", `{"issues":["`+story+`"],"rankAfterIssue":"`+epic+`","rankCustomFieldId":1}`, http.StatusBadRequest)
	call(http.MethodPut, agile+"/epic/"+secondEpic+"/rank", `{"rankBeforeEpic":"`+epic+`"}`, http.StatusNoContent)
	if rankOf(secondEpic) >= rankOf(epic) {
		t.Fatal("epic rank did not move")
	}
	call(http.MethodPut, agile+"/epic/"+secondEpic+"/rank", `{"rankBeforeEpic":"`+story+`"}`, http.StatusNotFound)

	// Boards are created from filters and deleted.
	call(http.MethodPost, agile+"/board", `{"name":"Nameless"}`, http.StatusBadRequest)
	created := call(http.MethodPost, agile+"/board", `{"name":"Team scrum","type":"scrum","filterId":`+strconv.FormatInt(board.FilterJiraID, 10)+`}`, http.StatusCreated)
	createdPath := agile + "/board/" + fmt.Sprint(created["id"])
	if created["location"].(map[string]any)["projectKey"] != projectKey {
		t.Fatal(created)
	}
	configuration := call(http.MethodGet, createdPath+"/configuration", "", http.StatusOK)
	estimation := configuration["estimation"].(map[string]any)
	if estimation["type"] != "field" || estimation["field"].(map[string]any)["displayName"] != "Story point estimate" ||
		fmt.Sprint(configuration["ranking"].(map[string]any)["rankCustomFieldId"]) != "10019" {
		t.Fatal(configuration)
	}
	if byFilter := call(http.MethodGet, agile+"/board/filter/"+strconv.FormatInt(board.FilterJiraID, 10), "", http.StatusOK); total(byFilter) != "1" {
		t.Fatal(byFilter)
	}

	// Estimation reads and writes the board's field.
	estimationPath := agile + "/issue/" + story + "/estimation?boardId=" + fmt.Sprint(created["id"])
	call(http.MethodGet, agile+"/issue/"+story+"/estimation", "", http.StatusBadRequest)
	if value := call(http.MethodGet, estimationPath, "", http.StatusOK); value["value"] != nil || !strings.HasPrefix(fmt.Sprint(value["fieldId"]), "customfield_") {
		t.Fatal(value)
	}
	if value := call(http.MethodPut, estimationPath, `{"value":"5"}`, http.StatusOK); fmt.Sprint(value["value"]) != "5" {
		t.Fatal(value)
	}
	call(http.MethodPut, estimationPath, `{"value":"five"}`, http.StatusBadRequest)
	if value := call(http.MethodGet, estimationPath, "", http.StatusOK); fmt.Sprint(value["value"]) != "5" {
		t.Fatal(value)
	}

	// Moving onto a scrum board needs its active sprint; the backlog move ranks.
	moved := call(http.MethodPost, createdPath+"/issue", `{"issues":["`+story+`"]}`, http.StatusMultiStatus)
	if message := fmt.Sprint(moved["entries"].([]any)[0].(map[string]any)["errors"]); !strings.Contains(message, "no active sprint") {
		t.Fatal(moved)
	}
	createdBoard, err := st.BoardByIDInWorkspace(ctx, workspaceID, fmt.Sprint(created["id"]))
	if err != nil {
		t.Fatal(err)
	}
	sprint, _, err := st.CreateSprint(ctx, actor, workspaceID, createdBoard.ID, "Sprint 1", "")
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE sprints SET state='active',start_date=now(),end_date=now()+interval '14 days' WHERE id=$1`, sprint.ID)
	call(http.MethodPost, createdPath+"/issue", `{"issues":["`+story+`"]}`, http.StatusNoContent)
	if inSprint := call(http.MethodGet, agile+"/issue/"+story, "", http.StatusOK); inSprint["fields"].(map[string]any)["sprint"].(map[string]any)["state"] != "active" {
		t.Fatal(inSprint)
	}
	call(http.MethodPost, agile+"/backlog/"+fmt.Sprint(created["id"])+"/issue", `{"issues":["`+story+`"],"rankBeforeIssue":"`+secondEpic+`"}`, http.StatusNoContent)
	if backlog := call(http.MethodGet, agile+"/issue/"+story, "", http.StatusOK); backlog["fields"].(map[string]any)["sprint"] != nil || rankOf(story) >= rankOf(secondEpic) {
		t.Fatal(backlog)
	}

	call(http.MethodDelete, createdPath, "", http.StatusNoContent)
	call(http.MethodGet, createdPath, "", http.StatusNotFound)

	// Deleting an epic keeps its issues.
	if _, _, err = st.DeleteIssue(ctx, actor, workspaceID, issueIDs[epic], "test"); err != nil {
		t.Fatal(err)
	}
	if orphan := call(http.MethodGet, agile+"/issue/"+task, "", http.StatusOK); orphan["fields"].(map[string]any)["epic"] != nil {
		t.Fatal(orphan)
	}
}
