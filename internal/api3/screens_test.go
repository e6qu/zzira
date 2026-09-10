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

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestScreenContract(t *testing.T) {
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
	workspaceID, adminID, memberID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Screen contract')`, workspaceID)
	for _, identity := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Screen "+identity.id)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})

	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(user, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if user != "" {
			request.SetBasicAuth(user+"@example.test", user)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	decodeID := func(response *httptest.ResponseRecorder) string {
		t.Helper()
		var wire struct {
			ID json.RawMessage `json:"id"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &wire); err != nil || len(wire.ID) == 0 {
			t.Fatalf("id=%s err=%v body=%s", wire.ID, err, response.Body.String())
		}
		return strings.Trim(string(wire.ID), `"`)
	}

	call(memberID, http.MethodGet, "/rest/api/3/screens", "", http.StatusForbidden)

	// The workspace is provisioned with a default screen carrying one tab.
	listed := call(adminID, http.MethodGet, "/rest/api/3/screens", "", http.StatusOK)
	var page struct {
		Total  int `json:"total"`
		Values []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"values"`
	}
	if err = json.Unmarshal(listed.Body.Bytes(), &page); err != nil || page.Total != 1 || page.Values[0].Name != "Default Screen" {
		t.Fatalf("page=%+v err=%v body=%s", page, err, listed.Body.String())
	}
	defaultScreenID := fmt.Sprint(page.Values[0].ID)

	created := call(adminID, http.MethodPost, "/rest/api/3/screens", `{"name":"Release screen","description":"Fields shown during a release"}`, http.StatusCreated)
	screenID := decodeID(created)
	screenPath := "/rest/api/3/screens/" + screenID

	call(adminID, http.MethodPost, "/rest/api/3/screens", `{"name":"release SCREEN"}`, http.StatusConflict)
	call(adminID, http.MethodPost, "/rest/api/3/screens", `{"name":"  "}`, http.StatusBadRequest)
	call(adminID, http.MethodPut, screenPath, `{"name":"Release readiness","description":"Fields reviewed before release"}`, http.StatusOK)
	call(adminID, http.MethodGet, "/rest/api/3/screens?queryString=readiness", "", http.StatusOK)
	call(adminID, http.MethodGet, "/rest/api/3/screens?id="+screenID+"&maxResults=1", "", http.StatusOK)

	// Every new screen starts with one tab.
	tabs := call(adminID, http.MethodGet, screenPath+"/tabs", "", http.StatusOK)
	var tabList []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err = json.Unmarshal(tabs.Body.Bytes(), &tabList); err != nil || len(tabList) != 1 {
		t.Fatalf("tabs=%+v err=%v body=%s", tabList, err, tabs.Body.String())
	}
	firstTab := fmt.Sprint(tabList[0].ID)
	secondTab := decodeID(call(adminID, http.MethodPost, screenPath+"/tabs", `{"name":"Release checks"}`, http.StatusOK))
	call(adminID, http.MethodPost, screenPath+"/tabs", `{"name":"release CHECKS"}`, http.StatusConflict)
	call(adminID, http.MethodPut, screenPath+"/tabs/"+secondTab, `{"name":"Readiness checks"}`, http.StatusOK)

	// Moving the second tab to the front reverses the order.
	call(adminID, http.MethodPost, screenPath+"/tabs/"+secondTab+"/move/0", "", http.StatusOK)
	tabs = call(adminID, http.MethodGet, screenPath+"/tabs", "", http.StatusOK)
	tabList = nil
	if err = json.Unmarshal(tabs.Body.Bytes(), &tabList); err != nil || len(tabList) != 2 || fmt.Sprint(tabList[0].ID) != secondTab {
		t.Fatalf("tabs=%+v err=%v body=%s", tabList, err, tabs.Body.String())
	}
	call(adminID, http.MethodPost, screenPath+"/tabs/"+secondTab+"/move/5", "", http.StatusBadRequest)

	fieldsPath := screenPath + "/tabs/" + firstTab + "/fields"
	for _, field := range []string{"summary", "description", "priority"} {
		call(adminID, http.MethodPost, fieldsPath, `{"fieldId":"`+field+`"}`, http.StatusOK)
	}
	call(adminID, http.MethodPost, fieldsPath, `{"fieldId":"summary"}`, http.StatusConflict)
	call(adminID, http.MethodPost, fieldsPath, `{"fieldId":"not_a_field"}`, http.StatusBadRequest)

	assertFieldOrder := func(want ...string) {
		t.Helper()
		response := call(adminID, http.MethodGet, fieldsPath, "", http.StatusOK)
		var current []struct {
			ID string `json:"id"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &current); err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, field := range current {
			got = append(got, field.ID)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("field order got %v want %v", got, want)
		}
	}
	assertFieldOrder("summary", "description", "priority")
	call(adminID, http.MethodPost, fieldsPath+"/priority/move", `{"position":"First"}`, http.StatusOK)
	assertFieldOrder("priority", "summary", "description")
	call(adminID, http.MethodPost, fieldsPath+"/priority/move", `{"position":"Later"}`, http.StatusOK)
	assertFieldOrder("summary", "priority", "description")
	call(adminID, http.MethodPost, fieldsPath+"/priority/move", `{"position":"Last"}`, http.StatusOK)
	assertFieldOrder("summary", "description", "priority")
	call(adminID, http.MethodPost, fieldsPath+"/summary/move", `{"after":"https://zzira.test/rest/api/3/field/description"}`, http.StatusOK)
	assertFieldOrder("description", "summary", "priority")
	call(adminID, http.MethodPost, fieldsPath+"/summary/move", `{}`, http.StatusBadRequest)

	// availableFields excludes what the screen already shows.
	available := call(adminID, http.MethodGet, screenPath+"/availableFields", "", http.StatusOK)
	if strings.Contains(available.Body.String(), `"id":"summary"`) || !strings.Contains(available.Body.String(), `"id":"labels"`) {
		t.Fatal(available.Body.String())
	}

	// A field may live on only one tab of a screen.
	call(adminID, http.MethodPost, screenPath+"/tabs/"+secondTab+"/fields", `{"fieldId":"summary"}`, http.StatusConflict)

	forField := call(adminID, http.MethodGet, "/rest/api/3/field/summary/screens", "", http.StatusOK)
	if !strings.Contains(forField.Body.String(), `"id":`+screenID) {
		t.Fatal(forField.Body.String())
	}

	bulk := call(adminID, http.MethodGet, "/rest/api/3/screens/tabs?screenId="+screenID, "", http.StatusOK)
	if strings.Count(bulk.Body.String(), `"screenId"`) != 2 {
		t.Fatal(bulk.Body.String())
	}
	call(adminID, http.MethodGet, "/rest/api/3/screens/tabs", "", http.StatusOK)

	call(adminID, http.MethodDelete, fieldsPath+"/priority", "", http.StatusNoContent)
	call(adminID, http.MethodDelete, fieldsPath+"/priority", "", http.StatusNotFound)
	assertFieldOrder("description", "summary")

	// "labels" is already seeded on the default screen; "components" is not.
	call(adminID, http.MethodPost, "/rest/api/3/screens/addToDefault/labels", "", http.StatusConflict)
	call(adminID, http.MethodPost, "/rest/api/3/screens/addToDefault/components", "", http.StatusOK)
	defaultFields := call(adminID, http.MethodGet, "/rest/api/3/field/components/screens", "", http.StatusOK)
	if !strings.Contains(defaultFields.Body.String(), `"id":`+defaultScreenID) {
		t.Fatal(defaultFields.Body.String())
	}

	// A screen always keeps one tab, and the default screen cannot be deleted.
	call(adminID, http.MethodDelete, screenPath+"/tabs/"+secondTab, "", http.StatusNoContent)
	call(adminID, http.MethodDelete, screenPath+"/tabs/"+firstTab, "", http.StatusConflict)
	call(adminID, http.MethodDelete, "/rest/api/3/screens/"+defaultScreenID, "", http.StatusConflict)

	// A custom field joins the catalog, and deleting it clears it from screens.
	customField := call(adminID, http.MethodPost, "/rest/api/3/field", `{"name":"Release risk","type":"text"}`, http.StatusCreated)
	customFieldID := decodeID(customField)
	t.Cleanup(func() { exec(`DELETE FROM custom_fields WHERE id=$1`, customFieldID) })
	call(adminID, http.MethodPost, fieldsPath, `{"fieldId":"`+customFieldID+`"}`, http.StatusOK)
	if body := call(adminID, http.MethodGet, fieldsPath, "", http.StatusOK).Body.String(); !strings.Contains(body, customFieldID) {
		t.Fatal(body)
	}
	exec(`DELETE FROM custom_fields WHERE id=$1`, customFieldID)
	if body := call(adminID, http.MethodGet, fieldsPath, "", http.StatusOK).Body.String(); strings.Contains(body, customFieldID) {
		t.Fatalf("deleted custom field still on the screen: %s", body)
	}

	call(adminID, http.MethodDelete, screenPath, "", http.StatusNoContent)
	call(adminID, http.MethodPut, screenPath, `{"name":"Gone"}`, http.StatusNotFound)

	var actions int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM actions WHERE workspace_id=$1 AND entity_type LIKE 'screen%'`, workspaceID).Scan(&actions); err != nil || actions < 12 {
		t.Fatalf("actions=%d err=%v", actions, err)
	}
}
