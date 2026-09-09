package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/workflow"
)

const (
	apiTaskUpdateWorkflowScheme  = "workflow-scheme-update"
	apiTaskSwitchWorkflowScheme  = "workflow-scheme-switch"
	apiTaskPublishWorkflowScheme = "workflow-scheme-publish"
	apiTaskBulkWatch             = "bulk-issue-watch"
	apiTaskBulkUnwatch           = "bulk-issue-unwatch"
)

var (
	ErrAPITaskNotCancellable = errors.New("api task is not cancellable")
	ErrBulkTaskLimit         = errors.New("five bulk operations are already queued or running")
	errAPITaskCancelled      = errors.New("api task was cancelled")
)

type APITask struct {
	ID           string
	WorkspaceID  string
	SubmittedBy  string
	Description  string
	Kind         string
	Payload      json.RawMessage
	Status       string
	Progress     int
	Message      string
	Result       json.RawMessage
	SubmittedAt  time.Time
	StartedAt    *time.Time
	LastUpdateAt time.Time
	FinishedAt   *time.Time
}

func (task APITask) IsBulkIssueOperation() bool {
	return task.Kind == apiTaskBulkWatch || task.Kind == apiTaskBulkUnwatch
}

type updateWorkflowSchemeTaskPayload struct {
	Scheme          workflow.Scheme         `json:"scheme"`
	StatusMappings  []WorkflowStatusMapping `json:"statusMappings,omitempty"`
	ExpectedVersion int                     `json:"expectedVersion"`
}

type switchWorkflowSchemeTaskPayload struct {
	ProjectID      string                  `json:"projectId"`
	SchemeID       string                  `json:"workflowSchemeId"`
	StatusMappings []WorkflowStatusMapping `json:"statusMappings,omitempty"`
}

type publishWorkflowSchemeTaskPayload struct {
	SchemeID       string                  `json:"workflowSchemeId"`
	StatusMappings []WorkflowStatusMapping `json:"statusMappings,omitempty"`
}

type BulkIssueTaskItem struct {
	ID     string `json:"id"`
	JiraID int64  `json:"jiraId"`
}

type bulkWatchTaskPayload struct {
	Issues []BulkIssueTaskItem `json:"issues"`
	Watch  bool                `json:"watch"`
}

func (s *Store) EnqueueBulkWatchTask(ctx context.Context, workspaceID, actorID string, issues []BulkIssueTaskItem, watch bool) (APITask, error) {
	description, kind := "Bulk watch issues", apiTaskBulkWatch
	if !watch {
		description, kind = "Bulk unwatch issues", apiTaskBulkUnwatch
	}
	task, err := queuedAPITask(workspaceID, actorID, description, kind, bulkWatchTaskPayload{Issues: issues, Watch: watch})
	if err != nil {
		return APITask{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return APITask{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "bulk-issue:"+workspaceID); err != nil {
		return APITask{}, err
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM api_tasks WHERE workspace_id=$1 AND kind LIKE 'bulk-issue-%' AND status IN ('ENQUEUED','RUNNING')`, workspaceID).Scan(&active); err != nil {
		return APITask{}, err
	}
	if active >= 5 {
		return APITask{}, ErrBulkTaskLimit
	}
	if err := insertAPITask(ctx, tx, task); err != nil {
		return APITask{}, err
	}
	return task, tx.Commit(ctx)
}

func queuedAPITask(workspaceID, actorID, description, kind string, payload any) (APITask, error) {
	now := time.Now().UTC()
	encoded, err := json.Marshal(payload)
	if err != nil {
		return APITask{}, err
	}
	return APITask{
		ID: NewID("task"), WorkspaceID: workspaceID, SubmittedBy: actorID,
		Description: description, Kind: kind, Payload: encoded,
		Status: "ENQUEUED", Progress: 0, Message: "Task is queued.",
		SubmittedAt: now, LastUpdateAt: now,
	}, nil
}

func insertAPITask(ctx context.Context, tx pgx.Tx, task APITask) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO api_tasks(id,workspace_id,submitted_by,description,kind,payload,status,progress,message,result,submitted_at,started_at,last_update_at,finished_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		task.ID, task.WorkspaceID, task.SubmittedBy, task.Description, task.Kind, task.Payload,
		task.Status, task.Progress, task.Message, task.Result, task.SubmittedAt, task.StartedAt,
		task.LastUpdateAt, task.FinishedAt)
	return err
}

func (s *Store) enqueueAPITask(ctx context.Context, task APITask) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertAPITask(ctx, tx, task); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanAPITask(row pgx.Row) (APITask, error) {
	var task APITask
	err := row.Scan(
		&task.ID, &task.WorkspaceID, &task.SubmittedBy, &task.Description, &task.Kind,
		&task.Payload, &task.Status, &task.Progress, &task.Message, &task.Result,
		&task.SubmittedAt, &task.StartedAt, &task.LastUpdateAt, &task.FinishedAt,
	)
	return task, err
}

const apiTaskColumns = `id,workspace_id,submitted_by,description,kind,COALESCE(payload,'null'::jsonb),status,progress,message,COALESCE(result,'null'::jsonb),submitted_at,started_at,last_update_at,finished_at`

func (s *Store) APITaskByID(ctx context.Context, workspaceID, taskID string) (APITask, error) {
	return scanAPITask(s.Pool.QueryRow(ctx, `SELECT `+apiTaskColumns+` FROM api_tasks WHERE id=$1 AND workspace_id=$2`, taskID, workspaceID))
}

func (s *Store) CancelAPITask(ctx context.Context, workspaceID, taskID string) (APITask, error) {
	task, err := scanAPITask(s.Pool.QueryRow(ctx, `
		UPDATE api_tasks SET status='CANCELLED',message='Task was cancelled.',last_update_at=now(),finished_at=now()
		WHERE id=$1 AND workspace_id=$2 AND status IN ('ENQUEUED','RUNNING')
		RETURNING `+apiTaskColumns, taskID, workspaceID))
	if !errors.Is(err, pgx.ErrNoRows) {
		return task, err
	}
	if _, lookupErr := s.APITaskByID(ctx, workspaceID, taskID); lookupErr != nil {
		return APITask{}, lookupErr
	}
	return APITask{}, ErrAPITaskNotCancellable
}

// APITaskRunner drains durable Jira asynchronous tasks. Claims use SKIP LOCKED
// so multiple server replicas can run workers without executing a task twice.
type APITaskRunner struct {
	Store        *Store
	Logf         func(string, ...any)
	PollInterval time.Duration
}

func (r *APITaskRunner) Run(ctx context.Context, workspaceID string) {
	interval := r.PollInterval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := r.DrainOnce(ctx, workspaceID); err != nil && !errors.Is(err, context.Canceled) {
			logger := r.Logf
			if logger == nil {
				logger = log.Printf
			}
			logger("api task runner: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *APITaskRunner) DrainOnce(ctx context.Context, workspaceID string) error {
	if r.Store == nil {
		return errors.New("api task runner is not configured")
	}
	task, err := r.claim(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := r.execute(ctx, task); err != nil {
		if errors.Is(err, errAPITaskCancelled) {
			return nil
		}
		return r.fail(ctx, task, err)
	}
	return nil
}

func (r *APITaskRunner) claim(ctx context.Context, workspaceID string) (APITask, error) {
	return scanAPITask(r.Store.Pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT id FROM api_tasks
			WHERE workspace_id=$1 AND kind<>'' AND
			      (status='ENQUEUED' OR (status='RUNNING' AND last_update_at<now()-interval '2 minutes'))
			ORDER BY submitted_at,id FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE api_tasks task SET status='RUNNING',progress=5,message='Task is running.',
		       started_at=COALESCE(started_at,now()),last_update_at=now(),finished_at=NULL
		FROM candidate WHERE task.id=candidate.id
		RETURNING `+prefixedAPITaskColumns("task"), workspaceID))
}

func prefixedAPITaskColumns(alias string) string {
	return alias + `.id,` + alias + `.workspace_id,` + alias + `.submitted_by,` + alias + `.description,` + alias + `.kind,COALESCE(` + alias + `.payload,'null'::jsonb),` + alias + `.status,` + alias + `.progress,` + alias + `.message,COALESCE(` + alias + `.result,'null'::jsonb),` + alias + `.submitted_at,` + alias + `.started_at,` + alias + `.last_update_at,` + alias + `.finished_at`
}

func (r *APITaskRunner) execute(ctx context.Context, task APITask) error {
	switch task.Kind {
	case apiTaskUpdateWorkflowScheme:
		var payload updateWorkflowSchemeTaskPayload
		if err := json.Unmarshal(task.Payload, &payload); err != nil {
			return fmt.Errorf("decode workflow scheme update: %w", err)
		}
		return r.Store.savePublishedWorkflowScheme(ctx, task.WorkspaceID, task.SubmittedBy, payload.Scheme, payload.StatusMappings, payload.ExpectedVersion, task.ID)
	case apiTaskSwitchWorkflowScheme:
		var payload switchWorkflowSchemeTaskPayload
		if err := json.Unmarshal(task.Payload, &payload); err != nil {
			return fmt.Errorf("decode workflow scheme switch: %w", err)
		}
		return r.Store.switchWorkflowScheme(ctx, task.WorkspaceID, task.SubmittedBy, payload.ProjectID, payload.SchemeID, payload.StatusMappings, task.ID)
	case apiTaskPublishWorkflowScheme:
		var payload publishWorkflowSchemeTaskPayload
		if err := json.Unmarshal(task.Payload, &payload); err != nil {
			return fmt.Errorf("decode workflow scheme publish: %w", err)
		}
		return r.Store.publishWorkflowSchemeDraft(ctx, task.WorkspaceID, task.SubmittedBy, payload.SchemeID, payload.StatusMappings, task.ID)
	case apiTaskBulkWatch, apiTaskBulkUnwatch:
		var payload bulkWatchTaskPayload
		if err := json.Unmarshal(task.Payload, &payload); err != nil {
			return fmt.Errorf("decode bulk watch operation: %w", err)
		}
		return r.Store.executeBulkWatchTask(ctx, task, payload)
	default:
		return fmt.Errorf("unsupported task kind %q", task.Kind)
	}
}

func (s *Store) executeBulkWatchTask(ctx context.Context, task APITask, payload bulkWatchTaskPayload) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	processed := make([]int64, 0, len(payload.Issues))
	invalid := 0
	for _, issue := range payload.Issues {
		var visible bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM issues i WHERE i.workspace_id=$1 AND i.id=$2 AND `+VisibleIssuePredicate("i", "$3")+`)`, task.WorkspaceID, issue.ID, task.SubmittedBy).Scan(&visible); err != nil {
			return err
		}
		if !visible {
			invalid++
			continue
		}
		var changed bool
		if payload.Watch {
			result, err := tx.Exec(ctx, `INSERT INTO watchers(issue_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, issue.ID, task.SubmittedBy)
			if err != nil {
				return err
			}
			changed = result.RowsAffected() == 1
		} else {
			result, err := tx.Exec(ctx, `DELETE FROM watchers WHERE issue_id=$1 AND user_id=$2`, issue.ID, task.SubmittedBy)
			if err != nil {
				return err
			}
			changed = result.RowsAffected() == 1
		}
		if changed {
			seq, err := nextSeq(ctx, tx, task.WorkspaceID)
			if err != nil {
				return err
			}
			encoded, err := json.Marshal(models.WatcherPayload{IssueID: issue.ID, AccountID: task.SubmittedBy})
			if err != nil {
				return err
			}
			op := models.OpUpsert
			if !payload.Watch {
				op = models.OpDelete
			}
			if err := appendAction(ctx, tx, &models.Action{WorkspaceID: task.WorkspaceID, Seq: seq, EntityType: models.EntityWatcher, EntityID: issue.ID, Op: op, SchemaV: models.SchemaVersion, Payload: encoded, ActorID: task.SubmittedBy}); err != nil {
				return err
			}
		}
		processed = append(processed, issue.JiraID)
	}
	result := map[string]any{
		"processedAccessibleIssues":       processed,
		"invalidOrInaccessibleIssueCount": invalid,
		"totalIssueCount":                 len(payload.Issues),
	}
	message := fmt.Sprintf("Processed %d of %d issues.", len(processed), len(payload.Issues))
	if err := completeAPITask(ctx, tx, task.WorkspaceID, task.ID, message, result); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *APITaskRunner) fail(ctx context.Context, task APITask, executionErr error) error {
	result, err := json.Marshal(map[string]any{"errorMessages": []string{executionErr.Error()}, "errors": map[string]string{}})
	if err != nil {
		return err
	}
	_, err = r.Store.Pool.Exec(ctx, `
		UPDATE api_tasks SET status='FAILED',progress=100,message=$3,result=$4,last_update_at=now(),finished_at=now()
		WHERE id=$1 AND workspace_id=$2 AND status='RUNNING'`, task.ID, task.WorkspaceID, executionErr.Error(), result)
	return err
}

func completeAPITask(ctx context.Context, tx pgx.Tx, workspaceID, taskID, message string, result any) error {
	if taskID == "" {
		return nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE api_tasks SET status='COMPLETE',progress=100,message=$2,result=$3,last_update_at=now(),finished_at=now()
		WHERE id=$1 AND workspace_id=$4 AND status='RUNNING'`, taskID, message, encoded, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errAPITaskCancelled
	}
	return nil
}
