package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestBoardPageURLPreservesSelectedControls(t *testing.T) {
	target := boardPageURL("board / one", []string{"mine", "urgent"}, "usr 1")
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.EscapedPath() != "/board/board%20%2F%20one" {
		t.Fatalf("path = %q", parsed.EscapedPath())
	}
	if !reflect.DeepEqual(parsed.Query()["qf"], []string{"mine", "urgent"}) || parsed.Query().Get("assignee") != "usr 1" {
		t.Fatalf("query = %v", parsed.Query())
	}
}

func TestSelectedBoardFiltersDeduplicatesAndBoundsInput(t *testing.T) {
	values := url.Values{"qf": {"mine", "", "mine"}}
	for index := 0; index < 25; index++ {
		values.Add("qf", "filter-"+string(rune('a'+index)))
	}
	selected := selectedBoardFilters(values)
	if len(selected) != 20 {
		t.Fatalf("selected %d filters, want 20", len(selected))
	}
	if selected[0] != "mine" || selected[1] != "filter-a" {
		t.Fatalf("selected = %#v", selected)
	}
}

func TestBoardConfigurationFormParsesColumnsAndQuickFilterRows(t *testing.T) {
	form := url.Values{
		"swimlanes":              {"assignee"},
		"cardField":              {"priority", "labels"},
		"filterJQL":              {"project = ZZ"},
		"estimationField":        {"customfield_10001"},
		"columnKey":              {"c0", "c1", "c2"},
		"columnName":             {"To Do", "Working", "Gone"},
		"columnLimit":            {"0", "5", "0"},
		"deleteColumn":           {"c2"},
		"newColumnName":          {"Waiting"},
		"statusID":               {"todo", "doing", "review", "done", "cancelled"},
		"statusColumn_todo":      {"c0"},
		"statusColumn_doing":     {"c1"},
		"statusColumn_review":    {"c1"},
		"statusColumn_done":      {"new"},
		"statusColumn_cancelled": {"c2"},
		"swimlaneName":           {"Urgent", "Stale", "Gone"},
		"swimlaneJQL":            {"priority = High", "status = Done", "priority = Low"},
		"deleteSwimlane":         {"2"},
		"quickFilterID":          {"existing", "", "remove-me"},
		"quickFilterName":        {"Open", "Mine", "Removed"},
		"quickFilterJQL":         {"status != Done", "assignee = currentUser()", "status = Done"},
		"quickFilterDescription": {"Still moving", "My work", "Delete this"},
		"deleteQuickFilter":      {"remove-me"},
	}
	request := httptest.NewRequest("POST", "/board/brd/settings", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	board := &models.Board{Columns: []models.BoardColumn{{Name: "To Do", StatusIDs: []string{"todo"}}}}

	input, err := boardConfigurationForm(request, board)
	if err != nil {
		t.Fatal(err)
	}
	if input.SwimlaneStrategy != "assignee" || !reflect.DeepEqual(input.CardFields, []string{"priority", "labels"}) {
		t.Fatalf("layout input = %+v", input)
	}
	if input.FilterJQL != "project = ZZ" || input.EstimationFieldID != "customfield_10001" {
		t.Fatalf("scope input = %+v", input)
	}
	// The deleted column goes, the new one is added last, and a column keeps
	// every status that named it -- while the status that named the deleted
	// column leaves the board rather than following it.
	want := []models.BoardColumn{
		{Name: "To Do", StatusIDs: []string{"todo"}},
		{Name: "Working", StatusIDs: []string{"doing", "review"}, Limit: 5},
		{Name: "Waiting", StatusIDs: []string{"done"}},
	}
	if !reflect.DeepEqual(input.Columns, want) {
		t.Fatalf("columns = %#v", input.Columns)
	}
	if len(input.QuickFilters) != 2 || input.QuickFilters[0].ID != "existing" || input.QuickFilters[1].Name != "Mine" {
		t.Fatalf("quick filters = %+v", input.QuickFilters)
	}
	// The lane whose delete box is ticked goes; the rest keep their order.
	wantLanes := []models.BoardSwimlane{
		{Name: "Urgent", JQL: "priority = High"},
		{Name: "Stale", JQL: "status = Done"},
	}
	if !reflect.DeepEqual(input.Swimlanes, wantLanes) {
		t.Fatalf("swimlanes = %#v", input.Swimlanes)
	}
}

// A configuration the store refuses comes back as the page carrying the
// message. Until the board the page came from was kept, the refusal replaced
// it with the nothing the store answers on rejection and the handler
// dereferenced it: every store-level refusal crashed instead of explaining
// itself.
func TestBoardSettingsRejectionKeepsTheBoardItCameFrom(t *testing.T) {
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
	workspaceID, ownerID, projectID := store.NewID("ws"), store.NewID("usr"), store.NewID("prj")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Board rejection test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Board owner')`, ownerID, ownerID+"@example.invalid")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, ownerID)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id) VALUES($1,$2,$3,'Rejection project','wf_default')`,
		projectID, workspaceID, "BR"+strings.ToUpper(projectID[len(projectID)-5:]))
	t.Cleanup(func() {
		exec(`DELETE FROM sessions WHERE user_id=$1`, ownerID)
		exec(`DELETE FROM boards WHERE project_id=$1`, projectID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, ownerID)
	})
	board, err := st.CreateBoard(ctx, ownerID, workspaceID, store.BoardCreate{Name: "Rejection board", Type: "scrum", ProjectID: projectID})
	if err != nil {
		t.Fatal(err)
	}
	token, err := authn.LoginOIDC(ctx, st, ownerID, "id-token", "https://issuer.example.invalid", ownerID+"-subject", "")
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID}
	// A card field the store does not know: a refusal only it can make.
	form := url.Values{
		"swimlanes": {"none"}, "cardField": {"story_points"}, "filterJQL": {board.FilterJQL},
		"columnKey": {"c0"}, "columnName": {"To Do"}, "columnLimit": {"0"},
		"statusID": {"st_todo"}, "statusColumn_st_todo": {"c0"},
	}
	request := httptest.NewRequest(http.MethodPost, "/board/"+board.ID+"/settings", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "zzira_session", Value: token})
	response := httptest.NewRecorder()
	h.UpdateBoardSettings(response, request, board.ID)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("a refused configuration = %d, want 400: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "story_points") {
		t.Fatal("the refusal did not name what was wrong")
	}
	after, err := st.BoardByIDInWorkspace(ctx, workspaceID, board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after.CardFields, board.CardFields) {
		t.Fatalf("a refused configuration changed the board: %#v", after.CardFields)
	}
}

func TestBoardConfigurationFormRejectsMalformedRowsAndLimits(t *testing.T) {
	board := &models.Board{Columns: []models.BoardColumn{{Name: "To Do", StatusIDs: []string{"todo"}}}}
	for _, encoded := range []string{
		"swimlanes=none&columnKey=c0&columnName=To+Do&columnLimit=1.5",
		"swimlanes=none&columnKey=c0&columnName=To+Do",
		"swimlanes=none&quickFilterID=one&quickFilterName=One",
	} {
		request := httptest.NewRequest("POST", "/board/brd/settings", strings.NewReader(encoded))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if _, err := boardConfigurationForm(request, board); err == nil {
			t.Fatalf("form %q should fail", encoded)
		}
	}
}
