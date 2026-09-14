package api3

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
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/workflow"
)

// TestStatusAndWorkflowUsagesFollowSchemes covers usages Jira derives from
// workflow schemes: a status used through a scheme's workflow mapping, a
// status reached only by a workflow no project uses, a scheme using a workflow
// through its draft, and workflow search ordered by creation and update.
func TestStatusAndWorkflowUsagesFollowSchemes(t *testing.T) {
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
	workspaceID, adminID := store.NewID("ws"), store.NewID("usr")
	projectKey := fmt.Sprintf("SU%06d", time.Now().UnixNano()%1000000)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Usages')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Usage admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflow_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workflows WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM statuses WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, adminID)
		exec(`DELETE FROM users WHERE id=$1`, adminID)
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(adminID+"@example.test", adminID)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	entityID := func(workflowID string) string {
		t.Helper()
		var id string
		if err := st.Pool.QueryRow(ctx, `SELECT entity_id::text FROM workflows WHERE id=$1`, workflowID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	project := map[string]any{}
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/project", `{"key":"`+projectKey+`","name":"Usages `+projectKey+`","projectTypeKey":"software","leadAccountId":"`+adminID+`","assigneeType":"PROJECT_LEAD"}`, http.StatusCreated)), &project); err != nil {
		t.Fatal(err)
	}
	projectID := fmt.Sprint(project["id"])
	var created []map[string]any
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/statuses", `{"scope":{"type":"GLOBAL"},"statuses":[{"name":"Mapped review `+projectKey+`","statusCategory":"IN_PROGRESS"},{"name":"Unused review `+projectKey+`","statusCategory":"IN_PROGRESS"}]}`, http.StatusOK)), &created); err != nil || len(created) != 2 {
		t.Fatalf("statuses = %v err=%v", created, err)
	}
	statusID := func(index int) (wire, stored string) {
		t.Helper()
		wire = created[index]["id"].(string)
		if err := st.Pool.QueryRow(ctx, `SELECT id FROM statuses WHERE jira_id::text=$1 OR id=$1`, wire).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		return wire, stored
	}
	mappedWire, mappedStored := statusID(0)
	unusedWire, unusedStored := statusID(1)

	route := func(name, status string) workflow.Workflow {
		return workflow.Workflow{ID: store.NewID("workflow"), Name: name + " " + projectKey, Transitions: []workflow.Transition{
			{ID: store.NewID("transition"), Name: "Review", From: []string{"st_todo"}, To: status},
			{ID: store.NewID("transition"), Name: "Finish", From: []string{status}, To: "st_done"},
		}}
	}
	mapped, unassigned, drafted := route("Mapped", mappedStored), route("Unassigned", unusedStored), route("Drafted", "st_inprogress")
	for _, wf := range []workflow.Workflow{mapped, unassigned, drafted} {
		if err = st.CreateWorkflow(ctx, workspaceID, wf); err != nil {
			t.Fatal(err)
		}
	}

	// The project uses the mapped workflow through its workflow scheme.
	scheme := map[string]any{}
	if err = json.Unmarshal([]byte(call(http.MethodPost, "/rest/api/3/workflowscheme", `{"name":"Usage scheme `+projectKey+`","defaultWorkflow":"`+mapped.Name+`"}`, http.StatusCreated)), &scheme); err != nil {
		t.Fatal(err)
	}
	schemeID := fmt.Sprint(scheme["id"])
	call(http.MethodPut, "/rest/api/3/workflowscheme/project", `{"projectId":"`+projectID+`","workflowSchemeId":"`+schemeID+`"}`, http.StatusNoContent)
	if usage := call(http.MethodGet, "/rest/api/3/statuses/"+mappedWire+"/projectUsages", "", http.StatusOK); !strings.Contains(usage, `"id":"`+projectID+`"`) {
		t.Fatalf("projects using the mapped status = %s", usage)
	}
	if usage := call(http.MethodGet, "/rest/api/3/statuses/"+mappedWire+"/project/"+projectID+"/issueTypeUsages", "", http.StatusOK); !strings.Contains(usage, `"values":[{`) {
		t.Fatalf("work types using the mapped status = %s", usage)
	}

	// A workflow no project uses still uses its statuses.
	usage := call(http.MethodGet, "/rest/api/3/statuses/"+unusedWire+"/workflowUsages", "", http.StatusOK)
	if !strings.Contains(usage, entityID(unassigned.ID)) || strings.Contains(usage, unassigned.ID) {
		t.Fatalf("workflows using the unassigned status = %s", usage)
	}
	if usage = call(http.MethodGet, "/rest/api/3/statuses/"+unusedWire+"/projectUsages", "", http.StatusOK); strings.Contains(usage, projectID) {
		t.Fatalf("a project uses a status only an unassigned workflow reaches: %s", usage)
	}

	// A draft of the active scheme uses the drafted workflow.
	call(http.MethodPut, "/rest/api/3/workflowscheme/"+schemeID+"/default", `{"workflow":"`+drafted.Name+`","updateDraftIfNeeded":true}`, http.StatusOK)
	if usage = call(http.MethodGet, "/rest/api/3/workflow/"+drafted.ID+"/workflowSchemes", "", http.StatusOK); !strings.Contains(usage, `"id":"`+schemeID+`"`) {
		t.Fatalf("schemes using the drafted workflow = %s", usage)
	}

	// Workflow search orders by creation and by the last published update.
	order := func(orderBy string) []string {
		t.Helper()
		var page struct {
			Values []struct {
				ID string `json:"id"`
			} `json:"values"`
		}
		if err := json.Unmarshal([]byte(call(http.MethodGet, "/rest/api/3/workflows/search?queryString="+projectKey+"&orderBy="+orderBy, "", http.StatusOK)), &page); err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, value := range page.Values {
			ids = append(ids, value.ID)
		}
		return ids
	}
	oldestFirst, newestFirst := order("created"), order("-created")
	if len(oldestFirst) != 3 || oldestFirst[0] != entityID(mapped.ID) || newestFirst[0] != entityID(drafted.ID) {
		t.Fatalf("created ordering = %v then %v", oldestFirst, newestFirst)
	}
	if updated := order("updated"); len(updated) != 3 {
		t.Fatalf("updated ordering = %v", updated)
	}
	call(http.MethodGet, "/rest/api/3/workflows/search?orderBy=position", "", http.StatusBadRequest)
}
