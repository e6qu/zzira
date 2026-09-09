package store

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
)

// ProjectTrashRunner permanently removes projects after Jira's 60-day trash
// recovery window. The durable trashed_at timestamp is the schedule source, so
// restarts and multiple replicas do not lose work.
type ProjectTrashRunner struct {
	Store        *Store
	Logf         func(string, ...any)
	PollInterval time.Duration
}

func (r *ProjectTrashRunner) Run(ctx context.Context, workspaceID string) {
	interval := r.PollInterval
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := r.DrainOnce(ctx, workspaceID); err != nil && !errors.Is(err, context.Canceled) {
			logger := r.Logf
			if logger == nil {
				logger = log.Printf
			}
			logger("project trash runner: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *ProjectTrashRunner) DrainOnce(ctx context.Context, workspaceID string) error {
	if r.Store == nil {
		return errors.New("project trash runner is not configured")
	}
	rows, err := r.Store.Pool.Query(ctx, `
		SELECT p.id,COALESCE((
			SELECT m.user_id FROM memberships m
			WHERE m.workspace_id=p.workspace_id AND m.role='admin'
			ORDER BY m.user_id LIMIT 1
		),p.lifecycle_actor_id,'')
		FROM projects p
		WHERE p.workspace_id=$1 AND p.lifecycle_state='TRASHED'
		  AND p.trashed_at<=now()-interval '60 days'
		ORDER BY p.trashed_at,p.id LIMIT 100`, workspaceID)
	if err != nil {
		return err
	}
	type expiredProject struct{ projectID, actorID string }
	projects := []expiredProject{}
	for rows.Next() {
		var project expiredProject
		if err = rows.Scan(&project.projectID, &project.actorID); err != nil {
			rows.Close()
			return err
		}
		projects = append(projects, project)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, project := range projects {
		if project.actorID == "" {
			return errors.New("expired project has no administrator for deletion evidence")
		}
		err = r.Store.PermanentDeleteProject(ctx, workspaceID, project.actorID, project.projectID, nil)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	return nil
}
