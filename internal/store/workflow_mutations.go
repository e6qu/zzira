package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/workflow"
)

type WorkflowUpdateDefinition struct {
	Workflow        workflow.Workflow
	ExpectedVersion int
}

func addWorkflowAudit(ctx context.Context, tx pgx.Tx, workspaceID, actorID, action string, wf workflow.Workflow) error {
	detail, err := json.Marshal(map[string]any{"name": wf.Name, "version": wf.Version})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,$3,'workflow',$4,$5::jsonb FROM sites WHERE workspace_id=$1`,
		workspaceID, actorID, action, wf.ID, detail)
	return err
}

func (s *Store) CreateWorkflowBatch(ctx context.Context, workspaceID, actorID string, workflows []workflow.Workflow) ([]workflow.Workflow, error) {
	definitions := make([][]byte, len(workflows))
	for index, wf := range workflows {
		definition, err := s.validateWorkflow(ctx, workspaceID, wf)
		if err != nil {
			return nil, err
		}
		definitions[index] = definition
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('workflow:' || $1))`, workspaceID); err != nil {
		return nil, err
	}
	for index := range workflows {
		wf := &workflows[index]
		var duplicate bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflows WHERE id=$1 OR (workspace_id=$2 AND lower(name)=lower($3)))`, wf.ID, workspaceID, wf.Name).Scan(&duplicate); err != nil {
			return nil, err
		}
		if duplicate {
			return nil, fmt.Errorf("%w: a workflow already uses that name or id", ErrAdminConflict)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO workflows(id,name,def,workspace_id) VALUES($1,$2,$3,$4)`, wf.ID, wf.Name, definitions[index], workspaceID); err != nil {
			if isUniqueViolation(err) {
				return nil, fmt.Errorf("%w: a workflow already uses that id", ErrAdminConflict)
			}
			return nil, err
		}
		wf.Version = 1
		if err := addWorkflowAudit(ctx, tx, workspaceID, actorID, "workflow.created", *wf); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return workflows, nil
}

func workflowDefinitionStatuses(wf workflow.Workflow) []string {
	seen := make(map[string]bool)
	for _, transition := range wf.Transitions {
		seen[transition.To] = true
		for _, from := range transition.From {
			seen[from] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return ids
}

func (s *Store) UpdateWorkflowBatch(ctx context.Context, workspaceID, actorID string, updates []WorkflowUpdateDefinition) ([]workflow.Workflow, error) {
	definitions := make([][]byte, len(updates))
	for index, update := range updates {
		definition, err := s.validateWorkflow(ctx, workspaceID, update.Workflow)
		if err != nil {
			return nil, err
		}
		definitions[index] = definition
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for index, update := range updates {
		if update.Workflow.ID == workflow.Default().ID {
			return nil, fmt.Errorf("%w: the system workflow is read-only", ErrAdminValidation)
		}
		var currentVersion int
		if err := tx.QueryRow(ctx, `SELECT version FROM workflows WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, update.Workflow.ID, workspaceID).Scan(&currentVersion); err != nil {
			if err == pgx.ErrNoRows {
				return nil, ErrAdminNotFound
			}
			return nil, err
		}
		if currentVersion != update.ExpectedVersion {
			return nil, fmt.Errorf("%w: workflow version is stale", ErrAdminConflict)
		}
		statuses := workflowDefinitionStatuses(update.Workflow)
		var incompatible bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM issues i
				JOIN projects p ON p.id=i.project_id AND p.workspace_id=$1
				LEFT JOIN workflow_schemes ws ON ws.id=p.workflow_scheme_id AND ws.workspace_id=p.workspace_id
				WHERE COALESCE(ws.issue_type_mappings->>i.issuetype_id,ws.default_workflow_id,p.workflow_id,'wf_default')=$2
				AND NOT (i.status_id=ANY($3::text[]))
			)`, workspaceID, update.Workflow.ID, statuses).Scan(&incompatible); err != nil {
			return nil, err
		}
		if incompatible {
			return nil, fmt.Errorf("%w: status mappings are required for active issues", ErrAdminConflict)
		}
		updated := update.Workflow
		updated.Version = currentVersion + 1
		if _, err := tx.Exec(ctx, `UPDATE workflows SET def=$3,version=$4,published_at=now() WHERE id=$1 AND workspace_id=$2`, updated.ID, workspaceID, definitions[index], updated.Version); err != nil {
			return nil, err
		}
		updates[index].Workflow = updated
		if err := addWorkflowAudit(ctx, tx, workspaceID, actorID, "workflow.updated", updated); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out := make([]workflow.Workflow, len(updates))
	for index := range updates {
		out[index] = updates[index].Workflow
	}
	return out, nil
}
