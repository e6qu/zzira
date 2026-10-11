package store

import (
	"context"
	"github.com/e6qu/zzira/internal/models"
)

const publicCollectionPageSize = 50

// PublicProjectsAfter pages active projects granted to an anonymous subject.
// The cursor is a project key; it never supplies an identity or permission.
func (s *Store) PublicProjectsAfter(ctx context.Context, workspaceID, after string) ([]*models.Project, string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+projectSelectColumnsP+` FROM projects p WHERE p.workspace_id=$1 AND p.key>$2 AND p.lifecycle_state='ACTIVE' AND jira_project_permission_without_issue(p.workspace_id,p.id,NULL,'BROWSE_PROJECTS') ORDER BY p.key LIMIT $3`, workspaceID, after, publicCollectionPageSize+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	projects := []*models.Project{}
	for rows.Next() {
		project, e := scanProject(rows)
		if e != nil {
			return nil, "", e
		}
		projects = append(projects, project)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(projects) > publicCollectionPageSize {
		projects = projects[:publicCollectionPageSize]
		next = projects[len(projects)-1].Key
	}
	return projects, next, nil
}

// PublicIssuesAfter uses the shared issue visibility predicate with a null
// subject, including issue security, archived work and project lifecycle.
func (s *Store) PublicIssuesAfter(ctx context.Context, workspaceID, projectID, after string) ([]*models.Issue, string, error) {
	rows, err := s.Pool.Query(ctx, issueJoin+`WHERE i.workspace_id=$1 AND i.project_id=$2 AND i.key>$3 AND `+VisibleIssuePredicate("i", "NULL")+` ORDER BY i.key LIMIT $4`, workspaceID, projectID, after, publicCollectionPageSize+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	issues := []*models.Issue{}
	for rows.Next() {
		issue, e := scanIssue(rows)
		if e != nil {
			return nil, "", e
		}
		issues = append(issues, issue)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(issues) > publicCollectionPageSize {
		issues = issues[:publicCollectionPageSize]
		next = issues[len(issues)-1].Key
	}
	return issues, next, nil
}
