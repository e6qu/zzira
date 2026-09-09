package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var (
	ErrComponentValidation = errors.New("invalid project component")
	ErrComponentConflict   = errors.New("project component already exists")
)

type ComponentInput struct {
	ProjectIDOrKey string
	Name           string
	Description    string
	LeadAccountID  string
	AssigneeType   string
}

const componentSelect = `SELECT c.id,c.project_id,p.key,c.name,c.description,
	COALESCE(c.lead_account_id,''),c.assignee_type,
	COALESCE(CASE c.assignee_type
	  WHEN 'COMPONENT_LEAD' THEN c.lead_account_id
	  WHEN 'PROJECT_LEAD' THEN p.lead_account_id
	  WHEN 'PROJECT_DEFAULT' THEN CASE WHEN p.assignee_type='PROJECT_LEAD' THEN p.lead_account_id END
	END,''),
	CASE c.assignee_type WHEN 'PROJECT_DEFAULT' THEN p.assignee_type ELSE c.assignee_type END,
	(c.assignee_type<>'COMPONENT_LEAD' OR c.lead_account_id IS NOT NULL),
	(SELECT count(*) FROM issues i WHERE i.project_id=c.project_id AND (
	  EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(i.fields->'components')='array' THEN i.fields->'components' ELSE '[]'::jsonb END) ref WHERE ref->>'id'=c.id)
	  OR lower(COALESCE(i.fields->>'component',''))=lower(c.name)))
	FROM project_components c JOIN projects p ON p.id=c.project_id `

type componentRow interface{ Scan(...any) error }

func scanComponent(row componentRow) (*models.ProjectComponent, error) {
	c := &models.ProjectComponent{}
	err := row.Scan(&c.ID, &c.ProjectID, &c.ProjectKey, &c.Name, &c.Description,
		&c.LeadAccountID, &c.AssigneeType, &c.RealAssigneeID, &c.RealAssigneeType,
		&c.IsAssigneeTypeValid, &c.IssueCount)
	return c, err
}

func validateComponentInput(input *ComponentInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.LeadAccountID = strings.TrimSpace(input.LeadAccountID)
	input.AssigneeType = strings.ToUpper(strings.TrimSpace(input.AssigneeType))
	if input.Name == "" || len(input.Name) > 255 {
		return fmt.Errorf("%w: name is required and accepts at most 255 characters", ErrComponentValidation)
	}
	if len(input.Description) > 1000 {
		return fmt.Errorf("%w: description accepts at most 1000 characters", ErrComponentValidation)
	}
	if input.AssigneeType == "" {
		input.AssigneeType = "PROJECT_DEFAULT"
	}
	switch input.AssigneeType {
	case "PROJECT_DEFAULT", "COMPONENT_LEAD", "PROJECT_LEAD", "UNASSIGNED":
	default:
		return fmt.Errorf("%w: unsupported assignee type", ErrComponentValidation)
	}
	if input.AssigneeType == "COMPONENT_LEAD" && input.LeadAccountID == "" {
		return fmt.Errorf("%w: component lead assignment requires a lead", ErrComponentValidation)
	}
	return nil
}

func componentAudit(ctx context.Context, tx pgx.Tx, workspaceID, actorID, action string, component *models.ProjectComponent) error {
	detail, _ := json.Marshal(map[string]any{"projectId": component.ProjectID, "name": component.Name})
	if _, err := tx.Exec(ctx, `INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail) SELECT organization_id,$2,$3,'project_component',$4,$5::jsonb FROM sites WHERE workspace_id=$1`, workspaceID, actorID, action, component.ID, detail); err != nil {
		return err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"component": component})
	if err != nil {
		return err
	}
	op := models.OpUpsert
	if action == "component.deleted" {
		op = models.OpDelete
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: "component", EntityID: component.ID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID})
}

func validateComponentLead(ctx context.Context, tx pgx.Tx, workspaceID, leadID string) error {
	if leadID == "" {
		return nil
	}
	var active bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id AND u.active WHERE m.workspace_id=$1 AND m.user_id=$2)`, workspaceID, leadID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return fmt.Errorf("%w: lead must be an active workspace member", ErrComponentValidation)
	}
	return nil
}

func (s *Store) CreateComponent(ctx context.Context, workspaceID, actorID string, input ComponentInput) (*models.ProjectComponent, error) {
	if err := validateComponentInput(&input); err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	var projectID string
	if err = tx.QueryRow(ctx, `SELECT id FROM projects WHERE workspace_id=$1 AND (id=$2 OR upper(key)=upper($2)) FOR SHARE`, workspaceID, input.ProjectIDOrKey).Scan(&projectID); err != nil {
		return nil, err
	}
	if err = validateComponentLead(ctx, tx, workspaceID, input.LeadAccountID); err != nil {
		return nil, err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO project_components(project_id,name,description,lead_account_id,assignee_type) VALUES($1,$2,$3,NULLIF($4,''),$5) ON CONFLICT DO NOTHING RETURNING id`, projectID, input.Name, input.Description, input.LeadAccountID, input.AssigneeType).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrComponentConflict
	}
	if err != nil {
		return nil, err
	}
	component, err := scanComponent(tx.QueryRow(ctx, componentSelect+`WHERE c.id=$1`, id))
	if err != nil {
		return nil, err
	}
	if err = componentAudit(ctx, tx, workspaceID, actorID, "component.created", component); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return component, nil
}

func (s *Store) ComponentByID(ctx context.Context, workspaceID, id string) (*models.ProjectComponent, error) {
	return scanComponent(s.Pool.QueryRow(ctx, componentSelect+`WHERE p.workspace_id=$1 AND c.id=$2`, workspaceID, id))
}

func (s *Store) Components(ctx context.Context, workspaceID, projectIDOrKey, query, order string) ([]*models.ProjectComponent, error) {
	if order == "" {
		order = "name"
	}
	orderSQL := "lower(c.name),c.id"
	switch order {
	case "name":
	case "-name":
		orderSQL = "lower(c.name) DESC,c.id DESC"
	default:
		return nil, fmt.Errorf("%w: orderBy must be name or -name", ErrComponentValidation)
	}
	rows, err := s.Pool.Query(ctx, componentSelect+`WHERE p.workspace_id=$1 AND ($2='' OR p.id=$2 OR upper(p.key)=upper($2)) AND ($3='' OR c.name ILIKE '%'||$3||'%') ORDER BY `+orderSQL, workspaceID, projectIDOrKey, strings.TrimSpace(query))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.ProjectComponent{}
	for rows.Next() {
		component, err := scanComponent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, component)
	}
	return out, rows.Err()
}

// ComponentDefaultAssignee resolves Jira's component assignment before issue
// creation. The first selected component owns the default, matching the order
// supplied by the create screen or REST client.
func (s *Store) ComponentDefaultAssignee(ctx context.Context, workspaceID, projectID string, raw json.RawMessage) (string, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false, nil
	}
	var refs []componentRef
	if json.Unmarshal(raw, &refs) != nil {
		return "", false, fmt.Errorf("%w: components must be an array", ErrComponentValidation)
	}
	if len(refs) == 0 {
		return "", false, nil
	}
	ref := refs[0]
	component, err := scanComponent(s.Pool.QueryRow(ctx, componentSelect+`WHERE p.workspace_id=$1 AND c.project_id=$2 AND (($3<>'' AND c.id=$3) OR ($3='' AND lower(c.name)=lower($4)))`, workspaceID, projectID, ref.ID, strings.TrimSpace(ref.Name)))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("%w: component does not belong to this project", ErrComponentValidation)
	}
	if err != nil {
		return "", false, err
	}
	return component.RealAssigneeID, true, nil
}

func (s *Store) UpdateComponent(ctx context.Context, workspaceID, actorID, id string, input ComponentInput) (*models.ProjectComponent, error) {
	if err := validateComponentInput(&input); err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	if err = validateComponentLead(ctx, tx, workspaceID, input.LeadAccountID); err != nil {
		return nil, err
	}
	var updatedID string
	err = tx.QueryRow(ctx, `UPDATE project_components c SET name=$3,description=$4,lead_account_id=NULLIF($5,''),assignee_type=$6,updated_at=now() FROM projects p WHERE p.id=c.project_id AND p.workspace_id=$1 AND c.id=$2 AND NOT EXISTS(SELECT 1 FROM project_components duplicate WHERE duplicate.project_id=c.project_id AND duplicate.id<>c.id AND lower(duplicate.name)=lower($3)) RETURNING c.id`, workspaceID, id, input.Name, input.Description, input.LeadAccountID, input.AssigneeType).Scan(&updatedID)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, lookupErr := scanComponent(tx.QueryRow(ctx, componentSelect+`WHERE p.workspace_id=$1 AND c.id=$2`, workspaceID, id)); errors.Is(lookupErr, pgx.ErrNoRows) {
			return nil, lookupErr
		}
		return nil, ErrComponentConflict
	}
	if err != nil {
		return nil, err
	}
	component, err := scanComponent(tx.QueryRow(ctx, componentSelect+`WHERE c.id=$1`, updatedID))
	if err != nil {
		return nil, err
	}
	if err = refreshComponentIssues(ctx, tx, workspaceID, actorID, component, component); err != nil {
		return nil, err
	}
	if err = componentAudit(ctx, tx, workspaceID, actorID, "component.updated", component); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return component, nil
}

func (s *Store) DeleteComponent(ctx context.Context, workspaceID, actorID, id, moveIssuesTo string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	component, err := scanComponent(tx.QueryRow(ctx, componentSelect+`WHERE p.workspace_id=$1 AND c.id=$2 FOR UPDATE OF c`, workspaceID, id))
	if err != nil {
		return err
	}
	var replacement *models.ProjectComponent
	if moveIssuesTo != "" {
		replacement, err = scanComponent(tx.QueryRow(ctx, componentSelect+`WHERE p.workspace_id=$1 AND c.project_id=$2 AND c.id=$3 FOR SHARE OF c`, workspaceID, component.ProjectID, moveIssuesTo))
		if err != nil {
			return fmt.Errorf("%w: replacement component is unavailable", ErrComponentValidation)
		}
	}
	if err = refreshComponentIssues(ctx, tx, workspaceID, actorID, component, replacement); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM project_components WHERE id=$1`, id); err != nil {
		return err
	}
	if err = componentAudit(ctx, tx, workspaceID, actorID, "component.deleted", component); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type componentRef struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

func normalizeComponentFields(ctx context.Context, tx pgx.Tx, projectID string, fields map[string]json.RawMessage) error {
	raw, ok := fields["components"]
	if !ok {
		return nil
	}
	if string(raw) == "null" {
		fields["components"] = json.RawMessage(`[]`)
		return nil
	}
	var refs []componentRef
	if len(raw) > 64<<10 || json.Unmarshal(raw, &refs) != nil || refs == nil || len(refs) > 100 {
		return fmt.Errorf("%w: components must be an array of at most 100 references", ErrComponentValidation)
	}
	canonical := []componentRef{}
	seen := map[string]bool{}
	for _, ref := range refs {
		if ref.ID == "" && strings.TrimSpace(ref.Name) == "" {
			return fmt.Errorf("%w: every component requires an id or name", ErrComponentValidation)
		}
		component, err := scanComponent(tx.QueryRow(ctx, componentSelect+`WHERE c.project_id=$1 AND (($2<>'' AND c.id=$2) OR ($2='' AND lower(c.name)=lower($3)))`, projectID, ref.ID, strings.TrimSpace(ref.Name)))
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: component does not belong to this project", ErrComponentValidation)
		}
		if err != nil {
			return err
		}
		if !seen[component.ID] {
			canonical = append(canonical, componentRef{ID: component.ID, Name: component.Name, Description: component.Description})
			seen[component.ID] = true
		}
	}
	encoded, err := json.Marshal(canonical)
	if err == nil {
		fields["components"] = encoded
	}
	return err
}

func refreshComponentIssues(ctx context.Context, tx pgx.Tx, workspaceID, actorID string, current, replacement *models.ProjectComponent) error {
	match, _ := json.Marshal([]map[string]string{{"id": current.ID}})
	rows, err := tx.Query(ctx, `SELECT id FROM issues WHERE project_id=$1 AND fields->'components' @> $2::jsonb ORDER BY id FOR UPDATE`, current.ProjectID, match)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, issueID := range ids {
		issue, err := scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.id=$1`, issueID))
		if err != nil {
			return err
		}
		var refs []componentRef
		if err = json.Unmarshal(issue.Fields["components"], &refs); err != nil {
			return err
		}
		updated, seen := []componentRef{}, map[string]bool{}
		for _, ref := range refs {
			if ref.ID == current.ID {
				if replacement == nil || seen[replacement.ID] {
					continue
				}
				ref = componentRef{ID: replacement.ID, Name: replacement.Name, Description: replacement.Description}
			}
			if !seen[ref.ID] {
				updated, seen[ref.ID] = append(updated, ref), true
			}
		}
		encoded, _ := json.Marshal(updated)
		seq, err := nextSeq(ctx, tx, workspaceID)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE issues SET fields=jsonb_set(fields,'{components}',$2::jsonb),updated_seq=$3,updated_at=now() WHERE id=$1`, issueID, encoded, seq); err != nil {
			return err
		}
		issue, err = scanIssue(tx.QueryRow(ctx, issueJoin+`WHERE i.id=$1`, issueID))
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(models.IssueUpsertPayload{Issue: *issue})
		if err = appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssue, EntityID: issueID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}); err != nil {
			return err
		}
	}
	return nil
}
