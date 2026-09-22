package store

import (
	"context"
	"os"
	"testing"
)

// A policy covers the people in a group as well as the people named on it,
// and naming somebody is the more particular statement: their own policy wins
// over the one covering a group they are in.
func TestAuthenticationPolicyCoversAGroup(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	organization, err := st.OrganizationByWorkspace(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	organizationID := organization.ID
	directories, err := st.DirectoriesByOrganization(ctx, organizationID)
	if err != nil || len(directories) == 0 {
		t.Fatalf("directories: %+v, %v", directories, err)
	}
	group, err := st.CreateDirectoryGroup(ctx, workspaceID, actorID, directories[0].ID,
		"contractors-"+NewID("g")[3:11], "Everyone on a contract")
	if err != nil {
		t.Fatalf("create the group: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM groups WHERE id=$1`, group.ID) })

	contractorID := NewID("usr")
	contractor, err := st.CreateUser(ctx, contractorID, "contractor-"+contractorID+"@zzira.dev", "", "A Contractor")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(ctx, workspaceID, contractor.ID, "member"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM workspace_members WHERE user_id=$1`, contractor.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, contractor.ID)
	})
	if err := st.SetGroupMember(ctx, workspaceID, actorID, directories[0].ID, group.ID, contractor.ID, true); err != nil {
		t.Fatalf("put them in the group: %v", err)
	}

	policy, err := st.CreateOrganizationPolicy(ctx, workspaceID, actorID, PolicyInput{
		Type: "authentication-policy", Name: "Contractors " + NewID("p")[3:9], Status: "enabled",
		Config: &AuthenticationConfig{EnforceSSO: true, SessionDurationMinutes: 60},
	})
	if err != nil {
		t.Fatalf("create the policy: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM organization_policies WHERE id=$1`, policy.ID) })

	// Before the group is covered, nothing about this person has changed.
	before, err := st.AuthenticationPolicyForUser(ctx, contractor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if before.PolicyID == policy.ID {
		t.Fatal("a policy covering no group already covered the person in it")
	}
	if err := st.SetAuthenticationPolicyGroup(ctx, workspaceID, actorID, policy.ID, group.ID, true); err != nil {
		t.Fatalf("cover the group: %v", err)
	}
	covered, err := st.AuthenticationPolicyForUser(ctx, contractor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if covered.PolicyID != policy.ID || !covered.EnforceSSO {
		t.Fatalf("the person in the covered group reads policy %+v", covered)
	}
	groups, err := st.AuthenticationPolicyGroups(ctx, organizationID, policy.ID)
	if err != nil || len(groups) != 1 || groups[0] != group.ID {
		t.Fatalf("the policy's groups = %+v, %v", groups, err)
	}

	// Naming somebody is the more particular statement.
	own, err := st.CreateOrganizationPolicy(ctx, workspaceID, actorID, PolicyInput{
		Type: "authentication-policy", Name: "Named " + NewID("p")[3:9], Status: "enabled",
		Config: &AuthenticationConfig{SessionDurationMinutes: 120},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM organization_policies WHERE id=$1`, own.ID) })
	if err := st.SetAuthenticationPolicyMember(ctx, workspaceID, actorID, own.ID, contractor.ID, true); err != nil {
		t.Fatal(err)
	}
	named, err := st.AuthenticationPolicyForUser(ctx, contractor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if named.PolicyID != own.ID {
		t.Fatalf("being named on a policy did not win over the group: %+v", named)
	}

	// Taking the group out leaves the policy covering nobody through it.
	if err := st.SetAuthenticationPolicyGroup(ctx, workspaceID, actorID, policy.ID, group.ID, false); err != nil {
		t.Fatal(err)
	}
	if groups, err := st.AuthenticationPolicyGroups(ctx, organizationID, policy.ID); err != nil || len(groups) != 0 {
		t.Fatalf("the policy still covers %+v, %v", groups, err)
	}
}
