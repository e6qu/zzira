package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// WikiContentStateSettings says whether a space's pages carry content states,
// and whether writers may use the space's suggested states and their own
// custom ones.
type WikiContentStateSettings struct {
	ContentStatesAllowed       bool
	CustomContentStatesAllowed bool
	SpaceContentStatesAllowed  bool
}

// WikiContentStateSettings reads a space's settings for its administrators.
func (s *Store) WikiContentStateSettings(ctx context.Context, ws, actor, spaceKey string) (WikiContentStateSettings, error) {
	space, err := s.WikiSpaceByKey(ctx, ws, actor, spaceKey)
	if err != nil {
		return WikiContentStateSettings{}, err
	}
	admin, err := s.CanAdministerWikiSpace(ctx, ws, actor, space.ID)
	if err != nil {
		return WikiContentStateSettings{}, err
	}
	if !admin {
		return WikiContentStateSettings{}, ErrProjectPermission
	}
	var settings WikiContentStateSettings
	err = s.Pool.QueryRow(ctx, `SELECT content_states_allowed,custom_content_states_allowed,space_content_states_allowed FROM wiki_spaces WHERE id::text=$1`, space.ID).
		Scan(&settings.ContentStatesAllowed, &settings.CustomContentStatesAllowed, &settings.SpaceContentStatesAllowed)
	return settings, err
}

// SetWikiContentStateSettings changes a space's settings.
func (s *Store) SetWikiContentStateSettings(ctx context.Context, ws, actor, spaceKey string, settings WikiContentStateSettings) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	space, err := lockSpaceForAdmin(ctx, tx, ws, actor, spaceKey)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE wiki_spaces SET content_states_allowed=$2,custom_content_states_allowed=$3,space_content_states_allowed=$4 WHERE id::text=$1`,
		space.ID, settings.ContentStatesAllowed, settings.CustomContentStatesAllowed, settings.SpaceContentStatesAllowed); err != nil {
		return err
	}
	if err := wikiAction(ctx, tx, ws, actor, "wiki_space", space.ID, space.ID, space); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// wikiContentStatesPermitted refuses a state the space does not allow: any
// state when states are off, and a space or custom state when that kind is.
func wikiContentStatesPermitted(ctx context.Context, tx pgx.Tx, spaceID, kind string) error {
	var allowed, custom, suggested bool
	if err := tx.QueryRow(ctx, `SELECT content_states_allowed,custom_content_states_allowed,space_content_states_allowed FROM wiki_spaces WHERE id::text=$1`, spaceID).Scan(&allowed, &custom, &suggested); err != nil {
		return err
	}
	switch {
	case !allowed:
		return fmt.Errorf("%w: content states are turned off in this space", ErrWikiContentStateValidation)
	case kind == "custom" && !custom:
		return fmt.Errorf("%w: custom content states are turned off in this space", ErrWikiContentStateValidation)
	case kind == "space" && !suggested:
		return fmt.Errorf("%w: space content states are turned off in this space", ErrWikiContentStateValidation)
	}
	return nil
}
