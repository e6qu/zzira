package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/cql"
)

// CQL searches a normalised view of everything a reader may see: pages, blog
// posts, comments, attachments and spaces reduced to one set of columns. Each
// part of that view carries its own visibility rules, so a query narrows what a
// reader can reach and can never widen it.

var ErrWikiSearchValidation = errors.New("invalid search")

// WikiSearchResult is one match, in the shape both search endpoints render.
type WikiSearchResult struct {
	EntityType     string
	ID             string
	SpaceID        string
	SpaceKey       string
	Title          string
	Body           string
	Status         string
	Creator        string
	ParentID       string
	ContainerID    string
	ContainerTitle string
	CreatedAt      time.Time
	LastModified   time.Time
}

// WikiSearchRequest is everything a search is scoped by beyond its query.
type WikiSearchRequest struct {
	CQL string
	// EntityTypes narrows the view itself. The content search reads content,
	// so it never looks at spaces however the query is written.
	EntityTypes []string
	// ContentStatuses is the cqlcontext status scope. Confluence searches
	// current content unless told otherwise.
	ContentStatuses []string
	SpaceKey        string
	ContentID       string

	IncludeArchivedSpaces bool
	ExcludeCurrentSpaces  bool

	Start int
	Limit int
	Now   time.Time
}

// AllWikiSearchEntityTypes is everything CQL can reach.
var AllWikiSearchEntityTypes = []string{"page", "blogpost", "comment", "attachment", "space"}

// WikiSearchContentTypes is what the content search reads.
var WikiSearchContentTypes = []string{"page", "blogpost", "comment", "attachment"}

// pageAncestry is the parent chain every page has, computed once so each row
// can report the ancestors a query asks about.
const pageAncestry = `WITH RECURSIVE page_ancestry(page_id, ancestor_id) AS (
    SELECT wp.id, wp.parent_id FROM wiki_pages wp JOIN wiki_spaces ws ON ws.id=wp.space_id
      WHERE ws.workspace_id=$1 AND wp.parent_id IS NOT NULL
    UNION ALL
    SELECT pa.page_id, wp.parent_id FROM page_ancestry pa
      JOIN wiki_pages wp ON wp.id=pa.ancestor_id WHERE wp.parent_id IS NOT NULL
  )`

// macrosIn and mentionsIn read what a storage body refers to. A macro and a
// mention are written into the body rather than stored beside it, so this is
// where a query about them has to look.
func macrosIn(column string) string {
	return `ARRAY(SELECT m[1] FROM regexp_matches(` + column + `, '<ac:structured-macro[^>]*ac:name="([^"]+)"', 'g') AS m)`
}

func mentionsIn(column string) string {
	return `ARRAY(SELECT m[1] FROM regexp_matches(` + column + `, '<ri:user[^>]*ri:account-id="([^"]+)"', 'g') AS m)`
}

func watchersOf(id string) string {
	return `ARRAY(SELECT w.user_id FROM wiki_watches w WHERE w.workspace_id=s.workspace_id AND w.target_type='content' AND w.target_id=` + id + `)`
}

func favouritesOf(kind, key string) string {
	return `ARRAY(SELECT r.source_key FROM wiki_relations r WHERE r.workspace_id=s.workspace_id
		AND r.name='favourite' AND r.source_type='user' AND r.target_type='` + kind + `' AND r.target_key=` + key + `)`
}

func labelsFrom(table, column, id string) string {
	return `ARRAY(SELECT l.name FROM ` + table + ` jl JOIN wiki_labels l ON l.id=jl.label_id WHERE jl.` + column + `=` + id + `)`
}

const emptyTextArray = `ARRAY[]::text[]`

// arrayOfContainer reports what a comment or an attachment hangs from as the
// one ancestor it has, and reports nothing when it hangs from nothing.
func arrayOfContainer(expression string) string {
	return `CASE WHEN ` + expression + ` <> '' THEN ARRAY[` + expression + `] ELSE ` + emptyTextArray + ` END`
}

// searchableView builds the union the compiler reads. Each branch names the
// same columns in the same order, because the union is what makes one query
// able to ask about everything at once.
func (s *Store) searchableView(request WikiSearchRequest) (string, error) {
	wanted := map[string]bool{}
	for _, entityType := range request.EntityTypes {
		wanted[entityType] = true
	}
	branches := []string{}
	if wanted["page"] {
		branches = append(branches, `SELECT 'page' AS entity_type, p.id::text AS entity_id, s.id::text AS space_id,
			s.key AS space_key, s.space_type, p.title, p.body, p.status, p.author_id AS creator,
			COALESCE(p.parent_id::text,'') AS parent_id, s.key AS container_id, s.name AS container_title,
			p.created_at, v.created_at AS last_modified,
			`+labelsFrom("wiki_page_labels", "page_id", "p.id")+` AS labels,
			ARRAY(SELECT DISTINCT pv.author_id FROM wiki_page_versions pv WHERE pv.page_id=p.id) AS contributors,
			`+watchersOf("p.id::text")+` AS watchers,
			`+favouritesOf("content", "p.id::text")+` AS favourited_by,
			ARRAY(SELECT pa.ancestor_id::text FROM page_ancestry pa WHERE pa.page_id=p.id) AS ancestors,
			`+macrosIn("p.body")+` AS macros,
			`+mentionsIn("p.body")+` AS mentions
			FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
			JOIN wiki_page_versions v ON v.page_id=p.id AND v.version=p.version
			WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND p.status = ANY($3)`)
	}
	if wanted["blogpost"] {
		branches = append(branches, `SELECT 'blogpost', b.id::text, s.id::text,
			s.key, s.space_type, b.title, b.body, b.status, b.author_id,
			'', s.key, s.name,
			b.created_at, v.created_at,
			`+labelsFrom("wiki_blog_post_labels", "blog_post_id", "b.id")+`,
			ARRAY(SELECT DISTINCT bv.author_id FROM wiki_blog_post_versions bv WHERE bv.blog_post_id=b.id),
			`+watchersOf("b.id::text")+`,
			`+favouritesOf("content", "b.id::text")+`,
			`+emptyTextArray+`,
			`+macrosIn("b.body")+`,
			`+mentionsIn("b.body")+`
			FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id
			JOIN wiki_blog_post_versions v ON v.blog_post_id=b.id AND v.version=b.version
			WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiBlogPostVisible+` AND b.status = ANY($3)`)
	}
	if wanted["comment"] {
		// A comment has no title of its own, so it is shown by what it is on,
		// which is how a reader recognises it in a list of results.
		branches = append(branches, `SELECT 'comment', c.id::text, COALESCE(p.space_id,bp.space_id)::text,
			s.key, s.space_type, 'Re: ' || COALESCE(p.title,bp.title,''), c.body, 'current', c.author_id,
			COALESCE(p.id::text,bp.id::text,''), COALESCE(p.id::text,bp.id::text,''), COALESCE(p.title,bp.title,''),
			c.created_at, c.updated_at,
			`+emptyTextArray+`,
			ARRAY(SELECT DISTINCT cv.author_id FROM wiki_footer_comment_versions cv WHERE cv.comment_id=c.id),
			`+watchersOf("c.id::text")+`,
			`+favouritesOf("content", "c.id::text")+`,
			`+arrayOfContainer("COALESCE(p.id::text,bp.id::text,'')")+`,
			`+macrosIn("c.body")+`,
			`+mentionsIn("c.body")+`
			FROM wiki_footer_comments c
			LEFT JOIN wiki_attachments ca ON ca.id=c.attachment_id
			LEFT JOIN wiki_pages p ON p.id=COALESCE(c.page_id,ca.page_id)
			LEFT JOIN wiki_blog_posts bp ON bp.id=COALESCE(c.blog_post_id,ca.blog_post_id)
			JOIN wiki_spaces s ON s.id=COALESCE(p.space_id,bp.space_id)
			WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND 'current' = ANY($3)`)
	}
	if wanted["attachment"] {
		branches = append(branches, `SELECT 'attachment', a.id::text, COALESCE(p.space_id,b.space_id)::text,
			s.key, s.space_type, a.filename, a.comment, a.status, a.author_id,
			COALESCE(p.id::text,b.id::text,''), COALESCE(p.id::text,b.id::text,''), COALESCE(p.title,b.title,''),
			a.created_at, v.created_at,
			`+labelsFrom("wiki_attachment_labels", "attachment_id", "a.id")+`,
			ARRAY(SELECT DISTINCT av.author_id FROM wiki_attachment_versions av WHERE av.attachment_id=a.id),
			`+watchersOf("a.id::text")+`,
			`+favouritesOf("content", "a.id::text")+`,
			`+arrayOfContainer("COALESCE(p.id::text,b.id::text,'')")+`,
			`+emptyTextArray+`,
			`+emptyTextArray+`
			FROM wiki_attachments a
			LEFT JOIN wiki_pages p ON p.id=a.page_id
			LEFT JOIN wiki_blog_posts b ON b.id=a.blog_post_id
			JOIN wiki_spaces s ON s.id=COALESCE(p.space_id,b.space_id)
			JOIN wiki_attachment_versions v ON v.attachment_id=a.id AND v.version=a.version
			WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiAttachmentVisible+` AND a.status = ANY($3)`)
	}
	if wanted["space"] {
		// A space records when it was made but not when it last changed, so the
		// two stamps are the same one rather than an invented value.
		branches = append(branches, `SELECT 'space', s.id::text, s.id::text,
			s.key, s.space_type, s.name, s.description, s.status, s.author_id,
			'', s.key, s.name,
			s.created_at, s.created_at,
			`+labelsFrom("wiki_space_labels", "space_id", "s.id")+`,
			ARRAY[s.author_id],
			ARRAY(SELECT w.user_id FROM wiki_watches w WHERE w.workspace_id=s.workspace_id AND w.target_type='space' AND w.target_id=s.key),
			`+favouritesOf("space", "s.key")+`,
			`+emptyTextArray+`,
			`+emptyTextArray+`,
			`+emptyTextArray+`
			FROM wiki_spaces s WHERE s.workspace_id=$1 AND `+wikiSpaceVisible)
	}
	if len(branches) == 0 {
		return "", fmt.Errorf("%w: no content type to search", ErrWikiSearchValidation)
	}
	return strings.Join(branches, "\nUNION ALL\n"), nil
}

// SearchWiki runs a CQL query and returns the page of matches asked for,
// together with how many there were in total.
func (s *Store) SearchWiki(ctx context.Context, ws, actor string, request WikiSearchRequest) ([]WikiSearchResult, int, error) {
	if err := s.requireMember(ctx, ws, actor); err != nil {
		return nil, 0, err
	}
	query, err := cql.Parse(request.CQL)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %s", ErrWikiSearchValidation, err.Error())
	}
	if len(request.ContentStatuses) == 0 {
		request.ContentStatuses = []string{"current"}
	}
	for _, status := range request.ContentStatuses {
		switch status {
		case "current", "draft", "archived", "trashed":
		default:
			return nil, 0, fmt.Errorf("%w: a content status is current, draft, archived or trashed", ErrWikiSearchValidation)
		}
	}
	view, err := s.searchableView(request)
	if err != nil {
		return nil, 0, err
	}

	// $1 and $2 belong to the visibility rules and $3 to the status scope, so
	// the query's own values start after them.
	args := []any{ws, actor, request.ContentStatuses}
	predicate, queryArgs, order, err := cql.Compile(query, cql.Context{
		Actor: actor, Now: request.Now, CurrentSpace: request.SpaceKey,
	}, len(args)+1)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %s", ErrWikiSearchValidation, err.Error())
	}
	args = append(args, queryArgs...)

	scope := []string{predicate}
	if request.SpaceKey != "" {
		args = append(args, request.SpaceKey)
		scope = append(scope, cql.ColumnSpaceKey+" = $"+strconv.Itoa(len(args)))
	}
	if request.ContentID != "" {
		args = append(args, request.ContentID)
		scope = append(scope, cql.ColumnEntityID+" = $"+strconv.Itoa(len(args)))
	}
	// Archived spaces are left out of a search unless they were asked for, and
	// asking only for them leaves out the current ones.
	switch {
	case request.ExcludeCurrentSpaces:
		scope = append(scope, "space_status = 'archived'")
	case !request.IncludeArchivedSpaces:
		scope = append(scope, "space_status <> 'archived'")
	}

	// The space status is needed to scope the search but is not something CQL
	// compiles against, so it is carried alongside the compiler's columns.
	statement := pageAncestry + `, searchable AS (
		SELECT v.*, (SELECT ws.status FROM wiki_spaces ws WHERE ws.id::text=v.space_id) AS space_status
		FROM (` + view + `) v
	)
	SELECT entity_type, entity_id, space_id, space_key, title, body, status, creator,
		parent_id, container_id, container_title, created_at, last_modified,
		count(*) OVER () AS total
	FROM searchable WHERE ` + strings.Join(scope, " AND ") + `
	ORDER BY ` + order

	limit := request.Limit
	if limit < 0 {
		limit = 0
	}
	start := request.Start
	if start < 0 {
		start = 0
	}
	args = append(args, limit, start)
	statement += ` LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args))

	rows, err := s.Pool.Query(ctx, statement, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	results := []WikiSearchResult{}
	total := 0
	for rows.Next() {
		var result WikiSearchResult
		if err = rows.Scan(&result.EntityType, &result.ID, &result.SpaceID, &result.SpaceKey,
			&result.Title, &result.Body, &result.Status, &result.Creator,
			&result.ParentID, &result.ContainerID, &result.ContainerTitle,
			&result.CreatedAt, &result.LastModified, &total); err != nil {
			return nil, 0, err
		}
		results = append(results, result)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	// A window count only arrives with a row, so an empty page needs the total
	// asked for separately — otherwise paging past the end would report none.
	if len(results) == 0 && (start > 0 || limit == 0) {
		if err = s.Pool.QueryRow(ctx, pageAncestry+`, searchable AS (
			SELECT v.*, (SELECT ws.status FROM wiki_spaces ws WHERE ws.id::text=v.space_id) AS space_status
			FROM (`+view+`) v
		) SELECT count(*) FROM searchable WHERE `+strings.Join(scope, " AND "),
			args[:len(args)-2]...).Scan(&total); err != nil {
			return nil, 0, err
		}
	}
	return results, total, nil
}
