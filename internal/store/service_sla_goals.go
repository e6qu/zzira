package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

func (s *Store) ServiceSLAGoals(ctx context.Context, workspaceID, serviceDeskID, metricID string) ([]models.ServiceSLAGoal, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT g.id,g.metric_id,g.name,g.jql,g.goal_millis,g.position
		FROM service_sla_goals g
		JOIN service_sla_metrics m ON m.id=g.metric_id
		JOIN service_desks sd ON sd.id=m.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND ($3='' OR m.id=$3)
		ORDER BY m.position,g.position,g.id::bigint`, workspaceID, serviceDeskID, metricID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	goals := make([]models.ServiceSLAGoal, 0)
	for rows.Next() {
		var goal models.ServiceSLAGoal
		if err := rows.Scan(&goal.ID, &goal.MetricID, &goal.Name, &goal.JQL, &goal.GoalMillis, &goal.Position); err != nil {
			return nil, err
		}
		goals = append(goals, goal)
	}
	return goals, rows.Err()
}

func (s *Store) CreateServiceSLAGoal(ctx context.Context, workspaceID, actorID, serviceDeskID, metricID, name, query string, goalMillis int64) (*models.ServiceSLAGoal, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	goal := &models.ServiceSLAGoal{MetricID: metricID, Name: name, JQL: query, GoalMillis: goalMillis}
	err = tx.QueryRow(ctx, `
		INSERT INTO service_sla_goals(metric_id,name,jql,goal_millis,position)
		SELECT m.id,$4,$5,$6,COALESCE((SELECT max(g.position)+1 FROM service_sla_goals g WHERE g.metric_id=m.id AND g.jql<>''),0)
		FROM service_sla_metrics m JOIN service_desks sd ON sd.id=m.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND m.id=$3
		  AND (SELECT count(*) FROM service_sla_goals g WHERE g.metric_id=m.id AND g.jql<>'') < 50
		RETURNING id,position`, workspaceID, serviceDeskID, metricID, name, query, goalMillis).Scan(&goal.ID, &goal.Position)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("SLA metric does not exist or already has 50 conditional goals")
		}
		return nil, err
	}
	if err := writeServiceSLAGoalAudit(ctx, tx, workspaceID, actorID, serviceDeskID, "service.sla.goal.created", goal); err != nil {
		return nil, err
	}
	return goal, tx.Commit(ctx)
}

func (s *Store) UpdateServiceSLAGoal(ctx context.Context, workspaceID, actorID, serviceDeskID, metricID, goalID, name, query string, goalMillis int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	goal := &models.ServiceSLAGoal{ID: goalID, MetricID: metricID, Name: name, JQL: query, GoalMillis: goalMillis}
	result, err := tx.Exec(ctx, `
		UPDATE service_sla_goals g SET name=$5,jql=$6,goal_millis=$7
		FROM service_sla_metrics m,service_desks sd
		WHERE g.metric_id=m.id AND m.service_desk_id=sd.id AND sd.workspace_id=$1 AND sd.id=$2 AND m.id=$3 AND g.id=$4 AND g.jql<>''`,
		workspaceID, serviceDeskID, metricID, goalID, name, query, goalMillis)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("conditional SLA goal does not exist")
	}
	if _, err := tx.Exec(ctx, `UPDATE service_sla_cycles SET goal_name=$2,goal_millis=$3 WHERE goal_id=$1 AND stopped_at IS NULL`, goalID, name, goalMillis); err != nil {
		return err
	}
	if err := writeServiceSLAGoalAudit(ctx, tx, workspaceID, actorID, serviceDeskID, "service.sla.goal.updated", goal); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteServiceSLAGoal(ctx context.Context, workspaceID, actorID, serviceDeskID, metricID, goalID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	goal := &models.ServiceSLAGoal{ID: goalID, MetricID: metricID}
	err = tx.QueryRow(ctx, `
		DELETE FROM service_sla_goals g USING service_sla_metrics m,service_desks sd
		WHERE g.metric_id=m.id AND m.service_desk_id=sd.id AND sd.workspace_id=$1 AND sd.id=$2 AND m.id=$3 AND g.id=$4 AND g.jql<>''
		RETURNING g.name,g.jql,g.goal_millis,g.position`, workspaceID, serviceDeskID, metricID, goalID).Scan(&goal.Name, &goal.JQL, &goal.GoalMillis, &goal.Position)
	if err != nil {
		return fmt.Errorf("conditional SLA goal does not exist")
	}
	if err := writeServiceSLAGoalAudit(ctx, tx, workspaceID, actorID, serviceDeskID, "service.sla.goal.deleted", goal); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func writeServiceSLAGoalAudit(ctx context.Context, tx pgx.Tx, workspaceID, actorID, serviceDeskID, action string, goal *models.ServiceSLAGoal) error {
	detail, err := json.Marshal(map[string]any{"serviceDeskId": serviceDeskID, "metricId": goal.MetricID, "name": goal.Name, "jql": goal.JQL, "goalMillis": goal.GoalMillis})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,$3,'service_sla_goal',$4,$5::jsonb FROM sites WHERE workspace_id=$1`, workspaceID, actorID, action, goal.ID, detail)
	return err
}

func (s *Store) serviceSLAGoalMatches(ctx context.Context, workspaceID, actorID, issueID, query string) (bool, error) {
	parsed, err := jql.Parse(query)
	if err != nil {
		return false, err
	}
	resolver := jql.DefaultResolver()
	fields, err := s.CustomFields(ctx)
	if err != nil {
		return false, err
	}
	compiled := jql.CompileAt(parsed, actorID, jql.WithCustomFields(resolver, fields), 3)
	if compiled.Err != nil {
		return false, compiled.Err
	}
	args := []any{workspaceID, issueID}
	args = append(args, compiled.Args...)
	var matches bool
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 `+searchJoin+` WHERE i.workspace_id=$1 AND i.id=$2 AND (`+compiled.Where+`))`, args...).Scan(&matches)
	return matches, err
}

func (s *Store) ApplyServiceSLAGoals(ctx context.Context, workspaceID, actorID, serviceDeskID, issueID string) error {
	metrics, err := s.ServiceSLAMetrics(ctx, workspaceID, serviceDeskID)
	if err != nil {
		return err
	}
	for _, metric := range metrics {
		goals, err := s.ServiceSLAGoals(ctx, workspaceID, serviceDeskID, metric.ID)
		if err != nil {
			return err
		}
		var selected *models.ServiceSLAGoal
		for index := range goals {
			goal := &goals[index]
			if goal.JQL == "" {
				if selected == nil {
					selected = goal
				}
				continue
			}
			matches, err := s.serviceSLAGoalMatches(ctx, workspaceID, actorID, issueID, goal.JQL)
			if err != nil {
				return err
			}
			if matches {
				selected = goal
				break
			}
		}
		if selected == nil {
			return fmt.Errorf("SLA metric %q has no default goal", metric.Name)
		}
		if _, err := s.Pool.Exec(ctx, `
			UPDATE service_sla_cycles SET goal_id=$5,goal_name=$6,goal_millis=$7
			WHERE request_issue_id=$1 AND metric_id=$2 AND stopped_at IS NULL
			  AND EXISTS(SELECT 1 FROM service_requests sr WHERE sr.issue_id=$1 AND sr.workspace_id=$3 AND sr.service_desk_id=$4)`,
			issueID, metric.ID, workspaceID, serviceDeskID, selected.ID, selected.Name, selected.GoalMillis); err != nil {
			return err
		}
	}
	return nil
}
