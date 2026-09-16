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
		SELECT g.id,g.metric_id,g.name,g.jql,g.goal_millis,g.position,COALESCE(g.calendar_id,'')
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
		if err := rows.Scan(&goal.ID, &goal.MetricID, &goal.Name, &goal.JQL, &goal.GoalMillis, &goal.Position, &goal.CalendarID); err != nil {
			return nil, err
		}
		goals = append(goals, goal)
	}
	return goals, rows.Err()
}

func (s *Store) CreateServiceSLAGoal(ctx context.Context, workspaceID, actorID, serviceDeskID, metricID, name, query, calendarID string, goalMillis int64) (*models.ServiceSLAGoal, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	goal := &models.ServiceSLAGoal{MetricID: metricID, Name: name, JQL: query, CalendarID: calendarID, GoalMillis: goalMillis}
	err = tx.QueryRow(ctx, `
		INSERT INTO service_sla_goals(metric_id,name,jql,goal_millis,position,calendar_id)
		SELECT m.id,$4,$5,$6,COALESCE((SELECT max(g.position)+1 FROM service_sla_goals g WHERE g.metric_id=m.id AND g.jql<>''),0),
		  (SELECT c.id FROM service_calendars c WHERE c.id=NULLIF($7,'') AND c.service_desk_id=sd.id)
		FROM service_sla_metrics m JOIN service_desks sd ON sd.id=m.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND m.id=$3
		  AND (SELECT count(*) FROM service_sla_goals g WHERE g.metric_id=m.id AND g.jql<>'') < 50
		  AND ($7='' OR EXISTS(SELECT 1 FROM service_calendars c WHERE c.id=$7 AND c.service_desk_id=sd.id))
		RETURNING id,position`, workspaceID, serviceDeskID, metricID, name, query, goalMillis, calendarID).Scan(&goal.ID, &goal.Position)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("SLA metric does not exist, already has 50 conditional goals, or names a calendar of another service desk")
		}
		return nil, err
	}
	if err := writeServiceSLAGoalAudit(ctx, tx, workspaceID, actorID, serviceDeskID, "service.sla.goal.created", goal); err != nil {
		return nil, err
	}
	return goal, tx.Commit(ctx)
}

func (s *Store) UpdateServiceSLAGoal(ctx context.Context, workspaceID, actorID, serviceDeskID, metricID, goalID, name, query, calendarID string, goalMillis int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	goal := &models.ServiceSLAGoal{ID: goalID, MetricID: metricID, Name: name, JQL: query, CalendarID: calendarID, GoalMillis: goalMillis}
	result, err := tx.Exec(ctx, `
		UPDATE service_sla_goals g SET name=$5,jql=$6,goal_millis=$7,calendar_id=NULLIF($8,'')
		FROM service_sla_metrics m,service_desks sd
		WHERE g.metric_id=m.id AND m.service_desk_id=sd.id AND sd.workspace_id=$1 AND sd.id=$2 AND m.id=$3 AND g.id=$4 AND g.jql<>''
		  AND ($8='' OR EXISTS(SELECT 1 FROM service_calendars c WHERE c.id=$8 AND c.service_desk_id=sd.id))`,
		workspaceID, serviceDeskID, metricID, goalID, name, query, goalMillis, calendarID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("conditional SLA goal does not exist, or names a calendar of another service desk")
	}
	if _, err := tx.Exec(ctx, `UPDATE service_sla_cycles SET goal_name=$2,goal_millis=$3,calendar_id=NULLIF($4,'') WHERE goal_id=$1 AND stopped_at IS NULL`, goalID, name, goalMillis, calendarID); err != nil {
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
	if err := s.ExpandAppJQL(ctx, workspaceID, parsed); err != nil {
		return false, err
	}
	resolver, err := s.JQLResolver(ctx, workspaceID)
	if err != nil {
		return false, err
	}
	compiled := jql.CompileAt(parsed, actorID, resolver, 3)
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
			UPDATE service_sla_cycles SET goal_id=$5,goal_name=$6,goal_millis=$7,calendar_id=NULLIF($8,'')
			WHERE request_issue_id=$1 AND metric_id=$2 AND stopped_at IS NULL
			  AND EXISTS(SELECT 1 FROM service_requests sr WHERE sr.issue_id=$1 AND sr.workspace_id=$3 AND sr.service_desk_id=$4)`,
			issueID, metric.ID, workspaceID, serviceDeskID, selected.ID, selected.Name, selected.GoalMillis, selected.CalendarID); err != nil {
			return err
		}
	}
	return nil
}

// MoveServiceSLAGoal moves a conditional goal one place up or down among its
// metric's conditional goals, which are then numbered in their new order. The
// default goal always stays last, and a goal already at the end stays put.
func (s *Store) MoveServiceSLAGoal(ctx context.Context, workspaceID, actorID, serviceDeskID, metricID, goalID, direction string) error {
	if direction != "up" && direction != "down" {
		return fmt.Errorf("move a conditional SLA goal up or down")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT g.id,g.name,g.jql,g.goal_millis FROM service_sla_goals g
		JOIN service_sla_metrics m ON m.id=g.metric_id JOIN service_desks sd ON sd.id=m.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND m.id=$3 AND g.jql<>''
		ORDER BY g.position,g.id::bigint FOR UPDATE OF g`, workspaceID, serviceDeskID, metricID)
	if err != nil {
		return err
	}
	goals := []models.ServiceSLAGoal{}
	for rows.Next() {
		goal := models.ServiceSLAGoal{MetricID: metricID}
		if err := rows.Scan(&goal.ID, &goal.Name, &goal.JQL, &goal.GoalMillis); err != nil {
			rows.Close()
			return err
		}
		goals = append(goals, goal)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	index := -1
	for position, goal := range goals {
		if goal.ID == goalID {
			index = position
		}
	}
	if index < 0 {
		return fmt.Errorf("conditional SLA goal does not exist")
	}
	target := index - 1
	if direction == "down" {
		target = index + 1
	}
	if target < 0 || target >= len(goals) {
		return tx.Commit(ctx)
	}
	goals[index], goals[target] = goals[target], goals[index]
	for position, goal := range goals {
		if _, err := tx.Exec(ctx, `UPDATE service_sla_goals SET position=$2 WHERE id=$1`, goal.ID, position); err != nil {
			return err
		}
	}
	moved := goals[target]
	moved.Position = target
	if err := writeServiceSLAGoalAudit(ctx, tx, workspaceID, actorID, serviceDeskID, "service.sla.goal.moved", &moved); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
