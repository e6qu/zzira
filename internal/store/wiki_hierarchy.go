package store

import (
	"context"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

type WikiPageRelation struct {
	Page          *models.WikiPage
	Depth         int
	ChildPosition int
}

func (s *Store) WikiPageAncestors(ctx context.Context, ws, user, pageID string) ([]WikiPageRelation, error) {
	root, err := s.WikiPage(ctx, ws, user, pageID)
	if err != nil {
		return nil, err
	}
	if root.Status != "current" {
		return nil, pgx.ErrNoRows
	}
	rows, err := s.Pool.Query(ctx, `WITH RECURSIVE hierarchy(id,depth) AS (
    SELECT parent_id,1 FROM wiki_pages WHERE id::text=$3 AND parent_id IS NOT NULL
    UNION ALL
    SELECT parent.parent_id,h.depth+1 FROM wiki_pages parent JOIN hierarchy h ON parent.id=h.id WHERE parent.parent_id IS NOT NULL
  )
  SELECT p.id::text,h.depth
  FROM hierarchy h JOIN wiki_pages p ON p.id=h.id JOIN wiki_spaces s ON s.id=p.space_id
  WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND p.status='current'
  ORDER BY h.depth DESC`, ws, user, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var relations []WikiPageRelation
	for rows.Next() {
		var id string
		var depth int
		if err := rows.Scan(&id, &depth); err != nil {
			return nil, err
		}
		page, err := s.WikiPage(ctx, ws, user, id)
		if err != nil {
			return nil, err
		}
		relations = append(relations, WikiPageRelation{Page: page, Depth: depth})
	}
	return relations, rows.Err()
}

func (s *Store) WikiPageDescendants(ctx context.Context, ws, user, pageID string, maxDepth int) ([]WikiPageRelation, error) {
	root, err := s.WikiPage(ctx, ws, user, pageID)
	if err != nil {
		return nil, err
	}
	if root.Status != "current" {
		return nil, pgx.ErrNoRows
	}
	rows, err := s.Pool.Query(ctx, `WITH RECURSIVE hierarchy(id,depth) AS (
    SELECT child.id,1 FROM wiki_pages child WHERE child.parent_id::text=$3
    UNION ALL
    SELECT child.id,h.depth+1 FROM wiki_pages child JOIN hierarchy h ON child.parent_id=h.id WHERE h.depth<$4
  )
  SELECT p.id::text,h.depth,
    (SELECT count(*)::int FROM wiki_pages sibling WHERE sibling.parent_id=p.parent_id AND sibling.status='current' AND sibling.id<p.id)
  FROM hierarchy h JOIN wiki_pages p ON p.id=h.id JOIN wiki_spaces s ON s.id=p.space_id
  WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND p.status='current'
  ORDER BY h.depth,p.parent_id,p.id`, ws, user, pageID, maxDepth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var relations []WikiPageRelation
	for rows.Next() {
		var id string
		relation := WikiPageRelation{}
		if err := rows.Scan(&id, &relation.Depth, &relation.ChildPosition); err != nil {
			return nil, err
		}
		relation.Page, err = s.WikiPage(ctx, ws, user, id)
		if err != nil {
			return nil, err
		}
		relations = append(relations, relation)
	}
	return relations, rows.Err()
}
