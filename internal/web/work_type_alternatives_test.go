package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/api3"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

func TestWorkTypeAlternativesRequireCompatibleProjectConfigurations(t *testing.T) {
	f := newAPITokenFixture(t)
	ctx, ws := context.Background(), f.handler.WorkspaceSlug
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM issues WHERE workspace_id=$1`, `DELETE FROM projects WHERE workspace_id=$1`, `DELETE FROM workflow_schemes WHERE workspace_id=$1`, `DELETE FROM workflows WHERE workspace_id=$1`} {
			if _, err := f.store.Pool.Exec(ctx, q, ws); err != nil {
				t.Error(err)
			}
		}
	})
	kinds := map[string]models.IssueType{}
	ids := []string{}
	for _, name := range []string{"Source", "Compatible", "Wrong workflow", "Wrong fields", "Wrong screen", "Missing from project"} {
		kind, err := f.store.CreateIssueType(ctx, ws, name, "", "standard", nil)
		if err != nil {
			t.Fatal(err)
		}
		kinds[name] = kind
		ids = append(ids, kind.ID)
	}
	projects := []*models.Project{}
	for _, key := range []string{"ALT", "ALS"} {
		p, err := f.store.CreateProject(ctx, f.ownerID, models.Project{WorkspaceID: ws, Key: key, Name: key, LeadAccountID: f.ownerID, AssigneeType: "PROJECT_LEAD", ProjectTypeKey: "software"}, "kanban")
		if err != nil {
			t.Fatal(err)
		}
		projects = append(projects, p)
	}
	for n, p := range projects {
		offered := ids
		if n == 1 {
			offered = ids[:len(ids)-1]
		}
		scheme, err := f.store.CreateIssueTypeScheme(ctx, ws, "Replacement types "+p.Key, "", kinds["Source"].ID, offered)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.AssignIssueTypeScheme(ctx, ws, scheme, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	otherWorkflow := workflow.Default()
	otherWorkflow.ID = store.NewID("wf")
	otherWorkflow.Name = "Different replacement workflow"
	if err := f.store.CreateWorkflow(ctx, ws, otherWorkflow); err != nil {
		t.Fatal(err)
	}
	wfScheme, err := f.store.CreateWorkflowScheme(ctx, ws, f.ownerID, workflow.Scheme{Name: "Replacement workflows", DefaultWorkflowID: "wf_default", IssueTypeMappings: map[string]string{kinds["Wrong workflow"].ID: otherWorkflow.ID, kinds["Compatible"].ID: "wf_default"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AssignWorkflowScheme(ctx, ws, f.ownerID, projects[0].ID, wfScheme.ID); err != nil {
		t.Fatal(err)
	}
	configuration, err := f.store.CreateFieldConfiguration(ctx, ws, f.ownerID, "Different replacement fields", "")
	if err != nil {
		t.Fatal(err)
	}
	var fieldScheme, defaultConfig string
	if err := f.store.Pool.QueryRow(ctx, `SELECT assigned.scheme_id::text,item.configuration_id::text FROM project_field_configuration_schemes assigned JOIN field_configuration_scheme_items item ON item.scheme_id=assigned.scheme_id AND item.issue_type_id=$2 WHERE assigned.project_id=$1`, projects[0].ID, store.DefaultIssueTypeMapping).Scan(&fieldScheme, &defaultConfig); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(ctx, `INSERT INTO field_configuration_scheme_items(workspace_id,scheme_id,issue_type_id,configuration_id) VALUES($1,$2,$3,$4),($1,$2,$5,$6)`, ws, fieldScheme, kinds["Wrong fields"].ID, configuration.ID, kinds["Compatible"].ID, defaultConfig); err != nil {
		t.Fatal(err)
	}
	screen, err := f.store.CreateScreen(ctx, ws, f.ownerID, "Different replacement screen", "")
	if err != nil {
		t.Fatal(err)
	}
	wrongScreen, err := f.store.CreateScreenScheme(ctx, ws, f.ownerID, "Different replacement screen scheme", "", map[string]string{"default": screen.ID})
	if err != nil {
		t.Fatal(err)
	}
	var typeScreenScheme, defaultScreenScheme string
	if err := f.store.Pool.QueryRow(ctx, `SELECT assigned.scheme_id::text,item.screen_scheme_id::text FROM project_issue_type_screen_schemes assigned JOIN issue_type_screen_scheme_items item ON item.scheme_id=assigned.scheme_id AND item.issue_type_id=$2 WHERE assigned.project_id=$1`, projects[0].ID, store.DefaultIssueTypeMapping).Scan(&typeScreenScheme, &defaultScreenScheme); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool.Exec(ctx, `INSERT INTO issue_type_screen_scheme_items(workspace_id,scheme_id,issue_type_id,screen_scheme_id) VALUES($1,$2,$3,$4),($1,$2,$5,$6)`, ws, typeScreenScheme, kinds["Wrong screen"].ID, wrongScreen.ID, kinds["Compatible"].ID, defaultScreenScheme); err != nil {
		t.Fatal(err)
	}
	alternatives, err := f.store.AlternativeIssueTypes(ctx, ws, kinds["Source"].ID)
	if err != nil || len(alternatives) != 1 || alternatives[0].ID != kinds["Compatible"].ID {
		t.Fatalf("replacement choices: %+v %v", alternatives, err)
	}
	batched, err := f.store.AlternativeIssueTypeIDs(ctx, ws, []string{kinds["Source"].ID, kinds["Compatible"].ID})
	if err != nil || len(batched[kinds["Source"].ID]) != 1 || batched[kinds["Source"].ID][0] != kinds["Compatible"].ID {
		t.Fatalf("batched choices: %+v %v", batched, err)
	}
	if _, err := f.store.Pool.Exec(ctx, `INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, f.ownerID, store.HashToken(f.ownerID)); err != nil {
		t.Fatal(err)
	}
	api := &api3.Handler{Store: f.store, Commands: f.handler.Commands, WorkspaceSlug: ws, BaseURL: "https://zzira.test"}
	r := httptest.NewRequest(http.MethodGet, "/rest/api/3/issuetype/"+kinds["Source"].ID+"/alternatives", nil)
	r.SetBasicAuth(f.ownerID+"@example.invalid", f.ownerID)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("REST %d: %s", w.Code, w.Body.String())
	}
	var beans []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &beans); err != nil || len(beans) != 1 {
		t.Fatalf("REST choices: %s %v", w.Body.String(), err)
	}
	r = httptest.NewRequest(http.MethodGet, "/settings/work-types", nil)
	r.AddCookie(&http.Cookie{Name: "zzira_session", Value: f.tokens[f.ownerID]})
	w = httptest.NewRecorder()
	f.handler.WorkTypesPage(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("settings %d", w.Code)
	}
	_, selectHTML, ok := strings.Cut(w.Body.String(), `id="work-type-alternative-`+kinds["Source"].ID+`"`)
	if !ok {
		t.Fatal("replacement picker missing")
	}
	selectHTML, _, _ = strings.Cut(selectHTML, "</select>")
	if !strings.Contains(selectHTML, `value="`+kinds["Compatible"].ID+`"`) {
		t.Fatal("compatible choice missing")
	}
	for _, name := range []string{"Wrong workflow", "Wrong fields", "Wrong screen", "Missing from project"} {
		if strings.Contains(selectHTML, `value="`+kinds[name].ID+`"`) {
			t.Fatalf("picker offers %s", name)
		}
	}
	issue, _, err := f.store.CreateIssue(ctx, f.ownerID, projects[0].ID, "Replacement journey", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", kinds["Source"].ID, "pr_medium", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Wrong workflow", "Wrong fields", "Wrong screen", "Missing from project"} {
		if err := f.store.DeleteIssueType(ctx, ws, f.ownerID, kinds["Source"].ID, kinds[name].ID); !errors.Is(err, store.ErrIssueMetadataValidation) {
			t.Fatalf("incompatible deletion %s: %v", name, err)
		}
		current, err := f.store.IssueByIDOrKey(ctx, ws, issue.Key)
		if err != nil || current.IssueType.ID != kinds["Source"].ID {
			t.Fatalf("refused deletion changed work: %+v %v", current, err)
		}
	}
	if err := f.store.DeleteIssueType(ctx, ws, f.ownerID, kinds["Source"].ID, kinds["Compatible"].ID); err != nil {
		t.Fatal(err)
	}
	current, err := f.store.IssueByIDOrKey(ctx, ws, issue.Key)
	if err != nil || current.IssueType.ID != kinds["Compatible"].ID {
		t.Fatalf("replacement not persisted: %+v %v", current, err)
	}
}
