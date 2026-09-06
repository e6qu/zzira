package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

func TestIdentityProviderStateIsBoundToProvider(t *testing.T) {
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
	state := NewID("state")
	if err := st.CreateIdentityProviderLoginState(ctx, state, "google", "nonce", "verifier", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.ConsumeIdentityProviderLoginState(ctx, state, "microsoft"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("mismatched provider consumed state: %v", err)
	}
	nonce, verifier, err := st.ConsumeIdentityProviderLoginState(ctx, state, "google")
	if err != nil || nonce != "nonce" || verifier != "verifier" {
		t.Fatalf("correct provider state = %q %q %v", nonce, verifier, err)
	}
}

func TestIdentityProviderSessionWritesOrganizationLoginAudit(t *testing.T) {
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
	email := NewID("provider-audit") + "@example.invalid"
	userID, err := st.ResolveOIDCUser(ctx, "https://accounts.google.com", NewID("subject"), email, "Provider audit", func() (string, error) { return "unusable", nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID) }()
	if err := st.CreateIdentityProviderSession(ctx, HashToken(NewID("session")), userID, "id-token", "https://accounts.google.com", "subject", "", "google", time.Hour); err != nil {
		t.Fatal(err)
	}
	var action, provider string
	if err := st.Pool.QueryRow(ctx, `
		SELECT action,detail->>'provider' FROM organization_audit_events
		WHERE actor_id=$1 AND action='identity.login' ORDER BY id DESC LIMIT 1`, userID).Scan(&action, &provider); err != nil {
		t.Fatal(err)
	}
	if action != "identity.login" || provider != "google" {
		t.Fatalf("audit = %q %q", action, provider)
	}
}

func TestIdentityProviderLinkLifecycleRevokesOnlyRemovedProvider(t *testing.T) {
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
	email := NewID("provider-link") + "@example.invalid"
	googleSubject := NewID("google-subject")
	atlassianSubject := NewID("atlassian-subject")
	userID, err := st.ResolveOIDCUser(ctx, "https://accounts.google.com", googleSubject, email, "Provider link", func() (string, error) { return "unusable", nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organization_audit_events WHERE target_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM oidc_identities WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE user_id=$1`, userID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	}()
	linkState := NewID("link-state")
	if err := st.CreateIdentityProviderLinkState(ctx, linkState, "atlassian", "link-nonce", "link-verifier", userID, time.Minute); err != nil {
		t.Fatal(err)
	}
	_, _, linkedUserID, err := st.ConsumeIdentityProviderState(ctx, linkState, "atlassian")
	if err != nil || linkedUserID != userID {
		t.Fatalf("link state user = %q, %v", linkedUserID, err)
	}
	if err := st.LinkOIDCIdentity(ctx, userID, "https://auth.atlassian.com", atlassianSubject, email); err != nil {
		t.Fatal(err)
	}
	identities, err := st.OIDCIdentitiesByUser(ctx, userID)
	if err != nil || len(identities) != 2 {
		t.Fatalf("linked identities = %#v, %v", identities, err)
	}
	googleSession := HashToken(NewID("google-session"))
	atlassianSession := HashToken(NewID("atlassian-session"))
	if err := st.CreateOIDCSession(ctx, googleSession, userID, "google-token", "https://accounts.google.com", googleSubject, "", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateOIDCSession(ctx, atlassianSession, userID, "atlassian-token", "https://auth.atlassian.com", atlassianSubject, "", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := st.UnlinkOIDCIdentity(ctx, userID, "https://auth.atlassian.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionUser(ctx, atlassianSession); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("removed-provider session remained valid: %v", err)
	}
	if got, err := st.SessionUser(ctx, googleSession); err != nil || got != userID {
		t.Fatalf("other-provider session = %q, %v", got, err)
	}
	if err := st.UnlinkOIDCIdentity(ctx, userID, "https://accounts.google.com"); !errors.Is(err, ErrAdminConflict) {
		t.Fatalf("last-provider unlink = %v", err)
	}
	var linked, unlinked int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE action='identity.linked'),count(*) FILTER (WHERE action='identity.unlinked') FROM organization_audit_events WHERE actor_id=$1`, userID).Scan(&linked, &unlinked); err != nil {
		t.Fatal(err)
	}
	if linked != 1 || unlinked != 1 {
		t.Fatalf("link audit counts = %d linked, %d unlinked", linked, unlinked)
	}
}

func TestIdentityProviderSettingPersistsAndDisablingRevokesIssuerSessions(t *testing.T) {
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
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	tokenHash := HashToken(NewID("disabled-provider-session"))
	if err := st.CreateOIDCSession(ctx, tokenHash, actorID, "id-token", "https://auth.atlassian.com", NewID("subject"), "", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := st.SetIdentityProviderEnabled(ctx, workspaceID, actorID, "atlassian", "https://auth.atlassian.com", false); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = st.SetIdentityProviderEnabled(ctx, workspaceID, actorID, "atlassian", "https://auth.atlassian.com", true)
	}()
	settings, err := st.IdentityProviderSettingsByWorkspace(ctx, workspaceID)
	if err != nil || settings["atlassian"] {
		t.Fatalf("provider settings = %#v, %v", settings, err)
	}
	if _, err := st.SessionUser(ctx, tokenHash); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("disabled-provider session remained valid: %v", err)
	}
	var disabledEvents int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM organization_audit_events WHERE actor_id=$1 AND action='identity.provider.disabled' AND target_id='atlassian'`, actorID).Scan(&disabledEvents); err != nil {
		t.Fatal(err)
	}
	if disabledEvents == 0 {
		t.Fatal("provider disable did not write an audit event")
	}
}

func TestStoredIdentityProviderRegistrationLifecycle(t *testing.T) {
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
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	key := "test-" + NewID("provider")[9:20]
	issuer := "https://" + key + ".example.invalid"
	registration := models.IdentityProviderRegistration{
		ProviderKey: key, DisplayName: "Stored provider", Issuer: issuer,
		ClientID: "client-id", SecretCiphertext: make([]byte, 48),
	}
	if err := st.SaveIdentityProviderRegistration(ctx, workspaceID, actorID, registration); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM identity_provider_registrations WHERE provider_key=$1`, key)
	}()
	registrations, err := st.IdentityProviderRegistrationsByWorkspace(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, current := range registrations {
		if current.ProviderKey == key {
			found = current.Issuer == issuer && current.ClientID == "client-id" && len(current.SecretCiphertext) == 48
		}
	}
	if !found {
		t.Fatalf("stored registration missing from %#v", registrations)
	}
	registration.SecretCiphertext = make([]byte, 64)
	if err := st.SaveIdentityProviderRegistration(ctx, workspaceID, actorID, registration); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteIdentityProviderRegistration(ctx, workspaceID, actorID, key, issuer); err != nil {
		t.Fatal(err)
	}
	var registered, rotated, deleted int
	if err := st.Pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE action='identity.provider.registered'),
		       count(*) FILTER (WHERE action='identity.provider.credentials.rotated'),
		       count(*) FILTER (WHERE action='identity.provider.deleted')
		FROM organization_audit_events WHERE actor_id=$1 AND target_id=$2`, actorID, key).Scan(&registered, &rotated, &deleted); err != nil {
		t.Fatal(err)
	}
	if registered != 1 || rotated != 1 || deleted != 1 {
		t.Fatalf("registration audit = %d registered, %d rotated, %d deleted", registered, rotated, deleted)
	}
}
