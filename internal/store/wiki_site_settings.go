package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// Confluence's look and feel is set for the site and may be overridden for one
// space. Each level says whether it is showing the global settings, its own
// custom ones, or the theme's — and the custom settings are kept even while
// something else is selected, so switching back restores what was there.

// DefaultLookAndFeel is the site's look before anyone changes it. Confluence
// answers the whole structure rather than only what differs, so a client can
// render without knowing the defaults.
func DefaultLookAndFeel() map[string]any {
	return map[string]any{
		"headings": map[string]any{"color": "#172B4D"},
		"links":    map[string]any{"color": "#0052CC"},
		"menus": map[string]any{
			"color":        "#172B4D",
			"hoverOrFocus": map[string]any{"backgroundColor": "#EBECF0"},
		},
		"header": map[string]any{
			"backgroundColor":     "#0052CC",
			"button":              map[string]any{"backgroundColor": "#FFFFFF", "color": "#0052CC"},
			"primaryNavigation":   map[string]any{"color": "#FFFFFF", "hoverOrFocus": map[string]any{"backgroundColor": "#0065FF", "color": "#FFFFFF"}},
			"secondaryNavigation": map[string]any{"color": "#FFFFFF", "hoverOrFocus": map[string]any{"backgroundColor": "#0065FF", "color": "#FFFFFF"}},
			"search":              map[string]any{"backgroundColor": "#FFFFFF", "color": "#172B4D"},
		},
		"content": map[string]any{
			"screen":    map[string]any{"background": "#FFFFFF", "backgroundColor": "#FFFFFF"},
			"container": map[string]any{"background": "#FFFFFF", "backgroundColor": "#FFFFFF", "padding": "0 20px", "borderRadius": "3px"},
			"header":    map[string]any{"background": "#FFFFFF", "backgroundColor": "#FFFFFF", "padding": "0", "borderRadius": "0"},
			"body":      map[string]any{"background": "#FFFFFF", "backgroundColor": "#FFFFFF", "padding": "0", "borderRadius": "0"},
		},
		"bordersAndDividers": map[string]any{"color": "#DFE1E6"},
		"spaceReference":     map[string]any{},
	}
}

// LookAndFeel is one level's settings, and which of them is showing.
type LookAndFeel struct {
	Selected string
	Custom   map[string]any
	SpaceKey string
}

// WikiLookAndFeel reports the settings for the site, or for one space when a
// key is given. A space with nothing of its own follows the site.
func (s *Store) WikiLookAndFeel(ctx context.Context, ws, actor, spaceKey string) (LookAndFeel, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return LookAndFeel{}, err
	}
	spaceID, err := s.lookAndFeelSpaceID(ctx, ws, actor, spaceKey)
	if err != nil {
		return LookAndFeel{}, err
	}
	settings := LookAndFeel{Selected: "global", SpaceKey: spaceKey}
	var custom []byte
	err = s.Pool.QueryRow(ctx, `SELECT selected, custom FROM wiki_look_and_feel
		WHERE workspace_id=$1 AND space_id IS NOT DISTINCT FROM $2::bigint`, ws, spaceID).Scan(&settings.Selected, &custom)
	if errors.Is(err, pgx.ErrNoRows) {
		return settings, nil
	}
	if err != nil {
		return LookAndFeel{}, err
	}
	if len(custom) > 0 {
		if err = json.Unmarshal(custom, &settings.Custom); err != nil {
			return LookAndFeel{}, err
		}
	}
	return settings, nil
}

// lookAndFeelSpaceID resolves the space a request names, or nothing for the
// site. A space nobody can see is not a space to configure.
func (s *Store) lookAndFeelSpaceID(ctx context.Context, ws, actor, spaceKey string) (any, error) {
	if strings.TrimSpace(spaceKey) == "" {
		return nil, nil
	}
	space, err := s.WikiSpaceByKey(ctx, ws, actor, spaceKey)
	if err != nil {
		return nil, err
	}
	return space.ID, nil
}

// SetWikiLookAndFeelSelection chooses which settings a space shows. Confluence
// allows custom and theme settings to be selected for a space only, because the
// site's own look is the global one by definition.
func (s *Store) SetWikiLookAndFeelSelection(ctx context.Context, ws, actor, spaceKey, selection string) (LookAndFeel, error) {
	switch selection {
	case "global", "custom", "theme":
	default:
		return LookAndFeel{}, fmt.Errorf("%w: lookAndFeelType is global, custom or theme", ErrWikiValidation)
	}
	if strings.TrimSpace(spaceKey) == "" {
		return LookAndFeel{}, fmt.Errorf("%w: a spaceKey is required to choose which settings a space shows", ErrWikiValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return LookAndFeel{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	space, err := lockSpaceForAdmin(ctx, tx, ws, actor, spaceKey)
	if err != nil {
		return LookAndFeel{}, err
	}
	if selection == "theme" && space.ThemeKey == "" {
		return LookAndFeel{}, fmt.Errorf("%w: the space has no theme to show", ErrWikiValidation)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_look_and_feel(workspace_id,space_id,selected)
		VALUES($1,$2::bigint,$3)
		ON CONFLICT (workspace_id,space_id) WHERE space_id IS NOT NULL
		DO UPDATE SET selected=EXCLUDED.selected, updated_at=now()`, ws, space.ID, selection); err != nil {
		return LookAndFeel{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LookAndFeel{}, err
	}
	return s.WikiLookAndFeel(ctx, ws, actor, spaceKey)
}

// UpsertWikiCustomLookAndFeel writes the custom settings for the site or a
// space. Confluence updates them if they exist and creates them if not, and
// selecting custom is a separate act.
func (s *Store) UpsertWikiCustomLookAndFeel(ctx context.Context, ws, actor, spaceKey string, custom map[string]any) (LookAndFeel, error) {
	if len(custom) == 0 {
		return LookAndFeel{}, fmt.Errorf("%w: the custom settings are empty", ErrWikiValidation)
	}
	if err := s.requireLookAndFeelAdmin(ctx, ws, actor, spaceKey); err != nil {
		return LookAndFeel{}, err
	}
	spaceID, err := s.lookAndFeelSpaceID(ctx, ws, actor, spaceKey)
	if err != nil {
		return LookAndFeel{}, err
	}
	encoded, err := json.Marshal(custom)
	if err != nil {
		return LookAndFeel{}, err
	}
	if err = s.upsertLookAndFeel(ctx, ws, spaceID, encoded); err != nil {
		return LookAndFeel{}, err
	}
	return s.WikiLookAndFeel(ctx, ws, actor, spaceKey)
}

// upsertLookAndFeel writes one row for the site or one for a space; the two
// need different conflict targets because only one site row may exist.
func (s *Store) upsertLookAndFeel(ctx context.Context, ws string, spaceID any, custom []byte) error {
	if spaceID == nil {
		_, err := s.Pool.Exec(ctx, `INSERT INTO wiki_look_and_feel(workspace_id,space_id,custom)
			VALUES($1,NULL,$2)
			ON CONFLICT (workspace_id) WHERE space_id IS NULL
			DO UPDATE SET custom=EXCLUDED.custom, updated_at=now()`, ws, custom)
		return err
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO wiki_look_and_feel(workspace_id,space_id,custom)
		VALUES($1,$2::bigint,$3)
		ON CONFLICT (workspace_id,space_id) WHERE space_id IS NOT NULL
		DO UPDATE SET custom=EXCLUDED.custom, updated_at=now()`, ws, spaceID, custom)
	return err
}

// ResetWikiCustomLookAndFeel returns the custom settings to the defaults. It
// does not change which settings are selected, which is Confluence's own rule.
func (s *Store) ResetWikiCustomLookAndFeel(ctx context.Context, ws, actor, spaceKey string) error {
	if err := s.requireLookAndFeelAdmin(ctx, ws, actor, spaceKey); err != nil {
		return err
	}
	spaceID, err := s.lookAndFeelSpaceID(ctx, ws, actor, spaceKey)
	if err != nil {
		return err
	}
	defaults, err := json.Marshal(DefaultLookAndFeel())
	if err != nil {
		return err
	}
	return s.upsertLookAndFeel(ctx, ws, spaceID, defaults)
}

// requireLookAndFeelAdmin accepts a space administrator for a space, and a
// workspace administrator for the site.
func (s *Store) requireLookAndFeelAdmin(ctx context.Context, ws, actor, spaceKey string) error {
	if strings.TrimSpace(spaceKey) == "" {
		return s.requireSiteAdmin(ctx, ws, actor)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = lockSpaceForAdmin(ctx, tx, ws, actor, spaceKey); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// WikiThemes lists the themes a site or space may select. Confluence leaves the
// default theme out of this list, because it is what a site shows when no theme
// is chosen rather than a theme to choose.
func (s *Store) WikiThemes(ctx context.Context, ws, actor string) ([]models.WikiTheme, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return nil, err
	}
	themes := make([]models.WikiTheme, 0, len(wikiThemes))
	for key, theme := range wikiThemes {
		if key == DefaultWikiThemeKey {
			continue
		}
		themes = append(themes, theme)
	}
	sort.Slice(themes, func(i, j int) bool { return themes[i].Key < themes[j].Key })
	return themes, nil
}

// WikiThemeByKey reads one theme, whether or not it is the default.
func (s *Store) WikiThemeByKey(ctx context.Context, ws, actor, key string) (models.WikiTheme, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return models.WikiTheme{}, err
	}
	theme, ok := wikiThemes[strings.TrimSpace(key)]
	if !ok {
		return models.WikiTheme{}, pgx.ErrNoRows
	}
	return theme, nil
}

// WikiGlobalTheme reports the theme assigned to the whole site, if one is.
func (s *Store) WikiGlobalTheme(ctx context.Context, ws, actor string) (models.WikiTheme, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return models.WikiTheme{}, err
	}
	var key string
	err := s.Pool.QueryRow(ctx, `SELECT global_theme_key FROM wiki_site_settings WHERE workspace_id=$1`, ws).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && key == "") {
		return models.WikiTheme{}, pgx.ErrNoRows
	}
	if err != nil {
		return models.WikiTheme{}, err
	}
	theme, ok := wikiThemes[key]
	if !ok {
		return models.WikiTheme{}, pgx.ErrNoRows
	}
	return theme, nil
}

// WikiSystemInfo describes the site, which is what Confluence reports here.
type WikiSystemInfo struct {
	CloudID         string
	SiteTitle       string
	DefaultLocale   string
	DefaultTimeZone string
}

func (s *Store) WikiSystemInfo(ctx context.Context, ws, actor string) (WikiSystemInfo, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return WikiSystemInfo{}, err
	}
	var info WikiSystemInfo
	err := s.Pool.QueryRow(ctx, `SELECT cloud_id::text, name FROM workspaces WHERE id=$1`, ws).
		Scan(&info.CloudID, &info.SiteTitle)
	if err != nil {
		return WikiSystemInfo{}, err
	}
	info.DefaultLocale, info.DefaultTimeZone = "en_US", "UTC"
	return info, nil
}
