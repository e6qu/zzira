package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// A release gate is what has to be true before a version ships. A project
// that sets none ships whenever a project administrator says so; a project
// that sets one is refused until it holds, and told which one it was.

// ReleaseGates are a project's release conditions.
type ReleaseGates struct {
	// RequireApprovals refuses a release while any approver has not approved.
	RequireApprovals bool
	// RequireResolved refuses a release while unresolved work still names the
	// version, unless that work is being moved to another one.
	RequireResolved bool
}

// ErrReleaseGate is a release the project's own conditions refuse.
var ErrReleaseGate = errors.New("release refused")

// ReleaseGatesFor reads a project's release conditions, or none.
func (s *Store) ReleaseGatesFor(ctx context.Context, projectID string) (ReleaseGates, error) {
	var gates ReleaseGates
	err := s.Pool.QueryRow(ctx, `SELECT require_approvals,require_resolved FROM project_release_gates WHERE project_id=$1`,
		projectID).Scan(&gates.RequireApprovals, &gates.RequireResolved)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseGates{}, nil
	}
	return gates, err
}

// SaveReleaseGates records what a project asks for before a version ships.
func (s *Store) SaveReleaseGates(ctx context.Context, workspaceID, actorID, projectID string, gates ReleaseGates) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdministrator(ctx, tx, workspaceID, actorID, projectID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO project_release_gates(project_id,require_approvals,require_resolved)
		VALUES($1,$2,$3) ON CONFLICT(project_id) DO UPDATE SET require_approvals=EXCLUDED.require_approvals,
		  require_resolved=EXCLUDED.require_resolved,updated_at=now()`, projectID, gates.RequireApprovals, gates.RequireResolved); err != nil {
		return err
	}
	if err := appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_release_gates", projectID, "upsert",
		map[string]any{"projectId": projectID, "requireApprovals": gates.RequireApprovals, "requireResolved": gates.RequireResolved}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// releaseRefusal is why a project's conditions refuse to ship this version, or
// empty when they do not. It reads inside the transaction that is releasing,
// so what it counts is what is being released.
func releaseRefusal(ctx context.Context, tx pgx.Tx, projectID, versionID, movingTo string) (string, error) {
	var gates ReleaseGates
	err := tx.QueryRow(ctx, `SELECT require_approvals,require_resolved FROM project_release_gates WHERE project_id=$1`,
		projectID).Scan(&gates.RequireApprovals, &gates.RequireResolved)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if gates.RequireApprovals {
		var waiting int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM project_version_approvers WHERE version_id=$1 AND status<>'APPROVED'`,
			versionID).Scan(&waiting); err != nil {
			return "", err
		}
		if waiting > 0 {
			return fmt.Sprintf("this project ships a version once every approver has approved it, and %d of them have not", waiting), nil
		}
	}
	// Work that is moving to another version is not left behind, so it does
	// not hold the release up.
	if gates.RequireResolved && movingTo == "" {
		match, err := json.Marshal([]map[string]string{{"id": versionID}})
		if err != nil {
			return "", err
		}
		var unresolved int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM issues
			WHERE project_id=$1 AND resolution_id IS NULL AND fields->'fixVersions' @> $2::jsonb`,
			projectID, match).Scan(&unresolved); err != nil {
			return "", err
		}
		if unresolved > 0 {
			return fmt.Sprintf("this project ships a version once its work is resolved, and %d work items in it are not; resolve them or move them to another version", unresolved), nil
		}
	}
	return "", nil
}

// ReleaseRefusalFor is what a project's conditions would say if this version
// were released now, or empty when they would let it ship. The release page
// reads it so a release manager sees the condition before the button refuses.
func (s *Store) ReleaseRefusalFor(ctx context.Context, projectID, versionID, movingTo string) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return releaseRefusal(ctx, tx, projectID, versionID, movingTo)
}
