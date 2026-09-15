package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/wikimarkup"
	"github.com/jackc/pgx/v5"
)

// A published page or blog post may have one unpublished draft beside it.
// Saving the draft leaves the published version as it is; publishing replaces
// the draft. A page or blog post that was never published is itself a draft,
// and discarding it removes it for good.

// WikiContentDraft is the unpublished draft of published content.
type WikiContentDraft struct {
	ContentType, ContentID, Title, AuthorID, CreatedAt, UpdatedAt string
	Body                                                          models.WikiBody
}

// WikiVersionBody is what one version of a page or blog post said.
type WikiVersionBody struct {
	Title, Body string
}

func lockWritablePublishedContent(ctx context.Context, tx pgx.Tx, ws, actor, contentType, id string) error {
	switch contentType {
	case "page":
		_, err := lockWritablePage(ctx, tx, ws, actor, id, "current")
		return err
	case "blogpost":
		var locked string
		return tx.QueryRow(ctx, `SELECT b.id::text FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id
			WHERE s.workspace_id=$1 AND `+wikiBlogPostWritable+` AND `+wikiBlogPostVisible+` AND b.id::text=$3 AND b.status='current'
			FOR UPDATE OF b`, ws, actor, id).Scan(&locked)
	}
	return fmt.Errorf("%w: drafts belong to pages and blog posts", ErrWikiValidation)
}

// SaveWikiContentDraft saves the draft of a published page or blog post,
// replacing any draft already there.
func (s *Store) SaveWikiContentDraft(ctx context.Context, ws, actor, contentType, id, title string, body models.WikiBody) (*WikiContentDraft, error) {
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 255 {
		return nil, fmt.Errorf("%w: a draft title of 1 to 255 characters is required", ErrWikiValidation)
	}
	if body.Representation != "" && body.Representation != "storage" {
		return nil, fmt.Errorf("%w: drafts are kept as storage", ErrWikiValidation)
	}
	if _, err := wikimarkup.Render(body.Value); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrWikiValidation, err)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockWritablePublishedContent(ctx, tx, ws, actor, contentType, id); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_content_drafts(workspace_id,content_type,content_id,title,body,author_id)
		VALUES($1,$2,$3::bigint,$4,$5,$6)
		ON CONFLICT (content_type,content_id) DO UPDATE SET title=EXCLUDED.title,body=EXCLUDED.body,author_id=EXCLUDED.author_id,updated_at=now()`,
		ws, contentType, id, title, body.Value, actor); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.WikiContentDraft(ctx, ws, actor, contentType, id)
}

// WikiContentDraft reads the draft of published content. Only people who may
// edit the content see its draft.
func (s *Store) WikiContentDraft(ctx context.Context, ws, user, contentType, id string) (*WikiContentDraft, error) {
	var canEdit bool
	var err error
	switch contentType {
	case "page":
		if _, err = s.WikiPage(ctx, ws, user, id); err != nil {
			return nil, err
		}
		canEdit, err = s.CanUpdateWikiPage(ctx, ws, user, id)
	case "blogpost":
		if _, err = s.WikiBlogPost(ctx, ws, user, id); err != nil {
			return nil, err
		}
		canEdit, err = s.CanUpdateWikiBlogPost(ctx, ws, user, id)
	default:
		return nil, fmt.Errorf("%w: drafts belong to pages and blog posts", ErrWikiValidation)
	}
	if err != nil {
		return nil, err
	}
	if !canEdit {
		return nil, pgx.ErrNoRows
	}
	draft := &WikiContentDraft{ContentType: contentType, ContentID: id, Body: models.WikiBody{Representation: "storage"}}
	err = s.Pool.QueryRow(ctx, `SELECT title,body,author_id,
		to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM wiki_content_drafts WHERE workspace_id=$1 AND content_type=$2 AND content_id::text=$3`, ws, contentType, id).
		Scan(&draft.Title, &draft.Body.Value, &draft.AuthorID, &draft.CreatedAt, &draft.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return draft, nil
}

// DiscardWikiContentDraft throws away the draft of published content.
func (s *Store) DiscardWikiContentDraft(ctx context.Context, ws, actor, contentType, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockWritablePublishedContent(ctx, tx, ws, actor, contentType, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM wiki_content_drafts WHERE workspace_id=$1 AND content_type=$2 AND content_id::text=$3`, ws, contentType, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return tx.Commit(ctx)
}

// DeleteUnpublishedWikiDraft removes a page or blog post that was never
// published. A discarded draft does not go to the trash.
func (s *Store) DeleteUnpublishedWikiDraft(ctx context.Context, ws, actor, contentType, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	switch contentType {
	case "page":
		page, lookupErr := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+`
			AND p.id::text=$3 AND p.status='draft' AND NOT p.published FOR UPDATE OF p`, ws, actor, id))
		if lookupErr != nil {
			return lookupErr
		}
		if page.AuthorID != actor {
			return pgx.ErrNoRows
		}
		// A draft that something else hangs off cannot vanish from under it.
		var holdsContent bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_pages WHERE parent_id::text=$1)
			OR EXISTS(SELECT 1 FROM wiki_content WHERE parent_page_id::text=$1 OR root_page_id::text=$1)`, id).Scan(&holdsContent); err != nil {
			return err
		}
		if holdsContent {
			return fmt.Errorf("%w: move or delete the content beneath this draft before discarding it", ErrWikiValidation)
		}
		// Versions are not removed with their page, so they go first.
		if _, err = tx.Exec(ctx, `DELETE FROM wiki_page_versions WHERE page_id::text=$1`, id); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM wiki_pages WHERE id::text=$1`, id); err != nil {
			return err
		}
		if err = wikiDeleteAction(ctx, tx, ws, actor, "wiki_page", page.ID, page.SpaceID, page); err != nil {
			return err
		}
	case "blogpost":
		post, lookupErr := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiBlogPostVisible+`
			AND b.id::text=$3 AND b.status='draft' AND NOT b.published FOR UPDATE OF b`, ws, actor, id))
		if lookupErr != nil {
			return lookupErr
		}
		if post.AuthorID != actor {
			return pgx.ErrNoRows
		}
		if _, err = tx.Exec(ctx, `DELETE FROM wiki_blog_posts WHERE id::text=$1`, id); err != nil {
			return err
		}
		if err = wikiDeleteAction(ctx, tx, ws, actor, "wiki_blogpost", post.ID, post.SpaceID, post); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: drafts belong to pages and blog posts", ErrWikiValidation)
	}
	return tx.Commit(ctx)
}

func wikiDeleteAction(ctx context.Context, tx pgx.Tx, ws, actor, entity, id, spaceID string, value any) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": spaceID, entity: value})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: entity, EntityID: id, Op: models.OpDelete, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

// PurgeWikiPage takes a trashed page out of the trash. It becomes deleted,
// which only space administrators see and from which it can be restored.
func (s *Store) PurgeWikiPage(ctx context.Context, ws, actor, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var spaceID string
	if err = tx.QueryRow(ctx, `SELECT p.space_id::text FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
		WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND p.id::text=$3 AND p.status='trashed'
		FOR UPDATE OF p`, ws, actor, id).Scan(&spaceID); err != nil {
		return err
	}
	if err = wikiSpaceAdmin(ctx, tx, ws, actor, spaceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_pages SET status='deleted' WHERE id::text=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_content_drafts WHERE content_type='page' AND content_id::text=$1`, id); err != nil {
		return err
	}
	if err = wikiPageSnapshotAction(ctx, tx, ws, actor, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// PurgeWikiBlogPost takes a trashed blog post out of the trash, as a purged
// page is.
func (s *Store) PurgeWikiBlogPost(ctx context.Context, ws, actor, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	post, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiBlogPostVisible+`
		AND `+wikiBlogPostAuthorWritable+` AND b.id::text=$3 AND b.status='trashed' FOR UPDATE OF b`, ws, actor, id))
	if err != nil {
		return err
	}
	if err = wikiSpaceAdmin(ctx, tx, ws, actor, post.SpaceID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_blog_posts SET status='deleted' WHERE id::text=$1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_content_drafts WHERE content_type='blogpost' AND content_id::text=$1`, id); err != nil {
		return err
	}
	post.Status = "deleted"
	if err = wikiAction(ctx, tx, ws, actor, "wiki_blogpost", post.ID, post.SpaceID, post); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// WikiPageVersionBodies reads what each version of a page said.
func (s *Store) WikiPageVersionBodies(ctx context.Context, ws, user, pageID string) (map[int]WikiVersionBody, error) {
	if _, err := s.WikiPage(ctx, ws, user, pageID); err != nil {
		return nil, err
	}
	return queryVersionBodies(ctx, s, `SELECT version,title,body FROM wiki_page_versions
		WHERE page_id::text=$1 AND (status<>'draft' OR author_id=$2)`, pageID, user)
}

// WikiBlogPostVersionBodies reads what each version of a blog post said.
func (s *Store) WikiBlogPostVersionBodies(ctx context.Context, ws, user, blogPostID string) (map[int]WikiVersionBody, error) {
	if _, err := s.WikiBlogPost(ctx, ws, user, blogPostID); err != nil {
		return nil, err
	}
	return queryVersionBodies(ctx, s, `SELECT version,title,body FROM wiki_blog_post_versions
		WHERE blog_post_id::text=$1 AND (status<>'draft' OR author_id=$2)`, blogPostID, user)
}

func queryVersionBodies(ctx context.Context, s *Store, query string, args ...any) (map[int]WikiVersionBody, error) {
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	bodies := map[int]WikiVersionBody{}
	for rows.Next() {
		var number int
		var body WikiVersionBody
		if err := rows.Scan(&number, &body.Title, &body.Body); err != nil {
			return nil, err
		}
		bodies[number] = body
	}
	return bodies, rows.Err()
}

// WikiBlogPostAtVersion reads a blog post as an earlier version left it.
func (s *Store) WikiBlogPostAtVersion(ctx context.Context, ws, user, id string, version int) (*models.WikiBlogPost, error) {
	post, err := s.WikiBlogPost(ctx, ws, user, id)
	if err != nil {
		return nil, err
	}
	err = s.Pool.QueryRow(ctx, `SELECT title,status,body,version,message,minor_edit,author_id,
		to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM wiki_blog_post_versions WHERE blog_post_id::text=$1 AND version=$2 AND (status<>'draft' OR author_id=$3)`, id, version, user).
		Scan(&post.Title, &post.Status, &post.Body.Value, &post.Version.Number, &post.Version.Message, &post.Version.MinorEdit, &post.Version.AuthorID, &post.Version.CreatedAt)
	return post, err
}

// WikiBlogPostCollaborators lists everyone who wrote a version of a blog post.
func (s *Store) WikiBlogPostCollaborators(ctx context.Context, ws, user, id string) ([]string, error) {
	if _, err := s.WikiBlogPost(ctx, ws, user, id); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT author_id FROM wiki_blog_post_versions WHERE blog_post_id::text=$1
		GROUP BY author_id ORDER BY min(version)`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	collaborators := []string{}
	for rows.Next() {
		var accountID string
		if err := rows.Scan(&accountID); err != nil {
			return nil, err
		}
		collaborators = append(collaborators, accountID)
	}
	return collaborators, rows.Err()
}
