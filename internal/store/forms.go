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

func (s *Store) AttachIssueForm(ctx context.Context, workspaceID, issueID, templateID, name string) (*models.IssueForm, error) {
	templateID, name = strings.TrimSpace(templateID), strings.TrimSpace(name)
	if templateID == "" {
		return nil, fmt.Errorf("form template id is required")
	}
	if name == "" {
		name = templateID
	}
	if len(templateID) > 255 || len(name) > 255 {
		return nil, fmt.Errorf("form template id and name must be at most 255 characters")
	}
	id := NewID("form")
	return scanIssueForm(s.Pool.QueryRow(ctx, `
		INSERT INTO issue_forms AS f(id,issue_id,form_template_id,name)
		SELECT $1,i.id,$4,$5 FROM issues i WHERE i.workspace_id=$2 AND i.id=$3
		RETURNING `+issueFormColumns, id, workspaceID, issueID, templateID, name))
}

func (s *Store) SaveIssueFormAnswers(ctx context.Context, workspaceID, issueID, formID string, answers json.RawMessage) (*models.IssueForm, error) {
	trimmed := bytes.TrimSpace(answers)
	if len(trimmed) == 0 || !json.Valid(trimmed) || trimmed[0] != '{' {
		return nil, fmt.Errorf("form answers must be a JSON object")
	}
	return scanIssueForm(s.Pool.QueryRow(ctx, `
		UPDATE issue_forms f SET answers=$4::jsonb,updated_at=now()
		FROM issues i WHERE i.id=f.issue_id AND i.workspace_id=$1 AND f.issue_id=$2 AND f.id=$3 AND NOT f.locked AND NOT f.submitted
		RETURNING `+issueFormColumns, workspaceID, issueID, formID, answers))
}

func (s *Store) SetIssueFormStatus(ctx context.Context, workspaceID, issueID, formID string, submitted bool) (*models.IssueForm, error) {
	return scanIssueForm(s.Pool.QueryRow(ctx, `
		UPDATE issue_forms f SET submitted=$4,locked=false,updated_at=now()
		FROM issues i WHERE i.id=f.issue_id AND i.workspace_id=$1 AND f.issue_id=$2 AND f.id=$3
		RETURNING `+issueFormColumns, workspaceID, issueID, formID, submitted))
}

func (s *Store) SetIssueFormVisibility(ctx context.Context, workspaceID, issueID, formID string, internal bool) (*models.IssueForm, error) {
	return scanIssueForm(s.Pool.QueryRow(ctx, `
		UPDATE issue_forms f SET internal=$4,updated_at=now()
		FROM issues i WHERE i.id=f.issue_id AND i.workspace_id=$1 AND f.issue_id=$2 AND f.id=$3
		RETURNING `+issueFormColumns, workspaceID, issueID, formID, internal))
}

func (s *Store) DeleteIssueForm(ctx context.Context, workspaceID, issueID, formID string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM issue_forms f USING issues i WHERE i.id=f.issue_id AND i.workspace_id=$1 AND f.issue_id=$2 AND f.id=$3`, workspaceID, issueID, formID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) IssueFormState(ctx context.Context, workspaceID, issueID string) (attached int, allSubmitted bool, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT count(*),COALESCE(bool_and(f.submitted),false) FROM issue_forms f JOIN issues i ON i.id=f.issue_id WHERE i.workspace_id=$1 AND f.issue_id=$2`, workspaceID, issueID).Scan(&attached, &allSubmitted)
	return
}
