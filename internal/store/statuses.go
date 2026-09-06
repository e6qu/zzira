package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

type StatusUsage struct {
	Status      models.Status
	Issues      int
	Boards      int
	Workflows   int
	Automations int
}

func (u StatusUsage) Total() int { return u.Issues + u.Boards + u.Workflows + u.Automations }

func validateStatus(status models.Status) (models.Status, error) {
	status.Name = strings.TrimSpace(status.Name)
	status.Description = strings.TrimSpace(status.Description)
	if status.Name == "" || len(status.Name) > 255 {
		return status, fmt.Errorf("%w: status name is required (max 255 characters)", ErrAdminValidation)
	}
	if len(status.Description) > 1000 {
		return status, fmt.Errorf("%w: status description must be at most 1000 characters", ErrAdminValidation)
	}
	switch status.Category {
	case "new", "indeterminate", "done":
	default:
		return status, fmt.Errorf("%w: status category must be To do, In progress, or Done", ErrAdminValidation)
	}
	return status, nil
}

func statusNameAvailable(ctx context.Context, tx pgx.Tx, workspaceID, name, exceptID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('status:' || $1))`, workspaceID); err != nil {
		return err
	}
	var duplicate bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM statuses
		 WHERE (workspace_id IS NULL OR workspace_id=$1) AND lower(name)=lower($2) AND id<>$3)`,
		workspaceID, name, exceptID).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate {
		return fmt.Errorf("%w: a visible status already uses that name", ErrAdminConflict)
	}
	return nil
}

func addStatusAudit(ctx context.Context, tx pgx.Tx, workspaceID, actorID, action, statusID string, detail map[string]any) error {
	encoded, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,$3,'status',$4,$5::jsonb FROM sites WHERE workspace_id=$1`,
		workspaceID, actorID, action, statusID, encoded)
	return err
}

func (s *Store) CreateStatus(ctx context.Context, workspaceID, actorID string, status models.Status) (models.Status, error) {
	status, err := validateStatus(status)
	if err != nil {
		return status, err
	}
	if status.ID == "" {
		status.ID = NewID("status")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return status, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := statusNameAvailable(ctx, tx, workspaceID, status.Name, ""); err != nil {
		return status, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO statuses(id,name,description,category,workspace_id) VALUES($1,$2,$3,$4,$5)`, status.ID, status.Name, status.Description, status.Category, workspaceID); err != nil {
		if isUniqueViolation(err) {
			return status, fmt.Errorf("%w: a status already uses that name", ErrAdminConflict)
		}
		return status, err
	}
	if err := addStatusAudit(ctx, tx, workspaceID, actorID, "status.created", status.ID, map[string]any{"name": status.Name, "category": status.Category}); err != nil {
		return status, err
	}
	status.Protected = false
	return status, tx.Commit(ctx)
}

func (s *Store) UpdateStatus(ctx context.Context, workspaceID, actorID string, status models.Status) error {
	status, err := validateStatus(status)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := statusNameAvailable(ctx, tx, workspaceID, status.Name, status.ID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE statuses SET name=$3,description=$4,category=$5,updated_at=now() WHERE id=$1 AND workspace_id=$2`, status.ID, workspaceID, status.Name, status.Description, status.Category)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: a status already uses that name", ErrAdminConflict)
		}
		return err
	}
	if tag.RowsAffected() != 1 {
		return statusMutationNotFound(ctx, tx, status.ID)
	}
	if err := addStatusAudit(ctx, tx, workspaceID, actorID, "status.updated", status.ID, map[string]any{"name": status.Name, "category": status.Category}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func statusMutationNotFound(ctx context.Context, tx pgx.Tx, statusID string) error {
	var owner sql.NullString
	if err := tx.QueryRow(ctx, `SELECT workspace_id FROM statuses WHERE id=$1`, statusID).Scan(&owner); err != nil {
		if err == pgx.ErrNoRows {
			return ErrAdminNotFound
		}
		return err
	}
	if !owner.Valid {
		return fmt.Errorf("%w: built-in statuses are protected", ErrAdminConflict)
	}
	return ErrAdminNotFound
}

func statusUsage(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, workspaceID, statusID string) (StatusUsage, error) {
	var usage StatusUsage
	err := q.QueryRow(ctx, `
		SELECT st.id,st.name,st.description,st.category,st.workspace_id IS NULL,
		 (SELECT count(*) FROM issues i WHERE i.workspace_id=$1 AND i.status_id=st.id),
		 (SELECT count(*) FROM boards b JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1 AND st.id=ANY(b.column_status_ids)),
		 (SELECT count(*) FROM workflows w WHERE
		   EXISTS (SELECT 1 FROM jsonb_array_elements(w.def->'transitions') t WHERE t->>'to'=st.id OR (t->'from') ? st.id)
		   OR EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE(w.draft_def,w.def)->'transitions') t WHERE t->>'to'=st.id OR (t->'from') ? st.id)),
		 (SELECT count(*) FROM automation_rules a WHERE a.workspace_id=$1 AND
		   jsonb_path_exists(a.payload, '$.**.statusId ? (@ == $status)', jsonb_build_object('status',to_jsonb(st.id))))
		FROM statuses st WHERE st.id=$2 AND (st.workspace_id IS NULL OR st.workspace_id=$1)`, workspaceID, statusID).
		Scan(&usage.Status.ID, &usage.Status.Name, &usage.Status.Description, &usage.Status.Category, &usage.Status.Protected,
			&usage.Issues, &usage.Boards, &usage.Workflows, &usage.Automations)
	return usage, err
}

func (s *Store) StatusUsage(ctx context.Context, workspaceID, statusID string) (StatusUsage, error) {
	return statusUsage(ctx, s.Pool, workspaceID, statusID)
}

func (s *Store) StatusDirectory(ctx context.Context, workspaceID string) ([]StatusUsage, error) {
	statuses, err := s.StatusesForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]StatusUsage, 0, len(statuses))
	for _, status := range statuses {
		usage, err := s.StatusUsage(ctx, workspaceID, status.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, usage)
	}
	return out, nil
}

func (s *Store) DeleteStatus(ctx context.Context, workspaceID, actorID, statusID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	usage, err := statusUsage(ctx, tx, workspaceID, statusID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrAdminNotFound
		}
		return err
	}
	if usage.Status.Protected {
		return fmt.Errorf("%w: built-in statuses are protected", ErrAdminConflict)
	}
	if usage.Total() != 0 {
		return fmt.Errorf("%w: status is still used by issues, boards, workflows, or automation", ErrAdminConflict)
	}
	tag, err := tx.Exec(ctx, `DELETE FROM statuses WHERE id=$1 AND workspace_id=$2`, statusID, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrAdminNotFound
	}
	if err := addStatusAudit(ctx, tx, workspaceID, actorID, "status.deleted", statusID, map[string]any{"name": usage.Status.Name}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
