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
	var idToken string
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(oidc_id_token, '') FROM sessions WHERE token_hash=$1 AND expires_at > now()`, tokenHash).Scan(&idToken)
	return idToken, err
}

func (s *Store) CreateOIDCLoginState(ctx context.Context, state, nonce, codeVerifier string, ttl time.Duration) error {
	_, err := s.Pool.Exec(ctx,
		`WITH expired AS (DELETE FROM oidc_login_states WHERE expires_at <= now())
		 INSERT INTO oidc_login_states (state_hash, nonce, code_verifier, expires_at) VALUES ($1,$2,$3,now() + $4::interval)`,
		HashToken(state), nonce, codeVerifier, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	return err
}

func (s *Store) ConsumeOIDCLoginState(ctx context.Context, state string) (nonce, codeVerifier string, err error) {
	err = s.Pool.QueryRow(ctx,
		`DELETE FROM oidc_login_states WHERE state_hash=$1 AND expires_at > now() RETURNING nonce, code_verifier`, HashToken(state)).
		Scan(&nonce, &codeVerifier)
	return nonce, codeVerifier, err
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
		if err := tx.QueryRow(ctx, `SELECT id FROM workspaces ORDER BY id LIMIT 1`).Scan(&workspaceID); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1,$2,'member') ON CONFLICT (workspace_id, user_id) DO NOTHING`,
			workspaceID, userID); err != nil {
			return "", err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO oidc_identities (issuer, subject, user_id) VALUES ($1,$2,$3)`, issuer, subject, userID); err != nil {
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
// column and never successfully compare. A user or membership that already
// exists is left untouched -- this only ever adds, on every boot, matching
// migrations/002_seed.sql's own idempotent shape.
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
		`INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1,$2,$3) ON CONFLICT (workspace_id, user_id) DO NOTHING`,
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
	err = s.Pool.QueryRow(ctx, `SELECT id, slug FROM workspaces ORDER BY id LIMIT 1`).Scan(&id, &slug)
	return
}

func (s *Store) WorkspaceBySlug(ctx context.Context, slug string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT id FROM workspaces WHERE slug=$1`, slug).Scan(&id)
	return id, err
}

// IsMember is the V0 authz scope: workspace membership grants the workspace log.
func (s *Store) IsMember(ctx context.Context, workspaceID, userID string) (bool, error) {
	var ok bool
	err := s.Pool.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id
			WHERE m.workspace_id=$1 AND m.user_id=$2 AND u.active
		)`,
		workspaceID, userID).Scan(&ok)
	return ok, err
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
		`SELECT id, name, COALESCE(workflow_id,''), COALESCE(security_scheme_id,'') FROM projects WHERE workspace_id=$1 AND upper(key)=upper($2)`,
		workspaceID, key).Scan(&p.ID, &p.Name, &p.WorkflowID, &p.SecuritySchemeID)
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
`

func scanIssue(row pgx.Row) (*models.Issue, error) {
	i := &models.Issue{}
	var priorityID, priorityName *string
	var assigneeID, assigneeName *string
	var reporterID, reporterName *string
	var updatedAt time.Time
	var securityLevelID *string
	var fieldsJSON []byte
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.ProjectID, &i.Key, &i.Summary, &i.Description,
		&i.Status.ID, &i.Status.Name, &i.Status.Category,
		&i.IssueType.ID, &i.IssueType.Name, &i.IssueType.Icon,
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
	return i, nil
}

func (s *Store) IssueByIDOrKey(ctx context.Context, workspaceID, idOrKey string) (*models.Issue, error) {
	return scanIssue(s.Pool.QueryRow(ctx, issueJoin+`
		WHERE i.workspace_id=$1 AND (i.id=$2 OR upper(i.key)=upper($2))`, workspaceID, idOrKey))
}

// CreateIssue runs the canonical write transaction: state change + action append +
// notify, all-or-nothing. Returns the persisted issue and its action.
func (s *Store) CreateIssue(ctx context.Context, actorID, projectID, summary string, description json.RawMessage, statusID, issueTypeID, priorityID, assigneeID string, labels []string, fields map[string]json.RawMessage, securityLevelID string) (*models.Issue, *models.Action, error) {
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

	reporter := actorID
	if assigneeID != "" {
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, assigneeID).Scan(&exists)
		if err != nil || !exists {
			assigneeID = ""
		}
	}

	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO issues (id, workspace_id, project_id, key, summary, description, fields, labels,
		                    status_id, issuetype_id, priority_id, assignee_id, reporter_id, security_level_id, updated_seq)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,0)`,
		issueID, wsID, projectID, issueKey, summary, description, fieldsJSON, labels, statusID, issueTypeID, nilIfEmpty(priorityID), nilIfEmpty(assigneeID), reporter, nilIfEmpty(securityLevelID))
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
		  AND (
		    CASE a.entity_type
		      WHEN $4 THEN a.payload->'notification'->>'userId'
		      WHEN $5 THEN a.payload->>'userId'
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
