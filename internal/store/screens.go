package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var (
	ErrScreenValidation = errors.New("screen request is invalid")
	ErrScreenNotFound   = errors.New("screen, tab, or field does not exist")
	ErrScreenConflict   = errors.New("screen is in use")
)

// systemScreenFields is the ordered catalog of built-in fields an administrator
// may place on a screen. It mirrors the system fields IssueCreateMetadata
// builds, so a screen can never reference a field the issue forms cannot render.
var systemScreenFields = []models.ScreenField{
	{ID: "summary", Name: "Summary"},
	{ID: "description", Name: "Description"},
	{ID: "assignee", Name: "Assignee"},
	{ID: "priority", Name: "Priority"},
	{ID: "labels", Name: "Labels"},
	{ID: "parent", Name: "Parent"},
	{ID: "components", Name: "Components"},
	{ID: "fixVersions", Name: "Fix versions"},
	{ID: "versions", Name: "Affects versions"},
	{ID: "security", Name: "Restrict to"},
	{ID: "issuetype", Name: "Issue type"},
	{ID: "project", Name: "Project"},
}

type ScreenFilter struct {
	IDs         []string
	QueryString string
}

// ScreenFieldCatalog lists every field that may be placed on a screen: the
// built-in fields followed by the workspace's custom fields.
func (s *Store) ScreenFieldCatalog(ctx context.Context, workspaceID string) ([]models.ScreenField, error) {
	catalog := make([]models.ScreenField, 0, len(systemScreenFields))
	catalog = append(catalog, systemScreenFields...)
	custom, err := s.CustomFieldsForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, field := range custom {
		catalog = append(catalog, models.ScreenField{ID: field.ID, Name: field.Name, Custom: true})
	}
	return catalog, nil
}

func screenFieldCatalogTx(ctx context.Context, tx pgx.Tx, workspaceID string) (map[string]models.ScreenField, error) {
	catalog := map[string]models.ScreenField{}
	for _, field := range systemScreenFields {
		catalog[field.ID] = field
	}
	rows, err := tx.Query(ctx, `SELECT id,name FROM custom_fields WHERE workspace_id=$1 AND trashed_at IS NULL`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var field models.ScreenField
		if err = rows.Scan(&field.ID, &field.Name); err != nil {
			return nil, err
		}
		field.Custom = true
		catalog[field.ID] = field
	}
	return catalog, rows.Err()
}

func validateScreenName(name string, kind string) error {
	if name == "" || name != strings.TrimSpace(name) || len(name) > 255 {
		return fmt.Errorf("%w: %s name must contain 1 to 255 characters and cannot begin or end with whitespace", ErrScreenValidation, kind)
	}
	return nil
}

func scanScreen(row pgx.Row) (*models.Screen, error) {
	screen := &models.Screen{}
	if err := row.Scan(&screen.ID, &screen.WorkspaceID, &screen.Name, &screen.Description, &screen.IsDefault); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrScreenNotFound
		}
		return nil, err
	}
	return screen, nil
}

const screenColumns = `id::text,workspace_id,name,description,is_default`

func screenTx(ctx context.Context, tx pgx.Tx, workspaceID, screenID string) (*models.Screen, error) {
	id, err := strconv.ParseInt(screenID, 10, 64)
	if err != nil {
		return nil, ErrScreenNotFound
	}
	return scanScreen(tx.QueryRow(ctx, `SELECT `+screenColumns+` FROM screens WHERE workspace_id=$1 AND id=$2`, workspaceID, id))
}

func (s *Store) Screen(ctx context.Context, workspaceID, screenID string) (*models.Screen, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return screenTx(ctx, tx, workspaceID, screenID)
}

// Screens lists workspace screens, optionally filtered by ID or a
// case-insensitive substring of the name or description.
func (s *Store) Screens(ctx context.Context, workspaceID string, filter ScreenFilter) ([]*models.Screen, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+screenColumns+` FROM screens WHERE workspace_id=$1 ORDER BY id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	allowed := map[string]bool{}
	for _, id := range filter.IDs {
		allowed[id] = true
	}
	needle := strings.ToLower(strings.TrimSpace(filter.QueryString))
	screens := []*models.Screen{}
	for rows.Next() {
		screen, scanErr := scanScreen(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		if len(allowed) > 0 && !allowed[screen.ID] {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(screen.Name), needle) &&
			!strings.Contains(strings.ToLower(screen.Description), needle) {
			continue
		}
		screens = append(screens, screen)
	}
	return screens, rows.Err()
}

// ScreensForField lists the screens that currently show one field.
func (s *Store) ScreensForField(ctx context.Context, workspaceID, fieldID string) ([]*models.Screen, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+screenColumns+` FROM screens
		WHERE workspace_id=$1 AND EXISTS(
			SELECT 1 FROM screen_tab_fields f WHERE f.screen_id=screens.id AND f.field_id=$2)
		ORDER BY id`, workspaceID, fieldID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	screens := []*models.Screen{}
	for rows.Next() {
		screen, scanErr := scanScreen(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		screens = append(screens, screen)
	}
	return screens, rows.Err()
}

func screenTabsTx(ctx context.Context, tx pgx.Tx, workspaceID string, screenIDs []int64) ([]models.ScreenTab, error) {
	rows, err := tx.Query(ctx, `SELECT id::text,screen_id::text,name,position FROM screen_tabs
		WHERE workspace_id=$1 AND screen_id = ANY($2) ORDER BY screen_id,position,id`, workspaceID, screenIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tabs := []models.ScreenTab{}
	for rows.Next() {
		var tab models.ScreenTab
		if err = rows.Scan(&tab.ID, &tab.ScreenID, &tab.Name, &tab.Position); err != nil {
			return nil, err
		}
		tabs = append(tabs, tab)
	}
	return tabs, rows.Err()
}

// ScreenTabs returns one screen's tabs in display order.
func (s *Store) ScreenTabs(ctx context.Context, workspaceID, screenID string) ([]models.ScreenTab, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = screenTx(ctx, tx, workspaceID, screenID); err != nil {
		return nil, err
	}
	id, _ := strconv.ParseInt(screenID, 10, 64)
	return screenTabsTx(ctx, tx, workspaceID, []int64{id})
}

// BulkScreenTabs returns tabs for several screens at once, keyed by screen ID.
func (s *Store) BulkScreenTabs(ctx context.Context, workspaceID string, screenIDs []string) (map[string][]models.ScreenTab, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ids := []int64{}
	if len(screenIDs) == 0 {
		rows, queryErr := tx.Query(ctx, `SELECT id FROM screens WHERE workspace_id=$1`, workspaceID)
		if queryErr != nil {
			return nil, queryErr
		}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}
	for _, raw := range screenIDs {
		id, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			return nil, ErrScreenNotFound
		}
		ids = append(ids, id)
	}
	tabs, err := screenTabsTx(ctx, tx, workspaceID, ids)
	if err != nil {
		return nil, err
	}
	grouped := map[string][]models.ScreenTab{}
	for _, tab := range tabs {
		grouped[tab.ScreenID] = append(grouped[tab.ScreenID], tab)
	}
	return grouped, nil
}

func screenTabTx(ctx context.Context, tx pgx.Tx, workspaceID, screenID, tabID string) (models.ScreenTab, error) {
	screen, tab := int64(0), int64(0)
	var err error
	if screen, err = strconv.ParseInt(screenID, 10, 64); err != nil {
		return models.ScreenTab{}, ErrScreenNotFound
	}
	if tab, err = strconv.ParseInt(tabID, 10, 64); err != nil {
		return models.ScreenTab{}, ErrScreenNotFound
	}
	var found models.ScreenTab
	err = tx.QueryRow(ctx, `SELECT id::text,screen_id::text,name,position FROM screen_tabs
		WHERE workspace_id=$1 AND screen_id=$2 AND id=$3`, workspaceID, screen, tab).
		Scan(&found.ID, &found.ScreenID, &found.Name, &found.Position)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.ScreenTab{}, ErrScreenNotFound
	}
	return found, err
}

func screenTabFieldsTx(ctx context.Context, tx pgx.Tx, workspaceID, tabID string, catalog map[string]models.ScreenField) ([]models.ScreenField, error) {
	rows, err := tx.Query(ctx, `SELECT field_id,position FROM screen_tab_fields
		WHERE workspace_id=$1 AND tab_id=$2 ORDER BY position,field_id`, workspaceID, tabID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := []models.ScreenField{}
	for rows.Next() {
		var field models.ScreenField
		if err = rows.Scan(&field.ID, &field.Position); err != nil {
			return nil, err
		}
		if known, ok := catalog[field.ID]; ok {
			field.Name, field.Custom = known.Name, known.Custom
		} else {
			field.Name = field.ID
		}
		fields = append(fields, field)
	}
	return fields, rows.Err()
}

// ScreenTabFields returns one tab's fields in display order.
func (s *Store) ScreenTabFields(ctx context.Context, workspaceID, screenID, tabID string) ([]models.ScreenField, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tab, err := screenTabTx(ctx, tx, workspaceID, screenID, tabID)
	if err != nil {
		return nil, err
	}
	catalog, err := screenFieldCatalogTx(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	return screenTabFieldsTx(ctx, tx, workspaceID, tab.ID, catalog)
}

// ExpandedScreen loads a screen with every tab and field, for the admin UI.
func (s *Store) ExpandedScreen(ctx context.Context, workspaceID, screenID string) (*models.Screen, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	screen, err := screenTx(ctx, tx, workspaceID, screenID)
	if err != nil {
		return nil, err
	}
	catalog, err := screenFieldCatalogTx(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	id, _ := strconv.ParseInt(screen.ID, 10, 64)
	tabs, err := screenTabsTx(ctx, tx, workspaceID, []int64{id})
	if err != nil {
		return nil, err
	}
	for index := range tabs {
		fields, fieldErr := screenTabFieldsTx(ctx, tx, workspaceID, tabs[index].ID, catalog)
		if fieldErr != nil {
			return nil, fieldErr
		}
		tabs[index].Fields = fields
	}
	screen.Tabs = tabs
	return screen, nil
}

// AvailableScreenFields lists catalog fields the screen does not already show.
func (s *Store) AvailableScreenFields(ctx context.Context, workspaceID, screenID string) ([]models.ScreenField, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	screen, err := screenTx(ctx, tx, workspaceID, screenID)
	if err != nil {
		return nil, err
	}
	catalog, err := screenFieldCatalogTx(ctx, tx, workspaceID)
	if err != nil {
		return nil, err
	}
	used, err := screenFieldSetTx(ctx, tx, screen.ID)
	if err != nil {
		return nil, err
	}
	available := []models.ScreenField{}
	for _, field := range systemScreenFields {
		if !used[field.ID] {
			available = append(available, catalog[field.ID])
		}
	}
	custom := []models.ScreenField{}
	for id, field := range catalog {
		if field.Custom && !used[id] {
			custom = append(custom, field)
		}
	}
	sortScreenFields(custom)
	return append(available, custom...), nil
}

func screenFieldSetTx(ctx context.Context, tx pgx.Tx, screenID string) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `SELECT field_id FROM screen_tab_fields WHERE screen_id=$1`, screenID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	used := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		used[id] = true
	}
	return used, rows.Err()
}

func sortScreenFields(fields []models.ScreenField) {
	sort.Slice(fields, func(i, j int) bool {
		if strings.EqualFold(fields[i].Name, fields[j].Name) {
			return fields[i].ID < fields[j].ID
		}
		return strings.ToLower(fields[i].Name) < strings.ToLower(fields[j].Name)
	})
}

func (s *Store) CreateScreen(ctx context.Context, workspaceID, actorID, name, description string) (*models.Screen, error) {
	if err := validateScreenName(name, "screen"); err != nil {
		return nil, err
	}
	if len(description) > 255 {
		return nil, fmt.Errorf("%w: screen description accepts at most 255 characters", ErrScreenValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	screen, err := scanScreen(tx.QueryRow(ctx, `INSERT INTO screens(workspace_id,name,description)
		VALUES($1,$2,$3) RETURNING `+screenColumns, workspaceID, name, description))
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a screen with this name already exists", ErrScreenConflict)
	}
	if err != nil {
		return nil, err
	}
	// Jira creates every screen with one tab so fields have somewhere to land.
	if _, err = tx.Exec(ctx, `INSERT INTO screen_tabs(workspace_id,screen_id,name,position) VALUES($1,$2,'Field Tab',0)`,
		workspaceID, screen.ID); err != nil {
		return nil, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "screen", screen.ID, models.OpUpsert, screen); err != nil {
		return nil, err
	}
	return screen, tx.Commit(ctx)
}

func (s *Store) UpdateScreen(ctx context.Context, workspaceID, actorID, screenID string, name, description *string) (*models.Screen, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return nil, err
	}
	screen, err := screenTx(ctx, tx, workspaceID, screenID)
	if err != nil {
		return nil, err
	}
	if name != nil {
		if err = validateScreenName(*name, "screen"); err != nil {
			return nil, err
		}
		screen.Name = *name
	}
	if description != nil {
		if len(*description) > 255 {
			return nil, fmt.Errorf("%w: screen description accepts at most 255 characters", ErrScreenValidation)
		}
		screen.Description = *description
	}
	_, err = tx.Exec(ctx, `UPDATE screens SET name=$3,description=$4,updated_at=now() WHERE workspace_id=$1 AND id=$2`,
		workspaceID, screen.ID, screen.Name, screen.Description)
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a screen with this name already exists", ErrScreenConflict)
	}
	if err != nil {
		return nil, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "screen", screen.ID, models.OpUpsert, screen); err != nil {
		return nil, err
	}
	return screen, tx.Commit(ctx)
}

func (s *Store) DeleteScreen(ctx context.Context, workspaceID, actorID, screenID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	screen, err := screenTx(ctx, tx, workspaceID, screenID)
	if err != nil {
		return err
	}
	if screen.IsDefault {
		return fmt.Errorf("%w: the default screen cannot be deleted", ErrScreenConflict)
	}
	var schemes int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM screen_scheme_items WHERE screen_id=$1`, screen.ID).Scan(&schemes); err != nil {
		return err
	}
	if schemes > 0 {
		return fmt.Errorf("%w: remove this screen from every screen scheme first", ErrScreenConflict)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM screens WHERE workspace_id=$1 AND id=$2`, workspaceID, screen.ID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "screen", screen.ID, models.OpDelete, screen); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AddFieldToDefaultScreen places one field on the workspace default screen's
// first tab, which is what Jira's addToDefault endpoint does.
func (s *Store) AddFieldToDefaultScreen(ctx context.Context, workspaceID, actorID, fieldID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	var screenID, tabID string
	err = tx.QueryRow(ctx, `SELECT s.id::text,t.id::text FROM screens s
		JOIN screen_tabs t ON t.screen_id=s.id
		WHERE s.workspace_id=$1 AND s.is_default ORDER BY t.position,t.id LIMIT 1`, workspaceID).Scan(&screenID, &tabID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrScreenNotFound
	}
	if err != nil {
		return err
	}
	if err = addScreenTabFieldTx(ctx, tx, workspaceID, screenID, tabID, fieldID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "screen_tab_field", fieldID, models.OpUpsert,
		map[string]any{"screenId": screenID, "tabId": tabID, "fieldId": fieldID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) AddScreenTab(ctx context.Context, workspaceID, actorID, screenID, name string) (models.ScreenTab, error) {
	if err := validateScreenName(name, "tab"); err != nil {
		return models.ScreenTab{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return models.ScreenTab{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return models.ScreenTab{}, err
	}
	screen, err := screenTx(ctx, tx, workspaceID, screenID)
	if err != nil {
		return models.ScreenTab{}, err
	}
	var tab models.ScreenTab
	err = tx.QueryRow(ctx, `INSERT INTO screen_tabs(workspace_id,screen_id,name,position)
		VALUES($1,$2,$3,COALESCE((SELECT max(position)+1 FROM screen_tabs WHERE screen_id=$2),0))
		RETURNING id::text,screen_id::text,name,position`, workspaceID, screen.ID, name).
		Scan(&tab.ID, &tab.ScreenID, &tab.Name, &tab.Position)
	if isUniqueViolation(err) {
		return models.ScreenTab{}, fmt.Errorf("%w: a tab with this name already exists on the screen", ErrScreenConflict)
	}
	if err != nil {
		return models.ScreenTab{}, err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "screen_tab", tab.ID, models.OpUpsert, tab); err != nil {
		return models.ScreenTab{}, err
	}
	return tab, tx.Commit(ctx)
}

func (s *Store) RenameScreenTab(ctx context.Context, workspaceID, actorID, screenID, tabID, name string) (models.ScreenTab, error) {
	if err := validateScreenName(name, "tab"); err != nil {
		return models.ScreenTab{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return models.ScreenTab{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return models.ScreenTab{}, err
	}
	tab, err := screenTabTx(ctx, tx, workspaceID, screenID, tabID)
	if err != nil {
		return models.ScreenTab{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE screen_tabs SET name=$3 WHERE workspace_id=$1 AND id=$2`, workspaceID, tab.ID, name)
	if isUniqueViolation(err) {
		return models.ScreenTab{}, fmt.Errorf("%w: a tab with this name already exists on the screen", ErrScreenConflict)
	}
	if err != nil {
		return models.ScreenTab{}, err
	}
	tab.Name = name
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "screen_tab", tab.ID, models.OpUpsert, tab); err != nil {
		return models.ScreenTab{}, err
	}
	return tab, tx.Commit(ctx)
}

func (s *Store) DeleteScreenTab(ctx context.Context, workspaceID, actorID, screenID, tabID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	tab, err := screenTabTx(ctx, tx, workspaceID, screenID, tabID)
	if err != nil {
		return err
	}
	var remaining int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM screen_tabs WHERE screen_id=$1`, tab.ScreenID).Scan(&remaining); err != nil {
		return err
	}
	if remaining <= 1 {
		return fmt.Errorf("%w: a screen keeps at least one tab", ErrScreenConflict)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM screen_tabs WHERE workspace_id=$1 AND id=$2`, workspaceID, tab.ID); err != nil {
		return err
	}
	if err = renumberScreenTabsTx(ctx, tx, tab.ScreenID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "screen_tab", tab.ID, models.OpDelete, tab); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func renumberScreenTabsTx(ctx context.Context, tx pgx.Tx, screenID string) error {
	_, err := tx.Exec(ctx, `UPDATE screen_tabs SET position=ordered.rank FROM (
		SELECT id, (row_number() OVER (ORDER BY position,id))-1 AS rank
		FROM screen_tabs WHERE screen_id=$1) ordered
		WHERE screen_tabs.id=ordered.id AND screen_tabs.position IS DISTINCT FROM ordered.rank`, screenID)
	return err
}

func (s *Store) MoveScreenTab(ctx context.Context, workspaceID, actorID, screenID, tabID string, position int) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	tab, err := screenTabTx(ctx, tx, workspaceID, screenID, tabID)
	if err != nil {
		return err
	}
	tabs, err := screenTabsTx(ctx, tx, workspaceID, []int64{mustParseScreenID(tab.ScreenID)})
	if err != nil {
		return err
	}
	if position < 0 || position >= len(tabs) {
		return fmt.Errorf("%w: tab position must be between 0 and %d", ErrScreenValidation, len(tabs)-1)
	}
	order := make([]string, 0, len(tabs))
	for _, existing := range tabs {
		if existing.ID != tab.ID {
			order = append(order, existing.ID)
		}
	}
	order = append(order[:position], append([]string{tab.ID}, order[position:]...)...)
	if err = writeScreenTabOrderTx(ctx, tx, workspaceID, order); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "screen_tab", tab.ID, models.OpUpsert,
		map[string]any{"screenId": tab.ScreenID, "tabId": tab.ID, "position": position}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func mustParseScreenID(value string) int64 {
	id, _ := strconv.ParseInt(value, 10, 64)
	return id
}

func writeScreenTabOrderTx(ctx context.Context, tx pgx.Tx, workspaceID string, order []string) error {
	for index, id := range order {
		if _, err := tx.Exec(ctx, `UPDATE screen_tabs SET position=$3 WHERE workspace_id=$1 AND id=$2`,
			workspaceID, id, index); err != nil {
			return err
		}
	}
	return nil
}

func addScreenTabFieldTx(ctx context.Context, tx pgx.Tx, workspaceID, screenID, tabID, fieldID string) error {
	fieldID = strings.TrimSpace(fieldID)
	catalog, err := screenFieldCatalogTx(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	if _, known := catalog[fieldID]; !known {
		return fmt.Errorf("%w: field %q does not exist", ErrScreenValidation, fieldID)
	}
	_, err = tx.Exec(ctx, `INSERT INTO screen_tab_fields(workspace_id,screen_id,tab_id,field_id,position)
		VALUES($1,$2,$3,$4,COALESCE((SELECT max(position)+1 FROM screen_tab_fields WHERE tab_id=$3),0))`,
		workspaceID, screenID, tabID, fieldID)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: the field is already on this screen", ErrScreenConflict)
	}
	return err
}

func (s *Store) AddScreenTabField(ctx context.Context, workspaceID, actorID, screenID, tabID, fieldID string) (models.ScreenField, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return models.ScreenField{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return models.ScreenField{}, err
	}
	tab, err := screenTabTx(ctx, tx, workspaceID, screenID, tabID)
	if err != nil {
		return models.ScreenField{}, err
	}
	if err = addScreenTabFieldTx(ctx, tx, workspaceID, tab.ScreenID, tab.ID, fieldID); err != nil {
		return models.ScreenField{}, err
	}
	catalog, err := screenFieldCatalogTx(ctx, tx, workspaceID)
	if err != nil {
		return models.ScreenField{}, err
	}
	field := catalog[strings.TrimSpace(fieldID)]
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "screen_tab_field", field.ID, models.OpUpsert,
		map[string]any{"screenId": tab.ScreenID, "tabId": tab.ID, "fieldId": field.ID}); err != nil {
		return models.ScreenField{}, err
	}
	return field, tx.Commit(ctx)
}

func (s *Store) RemoveScreenTabField(ctx context.Context, workspaceID, actorID, screenID, tabID, fieldID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	tab, err := screenTabTx(ctx, tx, workspaceID, screenID, tabID)
	if err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `DELETE FROM screen_tab_fields WHERE workspace_id=$1 AND tab_id=$2 AND field_id=$3`,
		workspaceID, tab.ID, fieldID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrScreenNotFound
	}
	if err = renumberScreenTabFieldsTx(ctx, tx, workspaceID, tab.ID); err != nil {
		return err
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "screen_tab_field", fieldID, models.OpDelete,
		map[string]any{"screenId": tab.ScreenID, "tabId": tab.ID, "fieldId": fieldID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func renumberScreenTabFieldsTx(ctx context.Context, tx pgx.Tx, workspaceID, tabID string) error {
	_, err := tx.Exec(ctx, `UPDATE screen_tab_fields SET position=ordered.rank FROM (
		SELECT field_id, (row_number() OVER (ORDER BY position,field_id))-1 AS rank
		FROM screen_tab_fields WHERE tab_id=$2) ordered
		WHERE screen_tab_fields.tab_id=$2 AND screen_tab_fields.field_id=ordered.field_id
		AND screen_tab_fields.workspace_id=$1 AND screen_tab_fields.position IS DISTINCT FROM ordered.rank`,
		workspaceID, tabID)
	return err
}

// MoveScreenTabField applies Jira's move request: an explicit "after" field, or
// a relative First/Earlier/Later/Last position.
func (s *Store) MoveScreenTabField(ctx context.Context, workspaceID, actorID, screenID, tabID, fieldID, after, position string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, workspaceID, actorID); err != nil {
		return err
	}
	tab, err := screenTabTx(ctx, tx, workspaceID, screenID, tabID)
	if err != nil {
		return err
	}
	catalog, err := screenFieldCatalogTx(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	fields, err := screenTabFieldsTx(ctx, tx, workspaceID, tab.ID, catalog)
	if err != nil {
		return err
	}
	current := -1
	for index, field := range fields {
		if field.ID == fieldID {
			current = index
		}
	}
	if current < 0 {
		return ErrScreenNotFound
	}
	target, err := screenFieldTargetIndex(fields, current, after, position)
	if err != nil {
		return err
	}
	order := make([]string, 0, len(fields))
	for index, field := range fields {
		if index != current {
			order = append(order, field.ID)
		}
	}
	order = append(order[:target], append([]string{fieldID}, order[target:]...)...)
	for index, id := range order {
		if _, err = tx.Exec(ctx, `UPDATE screen_tab_fields SET position=$3 WHERE workspace_id=$1 AND tab_id=$2 AND field_id=$4`,
			workspaceID, tab.ID, index, id); err != nil {
			return err
		}
	}
	if err = appendProjectGovernanceAction(ctx, tx, workspaceID, actorID, "screen_tab_field", fieldID, models.OpUpsert,
		map[string]any{"screenId": tab.ScreenID, "tabId": tab.ID, "fieldId": fieldID, "position": target}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// screenFieldTargetIndex resolves a move request to an index in the list with
// the moved field already removed.
func screenFieldTargetIndex(fields []models.ScreenField, current int, after, position string) (int, error) {
	last := len(fields) - 1
	if after != "" {
		for index, field := range fields {
			if field.ID != after {
				continue
			}
			if field.ID == fields[current].ID {
				return 0, fmt.Errorf("%w: a field cannot be moved after itself", ErrScreenValidation)
			}
			if index > current {
				return index, nil
			}
			return index + 1, nil
		}
		return 0, fmt.Errorf("%w: the field to move after is not on this tab", ErrScreenValidation)
	}
	switch strings.ToLower(position) {
	case "first":
		return 0, nil
	case "last":
		return last, nil
	case "earlier":
		if current == 0 {
			return 0, nil
		}
		return current - 1, nil
	case "later":
		if current >= last {
			return last, nil
		}
		return current + 1, nil
	}
	return 0, fmt.Errorf("%w: move requires after or position First, Earlier, Later, or Last", ErrScreenValidation)
}
