package store

import (
	"context"
	"os"
	"testing"
)

func TestOrganizationProvisioningAndGroupAuthorization(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}

	workspaceID := NewID("ws")
	adminID := NewID("usr")
	memberID := NewID("usr")
	for _, user := range []struct{ id, email, name string }{
		{adminID, adminID + "@example.invalid", "Organization Admin"},
		{memberID, memberID + "@example.invalid", "Directory Member"},
	} {
		if _, err := st.Pool.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'unused',$3)`, user.id, user.email, user.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Organization test')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	organization, err := st.OrganizationByWorkspace(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `
			DELETE FROM role_bindings rb
			WHERE rb.principal_id IN ($1,$2)
			   OR rb.scope_id=$3
			   OR rb.scope_id IN (SELECT id::text FROM sites WHERE organization_id=$3::uuid)
			   OR rb.scope_id IN (SELECT p.id::text FROM products p JOIN sites s ON s.id=p.site_id WHERE s.organization_id=$3::uuid)
			   OR rb.principal_id IN (SELECT g.id::text FROM groups g JOIN directories d ON d.id=g.directory_id WHERE d.organization_id=$3::uuid)`, adminID, memberID, organization.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, workspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organizations WHERE id::text=$1`, organization.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, adminID, memberID)
	}()

	if err := st.AddMember(ctx, workspaceID, adminID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(ctx, workspaceID, memberID, "member"); err != nil {
		t.Fatal(err)
	}
	site, err := st.SiteByWorkspace(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if site.OrganizationID != organization.ID {
		t.Fatalf("site organization=%q, want %q", site.OrganizationID, organization.ID)
	}
	products, err := st.ProductsBySite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(products) != 3 {
		t.Fatalf("products=%d, want 3", len(products))
	}
	directories, err := st.DirectoriesByOrganization(ctx, organization.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(directories) != 1 {
		t.Fatalf("directories=%d, want 1", len(directories))
	}
	users, err := st.DirectoryUsers(ctx, directories[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("directory users=%d, want 2", len(users))
	}
	if err := st.UpdateDirectoryUserProfile(ctx, workspaceID, adminID, directories[0].ID, memberID, ManagedProfileUpdate{
		DisplayName: "Directory Member", Nickname: "Dee", JobTitle: "Service lead",
		Department: "Support", OrganizationName: "Example", Location: "Bucharest", TimeZone: "Europe/Bucharest",
	}); err != nil {
		t.Fatal(err)
	}
	users, err = st.DirectoryUsers(ctx, directories[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if user.ID == memberID && (user.Nickname != "Dee" || user.JobTitle != "Service lead" || user.TimeZone != "Europe/Bucharest") {
			t.Fatalf("managed profile was not persisted: %+v", user)
		}
	}
	admin, err := st.IsAdmin(ctx, workspaceID, adminID)
	if err != nil || !admin {
		t.Fatalf("direct admin=%v, err=%v", admin, err)
	}
	admin, err = st.IsAdmin(ctx, workspaceID, memberID)
	if err != nil || admin {
		t.Fatalf("member admin=%v, err=%v", admin, err)
	}

	group, err := st.CreateDirectoryGroup(ctx, workspaceID, adminID, directories[0].ID, "site-admins", "Site administration")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetGroupMember(ctx, workspaceID, adminID, directories[0].ID, group.ID, memberID, true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRoleBinding(ctx, workspaceID, adminID, "group", group.ID, "product", products[0].ID, "atlassian/user", true); err != nil {
		t.Fatal(err)
	}
	bindings, err := st.RoleBindingsForPrincipal(ctx, workspaceID, "user", memberID)
	if err != nil {
		t.Fatal(err)
	}
	foundGroupProductAccess := false
	for _, binding := range bindings {
		if binding.ScopeID == products[0].ID && binding.RoleKey == "atlassian/user" && binding.Assignment == "group_direct" {
			foundGroupProductAccess = true
		}
	}
	if !foundGroupProductAccess {
		t.Fatalf("effective user assignments did not include group product access: %#v", bindings)
	}
	if _, err := st.Pool.Exec(ctx, `
		INSERT INTO role_bindings(scope_type,scope_id,role_key,principal_type,principal_id)
		VALUES('site',$1,'atlassian/site-admin','group',$2)`, site.ID, group.ID); err != nil {
		t.Fatal(err)
	}
	admin, err = st.IsAdmin(ctx, workspaceID, memberID)
	if err != nil || !admin {
		t.Fatalf("group admin=%v, err=%v", admin, err)
	}
	if err := st.SetGroupMember(ctx, workspaceID, adminID, directories[0].ID, group.ID, memberID, false); err != nil {
		t.Fatal(err)
	}
	admin, err = st.IsAdmin(ctx, workspaceID, memberID)
	if err != nil || admin {
		t.Fatalf("removed group member admin=%v, err=%v", admin, err)
	}
	if err := st.SetRoleBinding(ctx, workspaceID, adminID, "group", group.ID, "product", products[0].ID, "atlassian/user", false); err != nil {
		t.Fatal(err)
	}
	secondWorkspaceID := NewID("ws")
	if _, err := st.Pool.Exec(ctx, `INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Second organization')`, secondWorkspaceID); err != nil {
		t.Fatal(err)
	}
	secondOrganization, err := st.OrganizationByWorkspace(ctx, secondWorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE workspace_id=$1`, secondWorkspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, secondWorkspaceID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organizations WHERE id::text=$1`, secondOrganization.ID)
	}()
	if err := st.AddMember(ctx, secondWorkspaceID, memberID, "member"); err != nil {
		t.Fatal(err)
	}
	token := NewID("secret")
	tokenID := NewID("tok")
	if err := st.CreateAPIToken(ctx, tokenID, memberID, HashToken(token), "multi-directory"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE id=$1`, tokenID) }()
	if err := st.SetDirectoryUserActive(ctx, workspaceID, adminID, directories[0].ID, memberID, false); err != nil {
		t.Fatal(err)
	}
	if allowed, err := st.IsMember(ctx, workspaceID, memberID); err != nil || allowed {
		t.Fatalf("suspended directory access=%v, err=%v", allowed, err)
	}
	if allowed, err := st.IsMember(ctx, secondWorkspaceID, memberID); err != nil || !allowed {
		t.Fatalf("other directory access=%v, err=%v", allowed, err)
	}
	if tokenUser, err := st.UserByAPIToken(ctx, HashToken(token)); err != nil || tokenUser != memberID {
		t.Fatalf("cross-organization token user=%q, err=%v", tokenUser, err)
	}
	users, err = st.DirectoryUsers(ctx, directories[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if user.ID == memberID && (user.Active || !user.AccountActive || user.SuspendedAt == "") {
			t.Fatalf("directory suspension profile=%+v", user)
		}
	}
	if err := st.SetDirectoryUserActive(ctx, workspaceID, adminID, directories[0].ID, memberID, true); err != nil {
		t.Fatal(err)
	}
	audit, err := st.OrganizationAuditEvents(ctx, organization.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit) != 8 {
		t.Fatalf("audit events=%d, want 8", len(audit))
	}
}
