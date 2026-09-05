package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var ErrWikiValidation = errors.New("invalid wiki content")

var ErrWikiConflict = errors.New("the page changed; reload the latest version before saving")

// Wiki visibility is always evaluated against current membership. Private
// spaces and drafts belong to their author; an admin can manage public spaces.
const wikiSpaceVisible = `EXISTS (SELECT 1 FROM memberships wm JOIN users wu ON wu.id=wm.user_id AND wu.active WHERE wm.workspace_id=s.workspace_id AND wm.user_id=$2) AND (NOT s.private OR s.author_id=$2)`
const wikiPageVisible = `(p.published OR p.author_id=$2)`
const wikiSpaceSelect = `SELECT s.id::text,s.workspace_id,s.key,s.name,s.description,s.author_id,s.private,to_char(s.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_spaces s`
const wikiPageSelect = `SELECT p.id::text,s.workspace_id,p.space_id::text,COALESCE(p.parent_id::text,''),p.title,p.status,p.published,p.body,p.author_id,to_char(p.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),v.version,v.message,v.minor_edit,v.author_id,to_char(v.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id JOIN wiki_page_versions v ON v.page_id=p.id AND v.version=p.version`

func scanWikiSpace(row pgx.Row) (*models.WikiSpace, error) {
	s := &models.WikiSpace{}
	err := row.Scan(&s.ID, &s.WorkspaceID, &s.Key, &s.Name, &s.Description, &s.AuthorID, &s.Private, &s.CreatedAt)
	return s, err
}
func scanWikiPage(row pgx.Row) (*models.WikiPage, error) {
	p := &models.WikiPage{Body: models.WikiBody{Representation: "storage"}}
	err := row.Scan(&p.ID, &p.WorkspaceID, &p.SpaceID, &p.ParentID, &p.Title, &p.Status, &p.Published, &p.Body.Value, &p.AuthorID, &p.CreatedAt, &p.Version.Number, &p.Version.Message, &p.Version.MinorEdit, &p.Version.AuthorID, &p.Version.CreatedAt)
	return p, err
}

func (s *Store) WikiSpace(ctx context.Context, ws, user, id string) (*models.WikiSpace, error) {
	return scanWikiSpace(s.Pool.QueryRow(ctx, wikiSpaceSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND s.id::text=$3`, ws, user, id))
}
func (s *Store) WikiSpaces(ctx context.Context, ws, user string) ([]*models.WikiSpace, error) {
	rows, err := s.Pool.Query(ctx, wikiSpaceSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` ORDER BY s.id`, ws, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.WikiSpace{}
	for rows.Next() {
		item, err := scanWikiSpace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Store) WikiPage(ctx context.Context, ws, user, id string) (*models.WikiPage, error) {
	return scanWikiPage(s.Pool.QueryRow(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND p.id::text=$3`, ws, user, id))
}
func (s *Store) WikiPages(ctx context.Context, ws, user, space, status, title string) ([]*models.WikiPage, error) {
	rows, err := s.Pool.Query(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND ($3='' OR s.id::text=$3) AND p.status=$4 AND ($5='' OR p.title=$5) ORDER BY p.id`, ws, user, space, status, title)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.WikiPage{}
	for rows.Next() {
		p, err := scanWikiPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func wikiAction(ctx context.Context, tx pgx.Tx, ws, actor, entity, id, spaceID string, value any) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": spaceID, entity: value})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: entity, EntityID: id, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func (s *Store) CreateWikiSpace(ctx context.Context, ws, actor, key, name, description string, private bool) (*models.WikiSpace, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return nil, err
	}
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO wiki_spaces(workspace_id,key,name,description,author_id,private) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text`, ws, key, name, description, actor, private).Scan(&id); err != nil {
		return nil, err
	}
	space, err := scanWikiSpace(tx.QueryRow(ctx, wikiSpaceSelect+` WHERE s.id::text=$1`, id))
	if err != nil {
		return nil, err
	}
	if err := wikiAction(ctx, tx, ws, actor, "wiki_space", id, id, space); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return space, nil
}

// SaveWikiPage serializes writes within a space, validates parent membership
// and cycles, then writes the page, immutable version and action atomically.
func (s *Store) SaveWikiPage(ctx context.Context, ws, actor string, input models.WikiPage) (*models.WikiPage, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var spaceID string
	err = tx.QueryRow(ctx, `SELECT s.id::text FROM wiki_spaces s WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND s.id::text=$3 FOR UPDATE`, ws, actor, input.SpaceID).Scan(&spaceID)
	if err != nil {
		return nil, err
	}
	if input.ID != "" {
		old, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND p.id::text=$3`, ws, actor, input.ID))
		if err != nil {
			return nil, err
		}
		if old.SpaceID != input.SpaceID {
			return nil, fmt.Errorf("%w: moving pages between spaces is not supported", ErrWikiValidation)
		}
		if input.Version.Number != old.Version.Number+1 {
			return nil, ErrWikiConflict
		}
		if input.Status == "draft" && old.Published {
			return nil, fmt.Errorf("%w: a published page cannot be converted to a draft", ErrWikiValidation)
		}
		if old.Status == "trashed" && input.Status == "current" {
			input.Title = old.Title
			input.Body = old.Body
			input.ParentID = old.ParentID
		}
		input.AuthorID = old.AuthorID
	} else {
		input.Version.Number = 1
		input.AuthorID = actor
	}
	if input.ParentID != "" {
		var valid bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_pages WHERE id::text=$1 AND space_id::text=$2 AND status='current')`, input.ParentID, spaceID).Scan(&valid)
		if err != nil {
			return nil, err
		}
		if !valid {
			return nil, fmt.Errorf("%w: choose a published parent page from this space", ErrWikiValidation)
		}
		if input.ID != "" {
			var cycle bool
			err = tx.QueryRow(ctx, `WITH RECURSIVE ancestors AS (SELECT id,parent_id FROM wiki_pages WHERE id::text=$1 UNION SELECT p.id,p.parent_id FROM wiki_pages p JOIN ancestors a ON p.id=a.parent_id) SELECT EXISTS(SELECT 1 FROM ancestors WHERE id::text=$2)`, input.ParentID, input.ID).Scan(&cycle)
			if err != nil {
				return nil, err
			}
			if cycle {
				return nil, fmt.Errorf("%w: a page cannot be its own ancestor", ErrWikiValidation)
			}
		}
	}
	if input.Status == "trashed" {
		var children bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_pages WHERE parent_id::text=$1 AND status<>'trashed')`, input.ID).Scan(&children); err != nil {
			return nil, err
		}
		if children {
			return nil, fmt.Errorf("%w: move or trash child pages before deleting this page", ErrWikiValidation)
		}
	}
	if input.ID == "" {
		err = tx.QueryRow(ctx, `INSERT INTO wiki_pages(space_id,parent_id,title,status,body,author_id,published) VALUES ($1::bigint,$2::bigint,$3,$4,$5,$6,$4='current') RETURNING id::text`, spaceID, nilIfEmpty(input.ParentID), input.Title, input.Status, input.Body.Value, actor).Scan(&input.ID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE wiki_pages SET parent_id=$2::bigint,title=$3,status=$4,body=$5,version=$6,published=(published OR $4='current') WHERE id::text=$1`, input.ID, nilIfEmpty(input.ParentID), input.Title, input.Status, input.Body.Value, input.Version.Number)
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO wiki_page_versions(page_id,version,title,body,status,author_id,message,minor_edit) VALUES ($1::bigint,$2,$3,$4,$5,$6,$7,$8)`, input.ID, input.Version.Number, input.Title, input.Body.Value, input.Status, actor, input.Version.Message, input.Version.MinorEdit)
	if err != nil {
		return nil, err
	}
	page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE p.id::text=$1`, input.ID))
	if err != nil {
		return nil, err
	}
	if err := wikiAction(ctx, tx, ws, actor, "wiki_page", page.ID, spaceID, page); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return page, nil
}

func (s *Store) WikiVersions(ctx context.Context, ws, user, id string) ([]models.WikiVersion, error) {
	if _, err := s.WikiPage(ctx, ws, user, id); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT version,message,minor_edit,author_id,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_page_versions WHERE page_id::text=$1 AND (status<>'draft' OR author_id=$2) ORDER BY version`, id, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.WikiVersion{}
	for rows.Next() {
		var v models.WikiVersion
		if err := rows.Scan(&v.Number, &v.Message, &v.MinorEdit, &v.AuthorID, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
