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
	if _, err := st.ResolveOIDCUser(ctx, issuer, subject, email, "test", unusedPasswordHash(t)); err != nil {
		t.Fatalf("bind OIDC identity: %v", err)
	}
	tokenHash := HashToken("logout-test-" + userID)
	if err := st.CreateOIDCSession(ctx, tokenHash, userID, "id-token", issuer, subject, "", time.Hour); err != nil {
		t.Fatalf("create session: %v", err)
	}
	localTokenHash := HashToken("logout-test-local-" + userID)
	if err := st.CreateSession(ctx, localTokenHash, userID, time.Hour); err != nil {
		t.Fatalf("create local session: %v", err)
	}
	jti := NewID("jti")
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM oidc_logout_tokens WHERE jti=$1`, jti)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM oidc_identities WHERE issuer=$1 AND subject=$2`, issuer, subject)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	}()

	if _, err := st.SessionUser(ctx, tokenHash); err != nil {
		t.Fatalf("session should exist before logout: %v", err)
	}

	expiresAt := time.Now().Add(time.Hour)
	claimed, err := st.ClaimOIDCLogoutAndDeleteSessions(ctx, jti, expiresAt, issuer, subject, "")
	if err != nil {
		t.Fatalf("claim logout token: %v", err)
	}
	if !claimed {
		t.Fatal("first claim of a fresh jti must succeed")
	}
	if _, err := st.SessionUser(ctx, tokenHash); err == nil {
		t.Fatal("session must be revoked after a sub-only back-channel logout")
	}
	if _, err := st.SessionUser(ctx, localTokenHash); err != nil {
		t.Fatalf("password session must survive an OIDC subject logout: %v", err)
	}

	claimedAgain, err := st.ClaimOIDCLogoutAndDeleteSessions(ctx, jti, expiresAt, issuer, subject, "")
	if err != nil {
		t.Fatalf("replay claim: %v", err)
	}
	if claimedAgain {
		t.Fatal("a jti already claimed must not be claimable again")
	}
}

// TestClaimOIDCLogoutAndDeleteSessionsBySID covers the logout token shape Ory
// Hydra's back-channel logout actually sends in production: a sid claim and
// no sub. ClaimOIDCLogoutAndDeleteSessions must revoke by the recorded sid
// rather than requiring a subject, and must not touch another session bound
// to a different sid for the same user.
func TestClaimOIDCLogoutAndDeleteSessionsBySID(t *testing.T) {
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
	email := userID + "@example.invalid"
	if _, err := st.CreateUser(ctx, userID, email, "test", userID); err != nil {
		t.Fatal(err)
	}
	loggedOutSID := userID + "-sid-a"
	survivingSID := userID + "-sid-b"
	loggedOutHash := HashToken("logout-test-sid-a-" + userID)
	survivingHash := HashToken("logout-test-sid-b-" + userID)
	const issuer = "https://auth.dev.e6qu.dev"
	subject := userID + "-subject"
	if err := st.CreateOIDCSession(ctx, loggedOutHash, userID, "id-token", issuer, subject, loggedOutSID, time.Hour); err != nil {
		t.Fatalf("create session for sid a: %v", err)
	}
	if err := st.CreateOIDCSession(ctx, survivingHash, userID, "id-token", issuer, subject, survivingSID, time.Hour); err != nil {
		t.Fatalf("create session for sid b: %v", err)
	}
	foreignHash := HashToken("logout-test-foreign-sid-" + userID)
	if err := st.CreateOIDCSession(ctx, foreignHash, userID, "id-token", "https://other-issuer.example", subject, loggedOutSID, time.Hour); err != nil {
		t.Fatalf("create same-sid session from another issuer: %v", err)
	}
	jti := NewID("jti")
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM oidc_logout_tokens WHERE jti=$1`, jti)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	}()

	claimed, err := st.ClaimOIDCLogoutAndDeleteSessions(ctx, jti, time.Now().Add(time.Hour), issuer, "", loggedOutSID)
	if err != nil {
		t.Fatalf("claim sid-only logout token: %v", err)
	}
	if !claimed {
		t.Fatal("first claim of a fresh jti must succeed")
	}
	if _, err := st.SessionUser(ctx, loggedOutHash); err == nil {
		t.Fatal("the session bound to the logged-out sid must be revoked")
	}
	if _, err := st.SessionUser(ctx, survivingHash); err != nil {
		t.Fatalf("a session bound to a different sid must survive: %v", err)
	}
	if _, err := st.SessionUser(ctx, foreignHash); err != nil {
		t.Fatalf("same sid from a different issuer must survive: %v", err)
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

	// A logout token naming a subject or sid zzira has never seen must still
	// claim cleanly -- there is simply nothing to revoke.
	jti := NewID("jti")
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM oidc_logout_tokens WHERE jti=$1`, jti)
	}()
	claimed, err := st.ClaimOIDCLogoutAndDeleteSessions(ctx, jti, time.Now().Add(time.Hour), "https://auth.dev.e6qu.dev", "no-such-subject", "")
	if err != nil {
		t.Fatalf("claim logout token for unknown subject: %v", err)
	}
	if !claimed {
		t.Fatal("a fresh jti must claim even when its subject matches no identity")
	}
}
