package api3

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

func TestTheAssetsAPIServesOneSiteInventory(t *testing.T) {
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
	workspaceID, adminID, memberID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Assets API test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Assets admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Assets onlooker')`, memberID, memberID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, memberID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, memberID, store.HashToken(memberID))
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM wiki_spaces WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id IN ($1,$2)`, adminID, memberID)
		exec(`DELETE FROM users WHERE id IN ($1,$2)`, adminID, memberID)
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := &commands.Service{Store: st, Blobs: blobs}
	handler := &Handler{Store: st, Commands: service, Blobs: blobs, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	callAs := func(accountID, method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(accountID+"@example.test", accountID)
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response.Body.String()
	}
	call := func(method, path, body string, want int) string {
		return callAs(adminID, method, path, body, want)
	}

	projectKey := "AS" + strconv.FormatInt(time.Now().UnixNano()%1000, 10)
	project, err := service.CreateProject(ctx, adminID, workspaceID, commands.CreateProjectInput{Key: projectKey, Name: "Assets API", LeadAccountID: adminID, ProjectTypeKey: "service_desk", ProjectTemplateKey: "com.atlassian.servicedesk:simplified-it-service-management"})
	if err != nil {
		t.Fatal(err)
	}
	var deskID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_desks WHERE project_id=$1`, project.ID).Scan(&deskID); err != nil {
		t.Fatal(err)
	}
	schema, err := service.CreateServiceAssetSchema(ctx, adminID, workspaceID, deskID, models.ServiceAssetSchema{
		Key: "service", Name: "Business service",
		Attributes: []models.ServiceAssetAttribute{
			{Key: "tier", Name: "Service tier", Type: "select", Required: true, Options: []string{"Tier 1", "Tier 2"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	checkout, err := service.SaveServiceAssetObject(ctx, adminID, workspaceID, deskID, models.ServiceAssetObject{SchemaID: schema.ID, Key: "SVC-1", Label: "Checkout", X: 100, Y: 100, Values: map[string]string{"tier": "Tier 1"}})
	if err != nil {
		t.Fatal(err)
	}
	database, err := service.SaveServiceAssetObject(ctx, adminID, workspaceID, deskID, models.ServiceAssetObject{SchemaID: schema.ID, Key: "SVC-2", Label: "Ledger database", X: 300, Y: 100, Values: map[string]string{"tier": "Tier 2"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateServiceAssetRelationship(ctx, adminID, workspaceID, deskID, models.ServiceAssetRelationship{Relationship: "depends on", From: models.ServiceAssetObject{ID: checkout.ID}, To: models.ServiceAssetObject{ID: database.ID}}); err != nil {
		t.Fatal(err)
	}

	// The Assets API is served per Assets workspace, and the site names its
	// own through the service desk API.
	var workspaces struct {
		Values []struct {
			WorkspaceID string `json:"workspaceId"`
		} `json:"values"`
	}
	if err := json.Unmarshal([]byte(call("GET", "/rest/servicedeskapi/assets/workspace", "", 200)), &workspaces); err != nil || len(workspaces.Values) != 1 {
		t.Fatalf("assets workspaces = %+v, %v", workspaces, err)
	}
	assets := "/jsm/assets/workspace/" + workspaces.Values[0].WorkspaceID + "/v1"
	if body := call("GET", "/jsm/assets/workspace/not-a-workspace/v1/objectschema/list", "", 404); !strings.Contains(body, "does not exist") {
		t.Fatal(body)
	}

	list := call("GET", assets+"/objectschema/list", "", 200)
	if !strings.Contains(list, `"objectSchemaKey":"SERVICE"`) || !strings.Contains(list, `"objectCount":2`) {
		t.Fatal(list)
	}
	if one := call("GET", assets+"/objectschema/"+schema.ID, "", 200); !strings.Contains(one, `"name":"Business service"`) {
		t.Fatal(one)
	}
	if flat := call("GET", assets+"/objectschema/"+schema.ID+"/objecttypes/flat", "", 200); !strings.Contains(flat, `"objectSchemaId":"`+schema.ID+`"`) {
		t.Fatal(flat)
	}
	if attributes := call("GET", assets+"/objecttype/"+schema.ID+"/attributes", "", 200); !strings.Contains(attributes, `"id":"tier"`) || !strings.Contains(attributes, `"Tier 2"`) {
		t.Fatal(attributes)
	}

	// An AQL query reads the same filter language the portal's Assets fields
	// use, and answers with the objects it matches.
	found := call("POST", assets+"/object/navlist/aql", `{"qlQuery":"\"Service tier\" = \"Tier 1\""}`, 200)
	if !strings.Contains(found, `"objectKey":"SVC-1"`) || strings.Contains(found, `"objectKey":"SVC-2"`) || !strings.Contains(found, `"total":1`) {
		t.Fatal(found)
	}
	if refused := call("POST", assets+"/object/navlist/aql", `{"qlQuery":"tier ="}`, 400); !strings.Contains(refused, "Assets filter") {
		t.Fatal(refused)
	}

	created := call("POST", assets+"/object/create", `{"objectTypeId":"`+schema.ID+`","objectKey":"SVC-3","label":"Search","attributes":{"tier":"Tier 2"},"position":{"x":500,"y":100}}`, 201)
	var object struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(created), &object); err != nil || object.ID == "" {
		t.Fatalf("created object = %s, %v", created, err)
	}
	if fetched := call("GET", assets+"/object/"+object.ID, "", 200); !strings.Contains(fetched, `"label":"Search"`) || !strings.Contains(fetched, `"x":500`) {
		t.Fatal(fetched)
	}
	if updated := call("PUT", assets+"/object/"+object.ID, `{"label":"Search service"}`, 200); !strings.Contains(updated, `"label":"Search service"`) || !strings.Contains(updated, `"tier":"Tier 2"`) {
		t.Fatal(updated)
	}
	if refused := call("POST", assets+"/object/create", `{"objectTypeId":"`+schema.ID+`","objectKey":"SVC-4","label":"Broken","attributes":{"tier":"Tier 9"}}`, 400); !strings.Contains(refused, "Service tier") {
		t.Fatal(refused)
	}
	call("DELETE", assets+"/object/"+object.ID, "", 204)
	call("GET", assets+"/object/"+object.ID, "", 404)

	if references := call("GET", assets+"/object/"+checkout.ID+"/referenceinfo", "", 200); !strings.Contains(references, `"relationship":"depends on"`) || !strings.Contains(references, `"direction":"outbound"`) || !strings.Contains(references, `"objectKey":"SVC-2"`) {
		t.Fatal(references)
	}
	if references := call("GET", assets+"/object/"+database.ID+"/referenceinfo", "", 200); !strings.Contains(references, `"direction":"inbound"`) {
		t.Fatal(references)
	}
	if tickets := call("GET", assets+"/object/"+checkout.ID+"/connectedTickets", "", 200); !strings.Contains(tickets, `"total":0`) {
		t.Fatal(tickets)
	}

	imported := call("POST", assets+"/objectschema/"+schema.ID+"/import", `{"file":"Key,Label,Service tier\nSVC-1,Checkout,Tier 2\nSVC-5,Warehouse,Tier 2\n"}`, 200)
	if !strings.Contains(imported, `"created":1`) || !strings.Contains(imported, `"updated":1`) || !strings.Contains(imported, `"objectKey":"SVC-5"`) {
		t.Fatal(imported)
	}
	if after := call("GET", assets+"/object/"+checkout.ID, "", 200); !strings.Contains(after, `"tier":"Tier 2"`) {
		t.Fatal(after)
	}

	// A member of the site who does not agent this desk is told the object is
	// not there, rather than that it is hidden.
	callAs(memberID, "GET", assets+"/object/"+checkout.ID, "", 404)
	callAs(memberID, "GET", assets+"/objectschema/"+schema.ID, "", 404)
	if list := callAs(memberID, "GET", assets+"/objectschema/list", "", 200); !strings.Contains(list, `"total":0`) {
		t.Fatal(list)
	}
}
