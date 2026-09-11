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

// Confluence has two kinds of content state. Space content states are the ones
// a space suggests, and custom ones are made by a writer as they work. They are
// kept apart rather than merged because they answer different questions — what
// this space expects, and what this person has been using — and because only
// one of them is configured per space.
//
// No pinned operation writes a space content state, so the suggested set is the
// product's default rather than a table an administrator edits. Custom states
// are created by the write that uses them, which is a pinned operation, so they
// are rows.
var defaultSpaceContentStates = []models.WikiContentState{
	{ID: "1", Name: "Rough draft", Color: "#FF8B00", Kind: "space"},
	{ID: "2", Name: "In progress", Color: "#0052CC", Kind: "space"},
	{ID: "3", Name: "Ready for review", Color: "#36B37E", Kind: "space"},
	{ID: "4", Name: "Published", Color: "#00875A", Kind: "space"},
}

var ErrWikiContentStateValidation = errors.New("invalid content state")

// SpaceContentStates returns the states a space suggests.
func (s *Store) SpaceContentStates(ctx context.Context, ws, actor, spaceKey string) ([]models.WikiContentState, error) {
	if _, err := s.WikiSpaceByKey(ctx, ws, actor, spaceKey); err != nil {
		return nil, err
	}
	states := make([]models.WikiContentState, len(defaultSpaceContentStates))
	copy(states, defaultSpaceContentStates)
	return states, nil
}

func spaceContentState(id string) (models.WikiContentState, bool) {
	for _, state := range defaultSpaceContentStates {
		if state.ID == id {
			return state, true
		}
	}
	return models.WikiContentState{}, false
}

// CustomContentStates returns the states this user has made, most recent first,
// which is the order Confluence's editor offers them in.
func (s *Store) CustomContentStates(ctx context.Context, ws, actor string, limit int) ([]models.WikiContentState, error) {
	member, err := s.IsMember(ctx, ws, actor)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrProjectPermission
	}
	query := `SELECT id::text,name,color FROM wiki_content_states
		WHERE workspace_id=$1 AND creator_id=$2 ORDER BY created_at DESC, id DESC`
	args := []any{ws, actor}
	if limit > 0 {
		query += ` LIMIT $3`
		args = append(args, limit)
	}
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := []models.WikiContentState{}
	for rows.Next() {
		state := models.WikiContentState{Kind: "custom"}
		if err = rows.Scan(&state.ID, &state.Name, &state.Color); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

// WikiPageContentState reports the state on one status of a page.
func (s *Store) WikiPageContentState(ctx context.Context, ws, actor, pageID, status string) (*models.WikiContentState, string, error) {
	if err := validContentStatus(status); err != nil {
		return nil, "", err
	}
	var stateID, kind, updated *string
	err := s.Pool.QueryRow(ctx, `SELECT v.content_state_id::text, v.content_state_kind,
		to_char(v.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
		JOIN wiki_page_versions v ON v.page_id=p.id AND v.version=p.version
		WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+`
		AND p.id::text=$3 AND p.status=$4`, ws, actor, pageID, status).Scan(&stateID, &kind, &updated)
	if err != nil {
		return nil, "", err
	}
	if stateID == nil || kind == nil {
		return nil, derefOr(updated), nil
	}
	state, err := s.contentStateByID(ctx, ws, *stateID, *kind)
	if err != nil {
		return nil, "", err
	}
	return &state, derefOr(updated), nil
}

func derefOr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Store) contentStateByID(ctx context.Context, ws, id, kind string) (models.WikiContentState, error) {
	if kind == "space" {
		state, ok := spaceContentState(id)
		if !ok {
			return models.WikiContentState{}, pgx.ErrNoRows
		}
		return state, nil
	}
	state := models.WikiContentState{Kind: "custom"}
	err := s.Pool.QueryRow(ctx, contentStateSelect, ws, id).Scan(&state.ID, &state.Name, &state.Color)
	return state, err
}

const contentStateSelect = `SELECT id::text,name,color FROM wiki_content_states
	WHERE workspace_id=$1 AND id::text=$2`

func contentStateByIDTx(ctx context.Context, tx pgx.Tx, ws, id, kind string) (models.WikiContentState, error) {
	if kind == "space" {
		state, ok := spaceContentState(id)
		if !ok {
			return models.WikiContentState{}, pgx.ErrNoRows
		}
		return state, nil
	}
	state := models.WikiContentState{Kind: "custom"}
	err := tx.QueryRow(ctx, contentStateSelect, ws, id).Scan(&state.ID, &state.Name, &state.Color)
	return state, err
}

func validContentStatus(status string) error {
	switch status {
	case "current", "draft", "archived":
		return nil
	default:
		return fmt.Errorf("%w: status must be current, draft or archived", ErrWikiContentStateValidation)
	}
}

// SetWikiPageContentState puts a state on a page, creating a custom state when
// the caller described one instead of naming an existing id. Setting a state
// publishes a new version without changing the body, which is how the change
// appears in the page's history.
func (s *Store) SetWikiPageContentState(ctx context.Context, ws, actor, pageID, status string, stateID, name, color string) (*models.WikiContentState, string, error) {
	if err := validContentStatus(status); err != nil {
		return nil, "", err
	}
	described := name != "" || color != ""
	if stateID != "" && described {
		return nil, "", fmt.Errorf("%w: name a state by id or describe a new one, not both", ErrWikiContentStateValidation)
	}
	if stateID == "" && !described {
		return nil, "", fmt.Errorf("%w: a state id, or a name and colour, is required", ErrWikiContentStateValidation)
	}
	if described {
		name = strings.TrimSpace(name)
		if name == "" || len([]rune(name)) > 20 {
			return nil, "", fmt.Errorf("%w: a name of 1 to 20 characters is required", ErrWikiContentStateValidation)
		}
		if !validStateColor(color) {
			return nil, "", fmt.Errorf("%w: the colour must be a hex value such as #36B37E", ErrWikiContentStateValidation)
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page, err := lockWritablePage(ctx, tx, ws, actor, pageID, status)
	if err != nil {
		return nil, "", err
	}
	kind := "custom"
	if described {
		// A writer re-using a name they have used before keeps one state
		// rather than accumulating a new one on every edit.
		if err = tx.QueryRow(ctx, `INSERT INTO wiki_content_states(workspace_id,name,color,creator_id)
			VALUES($1,$2,$3,$4)
			ON CONFLICT (workspace_id,creator_id,lower(name)) DO UPDATE SET color=EXCLUDED.color
			RETURNING id::text`, ws, name, color, actor).Scan(&stateID); err != nil {
			return nil, "", err
		}
	} else if _, ok := spaceContentState(stateID); ok {
		kind = "space"
	} else {
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_content_states
			WHERE workspace_id=$1 AND id::text=$2)`, ws, stateID).Scan(&exists); err != nil {
			return nil, "", err
		}
		if !exists {
			return nil, "", pgx.ErrNoRows
		}
	}
	updated, err := publishContentState(ctx, tx, actor, page, stateID, kind)
	if err != nil {
		return nil, "", err
	}
	// The state has to be read inside the transaction: a state created by this
	// call does not exist on any other connection until the commit.
	state, err := contentStateByIDTx(ctx, tx, ws, stateID, kind)
	if err != nil {
		return nil, "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	return &state, updated, nil
}

// ClearWikiPageContentState takes the state off, also as a new version, so the
// history shows when the state was removed as well as when it was set.
func (s *Store) ClearWikiPageContentState(ctx context.Context, ws, actor, pageID, status string) (string, error) {
	if err := validContentStatus(status); err != nil {
		return "", err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page, err := lockWritablePage(ctx, tx, ws, actor, pageID, status)
	if err != nil {
		return "", err
	}
	updated, err := publishContentState(ctx, tx, actor, page, "", "")
	if err != nil {
		return "", err
	}
	return updated, tx.Commit(ctx)
}

type lockedPage struct {
	ID      string
	Title   string
	Body    string
	Version int
	Status  string
}

func lockWritablePage(ctx context.Context, tx pgx.Tx, ws, actor, pageID, status string) (lockedPage, error) {
	var page lockedPage
	err := tx.QueryRow(ctx, `SELECT p.id::text,p.title,p.body,p.version,p.status
		FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
		WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+`
		AND `+wikiPageRestrictionWritable+` AND `+wikiSpacePermissionAllowed("update/page")+`
		AND p.id::text=$3 AND p.status=$4 FOR UPDATE OF p`, ws, actor, pageID, status).
		Scan(&page.ID, &page.Title, &page.Body, &page.Version, &page.Status)
	return page, err
}

// publishContentState writes the new version that carries the state change.
func publishContentState(ctx context.Context, tx pgx.Tx, actor string, page lockedPage, stateID, kind string) (string, error) {
	var state, stateKind any
	if stateID != "" {
		state, stateKind = stateID, kind
	}
	next := page.Version + 1
	if _, err := tx.Exec(ctx, `UPDATE wiki_pages SET version=$2,content_state_id=$3::bigint,content_state_kind=$4
		WHERE id::text=$1`, page.ID, next, state, stateKind); err != nil {
		return "", err
	}
	var updated string
	if err := tx.QueryRow(ctx, `INSERT INTO wiki_page_versions(page_id,version,title,body,status,author_id,message,content_state_id,content_state_kind)
		VALUES($1::bigint,$2,$3,$4,$5,$6,$7,$8::bigint,$9)
		RETURNING to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		page.ID, next, page.Title, page.Body, page.Status, actor, contentStateMessage(stateID), state, stateKind).Scan(&updated); err != nil {
		return "", err
	}
	return updated, nil
}

func contentStateMessage(stateID string) string {
	if stateID == "" {
		return "Removed the content state"
	}
	return "Set the content state"
}

func validStateColor(color string) bool {
	if len(color) != 7 || !strings.HasPrefix(color, "#") {
		return false
	}
	_, err := strconv.ParseUint(color[1:], 16, 32)
	return err == nil
}

// WikiPagesInContentState lists a space's content carrying one state.
func (s *Store) WikiPagesInContentState(ctx context.Context, ws, actor, spaceKey, stateID string) ([]*models.WikiPage, error) {
	space, err := s.WikiSpaceByKey(ctx, ws, actor, spaceKey)
	if err != nil {
		return nil, err
	}
	kind := "custom"
	if _, ok := spaceContentState(stateID); ok {
		kind = "space"
	}
	rows, err := s.Pool.Query(ctx, `SELECT p.id::text,p.title,p.status,p.version,p.space_id::text
		FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
		WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+`
		AND p.space_id::text=$3 AND p.content_state_id::text=$4 AND p.content_state_kind=$5
		ORDER BY p.id`, ws, actor, space.ID, stateID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pages := []*models.WikiPage{}
	for rows.Next() {
		page := &models.WikiPage{}
		if err = rows.Scan(&page.ID, &page.Title, &page.Status, &page.Version.Number, &page.SpaceID); err != nil {
			return nil, err
		}
		pages = append(pages, page)
	}
	return pages, rows.Err()
}
