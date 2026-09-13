package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// A view is one person opening one piece of content. Confluence reports two
// numbers from them — how many views content has had, and how many distinct
// people made them — each optionally counted from a date.

// WikiContentAnalytics is what the analytics reads report for one piece of
// content: its id and the count asked for.
type WikiContentAnalytics struct {
	ContentID   string
	ContentType string
	Count       int
}

// RecordWikiContentView notes that a person opened content. It is called only
// after the content was read successfully, so a view is never recorded for
// content the reader could not see.
func (s *Store) RecordWikiContentView(ctx context.Context, ws, actor, contentType, contentID string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO wiki_content_views(workspace_id,content_type,content_id,user_id)
		VALUES($1,$2,$3::bigint,$4)`, ws, contentType, contentID, actor)
	return err
}

// resolveViewedContent finds the content a bare id names, the way every v1
// content route here does: a page first, then a blog post. Reading it through
// the ordinary visibility rules means analytics for content a reader may not
// open is a 404, exactly as the content itself is.
func (s *Store) resolveViewedContent(ctx context.Context, ws, actor, id string) (string, error) {
	if _, err := s.WikiPage(ctx, ws, actor, id); err == nil {
		return "page", nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if _, err := s.WikiBlogPost(ctx, ws, actor, id); err != nil {
		return "", err
	}
	return "blogpost", nil
}

// WikiContentViews counts the views content has had since from, or ever when
// from is zero.
func (s *Store) WikiContentViews(ctx context.Context, ws, actor, id string, from time.Time) (WikiContentAnalytics, error) {
	return s.wikiContentAnalytics(ctx, ws, actor, id, from, `count(*)`)
}

// WikiContentViewers counts the distinct people who viewed content since from.
func (s *Store) WikiContentViewers(ctx context.Context, ws, actor, id string, from time.Time) (WikiContentAnalytics, error) {
	return s.wikiContentAnalytics(ctx, ws, actor, id, from, `count(DISTINCT user_id)`)
}

func (s *Store) wikiContentAnalytics(ctx context.Context, ws, actor, id string, from time.Time, aggregate string) (WikiContentAnalytics, error) {
	contentType, err := s.resolveViewedContent(ctx, ws, actor, id)
	if err != nil {
		return WikiContentAnalytics{}, err
	}
	result := WikiContentAnalytics{ContentID: id, ContentType: contentType}
	err = s.Pool.QueryRow(ctx, `SELECT `+aggregate+` FROM wiki_content_views
		WHERE workspace_id=$1 AND content_type=$2 AND content_id=$3::bigint
		AND ($4::timestamptz IS NULL OR viewed_at >= $4)`,
		ws, contentType, id, nullableTime(from)).Scan(&result.Count)
	return result, err
}

func nullableTime(moment time.Time) any {
	if moment.IsZero() {
		return nil
	}
	return moment
}
