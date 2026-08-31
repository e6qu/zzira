package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	_, _ = rand.Read(b)
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
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
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
		`SELECT email, display_name, time_zone FROM users WHERE id=$1`, id).
		Scan(&u.Email, &u.DisplayName, &u.TimeZone)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) CreateSession(ctx context.Context, tokenHash, userID string, ttl time.Duration) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1,$2,now() + $3::interval)`,
		tokenHash, userID, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	return err
}

// CreateOIDCSession records an opaque browser session and its ID token server-side
// so RP-initiated logout can send the provider an id_token_hint.
func (s *Store) CreateOIDCSession(ctx context.Context, tokenHash, userID, idToken string, ttl time.Duration) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, oidc_id_token, expires_at) VALUES ($1,$2,$3,now() + $4::interval)`,
		tokenHash, userID, idToken, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	return err
}

func (s *Store) SessionUser(ctx context.Context, tokenHash string) (string, error) {
	var userID string
	err := s.Pool.QueryRow(ctx,
		`SELECT user_id FROM sessions WHERE token_hash=$1 AND expires_at > now()`, tokenHash).Scan(&userID)
	return userID, err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, tokenHash)
	return err
}

func (s *Store) OIDCSessionToken(ctx context.Context, tokenHash string) (string, error) {
	var idToken string
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(oidc_id_token, '') FROM sessions WHERE token_hash=$1 AND expires_at > now()`, tokenHash).Scan(&idToken)
	return idToken, err
}

func (s *Store) CreateOIDCLoginState(ctx context.Context, state, nonce, codeVerifier string, ttl time.Duration) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO oidc_login_states (state_hash, nonce, code_verifier, expires_at) VALUES ($1,$2,$3,now() + $4::interval)`,
		HashToken(state), nonce, codeVerifier, fmt.Sprintf("%d seconds", int(ttl.Seconds())))
	return err
}

func (s *Store) ConsumeOIDCLoginState(ctx context.Context, state string) (nonce, codeVerifier string, err error) {
	err = s.Pool.QueryRow(ctx,
		`DELETE FROM oidc_login_states WHERE state_hash=$1 AND expires_at > now() RETURNING nonce, code_verifier`, HashToken(state)).
		Scan(&nonce, &codeVerifier)
	return nonce, codeVerifier, err
}

// ResolveOIDCUser binds the first verified sign-in to a pre-existing member by
// email, then identifies all later sign-ins by the immutable issuer/subject
// pair. It never creates users or grants workspace membership.
func (s *Store) ResolveOIDCUser(ctx context.Context, issuer, subject, email string) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID string
	err = tx.QueryRow(ctx, `SELECT user_id FROM oidc_identities WHERE issuer=$1 AND subject=$2`, issuer, subject).Scan(&userID)
	if err == nil {
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
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO oidc_identities (issuer, subject, user_id) VALUES ($1,$2,$3)`, issuer, subject, userID); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return userID, nil
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
		`SELECT EXISTS(SELECT 1 FROM memberships WHERE workspace_id=$1 AND user_id=$2)`,
		workspaceID, userID).Scan(&ok)
	return ok, err
}

func (s *Store) AddMember(ctx context.Context, workspaceID, userID, role string) error {
	_, err := s.Pool.Exec(ctx,
		`INSERT INTO memberships (workspace_id, user_id, role) VALUES ($1,$2,$3)
		 ON CONFLICT DO NOTHING`, workspaceID, userID, role)
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
       i.security_level_id, i.fields,
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
		&securityLevelID, &fieldsJSON,
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
func (s *Store) CreateIssue(ctx context.Context, actorID, projectID, summary string, description json.RawMessage, statusID, issueTypeID, priorityID, assigneeID string, fields map[string]json.RawMessage) (*models.Issue, *models.Action, error) {
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
		INSERT INTO issues (id, workspace_id, project_id, key, summary, description, fields,
		                    status_id, issuetype_id, priority_id, assignee_id, reporter_id, updated_seq)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,0)`,
		issueID, wsID, projectID, issueKey, summary, description, fieldsJSON, statusID, issueTypeID, nilIfEmpty(priorityID), nilIfEmpty(assigneeID), reporter)
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

// ActionsSince returns the workspace's actions after the checkpoint. Per-user
// entities (notifications, tombstones) are filtered to the caller, and actions
// on security-restricted issues are hidden from non-members.
func (s *Store) ActionsSince(ctx context.Context, workspaceID, userID string, since, limit int64) ([]models.Action, error) {
	issueRef := `CASE WHEN a.entity_type = 'issue' THEN a.entity_id ELSE NULLIF(a.payload->>'issueId','') END`
	rows, err := s.Pool.Query(ctx, `
		SELECT a.workspace_id, a.seq, a.entity_type, a.entity_id, a.op, a.schema_v, a.payload, a.actor_id,
		       to_char(a.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM actions a
		WHERE a.workspace_id=$1 AND a.seq > $2
		  AND (
		    CASE a.entity_type
		      WHEN $4 THEN a.payload->'notification'->>'userId'
		      WHEN $5 THEN a.payload->>'userId'
		      ELSE $3
		    END = $3
		  )
		  AND (
		    a.entity_type NOT IN ('issue','comment','attachment','worklog')
		    OR NOT EXISTS (SELECT 1 FROM issues ci WHERE ci.id = `+issueRef+`)
		    OR NOT EXISTS (
		      SELECT 1
		      FROM issues ci
		      WHERE ci.id = `+issueRef+`
		        AND ci.security_level_id IS NOT NULL
		        AND NOT EXISTS (
		          SELECT 1 FROM memberships m
		          WHERE m.workspace_id = $1 AND m.user_id = $3 AND m.role = 'admin'
		        )
		        AND EXISTS (
		          SELECT 1
		          FROM projects p
		          JOIN security_schemes ss ON ss.id = p.security_scheme_id
		          , jsonb_array_elements(ss.levels) lvl
		          WHERE p.id = ci.project_id
		            AND lvl->>'id' = ci.security_level_id
		            AND NOT ((lvl->'members') @> jsonb_build_array($3))
		        )
		    )
		  )
		ORDER BY a.seq LIMIT $6`,
		workspaceID, since, userID, models.EntityNotification, models.EntityTombstone, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Action
	for rows.Next() {
		var a models.Action
		if err := rows.Scan(&a.WorkspaceID, &a.Seq, &a.EntityType, &a.EntityID,
			&a.Op, &a.SchemaV, &a.Payload, &a.ActorID, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func DSNFromEnv() string {
	if d := os.Getenv("DATABASE_URL"); d != "" {
		return d
	}
	return "postgres://zzira:zzira@localhost:5433/zzira?sslmode=disable"
}
