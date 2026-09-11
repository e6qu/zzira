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

// Jira's issue field options belong to a select list an app provides, and are
// deliberately a different resource from the context-scoped options an
// administrator manages. What separates them is the field: this surface serves
// an app's field and refuses one created here, and the context option surface
// does the opposite. They share one option table because they are the same
// thing to everything downstream — the create form, write validation and
// search all resolve a select value the same way whoever supplied the field.

var ErrAppFieldOption = errors.New("invalid issue field option")

// AppFieldOption is one option on an app-provided select list.
type AppFieldOption struct {
	ID            string
	Value         string
	NotSelectable bool
	Properties    json.RawMessage
	Scope         json.RawMessage
}

const appFieldOptionColumns = `o.id::text,o.value,o.disabled,COALESCE(o.properties,'{}'::jsonb),o.scope`

func scanAppFieldOption(row pgx.Row) (AppFieldOption, error) {
	option := AppFieldOption{}
	var scope []byte
	err := row.Scan(&option.ID, &option.Value, &option.NotSelectable, &option.Properties, &scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return option, pgx.ErrNoRows
	}
	option.Scope = json.RawMessage(scope)
	return option, err
}

// appSelectField resolves an app-provided select field by the key Jira
// addresses it with, refusing a field this product's own administration
// created. That refusal is the whole point of the separation: a client must not
// be able to manage an administrator's options through the app surface.
func appSelectField(ctx context.Context, tx pgx.Tx, workspaceID, fieldKey string) (fieldID string, contextID int64, err error) {
	var fieldType, appKey, moduleKey string
	err = tx.QueryRow(ctx, `SELECT cf.id, cf.type, COALESCE(ai.app_key,''), cf.app_module_key
		FROM custom_fields cf LEFT JOIN app_installations ai ON ai.id=cf.app_installation_id
		WHERE cf.active AND cf.trashed_at IS NULL AND (cf.workspace_id IS NULL OR cf.workspace_id=$1)
		  AND (cf.id=$2 OR COALESCE(ai.app_key,'') || '__' || cf.app_module_key = $2)`,
		workspaceID, fieldKey).Scan(&fieldID, &fieldType, &appKey, &moduleKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, pgx.ErrNoRows
	}
	if err != nil {
		return "", 0, err
	}
	if appKey == "" {
		return "", 0, fmt.Errorf("%w: this operation is only for select lists provided by an app", ErrAppFieldOption)
	}
	if fieldType != models.CustomFieldSelect {
		return "", 0, fmt.Errorf("%w: the field is not a select list", ErrAppFieldOption)
	}
	if err = tx.QueryRow(ctx, `SELECT id FROM custom_field_contexts WHERE field_id=$1 ORDER BY id LIMIT 1`,
		fieldID).Scan(&contextID); err != nil {
		return "", 0, err
	}
	return fieldID, contextID, nil
}

// AppFieldIsAppProvided reports whether a field came from an app, so the
// context option surface can refuse one and point at this one instead.
func (s *Store) AppFieldIsAppProvided(ctx context.Context, workspaceID, fieldID string) (bool, error) {
	var appKey string
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(ai.app_key,'') FROM custom_fields cf
		LEFT JOIN app_installations ai ON ai.id=cf.app_installation_id
		WHERE cf.id=$1 AND (cf.workspace_id IS NULL OR cf.workspace_id=$2)`, fieldID, workspaceID).Scan(&appKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return appKey != "", err
}

// AppFieldOptions lists an app select list's options in display order.
func (s *Store) AppFieldOptions(ctx context.Context, workspaceID, fieldKey string) ([]AppFieldOption, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, contextID, err := appSelectField(ctx, tx, workspaceID, fieldKey)
	if err != nil {
		return nil, err
	}
	return appFieldOptionsTx(ctx, tx, contextID)
}

func appFieldOptionsTx(ctx context.Context, tx pgx.Tx, contextID int64) ([]AppFieldOption, error) {
	rows, err := tx.Query(ctx, `SELECT `+appFieldOptionColumns+` FROM custom_field_options o
		WHERE o.context_id=$1 ORDER BY o.position, o.id`, contextID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	options := []AppFieldOption{}
	for rows.Next() {
		option, scanErr := scanAppFieldOption(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		options = append(options, option)
	}
	return options, rows.Err()
}

// AppFieldOptionByID reads one option of an app select list.
func (s *Store) AppFieldOptionByID(ctx context.Context, workspaceID, fieldKey, optionID string) (AppFieldOption, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return AppFieldOption{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, contextID, err := appSelectField(ctx, tx, workspaceID, fieldKey)
	if err != nil {
		return AppFieldOption{}, err
	}
	return scanAppFieldOption(tx.QueryRow(ctx, `SELECT `+appFieldOptionColumns+`
		FROM custom_field_options o WHERE o.context_id=$1 AND o.id::text=$2`, contextID, optionID))
}

// SaveAppFieldOption creates an option, or replaces one when optionID is given.
func (s *Store) SaveAppFieldOption(ctx context.Context, workspaceID, actorID, fieldKey, optionID, value string, properties, scope json.RawMessage) (AppFieldOption, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 {
		return AppFieldOption{}, fmt.Errorf("%w: a value of 1 to 255 characters is required", ErrAppFieldOption)
	}
	if len(properties) > 0 && !json.Valid(properties) {
		return AppFieldOption{}, fmt.Errorf("%w: the properties are not valid JSON", ErrAppFieldOption)
	}
	notSelectable, err := scopeMarksNotSelectable(scope)
	if err != nil {
		return AppFieldOption{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return AppFieldOption{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return AppFieldOption{}, err
	}
	_, contextID, err := appSelectField(ctx, tx, workspaceID, fieldKey)
	if err != nil {
		return AppFieldOption{}, err
	}
	if len(properties) == 0 {
		properties = json.RawMessage(`{}`)
	}
	var scopeArg any
	if len(scope) > 0 {
		scopeArg = []byte(scope)
	}
	var saved AppFieldOption
	if optionID == "" {
		saved, err = scanAppFieldOption(tx.QueryRow(ctx, `INSERT INTO custom_field_options(context_id,value,disabled,position,properties,scope)
			SELECT $1,$2,$3,COALESCE(MAX(position),0)+1,$4,$5 FROM custom_field_options WHERE context_id=$1
			RETURNING id::text,value,disabled,COALESCE(properties,'{}'::jsonb),scope`,
			contextID, value, notSelectable, []byte(properties), scopeArg))
	} else {
		saved, err = scanAppFieldOption(tx.QueryRow(ctx, `UPDATE custom_field_options
			SET value=$3,disabled=$4,properties=$5,scope=$6 WHERE context_id=$1 AND id::text=$2
			RETURNING id::text,value,disabled,COALESCE(properties,'{}'::jsonb),scope`,
			contextID, optionID, value, notSelectable, []byte(properties), scopeArg))
	}
	if isUniqueViolation(err) {
		return AppFieldOption{}, fmt.Errorf("%w: this select list already has an option with that value", ErrAppFieldOption)
	}
	if err != nil {
		return AppFieldOption{}, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "app_field_option", saved.ID, models.OpUpsert,
		map[string]any{"fieldKey": fieldKey, "value": saved.Value}); err != nil {
		return AppFieldOption{}, err
	}
	return saved, tx.Commit(ctx)
}

// scopeMarksNotSelectable reads Jira's deprecated per-option attributes, which
// is where an option says it can be seen but not chosen.
func scopeMarksNotSelectable(scope json.RawMessage) (bool, error) {
	if len(scope) == 0 {
		return false, nil
	}
	var config struct {
		Attributes []string `json:"attributes"`
	}
	if err := json.Unmarshal(scope, &config); err != nil {
		return false, fmt.Errorf("%w: the config is not valid JSON", ErrAppFieldOption)
	}
	for _, attribute := range config.Attributes {
		if attribute == "notSelectable" {
			return true, nil
		}
	}
	return false, nil
}

// DeleteAppFieldOption removes an option. Jira refuses one that work items
// still use, which is why the deselect is a separate operation.
func (s *Store) DeleteAppFieldOption(ctx context.Context, workspaceID, actorID, fieldKey, optionID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	fieldID, contextID, err := appSelectField(ctx, tx, workspaceID, fieldKey)
	if err != nil {
		return err
	}
	option, err := scanAppFieldOption(tx.QueryRow(ctx, `SELECT `+appFieldOptionColumns+`
		FROM custom_field_options o WHERE o.context_id=$1 AND o.id::text=$2`, contextID, optionID))
	if err != nil {
		return err
	}
	var used int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM issues WHERE workspace_id=$1 AND fields->>$2 = $3`,
		workspaceID, fieldID, option.ID).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return fmt.Errorf("%w: %d work items still use this option; deselect it first", ErrFieldContextConflict, used)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM custom_field_options WHERE context_id=$1 AND id::text=$2`, contextID, optionID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "app_field_option", option.ID, models.OpDelete,
		map[string]any{"fieldKey": fieldKey}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AppFieldOptionSuggestions returns the options a user may see for a project,
// narrowed to the selectable ones when the caller is filling in a form.
func (s *Store) AppFieldOptionSuggestions(ctx context.Context, workspaceID, fieldKey, projectID string, selectableOnly bool) ([]AppFieldOption, error) {
	options, err := s.AppFieldOptions(ctx, workspaceID, fieldKey)
	if err != nil {
		return nil, err
	}
	out := []AppFieldOption{}
	for _, option := range options {
		if selectableOnly && option.NotSelectable {
			continue
		}
		visible, err := optionInProjectScope(option.Scope, projectID)
		if err != nil {
			return nil, err
		}
		if visible {
			out = append(out, option)
		}
	}
	return out, nil
}

// optionInProjectScope honors Jira's per-option scope. An option with no scope
// is available in every project, which is what the absence of one means.
func optionInProjectScope(scope json.RawMessage, projectID string) (bool, error) {
	if len(scope) == 0 || projectID == "" {
		return true, nil
	}
	var config struct {
		Scope *struct {
			Global   *struct{} `json:"global"`
			Projects []string  `json:"projects"`
		} `json:"scope"`
	}
	if err := json.Unmarshal(scope, &config); err != nil {
		return false, fmt.Errorf("%w: the config is not valid JSON", ErrAppFieldOption)
	}
	if config.Scope == nil || config.Scope.Global != nil || len(config.Scope.Projects) == 0 {
		return true, nil
	}
	for _, scoped := range config.Scope.Projects {
		if scoped == projectID {
			return true, nil
		}
	}
	return false, nil
}

// AppFieldOptionIssues finds the work items an option is selected on, so the
// deselect can be queued as an ordinary bulk edit rather than a special path.
func (s *Store) AppFieldOptionIssues(ctx context.Context, workspaceID, fieldKey, optionID string, issueIDs []string) ([]BulkIssueTaskItem, string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fieldID, contextID, err := appSelectField(ctx, tx, workspaceID, fieldKey)
	if err != nil {
		return nil, "", err
	}
	if _, err = scanAppFieldOption(tx.QueryRow(ctx, `SELECT `+appFieldOptionColumns+`
		FROM custom_field_options o WHERE o.context_id=$1 AND o.id::text=$2`, contextID, optionID)); err != nil {
		return nil, "", err
	}
	rows, err := tx.Query(ctx, `SELECT id, jira_id FROM issues
		WHERE workspace_id=$1 AND fields->>$2 = $3 AND ($4::text[] IS NULL OR id = ANY($4))
		ORDER BY jira_id`, workspaceID, fieldID, optionID, nullableIDs(issueIDs))
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []BulkIssueTaskItem{}
	for rows.Next() {
		var item BulkIssueTaskItem
		if err = rows.Scan(&item.ID, &item.JiraID); err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	return items, fieldID, rows.Err()
}
