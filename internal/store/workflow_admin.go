package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/workflow"
)

// DeleteInactiveWorkflow deletes a workspace workflow only when no project,
// published scheme, or draft scheme can resolve to it.
func (s *Store) DeleteInactiveWorkflow(ctx context.Context, workspaceID, actorID, workflowID string) error {
	if workflowID == workflow.Default().ID {
		return fmt.Errorf("%w: a system workflow cannot be deleted", ErrAdminValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM workflows WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, workflowID, workspaceID).Scan(&name); err != nil {
		if err == pgx.ErrNoRows {
			return ErrAdminNotFound
		}
		return err
	}
	var referenced bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM projects WHERE workspace_id=$1 AND workflow_id=$2
			UNION ALL
			SELECT 1 FROM workflow_schemes
			WHERE workspace_id=$1 AND (
				default_workflow_id=$2 OR
				EXISTS (SELECT 1 FROM jsonb_each_text(issue_type_mappings) mapping WHERE mapping.value=$2) OR
				draft_def->>'defaultWorkflowId'=$2 OR
				EXISTS (SELECT 1 FROM jsonb_each_text(COALESCE(draft_def->'issueTypeMappings','{}'::jsonb)) mapping WHERE mapping.value=$2)
			)
		)`, workspaceID, workflowID).Scan(&referenced); err != nil {
		return err
	}
	if referenced {
		return fmt.Errorf("%w: an active or associated workflow cannot be deleted", ErrAdminValidation)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM workflows WHERE id=$1 AND workspace_id=$2`, workflowID, workspaceID); err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]any{"name": name})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,'workflow.deleted','workflow',$3,$4::jsonb FROM sites WHERE workspace_id=$1`,
		workspaceID, actorID, workflowID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
