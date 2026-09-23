package api3

import (
	"context"
	"encoding/json"
	"net/http"
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

// An inventory with more in it than a flat list: object types that sit under
// one another, and the references between objects that AQL walks.
func TestAssetsObjectTypeHierarchyAndReferences(t *testing.T) {
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
	workspaceID, adminID := store.NewID("ws"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Assets hierarchy test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Assets admin')`, adminID, adminID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'admin')`, workspaceID, adminID)
	exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, adminID, store.HashToken(adminID))
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM api_tokens WHERE user_id=$1`, adminID)
		exec(`DELETE FROM users WHERE id=$1`, adminID)
	})
	blobs, err := attachments.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service := &commands.Service{Store: st, Blobs: blobs}
	handler := &Handler{Store: st, Commands: service, Blobs: blobs, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(method, path, body string, want int) string {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(adminID+"@example.test", adminID)
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
	projectKey := "AH" + strconv.FormatInt(time.Now().UnixNano()%1000, 10)
	project, err := service.CreateProject(ctx, adminID, workspaceID, commands.CreateProjectInput{Key: projectKey, Name: "Assets hierarchy", LeadAccountID: adminID, ProjectTypeKey: "service_desk", ProjectTemplateKey: "com.atlassian.servicedesk:simplified-it-service-management"})
	if err != nil {
		t.Fatal(err)
	}
	var deskID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM service_desks WHERE project_id=$1`, project.ID).Scan(&deskID); err != nil {
		t.Fatal(err)
	}
	var workspaces struct {
		Values []struct {
			WorkspaceID string `json:"workspaceId"`
		} `json:"values"`
	}
	if err := json.Unmarshal([]byte(call("GET", "/rest/servicedeskapi/assets/workspace", "", 200)), &workspaces); err != nil || len(workspaces.Values) != 1 {
		t.Fatalf("assets workspaces = %+v, %v", workspaces, err)
	}
	assets := "/jsm/assets/workspace/" + workspaces.Values[0].WorkspaceID + "/v1"

	// Infrastructure, with laptops beneath it, and services beside both.
	created := func(body string) string {
		t.Helper()
		var bean struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(call(http.MethodPost, assets+"/objectschema/create", body, http.StatusCreated)), &bean); err != nil {
			t.Fatal(err)
		}
		return bean.ID
	}
	infrastructure := created(`{"name":"Infrastructure","objectSchemaKey":"INF","serviceDeskId":"` + deskID + `","attributes":[{"id":"tier","name":"Tier"}]}`)
	laptops := created(`{"name":"Laptops","objectSchemaKey":"LAP","serviceDeskId":"` + deskID + `","parentObjectTypeId":"` + infrastructure + `","attributes":[{"id":"tier","name":"Tier"}]}`)
	services := created(`{"name":"Business services","objectSchemaKey":"SVC","serviceDeskId":"` + deskID + `","attributes":[{"id":"tier","name":"Tier"}]}`)
	if bean := call("GET", assets+"/objectschema/"+laptops, "", 200); !strings.Contains(bean, `"parentObjectTypeId":"`+infrastructure+`"`) {
		t.Fatalf("the laptops type = %s", bean)
	}
	// A flat list of an object type is the type and everything beneath it.
	flat := call("GET", assets+"/objectschema/"+infrastructure+"/objecttypes/flat", "", 200)
	if !strings.Contains(flat, `"name":"Infrastructure"`) || !strings.Contains(flat, `"name":"Laptops"`) || strings.Contains(flat, `"name":"Business services"`) {
		t.Fatalf("flat object types = %s", flat)
	}

	object := func(schemaID, key, label, tier string) *models.ServiceAssetObject {
		t.Helper()
		saved, err := service.SaveServiceAssetObject(ctx, adminID, workspaceID, deskID, models.ServiceAssetObject{SchemaID: schemaID, Key: key, Label: label, Values: map[string]string{"tier": tier}, X: 40, Y: 40})
		if err != nil {
			t.Fatal(err)
		}
		return saved
	}
	database := object(infrastructure, "INF-1", "Ledger database", "1")
	laptop := object(laptops, "LAP-1", "Asha's laptop", "3")
	checkout := object(services, "SVC-1", "Checkout", "1")
	if _, err := service.CreateServiceAssetRelationship(ctx, adminID, workspaceID, deskID, models.ServiceAssetRelationship{Relationship: "runs on", From: models.ServiceAssetObject{ID: checkout.ID}, To: models.ServiceAssetObject{ID: database.ID}}); err != nil {
		t.Fatal(err)
	}

	search := func(query string) string {
		t.Helper()
		body, err := json.Marshal(map[string]string{"qlQuery": query})
		if err != nil {
			t.Fatal(err)
		}
		return call(http.MethodPost, assets+"/object/navlist/aql", string(body), http.StatusOK)
	}
	// An object type and everything beneath it: the database and the laptop,
	// not the service beside them.
	beneath := search(`objectType IN objectTypeAndChildren("Infrastructure")`)
	if !strings.Contains(beneath, `"objectKey":"INF-1"`) || !strings.Contains(beneath, `"objectKey":"LAP-1"`) || strings.Contains(beneath, `"objectKey":"SVC-1"`) {
		t.Fatalf("objectTypeAndChildren = %s", beneath)
	}
	if byKey := search(`objectType IN objectTypeAndChildren("INF")`); !strings.Contains(byKey, `"objectKey":"LAP-1"`) {
		t.Fatalf("objectTypeAndChildren by key = %s", byKey)
	}
	if only := search(`objectType = "Infrastructure"`); !strings.Contains(only, `"objectKey":"INF-1"`) || strings.Contains(only, `"objectKey":"LAP-1"`) {
		t.Fatalf("one object type = %s", only)
	}
	// A reference walked by name, from one end and from the other.
	if runsOn := search(`"runs on".Name = "Ledger database"`); !strings.Contains(runsOn, `"objectKey":"SVC-1"`) || strings.Contains(runsOn, `"objectKey":"INF-1"`) {
		t.Fatalf("a dotted path = %s", runsOn)
	}
	if inbound := search(`inboundReferences(objectType = "Business services")`); !strings.Contains(inbound, `"objectKey":"INF-1"`) || strings.Contains(inbound, `"objectKey":"SVC-1"`) {
		t.Fatalf("inboundReferences = %s", inbound)
	}
	if outbound := search(`outboundReferences(Tier = "1")`); !strings.Contains(outbound, `"objectKey":"SVC-1"`) || strings.Contains(outbound, `"objectKey":"LAP-1"`) {
		t.Fatalf("outboundReferences = %s", outbound)
	}
	// ORDER BY says which object comes first: by name, backwards, the ledger
	// database leads and the laptop trails.
	ordered := search(`Tier IS NOT EMPTY ORDER BY Name DESC`)
	places := func(body string, keys ...string) bool {
		previous := -1
		for _, key := range keys {
			at := strings.Index(body, `"objectKey":"`+key+`"`)
			if at < 0 || at < previous {
				return false
			}
			previous = at
		}
		return true
	}
	if !places(ordered, "INF-1", "SVC-1", "LAP-1") {
		t.Fatalf("ORDER BY Name DESC = %s", ordered)
	}
	if forwards := search(`Tier IS NOT EMPTY ORDER BY Name`); !places(forwards, "LAP-1", "SVC-1", "INF-1") {
		t.Fatalf("ORDER BY Name = %s", forwards)
	}

	// An object type moves, and never under one of its own children.
	if refused := call(http.MethodPut, assets+"/objecttype/"+infrastructure, `{"parentObjectTypeId":"`+laptops+`"}`, http.StatusBadRequest); !strings.Contains(refused, "beneath one of its own children") {
		t.Fatalf("a cycle = %s", refused)
	}
	if refused := call(http.MethodPut, assets+"/objecttype/"+laptops, `{"parentObjectTypeId":"`+laptops+`"}`, http.StatusBadRequest); !strings.Contains(refused, "under itself") {
		t.Fatalf("its own parent = %s", refused)
	}
	call(http.MethodPut, assets+"/objecttype/"+laptops, `{"parentObjectTypeId":"`+services+`"}`, http.StatusOK)
	if moved := search(`objectType IN objectTypeAndChildren("Business services")`); !strings.Contains(moved, `"objectKey":"LAP-1"`) {
		t.Fatalf("after the move = %s", moved)
	}
	call(http.MethodPut, assets+"/objecttype/"+laptops, `{"parentObjectTypeId":""}`, http.StatusOK)
	if top := call("GET", assets+"/objectschema/"+laptops, "", 200); !strings.Contains(top, `"parentObjectTypeId":null`) {
		t.Fatalf("back at the top = %s", top)
	}
	_ = laptop
}
