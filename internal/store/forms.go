package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/models"
)

const issueFormColumns = `f.id, f.issue_id, f.form_template_id, f.name, f.internal, f.submitted, f.locked, f.answers,
to_char(f.updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`

func scanIssueForm(row pgx.Row) (*models.IssueForm, error) {
	form := &models.IssueForm{}
	err := row.Scan(&form.ID, &form.IssueID, &form.TemplateID, &form.Name, &form.Internal, &form.Submitted, &form.Locked, &form.Answers, &form.Updated)
	return form, err
}

func (s *Store) IssueForms(ctx context.Context, workspaceID, issueID string) ([]*models.IssueForm, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+issueFormColumns+` FROM issue_forms f JOIN issues i ON i.id=f.issue_id WHERE i.workspace_id=$1 AND f.issue_id=$2 ORDER BY f.created_at,f.id`, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	forms := make([]*models.IssueForm, 0)
	for rows.Next() {
		form, err := scanIssueForm(rows)
		if err != nil {
			return nil, err
		}
		forms = append(forms, form)
	}
	return forms, rows.Err()
}

func (s *Store) IssueForm(ctx context.Context, workspaceID, issueID, formID string) (*models.IssueForm, error) {
	return scanIssueForm(s.Pool.QueryRow(ctx, `SELECT `+issueFormColumns+` FROM issue_forms f JOIN issues i ON i.id=f.issue_id WHERE i.workspace_id=$1 AND f.issue_id=$2 AND f.id=$3`, workspaceID, issueID, formID))
}

// IssueFormChange describes one mutation of an attached form.
type IssueFormChange struct {
	Action     string
	TemplateID string
	Name       string
	Answers    json.RawMessage
}

// ChangeIssueForm commits a form and its permission-scoped action together.
func (s *Store) ChangeIssueForm(ctx context.Context, actorID, workspaceID, issueID, formID string, in IssueFormChange) (*models.IssueForm, error) {
	if in.Action == "attach" {
		in.TemplateID, in.Name = strings.TrimSpace(in.TemplateID), strings.TrimSpace(in.Name)
		if in.TemplateID == "" {
			return nil, fmt.Errorf("form template id is required")
		}
		if in.Name == "" {
			in.Name = in.TemplateID
		}
		if len(in.TemplateID) > 255 || len(in.Name) > 255 {
			return nil, fmt.Errorf("form template id and name must be at most 255 characters")
		}
	}
	if in.Action == "answers" {
		trimmed := bytes.TrimSpace(in.Answers)
		if len(trimmed) == 0 || !json.Valid(trimmed) || trimmed[0] != '{' {
			return nil, fmt.Errorf("form answers must be a JSON object")
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var form *models.IssueForm
	var row pgx.Row
	switch in.Action {
	case "attach":
		row = tx.QueryRow(ctx, `INSERT INTO issue_forms AS f(id,issue_id,form_template_id,name)
			SELECT $1,i.id,$4,$5 FROM issues i WHERE i.workspace_id=$2 AND i.id=$3
			RETURNING `+issueFormColumns, NewID("form"), workspaceID, issueID, in.TemplateID, in.Name)
	case "answers":
		row = tx.QueryRow(ctx, `UPDATE issue_forms f SET answers=$4::jsonb,updated_at=now()
			FROM issues i WHERE i.id=f.issue_id AND i.workspace_id=$1 AND f.issue_id=$2 AND f.id=$3 AND NOT f.locked AND NOT f.submitted
			RETURNING `+issueFormColumns, workspaceID, issueID, formID, in.Answers)
	case "submit", "reopen":
		row = tx.QueryRow(ctx, `UPDATE issue_forms f SET submitted=$4,locked=false,updated_at=now()
			FROM issues i WHERE i.id=f.issue_id AND i.workspace_id=$1 AND f.issue_id=$2 AND f.id=$3
			RETURNING `+issueFormColumns, workspaceID, issueID, formID, in.Action == "submit")
	case "internal", "external":
		row = tx.QueryRow(ctx, `UPDATE issue_forms f SET internal=$4,updated_at=now()
			FROM issues i WHERE i.id=f.issue_id AND i.workspace_id=$1 AND f.issue_id=$2 AND f.id=$3
			RETURNING `+issueFormColumns, workspaceID, issueID, formID, in.Action == "internal")
	case "delete":
		row = tx.QueryRow(ctx, `DELETE FROM issue_forms f USING issues i
			WHERE i.id=f.issue_id AND i.workspace_id=$1 AND f.issue_id=$2 AND f.id=$3
			RETURNING `+issueFormColumns, workspaceID, issueID, formID)
	default:
		return nil, fmt.Errorf("unknown form action")
	}
	form, err = scanIssueForm(row)
	if err != nil {
		return nil, err
	}
	seq, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{"issueId": issueID, "form": form, "action": in.Action})
	if err != nil {
		return nil, err
	}
	op := models.OpUpsert
	if in.Action == "delete" {
		op = models.OpDelete
	}
	if err := appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityIssueForm,
		EntityID: form.ID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}); err != nil {
		return nil, err
	}
	return form, tx.Commit(ctx)
}

func (s *Store) IssueFormState(ctx context.Context, workspaceID, issueID string) (attached int, allSubmitted bool, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT count(*),COALESCE(bool_and(f.submitted),false) FROM issue_forms f JOIN issues i ON i.id=f.issue_id WHERE i.workspace_id=$1 AND f.issue_id=$2`, workspaceID, issueID).Scan(&attached, &allSubmitted)
	return
}
