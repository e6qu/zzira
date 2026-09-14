package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Plan errors separate Jira's 400, 404 and 409 answers.
var (
	ErrPlanValidation = errors.New("invalid plan")
	ErrPlanNotFound   = errors.New("plan not found")
	ErrPlanNotActive  = errors.New("plan is not active")
)

// ---- Atlassian teams ----

type AtlassianTeam struct {
	ID, Name, Description string
	MemberIDs             []string
	CreatedAt             time.Time
}

func (s *Store) AtlassianTeams(ctx context.Context, workspaceID string) ([]AtlassianTeam, error) {
	rows, err := s.Pool.Query(ctx, `SELECT t.id::text,t.name,t.description,t.created_at,
		COALESCE(array_agg(m.user_id ORDER BY m.added_at,m.user_id) FILTER (WHERE m.user_id IS NOT NULL),'{}')
		FROM atlassian_teams t LEFT JOIN atlassian_team_members m ON m.team_id=t.id
		WHERE t.workspace_id=$1 GROUP BY t.id ORDER BY lower(t.name),t.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	teams := []AtlassianTeam{}
	for rows.Next() {
		var team AtlassianTeam
		if err := rows.Scan(&team.ID, &team.Name, &team.Description, &team.CreatedAt, &team.MemberIDs); err != nil {
			return nil, err
		}
		teams = append(teams, team)
	}
	return teams, rows.Err()
}

func (s *Store) AtlassianTeam(ctx context.Context, workspaceID, teamID string) (AtlassianTeam, error) {
	teams, err := s.AtlassianTeams(ctx, workspaceID)
	if err != nil {
		return AtlassianTeam{}, err
	}
	for _, team := range teams {
		if team.ID == teamID {
			return team, nil
		}
	}
	return AtlassianTeam{}, pgx.ErrNoRows
}

func (s *Store) CreateAtlassianTeam(ctx context.Context, workspaceID, actorID, name, description string) (string, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || len([]rune(name)) > 255 || len([]rune(description)) > 2000 {
		return "", fmt.Errorf("%w: a team needs a name of 1 to 255 characters and a description of at most 2000", ErrPlanValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO atlassian_teams(workspace_id,name,description,created_by) VALUES($1,$2,$3,$4) RETURNING id::text`, workspaceID, name, description, actorID).Scan(&id); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO atlassian_team_members(team_id,user_id) VALUES($1,$2)`, id, actorID); err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

// SetAtlassianTeamMember adds or removes an active member of the site.
func (s *Store) SetAtlassianTeamMember(ctx context.Context, workspaceID, teamID, userID string, member bool) error {
	if _, err := s.AtlassianTeam(ctx, workspaceID, teamID); err != nil {
		return err
	}
	if !member {
		_, err := s.Pool.Exec(ctx, `DELETE FROM atlassian_team_members WHERE team_id=$1 AND user_id=$2`, teamID, userID)
		return err
	}
	if _, err := s.MemberByID(ctx, workspaceID, userID); err != nil {
		return fmt.Errorf("%w: choose a member of this site", ErrPlanValidation)
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO atlassian_team_members(team_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, teamID, userID)
	return err
}

func (s *Store) DeleteAtlassianTeam(ctx context.Context, workspaceID, teamID string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM atlassian_teams WHERE workspace_id=$1 AND id::text=$2`, workspaceID, teamID)
	if err == nil && tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

// ---- plans ----

type PlanDateField struct {
	Type              string `json:"type"`
	DateCustomFieldID *int64 `json:"dateCustomFieldId,omitempty"`
}

type PlanScheduling struct {
	Dependencies  string        `json:"dependencies"`
	EndDate       PlanDateField `json:"endDate"`
	Estimation    string        `json:"estimation"`
	InferredDates string        `json:"inferredDates"`
	StartDate     PlanDateField `json:"startDate"`
}

type PlanExclusionRules struct {
	IssueIDs                          []int64 `json:"issueIds"`
	IssueTypeIDs                      []int64 `json:"issueTypeIds"`
	NumberOfDaysToShowCompletedIssues int     `json:"numberOfDaysToShowCompletedIssues"`
	ReleaseIDs                        []int64 `json:"releaseIds"`
	WorkStatusCategoryIDs             []int64 `json:"workStatusCategoryIds"`
	WorkStatusIDs                     []int64 `json:"workStatusIds"`
}

type PlanIssueSource struct {
	ID    int64  `json:"-"`
	Type  string `json:"type"`
	Value int64  `json:"value"`
}

type PlanCustomField struct {
	CustomFieldID int64 `json:"customFieldId"`
	Filter        bool  `json:"filter"`
}

type PlanRelease struct {
	Name       string  `json:"name"`
	ReleaseIDs []int64 `json:"releaseIds"`
}

// PlanPermission holds a group by its id or a person by account id.
type PlanPermission struct {
	Type       string `json:"type"`
	HolderType string `json:"holderType"`
	Holder     string `json:"holder"`
}

type Plan struct {
	ID                          int64
	Name, LeadAccountID, Status string
	ScenarioID                  int64
	Scheduling                  PlanScheduling
	ExclusionRules              PlanExclusionRules
	IssueSources                []PlanIssueSource
	CustomFields                []PlanCustomField
	CrossProjectReleases        []PlanRelease
	Permissions                 []PlanPermission
	UpdatedAt                   time.Time
}

func planInvalid(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrPlanValidation}, args...)...)
}

// NormalizePlan applies Jira's defaults for omitted scheduling and exclusion
// settings.
func NormalizePlan(plan *Plan) {
	plan.Name = strings.TrimSpace(plan.Name)
	if plan.Scheduling.Dependencies == "" {
		plan.Scheduling.Dependencies = "Sequential"
	}
	if plan.Scheduling.InferredDates == "" {
		plan.Scheduling.InferredDates = "None"
	}
	if plan.Scheduling.StartDate.Type == "" {
		plan.Scheduling.StartDate.Type = "TargetStartDate"
	}
	if plan.Scheduling.EndDate.Type == "" {
		plan.Scheduling.EndDate.Type = "TargetEndDate"
	}
	rules := &plan.ExclusionRules
	for _, list := range []*[]int64{&rules.IssueIDs, &rules.IssueTypeIDs, &rules.ReleaseIDs, &rules.WorkStatusCategoryIDs, &rules.WorkStatusIDs} {
		if *list == nil {
			*list = []int64{}
		}
	}
	if plan.CustomFields == nil {
		plan.CustomFields = []PlanCustomField{}
	}
	if plan.CrossProjectReleases == nil {
		plan.CrossProjectReleases = []PlanRelease{}
	}
	if plan.Permissions == nil {
		plan.Permissions = []PlanPermission{}
	}
	for index := range plan.CrossProjectReleases {
		if plan.CrossProjectReleases[index].ReleaseIDs == nil {
			plan.CrossProjectReleases[index].ReleaseIDs = []int64{}
		}
	}
}

func exists(ctx context.Context, tx pgx.Tx, query string, args ...any) (bool, error) {
	var found bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(`+query+`)`, args...).Scan(&found)
	return found, err
}

// validatePlanTx checks a plan's settings and every entity it names.
func validatePlanTx(ctx context.Context, tx pgx.Tx, workspaceID string, plan *Plan) error {
	if plan.Name == "" || len([]rune(plan.Name)) > 255 {
		return planInvalid("The plan name must be between 1 and 255 characters.")
	}
	switch plan.Scheduling.Estimation {
	case "StoryPoints", "Days", "Hours":
	default:
		return planInvalid("The estimation must be StoryPoints, Days or Hours.")
	}
	if plan.Scheduling.Dependencies != "Sequential" && plan.Scheduling.Dependencies != "Concurrent" {
		return planInvalid("The dependencies must be Sequential or Concurrent.")
	}
	switch plan.Scheduling.InferredDates {
	case "None", "SprintDates", "ReleaseDates":
	default:
		return planInvalid("The inferred dates must be None, SprintDates or ReleaseDates.")
	}
	for _, date := range []*PlanDateField{&plan.Scheduling.StartDate, &plan.Scheduling.EndDate} {
		switch date.Type {
		case "DueDate", "TargetStartDate", "TargetEndDate":
			date.DateCustomFieldID = nil
		case "DateCustomField":
			if date.DateCustomFieldID == nil {
				return planInvalid("A DateCustomField date needs dateCustomFieldId.")
			}
			found, err := exists(ctx, tx, `SELECT 1 FROM custom_fields WHERE id='customfield_'||$2::bigint::text AND (workspace_id IS NULL OR workspace_id=$1) AND type='datetime'`, workspaceID, *date.DateCustomFieldID)
			if err != nil {
				return err
			}
			if !found {
				return planInvalid("The date custom field %d does not exist or is not a date field.", *date.DateCustomFieldID)
			}
		default:
			return planInvalid("The date field type must be DueDate, TargetStartDate, TargetEndDate or DateCustomField.")
		}
	}
	if plan.LeadAccountID != "" {
		found, err := exists(ctx, tx, `SELECT 1 FROM memberships WHERE workspace_id=$1 AND user_id=$2`, workspaceID, plan.LeadAccountID)
		if err != nil {
			return err
		}
		if !found {
			return planInvalid("The plan lead %s is not a user of this site.", plan.LeadAccountID)
		}
	}
	if len(plan.IssueSources) == 0 || len(plan.IssueSources) > 100 {
		return planInvalid("A plan needs between 1 and 100 issue sources.")
	}
	seenSources := map[string]bool{}
	for _, source := range plan.IssueSources {
		key := source.Type + ":" + strconv.FormatInt(source.Value, 10)
		if seenSources[key] {
			return planInvalid("The issue source %s %d is included more than once.", source.Type, source.Value)
		}
		seenSources[key] = true
		query := ""
		switch source.Type {
		case "Board":
			query = `SELECT 1 FROM boards b JOIN projects p ON p.id=b.project_id WHERE p.workspace_id=$1 AND b.jira_id=$2`
		case "Project":
			query = `SELECT 1 FROM projects WHERE workspace_id=$1 AND id=$2::bigint::text AND trashed_at IS NULL`
		case "Filter":
			query = `SELECT 1 FROM filters WHERE workspace_id=$1 AND jira_id=$2`
		default:
			return planInvalid("The issue source type must be Board, Project or Filter.")
		}
		found, err := exists(ctx, tx, query, workspaceID, source.Value)
		if err != nil {
			return err
		}
		if !found {
			return planInvalid("The %s %d does not exist.", strings.ToLower(source.Type), source.Value)
		}
	}
	seenFields := map[int64]bool{}
	for _, field := range plan.CustomFields {
		found, err := exists(ctx, tx, `SELECT 1 FROM custom_fields WHERE id='customfield_'||$2::bigint::text AND (workspace_id IS NULL OR workspace_id=$1)`, workspaceID, field.CustomFieldID)
		if err != nil {
			return err
		}
		if !found || seenFields[field.CustomFieldID] {
			return planInvalid("The custom field %d does not exist or is included more than once.", field.CustomFieldID)
		}
		seenFields[field.CustomFieldID] = true
	}
	releaseExists := func(id int64) error {
		found, err := exists(ctx, tx, `SELECT 1 FROM project_versions v JOIN projects p ON p.id=v.project_id WHERE p.workspace_id=$1 AND v.id=$2::bigint::text`, workspaceID, id)
		if err != nil {
			return err
		}
		if !found {
			return planInvalid("The release %d does not exist.", id)
		}
		return nil
	}
	releaseNames := map[string]bool{}
	for index, release := range plan.CrossProjectReleases {
		release.Name = strings.TrimSpace(release.Name)
		plan.CrossProjectReleases[index].Name = release.Name
		if release.Name == "" || len([]rune(release.Name)) > 255 || releaseNames[strings.ToLower(release.Name)] {
			return planInvalid("Cross-project releases need unique names of 1 to 255 characters.")
		}
		releaseNames[strings.ToLower(release.Name)] = true
		for _, id := range release.ReleaseIDs {
			if err := releaseExists(id); err != nil {
				return err
			}
		}
	}
	rules := plan.ExclusionRules
	if rules.NumberOfDaysToShowCompletedIssues < 0 {
		return planInvalid("numberOfDaysToShowCompletedIssues must not be negative.")
	}
	for _, id := range rules.IssueIDs {
		found, err := exists(ctx, tx, `SELECT 1 FROM issues WHERE workspace_id=$1 AND jira_id=$2`, workspaceID, id)
		if err != nil {
			return err
		}
		if !found {
			return planInvalid("The issue %d does not exist.", id)
		}
	}
	for _, id := range rules.IssueTypeIDs {
		found, err := exists(ctx, tx, `SELECT 1 FROM issue_types WHERE (workspace_id IS NULL OR workspace_id=$1) AND jira_id=$2`, workspaceID, id)
		if err != nil {
			return err
		}
		if !found {
			return planInvalid("The issue type %d does not exist.", id)
		}
	}
	for _, id := range rules.ReleaseIDs {
		if err := releaseExists(id); err != nil {
			return err
		}
	}
	for _, id := range rules.WorkStatusCategoryIDs {
		if id != 2 && id != 3 && id != 4 {
			return planInvalid("The status category %d does not exist.", id)
		}
	}
	for _, id := range rules.WorkStatusIDs {
		found, err := exists(ctx, tx, `SELECT 1 FROM statuses WHERE (workspace_id IS NULL OR workspace_id=$1) AND jira_id=$2`, workspaceID, id)
		if err != nil {
			return err
		}
		if !found {
			return planInvalid("The status %d does not exist.", id)
		}
	}
	for _, permission := range plan.Permissions {
		if permission.Type != "View" && permission.Type != "Edit" {
			return planInvalid("The permission type must be View or Edit.")
		}
		switch permission.HolderType {
		case "Group":
			found, err := exists(ctx, tx, `SELECT 1 FROM groups g JOIN directories d ON d.id=g.directory_id JOIN sites si ON si.organization_id=d.organization_id WHERE si.workspace_id=$1 AND g.id::text=$2`, workspaceID, permission.Holder)
			if err != nil {
				return err
			}
			if !found {
				return planInvalid("The group does not exist.")
			}
		case "AccountId":
			found, err := exists(ctx, tx, `SELECT 1 FROM memberships WHERE workspace_id=$1 AND user_id=$2`, workspaceID, permission.Holder)
			if err != nil {
				return err
			}
			if !found {
				return planInvalid("The user %s does not exist.", permission.Holder)
			}
		default:
			return planInvalid("The permission holder type must be Group or AccountId.")
		}
	}
	return nil
}

func marshalPlanColumns(plan *Plan) ([]byte, []byte, []byte, []byte, []byte, error) {
	var out [5][]byte
	for index, value := range []any{plan.Scheduling, plan.ExclusionRules, plan.CustomFields, plan.CrossProjectReleases, plan.Permissions} {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		out[index] = raw
	}
	return out[0], out[1], out[2], out[3], out[4], nil
}

// writePlanSourcesTx replaces a plan's issue sources, keeping the id of each
// source that stays so plan teams keep pointing at it.
func writePlanSourcesTx(ctx context.Context, tx pgx.Tx, planID int64, sources []PlanIssueSource) error {
	keep := []int64{}
	for position, source := range sources {
		var id int64
		err := tx.QueryRow(ctx, `INSERT INTO plan_issue_sources(plan_id,position,type,value) VALUES($1,$2,$3,$4)
			ON CONFLICT(plan_id,type,value) DO UPDATE SET position=EXCLUDED.position RETURNING id`, planID, position, source.Type, source.Value).Scan(&id)
		if err != nil {
			return err
		}
		keep = append(keep, id)
	}
	_, err := tx.Exec(ctx, `DELETE FROM plan_issue_sources WHERE plan_id=$1 AND NOT (id = ANY($2))`, planID, keep)
	return err
}

func (s *Store) CreatePlan(ctx context.Context, workspaceID, actorID string, plan Plan) (int64, error) {
	NormalizePlan(&plan)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := validatePlanTx(ctx, tx, workspaceID, &plan); err != nil {
		return 0, err
	}
	scheduling, rules, fields, releases, permissions, err := marshalPlanColumns(&plan)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO plans(workspace_id,name,lead_account_id,scheduling,exclusion_rules,custom_fields,cross_project_releases,permissions,created_by)
		VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9) RETURNING id`, workspaceID, plan.Name, plan.LeadAccountID, scheduling, rules, fields, releases, permissions, actorID).Scan(&id); err != nil {
		return 0, err
	}
	if err := writePlanSourcesTx(ctx, tx, id, plan.IssueSources); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

const planColumns = `p.id,p.name,COALESCE(p.lead_account_id,''),p.status,p.scenario_id,p.scheduling,p.exclusion_rules,p.custom_fields,p.cross_project_releases,p.permissions,p.updated_at`

func scanPlan(row pgx.Row) (Plan, error) {
	var plan Plan
	var scheduling, rules, fields, releases, permissions []byte
	if err := row.Scan(&plan.ID, &plan.Name, &plan.LeadAccountID, &plan.Status, &plan.ScenarioID, &scheduling, &rules, &fields, &releases, &permissions, &plan.UpdatedAt); err != nil {
		return Plan{}, err
	}
	for raw, target := range map[*[]byte]any{&scheduling: &plan.Scheduling, &rules: &plan.ExclusionRules, &fields: &plan.CustomFields, &releases: &plan.CrossProjectReleases, &permissions: &plan.Permissions} {
		if err := json.Unmarshal(*raw, target); err != nil {
			return Plan{}, err
		}
	}
	NormalizePlan(&plan)
	return plan, nil
}

func (s *Store) planSources(ctx context.Context, planIDs []int64) (map[int64][]PlanIssueSource, error) {
	rows, err := s.Pool.Query(ctx, `SELECT plan_id,id,type,value FROM plan_issue_sources WHERE plan_id = ANY($1) ORDER BY plan_id,position`, planIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := map[int64][]PlanIssueSource{}
	for rows.Next() {
		var planID int64
		var source PlanIssueSource
		if err := rows.Scan(&planID, &source.ID, &source.Type, &source.Value); err != nil {
			return nil, err
		}
		sources[planID] = append(sources[planID], source)
	}
	return sources, rows.Err()
}

func (s *Store) Plan(ctx context.Context, workspaceID string, planID int64) (Plan, error) {
	plan, err := scanPlan(s.Pool.QueryRow(ctx, `SELECT `+planColumns+` FROM plans p WHERE p.workspace_id=$1 AND p.id=$2`, workspaceID, planID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Plan{}, ErrPlanNotFound
	}
	if err != nil {
		return Plan{}, err
	}
	sources, err := s.planSources(ctx, []int64{plan.ID})
	plan.IssueSources = sources[plan.ID]
	return plan, err
}

// Plans pages plans in id order after the cursor id.
func (s *Store) Plans(ctx context.Context, workspaceID string, includeTrashed, includeArchived bool, after int64, limit int) ([]Plan, int, error) {
	statuses := []string{"Active"}
	if includeTrashed {
		statuses = append(statuses, "Trashed")
	}
	if includeArchived {
		statuses = append(statuses, "Archived")
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM plans WHERE workspace_id=$1 AND status = ANY($2)`, workspaceID, statuses).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+planColumns+` FROM plans p WHERE p.workspace_id=$1 AND p.status = ANY($2) AND p.id>$3 ORDER BY p.id LIMIT $4`, workspaceID, statuses, after, limit)
	if err != nil {
		return nil, 0, err
	}
	plans := []Plan{}
	ids := []int64{}
	for rows.Next() {
		plan, err := scanPlan(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		plans = append(plans, plan)
		ids = append(ids, plan.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	sources, err := s.planSources(ctx, ids)
	for index := range plans {
		plans[index].IssueSources = sources[plans[index].ID]
	}
	return plans, total, err
}

// activePlanTx locks a plan for a change, which Jira allows only while the
// plan is active.
func activePlanTx(ctx context.Context, tx pgx.Tx, workspaceID string, planID int64) error {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM plans WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, workspaceID, planID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrPlanNotFound
	}
	if err != nil {
		return err
	}
	if status != "Active" {
		return ErrPlanNotActive
	}
	return nil
}

func (s *Store) UpdatePlan(ctx context.Context, workspaceID string, plan Plan) error {
	NormalizePlan(&plan)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activePlanTx(ctx, tx, workspaceID, plan.ID); err != nil {
		return err
	}
	if err := validatePlanTx(ctx, tx, workspaceID, &plan); err != nil {
		return err
	}
	scheduling, rules, fields, releases, permissions, err := marshalPlanColumns(&plan)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE plans SET name=$3,lead_account_id=NULLIF($4,''),scheduling=$5,exclusion_rules=$6,custom_fields=$7,cross_project_releases=$8,permissions=$9,updated_at=now()
		WHERE workspace_id=$1 AND id=$2`, workspaceID, plan.ID, plan.Name, plan.LeadAccountID, scheduling, rules, fields, releases, permissions); err != nil {
		return err
	}
	if err := writePlanSourcesTx(ctx, tx, plan.ID, plan.IssueSources); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// SetPlanStatus archives or trashes an active plan.
func (s *Store) SetPlanStatus(ctx context.Context, workspaceID string, planID int64, status string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activePlanTx(ctx, tx, workspaceID, planID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE plans SET status=$3,updated_at=now() WHERE workspace_id=$1 AND id=$2`, workspaceID, planID, status); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DuplicatePlan copies an active plan with its issue sources and teams.
func (s *Store) DuplicatePlan(ctx context.Context, workspaceID, actorID string, planID int64, name string) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 255 {
		return 0, planInvalid("The plan name must be between 1 and 255 characters.")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activePlanTx(ctx, tx, workspaceID, planID); err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRow(ctx, `INSERT INTO plans(workspace_id,name,lead_account_id,scheduling,exclusion_rules,custom_fields,cross_project_releases,permissions,created_by)
		SELECT workspace_id,$3,lead_account_id,scheduling,exclusion_rules,custom_fields,cross_project_releases,permissions,$4 FROM plans WHERE workspace_id=$1 AND id=$2 RETURNING id`,
		workspaceID, planID, name, actorID).Scan(&id); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `WITH copied AS (
			INSERT INTO plan_issue_sources(plan_id,position,type,value) SELECT $2,position,type,value FROM plan_issue_sources WHERE plan_id=$1 RETURNING id,type,value
		)
		INSERT INTO plan_teams(plan_id,atlassian_team_id,name,planning_style,issue_source_id,sprint_length,capacity,member_account_ids)
		SELECT $2,t.atlassian_team_id,t.name,t.planning_style,c.id,t.sprint_length,t.capacity,t.member_account_ids
		FROM plan_teams t LEFT JOIN plan_issue_sources o ON o.id=t.issue_source_id LEFT JOIN copied c ON c.type=o.type AND c.value=o.value
		WHERE t.plan_id=$1 ORDER BY t.id`, planID, id); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

// ---- plan teams ----

type PlanTeam struct {
	ID              int64
	AtlassianTeamID string
	Name            string
	PlanningStyle   string
	IssueSourceID   *int64
	SprintLength    *int64
	Capacity        *float64
	MemberIDs       []string
}

func (s *Store) PlanTeams(ctx context.Context, workspaceID string, planID int64, after int64, limit int) ([]PlanTeam, int, error) {
	if _, err := s.Plan(ctx, workspaceID, planID); err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM plan_teams WHERE plan_id=$1`, planID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+planTeamColumns+` FROM plan_teams WHERE plan_id=$1 AND id>$2 ORDER BY id LIMIT $3`, planID, after, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	teams := []PlanTeam{}
	for rows.Next() {
		team, err := scanPlanTeam(rows)
		if err != nil {
			return nil, 0, err
		}
		teams = append(teams, team)
	}
	return teams, total, rows.Err()
}

const planTeamColumns = `id,COALESCE(atlassian_team_id::text,''),COALESCE(name,''),planning_style,issue_source_id,sprint_length,capacity,member_account_ids`

func scanPlanTeam(row pgx.Row) (PlanTeam, error) {
	var team PlanTeam
	err := row.Scan(&team.ID, &team.AtlassianTeamID, &team.Name, &team.PlanningStyle, &team.IssueSourceID, &team.SprintLength, &team.Capacity, &team.MemberIDs)
	return team, err
}

// PlanTeam finds a plan-only team by id or an Atlassian team by its team id.
// Jira answers 409 for any team operation on a plan that is not active.
func (s *Store) PlanTeam(ctx context.Context, workspaceID string, planID int64, atlassianTeamID string, planOnlyID int64) (PlanTeam, error) {
	plan, err := s.Plan(ctx, workspaceID, planID)
	if err != nil {
		return PlanTeam{}, err
	}
	if plan.Status != "Active" {
		return PlanTeam{}, ErrPlanNotActive
	}
	query := `SELECT ` + planTeamColumns + ` FROM plan_teams WHERE plan_id=$1 AND name IS NOT NULL AND id=$2`
	arg := any(planOnlyID)
	if atlassianTeamID != "" {
		query = `SELECT ` + planTeamColumns + ` FROM plan_teams WHERE plan_id=$1 AND atlassian_team_id::text=$2`
		arg = atlassianTeamID
	}
	team, err := scanPlanTeam(s.Pool.QueryRow(ctx, query, planID, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanTeam{}, ErrPlanNotFound
	}
	return team, err
}

func validatePlanTeamTx(ctx context.Context, tx pgx.Tx, workspaceID string, planID int64, team *PlanTeam) error {
	if team.AtlassianTeamID == "" {
		team.Name = strings.TrimSpace(team.Name)
		if team.Name == "" || len([]rune(team.Name)) > 255 {
			return planInvalid("The plan-only team name must be between 1 and 255 characters.")
		}
	}
	if team.PlanningStyle != "Scrum" && team.PlanningStyle != "Kanban" {
		return planInvalid("The planning style must be Scrum or Kanban.")
	}
	if team.SprintLength != nil && *team.SprintLength <= 0 {
		return planInvalid("The sprint length must be a positive number of days.")
	}
	if team.Capacity != nil && *team.Capacity < 0 {
		return planInvalid("The capacity must not be negative.")
	}
	if team.IssueSourceID != nil {
		found, err := exists(ctx, tx, `SELECT 1 FROM plan_issue_sources WHERE plan_id=$1 AND id=$2`, planID, *team.IssueSourceID)
		if err != nil {
			return err
		}
		if !found {
			return planInvalid("The issue source %d is not part of the plan.", *team.IssueSourceID)
		}
	}
	seen := map[string]bool{}
	members := []string{}
	for _, member := range team.MemberIDs {
		if seen[member] {
			continue
		}
		seen[member] = true
		found, err := exists(ctx, tx, `SELECT 1 FROM memberships WHERE workspace_id=$1 AND user_id=$2`, workspaceID, member)
		if err != nil {
			return err
		}
		if !found {
			return planInvalid("The user %s does not exist.", member)
		}
		members = append(members, member)
	}
	team.MemberIDs = members
	return nil
}

// SavePlanTeam adds a team to an active plan or updates one already in it.
func (s *Store) SavePlanTeam(ctx context.Context, workspaceID string, planID int64, team PlanTeam, create bool) (int64, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activePlanTx(ctx, tx, workspaceID, planID); err != nil {
		return 0, err
	}
	if team.AtlassianTeamID != "" {
		team.Name, team.MemberIDs = "", nil
		found, err := exists(ctx, tx, `SELECT 1 FROM atlassian_teams WHERE workspace_id=$1 AND id::text=$2`, workspaceID, team.AtlassianTeamID)
		if err != nil {
			return 0, err
		}
		if !found {
			return 0, ErrPlanNotFound
		}
	}
	if err := validatePlanTeamTx(ctx, tx, workspaceID, planID, &team); err != nil {
		return 0, err
	}
	if team.MemberIDs == nil {
		team.MemberIDs = []string{}
	}
	var name any
	if team.AtlassianTeamID == "" {
		name = team.Name
	}
	var id int64
	if create {
		if team.AtlassianTeamID != "" {
			found, err := exists(ctx, tx, `SELECT 1 FROM plan_teams WHERE plan_id=$1 AND atlassian_team_id::text=$2`, planID, team.AtlassianTeamID)
			if err != nil {
				return 0, err
			}
			if found {
				return 0, planInvalid("The Atlassian team is already in the plan.")
			}
		}
		err = tx.QueryRow(ctx, `INSERT INTO plan_teams(plan_id,atlassian_team_id,name,planning_style,issue_source_id,sprint_length,capacity,member_account_ids)
			VALUES($1,NULLIF($2,'')::uuid,$3,$4,$5,$6,$7,$8) RETURNING id`, planID, team.AtlassianTeamID, name, team.PlanningStyle, team.IssueSourceID, team.SprintLength, team.Capacity, team.MemberIDs).Scan(&id)
	} else {
		err = tx.QueryRow(ctx, `UPDATE plan_teams SET name=$3,planning_style=$4,issue_source_id=$5,sprint_length=$6,capacity=$7,member_account_ids=$8
			WHERE plan_id=$1 AND id=$2 RETURNING id`, planID, team.ID, name, team.PlanningStyle, team.IssueSourceID, team.SprintLength, team.Capacity, team.MemberIDs).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrPlanNotFound
		}
	}
	if err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

func (s *Store) DeletePlanTeam(ctx context.Context, workspaceID string, planID, teamID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := activePlanTx(ctx, tx, workspaceID, planID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM plan_teams WHERE plan_id=$1 AND id=$2`, planID, teamID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPlanNotFound
	}
	return tx.Commit(ctx)
}
