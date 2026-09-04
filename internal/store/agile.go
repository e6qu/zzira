package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/lexorank"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/workflow"
)

var (
	ErrSprintValidation = errors.New("invalid sprint")
	ErrSprintConflict   = errors.New("sprint conflict")
	ErrBoardValidation  = errors.New("invalid board configuration")
)

type SprintUpdate struct {
	Name      string
	Goal      string
	State     string
	StartDate *time.Time
	EndDate   *time.Time
}

type BoardConfigurationUpdate struct {
	QuickFilters     []models.BoardQuickFilter
	SwimlaneStrategy string
	CardFields       []string
	ColumnLimits     map[string]int
}

// SetIssueRank repositions an issue (and optionally moves it to a new status).
// Rank changes materialize but stay out of the changelog.
func (s *Store) SetIssueRank(ctx context.Context, actorID, workspaceID, issueID, rank, newStatusID string) (*models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if newStatusID != "" {
		if _, err := tx.Exec(ctx, `UPDATE issues SET status_id=$3 WHERE id=$1 AND workspace_id=$2`, issueID, workspaceID, newStatusID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE issues SET rank=$3 WHERE id=$1 AND workspace_id=$2`, issueID, workspaceID, rank); err != nil {
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

// RankBetween computes a rank between adjacent issues in a project+status
// column. beforeID is the next issue and afterID is the preceding issue, as
// defined by Jira's rankBeforeIssue and rankAfterIssue API fields.
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
	if afterID != "" {
		if lo, err = fetch(afterID); err != nil {
			return "", fmt.Errorf("rank-after issue %q not found in column", afterID)
		}
	}
	if beforeID != "" {
		if hi, err = fetch(beforeID); err != nil {
			return "", fmt.Errorf("rank-before issue %q not found in column", beforeID)
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
SELECT b.id, b.project_id, p.key, p.name, b.name, b.type, b.column_status_ids, b.filter_jql,
       b.quick_filters, b.swimlane_strategy, b.card_fields, b.column_limits
FROM boards b JOIN projects p ON p.id = b.project_id
`

func scanBoard(row pgx.Row) (*models.Board, error) {
	b := &models.Board{}
	var quickFilters, columnLimits []byte
	err := row.Scan(&b.ID, &b.ProjectID, &b.ProjectKey, &b.ProjectName, &b.Name, &b.Type, &b.ColumnStatusIDs, &b.FilterJQL,
		&quickFilters, &b.SwimlaneStrategy, &b.CardFields, &columnLimits)
	if err != nil {
		return b, err
	}
	if err := json.Unmarshal(quickFilters, &b.QuickFilters); err != nil {
		return b, fmt.Errorf("decode board quick filters: %w", err)
	}
	if err := json.Unmarshal(columnLimits, &b.ColumnLimits); err != nil {
		return b, fmt.Errorf("decode board column limits: %w", err)
	}
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

func boardFilterQuery(board *models.Board, selectedQuickFilters []string, assignee string) (*jql.Query, error) {
	terms := make([]jql.Node, 0, len(selectedQuickFilters)+2)
	if strings.TrimSpace(board.FilterJQL) != "" {
		query, err := jql.Parse(board.FilterJQL)
		if err != nil {
			return nil, fmt.Errorf("%w: board filter: %v", ErrBoardValidation, err)
		}
		terms = append(terms, query.Root)
	}
	quickFilters := make(map[string]models.BoardQuickFilter, len(board.QuickFilters))
	for _, filter := range board.QuickFilters {
		quickFilters[filter.ID] = filter
	}
	seen := map[string]bool{}
	for _, id := range selectedQuickFilters {
		if seen[id] {
			continue
		}
		seen[id] = true
		filter, ok := quickFilters[id]
		if !ok {
			return nil, fmt.Errorf("%w: quick filter %q does not exist", ErrBoardValidation, id)
		}
		query, err := jql.Parse(filter.JQL)
		if err != nil {
			return nil, fmt.Errorf("%w: quick filter %q: %v", ErrBoardValidation, filter.Name, err)
		}
		terms = append(terms, query.Root)
	}
	switch assignee {
	case "":
	case "unassigned":
		terms = append(terms, jql.Clause{Field: "assignee", Op: "empty"})
	default:
		terms = append(terms, jql.Clause{Field: "assignee", Op: "=", Values: []string{assignee}})
	}
	var root jql.Node = jql.Text{Value: ""}
	if len(terms) == 1 {
		root = terms[0]
	} else if len(terms) > 1 {
		root = jql.And{Terms: terms}
	}
	return &jql.Query{Root: root}, nil
}

// BoardIssues returns the board's issues ordered by status column then rank,
// filtered to issues the user may see and by the board's base filter.
func (s *Store) BoardIssues(ctx context.Context, boardID, userID string) (map[string][]*models.Issue, error) {
	return s.BoardIssuesFiltered(ctx, boardID, userID, nil, "")
}

// BoardIssuesFiltered applies selected quick filters and an optional assignee
// on top of the board's base filter. Multiple quick filters are combined with
// AND, matching Jira's board control behavior.
func (s *Store) BoardIssuesFiltered(ctx context.Context, boardID, userID string, selectedQuickFilters []string, assignee string) (map[string][]*models.Issue, error) {
	board, err := s.BoardByID(ctx, boardID)
	if err != nil {
		return nil, err
	}
	query, err := boardFilterQuery(board, selectedQuickFilters, assignee)
	if err != nil {
		return nil, err
	}
	compiled := jql.CompileAt(query, userID, jql.DefaultResolver(), 3)
	if compiled.Err != nil {
		return nil, compiled.Err
	}
	args := []any{board.ProjectID, userID}
	args = append(args, compiled.Args...)
	rows, err := s.Pool.Query(ctx, issueJoin+`
		WHERE i.project_id=$1 AND `+VisibleIssuePredicate("i", "$2")+` AND (`+compiled.Where+`)
		ORDER BY i.rank, i.key`, args...)
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

func validBoardFilterID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func normalizeBoardConfiguration(input BoardConfigurationUpdate, statusIDs []string) (BoardConfigurationUpdate, error) {
	if input.SwimlaneStrategy != "none" && input.SwimlaneStrategy != "assignee" {
		return input, fmt.Errorf("%w: swimlanes must be none or assignee", ErrBoardValidation)
	}
	if len(input.QuickFilters) > 20 {
		return input, fmt.Errorf("%w: boards support at most 20 quick filters", ErrBoardValidation)
	}
	seenFilterIDs := map[string]bool{}
	for index := range input.QuickFilters {
		filter := &input.QuickFilters[index]
		filter.ID = strings.TrimSpace(filter.ID)
		filter.Name = strings.TrimSpace(filter.Name)
		filter.Description = strings.TrimSpace(filter.Description)
		filter.JQL = strings.TrimSpace(filter.JQL)
		if filter.ID == "" {
			filter.ID = NewID("qf")
		}
		if !validBoardFilterID(filter.ID) || seenFilterIDs[filter.ID] {
			return input, fmt.Errorf("%w: quick filter IDs must be unique letters, numbers, dashes, or underscores", ErrBoardValidation)
		}
		seenFilterIDs[filter.ID] = true
		if filter.Name == "" || utf8.RuneCountInString(filter.Name) > 64 {
			return input, fmt.Errorf("%w: quick filter names are required and cannot exceed 64 characters", ErrBoardValidation)
		}
		if filter.JQL == "" || utf8.RuneCountInString(filter.JQL) > 2000 {
			return input, fmt.Errorf("%w: quick filter JQL is required and cannot exceed 2000 characters", ErrBoardValidation)
		}
		if utf8.RuneCountInString(filter.Description) > 255 {
			return input, fmt.Errorf("%w: quick filter descriptions cannot exceed 255 characters", ErrBoardValidation)
		}
		query, err := jql.Parse(filter.JQL)
		if err != nil {
			return input, fmt.Errorf("%w: %s: %v", ErrBoardValidation, filter.Name, err)
		}
		if compiled := jql.Compile(query, "validation-user", jql.DefaultResolver()); compiled.Err != nil {
			return input, fmt.Errorf("%w: %s: %v", ErrBoardValidation, filter.Name, compiled.Err)
		}
		filter.Position = index
	}
	allowedFields := map[string]bool{"priority": true, "assignee": true, "labels": true}
	seenFields := map[string]bool{}
	cardFields := make([]string, 0, len(input.CardFields))
	for _, field := range input.CardFields {
		if !allowedFields[field] {
			return input, fmt.Errorf("%w: unsupported card field %q", ErrBoardValidation, field)
		}
		if seenFields[field] {
			return input, fmt.Errorf("%w: card fields must be unique", ErrBoardValidation)
		}
		seenFields[field] = true
		cardFields = append(cardFields, field)
	}
	if len(cardFields) > 3 {
		return input, fmt.Errorf("%w: cards support at most three optional fields", ErrBoardValidation)
	}
	input.CardFields = cardFields
	allowedStatuses := make(map[string]bool, len(statusIDs))
	for _, id := range statusIDs {
		allowedStatuses[id] = true
	}
	limits := map[string]int{}
	for statusID, limit := range input.ColumnLimits {
		if !allowedStatuses[statusID] {
			return input, fmt.Errorf("%w: a column limit references an unknown status", ErrBoardValidation)
		}
		if limit < 0 || limit > 999 {
			return input, fmt.Errorf("%w: column limits must be between 1 and 999, or 0 for no limit", ErrBoardValidation)
		}
		if limit > 0 {
			limits[statusID] = limit
		}
	}
	input.ColumnLimits = limits
	return input, nil
}

// UpdateBoardConfiguration stores board-level planning controls and emits a
// board action so connected replicas observe the same configuration.
func (s *Store) UpdateBoardConfiguration(ctx context.Context, actorID, workspaceID, boardID string, input BoardConfigurationUpdate) (*models.Board, *models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	board, err := scanBoard(tx.QueryRow(ctx, boardJoin+`WHERE b.id=$1 AND p.workspace_id=$2 FOR UPDATE OF b`, boardID, workspaceID))
	if err != nil {
		return nil, nil, err
	}
	input, err = normalizeBoardConfiguration(input, board.ColumnStatusIDs)
	if err != nil {
		return nil, nil, err
	}
	quickFilters, err := json.Marshal(input.QuickFilters)
	if err != nil {
		return nil, nil, err
	}
	columnLimits, err := json.Marshal(input.ColumnLimits)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE boards
		SET quick_filters=$2, swimlane_strategy=$3, card_fields=$4, column_limits=$5
		WHERE id=$1`, boardID, quickFilters, input.SwimlaneStrategy, input.CardFields, columnLimits); err != nil {
		return nil, nil, err
	}
	board.QuickFilters = input.QuickFilters
	board.SwimlaneStrategy = input.SwimlaneStrategy
	board.CardFields = input.CardFields
	board.ColumnLimits = input.ColumnLimits
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(models.BoardUpsertPayload{Board: *board})
	if err != nil {
		return nil, nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityBoard, EntityID: boardID,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return board, action, nil
}

// ---- sprints ----

func (s *Store) CreateSprint(ctx context.Context, actorID, workspaceID, boardID, name, goal string) (*models.Sprint, *models.Action, error) {
	name = strings.TrimSpace(name)
	goal = strings.TrimSpace(goal)
	if name == "" {
		return nil, nil, fmt.Errorf("%w: a sprint name is required", ErrSprintValidation)
	}
	if utf8.RuneCountInString(name) > 255 {
		return nil, nil, fmt.Errorf("%w: sprint names cannot exceed 255 characters", ErrSprintValidation)
	}
	if utf8.RuneCountInString(goal) > 2000 {
		return nil, nil, fmt.Errorf("%w: sprint goals cannot exceed 2000 characters", ErrSprintValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var boardExists bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM boards b JOIN projects p ON p.id=b.project_id
			WHERE b.id=$1 AND p.workspace_id=$2
		)`, boardID, workspaceID).Scan(&boardExists); err != nil {
		return nil, nil, err
	}
	if !boardExists {
		return nil, nil, fmt.Errorf("board %q does not belong to workspace %q", boardID, workspaceID)
	}
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
			 FROM sprints s WHERE s.board_id=$1 ORDER BY s.created_at, s.id`, boardID)
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

// UpdateSprint changes sprint metadata or advances its lifecycle. Sprint state
// is monotonic: future -> active -> closed. A board may only have one active
// sprint because ZZIRA does not expose Jira's parallel-sprints setting.
func (s *Store) UpdateSprint(ctx context.Context, actorID, workspaceID, sprintID string, input SprintUpdate) (*models.Sprint, *models.Action, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Goal = strings.TrimSpace(input.Goal)
	if input.Name == "" {
		return nil, nil, fmt.Errorf("%w: a sprint name is required", ErrSprintValidation)
	}
	if utf8.RuneCountInString(input.Name) > 255 {
		return nil, nil, fmt.Errorf("%w: sprint names cannot exceed 255 characters", ErrSprintValidation)
	}
	if utf8.RuneCountInString(input.Goal) > 2000 {
		return nil, nil, fmt.Errorf("%w: sprint goals cannot exceed 2000 characters", ErrSprintValidation)
	}
	if input.State != "future" && input.State != "active" && input.State != "closed" {
		return nil, nil, fmt.Errorf("%w: state must be future, active, or closed", ErrSprintValidation)
	}
	if input.State == "active" {
		if input.StartDate == nil || input.EndDate == nil {
			return nil, nil, fmt.Errorf("%w: active sprints require start and end dates", ErrSprintValidation)
		}
		if !input.EndDate.After(*input.StartDate) {
			return nil, nil, fmt.Errorf("%w: the end date must be after the start date", ErrSprintValidation)
		}
	}

	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var boardID, currentState string
	if err := tx.QueryRow(ctx, `
		SELECT s.board_id, s.state
		FROM sprints s
		JOIN boards b ON b.id=s.board_id
		JOIN projects p ON p.id=b.project_id
		WHERE s.id=$1 AND p.workspace_id=$2
		FOR UPDATE OF s, b`, sprintID, workspaceID).Scan(&boardID, &currentState); err != nil {
		return nil, nil, err
	}
	allowed := input.State == currentState ||
		(currentState == "future" && input.State == "active") ||
		(currentState == "active" && input.State == "closed")
	if !allowed {
		return nil, nil, fmt.Errorf("%w: sprint state cannot move from %s to %s", ErrSprintValidation, currentState, input.State)
	}
	if input.State == "active" && currentState != "active" {
		var anotherActive bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM sprints WHERE board_id=$1 AND state='active' AND id<>$2)`,
			boardID, sprintID).Scan(&anotherActive); err != nil {
			return nil, nil, err
		}
		if anotherActive {
			return nil, nil, fmt.Errorf("%w: complete the active sprint before starting another", ErrSprintConflict)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE sprints SET name=$2, goal=$3, state=$4, start_date=$5, end_date=$6
		WHERE id=$1`, sprintID, input.Name, input.Goal, input.State, input.StartDate, input.EndDate); err != nil {
		return nil, nil, err
	}
	updated := &models.Sprint{ID: sprintID, BoardID: boardID, Name: input.Name, Goal: input.Goal, State: input.State}
	if input.StartDate != nil {
		updated.StartDate = input.StartDate.UTC().Format(time.RFC3339)
	}
	if input.EndDate != nil {
		updated.EndDate = input.EndDate.UTC().Format(time.RFC3339)
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(models.SprintUpsertPayload{Sprint: *updated})
	if err != nil {
		return nil, nil, err
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntitySprint, EntityID: sprintID,
		Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return updated, action, nil
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
		`SELECT s.id, s.board_id, s.name, s.state,
		        COALESCE(to_char(s.start_date AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		        COALESCE(to_char(s.end_date AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		        s.goal
		 FROM sprints s `+clause, args...).
		Scan(&sp.ID, &sp.BoardID, &sp.Name, &sp.State, &sp.StartDate, &sp.EndDate, &sp.Goal)
	if err != nil {
		return nil, err
	}
	return sp, nil
}

// NextSprintRank returns a rank after the sprint's current final issue.
func (s *Store) NextSprintRank(ctx context.Context, sprintID string) (string, error) {
	var last string
	err := s.Pool.QueryRow(ctx,
		`SELECT rank FROM sprint_issues WHERE sprint_id=$1 ORDER BY rank DESC LIMIT 1`,
		sprintID).Scan(&last)
	if err != nil && err != pgx.ErrNoRows {
		return "", err
	}
	rank, err := lexorank.Mid(last, "")
	if err != nil {
		return "", fmt.Errorf("sprint rank: %w", err)
	}
	return rank, nil
}

// SprintRankBetween computes an ordering key between two issues already in the
// same sprint. The moving issue may be supplied as excludeID so dropping next
// to itself remains well-defined.
func (s *Store) SprintRankBetween(ctx context.Context, sprintID, beforeID, afterID, excludeID string) (string, error) {
	fetch := func(id string) (string, error) {
		if id == "" || id == excludeID {
			return "", nil
		}
		var rank string
		err := s.Pool.QueryRow(ctx,
			`SELECT rank FROM sprint_issues WHERE sprint_id=$1 AND issue_id=$2`, sprintID, id).Scan(&rank)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("issue %q is not in the sprint", id)
		}
		return rank, err
	}
	lo, err := fetch(afterID)
	if err != nil {
		return "", err
	}
	hi, err := fetch(beforeID)
	if err != nil {
		return "", err
	}
	rank, err := lexorank.Mid(lo, hi)
	if err != nil {
		return "", fmt.Errorf("sprint rank: %w", err)
	}
	return rank, nil
}

// PlanningRankBetween computes the project-wide backlog ordering key. Board
// columns still display this same rank within each status, while backlog
// planning is allowed to compare issues across statuses.
func (s *Store) PlanningRankBetween(ctx context.Context, workspaceID, projectID, beforeID, afterID, excludeID string) (string, error) {
	fetch := func(id string) (string, error) {
		if id == "" || id == excludeID {
			return "", nil
		}
		var rank string
		err := s.Pool.QueryRow(ctx,
			`SELECT rank FROM issues WHERE workspace_id=$1 AND project_id=$2 AND id=$3`,
			workspaceID, projectID, id).Scan(&rank)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("issue %q is not in the project", id)
		}
		return rank, err
	}
	lo, err := fetch(afterID)
	if err != nil {
		return "", err
	}
	hi, err := fetch(beforeID)
	if err != nil {
		return "", err
	}
	rank, err := lexorank.Mid(lo, hi)
	if err != nil {
		return "", fmt.Errorf("backlog rank: %w", err)
	}
	return rank, nil
}

// BacklogIssues lists work not assigned to an active or future sprint. Closed
// sprint membership is historical and does not keep an issue out of backlog.
func (s *Store) BacklogIssues(ctx context.Context, boardID, userID string) ([]*models.Issue, error) {
	board, err := s.BoardByID(ctx, boardID)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, issueJoin+`
		WHERE i.project_id=$1 AND `+VisibleIssuePredicate("i", "$2")+`
		  AND NOT EXISTS (
			SELECT 1 FROM sprint_issues si JOIN sprints s ON s.id=si.sprint_id
			WHERE si.issue_id=i.id AND s.state IN ('future','active')
		  )
		ORDER BY i.rank, i.key`, board.ProjectID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	issues := make([]*models.Issue, 0)
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

func appendSprintMembershipAction(ctx context.Context, tx pgx.Tx, actorID, workspaceID, sprintID, issueID, rank string, removed bool) (*models.Action, error) {
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(models.SprintIssuePayload{SprintID: sprintID, IssueID: issueID, Rank: rank, Removed: removed})
	if err != nil {
		return nil, err
	}
	op := models.OpUpsert
	if removed {
		op = models.OpDelete
	}
	action := &models.Action{
		WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntitySprintIssue, EntityID: sprintID,
		Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
	}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, err
	}
	return action, nil
}

// AddIssueToSprint places an issue at the supplied backlog rank.
func (s *Store) AddIssueToSprint(ctx context.Context, actorID, workspaceID, sprintID, issueID, rank string) (*models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('sprint-issue:' || $1, 0))`, issueID); err != nil {
		return nil, err
	}
	var belongs bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM sprints s
			JOIN boards b ON b.id=s.board_id
			JOIN projects p ON p.id=b.project_id
				JOIN issues i ON i.id=$2 AND i.workspace_id=$3 AND i.project_id=b.project_id
			WHERE s.id=$1 AND p.workspace_id=$3 AND s.state IN ('future','active')
		)`, sprintID, issueID, workspaceID).Scan(&belongs); err != nil {
		return nil, err
	}
	if !belongs {
		return nil, fmt.Errorf("sprint and issue must belong to the same project in workspace %q", workspaceID)
	}
	rows, err := tx.Query(ctx, `
		SELECT si.sprint_id, si.rank
		FROM sprint_issues si JOIN sprints s ON s.id=si.sprint_id
		WHERE si.issue_id=$1 AND s.state IN ('future','active') AND si.sprint_id<>$2
		FOR UPDATE OF si`, issueID, sprintID)
	if err != nil {
		return nil, err
	}
	type previousMembership struct{ sprintID, rank string }
	previous := make([]previousMembership, 0)
	for rows.Next() {
		var membership previousMembership
		if err := rows.Scan(&membership.sprintID, &membership.rank); err != nil {
			rows.Close()
			return nil, err
		}
		previous = append(previous, membership)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, membership := range previous {
		if _, err := tx.Exec(ctx, `DELETE FROM sprint_issues WHERE sprint_id=$1 AND issue_id=$2`, membership.sprintID, issueID); err != nil {
			return nil, err
		}
		if _, err := appendSprintMembershipAction(ctx, tx, actorID, workspaceID, membership.sprintID, issueID, membership.rank, true); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO sprint_issues (sprint_id, issue_id, rank) VALUES ($1,$2,$3)
		 ON CONFLICT (sprint_id, issue_id) DO UPDATE SET rank=$4`, sprintID, issueID, rank, rank); err != nil {
		return nil, err
	}
	action, err := appendSprintMembershipAction(ctx, tx, actorID, workspaceID, sprintID, issueID, rank, false)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return action, nil
}

// RemoveIssueFromPlanning moves an issue back to the backlog by deleting all
// active/future sprint memberships while retaining closed-sprint history.
func (s *Store) RemoveIssueFromPlanning(ctx context.Context, actorID, workspaceID, issueID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('sprint-issue:' || $1, 0))`, issueID); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT si.sprint_id, si.rank
		FROM sprint_issues si
		JOIN sprints s ON s.id=si.sprint_id
		JOIN boards b ON b.id=s.board_id
		JOIN projects p ON p.id=b.project_id
		WHERE si.issue_id=$1 AND p.workspace_id=$2 AND s.state IN ('future','active')
		FOR UPDATE OF si`, issueID, workspaceID)
	if err != nil {
		return err
	}
	type membership struct{ sprintID, rank string }
	memberships := make([]membership, 0)
	for rows.Next() {
		var item membership
		if err := rows.Scan(&item.sprintID, &item.rank); err != nil {
			rows.Close()
			return err
		}
		memberships = append(memberships, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range memberships {
		if _, err := tx.Exec(ctx, `DELETE FROM sprint_issues WHERE sprint_id=$1 AND issue_id=$2`, item.sprintID, issueID); err != nil {
			return err
		}
		if _, err := appendSprintMembershipAction(ctx, tx, actorID, workspaceID, item.sprintID, issueID, item.rank, true); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ---- watchers ----

func (s *Store) AddWatcher(ctx context.Context, actorID, workspaceID, issueID, userID string) (*models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx,
		`INSERT INTO watchers (issue_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		issueID, userID)
	if err != nil {
		return nil, err
	}
	if result.RowsAffected() == 0 {
		return nil, nil
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
	result, err := tx.Exec(ctx, `DELETE FROM watchers WHERE issue_id=$1 AND user_id=$2`, issueID, userID)
	if err != nil {
		return nil, err
	}
	if result.RowsAffected() == 0 {
		return nil, nil
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
	if err := tx.QueryRow(ctx, `
		INSERT INTO notifications (id, workspace_id, user_id, actor_id, kind, entity_type, entity_id, message)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		n.ID, workspaceID, n.TargetUser, n.ActorID, n.Kind, n.EntityType, n.EntityID, n.Message).Scan(&n.Created); err != nil {
		return nil, err
	}
	n.WorkspaceID = workspaceID
	n.Read = false
	n.ReadAt = ""
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
	notifications, _, err := s.NotificationsPageByUser(ctx, workspaceID, userID, false, 0, limit)
	return notifications, err
}

// NotificationsPageByUser returns a stable, private page and applies unread
// filtering in SQL before LIMIT/OFFSET so older unread work cannot disappear
// behind a page of newer read notifications.
func (s *Store) NotificationsPageByUser(ctx context.Context, workspaceID, userID string, unreadOnly bool, startAt, maxResults int) ([]*models.Notification, int, error) {
	unreadClause := ""
	if unreadOnly {
		unreadClause = " AND n.read_at IS NULL"
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications n WHERE n.workspace_id=$1 AND n.user_id=$2`+unreadClause,
		workspaceID, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT n.id, n.user_id, n.actor_id, COALESCE(u.display_name,''), n.kind, n.entity_type, n.entity_id, n.message,
		       to_char(n.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       n.read_at IS NOT NULL,
		       COALESCE(to_char(n.read_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '')
		FROM notifications n LEFT JOIN users u ON u.id = n.actor_id
		WHERE n.workspace_id=$1 AND n.user_id=$2`+unreadClause+`
		ORDER BY n.created_at DESC, n.id DESC LIMIT $3 OFFSET $4`, workspaceID, userID, maxResults, startAt)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*models.Notification
	for rows.Next() {
		n := &models.Notification{}
		if err := rows.Scan(&n.ID, &n.TargetUser, &n.ActorID, &n.ActorName, &n.Kind, &n.EntityType, &n.EntityID, &n.Message, &n.Created, &n.Read, &n.ReadAt); err != nil {
			return nil, 0, err
		}
		out = append(out, n)
	}
	return out, total, rows.Err()
}

// UnreadNotificationCount returns the badge count for one workspace member.
func (s *Store) UnreadNotificationCount(ctx context.Context, workspaceID, userID string) (int, error) {
	var count int
	err := s.Pool.QueryRow(ctx, `
		SELECT count(*) FROM notifications
		WHERE workspace_id=$1 AND user_id=$2 AND read_at IS NULL`, workspaceID, userID).Scan(&count)
	return count, err
}

const notificationByIDQuery = `
	SELECT n.id, n.user_id, n.actor_id, COALESCE(u.display_name,''), n.kind, n.entity_type, n.entity_id, n.message,
	       to_char(n.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	       n.read_at IS NOT NULL,
	       COALESCE(to_char(n.read_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '')
	FROM notifications n LEFT JOIN users u ON u.id=n.actor_id
	WHERE n.workspace_id=$1 AND n.user_id=$2 AND n.id=$3`

func scanNotification(row pgx.Row) (*models.Notification, error) {
	n := &models.Notification{}
	err := row.Scan(&n.ID, &n.TargetUser, &n.ActorID, &n.ActorName, &n.Kind, &n.EntityType, &n.EntityID,
		&n.Message, &n.Created, &n.Read, &n.ReadAt)
	return n, err
}

// NotificationByIDForUser prevents notification IDs from becoming a
// cross-user disclosure primitive.
func (s *Store) NotificationByIDForUser(ctx context.Context, workspaceID, userID, notificationID string) (*models.Notification, error) {
	return scanNotification(s.Pool.QueryRow(ctx, notificationByIDQuery, workspaceID, userID, notificationID))
}

// SetNotificationRead changes one member-owned notification and publishes the
// full current value so every local replica converges on the same read state.
// Repeating the current state is idempotent and does not grow the action log.
func (s *Store) SetNotificationRead(ctx context.Context, workspaceID, userID, notificationID string, read bool) (*models.Notification, *models.Action, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	current, err := scanNotification(tx.QueryRow(ctx, notificationByIDQuery, workspaceID, userID, notificationID))
	if err != nil {
		return nil, nil, err
	}
	if current.Read == read {
		return current, nil, nil
	}
	if read {
		_, err = tx.Exec(ctx, `UPDATE notifications SET read_at=now() WHERE workspace_id=$1 AND user_id=$2 AND id=$3`, workspaceID, userID, notificationID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE notifications SET read_at=NULL WHERE workspace_id=$1 AND user_id=$2 AND id=$3`, workspaceID, userID, notificationID)
	}
	if err != nil {
		return nil, nil, err
	}
	updated, err := scanNotification(tx.QueryRow(ctx, notificationByIDQuery, workspaceID, userID, notificationID))
	if err != nil {
		return nil, nil, err
	}
	payload, err := json.Marshal(models.NotificationPayload{Notification: *updated})
	if err != nil {
		return nil, nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	action := &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityNotification,
		EntityID: updated.ID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: userID}
	if err := appendAction(ctx, tx, action); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return updated, action, nil
}

// MarkAllNotificationsRead records each changed notification as its own
// targeted action, preserving the existing entity-level sync contract.
func (s *Store) MarkAllNotificationsRead(ctx context.Context, workspaceID, userID string) (int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		UPDATE notifications SET read_at=now()
		WHERE workspace_id=$1 AND user_id=$2 AND read_at IS NULL
		RETURNING id`, workspaceID, userID)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, id := range ids {
		notification, err := scanNotification(tx.QueryRow(ctx, notificationByIDQuery, workspaceID, userID, id))
		if err != nil {
			return 0, err
		}
		payload, err := json.Marshal(models.NotificationPayload{Notification: *notification})
		if err != nil {
			return 0, err
		}
		seq, err := nextSeq(ctx, tx, workspaceID)
		if err != nil {
			return 0, err
		}
		action := &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityNotification,
			EntityID: notification.ID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: userID}
		if err := appendAction(ctx, tx, action); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// IssuesBySprint lists a sprint's issues in rank order.
func (s *Store) IssuesBySprint(ctx context.Context, sprintID, userID string) ([]*models.Issue, error) {
	rows, err := s.Pool.Query(ctx, issueJoin+`
		JOIN sprint_issues si ON si.issue_id = i.id
		WHERE si.sprint_id=$1 AND `+VisibleIssuePredicate("i", "$2")+` ORDER BY si.rank`, sprintID, userID)
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
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // no scheme assigned
	}
	if err != nil {
		return nil, err
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
	if errors.Is(err, pgx.ErrNoRows) {
		return workflow.Default(), nil
	}
	if err != nil {
		return workflow.Workflow{}, err
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
	if strings.TrimSpace(wf.ID) == "" || strings.TrimSpace(wf.Name) == "" || len(wf.Transitions) == 0 {
		return fmt.Errorf("workflow id, name, and at least one transition are required")
	}
	statuses, err := s.AllStatuses(ctx)
	if err != nil {
		return err
	}
	knownStatuses := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		knownStatuses[status.ID] = struct{}{}
	}
	transitionIDs := make(map[string]struct{}, len(wf.Transitions))
	for _, transition := range wf.Transitions {
		if strings.TrimSpace(transition.ID) == "" || strings.TrimSpace(transition.Name) == "" || transition.To == "" || len(transition.From) == 0 {
			return fmt.Errorf("every workflow transition requires an id, name, source, and destination")
		}
		if _, duplicate := transitionIDs[transition.ID]; duplicate {
			return fmt.Errorf("workflow transition id %q is duplicated", transition.ID)
		}
		transitionIDs[transition.ID] = struct{}{}
		if _, ok := knownStatuses[transition.To]; !ok {
			return fmt.Errorf("workflow transition %q has unknown destination status %q", transition.ID, transition.To)
		}
		for _, from := range transition.From {
			if _, ok := knownStatuses[from]; !ok {
				return fmt.Errorf("workflow transition %q has unknown source status %q", transition.ID, from)
			}
		}
	}
	def, err := json.Marshal(wf)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx,
		`INSERT INTO workflows (id, name, def) VALUES ($1,$2,$3)
		 ON CONFLICT (id) DO UPDATE SET name=$2, def=$3`, wf.ID, wf.Name, def)
	return err
}

// WorkflowByID returns one stored workflow definition.
func (s *Store) WorkflowByID(ctx context.Context, id string) (workflow.Workflow, error) {
	var wf workflow.Workflow
	var def []byte
	err := s.Pool.QueryRow(ctx, `SELECT def FROM workflows WHERE id=$1`, id).Scan(&def)
	if err != nil {
		return wf, err
	}
	if err := json.Unmarshal(def, &wf); err != nil {
		return workflow.Workflow{}, err
	}
	return wf, nil
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
	result, err := s.Pool.Exec(ctx, `UPDATE projects SET workflow_id=$2 WHERE id=$1`, projectID, workflowID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("project %q does not exist", projectID)
	}
	return nil
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
