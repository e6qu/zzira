package api3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

func TestFilterAdministrationContractJourney(t *testing.T) {
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
	workspaceID := store.NewID("ws")
	ownerID := store.NewID("usr")
	memberID := store.NewID("usr")
	projectID := store.NewID("prj")
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Filter contract')`, workspaceID)
	for _, user := range []struct {
		id   string
		role string
	}{{ownerID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`,
			user.id, user.id+"@example.test", strings.TrimPrefix(user.id, "usr_"))
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, user.id, user.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, user.id, store.HashToken(user.id))
	}
	exec(`INSERT INTO projects(id,workspace_id,key,name) VALUES($1,$2,'FLT','Filter project')`, projectID, workspaceID)
	var groupID string
	if err = st.Pool.QueryRow(ctx, `
		INSERT INTO groups(directory_id,name)
		SELECT d.id,$2 FROM directories d JOIN sites s ON s.organization_id=d.organization_id
		WHERE s.workspace_id=$1 ORDER BY d.created_at LIMIT 1 RETURNING id::TEXT`,
		workspaceID, "filter-reviewers-"+memberID).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO group_members(group_id,user_id) VALUES($1,$2)`, groupID, memberID)
	t.Cleanup(func() {
		exec(`DELETE FROM filters WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM groups WHERE id=$1`, groupID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, user := range []string{ownerID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, user)
			exec(`DELETE FROM users WHERE id=$1`, user)
		}
	})

	handler := &Handler{Store: st, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(userID, method, path string, body any, want int) any {
		t.Helper()
		var requestBody string
		if body != nil {
			raw, marshalErr := json.Marshal(body)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			requestBody = string(raw)
		}
		request := httptest.NewRequest(method, path, strings.NewReader(requestBody))
		request.SetBasicAuth(userID+"@example.test", userID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		if response.Code == http.StatusNoContent {
			return nil
		}
		var value any
		if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
			t.Fatalf("decode %s %s: %v: %s", method, path, err, response.Body.String())
		}
		return value
	}
	object := func(value any) map[string]any {
		t.Helper()
		result, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("expected object, got %#v", value)
		}
		return result
	}

	call(ownerID, http.MethodPut, "/rest/api/3/filter/defaultShareScope", map[string]any{"scope": "PRIVATE"}, http.StatusOK)
	created := object(call(ownerID, http.MethodPost, "/rest/api/3/filter", map[string]any{
		"name": "Release readiness", "jql": "project = FLT", "description": "Release gate",
		"sharePermissions": []any{}, "editPermissions": []any{},
	}, http.StatusOK))
	filterID := created["id"].(string)
	call(memberID, http.MethodGet, "/rest/api/3/filter/"+filterID, nil, http.StatusBadRequest)
	subscription, err := st.SaveFilterSubscription(ctx, workspaceID, ownerID, filterID, "0 8 * * *", []string{ownerID, memberID})
	if err != nil {
		t.Fatal(err)
	}
	withSubscription := object(call(ownerID, http.MethodGet, "/rest/api/3/filter/"+filterID, nil, http.StatusOK))
	subscriptions := object(withSubscription["subscriptions"])
	if subscriptions["size"] != float64(1) || len(subscriptions["items"].([]any)) != 1 {
		t.Fatalf("filter subscriptions = %#v", subscriptions)
	}
	if err := st.DeleteFilterSubscription(ctx, workspaceID, memberID, filterID, subscription.ID); !errors.Is(err, store.ErrFilterPermission) {
		t.Fatalf("non-owner deleted subscription: %v", err)
	}
	if err := st.DeleteFilterSubscription(ctx, workspaceID, ownerID, filterID, subscription.ID); err != nil {
		t.Fatal(err)
	}

	created = object(call(ownerID, http.MethodPut, "/rest/api/3/filter/"+filterID+"/favourite", nil, http.StatusOK))
	if created["favourite"] != true || created["favouritedCount"] != float64(1) {
		t.Fatalf("favorite response = %#v", created)
	}
	share := object(call(ownerID, http.MethodPost, "/rest/api/3/filter/"+filterID+"/permission", map[string]any{
		"type": "group", "groupId": groupID,
	}, http.StatusCreated))
	permissionID := int64(share["id"].(float64))
	call(memberID, http.MethodGet, "/rest/api/3/filter/"+filterID, nil, http.StatusOK)
	call(ownerID, http.MethodGet, fmt.Sprintf("/rest/api/3/filter/%s/permission/%d", filterID, permissionID), nil, http.StatusOK)

	call(ownerID, http.MethodPut, "/rest/api/3/filter/"+filterID+"/columns", map[string]any{
		"columns": []string{"key", "summary", "status"},
	}, http.StatusOK)
	columns := call(memberID, http.MethodGet, "/rest/api/3/filter/"+filterID+"/columns", nil, http.StatusOK).([]any)
	if len(columns) != 3 || object(columns[0])["value"] != "key" {
		t.Fatalf("columns = %#v", columns)
	}

	call(ownerID, http.MethodPost, "/rest/api/3/filter/"+filterID+"/permission", map[string]any{
		"type": "user", "accountId": memberID, "rights": 2,
	}, http.StatusCreated)
	updated := object(call(memberID, http.MethodPut, "/rest/api/3/filter/"+filterID, map[string]any{
		"name": "Release readiness shared", "jql": "project = FLT AND status != Done", "description": "Shared release gate",
	}, http.StatusOK))
	if updated["name"] != "Release readiness shared" {
		t.Fatal(updated)
	}
	call(memberID, http.MethodPut, "/rest/api/3/filter/"+filterID, map[string]any{
		"name": "Release readiness shared", "jql": "project = FLT",
		"sharePermissions": []any{},
	}, http.StatusForbidden)

	page := object(call(memberID, http.MethodGet, "/rest/api/3/filter/search?filterName=readiness&isSubstringMatch=true&orderBy=-name", nil, http.StatusOK))
	if page["total"] != float64(1) || len(page["values"].([]any)) != 1 {
		t.Fatalf("filter search = %#v", page)
	}
	favourites := call(ownerID, http.MethodGet, "/rest/api/3/filter/favourite", nil, http.StatusOK).([]any)
	if len(favourites) != 1 {
		t.Fatalf("favorites = %#v", favourites)
	}

	call(ownerID, http.MethodPut, "/rest/api/3/filter/"+filterID+"/owner", map[string]any{"accountId": memberID}, http.StatusNoContent)
	call(ownerID, http.MethodDelete, "/rest/api/3/filter/"+filterID, nil, http.StatusBadRequest)
	call(memberID, http.MethodDelete, "/rest/api/3/filter/"+filterID, nil, http.StatusNoContent)

	scope := object(call(ownerID, http.MethodPut, "/rest/api/3/filter/defaultShareScope", map[string]any{"scope": "GLOBAL"}, http.StatusOK))
	if scope["scope"] != "AUTHENTICATED" {
		t.Fatalf("normalized default scope = %#v", scope)
	}
	call(ownerID, http.MethodPut, "/rest/api/3/filter/defaultShareScope", map[string]any{"scope": "PUBLIC"}, http.StatusBadRequest)
	defaultShared := object(call(ownerID, http.MethodPost, "/rest/api/3/filter", map[string]any{
		"name": "Default shared", "jql": "project = FLT",
	}, http.StatusOK))
	call(memberID, http.MethodGet, "/rest/api/3/filter/"+defaultShared["id"].(string), nil, http.StatusOK)
	var auditEvents int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events e JOIN sites s ON s.organization_id=e.organization_id WHERE s.workspace_id=$1 AND e.target_type='filter'`, workspaceID).Scan(&auditEvents); err != nil {
		t.Fatal(err)
	}
	if auditEvents < 6 {
		t.Fatalf("filter audit events = %d", auditEvents)
	}
}
