package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

// ErrBoardAdminValidation reports an administrator a board cannot take.
var ErrBoardAdminValidation = errors.New("invalid board administrator")

// BoardAdminInput names one administrator to add. Jira Software administers a
// board by user and by group, so those are the two holders accepted here.
type BoardAdminInput struct {
	Type      string
	AccountID string
	GroupID   string
}

// BoardAdmins lists a board's administrators, users before groups and each in
// display order, so the board settings page and the REST expansion agree.
func (s *Store) BoardAdmins(ctx context.Context, boardID string) ([]models.BoardAdmin, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT a.id, a.admin_type, COALESCE(a.account_id,''), COALESCE(u.display_name,''),
		       COALESCE(a.group_id::text,''), COALESCE(g.name,'')
		FROM board_admins a
		LEFT JOIN users u ON u.id = a.account_id
		LEFT JOIN groups g ON g.id = a.group_id
		WHERE a.board_id=$1
		ORDER BY a.admin_type, lower(COALESCE(u.display_name, g.name, '')), a.id`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	admins := []models.BoardAdmin{}
	for rows.Next() {
		var admin models.BoardAdmin
		if err := rows.Scan(&admin.ID, &admin.Type, &admin.AccountID, &admin.UserName, &admin.GroupID, &admin.GroupName); err != nil {
			return nil, err
		}
		admins = append(admins, admin)
	}
	return admins, rows.Err()
}

// CanAdministerBoard reports whether a user may configure a board. Jira lets a
// board's own administrators configure it, and a project's administrators keep
// administering the project's boards regardless.
func (s *Store) CanAdministerBoard(ctx context.Context, workspaceID, userID string, board *models.Board) (bool, error) {
	admin, err := s.IsAdmin(ctx, workspaceID, userID)
	if err != nil {
		return false, err
	}
	if admin {
		return true, nil
	}
	allowed, err := s.HasProjectPermission(ctx, workspaceID, userID, board.ProjectID, "", "ADMINISTER_PROJECTS")
	if err != nil {
		return false, err
	}
	if allowed {
		return true, nil
	}
	var named bool
	err = s.Pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM board_admins a
			LEFT JOIN group_members m ON m.group_id = a.group_id
			WHERE a.board_id=$1
			  AND ((a.admin_type='user' AND a.account_id=$2)
			    OR (a.admin_type='group' AND m.user_id=$2)))`, board.ID, userID).Scan(&named)
	return named, err
}

// AddBoardAdmin gives a user or a group administration rights over a board.
func (s *Store) AddBoardAdmin(ctx context.Context, actorID, workspaceID, boardID string, input BoardAdminInput) (*models.Action, error) {
	kind := strings.TrimSpace(input.Type)
	accountID, groupID := strings.TrimSpace(input.AccountID), strings.TrimSpace(input.GroupID)
	switch kind {
	case "user":
		if accountID == "" {
			return nil, fmt.Errorf("%w: choose a person to administer the board", ErrBoardAdminValidation)
		}
		groupID = ""
	case "group":
		if groupID == "" {
			return nil, fmt.Errorf("%w: choose a group to administer the board", ErrBoardAdminValidation)
		}
		accountID = ""
	default:
		return nil, fmt.Errorf("%w: a board administrator is a person or a group", ErrBoardAdminValidation)
	}
	board, err := s.BoardByIDInWorkspace(ctx, workspaceID, boardID)
	if err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO board_admins(board_id, admin_type, account_id, group_id)
		VALUES ($1,$2,NULLIF($3,''),NULLIF($4,'')::uuid)
		ON CONFLICT DO NOTHING`, boardID, kind, accountID, groupID); err != nil {
		return nil, fmt.Errorf("%w: that person or group is not available", ErrBoardAdminValidation)
	}
	action, err := appendBoardAction(ctx, tx, workspaceID, actorID, board)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return action, nil
}

// DeleteBoardAdmin takes administration rights over a board away again.
func (s *Store) DeleteBoardAdmin(ctx context.Context, actorID, workspaceID, boardID string, adminID int64) (*models.Action, error) {
	board, err := s.BoardByIDInWorkspace(ctx, workspaceID, boardID)
	if err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `DELETE FROM board_admins WHERE id=$1 AND board_id=$2`, adminID, boardID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, fmt.Errorf("%w: that administrator is no longer on the board", ErrBoardAdminValidation)
	}
	action, err := appendBoardAction(ctx, tx, workspaceID, actorID, board)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return action, nil
}

// appendBoardAction records a board change under the board's own entity, the
// way the rest of the board configuration does.
func appendBoardAction(ctx context.Context, tx pgx.Tx, workspaceID, actorID string, board *models.Board) (*models.Action, error) {
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(models.BoardUpsertPayload{Board: *board})
	if err != nil {
		return nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityBoard, EntityID: board.ID,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, err
	}
	return action, nil
}
