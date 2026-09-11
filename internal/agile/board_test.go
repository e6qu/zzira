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

// TestBoardContract covers the board reads Jira serves under both
// /rest/agile/1.0 and /rest/software/1.0.
func TestBoardContract(t *testing.T) {
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
	projectKey := fmt.Sprintf("B%08d", time.Now().UnixNano()%100000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Board contract')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Board actor')`, actor, actor+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, actor)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, actor, store.HashToken(actor))
	t.Cleanup(func() {
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, actor)
		exec(`DELETE FROM users WHERE id=$1`, actor)
	})

	service := &commands.Service{Store: st}
	project, err := service.CreateProject(ctx, actor, workspaceID, commands.CreateProjectInput{
		Key: projectKey, Name: "Board contract", ProjectTypeKey: "software", LeadAccountID: actor,
	})
	if err != nil {
		t.Fatal(err)
	}
	boards, err := st.BoardsByWorkspace(ctx, workspaceID)
	if err != nil || len(boards) == 0 {
		t.Fatalf("boards=%d err=%v", len(boards), err)
	}
	board := boards[0]

	h := &Handler{
		Store: st, Commands: service, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test",
		IssueBean: func(issue *models.Issue) map[string]any {
			return map[string]any{"id": issue.ID, "key": issue.Key}
		},
	}
	call := func(method, path string, want int) map[string]any {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(""))
		request.SetBasicAuth(actor+"@example.test", actor)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		out := map[string]any{}
		if len(response.Body.Bytes()) > 0 && response.Body.Bytes()[0] == '{' {
			if err = json.Unmarshal(response.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	agile := "/rest/agile/1.0/board/" + board.ID
	software := "/rest/software/1.0/board/" + board.ID

	// The board's project, in both shapes.
	projects := call(http.MethodGet, agile+"/project", http.StatusOK)
	if fmt.Sprint(projects["total"]) != "1" {
		t.Fatal(projects)
	}
	full := call(http.MethodGet, agile+"/project/full", http.StatusOK)
	values, _ := full["values"].([]any)
	if len(values) != 1 || values[0].(map[string]any)["projectTypeKey"] != "software" {
		t.Fatal(full)
	}

	// Versions of the board's project, with Jira's released filter.
	if _, err = st.SaveVersion(ctx, workspaceID, actor, project.ID, "", store.VersionUpdate{Name: strPtr("1.0")}); err != nil {
		t.Fatal(err)
	}
	versions := call(http.MethodGet, agile+"/version", http.StatusOK)
	if fmt.Sprint(versions["total"]) != "1" {
		t.Fatal(versions)
	}
	if released := call(http.MethodGet, agile+"/version?released=true", http.StatusOK); fmt.Sprint(released["total"]) != "0" {
		t.Fatal(released)
	}

	// No work type is an epic, so the epic list is empty and every board issue
	// counts as having no epic.
	if epics := call(http.MethodGet, agile+"/epic", http.StatusOK); fmt.Sprint(epics["total"]) != "0" {
		t.Fatal(epics)
	}
	call(http.MethodGet, agile+"/epic/none/issue", http.StatusOK)
	call(http.MethodGet, agile+"/epic/10001/issue", http.StatusNotFound)

	// Features and reports follow the board's own configuration.
	features := call(http.MethodGet, agile+"/features", http.StatusOK)
	if _, ok := features["features"]; !ok {
		t.Fatal(features)
	}
	call(http.MethodPut, agile+"/features", http.StatusBadRequest)
	reports := call(http.MethodGet, agile+"/reports", http.StatusOK)
	if _, ok := reports["reports"]; !ok {
		t.Fatal(reports)
	}

	// Sprint issues resolve only for a sprint on this board.
	sprint, _, err := st.CreateSprint(ctx, actor, workspaceID, board.ID, "Contract sprint", "")
	if err != nil {
		t.Fatal(err)
	}
	call(http.MethodGet, agile+"/sprint/"+sprint.ID+"/issue", http.StatusOK)
	call(http.MethodGet, agile+"/sprint/spr_missing/issue", http.StatusNotFound)

	// Board properties round-trip, and Jira distinguishes create from replace.
	propertyPath := agile + "/properties/prefs"
	put := func(body string, want int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPut, propertyPath, strings.NewReader(body))
		request.SetBasicAuth(actor+"@example.test", actor)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("PUT %s: got %d want %d: %s", propertyPath, response.Code, want, response.Body.String())
		}
	}
	put(`{"theme":"dark"}`, http.StatusCreated)
	put(`{"theme":"light"}`, http.StatusOK)
	stored := call(http.MethodGet, propertyPath, http.StatusOK)
	if fmt.Sprint(stored["key"]) != "prefs" {
		t.Fatal(stored)
	}
	keys := call(http.MethodGet, agile+"/properties", http.StatusOK)
	if listed, _ := keys["keys"].([]any); len(listed) != 1 {
		t.Fatal(keys)
	}
	call(http.MethodDelete, propertyPath, http.StatusNoContent)
	call(http.MethodGet, propertyPath, http.StatusNotFound)
	call(http.MethodDelete, propertyPath, http.StatusNotFound)

	// The /rest/software/1.0 aliases answer the same reads plus the counts.
	call(http.MethodGet, software+"/backlog", http.StatusOK)
	call(http.MethodGet, software+"/issue", http.StatusOK)
	call(http.MethodGet, software+"/epic/none/issue", http.StatusOK)
	call(http.MethodGet, software+"/epic/10001/issue", http.StatusNotFound)
	call(http.MethodGet, software+"/sprint/"+sprint.ID+"/issue", http.StatusOK)
	for _, path := range []string{software + "/backlog/approximate-count", software + "/issue/approximate-count"} {
		counted := call(http.MethodGet, path, http.StatusOK)
		if _, ok := counted["issuesCount"]; !ok {
			t.Fatalf("%s: %v", path, counted)
		}
	}
	call(http.MethodGet, "/rest/software/1.0/board/brd_missing/issue", http.StatusNotFound)
	call(http.MethodGet, "/rest/software/1.0/nope", http.StatusNotFound)

	// ---- sprints ----

	sprintPath := "/rest/agile/1.0/sprint/" + sprint.ID
	// Jira's POST is the partial update and PUT the replacement.
	send := func(method, path, body string, want int) {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(actor+"@example.test", actor)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
	}
	send(http.MethodPost, sprintPath, `{"goal":"Ship the audit"}`, http.StatusOK)
	if reread := call(http.MethodGet, sprintPath, http.StatusOK); reread["goal"] != "Ship the audit" {
		t.Fatal(reread)
	}

	send(http.MethodPut, sprintPath+"/properties/prefs", `{"colour":"blue"}`, http.StatusCreated)
	send(http.MethodPut, sprintPath+"/properties/prefs", `{"colour":"green"}`, http.StatusOK)
	if keys := call(http.MethodGet, sprintPath+"/properties", http.StatusOK); len(keys["keys"].([]any)) != 1 {
		t.Fatal(keys)
	}
	call(http.MethodGet, sprintPath+"/properties/prefs", http.StatusOK)
	call(http.MethodDelete, sprintPath+"/properties/prefs", http.StatusNoContent)
	call(http.MethodGet, sprintPath+"/properties/prefs", http.StatusNotFound)
	call(http.MethodDelete, sprintPath+"/properties/prefs", http.StatusNotFound)

	// Swapping exchanges two sprints' order on the board; the listing follows.
	later, _, err := st.CreateSprint(ctx, actor, workspaceID, board.ID, "Later sprint", "")
	if err != nil {
		t.Fatal(err)
	}
	names := func() []string {
		t.Helper()
		sprints, listErr := st.SprintsByBoard(ctx, board.ID)
		if listErr != nil {
			t.Fatal(listErr)
		}
		out := []string{}
		for _, each := range sprints {
			out = append(out, each.Name)
		}
		return out
	}
	if strings.Join(names(), ",") != "Contract sprint,Later sprint" {
		t.Fatalf("order=%v", names())
	}
	send(http.MethodPost, sprintPath+"/swap", `{"sprintToSwapWith":"`+later.ID+`"}`, http.StatusNoContent)
	if strings.Join(names(), ",") != "Later sprint,Contract sprint" {
		t.Fatalf("order=%v", names())
	}
	send(http.MethodPost, sprintPath+"/swap", `{"sprintToSwapWith":"`+sprint.ID+`"}`, http.StatusBadRequest)
	send(http.MethodPost, sprintPath+"/swap", `{"sprintToSwapWith":"spr_missing"}`, http.StatusNotFound)
	send(http.MethodPost, sprintPath+"/swap", `{}`, http.StatusBadRequest)

	// The software alias serves the same sprint issue read.
	call(http.MethodGet, "/rest/software/1.0/sprint/"+sprint.ID+"/issue", http.StatusOK)
	call(http.MethodGet, "/rest/software/1.0/sprint/spr_missing/issue", http.StatusNotFound)

	// Deleting a sprint returns its work to the backlog rather than removing it.
	call(http.MethodDelete, "/rest/agile/1.0/sprint/"+later.ID, http.StatusNoContent)
	if strings.Join(names(), ",") != "Contract sprint" {
		t.Fatalf("order=%v", names())
	}
	call(http.MethodDelete, "/rest/agile/1.0/sprint/"+later.ID, http.StatusNotFound)
}

func strPtr(value string) *string { return &value }
