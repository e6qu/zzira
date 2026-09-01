package store

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestClaimOIDCLogoutAndDeleteSessions(t *testing.T) {
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

	const issuer = "https://auth.dev.e6qu.dev"
	userID := NewID("usr")
	email := userID + "@example.invalid"
	if _, err := st.CreateUser(ctx, userID, email, "test", userID); err != nil {
		t.Fatal(err)
	}
	subject := userID + "-sub"
	if _, err := st.ResolveOIDCUser(ctx, issuer, subject, email); err != nil {
		t.Fatalf("bind OIDC identity: %v", err)
	}
	tokenHash := HashToken("logout-test-" + userID)
	if err := st.CreateOIDCSession(ctx, tokenHash, userID, "id-token", time.Hour); err != nil {
		t.Fatalf("create session: %v", err)
	}
	jti := NewID("jti")
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM oidc_logout_tokens WHERE jti=$1`, jti)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM oidc_identities WHERE issuer=$1 AND subject=$2`, issuer, subject)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	}()

	if _, err := st.SessionUser(ctx, tokenHash); err != nil {
		t.Fatalf("session should exist before logout: %v", err)
	}

	expiresAt := time.Now().Add(time.Hour)
	claimed, err := st.ClaimOIDCLogoutAndDeleteSessions(ctx, jti, expiresAt, issuer, subject)
	if err != nil {
		t.Fatalf("claim logout token: %v", err)
	}
	if !claimed {
		t.Fatal("first claim of a fresh jti must succeed")
	}
	if _, err := st.SessionUser(ctx, tokenHash); err == nil {
		t.Fatal("session must be revoked after back-channel logout")
	}

	claimedAgain, err := st.ClaimOIDCLogoutAndDeleteSessions(ctx, jti, expiresAt, issuer, subject)
	if err != nil {
		t.Fatalf("replay claim: %v", err)
	}
	if claimedAgain {
		t.Fatal("a jti already claimed must not be claimable again")
	}
}

func TestClaimOIDCLogoutAndDeleteSessionsWithNoMatchingIdentity(t *testing.T) {
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

	// A logout token for a subject zzira has never seen (or a sid-only token,
	// since zzira tracks no sid) must still claim cleanly -- there is simply
	// nothing to revoke.
	jti := NewID("jti")
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM oidc_logout_tokens WHERE jti=$1`, jti)
	}()
	claimed, err := st.ClaimOIDCLogoutAndDeleteSessions(ctx, jti, time.Now().Add(time.Hour), "https://auth.dev.e6qu.dev", "no-such-subject")
	if err != nil {
		t.Fatalf("claim logout token for unknown subject: %v", err)
	}
	if !claimed {
		t.Fatal("a fresh jti must claim even when its subject matches no identity")
	}
}
