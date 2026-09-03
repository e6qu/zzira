package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestInactiveUserCannotReuseSessionOrMembership(t *testing.T) {
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

	userID := NewID("usr")
	if _, err := st.CreateUser(ctx, userID, userID+"@example.invalid", "test", "Inactive session test"); err != nil {
		t.Fatal(err)
	}
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddMember(ctx, workspaceID, userID, "member"); err != nil {
		t.Fatal(err)
	}
	tokenHash := HashToken("inactive-session-" + userID)
	if err := st.CreateSession(ctx, tokenHash, userID, time.Hour); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	}()

	if _, err := st.SessionUser(ctx, tokenHash); err != nil {
		t.Fatalf("active user's session was rejected: %v", err)
	}
	if ok, err := st.IsMember(ctx, workspaceID, userID); err != nil || !ok {
		t.Fatalf("active membership = %t, %v", ok, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET active=false WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionUser(ctx, tokenHash); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("inactive user's session lookup err=%v, want pgx.ErrNoRows", err)
	}
	if _, err := st.UserByID(ctx, userID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("inactive user lookup err=%v, want pgx.ErrNoRows", err)
	}
	if ok, err := st.IsMember(ctx, workspaceID, userID); err != nil || ok {
		t.Fatalf("inactive membership = %t, %v; want false", ok, err)
	}
}
