package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestMemberByIDScopesUserToActiveWorkspaceMembership(t *testing.T) {
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

	const workspaceID = "ws_default"
	memberID := NewID("usr")
	nonMemberID := NewID("usr")
	for _, user := range []struct {
		id, email string
	}{
		{memberID, memberID + "@example.invalid"},
		{nonMemberID, nonMemberID + "@example.invalid"},
	} {
		if _, err := st.CreateUser(ctx, user.id, user.email, "test", user.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddMember(ctx, workspaceID, memberID, "member"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := st.Pool.Exec(ctx, `DELETE FROM memberships WHERE workspace_id=$1 AND user_id=$2`, workspaceID, memberID); err != nil {
			t.Errorf("cleanup membership: %v", err)
		}
		if _, err := st.Pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, memberID, nonMemberID); err != nil {
			t.Errorf("cleanup users: %v", err)
		}
	}()

	member, err := st.MemberByID(ctx, workspaceID, memberID)
	if err != nil || member.ID != memberID {
		t.Fatalf("member lookup = (%+v, %v)", member, err)
	}
	if _, err := st.MemberByID(ctx, workspaceID, nonMemberID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("non-member lookup err=%v, want pgx.ErrNoRows", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET active=false WHERE id=$1`, memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MemberByID(ctx, workspaceID, memberID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("inactive member lookup err=%v, want pgx.ErrNoRows", err)
	}
}
