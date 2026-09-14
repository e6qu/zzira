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
	"time"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/store"
)

// TestGlobalPermissionGrants covers Jira's global permissions page: sites
// start with the member permissions granted to everyone with Jira access, and
// administrators move them to groups, which every global permission check
// follows.
func TestGlobalPermissionGrants(t *testing.T) {
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
	workspaceID, adminID, memberID, outsiderID := store.NewID("ws"), store.NewID("usr"), store.NewID("usr"), store.NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Global permissions')`, workspaceID)
	users := []struct{ id, role string }{{adminID, "admin"}, {memberID, "member"}, {outsiderID, "member"}}
	for _, identity := range users {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, identity.id, identity.id+"@example.test", "Global "+identity.id)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,$3)`, workspaceID, identity.id, identity.role)
		exec(`INSERT INTO api_tokens(id,user_id,token_hash) VALUES($1,$1,$2)`, identity.id, store.HashToken(identity.id))
	}
	var directoryID, groupID string
	if err = st.Pool.QueryRow(ctx, `SELECT d.id::text FROM directories d JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1 LIMIT 1`, workspaceID).Scan(&directoryID); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO groups(directory_id,name) VALUES($1,$2) RETURNING id::text`, directoryID, fmt.Sprintf("Bulk editors %d", time.Now().UnixNano())).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO group_members(group_id,user_id) VALUES($1,$2)`, groupID, memberID)
	t.Cleanup(func() {
		exec(`DELETE FROM groups WHERE id=$1::uuid`, groupID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, identity := range users {
			exec(`DELETE FROM api_tokens WHERE user_id=$1`, identity.id)
			exec(`DELETE FROM users WHERE id=$1`, identity.id)
		}
	})
	h := &Handler{Store: st, Commands: &commands.Service{Store: st}, WorkspaceSlug: workspaceID, BaseURL: "https://zzira.test"}
	holds := func(user, permission string) bool {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/rest/api/3/mypermissions?permissions="+permission, nil)
		request.SetBasicAuth(user+"@example.test", user)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		var decoded struct {
			Permissions map[string]struct {
				HavePermission bool `json:"havePermission"`
			} `json:"permissions"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &decoded) != nil {
			t.Fatalf("mypermissions: %d %s", response.Code, response.Body.String())
		}
		return decoded.Permissions[permission].HavePermission
	}

	// Sites start with the member permissions granted to everyone with Jira.
	if !holds(memberID, "BULK_CHANGE") || !holds(outsiderID, "USER_PICKER") || holds(memberID, "MANAGE_GROUP_FILTER_SUBSCRIPTIONS") {
		t.Fatal("default global permission grants")
	}
	grants, err := st.GlobalPermissionGrants(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var bulkGrant int64
	for _, grant := range grants {
		if grant.Permission == "BULK_CHANGE" && grant.ProductKey == "jira-software" {
			bulkGrant = grant.ID
		}
	}
	if bulkGrant == 0 {
		t.Fatalf("grants = %+v", grants)
	}

	// Moving Bulk change to a group takes it from everyone else.
	if err = st.RemoveGlobalPermissionGrant(ctx, workspaceID, memberID, bulkGrant); !errors.Is(err, store.ErrProjectPermission) {
		t.Fatalf("a member removed a grant: %v", err)
	}
	if err = st.RemoveGlobalPermissionGrant(ctx, workspaceID, adminID, bulkGrant); err != nil {
		t.Fatal(err)
	}
	if holds(memberID, "BULK_CHANGE") {
		t.Fatal("bulk change survived removing its grant")
	}
	request := httptest.NewRequest(http.MethodPost, "/rest/api/3/bulk/issues/watch", strings.NewReader(`{"selectedIssueIdsOrKeys":["GP-1"]}`))
	request.SetBasicAuth(memberID+"@example.test", memberID)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("bulk watch without Bulk change: %d %s", response.Code, response.Body.String())
	}
	if _, err = st.AddGlobalPermissionGrant(ctx, workspaceID, adminID, "BULK_CHANGE", groupID, ""); err != nil {
		t.Fatal(err)
	}
	if !holds(memberID, "BULK_CHANGE") || holds(outsiderID, "BULK_CHANGE") {
		t.Fatal("group grant of bulk change")
	}
	if !holds(adminID, "BULK_CHANGE") {
		t.Fatal("administrators hold every global permission")
	}

	for name, attempt := range map[string]struct{ permission, group, product string }{
		"administer":  {"ADMINISTER", "", "jira-software"},
		"project":     {"BROWSE_PROJECTS", "", "jira-software"},
		"both":        {"USER_PICKER", groupID, "jira-software"},
		"neither":     {"USER_PICKER", "", ""},
		"product":     {"USER_PICKER", "", "confluence"},
		"group":       {"USER_PICKER", "00000000-0000-0000-0000-000000000000", ""},
		"app unknown": {"missing__permission", "", "jira-software"},
	} {
		if _, err = st.AddGlobalPermissionGrant(ctx, workspaceID, adminID, attempt.permission, attempt.group, attempt.product); !errors.Is(err, store.ErrGlobalPermissionValidation) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if _, err = st.AddGlobalPermissionGrant(ctx, workspaceID, adminID, "BULK_CHANGE", groupID, ""); !errors.Is(err, store.ErrGlobalPermissionConflict) {
		t.Fatalf("duplicate grant err = %v", err)
	}
}
