package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/api3"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

func TestEnvironmentPersistsAcrossScreensAPIAndBrowser(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "ENV", Name: "Environment work", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`, `DELETE FROM workflow_schemes WHERE workspace_id=$1`, `DELETE FROM workflows WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, q, ws); err != nil {
				t.Error(err)
			}
		}
	})
	if _, err = f.store.Pool.Exec(ctx, `INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, f.ownerID, store.HashToken(f.ownerID)); err != nil {
		t.Fatal(err)
	}
	api := &api3.Handler{Store: f.store, Commands: f.handler.Commands, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(f.ownerID+"@example.invalid", f.ownerID)
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	screen, err := f.store.CreateScreen(ctx, ws, f.ownerID, "Environment form", "")
	if err != nil {
		t.Fatal(err)
	}
	tabs, err := f.store.ScreenTabs(ctx, ws, screen.ID)
	if err != nil || len(tabs) != 1 {
		t.Fatalf("screen tabs: %+v %v", tabs, err)
	}
	for _, field := range []string{"summary", "description"} {
		if _, err = f.store.AddScreenTabField(ctx, ws, f.ownerID, screen.ID, tabs[0].ID, field); err != nil {
			t.Fatal(err)
		}
	}
	screenScheme, err := f.store.CreateScreenScheme(ctx, ws, f.ownerID, "Environment screen scheme", "", map[string]string{"default": screen.ID})
	if err != nil {
		t.Fatal(err)
	}
	typeScheme, err := f.store.CreateIssueTypeScreenScheme(ctx, ws, f.ownerID, "Environment type scheme", "", []models.IssueTypeScreenSchemeItem{{IssueTypeID: store.DefaultIssueTypeMapping, ScreenSchemeID: screenScheme.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.AssignIssueTypeScreenScheme(ctx, ws, f.ownerID, project.ID, typeScheme.ID); err != nil {
		t.Fatal(err)
	}
	source := `{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Production Linux","marks":[{"type":"strong"}]}]}]}`
	descriptionSource := strings.ReplaceAll(source, "Production Linux", "Rich description")
	create := `{"fields":{"project":{"key":"ENV"},"issuetype":{"name":"Task"},"summary":"Environment journey","description":` + descriptionSource + `,"environment":` + source + `}}`
	call("POST", "/rest/api/3/issue", create, 400)
	if _, err = f.store.AddScreenTabField(ctx, ws, f.ownerID, screen.ID, tabs[0].ID, "environment"); err != nil {
		t.Fatal(err)
	}
	var created struct {
		Key string `json:"key"`
	}
	if err = json.Unmarshal(call("POST", "/rest/api/3/issue", create, 201).Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	read := func() *models.Issue {
		t.Helper()
		issue, err := f.store.IssueByIDOrKey(ctx, ws, created.Key)
		if err != nil {
			t.Fatal(err)
		}
		return issue
	}
	if adf.PlainText(read().Fields["environment"]) != "Production Linux" {
		t.Fatal("environment was not persisted")
	}
	var expanded struct {
		Fields   map[string]json.RawMessage `json:"fields"`
		Rendered map[string]string          `json:"renderedFields"`
	}
	if err = json.Unmarshal(call("GET", "/rest/api/3/issue/"+created.Key+"?fields=environment&expand=renderedFields,names,schema", "", 200).Body.Bytes(), &expanded); err != nil {
		t.Fatal(err)
	}
	expression := call("POST", "/rest/api/3/expression/eval", `{"expression":"issue.environment.plainText","context":{"issue":{"key":"`+created.Key+`"}}}`, 200)
	if !strings.Contains(expression.Body.String(), `"value":"Production Linux"`) {
		t.Fatalf("environment expression: %s", expression.Body.String())
	}
	if !adf.Equal(expanded.Fields["environment"], json.RawMessage(source)) || !strings.Contains(expanded.Rendered["environment"], "<strong>Production Linux</strong>") {
		t.Fatalf("environment projection: %+v", expanded)
	}
	call("PUT", "/rest/api/3/issue/"+created.Key, `{"fields":{"summary":"Must roll back","environment":"plain string"}}`, 400)
	if read().Summary != "Environment journey" {
		t.Fatal("invalid environment partially saved summary")
	}
	web := func(method, path string, form url.Values, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
		w := httptest.NewRecorder()
		switch {
		case method == "GET" && strings.HasSuffix(path, "/edit"):
			f.handler.EditDialog(w, r, created.Key)
		case method == "GET":
			f.handler.BrowseIssue(w, r, created.Key)
		default:
			f.handler.EditIssue(w, r, created.Key)
		}
		if w.Code != want {
			t.Fatalf("browser %s: %d want %d: %s", path, w.Code, want, w.Body.String())
		}
		return w
	}
	if !strings.Contains(web("GET", "/browse/"+created.Key, nil, 200).Body.String(), "<strong>Production Linux</strong>") {
		t.Fatal("browser environment missing")
	}
	if !strings.Contains(web("GET", "/issues/"+created.Key+"/edit", nil, 200).Body.String(), `name="environment"`) {
		t.Fatal("edit environment missing")
	}
	web("POST", "/issues/"+created.Key+"/edit", url.Values{"summary": {"Environment journey"}, "description": {"Rich description"}, "environment": {"Production Linux"}}, 200)
	if !adf.Equal(read().Description, json.RawMessage(descriptionSource)) {
		t.Fatal("unchanged browser Description lost rich formatting")
	}
	if !adf.Equal(read().Fields["environment"], json.RawMessage(source)) {
		t.Fatal("unchanged browser Environment lost rich formatting")
	}
	web("POST", "/issues/"+created.Key+"/edit", url.Values{"summary": {"Environment journey"}, "description": {"Rich description"}, "environment": {"Staging Linux"}}, 200)
	if adf.PlainText(read().Fields["environment"]) != "Staging Linux" {
		t.Fatal("browser edit was not persisted")
	}
	call("PUT", "/rest/api/3/issue/"+created.Key, `{"update":{"environment":[{"add":`+source+`}]}}`, 400)
	call("PUT", "/rest/api/3/issue/"+created.Key, `{"fields":{"environment":null},"update":{"environment":[{"set":null}]}}`, 400)
	call("PUT", "/rest/api/3/issue/"+created.Key, `{"update":{"environment":[{"set":`+source+`}]}}`, 204)
	var search struct {
		Issues []struct {
			Key string `json:"key"`
		} `json:"issues"`
	}
	for _, jql := range []string{`project = ENV AND environment ~ "Production"`, `project = ENV AND text ~ "Production"`} {
		path := "/rest/api/3/search?jql=" + url.QueryEscape(jql) + "&fields=environment"
		if err = json.Unmarshal(call("GET", path, "", 200).Body.Bytes(), &search); err != nil || len(search.Issues) != 1 || search.Issues[0].Key != created.Key {
			t.Fatalf("environment JQL %s: %+v %v", jql, search, err)
		}
	}
	path := "/rest/api/3/search?jql=" + url.QueryEscape(`project = ENV AND environment ~ "paragraph"`)
	if err = json.Unmarshal(call("GET", path, "", 200).Body.Bytes(), &search); err != nil || len(search.Issues) != 0 {
		t.Fatal("environment search indexed ADF structure")
	}
	var configID string
	if err = f.store.Pool.QueryRow(ctx, `SELECT mapping.configuration_id::text FROM project_field_configuration_schemes assigned JOIN field_configuration_scheme_items mapping ON mapping.scheme_id=assigned.scheme_id WHERE assigned.project_id=$1 AND mapping.issue_type_id=$2`, project.ID, store.DefaultIssueTypeMapping).Scan(&configID); err != nil {
		t.Fatal(err)
	}
	configPath := "/rest/api/3/fieldconfiguration/" + configID + "/fields"
	call("PUT", configPath, `{"fieldConfigurationItems":[{"id":"environment","isRequired":true}]}`, 204)
	call("PUT", "/rest/api/3/issue/"+created.Key, `{"fields":{"environment":null}}`, 400)
	call("PUT", "/rest/api/3/issue/"+created.Key, `{"fields":{"environment":{"type":"doc","version":1,"content":[]}}}`, 400)
	call("PUT", configPath, `{"fieldConfigurationItems":[{"id":"environment","isHidden":true}]}`, 204)
	if strings.Contains(web("GET", "/issues/"+created.Key+"/edit", nil, 200).Body.String(), `name="environment"`) {
		t.Fatal("hidden environment offered for edit")
	}
	if strings.Contains(web("GET", "/browse/"+created.Key, nil, 200).Body.String(), "<strong>Production Linux</strong>") {
		t.Fatal("hidden environment shown")
	}
	call("PUT", "/rest/api/3/issue/"+created.Key, `{"fields":{"environment":`+source+`}}`, 400)
	call("PUT", configPath, `{"fieldConfigurationItems":[{"id":"environment","isHidden":false}]}`, 204)
	call("PUT", "/rest/api/3/issue/"+created.Key, `{"fields":{"environment":null}}`, 204)
	if len(read().Fields["environment"]) != 0 && string(read().Fields["environment"]) != "null" {
		t.Fatal("environment did not clear")
	}
	if err = json.Unmarshal(call("GET", "/rest/api/3/search?jql="+url.QueryEscape(`project = ENV AND environment is EMPTY`), "", 200).Body.Bytes(), &search); err != nil || len(search.Issues) != 1 {
		t.Fatal("cleared environment not empty in search")
	}
	if err = f.store.RemoveScreenTabField(ctx, ws, f.ownerID, screen.ID, tabs[0].ID, "environment"); err != nil {
		t.Fatal(err)
	}
	call("PUT", "/rest/api/3/issue/"+created.Key, `{"fields":{"environment":`+source+`}}`, 400)
	if strings.Contains(web("GET", "/issues/"+created.Key+"/edit", nil, 200).Body.String(), `name="environment"`) {
		t.Fatal("removed screen field offered for edit")
	}
	wf := workflow.Workflow{ID: store.NewID("wf"), Name: "Environment transition", Transitions: []workflow.Transition{{ID: "1", Name: "Create", Type: workflow.TransitionInitial, To: "st_todo"}, {ID: "21", Name: "Start with environment", From: []string{"st_todo"}, To: "st_inprogress", Screen: &workflow.Rule{ID: "env-screen", RuleKey: workflow.RuleTransitionScreen, Parameters: map[string]string{"fields": "environment"}}}}}
	if err = f.store.CreateWorkflow(ctx, ws, wf); err != nil {
		t.Fatal(err)
	}
	if err = f.store.AssignWorkflowToProject(ctx, ws, project.ID, wf.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(web("GET", "/browse/"+created.Key, nil, 200).Body.String(), `name="field_environment"`) {
		t.Fatal("transition Environment control missing")
	}
	call("POST", "/rest/api/3/issue/"+created.Key+"/transitions", `{"transition":{"id":"21"},"fields":{"environment":"invalid text"}}`, 400)
	if read().Status.ID != "st_todo" {
		t.Fatal("invalid transition changed status")
	}
	call("POST", "/rest/api/3/issue/"+created.Key+"/transitions", `{"transition":{"id":"21"},"fields":{"environment":`+source+`}}`, 204)
	if read().Status.ID != "st_inprogress" || adf.PlainText(read().Fields["environment"]) != "Production Linux" {
		t.Fatal("transition Environment not persisted")
	}
	permissionScheme, err := f.store.CreatePermissionScheme(ctx, ws, f.ownerID, "Read only Environment", "", []store.PermissionGrantInput{{Permission: "BROWSE_PROJECTS", HolderType: "anyone"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.store.AssignPermissionScheme(ctx, ws, f.ownerID, project.ID, permissionScheme.ID); err != nil {
		t.Fatal(err)
	}
	denied := httptest.NewRequest("POST", "/issues/"+created.Key+"/edit", strings.NewReader(url.Values{"summary": {"Environment journey"}, "environment": {"Forbidden change"}}.Encode()))
	denied.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	denied.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.otherID]})
	deniedResponse := httptest.NewRecorder()
	f.handler.EditIssue(deniedResponse, denied, created.Key)
	if deniedResponse.Code != 403 || adf.PlainText(read().Fields["environment"]) != "Production Linux" {
		t.Fatalf("reader changed Environment: %d %s", deniedResponse.Code, deniedResponse.Body.String())
	}
	var count int
	if err = f.store.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_id=$2 AND payload->'diff' ? 'environment'`, ws, read().ID).Scan(&count); err != nil || count < 3 {
		t.Fatalf("missing environment history: %d %v", count, err)
	}
}
