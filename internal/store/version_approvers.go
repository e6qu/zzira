package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

// requireActiveSiteMember refuses an account that is not an active member of
// the workspace.
func requireActiveSiteMember(ctx context.Context, tx pgx.Tx, workspaceID, accountID, role string) error {
	var member bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.workspace_id=$1 AND m.user_id=$2 AND u.active)`, workspaceID, accountID).Scan(&member); err != nil {
		return err
	}
	if !member {
		return fmt.Errorf("%w: %s must be an active member of the site", ErrVersionValidation, role)
	}
	return nil
}

// VersionApprovers lists a release's approvers in the order they were added.
func (s *Store) VersionApprovers(ctx context.Context, versionID string) ([]models.VersionApprover, error) {
	rows, err := s.Pool.Query(ctx, `SELECT account_id,status,description,decline_reason FROM project_version_approvers WHERE version_id=$1 ORDER BY added_at,account_id`, versionID)
	if err != nil {
		return nil, err
	}
	approvers, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (models.VersionApprover, error) {
		var approver models.VersionApprover
		err := row.Scan(&approver.AccountID, &approver.Status, &approver.Description, &approver.DeclineReason)
		return approver, err
	})
	if approvers == nil {
		approvers = []models.VersionApprover{}
	}
	return approvers, err
}

func lockedWorkspaceVersion(ctx context.Context, tx pgx.Tx, ws, versionID string) (*models.Version, error) {
	v, err := scanVersion(tx.QueryRow(ctx, versionSelect+` JOIN projects p ON p.id=v.project_id WHERE v.id=$1 AND p.workspace_id=$2 AND p.lifecycle_state='ACTIVE'`, versionID, ws))
	if err != nil {
		return nil, err
	}
	return v, lockVersionProject(ctx, tx, ws, v.ProjectID)
}

// AddVersionApprover asks a site member to approve a release. Asking again
// replaces the request's description and resets the decision.
func (s *Store) AddVersionApprover(ctx context.Context, ws, actor, versionID, accountID, description string) error {
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(description) > 1000 {
		return fmt.Errorf("%w: an approval description must be at most 1000 characters", ErrVersionValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	v, err := lockedWorkspaceVersion(ctx, tx, ws, versionID)
	if err != nil {
		return err
	}
	if err = projectAdministrator(ctx, tx, ws, actor, v.ProjectID); err != nil {
		return err
	}
	if err = requireActiveSiteMember(ctx, tx, ws, accountID, "an approver"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO project_version_approvers(version_id,account_id,description) VALUES($1,$2,$3)
		ON CONFLICT(version_id,account_id) DO UPDATE SET description=EXCLUDED.description,status='PENDING',decline_reason=''`, v.ID, accountID, description); err != nil {
		return err
	}
	if err = versionAction(ctx, tx, ws, actor, v, models.OpUpsert); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemoveVersionApprover withdraws an approval request.
func (s *Store) RemoveVersionApprover(ctx context.Context, ws, actor, versionID, accountID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	v, err := lockedWorkspaceVersion(ctx, tx, ws, versionID)
	if err != nil {
		return err
	}
	if err = projectAdministrator(ctx, tx, ws, actor, v.ProjectID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `DELETE FROM project_version_approvers WHERE version_id=$1 AND account_id=$2`, v.ID, accountID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if err = versionAction(ctx, tx, ws, actor, v, models.OpUpsert); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DecideVersionApproval records the actor's own decision on a release they
// were asked to approve. Declining may give a reason.
func (s *Store) DecideVersionApproval(ctx context.Context, ws, actor, versionID string, approve bool, reason string) error {
	reason = strings.TrimSpace(reason)
	if approve {
		reason = ""
	}
	if utf8.RuneCountInString(reason) > 1000 {
		return fmt.Errorf("%w: a decline reason must be at most 1000 characters", ErrVersionValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	v, err := lockedWorkspaceVersion(ctx, tx, ws, versionID)
	if err != nil {
		return err
	}
	status := "DECLINED"
	if approve {
		status = "APPROVED"
	}
	command, err := tx.Exec(ctx, `UPDATE project_version_approvers SET status=$3,decline_reason=$4 WHERE version_id=$1 AND account_id=$2`, v.ID, actor, status, reason)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return errors.Join(ErrVersionValidation, fmt.Errorf("only an approver of this release can decide on it"))
	}
	if err = versionAction(ctx, tx, ws, actor, v, models.OpUpsert); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
