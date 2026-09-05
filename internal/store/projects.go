package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var ErrProjectPermission = errors.New("workspace administrator permission is required")
var ErrProjectLeadRequired = errors.New("a project lead is required for default assignment")

type ProjectUpdate struct {
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	URL           *string `json:"url"`
	LeadAccountID *string `json:"leadAccountId"`
	AssigneeType  *string `json:"assigneeType"`
}

// writeProjectAction keeps project metadata and its audit record atomic.
func writeProjectAction(ctx context.Context, tx pgx.Tx, actorID string, p *models.Project) error {
	seq, err := nextSeq(ctx, tx, p.WorkspaceID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"project": p})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: p.WorkspaceID, Seq: seq, EntityType: "project", EntityID: p.ID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID})
}

func projectAdmin(ctx context.Context, tx pgx.Tx, workspaceID, actorID string) error {
	var role string
	err := tx.QueryRow(ctx, `SELECT role FROM memberships WHERE workspace_id=$1 AND user_id=$2 FOR SHARE`, workspaceID, actorID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && role != "admin") {
		return ErrProjectPermission
	}
	return err
}

func (s *Store) CreateProject(ctx context.Context, actorID string, p models.Project, boardType string) (*models.Project, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, p.WorkspaceID, actorID); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, `SELECT nextval('jira_project_id')::text`).Scan(&p.ID); err != nil {
		return nil, err
	}
	p.WorkflowID = "wf_default"
	_, err = tx.Exec(ctx, `INSERT INTO projects (id,workspace_id,key,name,workflow_id,description,url,lead_account_id,assignee_type) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, p.ID, p.WorkspaceID, p.Key, p.Name, p.WorkflowID, p.Description, p.URL, nilIfEmpty(p.LeadAccountID), p.AssigneeType)
	if err != nil {
		return nil, err
	}
	if err := writeProjectAction(ctx, tx, actorID, &p); err != nil {
		return nil, err
	}
	board := models.Board{ID: NewID("brd"), ProjectID: p.ID, Name: p.Name + " board", Type: boardType}
	err = tx.QueryRow(ctx, `INSERT INTO boards (id,project_id,name,type,filter_jql) VALUES ($1,$2,$3,$4,$5) RETURNING column_status_ids,filter_jql,quick_filters,swimlane_strategy,card_fields,column_limits`, board.ID, p.ID, board.Name, board.Type, "project = "+p.Key).Scan(&board.ColumnStatusIDs, &board.FilterJQL, &board.QuickFilters, &board.SwimlaneStrategy, &board.CardFields, &board.ColumnLimits)
	if err != nil {
		return nil, err
	}
	seq, err := nextSeq(ctx, tx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(models.BoardUpsertPayload{Board: board})
	if err != nil {
		return nil, err
	}
	if err := appendAction(ctx, tx, &models.Action{WorkspaceID: p.WorkspaceID, Seq: seq, EntityType: models.EntityBoard, EntityID: board.ID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Store) UpdateProject(ctx context.Context, actorID, workspaceID, idOrKey string, up ProjectUpdate) (*models.Project, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	p := &models.Project{}
	err = tx.QueryRow(ctx, `UPDATE projects SET name=COALESCE($3,name),description=COALESCE($4,description),url=COALESCE($5,url),lead_account_id=CASE WHEN $6::text IS NULL THEN lead_account_id ELSE NULLIF($6,'') END,assignee_type=COALESCE($7,assignee_type) WHERE workspace_id=$1 AND (id=$2 OR upper(key)=upper($2)) RETURNING id,workspace_id,key,name,COALESCE(workflow_id,''),COALESCE(security_scheme_id,''),description,url,COALESCE(lead_account_id,''),assignee_type`, workspaceID, idOrKey, up.Name, up.Description, up.URL, up.LeadAccountID, up.AssigneeType).Scan(&p.ID, &p.WorkspaceID, &p.Key, &p.Name, &p.WorkflowID, &p.SecuritySchemeID, &p.Description, &p.URL, &p.LeadAccountID, &p.AssigneeType)
	if err != nil {
		return nil, err
	}
	if p.AssigneeType == "PROJECT_LEAD" && p.LeadAccountID == "" {
		return nil, ErrProjectLeadRequired
	}
	if err := writeProjectAction(ctx, tx, actorID, p); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}
