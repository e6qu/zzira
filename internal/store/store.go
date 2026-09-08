package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/migrations"
)

type Store struct {
	Pool *pgxpool.Pool
}

var ErrInactiveUser = errors.New("user account is inactive")

const migrationLockID int64 = 0x5A5A495241

func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	// Server-side prepared statements: JSONB operators like @>/? must never
	// pass through pgx's client-side SQL sanitizer (it rejects literal ?).
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() { s.Pool.Close() }

func NewID(prefix string) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("store: cryptographic randomness: %v", err))
	}
	return prefix + "_" + hex.EncodeToString(b)
}

// HashToken is the canonical API-token/session-token hash (SHA-256 hex).
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockID)
	}()

	_, err = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`)
	if err != nil {
		return err
	}
	entries, err := migrations.FS.ReadDir(".")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var exists bool
		err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)`, name).Scan(&exists)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		sqlBytes, err := migrations.FS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ---- Users / sessions / tokens ----

func (s *Store) CreateUser(ctx context.Context, id, email, passwordHash, displayName string) (*models.User, error) {
	u := &models.User{ID: id, Email: email, DisplayName: displayName, TimeZone: "UTC", Active: true, AccountType: "atlassian"}
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, display_name) VALUES ($1,$2,$3,$4)`,
		id, email, passwordHash, displayName)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (id, passwordHash, displayName string, err error) {
	err = s.Pool.QueryRow(ctx,
		`SELECT id, password_hash, display_name FROM users WHERE email=$1 AND active`, email).
		Scan(&id, &passwordHash, &displayName)
	return
}

func (s *Store) UserByID(ctx context.Context, id string) (*models.User, error) {
	u := &models.User{ID: id, Active: true, AccountType: "atlassian"}
	err := s.Pool.QueryRow(ctx,
		`SELECT email, display_name, time_zone, COALESCE(username, split_part(email, '@', 1)) FROM users WHERE id=$1 AND active`, id).
		Scan(&u.Email, &u.DisplayName, &u.TimeZone, &u.Username)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// SetOIDCUsername records the identity provider's preferred_username claim as
// the account's display handle, refreshed on every sign-in so a provider-side
// rename is reflected here too.
func (s *Store) SetOIDCUsername(ctx context.Context, userID, username string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET username=$1 WHERE id=$2`, username, userID)
	return err
}

// SetOIDCRole persists the identity provider's role claim (developer/admin),
// refreshed on every sign-in the same way SetOIDCUsername refreshes the
// display handle. This is the value /auth/validation exposes as
// data-testid="validation-role" -- distinct from a user's ZZIRA workspace
// membership role.
func (s *Store) SetOIDCRole(ctx context.Context, userID, role string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE users SET oidc_role=$1 WHERE id=$2`, role, userID)
	return err
}

// OIDCRole reads the identity provider's role claim persisted by
// SetOIDCRole, or "" if the account never signed in via OIDC.
func (s *Store) OIDCRole(ctx context.Context, userID string) (string, error) {
	var role *string
	if err := s.Pool.QueryRow(ctx, `SELECT oidc_role FROM users WHERE id=$1`, userID).Scan(&role); err != nil {
		return "", err
	}
	if role == nil {
		return "", nil
	}
	return *role, nil
}

func (s *Store) CreateSession(ctx context.Context, tokenHash, userID string, ttl time.Duration) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1,$2,now() + $3::interval)`,
		tokenHash, userID, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	return err
}

// CreateOIDCSession records an opaque browser session, its ID token (so
// RP-initiated logout can send the provider an id_token_hint), and the
// provider's sid so a later back-channel logout naming that sid can find it.
func (s *Store) CreateOIDCSession(ctx context.Context, tokenHash, userID, idToken, issuer, subject, sid string, ttl time.Duration) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, oidc_id_token, oidc_issuer, oidc_subject, oidc_session_id, expires_at)
		 VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),now() + $7::interval)`,
		tokenHash, userID, idToken, issuer, subject, sid, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	return err
}

// CreateIdentityProviderSession records a provider-backed browser session and
// its login evidence in one transaction. A user may belong to more than one
// organization; each organization receives its own immutable audit event.
func (s *Store) CreateIdentityProviderSession(ctx context.Context, tokenHash, userID, idToken, issuer, subject, sid, providerKey string, ttl time.Duration) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, oidc_id_token, oidc_issuer, oidc_subject, oidc_session_id, expires_at)
		 VALUES ($1,$2,NULLIF($3,''),$4,$5,NULLIF($6,''),now() + $7::interval)`,
		tokenHash, userID, idToken, issuer, subject, sid, fmt.Sprintf("%d seconds", int(ttl.Seconds()))); err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]any{"provider": providerKey, "issuer": issuer})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT DISTINCT d.organization_id,$1,'identity.login','user',$1,$2::jsonb
		FROM directory_users du JOIN directories d ON d.id=du.directory_id
		WHERE du.user_id=$1`, userID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SessionUser(ctx context.Context, tokenHash string) (string, error) {
	var userID string
	err := s.Pool.QueryRow(ctx,
		`SELECT s.user_id FROM sessions s JOIN users u ON u.id=s.user_id
		 WHERE s.token_hash=$1 AND s.expires_at > now() AND u.active`, tokenHash).Scan(&userID)
	return userID, err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, tokenHash)
	return err
}

// ClaimOIDCLogoutAndDeleteSessions atomically claims a Back-Channel Logout
// token's jti (replay protection) and, only on a first claim, revokes the
// session(s) it names. Per the OIDC Back-Channel Logout 1.0 spec, a sid names
// one specific provider session within its issuer and only OIDC sessions
// recorded under that pair are revoked; a token naming no sid means every
// OIDC session for the (issuer, subject) identity is revoked, without touching
// a password session for the same local account. Ory Hydra's real logout
// tokens carry sid without sub (its documented example omits sub entirely),
// so the sid path is the one production traffic actually takes.
func (s *Store) ClaimOIDCLogoutAndDeleteSessions(ctx context.Context, jti string, expiresAt time.Time, issuer, subject, sid string) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx,
		`WITH expired AS (DELETE FROM oidc_logout_tokens WHERE expires_at <= now())
		 INSERT INTO oidc_logout_tokens (jti, expires_at) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		jti, expiresAt)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if sid != "" {
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE oidc_issuer=$1 AND oidc_session_id=$2`, issuer, sid); err != nil {
			return false, err
		}
	} else if subject != "" {
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE oidc_issuer=$1 AND oidc_subject=$2`, issuer, subject); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) OIDCSessionToken(ctx context.Context, tokenHash string) (string, error) {
	idToken, _, err := s.IdentityProviderSession(ctx, tokenHash)
	return idToken, err
}

func (s *Store) IdentityProviderSession(ctx context.Context, tokenHash string) (idToken, issuer string, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT COALESCE(oidc_id_token, ''),COALESCE(oidc_issuer, '') FROM sessions WHERE token_hash=$1 AND expires_at > now()`, tokenHash).Scan(&idToken, &issuer)
	return idToken, issuer, err
}

func (s *Store) CreateOIDCLoginState(ctx context.Context, state, nonce, codeVerifier string, ttl time.Duration) error {
	return s.CreateIdentityProviderLoginState(ctx, state, "shauth", nonce, codeVerifier, ttl)
}

func (s *Store) CreateIdentityProviderLoginState(ctx context.Context, state, providerKey, nonce, codeVerifier string, ttl time.Duration) error {
	return s.createIdentityProviderState(ctx, state, providerKey, nonce, codeVerifier, "", ttl)
}

func (s *Store) CreateIdentityProviderLinkState(ctx context.Context, state, providerKey, nonce, codeVerifier, userID string, ttl time.Duration) error {
	return s.createIdentityProviderState(ctx, state, providerKey, nonce, codeVerifier, userID, ttl)
}

func (s *Store) createIdentityProviderState(ctx context.Context, state, providerKey, nonce, codeVerifier, userID string, ttl time.Duration) error {
	_, err := s.Pool.Exec(ctx,
		`WITH expired AS (DELETE FROM oidc_login_states WHERE expires_at <= now())
		 INSERT INTO oidc_login_states (state_hash, provider_key, nonce, code_verifier, link_user_id, expires_at)
		 VALUES ($1,$2,$3,$4,NULLIF($5,''),now() + $6::interval)`,
		HashToken(state), providerKey, nonce, codeVerifier, userID, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	return err
}

func (s *Store) ConsumeOIDCLoginState(ctx context.Context, state string) (nonce, codeVerifier string, err error) {
	return s.ConsumeIdentityProviderLoginState(ctx, state, "shauth")
}

func (s *Store) ConsumeIdentityProviderLoginState(ctx context.Context, state, providerKey string) (nonce, codeVerifier string, err error) {
	nonce, codeVerifier, _, err = s.ConsumeIdentityProviderState(ctx, state, providerKey)
	return nonce, codeVerifier, err
}

func (s *Store) ConsumeIdentityProviderState(ctx context.Context, state, providerKey string) (nonce, codeVerifier, linkUserID string, err error) {
	err = s.Pool.QueryRow(ctx,
		`DELETE FROM oidc_login_states WHERE state_hash=$1 AND provider_key=$2 AND expires_at > now()
		 RETURNING nonce,code_verifier,COALESCE(link_user_id,'')`, HashToken(state), providerKey).
		Scan(&nonce, &codeVerifier, &linkUserID)
	return nonce, codeVerifier, linkUserID, err
}

type OIDCIdentity struct {
	Issuer    string
	Subject   string
	Email     string
	CreatedAt time.Time
}

func (s *Store) OIDCIdentitiesByUser(ctx context.Context, userID string) ([]OIDCIdentity, error) {
	rows, err := s.Pool.Query(ctx, `SELECT issuer,subject,email,created_at FROM oidc_identities WHERE user_id=$1 ORDER BY created_at,issuer,subject`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	identities := make([]OIDCIdentity, 0)
	for rows.Next() {
		var identity OIDCIdentity
		if err := rows.Scan(&identity.Issuer, &identity.Subject, &identity.Email, &identity.CreatedAt); err != nil {
			return nil, err
		}
		identities = append(identities, identity)
	}
	return identities, rows.Err()
}

func (s *Store) LinkOIDCIdentity(ctx context.Context, userID, issuer, subject, email string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var active bool
	if err := tx.QueryRow(ctx, `SELECT active FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return ErrInactiveUser
	}
	var existingUserID string
	err = tx.QueryRow(ctx, `SELECT user_id FROM oidc_identities WHERE issuer=$1 AND subject=$2`, issuer, subject).Scan(&existingUserID)
	if err == nil {
		if existingUserID != userID {
			return fmt.Errorf("%w: identity is already linked to another account", ErrAdminConflict)
		}
		if _, err := tx.Exec(ctx, `UPDATE oidc_identities SET email=$3 WHERE issuer=$1 AND subject=$2`, issuer, subject, email); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err != pgx.ErrNoRows {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO oidc_identities(issuer,subject,user_id,email) VALUES($1,$2,$3,$4)`, issuer, subject, userID, email); err != nil {
		return err
	}
	if err := addIdentityAudit(ctx, tx, userID, "identity.linked", issuer); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) UnlinkOIDCIdentity(ctx context.Context, userID, issuer string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT issuer FROM oidc_identities WHERE user_id=$1 FOR UPDATE) locked`, userID).Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return fmt.Errorf("%w: the last linked provider cannot be removed", ErrAdminConflict)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM oidc_identities WHERE user_id=$1 AND issuer=$2`, userID, issuer)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: linked identity was not found", ErrAdminNotFound)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND oidc_issuer=$2`, userID, issuer); err != nil {
		return err
	}
	if err := addIdentityAudit(ctx, tx, userID, "identity.unlinked", issuer); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func addIdentityAudit(ctx context.Context, tx pgx.Tx, userID, action, issuer string) error {
	detail, err := json.Marshal(map[string]any{"issuer": issuer})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT DISTINCT d.organization_id,$1,$2,'user',$1,$3::jsonb
		FROM directory_users du JOIN directories d ON d.id=du.directory_id WHERE du.user_id=$1`, userID, action, detail)
	return err
}

func (s *Store) IdentityProviderSettingsByWorkspace(ctx context.Context, workspaceID string) (map[string]bool, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT settings.provider_key,settings.enabled
		FROM identity_provider_settings settings
		JOIN sites site ON site.organization_id=settings.organization_id
		WHERE site.workspace_id=$1`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settings := map[string]bool{}
	for rows.Next() {
		var key string
		var enabled bool
		if err := rows.Scan(&key, &enabled); err != nil {
			return nil, err
		}
		settings[key] = enabled
	}
	return settings, rows.Err()
}

func (s *Store) SetIdentityProviderEnabled(ctx context.Context, workspaceID, actorID, providerKey, issuer string, enabled bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationID string
	if err := tx.QueryRow(ctx, `SELECT organization_id::text FROM sites WHERE workspace_id=$1`, workspaceID).Scan(&organizationID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO identity_provider_settings(organization_id,provider_key,enabled,updated_by)
		VALUES($1::uuid,$2,$3,$4)
		ON CONFLICT(organization_id,provider_key) DO UPDATE
		SET enabled=EXCLUDED.enabled,updated_by=EXCLUDED.updated_by,updated_at=now()`, organizationID, providerKey, enabled, actorID); err != nil {
		return err
	}
	if !enabled {
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE oidc_issuer=$1`, issuer); err != nil {
			return err
		}
	}
	detail, err := json.Marshal(map[string]any{"provider": providerKey, "issuer": issuer, "enabled": enabled})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		VALUES($1::uuid,$2,$3,'identity-provider',$4,$5::jsonb)`, organizationID, actorID,
		map[bool]string{true: "identity.provider.enabled", false: "identity.provider.disabled"}[enabled], providerKey, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ResolveOIDCUser binds a verified sign-in to its immutable (issuer, subject)
// pair, identifying every later sign-in by that pair rather than the mutable
// email. The first sign-in for a pair binds to an existing active user
// matching the verified email if one exists; otherwise it provisions a new
// member of the default workspace for that email. The identity provider is
// the authorization boundary here (Shauth's own catalog registration and
// GitHub-org role mapping already decided this person may reach ZZIRA at
// all) -- ZZIRA does not additionally maintain its own separate invite list
// an operator must remember to keep in sync, which otherwise silently locks
// out every real member the identity provider has already vetted.
// newPasswordHash is invoked only when a new account is actually being
// created: password hashing is deliberately expensive and must not run on
// the common existing-user sign-in path.
func (s *Store) ResolveOIDCUser(ctx context.Context, issuer, subject, email, displayName string, newPasswordHash func() (string, error)) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockKey := fmt.Sprintf("oidc-identity:%d:%s%s", len(issuer), issuer, subject)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return "", err
	}
	var userID string
	var active bool
	err = tx.QueryRow(ctx, `
		SELECT i.user_id, u.active
		FROM oidc_identities i JOIN users u ON u.id=i.user_id
		WHERE i.issuer=$1 AND i.subject=$2`, issuer, subject).Scan(&userID, &active)
	if err == nil {
		if !active {
			return "", ErrInactiveUser
		}
		if _, err := tx.Exec(ctx, `UPDATE oidc_identities SET email=$3 WHERE issuer=$1 AND subject=$2`, issuer, subject, email); err != nil {
			return "", err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return userID, nil
	}
	if err != pgx.ErrNoRows {
		return "", err
	}
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE email=$1 AND active`, email).Scan(&userID)
	if err != nil {
		if err != pgx.ErrNoRows {
			return "", err
		}
		hash, err := newPasswordHash()
		if err != nil {
			return "", err
		}
		userID = NewID("usr")
		if _, err := tx.Exec(ctx,
			`INSERT INTO users (id, email, password_hash, display_name) VALUES ($1,$2,$3,$4)`,
			userID, email, hash, displayName); err != nil {
			return "", err
		}
		var workspaceID string
		if err := tx.QueryRow(ctx, `
			SELECT id FROM workspaces
			ORDER BY (id='ws_default') DESC,id LIMIT 1`).Scan(&workspaceID); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1,$2,'member') ON CONFLICT (workspace_id, user_id) DO NOTHING`,
			workspaceID, userID); err != nil {
			return "", err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO oidc_identities (issuer, subject, user_id, email) VALUES ($1,$2,$3,$4)`, issuer, subject, userID, email); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return userID, nil
}

// EnsureBootstrapAdmin idempotently grants the given email admin membership
// in the default workspace, creating the user first if none exists yet. An
// OIDC-only identity signs in by its immutable (issuer, subject) pair, never
// by password, so unusablePasswordHash only needs to satisfy the NOT NULL
// column and never successfully compare. Existing user profile and credential
// fields are left untouched; an existing membership is promoted to the
// requested role so the configured break-glass administrator cannot silently
// remain an ordinary member.
func (s *Store) EnsureBootstrapAdmin(ctx context.Context, email, displayName, unusablePasswordHash, role string) error {
	workspaceID, _, err := s.DefaultWorkspace(ctx)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID string
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE email=$1`, email).Scan(&userID)
	if err != nil {
		if err != pgx.ErrNoRows {
			return err
		}
		userID = NewID("usr")
		if _, err := tx.Exec(ctx,
			`INSERT INTO users (id, email, password_hash, display_name) VALUES ($1,$2,$3,$4)`,
			userID, email, unusablePasswordHash, displayName); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1,$2,$3)
		 ON CONFLICT (workspace_id, user_id) DO UPDATE SET role=EXCLUDED.role`,
		workspaceID, userID, role); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MonitoringSnapshot reports the shared PostgreSQL's real reachability and a
// real count of stored issues -- never a fabricated or cached figure.
func (s *Store) MonitoringSnapshot(ctx context.Context) (dbHealthy bool, issueCount int64, err error) {
	if pingErr := s.Pool.Ping(ctx); pingErr != nil {
		return false, 0, nil
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM issues`).Scan(&issueCount); err != nil {
		return true, 0, err
	}
	return true, issueCount, nil
}

func (s *Store) CreateAPIToken(ctx context.Context, id, userID, tokenHash, label string) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO api_tokens (id, user_id, token_hash, label) VALUES ($1,$2,$3,$4)`,
		id, userID, tokenHash, label)
	return err
}

func (s *Store) UserByAPIToken(ctx context.Context, tokenHash string) (string, error) {
	var userID string
	err := s.Pool.QueryRow(ctx,
		`SELECT t.user_id FROM api_tokens t JOIN users u ON u.id = t.user_id
		 WHERE t.token_hash=$1 AND u.active AND (t.expires_at IS NULL OR t.expires_at > now())`,
		tokenHash).Scan(&userID)
	return userID, err
}

// ---- Workspace / authz ----

func (s *Store) DefaultWorkspace(ctx context.Context) (id, slug string, err error) {
	err = s.Pool.QueryRow(ctx, `
		SELECT id,slug FROM workspaces
		ORDER BY (id='ws_default') DESC,id LIMIT 1`).Scan(&id, &slug)
	return
}

func (s *Store) WorkspaceBySlug(ctx context.Context, slug string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT id FROM workspaces WHERE slug=$1`, slug).Scan(&id)
	return id, err
}

// IsMember resolves membership from the organization/site/product role model.
// The legacy memberships table is mirrored into role_bindings by migration
// triggers so older command and fixture paths remain consistent during the
// authorization migration.
func (s *Store) IsMember(ctx context.Context, workspaceID, userID string) (bool, error) {
	roles, err := s.RolesForUserInWorkspace(ctx, workspaceID, userID)
	return len(roles) > 0, err
}

func (s *Store) AddMember(ctx context.Context, workspaceID, userID, role string) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1,$2,$3)
		 ON CONFLICT (workspace_id, user_id) DO UPDATE SET role=EXCLUDED.role`, workspaceID, userID, role)
	return err
}

// ---- Projects ----

func (s *Store) ProjectByKey(ctx context.Context, workspaceID, key string) (*models.Project, error) {
	p := &models.Project{WorkspaceID: workspaceID, Key: key}
	err := s.Pool.QueryRow(ctx,
		`SELECT id, name, COALESCE(workflow_id,''), COALESCE(security_scheme_id,''), description, url, COALESCE(lead_account_id,''), assignee_type, project_type_key FROM projects WHERE workspace_id=$1 AND upper(key)=upper($2)`,
		workspaceID, key).Scan(&p.ID, &p.Name, &p.WorkflowID, &p.SecuritySchemeID, &p.Description, &p.URL, &p.LeadAccountID, &p.AssigneeType, &p.ProjectTypeKey)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ---- Issues ----

const issueJoin = `
SELECT i.id, i.workspace_id, i.project_id, i.key, i.summary, i.description,
       st.id, st.name, st.category,
	       it.id, it.name, it.icon,
	       it.subtask,
	       parent.id, parent.key, parent.summary,
       pr.id, pr.name,
       a.id, a.display_name,
	       r.id, r.display_name,
	       i.rank,
	       i.security_level_id, i.fields, i.labels,
	       i.updated_seq, i.updated_at
FROM issues i
JOIN statuses st ON st.id = i.status_id
JOIN issue_types it ON it.id = i.issuetype_id
LEFT JOIN priorities pr ON pr.id = i.priority_id
LEFT JOIN users a ON a.id = i.assignee_id
LEFT JOIN users r ON r.id = i.reporter_id
LEFT JOIN issues parent ON parent.id = i.parent_id
`

func scanIssue(row pgx.Row) (*models.Issue, error) {
	i := &models.Issue{}
	var priorityID, priorityName *string
	var assigneeID, assigneeName *string
	var reporterID, reporterName *string
	var parentID, parentKey, parentSummary *string
	var updatedAt time.Time
	var securityLevelID *string
	var fieldsJSON []byte
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.ProjectID, &i.Key, &i.Summary, &i.Description,
		&i.Status.ID, &i.Status.Name, &i.Status.Category,
		&i.IssueType.ID, &i.IssueType.Name, &i.IssueType.Icon, &i.IssueType.Subtask,
		&parentID, &parentKey, &parentSummary,
		&priorityID, &priorityName,
		&assigneeID, &assigneeName,
		&reporterID, &reporterName,
		&i.Rank,
		&securityLevelID, &fieldsJSON, &i.Labels,
		&i.UpdatedSeq, &updatedAt)
	if err != nil {
		return nil, err
	}
	if securityLevelID != nil {
		i.SecurityLevelID = *securityLevelID
	}
	if len(fieldsJSON) > 0 {
		if err := json.Unmarshal(fieldsJSON, &i.Fields); err != nil {
			return nil, fmt.Errorf("issue fields: %w", err)
		}
	}
	if i.Labels == nil {
		i.Labels = []string{}
	}
	if err != nil {
		return nil, err
	}
	i.UpdatedAt = updatedAt.UTC().Format(time.RFC3339)
	if priorityID != nil {
		i.Priority = &models.Priority{ID: *priorityID, Name: *priorityName}
	}
	if assigneeID != nil {
		i.Assignee = &models.User{ID: *assigneeID, DisplayName: *assigneeName, Active: true, AccountType: "atlassian"}
	}
	if reporterID != nil {
		i.Reporter = &models.User{ID: *reporterID, DisplayName: *reporterName, Active: true, AccountType: "atlassian"}
	}
	if parentID != nil {
		i.Parent = &models.IssueParent{ID: *parentID, Key: *parentKey, Summary: *parentSummary}
	}
	return i, nil
}

func (s *Store) IssueByIDOrKey(ctx context.Context, workspaceID, idOrKey string) (*models.Issue, error) {
	return scanIssue(s.Pool.QueryRow(ctx, issueJoin+`
		WHERE i.workspace_id=$1 AND (i.id=$2 OR upper(i.key)=upper($2))`, workspaceID, idOrKey))
}

// CreateIssue runs the canonical write transaction: state change + action append +
// notify, all-or-nothing. Returns the persisted issue and its action.
func (s *Store) CreateIssue(ctx context.Context, actorID, projectID, summary string, description json.RawMessage, statusID, issueTypeID, priorityID, assigneeID string, labels []string, fields map[string]json.RawMessage, securityLevelID, parentID string) (*models.Issue, *models.Action, error) {
	return s.CreateIssueForReporter(ctx, actorID, actorID, projectID, summary, description, statusID, issueTypeID, priorityID, assigneeID, labels, fields, securityLevelID, parentID)
}

// CreateIssueForReporter separates the authenticated change actor from the
// issue reporter for on-behalf-of service requests.
func (s *Store) CreateIssueForReporter(ctx context.Context, actorID, reporterID, projectID, summary string, description json.RawMessage, statusID, issueTypeID, priorityID, assigneeID string, labels []string, fields map[string]json.RawMessage, securityLevelID, parentID string) (*models.Issue, *models.Action, error) {
	if labels == nil {
		labels = []string{}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var wsID, projectKey string
	err = tx.QueryRow(ctx, `SELECT workspace_id, key FROM projects WHERE id=$1`, projectID).Scan(&wsID, &projectKey)
	if err != nil {
		return nil, nil, err
	}

	var issueNum int64
	err = tx.QueryRow(ctx,
		`UPDATE projects SET issue_seq = issue_seq + 1 WHERE id=$1 RETURNING issue_seq`,
		projectID).Scan(&issueNum)
	if err != nil {
		return nil, nil, err
	}
	issueID := NewID("iss")
	issueKey := fmt.Sprintf("%s-%d", projectKey, issueNum)

	reporter := reporterID
	if reporter == "" {
		reporter = actorID
	}
	if reporter != actorID {
		var reporterExists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND active)`, reporter).Scan(&reporterExists); err != nil {
			return nil, nil, err
		}
		if !reporterExists {
			return nil, nil, fmt.Errorf("reporter account does not exist")
		}
	}
	if assigneeID != "" {
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, assigneeID).Scan(&exists)
		if err != nil || !exists {
			assigneeID = ""
		}
	}

	if err := normalizeVersionFields(ctx, tx, projectID, fields); err != nil {
		return nil, nil, err
	}
	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO issues (id, workspace_id, project_id, key, summary, description, fields, labels,
		                    status_id, issuetype_id, priority_id, assignee_id, reporter_id, security_level_id, parent_id, updated_seq)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,0)`,
		issueID, wsID, projectID, issueKey, summary, description, fieldsJSON, labels, statusID, issueTypeID, nilIfEmpty(priorityID), nilIfEmpty(assigneeID), reporter, nilIfEmpty(securityLevelID), nilIfEmpty(parentID))
	if err != nil {
		return nil, nil, err
	}

	var seq int64
	err = tx.QueryRow(ctx, `UPDATE workspaces SET seq = seq + 1 WHERE id=$1 RETURNING seq`, wsID).Scan(&seq)
	if err != nil {
		return nil, nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE issues SET updated_seq=$2 WHERE id=$1`, issueID, seq)
	if err != nil {
		return nil, nil, err
	}

	issue, err := scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.id=$1`, issueID))
	if err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(models.IssueUpsertPayload{Issue: *issue})
	if err != nil {
		return nil, nil, err
	}
	action := &models.Action{
		WorkspaceID: wsID, Seq: seq, EntityType: models.EntityIssue, EntityID: issueID,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO actions (workspace_id, seq, entity_type, entity_id, op, schema_v, payload, actor_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		wsID, seq, action.EntityType, action.EntityID, action.Op, action.SchemaV, payload, actorID)
	if err != nil {
		return nil, nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_notify('zzira_actions', $1 || '|' || $2)`, wsID, fmt.Sprintf("%d", seq)); err != nil {
		return nil, nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return issue, action, nil
}

// ---- Sync ----

func (s *Store) Head(ctx context.Context, workspaceID string) (int64, error) {
	var head int64
	err := s.Pool.QueryRow(ctx, `SELECT seq FROM workspaces WHERE id=$1`, workspaceID).Scan(&head)
	return head, err
}

// ActionsSince returns visible actions after the checkpoint. It is kept for
// callers that do not need the scan boundary; sync handlers should use
// ActionPageSince so a page containing only filtered actions can still advance.
func (s *Store) ActionsSince(ctx context.Context, workspaceID, userID string, since, limit int64) ([]models.Action, error) {
	actions, _, err := s.ActionPageSince(ctx, workspaceID, userID, since, limit)
	return actions, err
}

// ActionPageSince scans at most limit workspace actions, then filters that
// bounded page for the caller. The returned boundary is the last scanned
// sequence even when every action was hidden. Without it a client can become
// permanently stuck behind another user's notifications or tombstones.
func (s *Store) ActionPageSince(ctx context.Context, workspaceID, userID string, since, limit int64) ([]models.Action, int64, error) {
	to := since
	if err := s.Pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(seq), $2)
		FROM (
			SELECT seq FROM actions
			WHERE workspace_id=$1 AND seq > $2
			ORDER BY seq
			LIMIT $3
		) page`, workspaceID, since, limit).Scan(&to); err != nil {
		return nil, since, err
	}
	if to == since {
		return nil, to, nil
	}
	issueRef := `CASE a.entity_type
		WHEN 'issue' THEN a.entity_id
		WHEN 'comment' THEN COALESCE(a.payload->'comment'->>'issueId', a.payload->>'issueId')
		WHEN 'attachment' THEN COALESCE(a.payload->'attachment'->>'issueId', a.payload->>'issueId')
		WHEN 'worklog' THEN COALESCE(a.payload->'worklog'->>'issueId', a.payload->>'issueId')
		WHEN 'watcher' THEN a.payload->>'issueId'
		WHEN 'sprint_issue' THEN a.payload->>'issueId'
		WHEN 'issue_link' THEN COALESCE(a.payload->'link'->>'inwardIssueId', a.payload->>'inwardIssueId')
		ELSE NULL
	END`
	linkOtherIssueRef := `COALESCE(a.payload->'link'->>'outwardIssueId', a.payload->>'outwardIssueId')`
	canSeeIssue := func(ref string) string {
		return `EXISTS (
			SELECT 1
			FROM (
				SELECT i.project_id, i.security_level_id
				FROM issues i
				WHERE i.workspace_id=$1 AND i.id = ` + ref + `
				UNION ALL
				SELECT d.project_id, d.security_level_id
				FROM deleted_issue_visibility d
				WHERE d.workspace_id=$1 AND d.issue_id = ` + ref + `
			) scoped_issue
			WHERE scoped_issue.security_level_id IS NULL
			   OR EXISTS (
				 SELECT 1 FROM memberships m
				 WHERE m.workspace_id=$1 AND m.user_id=$3 AND m.role='admin'
			   )
			   OR EXISTS (
				 SELECT 1
				 FROM projects p
				 JOIN security_schemes ss ON ss.id=p.security_scheme_id
				 , jsonb_array_elements(ss.levels) lvl
				 WHERE p.id=scoped_issue.project_id
				   AND lvl->>'id'=scoped_issue.security_level_id
				   AND (lvl->'members') @> jsonb_build_array($3)
			   )
		)`
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT a.workspace_id, a.seq, a.entity_type, a.entity_id, a.op, a.schema_v, a.payload, a.actor_id,
		       to_char(a.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM actions a
		WHERE a.workspace_id=$1 AND a.seq > $2 AND a.seq <= $6
		  AND (a.entity_type <> 'dashboard' OR EXISTS (SELECT 1 FROM dashboards d WHERE d.workspace_id=$1 AND d.id=a.entity_id AND `+strings.ReplaceAll(dashboardAccess, "$2", "$3")+`))
		  AND (a.entity_type NOT IN ('wiki_space','wiki_page','wiki_page_like','wiki_page_property','wiki_blogpost','wiki_blogpost_property','wiki_blogpost_label','wiki_blogpost_like','wiki_footer_comment','wiki_footer_comment_like','wiki_inline_comment','wiki_inline_comment_like','wiki_task','wiki_label','wiki_restriction','wiki_attachment','wiki_attachment_property','wiki_content','wiki_content_property') OR EXISTS (
		    SELECT 1 FROM wiki_spaces s WHERE s.workspace_id=$1
		      AND s.id::text=a.payload->>'wikiSpaceId'
		      AND (NOT s.private OR s.author_id=$3)
		      AND (a.entity_type NOT IN ('wiki_page','wiki_page_like','wiki_page_property') OR a.payload->'wiki_page'->>'published'='true'
		        OR a.payload->'wiki_page'->>'authorId'=$3)
		      AND (a.entity_type NOT IN ('wiki_blogpost','wiki_blogpost_property','wiki_blogpost_label','wiki_blogpost_like') OR (a.payload->'wiki_blogpost'->>'published'='true'
		        OR a.payload->'wiki_blogpost'->>'authorId'=$3) AND (COALESCE((a.payload->'wiki_blogpost'->>'private')::boolean,false)=false OR a.payload->'wiki_blogpost'->>'authorId'=$3))
		      AND (a.entity_type NOT IN ('wiki_content','wiki_content_property') OR COALESCE((a.payload->>'contentPrivate')::boolean,false)=false OR a.payload->>'contentAuthorId'=$3)
		      AND (a.entity_type<>'wiki_attachment' OR COALESCE(a.payload->'wiki_attachment'->>'blogPostId','')='' OR
		        ((a.payload->>'blogPublished'='true' OR a.payload->>'blogAuthorId'=$3) AND (COALESCE((a.payload->>'blogPrivate')::boolean,false)=false OR a.payload->>'blogAuthorId'=$3)))
		      AND (a.entity_type NOT IN ('wiki_footer_comment','wiki_footer_comment_like','wiki_inline_comment','wiki_inline_comment_like') OR
		        COALESCE(a.payload->'wiki_footer_comment'->>'blogPostId',a.payload->'wiki_footer_comment_like'->>'blogPostId',a.payload->'wiki_inline_comment'->>'blogPostId',a.payload->'wiki_inline_comment_like'->>'blogPostId','')='' OR
		        ((a.payload->>'blogPublished'='true' OR a.payload->>'blogAuthorId'=$3) AND (COALESCE((a.payload->>'blogPrivate')::boolean,false)=false OR a.payload->>'blogAuthorId'=$3)))
		      AND (a.entity_type NOT IN ('wiki_footer_comment','wiki_footer_comment_like','wiki_inline_comment','wiki_inline_comment_like','wiki_task') OR EXISTS (
		        SELECT 1 FROM wiki_pages wp
		        WHERE wp.id::text=COALESCE(a.payload->'wiki_footer_comment'->>'pageId',a.payload->'wiki_footer_comment_like'->>'pageId',a.payload->'wiki_inline_comment'->>'pageId',a.payload->'wiki_inline_comment_like'->>'pageId',a.payload->'wiki_task'->>'pageId')
		          AND wp.space_id=s.id AND wp.status='current'
		      ) OR COALESCE(a.payload->'wiki_footer_comment'->>'blogPostId',a.payload->'wiki_footer_comment_like'->>'blogPostId',a.payload->'wiki_inline_comment'->>'blogPostId',a.payload->'wiki_inline_comment_like'->>'blogPostId','')<>'')
		      AND (a.entity_type<>'wiki_label' OR COALESCE(a.payload->'wiki_label'->>'pageId','')='' OR EXISTS (
		        SELECT 1 FROM wiki_pages wp
		        WHERE wp.id::text=a.payload->'wiki_label'->>'pageId'
		          AND wp.space_id=s.id AND wp.status='current'
		      ))
		      AND (a.entity_type NOT IN ('wiki_page','wiki_page_like','wiki_page_property','wiki_blogpost','wiki_blogpost_property','wiki_blogpost_label','wiki_blogpost_like','wiki_footer_comment','wiki_footer_comment_like','wiki_inline_comment','wiki_inline_comment_like','wiki_task','wiki_label','wiki_restriction','wiki_attachment','wiki_attachment_property','wiki_content','wiki_content_property')
		        OR COALESCE(a.payload->'wiki_label'->>'pageId','')='' AND a.entity_type='wiki_label'
		        OR a.entity_type IN ('wiki_blogpost','wiki_blogpost_property','wiki_blogpost_label','wiki_blogpost_like')
		        OR a.entity_type='wiki_attachment' AND COALESCE(a.payload->'wiki_attachment'->>'blogPostId','')<>''
		        OR a.entity_type IN ('wiki_footer_comment','wiki_footer_comment_like','wiki_inline_comment','wiki_inline_comment_like') AND
		          COALESCE(a.payload->'wiki_footer_comment'->>'blogPostId',a.payload->'wiki_footer_comment_like'->>'blogPostId',a.payload->'wiki_inline_comment'->>'blogPostId',a.payload->'wiki_inline_comment_like'->>'blogPostId','')<>''
		        OR a.entity_type IN ('wiki_content','wiki_content_property') AND COALESCE(a.payload->>'rootPageId','')=''
		        OR EXISTS (
		          SELECT 1 FROM wiki_pages access_page
		          WHERE access_page.id::text=CASE a.entity_type
		            WHEN 'wiki_page' THEN a.payload->'wiki_page'->>'id'
		            WHEN 'wiki_page_like' THEN a.payload->'wiki_page'->>'id'
		            WHEN 'wiki_page_property' THEN a.payload->'wiki_page'->>'id'
		            WHEN 'wiki_footer_comment' THEN a.payload->'wiki_footer_comment'->>'pageId'
		            WHEN 'wiki_footer_comment_like' THEN a.payload->'wiki_footer_comment_like'->>'pageId'
		            WHEN 'wiki_inline_comment' THEN a.payload->'wiki_inline_comment'->>'pageId'
		            WHEN 'wiki_inline_comment_like' THEN a.payload->'wiki_inline_comment_like'->>'pageId'
		            WHEN 'wiki_task' THEN a.payload->'wiki_task'->>'pageId'
		            WHEN 'wiki_label' THEN a.payload->'wiki_label'->>'pageId'
		            WHEN 'wiki_restriction' THEN a.payload->'wiki_restriction'->>'pageId'
		            WHEN 'wiki_attachment' THEN a.payload->'wiki_attachment'->>'pageId'
		            WHEN 'wiki_attachment_property' THEN a.payload->'wiki_attachment_property'->>'pageId'
		            WHEN 'wiki_content' THEN a.payload->>'rootPageId'
		            WHEN 'wiki_content_property' THEN a.payload->>'rootPageId'
		          END
		            AND access_page.space_id=s.id
		            AND access_page.status='current'
		            AND (
		              access_page.author_id=$3
		              OR EXISTS (SELECT 1 FROM memberships access_admin WHERE access_admin.workspace_id=$1 AND access_admin.user_id=$3 AND access_admin.role='admin')
		              OR NOT EXISTS (SELECT 1 FROM wiki_page_restrictions access_restriction WHERE access_restriction.page_id=access_page.id AND access_restriction.operation='read')
		              OR EXISTS (SELECT 1 FROM wiki_page_restrictions access_restriction WHERE access_restriction.page_id=access_page.id AND access_restriction.operation='read' AND access_restriction.subject_type='user' AND access_restriction.subject_id=$3)
		              OR EXISTS (SELECT 1 FROM wiki_page_restrictions access_restriction JOIN group_members access_member ON access_restriction.subject_type='group' AND access_member.group_id::text=access_restriction.subject_id WHERE access_restriction.page_id=access_page.id AND access_restriction.operation='read' AND access_member.user_id=$3)
		            )
		        )
		      )
		  ))
		  AND (
		    CASE a.entity_type
		      WHEN $4 THEN a.payload->'notification'->>'userId'
		      WHEN $5 THEN a.payload->>'userId'
		      WHEN 'wiki_watch' THEN a.payload->'wiki_watch'->>'userId'
		      ELSE $3
		    END = $3
			  )
			  AND (
			    a.entity_type NOT IN ('issue','comment','attachment','worklog','watcher','sprint_issue','issue_link')
			    OR (
			      `+canSeeIssue(issueRef)+`
			      AND (a.entity_type <> 'issue_link' OR `+canSeeIssue(linkOtherIssueRef)+`)
			    )
			  )
		ORDER BY a.seq`,
		workspaceID, since, userID, models.EntityNotification, models.EntityTombstone, to)
	if err != nil {
		return nil, since, err
	}
	defer rows.Close()
	var out []models.Action
	for rows.Next() {
		var a models.Action
		if err := rows.Scan(&a.WorkspaceID, &a.Seq, &a.EntityType, &a.EntityID,
			&a.Op, &a.SchemaV, &a.Payload, &a.ActorID, &a.CreatedAt); err != nil {
			return nil, since, err
		}
		out = append(out, a)
	}
	return out, to, rows.Err()
}

func DSNFromEnv() string {
	if d := os.Getenv("DATABASE_URL"); d != "" {
		return d
	}
	return "postgres://zzira:zzira@localhost:5433/zzira?sslmode=disable"
}
