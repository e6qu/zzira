package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
)

// searchJoin aliases match jql.DefaultResolver's column expressions
// (statuses=st, issuetypes=it, priorities=pr2, assignee=a, reporter=r, project=pr).
const searchJoin = `
FROM issues i
JOIN statuses st ON st.id = i.status_id
JOIN issue_types it ON it.id = i.issuetype_id
LEFT JOIN priorities pr2 ON pr2.id = i.priority_id
LEFT JOIN users a ON a.id = i.assignee_id
LEFT JOIN users r ON r.id = i.reporter_id
JOIN projects pr ON pr.id = i.project_id
`

const searchSelect = `
SELECT i.id, i.workspace_id, i.project_id, i.key, i.summary, i.description,
       st.id, st.name, st.category,
       it.id, it.name, it.icon,
       pr2.id, pr2.name,
       a.id, a.display_name,
       r.id, r.display_name,
       i.rank,
       i.security_level_id, i.fields,
       i.updated_seq, i.updated_at
`

// Search runs a compiled JQL query within one workspace. The workspace
// predicate plus issue-security visibility bound to userID form the
// permission scope.
func (s *Store) Search(ctx context.Context, workspaceID, userID string, c jql.Compiled, limit, offset int) ([]*models.Issue, int, error) {
	if c.Err != nil {
		return nil, 0, c.Err
	}
	where := "i.workspace_id = $1"
	args := []any{workspaceID}
	if c.Where != "" {
		// placeholders in c.Where are numbered from $2 (workspace owns $1)
		where += " AND (" + c.Where + ")"
		args = append(args, c.Args...)
	}
	// visibility: user arg appended after the compiled args
	userPH := "$" + fmt.Sprintf("%d", len(args)+1)
	args = append(args, userID)
	where += " AND " + VisibleIssuePredicate("i", userPH)
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT COUNT(*) `+searchJoin+` WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx,
		searchSelect+" "+searchJoin+" WHERE "+where+" ORDER BY "+c.OrderSQL+" LIMIT "+fmt.Sprintf("%d OFFSET %d", limit, offset),
		args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*models.Issue
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, i)
	}
	return out, total, rows.Err()
}

// MembersByWorkspace lists workspace members (assignee pickers, user search).
func (s *Store) MembersByWorkspace(ctx context.Context, workspaceID string) ([]*models.User, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT u.id, u.email, u.display_name, u.time_zone
		FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id=$1 AND u.active
		ORDER BY u.display_name`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.User
	for rows.Next() {
		u := &models.User{Active: true, AccountType: "atlassian"}
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.TimeZone); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// MemberByID returns an active user only when they belong to workspaceID.
// User lookup at the API edge must retain this scope: user IDs are globally
// addressable, but user profile data is visible only within a shared workspace.
func (s *Store) MemberByID(ctx context.Context, workspaceID, userID string) (*models.User, error) {
	u := &models.User{ID: userID, Active: true, AccountType: "atlassian"}
	err := s.Pool.QueryRow(ctx, `
		SELECT u.email, u.display_name, u.time_zone
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id=$1 AND u.id=$2 AND u.active`, workspaceID, userID).
		Scan(&u.Email, &u.DisplayName, &u.TimeZone)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// SearchMembers filters workspace members by name/email substring.
func (s *Store) SearchMembers(ctx context.Context, workspaceID, query string) ([]*models.User, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT u.id, u.email, u.display_name, u.time_zone
		FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id=$1 AND u.active
		  AND (u.display_name ILIKE $2 OR u.email ILIKE $2)
		ORDER BY u.display_name LIMIT 50`, workspaceID, "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.User
	for rows.Next() {
		u := &models.User{Active: true, AccountType: "atlassian"}
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.TimeZone); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ProjectsByWorkspace lists all projects in a workspace (V2: all visible to members).
func (s *Store) ProjectsByWorkspace(ctx context.Context, workspaceID string) ([]*models.Project, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, workspace_id, key, name FROM projects WHERE workspace_id=$1 ORDER BY key`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Project
	for rows.Next() {
		p := &models.Project{}
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.Key, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// BootstrapSnapshot dumps the workspace at a consistent head seq, filtered
// to what userID may see (issue security applied via the visibility
// predicate; admins bypass).
func (s *Store) BootstrapSnapshot(ctx context.Context, workspaceID, userID string) (*models.Snapshot, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var head int64
	if err := tx.QueryRow(ctx, `SELECT seq FROM workspaces WHERE id=$1`, workspaceID).Scan(&head); err != nil {
		return nil, err
	}

	issues := []*models.Issue{}
	irows, err := tx.Query(ctx, searchSelect+" "+issueJoinTables()+` WHERE i.workspace_id=$1 AND `+VisibleIssuePredicate("i", "$2")+` ORDER BY i.updated_seq`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer irows.Close()
	for irows.Next() {
		i, err := scanIssue(irows)
		if err != nil {
			return nil, err
		}
		issues = append(issues, i)
	}
	if err := irows.Err(); err != nil {
		return nil, err
	}

	comments := []models.Comment{}
	crows, err := tx.Query(ctx, `
		SELECT c.id, c.issue_id, c.author_id, COALESCE(u.display_name,''), c.body,
		       to_char(c.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM comments c
		JOIN issues i ON i.id = c.issue_id AND i.workspace_id = c.workspace_id
		LEFT JOIN users u ON u.id = c.author_id
		WHERE c.workspace_id=$1 AND `+VisibleIssuePredicate("i", "$2")+` ORDER BY c.created_at`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		c := models.Comment{}
		if err := crows.Scan(&c.ID, &c.IssueID, &c.AuthorID, &c.AuthorName, &c.Body, &c.Created); err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}

	attRows, err := tx.Query(ctx, `
		SELECT a.id, a.issue_id, a.filename, a.mime_type, a.size, a.author_id,
		       COALESCE(u.display_name,''),
		       to_char(a.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM attachments a
		JOIN issues i ON i.id = a.issue_id AND i.workspace_id = a.workspace_id
		LEFT JOIN users u ON u.id = a.author_id
		WHERE a.workspace_id=$1 AND `+VisibleIssuePredicate("i", "$2")+` ORDER BY a.created_at`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer attRows.Close()
	attachments := []models.Attachment{}
	for attRows.Next() {
		a := models.Attachment{}
		if err := attRows.Scan(&a.ID, &a.IssueID, &a.Filename, &a.MimeType, &a.Size, &a.AuthorID, &a.AuthorName, &a.Created); err != nil {
			return nil, err
		}
		attachments = append(attachments, a)
	}
	if err := attRows.Err(); err != nil {
		return nil, err
	}

	wlRows, err := tx.Query(ctx, `
		SELECT w.id, w.issue_id, w.author_id, COALESCE(u.display_name,''),
		       COALESCE(w.comment::text,''), w.time_spent_seconds,
		       to_char(w.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM worklogs w
		JOIN issues i ON i.id = w.issue_id AND i.workspace_id = w.workspace_id
		LEFT JOIN users u ON u.id = w.author_id
		WHERE w.workspace_id=$1 AND `+VisibleIssuePredicate("i", "$2")+` ORDER BY w.created_at`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer wlRows.Close()
	worklogs := []models.Worklog{}
	for wlRows.Next() {
		w := models.Worklog{}
		var comment string
		if err := wlRows.Scan(&w.ID, &w.IssueID, &w.AuthorID, &w.AuthorName, &comment, &w.TimeSpentSeconds, &w.Created); err != nil {
			return nil, err
		}
		if comment != "" {
			w.Comment = json.RawMessage(comment)
		}
		worklogs = append(worklogs, w)
	}
	if err := wlRows.Err(); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &models.Snapshot{Seq: head, Issues: derefIssues(issues), Comments: comments, Attachments: attachments, Worklogs: worklogs}, nil
}

func issueJoinTables() string {
	return ` FROM issues i
	JOIN statuses st ON st.id = i.status_id
	JOIN issue_types it ON it.id = i.issuetype_id
	LEFT JOIN priorities pr2 ON pr2.id = i.priority_id
	LEFT JOIN users a ON a.id = i.assignee_id
	LEFT JOIN users r ON r.id = i.reporter_id
	JOIN projects pr ON pr.id = i.project_id`
}

func derefIssues(in []*models.Issue) []models.Issue {
	out := make([]models.Issue, 0, len(in))
	for _, i := range in {
		out = append(out, *i)
	}
	return out
}

// ---- saved filters (read-only in V2) ----

func (s *Store) ListFilters(ctx context.Context, workspaceID, userID string) ([]*models.Filter, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT f.id, f.name, COALESCE(f.jql,''), COALESCE(f.description,''), COALESCE(f.owner_id,''), COALESCE(u.display_name,''),
		       EXISTS(SELECT 1 FROM filter_favourites ff WHERE ff.filter_id=f.id AND ff.user_id=$2)
		FROM filters f LEFT JOIN users u ON u.id = f.owner_id
		WHERE f.workspace_id=$1 ORDER BY f.name`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Filter
	for rows.Next() {
		f := &models.Filter{}
		if err := rows.Scan(&f.ID, &f.Name, &f.JQL, &f.Description, &f.OwnerID, &f.OwnerName, &f.Favourite); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) FilterByID(ctx context.Context, workspaceID, userID, id string) (*models.Filter, error) {
	f := &models.Filter{}
	err := s.Pool.QueryRow(ctx, `
		SELECT f.id, f.name, COALESCE(f.jql,''), COALESCE(f.description,''), COALESCE(f.owner_id,''), COALESCE(u.display_name,''),
		       EXISTS(SELECT 1 FROM filter_favourites ff WHERE ff.filter_id=f.id AND ff.user_id=$3)
		FROM filters f LEFT JOIN users u ON u.id = f.owner_id
		WHERE f.id=$1 AND f.workspace_id=$2`, id, workspaceID, userID).
		Scan(&f.ID, &f.Name, &f.JQL, &f.Description, &f.OwnerID, &f.OwnerName, &f.Favourite)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// SecuritySchemes lists all stored schemes.
func (s *Store) SecuritySchemes(ctx context.Context) ([]models.SecurityScheme, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, levels FROM security_schemes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.SecurityScheme
	for rows.Next() {
		var sc models.SecurityScheme
		var levels []byte
		if err := rows.Scan(&sc.ID, &sc.Name, &levels); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(levels, &sc.Levels); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// VisibleIssuePredicate returns a SQL fragment asserting that the issue row
// (aliased `alias`, e.g. "i") is visible to the user bound at placeholder
// `userPlaceholder` (e.g. "$2"). Admins bypass issue security. Callers must
// append userID to their args at that position.
func VisibleIssuePredicate(alias string, userPlaceholder string) string {
	return "(" +
		alias + ".security_level_id IS NULL" +
		" OR EXISTS (SELECT 1 FROM memberships m WHERE m.workspace_id = " + alias + ".workspace_id AND m.user_id = " + userPlaceholder + " AND m.role = 'admin')" +
		" OR EXISTS (" +
		"SELECT 1 FROM projects p" +
		" JOIN security_schemes ss ON ss.id = p.security_scheme_id" +
		", jsonb_array_elements(ss.levels) lvl" +
		" WHERE p.id = " + alias + ".project_id AND lvl->>'id' = " + alias + ".security_level_id" +
		" AND (lvl->'members') @> jsonb_build_array(" + userPlaceholder + ")" +
		"))"
}
