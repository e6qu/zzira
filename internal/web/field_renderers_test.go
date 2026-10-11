package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/api3"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestFieldRenderersRoundTripAcrossConfigurationViewsAndAPI(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	project, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: "FRT", Name: "Renderer work", LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, query := range []string{`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, query, ws); err != nil {
				t.Error(err)
			}
		}
	})
	number, err := f.store.NextCustomFieldNumber(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fieldID := fmt.Sprintf("customfield_%d", number)
	if _, err = f.store.CreateWorkspaceCustomFieldOfKind(ctx, ws, fieldID, "Rendered notes", models.CustomFieldText, models.CustomFieldTypeKeys[models.CustomFieldText], ""); err != nil {
		t.Fatal(err)
	}
	description := json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Rich description","marks":[{"type":"strong"}]}]}]}`)
	issue, _, err := f.store.CreateIssue(ctx, f.ownerID, project.ID, "Renderer journey", description, "st_todo", "it_task", "pr_medium", "", nil, map[string]json.RawMessage{fieldID: json.RawMessage(`"*Bold notes* <script>alert(1)</script>"`)}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var configID string
	if err = f.store.Pool.QueryRow(ctx, `SELECT mapping.configuration_id::text FROM project_field_configuration_schemes assigned JOIN field_configuration_scheme_items mapping ON mapping.scheme_id=assigned.scheme_id WHERE assigned.project_id=$1 AND mapping.issue_type_id=$2`, project.ID, store.DefaultIssueTypeMapping).Scan(&configID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.Pool.Exec(ctx, `INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, f.ownerID, store.HashToken(f.ownerID)); err != nil {
		t.Fatal(err)
	}
	api := &api3.Handler{Store: f.store, Commands: f.handler.Commands, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	callAPI := func(method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetBasicAuth(f.ownerID+"@example.invalid", f.ownerID)
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	path := "/rest/api/3/fieldconfiguration/" + configID + "/fields"
	callAPI("PUT", path, `{"fieldConfigurationItems":[{"id":"description","renderer":"text-renderer"},{"id":"`+fieldID+`","renderer":"wiki-renderer"}]}`, 204)
	readRules := func() map[string]models.FieldBehaviour {
		t.Helper()
		rules, err := f.store.ResolveFieldBehaviour(ctx, ws, project.ID, "it_task")
		if err != nil {
			t.Fatal(err)
		}
		return rules
	}
	rules := readRules()
	if rules["description"].Renderer != "jira-text-renderer" || rules[fieldID].Renderer != "atlassian-wiki-renderer" {
		t.Fatalf("renderer aliases lost: %+v", rules)
	}
	callAPI("PUT", path, `{"fieldConfigurationItems":[{"id":"`+fieldID+`","description":"Only help changed"}]}`, 204)
	if readRules()[fieldID].Renderer != "atlassian-wiki-renderer" {
		t.Fatal("partial update cleared renderer")
	}
	callAPI("PUT", path, `{"fieldConfigurationItems":[{"id":"description","renderer":"atlassian-wiki-renderer"},{"id":"`+fieldID+`","renderer":"unknown-plugin"}]}`, 400)
	if readRules()["description"].Renderer != "jira-text-renderer" {
		t.Fatal("refused batch partially committed")
	}
	if !strings.Contains(callAPI("GET", path, "", 200).Body.String(), `"renderer":"atlassian-wiki-renderer"`) {
		t.Fatal("legacy configuration read dropped renderer")
	}
	expanded := callAPI("GET", "/rest/api/3/issue/"+issue.Key+"?fields=description,"+fieldID+"&expand=renderedFields", "", 200)
	var bean struct {
		Rendered map[string]string `json:"renderedFields"`
	}
	if err = json.Unmarshal(expanded.Body.Bytes(), &bean); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bean.Rendered["description"], "<strong>") || !strings.Contains(bean.Rendered[fieldID], "<strong>Bold notes</strong>") || strings.Contains(bean.Rendered[fieldID], "<script>") {
		t.Fatalf("API ignored or executed renderer: %+v", bean.Rendered)
	}
	browser := func(preview bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/browse/"+issue.Key, nil)
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
		w := httptest.NewRecorder()
		if preview {
			f.handler.IssuePreview(w, r, issue.Key)
		} else {
			f.handler.BrowseIssue(w, r, issue.Key)
		}
		if w.Code != 200 {
			t.Fatalf("issue view %d: %s", w.Code, w.Body.String())
		}
		return w
	}
	page := browser(false).Body.String()
	if strings.Contains(page, "<strong>Rich description</strong>") || !strings.Contains(page, "<strong>Bold notes</strong>") || strings.Contains(page, "<script>alert(1)</script>") {
		t.Fatal("browser ignored or executed renderer")
	}
	if strings.Contains(browser(true).Body.String(), "<strong>Rich description</strong>") {
		t.Fatal("preview ignored plain text")
	}
	copied, err := f.store.CopyFieldConfiguration(ctx, ws, f.ownerID, configID)
	if err != nil {
		t.Fatal(err)
	}
	items, err := f.store.FieldConfigurationItems(ctx, ws, copied.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item.FieldID == fieldID {
			found = item.Renderer == "atlassian-wiki-renderer"
		}
	}
	if !found {
		t.Fatal("copy lost renderer-only rules")
	}
	post := func(user, value string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "/settings/field-configurations/"+configID, strings.NewReader(url.Values{"action": {"set-renderer"}, "fieldId": {"description"}, "renderer": {value}}.Encode()))
		r.SetPathValue("id", configID)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[user]})
		w := httptest.NewRecorder()
		f.handler.FieldConfigurationMutation(w, r)
		if w.Code != want {
			t.Fatalf("renderer write %d want %d: %s", w.Code, want, w.Body.String())
		}
		return w
	}
	post(f.otherID, "atlassian-wiki-renderer", 403)
	refused := post(f.ownerID, "unavailable-plugin", 400)
	if !strings.HasPrefix(refused.Header().Get("Content-Type"), "text/html") || !strings.Contains(refused.Body.String(), `value="unavailable-plugin" selected`) {
		t.Fatal("refusal lost renderer choice")
	}
	post(f.ownerID, "atlassian-wiki-renderer", 303)
	if !strings.Contains(browser(false).Body.String(), "<strong>Rich description</strong>") || !strings.Contains(browser(true).Body.String(), "<strong>Rich description</strong>") {
		t.Fatal("saved wiki renderer not used after reload")
	}
	callAPI("PUT", path, `{"fieldConfigurationItems":[{"id":"`+fieldID+`","renderer":""}]}`, 204)
	if readRules()[fieldID].Renderer != "" {
		t.Fatal("explicit renderer reset did not persist")
	}
	committed, err := f.store.IssueByIDOrKey(ctx, ws, issue.ID)
	if err != nil || string(committed.Description) != string(issue.Description) || string(committed.Fields[fieldID]) != string(issue.Fields[fieldID]) {
		t.Fatal("rendering changed stored content")
	}
}
