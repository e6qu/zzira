package store

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// PlanScenarioColors are the colors a scenario's changes are flagged in.
var PlanScenarioColors = []string{"blue", "green", "orange", "purple", "red", "teal"}

// PlanScenario is one "what if" version of a plan.
type PlanScenario struct {
	ID      int64
	Name    string
	Color   string
	Default bool
	Changes int
}

// PlanChangeFields are the work item fields a plan can change.
var PlanChangeFields = []string{"summary", "startDate", "endDate", "team", "sprint", "estimate"}

// PlanChange is a change to one field of a work item in a scenario.
type PlanChange struct {
	IssueID, IssueKey, Summary string
	Field                      string
	Value                      json.RawMessage
	ChangedBy                  string
	ChangedByName              string
	ChangedAt                  time.Time
}

// PlanIterationCapacity is the capacity a planner gave one iteration of a team.
type PlanIterationCapacity struct {
	TeamID    int64
	Iteration string
	Capacity  float64
}

var planDay = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// PlanScenarios lists a plan's scenarios, the default first, with how many
// unsaved changes each holds.
func (s *Store) PlanScenarios(ctx context.Context, workspaceID string, planID int64) ([]PlanScenario, error) {
	plan, err := s.Plan(ctx, workspaceID, planID)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT sc.id,sc.name,sc.color,(SELECT count(*) FROM plan_scenario_changes c WHERE c.scenario_id=sc.id)
		FROM plan_scenarios sc WHERE sc.plan_id=$1 ORDER BY sc.id<>$2, lower(sc.name), sc.id`, planID, plan.ScenarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scenarios := []PlanScenario{}
	for rows.Next() {
		var scenario PlanScenario
		if err := rows.Scan(&scenario.ID, &scenario.Name, &scenario.Color, &scenario.Changes); err != nil {
			return nil, err
		}
		scenario.Default = scenario.ID == plan.ScenarioID
		scenarios = append(scenarios, scenario)
	}
	return scenarios, rows.Err()
}

func validPlanScenario(name, color string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 255 {
		return "", planInvalid("The scenario name must be between 1 and 255 characters.")
	}
	for _, allowed := range PlanScenarioColors {
		if color == allowed {
			return name, nil
		}
	}
	return "", planInvalid("Choose one of the scenario colors.")
}

func planScenarioTx(ctx context.Context, tx pgx.Tx, workspaceID string, planID, scenarioID int64) (bool, error) {
	var isDefault bool
	err := tx.QueryRow(ctx, `SELECT sc.id=p.scenario_id FROM plan_scenarios sc JOIN plans p ON p.id=sc.plan_id
		WHERE p.workspace_id=$1 AND p.id=$2 AND sc.id=$3 FOR UPDATE OF sc`, workspaceID, planID, scenarioID).Scan(&isDefault)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrPlanNotFound
	}
	return isDefault, err
}

// CreatePlanScenario adds a scenario to an active plan: blank, from Jira's
// values, or a copy of another scenario's changes and capacities.
func (s *Store) CreatePlanScenario(ctx context.Context, workspaceID, actorID string, planID int64, name, color string, copyFrom int64) (int64, error) {
	name, err := validPlanScenario(name, color)
	if err != nil {
		return 0, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activePlanTx(ctx, tx, workspaceID, planID); err != nil {
		return 0, err
	}
	if copyFrom != 0 {
		if _, err := planScenarioTx(ctx, tx, workspaceID, planID, copyFrom); err != nil {
			return 0, err
		}
	}
	taken, err := exists(ctx, tx, `SELECT 1 FROM plan_scenarios WHERE plan_id=$1 AND lower(name)=lower($2)`, planID, name)
	if err != nil {
		return 0, err
	}
	if taken {
		return 0, planInvalid("The plan already has a scenario named %s.", name)
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO plan_scenarios(id,plan_id,name,color,created_by) VALUES(nextval('jira_plan_scenario_id'),$1,$2,$3,$4) RETURNING id`,
		planID, name, color, actorID).Scan(&id); err != nil {
		return 0, err
	}
	if copyFrom != 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO plan_scenario_changes(scenario_id,issue_id,field,value,changed_by,changed_at)
			SELECT $1,issue_id,field,value,changed_by,changed_at FROM plan_scenario_changes WHERE scenario_id=$2`, id, copyFrom); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO plan_scenario_iteration_capacity(scenario_id,team_id,iteration,capacity,changed_by,changed_at)
			SELECT $1,team_id,iteration,capacity,changed_by,changed_at FROM plan_scenario_iteration_capacity WHERE scenario_id=$2`, id, copyFrom); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit(ctx)
}

// UpdatePlanScenario renames or recolors a scenario.
func (s *Store) UpdatePlanScenario(ctx context.Context, workspaceID string, planID, scenarioID int64, name, color string) error {
	name, err := validPlanScenario(name, color)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activePlanTx(ctx, tx, workspaceID, planID); err != nil {
		return err
	}
	if _, err := planScenarioTx(ctx, tx, workspaceID, planID, scenarioID); err != nil {
		return err
	}
	taken, err := exists(ctx, tx, `SELECT 1 FROM plan_scenarios WHERE plan_id=$1 AND lower(name)=lower($2) AND id<>$3`, planID, name, scenarioID)
	if err != nil {
		return err
	}
	if taken {
		return planInvalid("The plan already has a scenario named %s.", name)
	}
	if _, err := tx.Exec(ctx, `UPDATE plan_scenarios SET name=$2,color=$3 WHERE id=$1`, scenarioID, name, color); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeletePlanScenario removes a scenario other than the plan's default, with
// its unsaved changes.
func (s *Store) DeletePlanScenario(ctx context.Context, workspaceID string, planID, scenarioID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activePlanTx(ctx, tx, workspaceID, planID); err != nil {
		return err
	}
	isDefault, err := planScenarioTx(ctx, tx, workspaceID, planID, scenarioID)
	if err != nil {
		return err
	}
	if isDefault {
		return planInvalid("The default scenario cannot be deleted.")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM plan_scenarios WHERE id=$1`, scenarioID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// NormalizePlanChangeValue checks a value for a change to one field and
// returns it in the form a scenario keeps: text for a summary, a yyyy-MM-dd
// day for dates, a plan team id, a sprint id, a non-negative estimate, or
// null to clear the field.
func NormalizePlanChangeValue(field, value string) (json.RawMessage, error) {
	value = strings.TrimSpace(value)
	switch field {
	case "summary":
		if value == "" || len([]rune(value)) > 255 {
			return nil, planInvalid("The summary must be between 1 and 255 characters.")
		}
		return json.Marshal(value)
	case "startDate", "endDate":
		if value == "" {
			return json.RawMessage("null"), nil
		}
		if _, err := time.Parse("2006-01-02", value); err != nil || !planDay.MatchString(value) {
			return nil, planInvalid("Dates are days in the form yyyy-MM-dd.")
		}
		return json.Marshal(value)
	case "team":
		if value == "" {
			return json.RawMessage("null"), nil
		}
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return nil, planInvalid("Choose a team of the plan.")
		}
		return json.Marshal(id)
	case "sprint":
		if value == "" {
			return json.RawMessage("null"), nil
		}
		if len(value) > 64 {
			return nil, planInvalid("Choose a sprint.")
		}
		return json.Marshal(value)
	case "estimate":
		if value == "" {
			return json.RawMessage("null"), nil
		}
		number, err := strconv.ParseFloat(value, 64)
		if err != nil || number < 0 || number > 1e6 {
			return nil, planInvalid("The estimate must be a number of at least 0.")
		}
		return json.Marshal(number)
	}
	return nil, planInvalid("Plans change the summary, dates, team, sprint and estimate of work.")
}

// SetPlanChange records a change to a work item in a scenario, or forgets
// it when the value is what the work item already has.
func (s *Store) SetPlanChange(ctx context.Context, workspaceID, actorID string, planID, scenarioID int64, issueID, field string, value json.RawMessage, current json.RawMessage) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activePlanTx(ctx, tx, workspaceID, planID); err != nil {
		return err
	}
	if _, err := planScenarioTx(ctx, tx, workspaceID, planID, scenarioID); err != nil {
		return err
	}
	if sameJSON(value, current) {
		_, err = tx.Exec(ctx, `DELETE FROM plan_scenario_changes WHERE scenario_id=$1 AND issue_id=$2 AND field=$3`, scenarioID, issueID, field)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO plan_scenario_changes(scenario_id,issue_id,field,value,changed_by) VALUES($1,$2,$3,$4,$5)
			ON CONFLICT (scenario_id,issue_id,field) DO UPDATE SET value=EXCLUDED.value,changed_by=EXCLUDED.changed_by,changed_at=now()`,
			scenarioID, issueID, field, []byte(value), actorID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func sameJSON(a, b json.RawMessage) bool {
	var left, right any
	if len(a) == 0 {
		a = json.RawMessage("null")
	}
	if len(b) == 0 {
		b = json.RawMessage("null")
	}
	if json.Unmarshal(a, &left) != nil || json.Unmarshal(b, &right) != nil {
		return false
	}
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

// PlanChanges lists a scenario's unsaved changes, by work item.
func (s *Store) PlanChanges(ctx context.Context, workspaceID string, planID, scenarioID int64) ([]PlanChange, error) {
	rows, err := s.Pool.Query(ctx, `SELECT c.issue_id,i.key,i.summary,c.field,c.value,c.changed_by,COALESCE(u.display_name,''),c.changed_at
		FROM plan_scenario_changes c JOIN plan_scenarios sc ON sc.id=c.scenario_id JOIN plans p ON p.id=sc.plan_id
		JOIN issues i ON i.id=c.issue_id LEFT JOIN users u ON u.id=c.changed_by
		WHERE p.workspace_id=$1 AND p.id=$2 AND sc.id=$3
		ORDER BY i.rank, i.key, array_position(ARRAY['summary','startDate','endDate','team','sprint','estimate'], c.field)`, workspaceID, planID, scenarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	changes := []PlanChange{}
	for rows.Next() {
		var change PlanChange
		var raw []byte
		if err := rows.Scan(&change.IssueID, &change.IssueKey, &change.Summary, &change.Field, &raw, &change.ChangedBy, &change.ChangedByName, &change.ChangedAt); err != nil {
			return nil, err
		}
		change.Value = raw
		changes = append(changes, change)
	}
	return changes, rows.Err()
}

// DiscardPlanChange forgets one unsaved change.
func (s *Store) DiscardPlanChange(ctx context.Context, workspaceID string, planID, scenarioID int64, issueID, field string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM plan_scenario_changes c USING plan_scenarios sc, plans p
		WHERE c.scenario_id=sc.id AND sc.plan_id=p.id AND p.workspace_id=$1 AND p.id=$2 AND sc.id=$3 AND c.issue_id=$4 AND c.field=$5`,
		workspaceID, planID, scenarioID, issueID, field)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrPlanNotFound
	}
	return err
}

// SetPlanIterationCapacity gives one iteration of a team its own capacity in
// a scenario, or goes back to the team's capacity when capacity is nil.
func (s *Store) SetPlanIterationCapacity(ctx context.Context, workspaceID, actorID string, planID, scenarioID, teamID int64, iteration string, capacity *float64) error {
	iteration = strings.TrimSpace(iteration)
	if iteration == "" || len(iteration) > 64 {
		return planInvalid("Choose an iteration.")
	}
	if capacity != nil && (*capacity < 0 || *capacity > 1e6) {
		return planInvalid("The capacity must not be negative.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activePlanTx(ctx, tx, workspaceID, planID); err != nil {
		return err
	}
	if _, err := planScenarioTx(ctx, tx, workspaceID, planID, scenarioID); err != nil {
		return err
	}
	found, err := exists(ctx, tx, `SELECT 1 FROM plan_teams WHERE plan_id=$1 AND id=$2`, planID, teamID)
	if err != nil {
		return err
	}
	if !found {
		return ErrPlanNotFound
	}
	if capacity == nil {
		_, err = tx.Exec(ctx, `DELETE FROM plan_scenario_iteration_capacity WHERE scenario_id=$1 AND team_id=$2 AND iteration=$3`, scenarioID, teamID, iteration)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO plan_scenario_iteration_capacity(scenario_id,team_id,iteration,capacity,changed_by) VALUES($1,$2,$3,$4,$5)
			ON CONFLICT (scenario_id,team_id,iteration) DO UPDATE SET capacity=EXCLUDED.capacity,changed_by=EXCLUDED.changed_by,changed_at=now()`,
			scenarioID, teamID, iteration, *capacity, actorID)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PlanIterationCapacities lists the iteration capacities a scenario sets.
func (s *Store) PlanIterationCapacities(ctx context.Context, scenarioID int64) ([]PlanIterationCapacity, error) {
	rows, err := s.Pool.Query(ctx, `SELECT team_id,iteration,capacity FROM plan_scenario_iteration_capacity WHERE scenario_id=$1`, scenarioID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlanIterationCapacity{}
	for rows.Next() {
		var capacity PlanIterationCapacity
		if err := rows.Scan(&capacity.TeamID, &capacity.Iteration, &capacity.Capacity); err != nil {
			return nil, err
		}
		out = append(out, capacity)
	}
	return out, rows.Err()
}
