package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestFiltersAreWorkspaceScopedOwnedAndPerUserFavourites(t *testing.T) {
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
	ownerID := NewID("usr")
	otherUserID := NewID("usr")
	for _, user := range []struct{ id, email string }{
		{ownerID, ownerID + "@example.invalid"},
		{otherUserID, otherUserID + "@example.invalid"},
	} {
		if _, err := st.CreateUser(ctx, user.id, user.email, "test", user.id); err != nil {
			t.Fatal(err)
		}
		if err := st.AddMember(ctx, workspaceID, user.id, "member"); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		if _, err := st.Pool.Exec(ctx, `DELETE FROM memberships WHERE workspace_id=$1 AND user_id IN ($2,$3)`, workspaceID, ownerID, otherUserID); err != nil {
			t.Errorf("cleanup memberships: %v", err)
		}
		if _, err := st.Pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, ownerID, otherUserID); err != nil {
			t.Errorf("cleanup users: %v", err)
		}
	}()
	filterID := NewID("flt")
	defer func() {
		if _, err := st.Pool.Exec(ctx, `DELETE FROM filters WHERE id=$1`, filterID); err != nil {
			t.Errorf("cleanup filter: %v", err)
		}
	}()

	if _, err := st.CreateFilter(ctx, filterID, workspaceID, "Owned filter", `project = ZZ`, "", ownerID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFilterFavourite(ctx, workspaceID, otherUserID, filterID, true); err != nil {
		t.Fatal(err)
	}
	ownerView, err := st.FilterByID(ctx, workspaceID, ownerID, filterID)
	if err != nil {
		t.Fatal(err)
	}
	if ownerView.Favourite {
		t.Fatal("owner inherited another user's favourite")
	}
	otherView, err := st.FilterByID(ctx, workspaceID, otherUserID, filterID)
	if err != nil {
		t.Fatal(err)
	}
	if !otherView.Favourite {
		t.Fatal("user's favourite was not persisted")
	}
	if _, err := st.UpdateFilter(ctx, workspaceID, otherUserID, filterID, "Hijacked", `project = ZZ`, ""); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("non-owner update err=%v, want not found", err)
	}
	if err := st.DeleteFilter(ctx, workspaceID, otherUserID, filterID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("non-owner delete err=%v, want not found", err)
	}
}
