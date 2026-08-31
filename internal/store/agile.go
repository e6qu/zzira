package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/lexorank"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/workflow"
)

// SetIssueRank repositions an issue (and optionally moves it to a new status).
// Rank changes materialize but stay out of the changelog.
func (s *Store) SetIssueRank(ctx context.Context, actorID, workspaceID, issueID, rank, newStatusID string) (*models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if newStatusID != "" {
		if _, err := tx.Exec(ctx, `UPDATE issues SET status_id=$2 WHERE id=$1`, issueID, newStatusID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE issues SET rank=$2 WHERE id=$1`, issueID, rank); err != nil {
		return nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	issue, err := scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.workspace_id=$1 AND i.id=$2`, workspaceID, issueID))
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(models.IssueUpdatePayload{Issue: *issue})
	if err != nil {
		return nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssue, EntityID: issueID,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return action, nil
}

// RankBetween computes the rank between two issues' current ranks (either may
// be absent) inside a project+status column.
func (s *Store) RankBetween(ctx context.Context, workspaceID, projectID, statusID, beforeID, afterID string) (string, error) {
	fetch := func(id string) (string, error) {
		var rank string
		err := s.Pool.QueryRow(ctx,
			`SELECT rank FROM issues WHERE workspace_id=$1 AND project_id=$2 AND status_id=$3 AND id=$4`,
			workspaceID, projectID, statusID, id).Scan(&rank)
		return rank, err
	}
	var lo, hi string
	var err error
	if beforeID != "" {
		if lo, err = fetch(beforeID); err != nil {
			return "", fmt.Errorf("rank-before issue %q not found in column", beforeID)
		}
	}
	if afterID != "" {
		if hi, err = fetch(afterID); err != nil {
			return "", fmt.Errorf("rank-after issue %q not found in column", afterID)
		}
	}
	rank, err := lexorank.Mid(lo, hi)
	if err != nil {
		return "", fmt.Errorf("rank: %w", err)
	}
	return rank, nil
}

// ---- boards ----

const boardJoin = `
SELECT b.id, b.project_id, p.key, b.name, b.type, b.column_status_ids, b.filter_jql
FROM boards b JOIN projects p ON p.id = b.project_id
`

func scanBoard(row pgx.Row) (*models.Board, error) {
	b := &models.Board{}
	err := row.Scan(&b.ID, &b.ProjectID, &b.ProjectKey, &b.Name, &b.Type, &b.ColumnStatusIDs, &b.FilterJQL)
	return b, err
}

func (s *Store) BoardByID(ctx context.Context, id string) (*models.Board, error) {
	return scanBoard(s.Pool.QueryRow(ctx, boardJoin+`WHERE b.id=$1`, id))
}

// BoardByIDInWorkspace returns a board only when its project belongs to the
// requested workspace. Callers handling authenticated requests must use this
// lookup instead of the global administrative lookup above.
func (s *Store) BoardByIDInWorkspace(ctx context.Context, workspaceID, id string) (*models.Board, error) {
	return scanBoard(s.Pool.QueryRow(ctx, boardJoin+`WHERE b.id=$1 AND p.workspace_id=$2`, id, workspaceID))
}

func (s *Store) BoardsByWorkspace(ctx context.Context, workspaceID string) ([]*models.Board, error) {
	rows, err := s.Pool.Query(ctx, boardJoin+`WHERE p.workspace_id=$1 ORDER BY b.name`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Board
	for rows.Next() {
		b, err := scanBoard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BoardIssues returns the board's issues ordered by status column then rank,
// filtered to issues the user may see.
func (s *Store) BoardIssues(ctx context.Context, boardID, userID string) (map[string][]*models.Issue, error) {
	board, err := s.BoardByID(ctx, boardID)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, issueJoin+`
		WHERE i.project_id=$1 AND `+VisibleIssuePredicate("i", "$2")+` ORDER BY i.rank`, board.ProjectID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]*models.Issue{}
	for _, st := range board.ColumnStatusIDs {
		out[st] = []*models.Issue{}
	}
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		if _, ok := out[i.Status.ID]; ok {
			out[i.Status.ID] = append(out[i.Status.ID], i)
		}
	}
	return out, rows.Err()
}

// ---- sprints ----

func (s *Store) CreateSprint(ctx context.Context, actorID, workspaceID, boardID, name, goal string) (*models.Sprint, *models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id := NewID("spr")
	if _, err := tx.Exec(ctx,
		`INSERT INTO sprints (id, board_id, name, state, goal) VALUES ($1,$2,$3,'future',$4)`,
		id, boardID, name, goal); err != nil {
		return nil, nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	sprint := &models.Sprint{ID: id, BoardID: boardID, Name: name, State: "future", Goal: goal}
	payload, err := json.Marshal(models.SprintUpsertPayload{Sprint: *sprint})
	if err != nil {
		return nil, nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntitySprint, EntityID: id,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return sprint, action, nil
}

func (s *Store) SprintsByBoard(ctx context.Context, boardID string) ([]*models.Sprint, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT s.id, s.board_id, s.name, s.state,
		        COALESCE(to_char(start_date AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		        COALESCE(to_char(end_date AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		        s.goal
		 FROM sprints WHERE board_id=$1 ORDER BY created_at, id`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Sprint
	for rows.Next() {
		sp := &models.Sprint{}
		if err := rows.Scan(&sp.ID, &sp.BoardID, &sp.Name, &sp.State, &sp.StartDate, &sp.EndDate, &sp.Goal); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

func (s *Store) SprintByID(ctx context.Context, id string) (*models.Sprint, error) {
	return s.sprintByID(ctx, "WHERE s.id=$1", id)
}

// SprintByIDInWorkspace returns a sprint only when its board's project belongs
// to the requested workspace.
func (s *Store) SprintByIDInWorkspace(ctx context.Context, workspaceID, id string) (*models.Sprint, error) {
	return s.sprintByID(ctx, "JOIN boards b ON b.id=s.board_id JOIN projects p ON p.id=b.project_id WHERE s.id=$1 AND p.workspace_id=$2", id, workspaceID)
}

func (s *Store) sprintByID(ctx context.Context, clause string, args ...any) (*models.Sprint, error) {
	sp := &models.Sprint{}
	err := s.Pool.QueryRow(ctx,
		`SELECT id, board_id, name, state,
		        COALESCE(to_char(start_date AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		        COALESCE(to_char(end_date AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		        goal
		 FROM sprints s `+clause, args...).
		Scan(&sp.ID, &sp.BoardID, &sp.Name, &sp.State, &sp.StartDate, &sp.EndDate, &sp.Goal)
	if err != nil {
		return nil, err
	}
	return sp, nil
}

// AddIssueToSprint places an issue in a sprint at the top of the backlog order.
func (s *Store) AddIssueToSprint(ctx context.Context, actorID, workspaceID, sprintID, issueID, rank string) (*models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`INSERT INTO sprint_issues (sprint_id, issue_id, rank) VALUES ($1,$2,$3)
		 ON CONFLICT (sprint_id, issue_id) DO UPDATE SET rank=$4`, sprintID, issueID, rank, rank); err != nil {
		return nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(models.SprintIssuePayload{SprintID: sprintID, IssueID: issueID, Rank: rank})
	if err != nil {
		return nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntitySprintIssue, EntityID: sprintID,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return action, nil
}

// ---- watchers ----

func (s *Store) AddWatcher(ctx context.Context, actorID, workspaceID, issueID, userID string) (*models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`INSERT INTO watchers (issue_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		issueID, userID); err != nil {
		return nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(models.WatcherPayload{IssueID: issueID, AccountID: userID})
	if err != nil {
		return nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityWatcher, EntityID: issueID,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return action, nil
}

func (s *Store) RemoveWatcher(ctx context.Context, actorID, workspaceID, issueID, userID string) (*models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM watchers WHERE issue_id=$1 AND user_id=$2`, issueID, userID); err != nil {
		return nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(models.WatcherPayload{IssueID: issueID, AccountID: userID})
	if err != nil {
		return nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityWatcher, EntityID: issueID,
		Op: models.OpDelete, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return action, nil
}

func (s *Store) WatchersByIssue(ctx context.Context, issueID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT user_id FROM watchers WHERE issue_id=$1 ORDER BY created_at`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---- notifications ----

// CreateNotification records a per-user notification and emits its action.
func (s *Store) CreateNotification(ctx context.Context, workspaceID string, n *models.Notification) (*models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO notifications (id, workspace_id, user_id, actor_id, kind, entity_type, entity_id, message)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		n.ID, workspaceID, n.TargetUser, n.ActorID, n.Kind, n.EntityType, n.EntityID, n.Message); err != nil {
		return nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(models.NotificationPayload{Notification: *n})
	if err != nil {
		return nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityNotification, EntityID: n.ID,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: n.ActorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return action, nil
}

// NotificationsByUser lists a user's notifications, newest first.
func (s *Store) NotificationsByUser(ctx context.Context, workspaceID, userID string, limit int) ([]*models.Notification, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT n.id, n.user_id, n.actor_id, COALESCE(u.display_name,''), n.kind, n.entity_type, n.entity_id, n.message,
		       to_char(n.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM notifications n LEFT JOIN users u ON u.id = n.actor_id
		WHERE n.workspace_id=$1 AND n.user_id=$2
		ORDER BY n.created_at DESC LIMIT $3`, workspaceID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Notification
	for rows.Next() {
		n := &models.Notification{}
		if err := rows.Scan(&n.ID, &n.TargetUser, &n.ActorID, &n.ActorName, &n.Kind, &n.EntityType, &n.EntityID, &n.Message, &n.Created); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// IssuesBySprint lists a sprint's issues in rank order.
func (s *Store) IssuesBySprint(ctx context.Context, sprintID string) ([]*models.Issue, error) {
	rows, err := s.Pool.Query(ctx, issueJoin+`
		JOIN sprint_issues si ON si.issue_id = i.id
		WHERE si.sprint_id=$1 ORDER BY si.rank`, sprintID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Issue
	for rows.Next() {
		i, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// IsAdmin reports the workspace admin role.
func (s *Store) IsAdmin(ctx context.Context, workspaceID, userID string) (bool, error) {
	var role string
	err := s.Pool.QueryRow(ctx,
		`SELECT role FROM memberships WHERE workspace_id=$1 AND user_id=$2`, workspaceID, userID).Scan(&role)
	if err != nil {
		return false, err
	}
	return role == "admin", nil
}

// SecuritySchemeForProject resolves the project's security scheme (nil = none).
func (s *Store) SecuritySchemeForProject(ctx context.Context, projectID string) (*models.SecurityScheme, error) {
	var id, name string
	var levels []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT ss.id, ss.name, ss.levels
		FROM projects p JOIN security_schemes ss ON ss.id = p.security_scheme_id
		WHERE p.id=$1`, projectID).Scan(&id, &name, &levels)
	if err != nil {
		return nil, nil // no scheme assigned
	}
	scheme := &models.SecurityScheme{ID: id, Name: name}
	if err := json.Unmarshal(levels, &scheme.Levels); err != nil {
		return nil, err
	}
	return scheme, nil
}

// SecurityLevelName resolves a level's display name within the project scheme.
func (s *Store) SecurityLevelName(ctx context.Context, projectID, levelID string) string {
	var name string
	err := s.Pool.QueryRow(ctx, `
		SELECT lvl->>'name'
		FROM projects p
		JOIN security_schemes ss ON ss.id = p.security_scheme_id
		, jsonb_array_elements(ss.levels) lvl
		WHERE p.id=$1 AND lvl->>'id'=$2`, projectID, levelID).Scan(&name)
	if err != nil {
		return ""
	}
	return name
}

// FirstAdminID returns any workspace admin (webhook JQL evaluation).
func (s *Store) FirstAdminID(ctx context.Context, workspaceID string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `
		SELECT user_id FROM memberships WHERE workspace_id=$1 AND role='admin' ORDER BY user_id LIMIT 1`,
		workspaceID).Scan(&id)
	return id, err
}

// WorkflowForProject resolves the project's workflow, Default when unassigned
// or the stored def is unusable.
func (s *Store) WorkflowForProject(ctx context.Context, projectID string) (workflow.Workflow, error) {
	var def []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT w.def FROM projects p JOIN workflows w ON w.id = p.workflow_id
		WHERE p.id=$1`, projectID).Scan(&def)
	if err != nil {
		return workflow.Default(), nil
	}
	var wf workflow.Workflow
	if err := json.Unmarshal(def, &wf); err != nil {
		return workflow.Default(), fmt.Errorf("workflow def for project %s: %w", projectID, err)
	}
	if len(wf.Transitions) == 0 {
		return workflow.Default(), nil
	}
	return wf, nil
}

// CreateWorkflow stores a workflow definition.
func (s *Store) CreateWorkflow(ctx context.Context, wf workflow.Workflow) error {
	def, err := json.Marshal(wf)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx,
		`INSERT INTO workflows (id, name, def) VALUES ($1,$2,$3)
		 ON CONFLICT (id) DO UPDATE SET name=$2, def=$3`, wf.ID, wf.Name, def)
	return err
}

// ListWorkflows returns all stored workflow definitions.
func (s *Store) ListWorkflows(ctx context.Context) ([]workflow.Workflow, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, def FROM workflows ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workflow.Workflow
	for rows.Next() {
		var wf workflow.Workflow
		var def []byte
		if err := rows.Scan(&wf.ID, &wf.Name, &def); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(def, &wf); err != nil {
			return nil, err
		}
		out = append(out, wf)
	}
	return out, rows.Err()
}

// AssignWorkflowToProject points a project at a stored workflow.
func (s *Store) AssignWorkflowToProject(ctx context.Context, projectID, workflowID string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE projects SET workflow_id=$2 WHERE id=$1`, projectID, workflowID)
	return err
}

// CreateSecurityScheme upserts a security scheme definition.
func (s *Store) CreateSecurityScheme(ctx context.Context, scheme models.SecurityScheme) error {
	levels, err := json.Marshal(scheme.Levels)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `
		INSERT INTO security_schemes (id, name, levels) VALUES ($1,$2,$3)
		ON CONFLICT (id) DO UPDATE SET name=$2, levels=$3`, scheme.ID, scheme.Name, levels)
	return err
}

// AssignSecurityScheme points a project at a security scheme.
func (s *Store) AssignSecurityScheme(ctx context.Context, projectID, schemeID string) error {
	var exists bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM security_schemes WHERE id=$1)`, schemeID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("security scheme %q does not exist", schemeID)
	}
	_, err := s.Pool.Exec(ctx, `UPDATE projects SET security_scheme_id=$2 WHERE id=$1`, projectID, schemeID)
	return err
}
