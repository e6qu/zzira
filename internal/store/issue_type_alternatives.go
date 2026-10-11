package store

import (
	"context"
	"github.com/jackc/pgx/v5"
)

type issueTypeAlternativeQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// AlternativeIssueTypeIDs batches compatible replacement choices for a site.
// A replacement must be offered by every project using the source type and
// resolve to its same workflow, field configuration and screen scheme there.
func (s *Store) AlternativeIssueTypeIDs(ctx context.Context, workspaceID string, sourceIDs []string) (map[string][]string, error) {
	return alternativeIssueTypeIDs(ctx, s.Pool, workspaceID, sourceIDs)
}

func alternativeIssueTypeIDs(ctx context.Context, q issueTypeAlternativeQuerier, workspaceID string, sourceIDs []string) (map[string][]string, error) {
	result := map[string][]string{}
	if len(sourceIDs) == 0 {
		return result, nil
	}
	rows, err := q.Query(ctx, compatibleIssueTypeAlternativesQuery, workspaceID, sourceIDs, DefaultIssueTypeMapping)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var source, target string
		if err := rows.Scan(&source, &target); err != nil {
			return nil, err
		}
		result[source] = append(result[source], target)
	}
	return result, rows.Err()
}

const compatibleIssueTypeAlternativesQuery = `
WITH types AS (
 SELECT t.id,t.subtask,t.jira_id FROM issue_types t
 LEFT JOIN issue_metadata_overrides o ON o.workspace_id=$1 AND o.entity_type='issuetype' AND o.entity_id=t.id
 WHERE (t.workspace_id IS NULL OR t.workspace_id=$1) AND NOT COALESCE(o.deleted,false)
), candidates AS (
 SELECT source.id AS source_id,target.id AS target_id,target.jira_id
 FROM types source CROSS JOIN types target
 WHERE source.id=ANY($2::text[]) AND target.id<>source.id AND target.subtask=source.subtask
)
SELECT c.source_id,c.target_id FROM candidates c
WHERE NOT EXISTS (
 SELECT 1 FROM projects p
 LEFT JOIN project_issue_type_schemes assigned ON assigned.project_id=p.id AND assigned.workspace_id=$1
 LEFT JOIN workflow_schemes wf ON wf.id=p.workflow_scheme_id AND wf.workspace_id=$1
 WHERE p.workspace_id=$1
 AND (
  EXISTS(SELECT 1 FROM issue_type_scheme_items item WHERE item.scheme_id=assigned.scheme_id AND item.issue_type_id=c.source_id)
  OR EXISTS(SELECT 1 FROM issues i WHERE i.project_id=p.id AND i.workspace_id=$1 AND i.issuetype_id=c.source_id)
 )
 AND (
  (assigned.scheme_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM issue_type_scheme_items item WHERE item.scheme_id=assigned.scheme_id AND item.issue_type_id=c.target_id))
  OR COALESCE(wf.issue_type_mappings->>c.source_id,wf.default_workflow_id,p.workflow_id,'wf_default')
     IS DISTINCT FROM COALESCE(wf.issue_type_mappings->>c.target_id,wf.default_workflow_id,p.workflow_id,'wf_default')
  OR (
   SELECT item.configuration_id FROM project_field_configuration_schemes config
   JOIN field_configuration_scheme_items item ON item.scheme_id=config.scheme_id AND item.workspace_id=$1
   WHERE config.project_id=p.id AND config.workspace_id=$1 AND item.issue_type_id IN(c.source_id,$3)
   ORDER BY (item.issue_type_id=c.source_id) DESC LIMIT 1
  ) IS DISTINCT FROM (
   SELECT item.configuration_id FROM project_field_configuration_schemes config
   JOIN field_configuration_scheme_items item ON item.scheme_id=config.scheme_id AND item.workspace_id=$1
   WHERE config.project_id=p.id AND config.workspace_id=$1 AND item.issue_type_id IN(c.target_id,$3)
   ORDER BY (item.issue_type_id=c.target_id) DESC LIMIT 1
  )
  OR (
   SELECT item.screen_scheme_id FROM project_issue_type_screen_schemes screen
   JOIN issue_type_screen_scheme_items item ON item.scheme_id=screen.scheme_id AND item.workspace_id=$1
   WHERE screen.project_id=p.id AND screen.workspace_id=$1 AND item.issue_type_id IN(c.source_id,$3)
   ORDER BY (item.issue_type_id=c.source_id) DESC LIMIT 1
  ) IS DISTINCT FROM (
   SELECT item.screen_scheme_id FROM project_issue_type_screen_schemes screen
   JOIN issue_type_screen_scheme_items item ON item.scheme_id=screen.scheme_id AND item.workspace_id=$1
   WHERE screen.project_id=p.id AND screen.workspace_id=$1 AND item.issue_type_id IN(c.target_id,$3)
   ORDER BY (item.issue_type_id=c.target_id) DESC LIMIT 1
  )
 )
)
ORDER BY c.source_id,c.jira_id`
