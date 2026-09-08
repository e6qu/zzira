package store

import "context"

func (s *Store) WorkflowProjectUsages(ctx context.Context, workspaceID, workflowID string) ([]string, error) {
	if _, err := s.WorkflowByID(ctx, workspaceID, workflowID); err != nil {
		return nil, ErrAdminNotFound
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT p.id
		FROM projects p
		LEFT JOIN workflow_schemes ws ON ws.id=p.workflow_scheme_id AND ws.workspace_id=p.workspace_id
		WHERE p.workspace_id=$1 AND (
			COALESCE(ws.default_workflow_id,p.workflow_id,'wf_default')=$2 OR
			EXISTS (SELECT 1 FROM jsonb_each_text(COALESCE(ws.issue_type_mappings,'{}'::jsonb)) mapping WHERE mapping.value=$2)
		)
		ORDER BY p.id`, workspaceID, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) WorkflowSchemeUsages(ctx context.Context, workspaceID, workflowID string) ([]string, error) {
	if _, err := s.WorkflowByID(ctx, workspaceID, workflowID); err != nil {
		return nil, ErrAdminNotFound
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id FROM workflow_schemes
		WHERE workspace_id=$1 AND (default_workflow_id=$2 OR
			EXISTS (SELECT 1 FROM jsonb_each_text(issue_type_mappings) mapping WHERE mapping.value=$2))
		ORDER BY id`, workspaceID, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) WorkflowProjectIssueTypeUsages(ctx context.Context, workspaceID, workflowID, projectID string) ([]string, error) {
	if _, err := s.WorkflowByID(ctx, workspaceID, workflowID); err != nil {
		return nil, ErrAdminNotFound
	}
	if _, err := s.ProjectByIDOrKey(ctx, workspaceID, projectID); err != nil {
		return nil, ErrAdminNotFound
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT it.id
		FROM issue_types it
		JOIN projects p ON p.id=$2 AND p.workspace_id=$1
		LEFT JOIN workflow_schemes ws ON ws.id=p.workflow_scheme_id AND ws.workspace_id=p.workspace_id
		WHERE COALESCE(ws.issue_type_mappings->>it.id,ws.default_workflow_id,p.workflow_id,'wf_default')=$3
		ORDER BY it.id`, workspaceID, projectID, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
