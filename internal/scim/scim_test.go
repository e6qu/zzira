package scim

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/store"
)

// An identity provider's whole conversation with this directory: discovery,
// then people and groups created, filtered, patched, deactivated and removed.
func TestSCIMProvisioningJourney(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, adminID, memberID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr")
	for _, userID := range []string{adminID, memberID} {
		if _, err := st.Pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$1 || '@example.invalid','unused',$1)`, userID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'SCIM test')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(ctx, workspaceID, adminID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(ctx, workspaceID, memberID, "member"); err != nil {
		t.Fatal(err)
	}
	adminToken, memberToken := store.NewID("secret"), store.NewID("secret")
	if err := st.CreateAPIToken(ctx, store.NewID("tok"), adminID, store.HashToken(adminToken), "scim-test"); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAPIToken(ctx, store.NewID("tok"), memberID, store.HashToken(memberToken), "scim-test"); err != nil {
		t.Fatal(err)
	}
	organization, err := st.OrganizationByWorkspace(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var directoryID string
	if err := st.Pool.QueryRow(ctx, `SELECT id::text FROM directories WHERE organization_id=$1::uuid ORDER BY created_at LIMIT 1`, organization.ID).Scan(&directoryID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE user_id IN ($1,$2)`, adminID, memberID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id IN (SELECT user_id FROM directory_users WHERE directory_id=$1::uuid) AND id NOT IN ($2,$3)`, directoryID, adminID, memberID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organizations WHERE id::text=$1`, organization.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, adminID, memberID)
	}()

	handler := &Handler{Store: st, BaseURL: "https://zzira.example", WorkspaceSlug: workspaceID}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /scim/directory/{directoryId}/ServiceProviderConfig", handler.ServiceProviderConfig)
	mux.HandleFunc("GET /scim/directory/{directoryId}/ResourceTypes", handler.ResourceTypes)
	mux.HandleFunc("GET /scim/directory/{directoryId}/Schemas", handler.Schemas)
	for _, method := range []string{"GET", "POST"} {
		mux.HandleFunc(method+" /scim/directory/{directoryId}/Users", handler.Users)
		mux.HandleFunc(method+" /scim/directory/{directoryId}/Groups", handler.Groups)
	}
	for _, method := range []string{"GET", "PUT", "PATCH", "DELETE"} {
		mux.HandleFunc(method+" /scim/directory/{directoryId}/Users/{userId}", handler.User)
		mux.HandleFunc(method+" /scim/directory/{directoryId}/Groups/{groupId}", handler.Group)
	}
	base := "/scim/directory/" + directoryID

	call := func(token, method, path, body string, want int) map[string]any {
		t.Helper()
		var reader *bytes.Reader
		if body == "" {
			reader = bytes.NewReader(nil)
		} else {
			reader = bytes.NewReader([]byte(body))
		}
		request := httptest.NewRequest(method, path, reader)
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/scim+json")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s = %d (want %d): %s", method, path, response.Code, want, response.Body.String())
		}
		if response.Body.Len() == 0 {
			return nil
		}
		if got := response.Header().Get("Content-Type"); got != contentType {
			t.Fatalf("%s %s content type = %q", method, path, got)
		}
		var decoded map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s %s body: %v (%s)", method, path, err, response.Body.String())
		}
		return decoded
	}

	// Only an organization administrator provisions, and only into a
	// directory that exists.
	call(memberToken, http.MethodGet, base+"/Users", "", http.StatusForbidden)
	call(adminToken, http.MethodGet, "/scim/directory/00000000-0000-0000-0000-000000000000/Users", "", http.StatusNotFound)

	// A provider holds a key issued for this directory: it provisions with
	// that and nothing else, and a revoked key provisions nothing.
	keyPlain, keyHash, err := authn.NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	key, err := st.CreateDirectoryAPIKey(ctx, organization.ID, adminID, directoryID, "Okta", keyHash)
	if err != nil {
		t.Fatalf("issue a provisioning key: %v", err)
	}
	call(keyPlain, http.MethodGet, base+"/Users", "", http.StatusOK)
	call(keyPlain, http.MethodGet, "/scim/directory/00000000-0000-0000-0000-000000000000/Users", "", http.StatusForbidden)
	keys, err := st.DirectoryAPIKeys(ctx, directoryID)
	if err != nil || len(keys) != 1 || keys[0].LastUsedAt == nil {
		t.Fatalf("keys after a provider used one = %+v, %v", keys, err)
	}
	if err := st.RevokeDirectoryAPIKey(ctx, organization.ID, key.ID); err != nil {
		t.Fatalf("revoke the key: %v", err)
	}
	call(keyPlain, http.MethodGet, base+"/Users", "", http.StatusUnauthorized)
	if err := st.RevokeDirectoryAPIKey(ctx, organization.ID, key.ID); err == nil {
		t.Fatal("revoking a key twice was accepted")
	}

	// Discovery.
	config := call(adminToken, http.MethodGet, base+"/ServiceProviderConfig", "", http.StatusOK)
	if patch, _ := config["patch"].(map[string]any); patch == nil || patch["supported"] != true {
		t.Fatalf("service provider config = %v", config)
	}
	types := call(adminToken, http.MethodGet, base+"/ResourceTypes", "", http.StatusOK)
	if types["totalResults"].(float64) != 2 {
		t.Fatalf("resource types = %v", types)
	}
	if schemas := call(adminToken, http.MethodGet, base+"/Schemas", "", http.StatusOK); schemas["totalResults"].(float64) != 2 {
		t.Fatalf("schemas = %v", schemas)
	}

	// A person the provider creates.
	created := call(adminToken, http.MethodPost, base+"/Users", `{
		"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],
		"externalId":"idp-1","userName":"dana@example.test","displayName":"Dana Example",
		"name":{"givenName":"Dana","familyName":"Example"},
		"emails":[{"value":"dana@example.test","primary":true,"type":"work"}],"active":true}`, http.StatusCreated)
	userID, _ := created["id"].(string)
	if userID == "" || created["userName"] != "dana@example.test" || created["active"] != true || created["externalId"] != "idp-1" {
		t.Fatalf("created user = %v", created)
	}
	if name, _ := created["name"].(map[string]any); name == nil || name["givenName"] != "Dana" {
		t.Fatalf("created user name = %v", created["name"])
	}
	// The same userName twice is a conflict, not a second person.
	call(adminToken, http.MethodPost, base+"/Users", `{"userName":"dana@example.test","displayName":"Dana Again"}`, http.StatusConflict)

	// Filters: the ones an identity provider sends, and a refusal for one
	// this directory cannot answer.
	found := call(adminToken, http.MethodGet, base+`/Users?filter=userName+eq+%22dana@example.test%22`, "", http.StatusOK)
	if found["totalResults"].(float64) != 1 {
		t.Fatalf("filtered users = %v", found)
	}
	if external := call(adminToken, http.MethodGet, base+`/Users?filter=externalId+eq+%22idp-1%22`, "", http.StatusOK); external["totalResults"].(float64) != 1 {
		t.Fatalf("filter by externalId = %v", external)
	}
	if missing := call(adminToken, http.MethodGet, base+`/Users?filter=userName+eq+%22nobody@example.test%22`, "", http.StatusOK); missing["totalResults"].(float64) != 0 {
		t.Fatalf("filter with no match = %v", missing)
	}
	call(adminToken, http.MethodGet, base+`/Users?filter=userName+co+%22dana%22`, "", http.StatusBadRequest)

	// Paging.
	page := call(adminToken, http.MethodGet, base+"/Users?startIndex=1&count=1", "", http.StatusOK)
	if page["itemsPerPage"].(float64) != 1 || page["startIndex"].(float64) != 1 {
		t.Fatalf("page = %v", page)
	}

	// A group, with that person in it.
	group := call(adminToken, http.MethodPost, base+"/Groups", `{
		"schemas":["urn:ietf:params:scim:schemas:core:2.0:Group"],
		"externalId":"idp-group-1","displayName":"Provisioned engineers",
		"members":[{"value":"`+userID+`"}]}`, http.StatusCreated)
	groupID, _ := group["id"].(string)
	members, _ := group["members"].([]any)
	if groupID == "" || len(members) != 1 {
		t.Fatalf("created group = %v", group)
	}
	if withGroups := call(adminToken, http.MethodGet, base+"/Users/"+userID, "", http.StatusOK); len(withGroups["groups"].([]any)) != 1 {
		t.Fatalf("the person's groups = %v", withGroups["groups"])
	}

	// A provider removes one member by naming them in the path.
	call(adminToken, http.MethodPatch, base+"/Groups/"+groupID, `{
		"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
		"Operations":[{"op":"remove","path":"members[value eq \"`+userID+`\"]"}]}`, http.StatusOK)
	if emptied := call(adminToken, http.MethodGet, base+"/Groups/"+groupID, "", http.StatusOK); len(emptied["members"].([]any)) != 0 {
		t.Fatalf("group after removing its member = %v", emptied["members"])
	}
	// ...and adds one back with a members operation.
	call(adminToken, http.MethodPatch, base+"/Groups/"+groupID, `{
		"Operations":[{"op":"add","path":"members","value":[{"value":"`+userID+`"}]}]}`, http.StatusOK)
	if refilled := call(adminToken, http.MethodGet, base+"/Groups/"+groupID, "", http.StatusOK); len(refilled["members"].([]any)) != 1 {
		t.Fatalf("group after adding a member = %v", refilled["members"])
	}

	// Renaming a group, and its filter.
	call(adminToken, http.MethodPatch, base+"/Groups/"+groupID, `{"Operations":[{"op":"replace","path":"displayName","value":"Platform engineers"}]}`, http.StatusOK)
	renamed := call(adminToken, http.MethodGet, base+`/Groups?filter=displayName+eq+%22Platform+engineers%22`, "", http.StatusOK)
	if renamed["totalResults"].(float64) != 1 {
		t.Fatalf("renamed group filter = %v", renamed)
	}

	// Deactivation is how SCIM takes access away.
	call(adminToken, http.MethodPatch, base+"/Users/"+userID, `{"Operations":[{"op":"replace","path":"active","value":false}]}`, http.StatusOK)
	if deactivated := call(adminToken, http.MethodGet, base+"/Users/"+userID, "", http.StatusOK); deactivated["active"] != false {
		t.Fatalf("deactivated user = %v", deactivated)
	}
	var active bool
	if err := st.Pool.QueryRow(ctx, `SELECT active FROM directory_users WHERE user_id=$1 AND directory_id=$2::uuid`, userID, directoryID).Scan(&active); err != nil || active {
		t.Fatalf("the person's directory membership is still active (%v)", err)
	}
	// ...and a replace brings them back, with a new name.
	call(adminToken, http.MethodPut, base+"/Users/"+userID, `{
		"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],
		"externalId":"idp-1","userName":"dana@example.test","displayName":"Dana Restored",
		"name":{"givenName":"Dana","familyName":"Restored"},"active":true}`, http.StatusOK)
	restored := call(adminToken, http.MethodGet, base+"/Users/"+userID, "", http.StatusOK)
	if restored["active"] != true || restored["displayName"] != "Dana Restored" {
		t.Fatalf("restored user = %v", restored)
	}

	// The directory is now SCIM-managed, which is what the organization says.
	managed, err := st.DirectorySCIMManaged(ctx, workspaceID)
	if err != nil || !managed {
		t.Fatalf("directory managed = %v (%v)", managed, err)
	}

	// Deprovisioning.
	call(adminToken, http.MethodDelete, base+"/Groups/"+groupID, "", http.StatusNoContent)
	call(adminToken, http.MethodGet, base+"/Groups/"+groupID, "", http.StatusNotFound)
	call(adminToken, http.MethodDelete, base+"/Users/"+userID, "", http.StatusNoContent)
	call(adminToken, http.MethodGet, base+"/Users/"+userID, "", http.StatusNotFound)
}
