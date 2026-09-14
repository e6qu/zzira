package store

import (
	"context"
	"regexp"

	"github.com/e6qu/zzira/internal/models"
)

// Custom content takes footer comments, and a comment on it is visible to
// whoever may see the custom content: its space, its privacy, and the page it
// sits beneath when it sits beneath one.
var wikiCustomContentCommentVisible = `(` + wikiSpacePermissionAllowed("read/custom") + `) AND (NOT cc.private OR cc.author_id=$2)
  AND (cc.root_page_id IS NULL OR EXISTS (SELECT 1 FROM wiki_pages rp WHERE rp.id=cc.root_page_id AND rp.status='current' AND ` +
	regexp.MustCompile(`\bp\.`).ReplaceAllString(wikiPageVisible, "rp.") + `))`

// WikiCustomContentFooterComments lists the top-level footer comments on
// custom content the reader can see.
func (s *Store) WikiCustomContentFooterComments(ctx context.Context, ws, user, customContentID string) ([]*models.WikiFooterComment, error) {
	if _, err := s.WikiContent(ctx, ws, user, customContentID, "custom"); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+`
		AND c.comment_type='footer' AND c.custom_content_id::text=$3 AND c.parent_id IS NULL ORDER BY c.created_at,c.id`, ws, user, customContentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiFooterComments(rows)
}
