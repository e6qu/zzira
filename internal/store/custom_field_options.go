package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

const customFieldOptionColumns = `id::text,context_id::text,value,disabled,position`

func scanCustomFieldOption(row pgx.Row) (models.CustomFieldOption, error) {
	option := models.CustomFieldOption{}
	err := row.Scan(&option.ID, &option.ContextID, &option.Value, &option.Disabled, &option.Position)
	if errors.Is(err, pgx.ErrNoRows) {
		return option, ErrFieldContextNotFound
	}
	return option, err
}

func customFieldOptionsTx(ctx context.Context, tx pgx.Tx, contextID string) ([]models.CustomFieldOption, error) {
	rows, err := tx.Query(ctx, `SELECT `+customFieldOptionColumns+` FROM custom_field_options
		WHERE context_id=$1 ORDER BY position,id`, contextID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	options := []models.CustomFieldOption{}
	for rows.Next() {
		option, scanErr := scanCustomFieldOption(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		options = append(options, option)
	}
	return options, rows.Err()
}

// CustomFieldOptions returns one context's options in display order.
func (s *Store) CustomFieldOptions(ctx context.Context, workspaceID, fieldID, contextID string) ([]models.CustomFieldOption, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = customFieldExistsTx(ctx, tx, workspaceID, fieldID); err != nil {
		return nil, err
	}
	found, err := customFieldContextTx(ctx, tx, fieldID, contextID)
	if err != nil {
		return nil, err
	}
	return customFieldOptionsTx(ctx, tx, found.ID)
}

// CustomFieldOption looks one option up by its own ID, which is how Jira's
// /customFieldOption/{id} endpoint addresses it.
func (s *Store) CustomFieldOption(ctx context.Context, workspaceID, optionID string) (models.CustomFieldOption, error) {
	id, err := strconv.ParseInt(optionID, 10, 64)
	if err != nil {
		return models.CustomFieldOption{}, ErrFieldContextNotFound
	}
	// The joined tables all have an id column, so every column is qualified.
	return scanCustomFieldOption(s.Pool.QueryRow(ctx, `SELECT
		o.id::text,o.context_id::text,o.value,o.disabled,o.position
		FROM custom_field_options o
		JOIN custom_field_contexts c ON c.id=o.context_id
		JOIN custom_fields f ON f.id=c.field_id
		WHERE o.id=$1 AND (f.workspace_id IS NULL OR f.workspace_id=$2)`, id, workspaceID))
}

func selectFieldTx(ctx context.Context, tx pgx.Tx, workspaceID, fieldID string) error {
	var fieldType string
	err := tx.QueryRow(ctx, `SELECT type FROM custom_fields
		WHERE id=$1 AND (workspace_id IS NULL OR workspace_id=$2)`, fieldID, workspaceID).Scan(&fieldType)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrFieldContextNotFound
	}
	if err != nil {
		return err
	}
	if fieldType != models.CustomFieldSelect {
		return fmt.Errorf("%w: only a select custom field has options", ErrFieldContextValidation)
	}
	return nil
}

func (s *Store) CreateCustomFieldOptions(ctx context.Context, workspaceID, actorID, fieldID, contextID string, values []string) ([]models.CustomFieldOption, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	if err = selectFieldTx(ctx, tx, workspaceID, fieldID); err != nil {
		return nil, err
	}
	found, err := customFieldContextTx(ctx, tx, fieldID, contextID)
	if err != nil {
		return nil, err
	}
	created := []models.CustomFieldOption{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 255 {
			return nil, fmt.Errorf("%w: an option value must contain 1 to 255 characters", ErrFieldContextValidation)
		}
		option, insertErr := scanCustomFieldOption(tx.QueryRow(ctx, `INSERT INTO custom_field_options(context_id,value,position)
			VALUES($1,$2,COALESCE((SELECT max(position)+1 FROM custom_field_options WHERE context_id=$1),0))
			RETURNING `+customFieldOptionColumns, found.ID, value))
		if isUniqueViolation(insertErr) {
			return nil, fmt.Errorf("%w: option %q already exists in this context", ErrFieldContextConflict, value)
		}
		if insertErr != nil {
			return nil, insertErr
		}
		created = append(created, option)
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "custom_field_option", found.ID, models.OpUpsert,
		map[string]any{"contextId": found.ID, "options": created}); err != nil {
		return nil, err
	}
	return created, tx.Commit(ctx)
}

func (s *Store) UpdateCustomFieldOptions(ctx context.Context, workspaceID, actorID, fieldID, contextID string, options []models.CustomFieldOption) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	found, err := customFieldContextTx(ctx, tx, fieldID, contextID)
	if err != nil {
		return err
	}
	for _, option := range options {
		value := strings.TrimSpace(option.Value)
		if value == "" || len(value) > 255 {
			return fmt.Errorf("%w: an option value must contain 1 to 255 characters", ErrFieldContextValidation)
		}
		command, execErr := tx.Exec(ctx, `UPDATE custom_field_options SET value=$3,disabled=$4
			WHERE context_id=$1 AND id=$2`, found.ID, option.ID, value, option.Disabled)
		if isUniqueViolation(execErr) {
			return fmt.Errorf("%w: option %q already exists in this context", ErrFieldContextConflict, value)
		}
		if execErr != nil {
			return execErr
		}
		if command.RowsAffected() == 0 {
			return ErrFieldContextNotFound
		}
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "custom_field_option", found.ID, models.OpUpsert,
		map[string]any{"contextId": found.ID, "options": options}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReorderCustomFieldOptions applies Jira's move request: the listed options are
// placed after a given option, or first when none is given.
func (s *Store) ReorderCustomFieldOptions(ctx context.Context, workspaceID, actorID, fieldID, contextID string, moved []string, after, position string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	found, err := customFieldContextTx(ctx, tx, fieldID, contextID)
	if err != nil {
		return err
	}
	options, err := customFieldOptionsTx(ctx, tx, found.ID)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, option := range options {
		known[option.ID] = true
	}
	if len(moved) == 0 {
		return fmt.Errorf("%w: customFieldOptionIds is required", ErrFieldContextValidation)
	}
	for _, id := range moved {
		if !known[id] {
			return ErrFieldContextNotFound
		}
	}
	if after != "" && !known[after] {
		return ErrFieldContextNotFound
	}
	movedSet := map[string]bool{}
	for _, id := range moved {
		movedSet[id] = true
	}
	remaining := make([]string, 0, len(options))
	for _, option := range options {
		if !movedSet[option.ID] {
			remaining = append(remaining, option.ID)
		}
	}
	target := 0
	switch {
	case after != "":
		for index, id := range remaining {
			if id == after {
				target = index + 1
			}
		}
	case strings.EqualFold(position, "Last"):
		target = len(remaining)
	case strings.EqualFold(position, "First"), position == "":
		target = 0
	default:
		return fmt.Errorf("%w: position must be First or Last", ErrFieldContextValidation)
	}
	order := append(remaining[:target:target], append(append([]string{}, moved...), remaining[target:]...)...)
	for index, id := range order {
		if _, err = tx.Exec(ctx, `UPDATE custom_field_options SET position=$3 WHERE context_id=$1 AND id=$2`,
			found.ID, id, index); err != nil {
			return err
		}
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "custom_field_option", found.ID, models.OpUpsert,
		map[string]any{"contextId": found.ID, "order": order}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteCustomFieldOption removes an option. When replacement is given, work
// items holding the option take the replacement first; otherwise an option
// still in use is refused so a work item never keeps a value the field no
// longer offers.
func (s *Store) DeleteCustomFieldOption(ctx context.Context, workspaceID, actorID, fieldID, contextID, optionID, replacement string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	found, err := customFieldContextTx(ctx, tx, fieldID, contextID)
	if err != nil {
		return err
	}
	option, err := scanCustomFieldOption(tx.QueryRow(ctx, `SELECT `+customFieldOptionColumns+`
		FROM custom_field_options WHERE context_id=$1 AND id=$2`, found.ID, optionID))
	if err != nil {
		return err
	}
	if replacement == option.ID {
		return fmt.Errorf("%w: the replacement must be a different option", ErrFieldContextValidation)
	}
	replacementID := ""
	if replacement != "" {
		target, replaceErr := scanCustomFieldOption(tx.QueryRow(ctx, `SELECT `+customFieldOptionColumns+`
			FROM custom_field_options WHERE context_id=$1 AND id=$2`, found.ID, replacement))
		if replaceErr != nil {
			return replaceErr
		}
		replacementID = target.ID
	}
	var used int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM issues
		WHERE workspace_id=$1 AND fields->>$2 = $3`, workspaceID, fieldID, option.ID).Scan(&used); err != nil {
		return err
	}
	if used > 0 && replacement == "" {
		return fmt.Errorf("%w: %d work items still use this option; supply a replacement", ErrFieldContextConflict, used)
	}
	if used > 0 {
		if _, err = tx.Exec(ctx, `UPDATE issues SET fields=jsonb_set(fields,ARRAY[$2],to_jsonb($3::text)),updated_at=now()
			WHERE workspace_id=$1 AND fields->>$2 = $4`, workspaceID, fieldID, replacementID, option.ID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM custom_field_options WHERE context_id=$1 AND id=$2`, found.ID, option.ID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "custom_field_option", option.ID, models.OpDelete,
		map[string]any{"contextId": found.ID, "optionId": option.ID, "replaceWith": replacement, "movedIssues": used}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
