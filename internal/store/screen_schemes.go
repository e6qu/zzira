package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// screenSchemeOperations is Jira's fixed operation set. "default" is required
// on every scheme; the others fall back to it when unmapped.
var screenSchemeOperations = []string{"default", "create", "edit", "view"}

// DefaultIssueTypeMapping is Jira's sentinel for a scheme's fallback work type.
const DefaultIssueTypeMapping = "default"

func validScreenSchemeOperation(operation string) bool {
	for _, known := range screenSchemeOperations {
		if known == operation {
			return true
		}
	}
	return false
}

func scanScreenScheme(row pgx.Row) (*models.ScreenScheme, error) {
	scheme := &models.ScreenScheme{Screens: map[string]string{}}
	if err := row.Scan(&scheme.ID, &scheme.Name, &scheme.Description, &scheme.IsDefault); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrScreenNotFound
		}
		return nil, err
	}
	return scheme, nil
}

const screenSchemeColumns = `id::text,name,description,is_default`

func loadScreenSchemeItemsTx(ctx context.Context, tx pgx.Tx, workspaceID string, schemes []*models.ScreenScheme) error {
	if len(schemes) == 0 {
		return nil
	}
	byID := map[string]*models.ScreenScheme{}
	ids := make([]int64, 0, len(schemes))
	for _, scheme := range schemes {
		byID[scheme.ID] = scheme
		ids = append(ids, mustParseScreenID(scheme.ID))
	}
	rows, err := tx.Query(ctx, `SELECT scheme_id::text,operation,screen_id::text FROM screen_scheme_items
		WHERE workspace_id=$1 AND scheme_id = ANY($2)`, workspaceID, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var schemeID, operation, screenID string
		if err = rows.Scan(&schemeID, &operation, &screenID); err != nil {
			return err
		}
		if scheme, ok := byID[schemeID]; ok {
			scheme.Screens[operation] = screenID
		}
	}
	return rows.Err()
}

func screenSchemeTx(ctx context.Context, tx pgx.Tx, workspaceID, schemeID string) (*models.ScreenScheme, error) {
	id, err := strconv.ParseInt(schemeID, 10, 64)
	if err != nil {
		return nil, ErrScreenNotFound
	}
	scheme, err := scanScreenScheme(tx.QueryRow(ctx, `SELECT `+screenSchemeColumns+`
		FROM screen_schemes WHERE workspace_id=$1 AND id=$2`, workspaceID, id))
	if err != nil {
		return nil, err
	}
	return scheme, loadScreenSchemeItemsTx(ctx, tx, workspaceID, []*models.ScreenScheme{scheme})
}

// ScreenSchemes lists workspace screen schemes with their operation mappings.
func (s *Store) ScreenSchemes(ctx context.Context, workspaceID string, ids []string) ([]*models.ScreenScheme, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT `+screenSchemeColumns+` FROM screen_schemes WHERE workspace_id=$1 ORDER BY id`, workspaceID)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	schemes := []*models.ScreenScheme{}
	for rows.Next() {
		scheme, scanErr := scanScreenScheme(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		if len(allowed) == 0 || allowed[scheme.ID] {
			schemes = append(schemes, scheme)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return schemes, loadScreenSchemeItemsTx(ctx, tx, workspaceID, schemes)
}

func writeScreenSchemeItemsTx(ctx context.Context, tx pgx.Tx, workspaceID, schemeID string, screens map[string]string) error {
	for operation, screenID := range screens {
		if !validScreenSchemeOperation(operation) {
			return fmt.Errorf("%w: screen scheme operation %q is unsupported", ErrScreenValidation, operation)
		}
		if screenID == "" {
			if _, err := tx.Exec(ctx, `DELETE FROM screen_scheme_items WHERE scheme_id=$1 AND operation=$2`, schemeID, operation); err != nil {
				return err
			}
			continue
		}
		if _, err := screenTx(ctx, tx, workspaceID, screenID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO screen_scheme_items(workspace_id,scheme_id,operation,screen_id)
			VALUES($1,$2,$3,$4) ON CONFLICT (scheme_id,operation) DO UPDATE SET screen_id=EXCLUDED.screen_id`,
			workspaceID, schemeID, operation, screenID); err != nil {
			return err
		}
	}
	var hasDefault bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM screen_scheme_items WHERE scheme_id=$1 AND operation='default')`, schemeID).Scan(&hasDefault); err != nil {
		return err
	}
	if !hasDefault {
		return fmt.Errorf("%w: a screen scheme requires a default screen", ErrScreenValidation)
	}
	return nil
}

func (s *Store) CreateScreenScheme(ctx context.Context, workspaceID, actorID, name, description string, screens map[string]string) (*models.ScreenScheme, error) {
	if err := validateScreenName(name, "screen scheme"); err != nil {
		return nil, err
	}
	if len(description) > 255 {
		return nil, fmt.Errorf("%w: screen scheme description accepts at most 255 characters", ErrScreenValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	scheme, err := scanScreenScheme(tx.QueryRow(ctx, `INSERT INTO screen_schemes(workspace_id,name,description)
		VALUES($1,$2,$3) RETURNING `+screenSchemeColumns, workspaceID, name, description))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a screen scheme with this name already exists", ErrScreenConflict)
	}
	if err != nil {
		return nil, err
	}
	if err = writeScreenSchemeItemsTx(ctx, tx, workspaceID, scheme.ID, screens); err != nil {
		return nil, err
	}
	if scheme, err = screenSchemeTx(ctx, tx, workspaceID, scheme.ID); err != nil {
		return nil, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "screen_scheme", scheme.ID, models.OpUpsert, scheme); err != nil {
		return nil, err
	}
	return scheme, tx.Commit(ctx)
}

func (s *Store) UpdateScreenScheme(ctx context.Context, workspaceID, actorID, schemeID string, name, description *string, screens map[string]string) (*models.ScreenScheme, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	scheme, err := screenSchemeTx(ctx, tx, workspaceID, schemeID)
	if err != nil {
		return nil, err
	}
	if name != nil {
		if err = validateScreenName(*name, "screen scheme"); err != nil {
			return nil, err
		}
		scheme.Name = *name
	}
	if description != nil {
		if len(*description) > 255 {
			return nil, fmt.Errorf("%w: screen scheme description accepts at most 255 characters", ErrScreenValidation)
		}
		scheme.Description = *description
	}
	_, err = tx.Exec(ctx, `UPDATE screen_schemes SET name=$3,description=$4,updated_at=now() WHERE workspace_id=$1 AND id=$2`,
		workspaceID, scheme.ID, scheme.Name, scheme.Description)
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a screen scheme with this name already exists", ErrScreenConflict)
	}
	if err != nil {
		return nil, err
	}
	if err = writeScreenSchemeItemsTx(ctx, tx, workspaceID, scheme.ID, screens); err != nil {
		return nil, err
	}
	if scheme, err = screenSchemeTx(ctx, tx, workspaceID, scheme.ID); err != nil {
		return nil, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "screen_scheme", scheme.ID, models.OpUpsert, scheme); err != nil {
		return nil, err
	}
	return scheme, tx.Commit(ctx)
}

func (s *Store) DeleteScreenScheme(ctx context.Context, workspaceID, actorID, schemeID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	scheme, err := screenSchemeTx(ctx, tx, workspaceID, schemeID)
	if err != nil {
		return err
	}
	if scheme.IsDefault {
		return fmt.Errorf("%w: the default screen scheme cannot be deleted", ErrScreenConflict)
	}
	var used int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM issue_type_screen_scheme_items WHERE screen_scheme_id=$1`, scheme.ID).Scan(&used); err != nil {
		return err
	}
	if used > 0 {
		return fmt.Errorf("%w: remove this screen scheme from every work type mapping first", ErrScreenConflict)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM screen_schemes WHERE workspace_id=$1 AND id=$2`, workspaceID, scheme.ID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "screen_scheme", scheme.ID, models.OpDelete, scheme); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
