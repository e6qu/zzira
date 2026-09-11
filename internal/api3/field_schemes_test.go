package api3

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestFieldAssociationSchemes pins Jira's field association scheme surface: the
// scheme lifecycle, the fields it associates and their parameters, the projects
// it governs, and the field/project association endpoints. The point of the
// test is that the scheme actually governs the create form, not merely that the
// endpoints answer.
func TestFieldAssociationSchemes(t *testing.T) {
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
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Field schemes')`, workspaceID)
	for _, value := range []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Scheme user')`, value.id, value.id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, value.id, value.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, value.id, store.HashToken(value.id))
	}
	t.Cleanup(func() {
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM project_field_configuration_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM field_configuration_scheme_items WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM field_configuration_schemes WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM field_configuration_items WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM field_configurations WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM boards WHERE project_id IN (SELECT id FROM projects WHERE workspace_id=$1)`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{adminID, memberID} {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, id)
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	handler := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	call := func(userID, method, path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.SetBasicAuth(userID+"@example.test", userID)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	// Numeric Jira ids arrive as JSON numbers, so a caller comparing or
	// interpolating one works from its text form.
	text := func(value any) string {
		switch typed := value.(type) {
		case string:
			return typed
		case float64:
			return strconv.FormatInt(int64(typed), 10)
		default:
			return ""
		}
	}
	decode := func(response *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, response.Body.String())
		}
		return out
	}
	call(adminID, "POST", "/rest/api/3/project", `{"key":"FSC","name":"Scheme work","projectTypeKey":"software","leadAccountId":"`+adminID+`"}`, 201)
	createdField := call(adminID, "POST", "/rest/api/3/field", `{"name":"Compliance owner","type":"text"}`, 201)
	var field struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(createdField.Body.Bytes(), &field); err != nil || field.ID == "" {
		t.Fatalf("created field: %v %s", err, createdField.Body.String())
	}
	var projectID string
	if err := st.Pool.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND key='FSC'`, workspaceID).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	workTypes := []string{}
	rows, err := st.Pool.Query(ctx, `SELECT id FROM issue_types ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		workTypes = append(workTypes, id)
	}
	rows.Close()
	if len(workTypes) < 2 {
		t.Fatalf("expected at least two work types, got %v", workTypes)
	}
	restricted, other := workTypes[0], workTypes[1]

	// The scheme lifecycle.
	created := decode(call(adminID, "POST", "/rest/api/3/config/fieldschemes", `{"name":"Compliance scheme","description":"audited"}`, 200))
	schemeID := text(created["id"])
	if schemeID == "" {
		t.Fatalf("created scheme: %v", created)
	}
	links, _ := created["links"].(map[string]any)
	if links["associations"] == "" || links["projects"] == "" {
		t.Fatalf("scheme links: %v", created)
	}
	read := decode(call(adminID, "GET", "/rest/api/3/config/fieldschemes/"+schemeID, "", 200))
	if read["name"] != "Compliance scheme" || read["isDefault"] != false {
		t.Fatalf("scheme read: %v", read)
	}
	renamed := decode(call(adminID, "PUT", "/rest/api/3/config/fieldschemes/"+schemeID, `{"name":"Compliance scheme v2"}`, 200))
	if renamed["name"] != "Compliance scheme v2" {
		t.Fatalf("scheme update: %v", renamed)
	}
	call(adminID, "GET", "/rest/api/3/config/fieldschemes/9999999", "", 404)
	// Field administration is not open to an ordinary member.
	call(memberID, "GET", "/rest/api/3/config/fieldschemes", "", 403)

	listed := decode(call(adminID, "GET", "/rest/api/3/config/fieldschemes?query=Compliance", "", 200))
	if listed["total"].(float64) != 1 {
		t.Fatalf("scheme search: %v", listed)
	}
	if none := decode(call(adminID, "GET", "/rest/api/3/config/fieldschemes?query=nothinglikethis", "", 200)); none["total"].(float64) != 0 {
		t.Fatalf("scheme search: %v", none)
	}

	// Associating the field with the scheme, with rules.
	write := decode(call(adminID, "PUT", "/rest/api/3/config/fieldschemes/fields",
		`{"`+field.ID+`":[{"schemeIds":["`+schemeID+`"],"parameters":{"isRequired":true,"description":"Who signs off"}}]}`, 200))
	results, _ := write["results"].([]any)
	if len(results) != 1 || results[0].(map[string]any)["success"] != true {
		t.Fatalf("field write: %v", write)
	}
	// A bad item is reported against that item, not by failing the request.
	bad := decode(call(adminID, "PUT", "/rest/api/3/config/fieldschemes/fields",
		`{"customfield_404404":[{"schemeIds":["`+schemeID+`"]}]}`, 200))
	badResults, _ := bad["results"].([]any)
	if len(badResults) != 1 || badResults[0].(map[string]any)["success"] != false || badResults[0].(map[string]any)["error"] == nil {
		t.Fatalf("unknown field write: %v", bad)
	}
	parameters := decode(call(adminID, "GET", "/rest/api/3/config/fieldschemes/"+schemeID+"/fields/"+field.ID+"/parameters", "", 200))
	base, _ := parameters["parameters"].(map[string]any)
	if base["isRequired"] != true || base["description"] != "Who signs off" {
		t.Fatalf("field parameters: %v", parameters)
	}
	call(adminID, "GET", "/rest/api/3/config/fieldschemes/"+schemeID+"/fields/customfield_404404/parameters", "", 404)

	// Assigning the scheme to the project is what makes it govern the forms.
	assigned := decode(call(adminID, "PUT", "/rest/api/3/config/fieldschemes/projects",
		`{"`+schemeID+`":{"projectIds":["`+projectID+`"]}}`, 200))
	assignedResults, _ := assigned["results"].([]any)
	if len(assignedResults) != 1 || assignedResults[0].(map[string]any)["success"] != true {
		t.Fatalf("project assignment: %v", assigned)
	}
	projects := decode(call(adminID, "GET", "/rest/api/3/config/fieldschemes/"+schemeID+"/projects", "", 200))
	if projects["total"].(float64) != 1 {
		t.Fatalf("scheme projects: %v", projects)
	}
	pairs := decode(call(adminID, "GET", "/rest/api/3/config/fieldschemes/projects?projectId="+projectID, "", 200))
	pairValues, _ := pairs["values"].([]any)
	if len(pairValues) != 1 || text(pairValues[0].(map[string]any)["schemeId"]) != schemeID {
		t.Fatalf("projects with schemes: %v", pairs)
	}
	requiredMeta := call(adminID, "GET", "/rest/api/3/issue/createmeta?projectKeys=FSC&expand=projects.issuetypes.fields", "", 200)
	if !strings.Contains(requiredMeta.Body.String(), field.ID) {
		t.Fatalf("scheme field missing from createmeta: %s", requiredMeta.Body.String())
	}

	// Restricting the field to one work type takes it off the others' forms.
	call(adminID, "PUT", "/rest/api/3/config/fieldschemes/fields",
		`{"`+field.ID+`":[{"schemeIds":["`+schemeID+`"],"restrictedToWorkTypes":["`+restricted+`"],"parameters":{"isRequired":true,"description":"Only here"}}]}`, 200)
	schemeFields := decode(call(adminID, "GET", "/rest/api/3/config/fieldschemes/"+schemeID+"/fields?fieldId="+field.ID, "", 200))
	values, _ := schemeFields["values"].([]any)
	if len(values) != 1 {
		t.Fatalf("scheme fields: %v", schemeFields)
	}
	entry, _ := values[0].(map[string]any)
	restrictions, _ := entry["restrictedToWorkTypes"].([]any)
	if len(restrictions) != 1 || text(restrictions[0]) != restricted {
		t.Fatalf("restriction: %v", entry)
	}
	overrides, _ := entry["workTypeParameters"].([]any)
	if len(overrides) != 1 || overrides[0].(map[string]any)["description"] != "Only here" {
		t.Fatalf("work type parameters: %v", entry)
	}
	fieldsOnForm := map[string]bool{}
	meta := decode(call(adminID, "GET", "/rest/api/3/issue/createmeta?projectKeys=FSC&expand=projects.issuetypes.fields", "", 200))
	for _, rawProject := range meta["projects"].([]any) {
		for _, rawType := range rawProject.(map[string]any)["issuetypes"].([]any) {
			workType, _ := rawType.(map[string]any)
			formFields, _ := workType["fields"].(map[string]any)
			_, present := formFields[field.ID]
			fieldsOnForm[text(workType["id"])] = present
		}
	}
	if !fieldsOnForm[restricted] {
		t.Fatalf("restricted work type lost the field: %v", fieldsOnForm)
	}
	if fieldsOnForm[other] {
		t.Fatalf("the restriction did not take the field off the other work type: %v", fieldsOnForm)
	}

	// Cloning copies the associations, and editing the copy leaves the original.
	clone := decode(call(adminID, "POST", "/rest/api/3/config/fieldschemes/"+schemeID+"/clone",
		`{"name":"Compliance clone","description":"copy"}`, 200))
	cloneID := text(clone["id"])
	if cloneID == "" || cloneID == schemeID {
		t.Fatalf("clone: %v", clone)
	}
	cloneFields := decode(call(adminID, "GET", "/rest/api/3/config/fieldschemes/"+cloneID+"/fields?fieldId="+field.ID, "", 200))
	if cloneFields["total"].(float64) != 1 {
		t.Fatalf("clone fields: %v", cloneFields)
	}
	call(adminID, "DELETE", "/rest/api/3/config/fieldschemes/fields",
		`{"`+field.ID+`":[{"schemeIds":["`+cloneID+`"]}]}`, 200)
	if after := decode(call(adminID, "GET", "/rest/api/3/config/fieldschemes/"+cloneID+"/fields?fieldId="+field.ID, "", 200)); after["total"].(float64) != 0 {
		t.Fatalf("removal did not take the field out of the clone: %v", after)
	}
	if origin := decode(call(adminID, "GET", "/rest/api/3/config/fieldschemes/"+schemeID+"/fields?fieldId="+field.ID, "", 200)); origin["total"].(float64) != 1 {
		t.Fatalf("removing from the clone changed the original: %v", origin)
	}

	// Per-work-type parameters, and removing the override.
	call(adminID, "PUT", "/rest/api/3/config/fieldschemes/fields/parameters",
		`{"`+field.ID+`":[{"schemeIds":["`+schemeID+`"],"parameters":{"isRequired":false,"description":"Base"},"workTypeParameters":[{"workTypeId":"`+restricted+`","isRequired":true,"description":"Sharper"}]}]}`, 200)
	withOverride := decode(call(adminID, "GET", "/rest/api/3/config/fieldschemes/"+schemeID+"/fields/"+field.ID+"/parameters", "", 200))
	overrideList, _ := withOverride["workTypeParameters"].([]any)
	if len(overrideList) != 1 || overrideList[0].(map[string]any)["description"] != "Sharper" {
		t.Fatalf("work type override: %v", withOverride)
	}
	call(adminID, "DELETE", "/rest/api/3/config/fieldschemes/fields/parameters",
		`{"`+field.ID+`":[{"schemeIds":["`+schemeID+`"],"workTypeIds":["`+restricted+`"]}]}`, 204)
	// The work type returns to the scheme's fallback rules rather than losing
	// the field, so the base parameters are what it reports afterwards.
	cleared := decode(call(adminID, "GET", "/rest/api/3/config/fieldschemes/"+schemeID+"/fields/"+field.ID+"/parameters", "", 200))
	clearedBase, _ := cleared["parameters"].(map[string]any)
	if clearedBase["isRequired"] != false || clearedBase["description"] != "Base" {
		t.Fatalf("cleared parameters: %v", cleared)
	}
	if remaining, _ := cleared["workTypeParameters"].([]any); len(remaining) != 0 {
		t.Fatalf("override survived its removal: %v", cleared)
	}

	// The association endpoints work on projects rather than schemes.
	association := `{"associationContexts":[{"type":"PROJECT_ID","identifier":"` + projectID + `"}],"fields":[{"type":"FIELD_ID","identifier":"` + field.ID + `"}]}`
	call(adminID, "PUT", "/rest/api/3/field/association", association, 204)
	associated := call(adminID, "GET", "/rest/api/3/issue/createmeta?projectKeys=FSC&expand=projects.issuetypes.fields", "", 200)
	if !strings.Contains(associated.Body.String(), field.ID) {
		t.Fatalf("association did not reach the form: %s", associated.Body.String())
	}
	call(adminID, "DELETE", "/rest/api/3/field/association", association, 204)
	unassociated := call(adminID, "GET", "/rest/api/3/issue/createmeta?projectKeys=FSC&expand=projects.issuetypes.fields", "", 200)
	if strings.Contains(unassociated.Body.String(), field.ID) {
		t.Fatalf("unassociation left the field on the form: %s", unassociated.Body.String())
	}
	call(adminID, "PUT", "/rest/api/3/field/association",
		`{"associationContexts":[{"type":"CUSTOM","identifier":"x"}],"fields":[]}`, 400)

	// Deleting the clone, and the 404 afterwards.
	deleted := decode(call(adminID, "DELETE", "/rest/api/3/config/fieldschemes/"+cloneID, "", 200))
	if deleted["deleted"] != true {
		t.Fatalf("delete: %v", deleted)
	}
	call(adminID, "GET", "/rest/api/3/config/fieldschemes/"+cloneID, "", 404)
}
