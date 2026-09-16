package store

import (
	"context"
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

// resolveViewedContent finds the content a bare id names and reads it through
// its own visibility rules. The kind is decided by what exists, not by what the
// reader may see: a page the reader may not open is a 404, never a blog post
// that happens to share its id.
func (s *Store) resolveViewedContent(ctx context.Context, ws, actor, id string) (string, error) {
	kind, err := s.WikiContentKindByID(ctx, ws, id)
	if err != nil {
		return "", err
	}
	switch kind.Type {
	case "page":
		_, err = s.WikiPage(ctx, ws, actor, id)
	case "blogpost":
		_, err = s.WikiBlogPost(ctx, ws, actor, id)
	default:
		// Only pages and blog posts are viewed in the sense these numbers
		// report, so any other content has no analytics to show.
		err = pgx.ErrNoRows
	}
	if err != nil {
		return "", err
	}
	return kind.Type, nil
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

// WikiViewCount is how often content was viewed, and by how many people.
type WikiViewCount struct {
	Views, Viewers int
}

// WikiContentViewCounts counts the views and distinct viewers of the given
// content of one type since from, or ever when from is zero. Callers pass only
// content the reader can see.
func (s *Store) WikiContentViewCounts(ctx context.Context, ws, contentType string, ids []string, from time.Time) (map[string]WikiViewCount, error) {
	counts := map[string]WikiViewCount{}
	if len(ids) == 0 {
		return counts, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT content_id::text,count(*),count(DISTINCT user_id) FROM wiki_content_views
		WHERE workspace_id=$1 AND content_type=$2 AND content_id = ANY($3::text[]::bigint[])
		AND ($4::timestamptz IS NULL OR viewed_at >= $4)
		GROUP BY content_id`, ws, contentType, ids, nullableTime(from))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var count WikiViewCount
		if err := rows.Scan(&id, &count.Views, &count.Viewers); err != nil {
			return nil, err
		}
		counts[id] = count
	}
	return counts, rows.Err()
}
