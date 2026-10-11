package store

import (
	"context"
	"errors"
	"log"
	"strconv"
	"time"
)

// PlanTrashRunner uses durable trash timestamps as its schedule. Row locks
// and atomic deletion evidence allow restarts and concurrent server replicas.
// An empty WorkspaceID processes expired plans from every workspace.
type PlanTrashRunner struct {
	Store        *Store
	WorkspaceID  string
	Logf         func(string, ...any)
	PollInterval time.Duration
}

func (r *PlanTrashRunner) Run(ctx context.Context) {
	interval := r.PollInterval
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := r.DrainOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger := r.Logf
			if logger == nil {
				logger = log.Printf
			}
			logger("plan trash runner: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (r *PlanTrashRunner) DrainOnce(ctx context.Context) error {
	if r.Store == nil {
		return errors.New("plan trash runner is not configured")
	}
	// Bound each poll while steadily draining large backlogs. Every batch has
	// its own transaction, so a failed batch leaves its plans eligible to retry.
	for batch := 0; batch < 10; batch++ {
		count, err := r.Store.PurgeExpiredPlans(ctx, r.WorkspaceID)
		if err != nil {
			return err
		}
		if count < 100 {
			return nil
		}
	}
	return nil
}

// PurgeExpiredPlans deletes one batch after the 60-day recovery deadline.
// Restoring and purging both lock the plan; a restored plan cannot be deleted
// using an earlier, unlocked list of candidates.
func (s *Store) PurgeExpiredPlans(ctx context.Context, workspaceID string) (int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT id,workspace_id,name FROM plans
 WHERE ($1='' OR workspace_id=$1) AND status='Trashed' AND trashed_at<=now()-interval '60 days'
 ORDER BY workspace_id,id LIMIT 100 FOR UPDATE SKIP LOCKED`, workspaceID)
	if err != nil {
		return 0, err
	}
	type expiredPlan struct {
		id              int64
		workspace, name string
	}
	plans := []expiredPlan{}
	for rows.Next() {
		var plan expiredPlan
		if err = rows.Scan(&plan.id, &plan.workspace, &plan.name); err != nil {
			rows.Close()
			return 0, err
		}
		plans = append(plans, plan)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, plan := range plans {
		if _, err = tx.Exec(ctx, `DELETE FROM plans WHERE workspace_id=$1 AND id=$2`, plan.workspace, plan.id); err != nil {
			return 0, err
		}
		if err = siteConfigurationAudit(ctx, tx, plan.workspace, "", "plan.trash_expired", "plan:"+strconv.FormatInt(plan.id, 10), map[string]any{"planId": plan.id, "name": plan.name, "reason": "60-day trash retention"}); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(plans), nil
}
