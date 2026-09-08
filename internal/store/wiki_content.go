package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var wikiContentRestrictionVisible = `(NOT c.private OR c.author_id=$2) AND (c.root_page_id IS NULL OR (p.status='current' AND ` + wikiPageVisible + `))`

var wikiContentRestrictionWritable = `(NOT c.private OR c.author_id=$2) AND (c.root_page_id IS NULL OR (p.status='current' AND ` + wikiPageVisible + ` AND ` + wikiPageRestrictionWritable + `))`

func wikiContentVisibleFor(contentType string) string {
	return `(` + wikiSpaceVisible + `) AND (` + wikiSpacePermissionAllowed("read/"+contentType) + `) AND (` + wikiContentRestrictionVisible + `)`
}

func wikiContentWritableFor(contentType string) string {
	return `(` + wikiSpaceVisible + `) AND (` + wikiSpacePermissionAllowed("update/"+contentType) + `) AND (` + wikiContentRestrictionWritable + `)`
}

const wikiContentSelect = `SELECT c.id::text,c.type,c.status,c.title,
  COALESCE(c.parent_content_id::text,c.parent_page_id::text,''),
  CASE WHEN c.parent_content_id IS NOT NULL THEN parent.type WHEN c.parent_page_id IS NOT NULL THEN 'page' ELSE '' END,
  (SELECT count(*)::int FROM wiki_content sibling WHERE sibling.space_id=c.space_id AND sibling.status='current'
    AND sibling.parent_page_id IS NOT DISTINCT FROM c.parent_page_id
    AND sibling.parent_content_id IS NOT DISTINCT FROM c.parent_content_id AND sibling.id<c.id),
  c.author_id,c.owner_id,to_char(c.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),
	c.space_id::text,c.embed_url,c.private,c.classification_level,c.template_key,c.locale,v.version,v.message,v.author_id,
  to_char(v.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
  FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id
  JOIN wiki_content_versions v ON v.content_id=c.id AND v.version=c.version
  LEFT JOIN wiki_content parent ON parent.id=c.parent_content_id
  LEFT JOIN wiki_pages p ON p.id=c.root_page_id`

func scanWikiContent(row pgx.Row) (*models.WikiContent, error) {
	content := &models.WikiContent{}
	err := row.Scan(&content.ID, &content.Type, &content.Status, &content.Title,
		&content.ParentID, &content.ParentType, &content.Position, &content.AuthorID,
		&content.OwnerID, &content.CreatedAt, &content.SpaceID, &content.EmbedURL, &content.Private, &content.ClassificationLevel, &content.TemplateKey, &content.Locale,
		&content.Version.Number, &content.Version.Message, &content.Version.AuthorID,
		&content.Version.CreatedAt)
	return content, err
}

func (s *Store) WikiContent(ctx context.Context, ws, user, id, contentType string) (*models.WikiContent, error) {
	return scanWikiContent(s.Pool.QueryRow(ctx, wikiContentSelect+` WHERE s.workspace_id=$1 AND `+wikiContentVisibleFor(contentType)+` AND c.id::text=$3 AND c.type=$4 AND c.status='current'`, ws, user, id, contentType))
}

func (s *Store) CanUpdateWikiContent(ctx context.Context, ws, user, id, contentType string) (bool, error) {
	if _, err := s.WikiContent(ctx, ws, user, id, contentType); err != nil {
		return false, err
	}
	var allowed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id LEFT JOIN wiki_pages p ON p.id=c.root_page_id WHERE s.workspace_id=$1 AND `+wikiContentWritableFor(contentType)+` AND c.id::text=$3 AND c.type=$4 AND c.status='current')`, ws, user, id, contentType).Scan(&allowed)
	return allowed, err
}

func (s *Store) CanDeleteWikiContent(ctx context.Context, ws, user, id, contentType string) (bool, error) {
	if _, err := s.WikiContent(ctx, ws, user, id, contentType); err != nil {
		return false, err
	}
	var allowed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id LEFT JOIN wiki_pages p ON p.id=c.root_page_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpacePermissionAllowed("delete/"+contentType)+` AND `+wikiContentRestrictionWritable+` AND c.id::text=$3 AND c.type=$4 AND c.status='current')`, ws, user, id, contentType).Scan(&allowed)
	return allowed, err
}

func finishWikiContentMutation(ctx context.Context, tx pgx.Tx, ws, actor string, content *models.WikiContent) error {
	if _, err := tx.Exec(ctx, `UPDATE wiki_content SET updated_at=now() WHERE id::text=$1`, content.ID); err != nil {
		return err
	}
	if err := wikiContentAction(ctx, tx, ws, actor, content, models.OpUpsert); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) WikiContents(ctx context.Context, ws, user, spaceID, contentType string) ([]*models.WikiContent, error) {
	rows, err := s.Pool.Query(ctx, wikiContentSelect+` WHERE s.workspace_id=$1 AND `+wikiContentVisibleFor(contentType)+` AND c.space_id::text=$3 AND c.type=$4 AND c.status='current' ORDER BY c.id`, ws, user, spaceID, contentType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contents := []*models.WikiContent{}
	for rows.Next() {
		content, err := scanWikiContent(rows)
		if err != nil {
			return nil, err
		}
		contents = append(contents, content)
	}
	return contents, rows.Err()
}

func (s *Store) CreateWikiContent(ctx context.Context, ws, actor string, input models.WikiContent) (*models.WikiContent, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var spaceID string
	createPermission := wikiSpacePermissionAllowed("create/" + input.Type)
	if err = tx.QueryRow(ctx, `SELECT s.id::text FROM wiki_spaces s WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+createPermission+` AND s.id::text=$3 FOR SHARE OF s`, ws, actor, input.SpaceID).Scan(&spaceID); err != nil {
		return nil, err
	}
	var parentPage, parentContent, rootPage any
	if input.ParentID != "" {
		var pageID string
		pageErr := tx.QueryRow(ctx, `SELECT p.id::text FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+createPermission+` AND `+wikiPageVisible+` AND `+wikiPageRestrictionWritable+` AND p.id::text=$3 AND p.space_id::text=$4 AND p.status='current' FOR SHARE OF p`, ws, actor, input.ParentID, spaceID).Scan(&pageID)
		if pageErr == nil {
			parentPage, rootPage, input.ParentType = pageID, pageID, "page"
		} else if pageErr != pgx.ErrNoRows {
			return nil, pageErr
		} else {
			var parentID, parentType, parentRoot string
			var parentPrivate bool
			contentErr := tx.QueryRow(ctx, `SELECT c.id::text,c.type,COALESCE(c.root_page_id::text,''),c.private FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id LEFT JOIN wiki_pages p ON p.id=c.root_page_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+createPermission+` AND `+wikiContentRestrictionWritable+` AND c.id::text=$3 AND c.space_id::text=$4 AND c.status='current' FOR SHARE OF c`, ws, actor, input.ParentID, spaceID).Scan(&parentID, &parentType, &parentRoot, &parentPrivate)
			if contentErr != nil {
				return nil, fmt.Errorf("%w: choose visible parent content from this space", ErrWikiValidation)
			}
			parentContent, input.ParentType = parentID, parentType
			input.Private = input.Private || parentPrivate
			if parentRoot != "" {
				rootPage = parentRoot
			}
		}
	}
	if err = tx.QueryRow(ctx, `INSERT INTO wiki_content(space_id,parent_page_id,parent_content_id,root_page_id,type,title,embed_url,private,template_key,locale,author_id,owner_id)
    VALUES($1::bigint,$2::bigint,$3::bigint,$4::bigint,$5,$6,$7,$8,$9,$10,$11,$11) RETURNING id::text`, spaceID, parentPage, parentContent, rootPage, input.Type, input.Title, input.EmbedURL, input.Private, input.TemplateKey, input.Locale, actor).Scan(&input.ID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_content_versions(content_id,version,title,status,embed_url,author_id) VALUES($1::bigint,1,$2,'current',$3,$4)`, input.ID, input.Title, input.EmbedURL, actor); err != nil {
		return nil, err
	}
	content, err := scanWikiContent(tx.QueryRow(ctx, wikiContentSelect+` WHERE c.id::text=$1`, input.ID))
	if err != nil {
		return nil, err
	}
	if err = wikiContentAction(ctx, tx, ws, actor, content, models.OpUpsert); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return content, nil
}

func (s *Store) DeleteWikiContent(ctx context.Context, ws, actor, id, contentType string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	content, err := scanWikiContent(tx.QueryRow(ctx, wikiContentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpacePermissionAllowed("delete/"+contentType)+` AND `+wikiContentRestrictionWritable+` AND c.id::text=$3 AND c.type=$4 AND c.status='current' FOR UPDATE OF c`, ws, actor, id, contentType))
	if err != nil {
		return err
	}
	var children bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_content WHERE parent_content_id::text=$1 AND status='current')`, id).Scan(&children); err != nil {
		return err
	}
	if children {
		return fmt.Errorf("%w: move or delete child content before deleting this content", ErrWikiValidation)
	}
	content.Status = "trashed"
	content.Version.Number++
	content.Version.Message = "Moved to trash"
	if _, err = tx.Exec(ctx, `UPDATE wiki_content SET status='trashed',version=$2,updated_at=now() WHERE id::text=$1`, id, content.Version.Number); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_content_versions(content_id,version,title,status,embed_url,author_id,message) VALUES($1::bigint,$2,$3,'trashed',$4,$5,$6)`, id, content.Version.Number, content.Title, content.EmbedURL, actor, content.Version.Message); err != nil {
		return err
	}
	if err = wikiContentAction(ctx, tx, ws, actor, content, models.OpDelete); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func wikiContentAction(ctx context.Context, tx pgx.Tx, ws, actor string, content *models.WikiContent, op string) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	var rootPage string
	if err = tx.QueryRow(ctx, `SELECT COALESCE(root_page_id::text,'') FROM wiki_content WHERE id::text=$1`, content.ID).Scan(&rootPage); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": content.SpaceID, "rootPageId": rootPage, "contentPrivate": content.Private, "contentAuthorId": content.AuthorID, "wiki_content": content})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: "wiki_content", EntityID: content.ID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func (s *Store) WikiContentAncestors(ctx context.Context, ws, user, id, contentType string) ([]map[string]string, error) {
	content, err := s.WikiContent(ctx, ws, user, id, contentType)
	if err != nil {
		return nil, err
	}
	chain := []map[string]string{}
	for content.ParentID != "" && content.ParentType != "page" {
		content, err = s.WikiContent(ctx, ws, user, content.ParentID, content.ParentType)
		if err != nil {
			return nil, err
		}
		chain = append([]map[string]string{{"id": content.ID, "type": content.Type}}, chain...)
	}
	if content.ParentType == "page" {
		page, err := s.WikiPage(ctx, ws, user, content.ParentID)
		if err != nil {
			return nil, err
		}
		pages, err := s.WikiPageAncestors(ctx, ws, user, page.ID)
		if err != nil {
			return nil, err
		}
		prefix := make([]map[string]string, 0, len(pages)+1)
		for _, relation := range pages {
			prefix = append(prefix, map[string]string{"id": relation.Page.ID, "type": "page"})
		}
		prefix = append(prefix, map[string]string{"id": page.ID, "type": "page"})
		chain = append(prefix, chain...)
	}
	return chain, nil
}

func (s *Store) WikiContentDescendants(ctx context.Context, ws, user, id, contentType string, maxDepth int) ([]models.WikiContentRelation, error) {
	if _, err := s.WikiContent(ctx, ws, user, id, contentType); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `WITH RECURSIVE hierarchy(id,depth) AS (
    SELECT child.id,1 FROM wiki_content child WHERE child.parent_content_id::text=$3 AND child.status='current'
    UNION ALL SELECT child.id,h.depth+1 FROM wiki_content child JOIN hierarchy h ON child.parent_content_id=h.id WHERE h.depth<$4 AND child.status='current'
  ) `+wikiContentSelect+` JOIN hierarchy h ON h.id=c.id
  WHERE s.workspace_id=$1 AND `+wikiContentVisibleFor(contentType)+` AND c.status='current' ORDER BY h.depth,c.parent_content_id,c.id`, ws, user, id, maxDepth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	relations := []models.WikiContentRelation{}
	for rows.Next() {
		content, err := scanWikiContent(rows)
		if err != nil {
			return nil, err
		}
		// The select cannot expose the CTE depth, so calculate it by walking the
		// already ordered parent chain below.
		depth := 1
		parent := content.ParentID
		for parent != id && parent != "" {
			depth++
			var next string
			if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(parent_content_id::text,'') FROM wiki_content WHERE id::text=$1`, parent).Scan(&next); err != nil {
				return nil, err
			}
			parent = next
		}
		relations = append(relations, models.WikiContentRelation{Content: content, Depth: depth, ChildPosition: content.Position})
	}
	return relations, rows.Err()
}
