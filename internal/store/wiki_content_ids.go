package store

import (
	"context"
)

// Confluence content ids are unique across every kind of content: a page, a
// blog post, a comment and an attachment never share one. Content created from
// migration 158 onward draws from one sequence, so that holds for new content.
// Content created before it may still share a low id with content of another
// kind, so resolving a bare id has to be deterministic and must never let one
// kind stand in for another.

// WikiContentKind is what a bare content id names.
type WikiContentKind struct {
	// Type is the v2 type: page, blogpost, footer-comment, inline-comment,
	// attachment, or a hierarchical content type such as folder.
	Type string
	// CustomType is the app-defined type when Type is custom content.
	CustomType string
}

// WikiContentKindByID reports what an id names in the workspace, without
// deciding whether the caller may see it. Callers resolve the kind first and
// then read the content through its own visibility rules, so a piece of
// content the caller may not open is a 404 rather than falling through to
// different content that happens to share its id.
//
// Where legacy ids collide, the order is page, blog post, comment, attachment,
// then hierarchical content — the order every v1 content route here already
// resolves a bare id in.
func (s *Store) WikiContentKindByID(ctx context.Context, ws, id string) (WikiContentKind, error) {
	var kind WikiContentKind
	err := s.Pool.QueryRow(ctx, `SELECT kind, custom_type FROM (
		SELECT 1 AS rank, 'page' AS kind, '' AS custom_type
			FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
			WHERE s.workspace_id=$1 AND p.id::text=$2
		UNION ALL
		SELECT 2, 'blogpost', ''
			FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id
			WHERE s.workspace_id=$1 AND b.id::text=$2
		UNION ALL
		SELECT 3, CASE WHEN c.comment_type='inline' THEN 'inline-comment' ELSE 'footer-comment' END, ''
			FROM wiki_footer_comments c
			LEFT JOIN wiki_attachments ca ON ca.id=c.attachment_id
			LEFT JOIN wiki_pages p ON p.id=COALESCE(c.page_id,ca.page_id)
			LEFT JOIN wiki_blog_posts bp ON bp.id=COALESCE(c.blog_post_id,ca.blog_post_id)
			JOIN wiki_spaces s ON s.id=COALESCE(p.space_id,bp.space_id)
			WHERE s.workspace_id=$1 AND c.id::text=$2
		UNION ALL
		SELECT 4, 'attachment', ''
			FROM wiki_attachments a
			LEFT JOIN wiki_pages p ON p.id=a.page_id
			LEFT JOIN wiki_blog_posts b ON b.id=a.blog_post_id
			JOIN wiki_spaces s ON s.id=COALESCE(p.space_id,b.space_id)
			WHERE s.workspace_id=$1 AND a.id::text=$2
		UNION ALL
		SELECT 5, c.type, COALESCE(c.custom_type,'')
			FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id
			WHERE s.workspace_id=$1 AND c.id::text=$2
	) kinds ORDER BY rank LIMIT 1`, ws, id).Scan(&kind.Type, &kind.CustomType)
	return kind, err
}
