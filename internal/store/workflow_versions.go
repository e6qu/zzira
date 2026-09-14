package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/workflow"
)

// workflowHistoryRetention is how long Jira keeps workflow versions.
const workflowHistoryRetention = "60 days"

// WorkflowHistoryEntry is one stored version of a workflow.
type WorkflowHistoryEntry struct {
	WorkflowID   string
	Version      int
	WrittenAt    time.Time
	Intermediate bool
}

// WorkflowHistory lists a workflow's retained versions, newest first.
func (s *Store) WorkflowHistory(ctx context.Context, workspaceID, workflowRef string, includeIntermediate bool) ([]WorkflowHistoryEntry, error) {
	var entityID string
	if err := s.Pool.QueryRow(ctx, `SELECT entity_id::text FROM workflows WHERE (id=$1 OR entity_id::text=$1) AND (workspace_id=$2 OR (id='wf_default' AND workspace_id IS NULL))`,
		workflowRef, workspaceID).Scan(&entityID); err != nil {
		return nil, ErrAdminNotFound
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT v.version, v.written_at, v.intermediate FROM workflow_versions v JOIN workflows w ON w.id=v.workflow_id
		WHERE w.entity_id::text=$1 AND v.written_at > now()-interval '`+workflowHistoryRetention+`' AND ($2 OR NOT v.intermediate)
		ORDER BY v.version DESC`, entityID, includeIntermediate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []WorkflowHistoryEntry{}
	for rows.Next() {
		entry := WorkflowHistoryEntry{WorkflowID: entityID}
		if err := rows.Scan(&entry.Version, &entry.WrittenAt, &entry.Intermediate); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// WorkflowHistoryVersion is one retained version of a workflow.
type WorkflowHistoryVersion struct {
	Workflow  workflow.Workflow
	WrittenAt time.Time
	AuthorID  string
}

func (s *Store) WorkflowHistoryVersion(ctx context.Context, workspaceID, workflowRef string, version int) (WorkflowHistoryVersion, error) {
	var result WorkflowHistoryVersion
	var def []byte
	var workflowID, projectID, entityID string
	err := s.Pool.QueryRow(ctx, `
		SELECT v.def, v.written_at, COALESCE(v.author_id,''), w.id, COALESCE(w.project_id,''), w.entity_id::text
		FROM workflow_versions v JOIN workflows w ON w.id=v.workflow_id
		WHERE (w.id=$1 OR w.entity_id::text=$1) AND (w.workspace_id=$2 OR (w.id='wf_default' AND w.workspace_id IS NULL))
		  AND v.version=$3 AND v.written_at > now()-interval '`+workflowHistoryRetention+`'`,
		workflowRef, workspaceID, version).Scan(&def, &result.WrittenAt, &result.AuthorID, &workflowID, &projectID, &entityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrAdminNotFound
	}
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(def, &result.Workflow); err != nil {
		return result, err
	}
	result.Workflow.ID, result.Workflow.ProjectID, result.Workflow.EntityID, result.Workflow.Version = workflowID, projectID, entityID, version
	return result, nil
}

// SaveWorkflowRules stores a workflow whose app rules changed. A published
// workflow gets a new version; a draft is changed in place.
func (s *Store) SaveWorkflowRules(ctx context.Context, workspaceID, actorID string, wf workflow.Workflow, draft bool) error {
	if wf.ID == workflow.Default().ID {
		return fmt.Errorf("%w: the system workflow is read-only", ErrAdminValidation)
	}
	def, err := s.validateWorkflow(ctx, workspaceID, wf)
	if err != nil {
		return err
	}
	if draft {
		result, err := s.Pool.Exec(ctx, `UPDATE workflows SET draft_def=$3,draft_updated_at=now() WHERE id=$1 AND workspace_id=$2 AND draft_def IS NOT NULL`, wf.ID, workspaceID, def)
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			return ErrAdminNotFound
		}
		return nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var version int
	if err := tx.QueryRow(ctx, `UPDATE workflows SET def=$3,version=version+1,published_at=now() WHERE id=$1 AND workspace_id=$2 RETURNING version`, wf.ID, workspaceID, def).Scan(&version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAdminNotFound
		}
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO workflow_versions(workflow_id,version,def,author_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, wf.ID, version, def, actorID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
