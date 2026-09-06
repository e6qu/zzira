package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

type APITask struct {
	ID           string
	WorkspaceID  string
	SubmittedBy  string
	Status       string
	Progress     int
	Message      string
	Result       json.RawMessage
	SubmittedAt  time.Time
	StartedAt    *time.Time
	LastUpdateAt time.Time
	FinishedAt   *time.Time
}

func completedAPITask(workspaceID, actorID, message string, result any) (APITask, error) {
	now := time.Now().UTC()
	encoded, err := json.Marshal(result)
	if err != nil {
		return APITask{}, err
	}
	return APITask{
		ID: NewID("task"), WorkspaceID: workspaceID, SubmittedBy: actorID,
		Status: "COMPLETE", Progress: 100, Message: message, Result: encoded,
		SubmittedAt: now, StartedAt: &now, LastUpdateAt: now, FinishedAt: &now,
	}, nil
}

func insertAPITask(ctx context.Context, tx pgx.Tx, task APITask) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO api_tasks(id,workspace_id,submitted_by,status,progress,message,result,submitted_at,started_at,last_update_at,finished_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		task.ID, task.WorkspaceID, task.SubmittedBy, task.Status, task.Progress, task.Message,
		task.Result, task.SubmittedAt, task.StartedAt, task.LastUpdateAt, task.FinishedAt)
	return err
}

func (s *Store) APITaskByID(ctx context.Context, workspaceID, taskID string) (APITask, error) {
	var task APITask
	err := s.Pool.QueryRow(ctx, `
		SELECT id,workspace_id,submitted_by,status,progress,message,COALESCE(result,'null'::jsonb),
		       submitted_at,started_at,last_update_at,finished_at
		FROM api_tasks WHERE id=$1 AND workspace_id=$2`, taskID, workspaceID).Scan(
		&task.ID, &task.WorkspaceID, &task.SubmittedBy, &task.Status, &task.Progress, &task.Message,
		&task.Result, &task.SubmittedAt, &task.StartedAt, &task.LastUpdateAt, &task.FinishedAt)
	return task, err
}
