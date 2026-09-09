package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
LEFT JOIN issues parent ON parent.id = i.parent_id
JOIN projects pr ON pr.id = i.project_id
`

const searchSelect = `
SELECT i.id, i.jira_id, i.workspace_id, i.project_id, i.key, i.summary, i.description,
       st.id, st.name, st.category,
	       it.id, it.name, it.icon,
	       it.subtask,
	       parent.id, parent.jira_id, parent.key, parent.summary,
       pr2.id, pr2.name,
       a.id, a.display_name,
	       r.id, r.display_name,
	       i.rank,
	       i.security_level_id, i.fields, i.labels,
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

// MatchIssueIDs evaluates one compiled query only against the caller-supplied
// issue IDs. This keeps Jira's bulk match resource bounded independently of the
// workspace's total issue count and applies the same issue-security predicate
// used by normal search.
func (s *Store) MatchIssueIDs(ctx context.Context, workspaceID, userID string, c jql.Compiled, issueIDs []int64) ([]int64, error) {
	if c.Err != nil {
		return nil, c.Err
	}
	if len(issueIDs) == 0 {
		return []int64{}, nil
	}
	where := "i.workspace_id = $1"
	args := []any{workspaceID}
	if c.Where != "" {
		where += " AND (" + c.Where + ")"
		args = append(args, c.Args...)
	}
	userPH := "$" + fmt.Sprintf("%d", len(args)+1)
	args = append(args, userID)
	where += " AND " + VisibleIssuePredicate("i", userPH)
	idsPH := "$" + fmt.Sprintf("%d", len(args)+1)
	args = append(args, issueIDs)
	where += " AND i.jira_id=ANY(" + idsPH + "::BIGINT[])"
	rows, err := s.Pool.Query(ctx, `SELECT i.jira_id `+searchJoin+` WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matched := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		matched = append(matched, id)
	}
	return matched, rows.Err()
}

// JQLFieldSuggestions returns distinct values observed on issues the viewer can
// browse. The field switch chooses fixed SQL fragments; user input is always a
// bound value.
func (s *Store) JQLFieldSuggestions(ctx context.Context, workspaceID, userID, field, needle string, limit int) ([]string, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	var valueSQL, extraJoin, present string
	switch strings.ToLower(field) {
	case "labels":
		valueSQL, extraJoin, present = "label", "CROSS JOIN LATERAL unnest(i.labels) label", "label <> ''"
	case "component", "sprint", "resolution":
		key := strings.ToLower(field)
		valueSQL = `i.fields->>'` + key + `'`
		present = valueSQL + " IS NOT NULL AND " + valueSQL + " <> ''"
	case "fixversion", "affectedversion":
		key := "fixVersions"
		if strings.EqualFold(field, "affectedversion") {
			key = "versions"
		}
		extraJoin = `CROSS JOIN LATERAL jsonb_array_elements(COALESCE(i.fields->'` + key + `','[]'::jsonb)) version`
		valueSQL, present = `COALESCE(version->>'name',version->>'id')`, `COALESCE(version->>'name',version->>'id','') <> ''`
	default:
		return nil, fmt.Errorf("field does not provide stored suggestions")
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT `+valueSQL+` AS value `+searchJoin+` `+extraJoin+`
		WHERE i.workspace_id=$1 AND `+VisibleIssuePredicate("i", "$2")+` AND `+present+`
		  AND ($3='' OR `+valueSQL+` ILIKE '%'||$3||'%')
		ORDER BY value LIMIT `+fmt.Sprintf("%d", limit), workspaceID, userID, needle)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// MembersByWorkspace lists workspace members (assignee pickers, user search).
func (s *Store) MembersByWorkspace(ctx context.Context, workspaceID string) ([]*models.User, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT u.id, u.email, u.display_name, u.time_zone
		FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id=$1 AND u.active AND u.id NOT LIKE 'app!_%' ESCAPE '!'
		  AND EXISTS (
		    SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id
		    JOIN directory_users du ON du.directory_id=d.id AND du.user_id=u.id
		    WHERE si.workspace_id=m.workspace_id AND d.active AND du.active)
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
		WHERE m.workspace_id=$1 AND u.id=$2 AND u.active
		  AND EXISTS (
		    SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id
		    JOIN directory_users du ON du.directory_id=d.id AND du.user_id=u.id
		    WHERE si.workspace_id=m.workspace_id AND d.active AND du.active)`, workspaceID, userID).
		Scan(&u.Email, &u.DisplayName, &u.TimeZone)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// SearchMembers filters workspace members by name/email substring.
func (s *Store) SearchMembers(ctx context.Context, workspaceID, query string) ([]*models.User, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT u.id, u.email, u.display_name, u.time_zone
		FROM memberships m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id=$1 AND u.active AND u.id NOT LIKE 'app!_%' ESCAPE '!'
		  AND EXISTS (
		    SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id
		    JOIN directory_users du ON du.directory_id=d.id AND du.user_id=u.id
		    WHERE si.workspace_id=m.workspace_id AND d.active AND du.active)
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
		`SELECT id, workspace_id, key, name, COALESCE(workflow_id,''), COALESCE(security_scheme_id,''), description, url, COALESCE(lead_account_id,''), assignee_type, project_type_key FROM projects WHERE workspace_id=$1 ORDER BY key`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Project
	for rows.Next() {
		p := &models.Project{}
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.Key, &p.Name, &p.WorkflowID, &p.SecuritySchemeID, &p.Description, &p.URL, &p.LeadAccountID, &p.AssigneeType, &p.ProjectTypeKey); err != nil {
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
	LEFT JOIN issues parent ON parent.id = i.parent_id
	JOIN projects pr ON pr.id = i.project_id`
}

func derefIssues(in []*models.Issue) []models.Issue {
	out := make([]models.Issue, 0, len(in))
	for _, i := range in {
		out = append(out, *i)
	}
	return out
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
