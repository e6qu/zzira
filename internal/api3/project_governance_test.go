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

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

func TestProjectGovernanceLifecycle(t *testing.T) {
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
		if _, err := st.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Project governance test')`, workspaceID)
	for _, identity := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Project User')`, identity.id, identity.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM project_categories WHERE workspace_id=$1`, workspaceID)
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
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if user != "" {
			r.SetBasicAuth(user+"@example.test", user)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}

	call("", http.MethodGet, "/rest/api/3/project/type", "", http.StatusOK)
	types := call(adminID, http.MethodGet, "/rest/api/3/project/type/accessible", "", http.StatusOK)
	if !strings.Contains(types.Body.String(), `"key":"business"`) || !strings.Contains(types.Body.String(), `"key":"software"`) || !strings.Contains(types.Body.String(), `"key":"service_desk"`) {
		t.Fatal(types.Body.String())
	}
	call(adminID, http.MethodGet, "/rest/api/3/project/type/missing", "", http.StatusNotFound)

	call(memberID, http.MethodPost, "/rest/api/3/projectCategory", `{"name":"Delivery"}`, http.StatusForbidden)
	created := call(adminID, http.MethodPost, "/rest/api/3/projectCategory", `{"name":"Delivery","description":"Shipping projects"}`, http.StatusCreated)
	var category struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &category); err != nil {
		t.Fatal(err)
	}
	if category.ID == "" || category.Name != "Delivery" {
		t.Fatal(created.Body.String())
	}
	call(adminID, http.MethodPost, "/rest/api/3/projectCategory", `{"name":"delivery"}`, http.StatusConflict)
	call(memberID, http.MethodGet, "/rest/api/3/projectCategory/"+category.ID, "", http.StatusOK)
	call(adminID, http.MethodPut, "/rest/api/3/projectCategory/"+category.ID, `{"name":"Delivery portfolio","description":"Release work"}`, http.StatusOK)

	categoryNumber, err := strconv.ParseInt(category.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	projectBody := `{"key":"GOV","name":"Governance","projectTypeKey":"software","leadAccountId":"` + adminID + `","assigneeType":"PROJECT_LEAD","categoryId":` + strconv.FormatInt(categoryNumber, 10) + `}`
	projectResponse := call(adminID, http.MethodPost, "/rest/api/3/project", projectBody, http.StatusCreated)
	var project struct {
		ID int64 `json:"id"`
	}
	if err = json.Unmarshal(projectResponse.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	projectPath := strconv.FormatInt(project.ID, 10)
	got := call(memberID, http.MethodGet, "/rest/api/3/project/GOV", "", http.StatusOK)
	if !strings.Contains(got.Body.String(), `"projectCategory":{"description":"Release work"`) {
		t.Fatal(got.Body.String())
	}
	search := call(memberID, http.MethodGet, "/rest/api/3/project/search?categoryId="+category.ID, "", http.StatusOK)
	if !strings.Contains(search.Body.String(), `"key":"GOV"`) {
		t.Fatal(search.Body.String())
	}

	call(memberID, http.MethodGet, "/rest/api/3/project/GOV/properties", "", http.StatusOK)
	call(memberID, http.MethodPut, "/rest/api/3/project/GOV/properties/app.release", `{"train":"weekly"}`, http.StatusForbidden)
	call(adminID, http.MethodPut, "/rest/api/3/project/GOV/properties/app.release", `{"train":"weekly"}`, http.StatusCreated)
	call(adminID, http.MethodPut, "/rest/api/3/project/GOV/properties/app.release", `{"train":"daily"}`, http.StatusOK)
	property := call(memberID, http.MethodGet, "/rest/api/3/project/GOV/properties/app.release", "", http.StatusOK)
	if !strings.Contains(property.Body.String(), `"value":{"train":"daily"}`) {
		t.Fatal(property.Body.String())
	}
	keys := call(memberID, http.MethodGet, "/rest/api/3/project/GOV/properties", "", http.StatusOK)
	if !strings.Contains(keys.Body.String(), `"key":"app.release"`) {
		t.Fatal(keys.Body.String())
	}
	call(adminID, http.MethodPut, "/rest/api/3/project/GOV/properties/bad", `{`, http.StatusBadRequest)

	features := call(memberID, http.MethodGet, "/rest/api/3/project/GOV/features", "", http.StatusOK)
	if !strings.Contains(features.Body.String(), `"feature":"jsw.classic.reports"`) {
		t.Fatal(features.Body.String())
	}
	call(memberID, http.MethodPut, "/rest/api/3/project/GOV/features/jsw.classic.reports", `{"state":"DISABLED"}`, http.StatusForbidden)
	disabled := call(adminID, http.MethodPut, "/rest/api/3/project/GOV/features/jsw.classic.reports", `{"state":"DISABLED"}`, http.StatusOK)
	if !strings.Contains(disabled.Body.String(), `"feature":"jsw.classic.reports","imageUri":"","localisedDescription":"Inspect project flow, delivery, and trends.","localisedName":"Reports","prerequisites":[],"projectId":`+projectPath+`,"state":"DISABLED"`) {
		t.Fatal(disabled.Body.String())
	}
	call(adminID, http.MethodPut, "/rest/api/3/project/GOV/features/missing", `{"state":"ENABLED"}`, http.StatusBadRequest)

	defaultEmail := call(memberID, http.MethodGet, "/rest/api/3/project/"+projectPath+"/email", "", http.StatusOK)
	if !strings.Contains(defaultEmail.Body.String(), `"emailAddress":"jira@zzira.test"`) {
		t.Fatal(defaultEmail.Body.String())
	}
	call(memberID, http.MethodPut, "/rest/api/3/project/"+projectPath+"/email", `{"emailAddress":"delivery@example.test"}`, http.StatusForbidden)
	call(adminID, http.MethodPut, "/rest/api/3/project/"+projectPath+"/email", `{"emailAddress":"delivery@example.test"}`, http.StatusNoContent)
	customEmail := call(memberID, http.MethodGet, "/rest/api/3/project/"+projectPath+"/email", "", http.StatusOK)
	if !strings.Contains(customEmail.Body.String(), `"emailAddress":"delivery@example.test"`) {
		t.Fatal(customEmail.Body.String())
	}

	duplicateKey := call(memberID, http.MethodGet, "/rest/api/3/projectvalidate/key?key=GOV", "", http.StatusOK)
	if !strings.Contains(duplicateKey.Body.String(), `"projectKey":"A project with that project key already exists."`) {
		t.Fatal(duplicateKey.Body.String())
	}
	validKey := call(memberID, http.MethodGet, "/rest/api/3/projectvalidate/validProjectKey?key=gov", "", http.StatusOK)
	if strings.TrimSpace(validKey.Body.String()) != `"GOV2"` {
		t.Fatal(validKey.Body.String())
	}
	validName := call(memberID, http.MethodGet, "/rest/api/3/projectvalidate/validProjectName?name=Governance", "", http.StatusOK)
	if strings.TrimSpace(validName.Body.String()) != `"Governance (2)"` {
		t.Fatal(validName.Body.String())
	}

	call(adminID, http.MethodDelete, "/rest/api/3/project/GOV/properties/app.release", "", http.StatusNoContent)
	call(adminID, http.MethodPut, "/rest/api/3/project/GOV", `{"categoryId":-1}`, http.StatusOK)
	withoutCategory := call(memberID, http.MethodGet, "/rest/api/3/project/GOV", "", http.StatusOK)
	if strings.Contains(withoutCategory.Body.String(), `projectCategory`) {
		t.Fatal(withoutCategory.Body.String())
	}
	call(adminID, http.MethodPut, "/rest/api/3/project/GOV", `{"categoryId":`+category.ID+`}`, http.StatusOK)
	call(adminID, http.MethodDelete, "/rest/api/3/projectCategory/"+category.ID, "", http.StatusNoContent)
	call(memberID, http.MethodGet, "/rest/api/3/projectCategory/"+category.ID, "", http.StatusNotFound)
	withoutDeletedCategory := call(memberID, http.MethodGet, "/rest/api/3/project/GOV", "", http.StatusOK)
	if strings.Contains(withoutDeletedCategory.Body.String(), `projectCategory`) {
		t.Fatal(withoutDeletedCategory.Body.String())
	}
}
