package store

import (
	"context"

	"github.com/e6qu/zzira/internal/models"
)

// FirstIssueType returns the seeded default issue type (V0 has exactly one).
func (s *Store) FirstIssueType(ctx context.Context) (*models.IssueType, error) {
	t := &models.IssueType{}
	err := s.Pool.QueryRow(ctx, `SELECT id, name, COALESCE(icon,'') FROM issue_types LIMIT 1`).
		Scan(&t.ID, &t.Name, &t.Icon)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// IssueTypeByIDOrName resolves the two Jira wire representations to the
// canonical internal identifier. Issue types are workspace-independent today.
func (s *Store) IssueTypeByIDOrName(ctx context.Context, idOrName string) (*models.IssueType, error) {
	t := &models.IssueType{}
	err := s.Pool.QueryRow(ctx, `SELECT id, name, COALESCE(icon,'') FROM issue_types WHERE id=$1 OR name=$1 LIMIT 1`, idOrName).
		Scan(&t.ID, &t.Name, &t.Icon)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// PriorityByIDOrName resolves the two Jira wire representations to the
// canonical internal identifier. Priorities are workspace-independent today.
func (s *Store) PriorityByIDOrName(ctx context.Context, idOrName string) (*models.Priority, error) {
	p := &models.Priority{}
	err := s.Pool.QueryRow(ctx, `SELECT id, name FROM priorities WHERE id=$1 OR name=$1 LIMIT 1`, idOrName).
		Scan(&p.ID, &p.Name)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// DefaultProject returns the seeded demo project (V0 has exactly one).
func (s *Store) DefaultProject(ctx context.Context) (*models.Project, error) {
	p := &models.Project{}
	err := s.Pool.QueryRow(ctx, `SELECT id, workspace_id, key, name, COALESCE(workflow_id,''), COALESCE(security_scheme_id,'') FROM projects LIMIT 1`).
		Scan(&p.ID, &p.WorkspaceID, &p.Key, &p.Name, &p.WorkflowID, &p.SecuritySchemeID)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// DefaultProjectInWorkspace returns the workspace's first project. Web flows
// must use this scoped lookup rather than the administrative global helper.
func (s *Store) DefaultProjectInWorkspace(ctx context.Context, workspaceID string) (*models.Project, error) {
	p := &models.Project{}
	err := s.Pool.QueryRow(ctx, `SELECT id, workspace_id, key, name, COALESCE(workflow_id,''), COALESCE(security_scheme_id,'') FROM projects WHERE workspace_id=$1 ORDER BY key LIMIT 1`, workspaceID).
		Scan(&p.ID, &p.WorkspaceID, &p.Key, &p.Name, &p.WorkflowID, &p.SecuritySchemeID)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// StatusByID returns one status (transitions beans, diff display names).
func (s *Store) StatusByID(ctx context.Context, id string) (models.Status, error) {
	var st models.Status
	err := s.Pool.QueryRow(ctx, `SELECT id, name, category FROM statuses WHERE id=$1`, id).
		Scan(&st.ID, &st.Name, &st.Category)
	return st, err
}

// IssuesByProject lists a project's issues, newest activity first (V0 navigator-lite),
// filtered to issues the user may see.
func (s *Store) IssuesByProject(ctx context.Context, workspaceID, projectID, userID string) ([]*models.Issue, error) {
	rows, err := s.Pool.Query(ctx, issueJoin+`
		WHERE i.workspace_id=$1 AND i.project_id=$2 AND `+VisibleIssuePredicate("i", "$3")+` ORDER BY i.updated_seq DESC LIMIT 200`,
		workspaceID, projectID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Issue
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Priorities lists the registry.
func (s *Store) Priorities(ctx context.Context) ([]*models.Priority, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, COALESCE(icon_url,'') FROM priorities ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Priority
	for rows.Next() {
		p := &models.Priority{}
		if err := rows.Scan(&p.ID, &p.Name, &p.ID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AllStatuses lists the status registry.
func (s *Store) AllStatuses(ctx context.Context) ([]models.Status, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, category FROM statuses ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Status
	for rows.Next() {
		var st models.Status
		if err := rows.Scan(&st.ID, &st.Name, &st.Category); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// DashboardStats powers the home dashboard: per-status counts, the caller's
// open assigned issues, and recent activity.
type DashboardStats struct {
	StatusCounts []struct {
		Status models.Status
		Count  int
	}
	MyOpenIssues int
	Recent       []RecentActivity
}

type RecentActivity struct {
	ActorName string
	Summary   string
	Op        string
	IssueKey  string
	Created   string
}

func (s *Store) DashboardData(ctx context.Context, workspaceID, userID string) (*DashboardStats, error) {
	stats := &DashboardStats{}
	rows, err := s.Pool.Query(ctx, `
		SELECT st.id, st.name, st.category, COUNT(*)
		FROM issues i JOIN statuses st ON st.id = i.status_id
		WHERE i.workspace_id=$1 AND `+VisibleIssuePredicate("i", "$2")+`
		GROUP BY st.id, st.name, st.category ORDER BY st.id`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sc struct {
			Status models.Status
			Count  int
		}
		if err := rows.Scan(&sc.Status.ID, &sc.Status.Name, &sc.Status.Category, &sc.Count); err != nil {
			return nil, err
		}
		stats.StatusCounts = append(stats.StatusCounts, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := s.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM issues
		WHERE workspace_id=$1 AND assignee_id=$2 AND status_id <> 'st_done'
		  AND `+VisibleIssuePredicate("issues", "$2"),
		workspaceID, userID).Scan(&stats.MyOpenIssues); err != nil {
		return nil, err
	}

	arows, err := s.Pool.Query(ctx, `
		SELECT a.entity_id, a.op, COALESCE(u.display_name,''), i.key, i.summary,
		       to_char(a.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM actions a
		JOIN issues i ON i.id = a.entity_id AND i.workspace_id = a.workspace_id
		LEFT JOIN users u ON u.id = a.actor_id
		WHERE a.workspace_id=$1 AND a.entity_type='issue' AND a.op <> 'delete'
		  AND `+VisibleIssuePredicate("i", "$2")+`
		ORDER BY a.seq DESC LIMIT 10`, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var ra RecentActivity
		var entityID string
		if err := arows.Scan(&entityID, &ra.Op, &ra.ActorName, &ra.IssueKey, &ra.Summary, &ra.Created); err != nil {
			return nil, err
		}
		stats.Recent = append(stats.Recent, ra)
	}
	return stats, arows.Err()
}
