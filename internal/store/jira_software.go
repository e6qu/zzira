package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/lexorank"
	"github.com/e6qu/zzira/internal/models"
)

// ---- epics ----

// EpicDetails are the epic-only facts Jira Software keeps beside the issue.
type EpicDetails struct {
	Name     string // empty: the epic's summary
	ColorKey string // empty: the epic's default color
	Done     bool
}

// EpicUpdate changes the fields that are present.
type EpicUpdate struct {
	Name     *string
	ColorKey *string
	Done     *bool
}

func (s *Store) EpicDetailsForIssues(ctx context.Context, workspaceID string, issueIDs []string) (map[string]EpicDetails, error) {
	out := make(map[string]EpicDetails, len(issueIDs))
	if len(issueIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT issue_id,COALESCE(name,''),COALESCE(color_key,''),done FROM issue_epics WHERE workspace_id=$1 AND issue_id=ANY($2)`, workspaceID, issueIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var issueID string
		var details EpicDetails
		if err := rows.Scan(&issueID, &details.Name, &details.ColorKey, &details.Done); err != nil {
			return nil, err
		}
		out[issueID] = details
	}
	return out, rows.Err()
}

func (s *Store) UpdateEpicDetails(ctx context.Context, workspaceID, issueID string, update EpicUpdate) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO issue_epics(issue_id,workspace_id,name,color_key,done)
		VALUES($1,$2,$3,$4,COALESCE($5::boolean,FALSE))
		ON CONFLICT (issue_id) DO UPDATE SET
		  name=CASE WHEN $6 THEN EXCLUDED.name ELSE issue_epics.name END,
		  color_key=CASE WHEN $7 THEN EXCLUDED.color_key ELSE issue_epics.color_key END,
		  done=COALESCE($5::boolean,issue_epics.done),
		  updated_at=now()`,
		issueID, workspaceID, update.Name, update.ColorKey, update.Done, update.Name != nil, update.ColorKey != nil)
	return err
}

// EpicsInProjects lists the epics of the given projects the user can see, in
// rank order.
func (s *Store) EpicsInProjects(ctx context.Context, workspaceID, userID string, projectIDs []string) ([]*models.Issue, error) {
	rows, err := s.Pool.Query(ctx, searchSelect+" "+searchJoin+`
		WHERE i.workspace_id=$1 AND i.project_id=ANY($2) AND it.hierarchy_level=1 AND `+VisibleIssuePredicate("i", "$3")+`
		ORDER BY i.rank, i.key`, workspaceID, projectIDs, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	issues := []*models.Issue{}
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, rows.Err()
}

// unlinkEpicChildren clears the parent of an epic's standard issues, recording
// each change so replicas follow.
func unlinkEpicChildren(ctx context.Context, tx pgx.Tx, actorID, workspaceID, epicID string) error {
	rows, err := tx.Query(ctx, `
		SELECT child.id FROM issues child JOIN issue_types child_type ON child_type.id=child.issuetype_id
		WHERE child.workspace_id=$1 AND child.parent_id=$2 AND NOT child_type.subtask ORDER BY child.id`, workspaceID, epicID)
	if err != nil {
		return err
	}
	var childIDs []string
	for rows.Next() {
		var childID string
		if err := rows.Scan(&childID); err != nil {
			rows.Close()
			return err
		}
		childIDs = append(childIDs, childID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, childID := range childIDs {
		if _, err := tx.Exec(ctx, `UPDATE issues SET parent_id=NULL WHERE id=$1 AND workspace_id=$2`, childID, workspaceID); err != nil {
			return err
		}
		child, err := scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.workspace_id=$1 AND i.id=$2`, workspaceID, childID))
		if err != nil {
			return err
		}
		payload, err := json.Marshal(models.IssueUpdatePayload{Issue: *child})
		if err != nil {
			return err
		}
		seq, err := nextSeq(ctx, tx, workspaceID)
		if err != nil {
			return err
		}
		if err := appendAction(ctx, tx, &models.Action{
			WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssue, EntityID: childID,
			Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ---- scoped search ----

// SearchScoped runs a compiled JQL query inside an extra SQL scope over the
// search columns. The scope names its own arguments {1}, {2}, ... in order.
// Without an ORDER BY the issues come back in rank order.
func (s *Store) SearchScoped(ctx context.Context, workspaceID, userID string, c jql.Compiled, scope string, scopeArgs []any, limit, offset int) ([]*models.Issue, int, error) {
	if c.Err != nil {
		return nil, 0, c.Err
	}
	where := "i.workspace_id = $1"
	args := []any{workspaceID}
	if c.Where != "" {
		where += " AND (" + c.Where + ")"
		args = append(args, c.Args...)
	}
	args = append(args, userID)
	where += " AND " + VisibleIssuePredicate("i", fmt.Sprintf("$%d", len(args)))
	order := strings.TrimSpace(c.OrderSQL)
	if order == "" {
		order = "i.rank, i.key"
	}
	if scope != "" {
		// Scope arguments may also be used by an order the caller gives, such
		// as a sprint's own rank.
		for index := len(scopeArgs) - 1; index >= 0; index-- {
			placeholder, parameter := fmt.Sprintf("{%d}", index+1), fmt.Sprintf("$%d", len(args)+index+1)
			scope = strings.ReplaceAll(scope, placeholder, parameter)
			order = strings.ReplaceAll(order, placeholder, parameter)
		}
		args = append(args, scopeArgs...)
		where += " AND (" + scope + ")"
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT COUNT(*) `+searchJoin+` WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, searchSelect+" "+searchJoin+" WHERE "+where+" ORDER BY "+order+fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	issues := []*models.Issue{}
	for rows.Next() {
		issue, err := scanIssue(rows)
		if err != nil {
			return nil, 0, err
		}
		issues = append(issues, issue)
	}
	return issues, total, rows.Err()
}

// ---- global rank ----

// RankAround computes the rank that puts an issue immediately before beforeID
// or immediately after afterID in Jira's single, site-wide rank order.
func (s *Store) RankAround(ctx context.Context, workspaceID, issueID, beforeID, afterID string) (string, error) {
	reference := beforeID
	if reference == "" {
		reference = afterID
	}
	var referenceRank string
	if err := s.Pool.QueryRow(ctx, `SELECT rank FROM issues WHERE workspace_id=$1 AND id=$2`, workspaceID, reference).Scan(&referenceRank); err != nil {
		return "", fmt.Errorf("rank reference %q not found", reference)
	}
	var lo, hi string
	if beforeID != "" {
		hi = referenceRank
		if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(max(rank),'') FROM issues WHERE workspace_id=$1 AND rank<$2 AND id<>$3`, workspaceID, referenceRank, issueID).Scan(&lo); err != nil {
			return "", err
		}
	} else {
		lo = referenceRank
		if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(min(rank),'') FROM issues WHERE workspace_id=$1 AND rank>$2 AND id<>$3`, workspaceID, referenceRank, issueID).Scan(&hi); err != nil {
			return "", err
		}
	}
	return lexorank.Mid(lo, hi)
}

// ---- boards ----

// BoardCreate describes a board Jira creates from a saved filter in a project.
type BoardCreate struct {
	Name      string
	Type      string
	ProjectID string
	Filter    *models.Filter
}

func (s *Store) CreateBoard(ctx context.Context, actorID, workspaceID string, input BoardCreate) (*models.Board, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) >= 255 {
		return nil, fmt.Errorf("%w: name must be less than 255 characters", ErrBoardValidation)
	}
	if input.Type != "scrum" && input.Type != "kanban" {
		return nil, fmt.Errorf("%w: type must be scrum or kanban", ErrBoardValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var projectKey string
	if err := tx.QueryRow(ctx, `SELECT key FROM projects WHERE id=$1 AND workspace_id=$2 AND lifecycle_state='ACTIVE'`, input.ProjectID, workspaceID).Scan(&projectKey); err != nil {
		return nil, fmt.Errorf("%w: the board location project does not exist", ErrBoardValidation)
	}
	filterJQL := "project = " + projectKey
	var sourceFilterID any
	if input.Filter != nil {
		filterJQL = input.Filter.JQL
		// A board's own filter is not a saved filter row.
		if !strings.HasPrefix(input.Filter.ID, "board-filter:") {
			sourceFilterID = input.Filter.ID
		}
	}
	boardID := NewID("brd")
	if _, err := tx.Exec(ctx, `INSERT INTO boards(id,project_id,name,type,filter_jql,source_filter_id,created_by) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		boardID, input.ProjectID, name, input.Type, filterJQL, sourceFilterID, actorID); err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: a board with this name already exists in the project", ErrBoardValidation)
		}
		return nil, err
	}
	board, err := scanBoard(tx.QueryRow(ctx, boardJoin+`WHERE b.id=$1`, boardID))
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(models.BoardUpsertPayload{Board: *board})
	if err != nil {
		return nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	if err := appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityBoard, EntityID: board.ID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}); err != nil {
		return nil, err
	}
	return board, tx.Commit(ctx)
}

// DeleteBoard removes a board with its sprints; the issues stay.
func (s *Store) DeleteBoard(ctx context.Context, actorID, workspaceID, boardID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	board, err := scanBoard(tx.QueryRow(ctx, boardJoin+`WHERE b.id=$1 AND p.workspace_id=$2 FOR UPDATE OF b`, boardID, workspaceID))
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT id FROM sprints WHERE board_id=$1 ORDER BY id`, board.ID)
	if err != nil {
		return err
	}
	var sprintIDs []string
	for rows.Next() {
		var sprintID string
		if err := rows.Scan(&sprintID); err != nil {
			rows.Close()
			return err
		}
		sprintIDs = append(sprintIDs, sprintID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM boards WHERE id=$1`, board.ID); err != nil {
		return err
	}
	payload, err := json.Marshal(models.DeletePayload{Reason: "board deleted"})
	if err != nil {
		return err
	}
	emit := func(entityType, entityID string) error {
		seq, err := nextSeq(ctx, tx, workspaceID)
		if err != nil {
			return err
		}
		return appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: entityType, EntityID: entityID, Op: models.OpDelete, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID})
	}
	for _, sprintID := range sprintIDs {
		if err := emit(models.EntitySprint, sprintID); err != nil {
			return err
		}
	}
	if err := emit(models.EntityBoard, board.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// BoardsUsingFilter lists the boards whose filter is the given filter id:
// boards created from that saved filter and the board that owns it.
func (s *Store) BoardsUsingFilter(ctx context.Context, workspaceID, filterID string) ([]*models.Board, error) {
	rows, err := s.Pool.Query(ctx, boardJoin+`WHERE p.workspace_id=$1 AND (sf.jira_id::text=$2 OR b.filter_jira_id::text=$2) ORDER BY b.name, b.jira_id`, workspaceID, filterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	boards := []*models.Board{}
	for rows.Next() {
		board, err := scanBoard(rows)
		if err != nil {
			return nil, err
		}
		boards = append(boards, board)
	}
	return boards, rows.Err()
}

// ---- DevOps provider modules ----

// ErrProviderEntityNotFound reports provider data Jira does not hold.
var ErrProviderEntityNotFound = errors.New("provider entity not found")

// ProviderEntity is one submitted entity of a DevOps provider module.
type ProviderEntity struct {
	Type           string
	ID             string
	UpdateSequence int64
	Properties     map[string]string
	IssueIDs       []string
	Payload        json.RawMessage
}

// UpsertProviderEntities stores submitted entities. Stored data is replaced
// only when its update sequence is lower than the submitted one.
func (s *Store) UpsertProviderEntities(ctx context.Context, workspaceID, module string, entities []ProviderEntity) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, entity := range entities {
		properties := entity.Properties
		if properties == nil {
			properties = map[string]string{}
		}
		encoded, err := json.Marshal(properties)
		if err != nil {
			return err
		}
		issueIDs := entity.IssueIDs
		if issueIDs == nil {
			issueIDs = []string{}
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO software_provider_entities(workspace_id,module,entity_type,entity_id,update_sequence,properties,issue_ids,payload)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (workspace_id,module,entity_type,entity_id) DO UPDATE SET
			  update_sequence=EXCLUDED.update_sequence, properties=EXCLUDED.properties,
			  issue_ids=EXCLUDED.issue_ids, payload=EXCLUDED.payload, updated_at=now()
			WHERE software_provider_entities.update_sequence<EXCLUDED.update_sequence`,
			workspaceID, module, entity.Type, entity.ID, entity.UpdateSequence, encoded, issueIDs, []byte(entity.Payload)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ProviderEntityRecord returns a stored entity with the issues it is
// associated with.
func (s *Store) ProviderEntityRecord(ctx context.Context, workspaceID, module, entityType, entityID string) (ProviderEntity, error) {
	entity := ProviderEntity{Type: entityType, ID: entityID}
	var properties []byte
	err := s.Pool.QueryRow(ctx, `
		SELECT update_sequence,properties,issue_ids,payload FROM software_provider_entities
		WHERE workspace_id=$1 AND module=$2 AND entity_type=$3 AND entity_id=$4`,
		workspaceID, module, entityType, entityID).Scan(&entity.UpdateSequence, &properties, &entity.IssueIDs, &entity.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity, ErrProviderEntityNotFound
	}
	if err != nil {
		return entity, err
	}
	return entity, json.Unmarshal(properties, &entity.Properties)
}

func (s *Store) DeleteProviderEntity(ctx context.Context, workspaceID, module, entityType, entityID string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM software_provider_entities WHERE workspace_id=$1 AND module=$2 AND entity_type=$3 AND entity_id=$4`, workspaceID, module, entityType, entityID)
	return err
}

// DeleteProviderEntitiesByProperties deletes a module's entities carrying every
// given property.
func (s *Store) DeleteProviderEntitiesByProperties(ctx context.Context, workspaceID, module string, properties map[string]string) error {
	encoded, err := json.Marshal(properties)
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `DELETE FROM software_provider_entities WHERE workspace_id=$1 AND module=$2 AND properties @> $3::jsonb`, workspaceID, module, encoded)
	return err
}

// ProviderEntitiesForIssue lists a module's stored entities associated with an
// issue, oldest first.
func (s *Store) ProviderEntitiesForIssue(ctx context.Context, workspaceID, module, issueID string) ([]ProviderEntity, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT entity_type,entity_id,update_sequence,issue_ids,payload FROM software_provider_entities
		WHERE workspace_id=$1 AND module=$2 AND $3=ANY(issue_ids) ORDER BY updated_at,entity_id`, workspaceID, module, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entities := []ProviderEntity{}
	for rows.Next() {
		var entity ProviderEntity
		if err := rows.Scan(&entity.Type, &entity.ID, &entity.UpdateSequence, &entity.IssueIDs, &entity.Payload); err != nil {
			return nil, err
		}
		entities = append(entities, entity)
	}
	return entities, rows.Err()
}

// LinkedProviderWorkspace is a provider workspace linked to the site.
type LinkedProviderWorkspace struct {
	ID        string
	UpdatedAt time.Time
}

func (s *Store) LinkProviderWorkspaces(ctx context.Context, workspaceID, module string, ids []string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO software_linked_workspaces(workspace_id,module,provider_workspace_id)
		SELECT $1,$2,unnest($3::text[])
		ON CONFLICT (workspace_id,module,provider_workspace_id) DO UPDATE SET updated_at=now()`, workspaceID, module, ids)
	return err
}

func (s *Store) UnlinkProviderWorkspaces(ctx context.Context, workspaceID, module string, ids []string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM software_linked_workspaces WHERE workspace_id=$1 AND module=$2 AND provider_workspace_id=ANY($3)`, workspaceID, module, ids)
	return err
}

func (s *Store) LinkedProviderWorkspaces(ctx context.Context, workspaceID, module string) ([]LinkedProviderWorkspace, error) {
	rows, err := s.Pool.Query(ctx, `SELECT provider_workspace_id,updated_at FROM software_linked_workspaces WHERE workspace_id=$1 AND module=$2 ORDER BY provider_workspace_id`, workspaceID, module)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	linked := []LinkedProviderWorkspace{}
	for rows.Next() {
		var workspace LinkedProviderWorkspace
		if err := rows.Scan(&workspace.ID, &workspace.UpdatedAt); err != nil {
			return nil, err
		}
		linked = append(linked, workspace)
	}
	return linked, rows.Err()
}

// SprintsForIssues returns each issue's sprints, oldest first.
func (s *Store) SprintsForIssues(ctx context.Context, issueIDs []string) (map[string][]*models.Sprint, error) {
	out := make(map[string][]*models.Sprint, len(issueIDs))
	if len(issueIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT si.issue_id, s.id, s.board_id, s.name, s.state,
		       COALESCE(to_char(s.start_date AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		       COALESCE(to_char(s.end_date AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		       s.goal, s.jira_id, b.jira_id,
		       COALESCE(to_char(s.activated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		       COALESCE(to_char(s.completed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),
		        to_char(s.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM sprint_issues si JOIN sprints s ON s.id=si.sprint_id JOIN boards b ON b.id=s.board_id
		WHERE si.issue_id=ANY($1)
		ORDER BY si.issue_id, s.created_at, s.jira_id`, issueIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var issueID string
		sprint := &models.Sprint{}
		if err := rows.Scan(&issueID, &sprint.ID, &sprint.BoardID, &sprint.Name, &sprint.State, &sprint.StartDate, &sprint.EndDate, &sprint.Goal, &sprint.JiraID, &sprint.BoardJiraID, &sprint.ActivatedDate, &sprint.CompleteDate, &sprint.CreatedDate); err != nil {
			return nil, err
		}
		out[issueID] = append(out[issueID], sprint)
	}
	return out, rows.Err()
}

// EpicIDs reports which of the given issues are epics.
func (s *Store) EpicIDs(ctx context.Context, issueIDs []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(issueIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT i.id FROM issues i JOIN issue_types t ON t.id=i.issuetype_id WHERE i.id=ANY($1) AND t.hierarchy_level=1`, issueIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ActiveSprintForBoard returns the board's active sprint, or nil when it has none.
func (s *Store) ActiveSprintForBoard(ctx context.Context, boardID string) (*models.Sprint, error) {
	sprint, err := s.sprintByID(ctx, "WHERE s.board_id=$1 AND s.state='active' ORDER BY s.start_date NULLS LAST, s.jira_id LIMIT 1", boardID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return sprint, err
}
