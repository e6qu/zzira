package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// A Confluence space is created, renamed, given a homepage, archived and
// deleted; it carries per-space settings and may select a theme. A personal
// space belongs to one person and is keyed by their account.

const apiTaskWikiDeleteSpace = "wiki-delete-space"

// DefaultWikiThemeKey is what a site shows when no theme is chosen, which is
// why Confluence leaves it out of the list of themes to choose from.
const DefaultWikiThemeKey = "com.atlassian.confluence.plugins.confluence-default-theme:default"

// wikiThemes is the set of themes a space may select.
var wikiThemes = map[string]models.WikiTheme{
	"com.atlassian.confluence.plugins.confluence-default-theme:default": {
		Key: "com.atlassian.confluence.plugins.confluence-default-theme:default", Name: "Default theme",
		Description: "The default Confluence theme.",
	},
	"com.atlassian.confluence.plugins.confluence-documentation-theme:documentation": {
		Key: "com.atlassian.confluence.plugins.confluence-documentation-theme:documentation", Name: "Documentation theme",
		Description: "A theme for documentation spaces, with the page tree in the sidebar.",
	},
	"com.atlassian.confluence.plugins.confluence-dark-theme:dark": {
		Key: "com.atlassian.confluence.plugins.confluence-dark-theme:dark", Name: "Dark theme",
		Description: "A dark look and feel for a space.",
	},
}

// PersonalSpaceKey is how Confluence keys the space belonging to one person.
func PersonalSpaceKey(accountID string) string { return "~" + accountID }

// CreateWikiSpaceInput is what a space is created from.
type CreateWikiSpaceInput struct {
	Key         string
	Alias       string
	Name        string
	Description string
	Private     bool
	Personal    bool
	OwnerID     string
}

// CreateWikiSpaceFull creates a space. A private one is visible to its creator
// alone, which Confluence does by granting the creator everything and nobody
// else anything; a personal one belongs to the person it is keyed for.
func (s *Store) CreateWikiSpaceFull(ctx context.Context, ws, actor string, input CreateWikiSpaceInput) (*models.WikiSpace, error) {
	key := strings.TrimSpace(input.Key)
	if input.Personal && input.OwnerID != "" {
		key = PersonalSpaceKey(input.OwnerID)
	}
	if err := validateSpaceKey(key); err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 255 {
		return nil, fmt.Errorf("%w: a space name of 1 to 255 characters is required", ErrWikiValidation)
	}
	spaceType := "global"
	if input.Personal || strings.HasPrefix(key, "~") {
		spaceType = "personal"
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = projectAdmin(ctx, tx, ws, actor); err != nil {
		return nil, err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO wiki_spaces(workspace_id,key,name,description,author_id,private,space_type,alias)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`,
		ws, key, input.Name, input.Description, actor, input.Private, spaceType, input.Alias).Scan(&id)
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: a space with this key already exists", ErrWikiValidation)
	}
	if err != nil {
		return nil, err
	}
	if input.Private {
		// Confluence's private space is the ordinary create with permissions
		// set to the creator alone, so it is written the same way here.
		for _, permission := range wikiSpacePermissionCatalogue() {
			if _, err = tx.Exec(ctx, `INSERT INTO wiki_space_permission_grants(space_id,subject_type,subject_id,permission)
				VALUES($1::bigint,'user',$2,$3) ON CONFLICT DO NOTHING`, id, actor, permission); err != nil {
				return nil, err
			}
		}
	}
	space, err := scanWikiSpace(tx.QueryRow(ctx, wikiSpaceSelect+` WHERE s.id::text=$1`, id))
	if err != nil {
		return nil, err
	}
	if err = wikiAction(ctx, tx, ws, actor, "wiki_space", id, id, space); err != nil {
		return nil, err
	}
	return space, tx.Commit(ctx)
}

func validateSpaceKey(key string) error {
	if key == "" || len(key) > 255 {
		return fmt.Errorf("%w: a space key of 1 to 255 characters is required", ErrWikiValidation)
	}
	body := key
	if strings.HasPrefix(key, "~") {
		body = key[1:]
	}
	if body == "" {
		return fmt.Errorf("%w: a personal space key needs the account it belongs to", ErrWikiValidation)
	}
	for _, r := range body {
		if !(r == '_' || r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return fmt.Errorf("%w: a space key may contain only letters, digits, underscores and hyphens", ErrWikiValidation)
		}
	}
	return nil
}

// UpdateWikiSpaceInput carries the fields Confluence's space update accepts.
type UpdateWikiSpaceInput struct {
	Name        *string
	Description *string
	HomepageID  *string
	Type        *string
	Status      *string
}

// UpdateWikiSpace changes a space's name, description, homepage, type or
// status.
func (s *Store) UpdateWikiSpace(ctx context.Context, ws, actor, spaceKey string, input UpdateWikiSpaceInput) (*models.WikiSpace, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	space, err := lockSpaceForAdmin(ctx, tx, ws, actor, spaceKey)
	if err != nil {
		return nil, err
	}
	if input.Name != nil {
		if strings.TrimSpace(*input.Name) == "" || len(*input.Name) > 255 {
			return nil, fmt.Errorf("%w: a space name of 1 to 255 characters is required", ErrWikiValidation)
		}
		space.Name = *input.Name
	}
	if input.Description != nil {
		space.Description = *input.Description
	}
	if input.Type != nil {
		if *input.Type != "global" && *input.Type != "personal" {
			return nil, fmt.Errorf("%w: a space type is global or personal", ErrWikiValidation)
		}
		space.Type = *input.Type
	}
	if input.Status != nil {
		if *input.Status != "current" && *input.Status != "archived" {
			return nil, fmt.Errorf("%w: a space status is current or archived", ErrWikiValidation)
		}
		space.Status = *input.Status
	}
	if input.HomepageID != nil {
		// A homepage has to be a page in this space, or the space would point
		// somewhere its readers cannot follow.
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_pages
			WHERE id::text=$1 AND space_id::text=$2 AND status='current')`, *input.HomepageID, space.ID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("%w: the homepage must be a current page in this space", ErrWikiValidation)
		}
		space.HomepageID = *input.HomepageID
	}
	var homepage any
	if space.HomepageID != "" {
		homepage = space.HomepageID
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_spaces SET name=$2,description=$3,space_type=$4,status=$5,homepage_id=$6::bigint
		WHERE id::text=$1`, space.ID, space.Name, space.Description, space.Type, space.Status, homepage); err != nil {
		return nil, err
	}
	updated, err := scanWikiSpace(tx.QueryRow(ctx, wikiSpaceSelect+` WHERE s.id::text=$1`, space.ID))
	if err != nil {
		return nil, err
	}
	if err = wikiAction(ctx, tx, ws, actor, "wiki_space", space.ID, space.ID, updated); err != nil {
		return nil, err
	}
	return updated, tx.Commit(ctx)
}

// lockSpaceForAdmin resolves a space the caller is about to administer. A
// workspace administrator administers every space, so the lookup does not go
// through the ordinary visibility gate for them.
func lockSpaceForAdmin(ctx context.Context, tx pgx.Tx, ws, actor, spaceKey string) (*models.WikiSpace, error) {
	space, err := scanWikiSpace(tx.QueryRow(ctx, wikiSpaceSelect+` WHERE s.workspace_id=$1 AND s.key=$2 FOR UPDATE OF s`, ws, spaceKey))
	if err != nil {
		return nil, err
	}
	if err = wikiSpaceAdmin(ctx, tx, ws, actor, space.ID); err != nil {
		return nil, err
	}
	return space, nil
}

// EnqueueWikiSpaceDeletion queues the removal. Confluence deletes a space in a
// long running task and answers with the task, because a space can hold a great
// deal of content.
func (s *Store) EnqueueWikiSpaceDeletion(ctx context.Context, ws, actor, spaceKey string) (APITask, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return APITask{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	space, err := lockSpaceForAdmin(ctx, tx, ws, actor, spaceKey)
	if err != nil {
		return APITask{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return APITask{}, err
	}
	task, err := queuedAPITask(ws, actor, "Delete space", apiTaskWikiDeleteSpace,
		map[string]any{"spaceId": space.ID, "spaceKey": space.Key})
	if err != nil {
		return APITask{}, err
	}
	return task, s.enqueueAPITask(ctx, task)
}

func (s *Store) executeWikiSpaceDeletion(ctx context.Context, task APITask) error {
	var payload struct {
		SpaceID  string `json:"spaceId"`
		SpaceKey string `json:"spaceKey"`
	}
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode space deletion: %w", err)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Permanently deleting a space takes everything in it. Most of the space's
	// belongings cascade, but its pages do not, and content and page versions
	// hold the pages in place, so those come off in order first.
	for _, statement := range []string{
		`DELETE FROM wiki_content WHERE space_id::text=$1`,
		`DELETE FROM wiki_page_versions WHERE page_id IN (SELECT id FROM wiki_pages WHERE space_id::text=$1)`,
		`UPDATE wiki_spaces SET homepage_id=NULL WHERE id::text=$1`,
		`UPDATE wiki_pages SET parent_id=NULL WHERE space_id::text=$1`,
		`DELETE FROM wiki_pages WHERE space_id::text=$1`,
	} {
		if _, err = tx.Exec(ctx, statement, payload.SpaceID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_spaces WHERE id::text=$1 AND workspace_id=$2`,
		payload.SpaceID, task.WorkspaceID); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	return s.CompleteAPITask(ctx, task, "Deleted the space.", map[string]any{"spaceKey": payload.SpaceKey})
}

// WikiSpaceByKeyForAdmin reads a space for its administrator.
func (s *Store) WikiSpaceByKeyForAdmin(ctx context.Context, ws, actor, spaceKey string) (*models.WikiSpace, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return lockSpaceForAdmin(ctx, tx, ws, actor, spaceKey)
}

// SetWikiSpaceSettings changes the settings of one space.
func (s *Store) SetWikiSpaceSettings(ctx context.Context, ws, actor, spaceKey string, routeOverride *bool, contentMode *string) (*models.WikiSpace, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	space, err := lockSpaceForAdmin(ctx, tx, ws, actor, spaceKey)
	if err != nil {
		return nil, err
	}
	if routeOverride != nil {
		space.RouteOverrideEnabled = *routeOverride
	}
	if contentMode != nil {
		if *contentMode != "standard" && *contentMode != "compact" {
			return nil, fmt.Errorf("%w: contentMode is standard or compact", ErrWikiValidation)
		}
		space.ContentMode = *contentMode
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_spaces SET route_override_enabled=$2,content_mode=$3 WHERE id::text=$1`,
		space.ID, space.RouteOverrideEnabled, space.ContentMode); err != nil {
		return nil, err
	}
	updated, err := scanWikiSpace(tx.QueryRow(ctx, wikiSpaceSelect+` WHERE s.id::text=$1`, space.ID))
	if err != nil {
		return nil, err
	}
	return updated, tx.Commit(ctx)
}

// WikiTheme reports the theme a space selected. A space with none set inherits
// the site's look and feel, which Confluence reports as no theme rather than as
// a default one.
func (s *Store) WikiSpaceTheme(ctx context.Context, ws, actor, spaceKey string) (models.WikiTheme, error) {
	space, err := s.WikiSpaceByKey(ctx, ws, actor, spaceKey)
	if err != nil {
		return models.WikiTheme{}, err
	}
	if space.ThemeKey == "" {
		return models.WikiTheme{}, pgx.ErrNoRows
	}
	theme, ok := wikiThemes[space.ThemeKey]
	if !ok {
		return models.WikiTheme{}, pgx.ErrNoRows
	}
	return theme, nil
}

// SetWikiSpaceTheme selects a theme for a space.
func (s *Store) SetWikiSpaceTheme(ctx context.Context, ws, actor, spaceKey, themeKey string) (models.WikiTheme, error) {
	theme, ok := wikiThemes[strings.TrimSpace(themeKey)]
	if !ok {
		return models.WikiTheme{}, fmt.Errorf("%w: no theme with this key is installed", ErrWikiValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return models.WikiTheme{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	space, err := lockSpaceForAdmin(ctx, tx, ws, actor, spaceKey)
	if err != nil {
		return models.WikiTheme{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_spaces SET theme_key=$2 WHERE id::text=$1`, space.ID, theme.Key); err != nil {
		return models.WikiTheme{}, err
	}
	return theme, tx.Commit(ctx)
}

// ResetWikiSpaceTheme returns a space to the site's look and feel.
func (s *Store) ResetWikiSpaceTheme(ctx context.Context, ws, actor, spaceKey string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	space, err := lockSpaceForAdmin(ctx, tx, ws, actor, spaceKey)
	if err != nil {
		return err
	}
	if space.ThemeKey == "" {
		return pgx.ErrNoRows
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_spaces SET theme_key='' WHERE id::text=$1`, space.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// WikiSpaceDataPolicies reports whether a data policy blocks content access in
// each space. No policy restricts a space here, so every space reports false —
// which is the answer, not an omission.
func (s *Store) WikiSpaceDataPolicies(ctx context.Context, ws, actor string, keys []string) ([]*models.WikiSpace, error) {
	spaces, err := s.WikiSpaces(ctx, ws, actor)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return spaces, nil
	}
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	out := []*models.WikiSpace{}
	for _, space := range spaces {
		if wanted[space.Key] || wanted[space.ID] {
			out = append(out, space)
		}
	}
	return out, nil
}
