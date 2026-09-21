package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

// DORAEnvironmentTypes are the environment types a deployment can report,
// which is Jira's own set.
var DORAEnvironmentTypes = []string{"production", "staging", "testing", "development", "unmapped"}

// DORASettings is which deployments count toward a project's DORA metrics.
// The zero value is what a project starts with: production deployments from
// every pipeline.
type DORASettings struct {
	EnvironmentTypes []string
	// PipelineIDs empty means every pipeline.
	PipelineIDs []string
	// Configured reports that the project has chosen, rather than taking the
	// default, so a page can say which it is showing.
	Configured bool
}

// DefaultDORASettings is the mapping a project has until it chooses one.
// The pipeline list is empty rather than nil: a nil slice reaches Postgres as
// NULL, and every comparison against it answers NULL, which counts nothing.
func DefaultDORASettings() DORASettings {
	return DORASettings{EnvironmentTypes: []string{"production"}, PipelineIDs: []string{}}
}

// DORAPipeline is a pipeline that has deployed work in this project, offered
// as a choice for the mapping.
type DORAPipeline struct {
	ID   string
	Name string
}

// DORASettingsFor reads a project's DORA mapping, or the default.
func (s *Store) DORASettingsFor(ctx context.Context, workspaceID, projectID string) (DORASettings, error) {
	settings := DefaultDORASettings()
	var environments, pipelines []string
	err := s.Pool.QueryRow(ctx, `SELECT environment_types,pipeline_ids FROM project_dora_settings WHERE project_id=$1 AND workspace_id=$2`,
		projectID, workspaceID).Scan(&environments, &pipelines)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return settings, nil
		}
		return settings, err
	}
	return DORASettings{EnvironmentTypes: environments, PipelineIDs: pipelines, Configured: true}, nil
}

// SaveDORASettings records which deployments count. It is a project
// administrator's choice, as the rest of a project's configuration is.
func (s *Store) SaveDORASettings(ctx context.Context, workspaceID, actorID, projectID string, settings DORASettings) error {
	environments, err := normalizeDORAEnvironments(settings.EnvironmentTypes)
	if err != nil {
		return err
	}
	pipelines := normalizeDORAPipelines(settings.PipelineIDs)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectRoleAdmin(ctx, tx, workspaceID, actorID, projectID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO project_dora_settings(project_id,workspace_id,environment_types,pipeline_ids)
		VALUES($1,$2,$3,$4)
		ON CONFLICT(project_id) DO UPDATE SET environment_types=EXCLUDED.environment_types,pipeline_ids=EXCLUDED.pipeline_ids,updated_at=now()`,
		projectID, workspaceID, environments, pipelines); err != nil {
		return err
	}
	if err := appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_dora_settings", projectID, models.OpUpsert,
		map[string]any{"environmentTypes": environments, "pipelineIds": pipelines}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ErrDORASettings is a mapping that would count nothing, or an environment
// type no deployment can report.
var ErrDORASettings = errors.New("invalid DORA mapping")

func normalizeDORAEnvironments(values []string) ([]string, error) {
	known := map[string]bool{}
	for _, value := range DORAEnvironmentTypes {
		known[value] = true
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		if !known[value] {
			return nil, fmt.Errorf("%w: %q is not an environment type", ErrDORASettings, value)
		}
		seen[value] = true
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("%w: choose at least one environment", ErrDORASettings)
	}
	sort.Strings(result)
	return result, nil
}

func normalizeDORAPipelines(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// DORAPipelines lists the pipelines that have deployed work items of this
// project, so the mapping offers what the project actually sees.
func (s *Store) DORAPipelines(ctx context.Context, workspaceID, projectID string) ([]DORAPipeline, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT f.pipeline_id,COALESCE(f.payload->'pipeline'->>'displayName',f.pipeline_id)
		FROM software_delivery_facts f
		WHERE f.workspace_id=$1 AND f.fact_type='deployment'
		  AND EXISTS (SELECT 1 FROM issues i WHERE i.workspace_id=$1 AND i.project_id=$2 AND i.key=ANY(f.issue_keys))
		ORDER BY 2,1`, workspaceID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pipelines := make([]DORAPipeline, 0)
	for rows.Next() {
		var pipeline DORAPipeline
		if err := rows.Scan(&pipeline.ID, &pipeline.Name); err != nil {
			return nil, err
		}
		pipelines = append(pipelines, pipeline)
	}
	return pipelines, rows.Err()
}

// DORAExcludedPeriod is a stretch of days a project leaves out of its
// delivery metrics: a code freeze, a shutdown, a drill.
type DORAExcludedPeriod struct {
	ID       int64
	StartsOn string
	EndsOn   string
	Reason   string
}

// DORAExcludedPeriods lists a project's excluded periods, earliest first.
func (s *Store) DORAExcludedPeriods(ctx context.Context, workspaceID, projectID string) ([]DORAExcludedPeriod, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,starts_on::text,ends_on::text,reason FROM project_dora_excluded_periods
		WHERE project_id=$1 AND workspace_id=$2 ORDER BY starts_on,id`, projectID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	periods := make([]DORAExcludedPeriod, 0)
	for rows.Next() {
		var period DORAExcludedPeriod
		if err := rows.Scan(&period.ID, &period.StartsOn, &period.EndsOn, &period.Reason); err != nil {
			return nil, err
		}
		periods = append(periods, period)
	}
	return periods, rows.Err()
}

// AddDORAExcludedPeriod records a period whose deployments and incidents do
// not count. It is a project administrator's choice, as the mapping is.
func (s *Store) AddDORAExcludedPeriod(ctx context.Context, workspaceID, actorID, projectID string, period DORAExcludedPeriod) error {
	start, err := time.Parse("2006-01-02", strings.TrimSpace(period.StartsOn))
	if err != nil {
		return fmt.Errorf("%w: a period starts on a date, as YYYY-MM-DD", ErrDORASettings)
	}
	end, err := time.Parse("2006-01-02", strings.TrimSpace(period.EndsOn))
	if err != nil {
		return fmt.Errorf("%w: a period ends on a date, as YYYY-MM-DD", ErrDORASettings)
	}
	if end.Before(start) {
		return fmt.Errorf("%w: a period ends on or after the day it starts", ErrDORASettings)
	}
	reason := strings.TrimSpace(period.Reason)
	if len([]rune(reason)) > 255 {
		return fmt.Errorf("%w: a reason is at most 255 characters", ErrDORASettings)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectRoleAdmin(ctx, tx, workspaceID, actorID, projectID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO project_dora_excluded_periods(project_id,workspace_id,starts_on,ends_on,reason) VALUES($1,$2,$3,$4,$5)`,
		projectID, workspaceID, start, end, reason); err != nil {
		return err
	}
	if err := appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_dora_excluded_period", projectID, models.OpUpsert,
		map[string]any{"startsOn": period.StartsOn, "endsOn": period.EndsOn, "reason": reason}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemoveDORAExcludedPeriod puts a period's deployments and incidents back.
func (s *Store) RemoveDORAExcludedPeriod(ctx context.Context, workspaceID, actorID, projectID string, id int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectRoleAdmin(ctx, tx, workspaceID, actorID, projectID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `DELETE FROM project_dora_excluded_periods WHERE id=$1 AND project_id=$2 AND workspace_id=$3`, id, projectID, workspaceID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return fmt.Errorf("%w: that period is already gone", ErrDORASettings)
	}
	if err := appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "project_dora_excluded_period", projectID, models.OpDelete,
		map[string]any{"id": id}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
