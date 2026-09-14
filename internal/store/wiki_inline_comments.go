package store

import (
	"context"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

func (s *Store) WikiInlineComment(ctx context.Context, ws, user, id string) (*models.WikiFooterComment, error) {
	return scanWikiFooterComment(s.Pool.QueryRow(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='inline' AND c.id::text=$3`, ws, user, id))
}

func (s *Store) WikiInlineComments(ctx context.Context, ws, user, pageID string) ([]*models.WikiFooterComment, error) {
	rows, err := s.Pool.Query(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='inline' AND c.parent_id IS NULL AND ($3='' OR c.page_id::text=$3) ORDER BY c.created_at,c.id`, ws, user, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiFooterComments(rows)
}

func (s *Store) WikiInlineCommentThread(ctx context.Context, ws, user, pageID string) ([]*models.WikiFooterComment, error) {
	if _, err := s.WikiPage(ctx, ws, user, pageID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='inline' AND c.page_id::text=$3 ORDER BY c.created_at,c.id`, ws, user, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiFooterComments(rows)
}

func (s *Store) WikiBlogInlineComments(ctx context.Context, ws, user, blogPostID string) ([]*models.WikiFooterComment, error) {
	if _, err := s.WikiBlogPost(ctx, ws, user, blogPostID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='inline' AND c.parent_id IS NULL AND c.blog_post_id::text=$3 ORDER BY c.created_at,c.id`, ws, user, blogPostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiFooterComments(rows)
}

func (s *Store) WikiBlogInlineCommentThread(ctx context.Context, ws, user, blogPostID string) ([]*models.WikiFooterComment, error) {
	if _, err := s.WikiBlogPost(ctx, ws, user, blogPostID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='inline' AND c.blog_post_id::text=$3 ORDER BY c.created_at,c.id`, ws, user, blogPostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiFooterComments(rows)
}

func (s *Store) WikiInlineCommentChildren(ctx context.Context, ws, user, parentID string) ([]*models.WikiFooterComment, error) {
	if _, err := s.WikiInlineComment(ctx, ws, user, parentID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='inline' AND c.parent_id::text=$3 ORDER BY c.created_at,c.id`, ws, user, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiFooterComments(rows)
}

func (s *Store) CreateWikiInlineComment(ctx context.Context, ws, actor string, input models.WikiFooterComment) (*models.WikiFooterComment, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if input.ParentCommentID != "" {
		parent, err := scanWikiFooterComment(tx.QueryRow(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanCreateComment+` AND `+wikiCommentVisible+` AND c.comment_type='inline' AND c.id::text=$3 FOR SHARE OF c`, ws, actor, input.ParentCommentID))
		if err != nil {
			return nil, err
		}
		input.PageID, input.BlogPostID, input.InlineSelection = parent.PageID, parent.BlogPostID, parent.InlineSelection
		input.InlineMatchCount, input.InlineMatchIndex = parent.InlineMatchCount, parent.InlineMatchIndex
	} else if input.BlogPostID != "" {
		blog, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanCreateComment+` AND `+wikiBlogPostVisible+` AND b.status='current' AND b.id::text=$3 FOR SHARE OF b`, ws, actor, input.BlogPostID))
		if err != nil {
			return nil, err
		}
		matches := strings.Count(blog.Body.Value, input.InlineSelection)
		if matches == 0 || matches != input.InlineMatchCount || input.InlineMatchIndex < 0 || input.InlineMatchIndex >= matches {
			return nil, ErrWikiValidation
		}
	} else {
		page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanCreateComment+` AND `+wikiPageVisible+` AND p.status='current' AND p.id::text=$3 FOR SHARE OF p`, ws, actor, input.PageID))
		if err != nil {
			return nil, err
		}
		matches := strings.Count(page.Body.Value, input.InlineSelection)
		if matches == 0 || matches != input.InlineMatchCount || input.InlineMatchIndex < 0 || input.InlineMatchIndex >= matches {
			return nil, ErrWikiValidation
		}
	}
	input.Version.Number, input.AuthorID, input.CommentType, input.ResolutionStatus = 1, actor, "inline", "open"
	if err := tx.QueryRow(ctx, `INSERT INTO wiki_footer_comments(page_id,blog_post_id,parent_id,body,author_id,comment_type,inline_selection,inline_match_count,inline_match_index,inline_marker_ref) VALUES($1::bigint,$2::bigint,$3::bigint,$4,$5,'inline',$6,$7,$8,'pending') RETURNING id::text`, nilIfEmpty(input.PageID), nilIfEmpty(input.BlogPostID), nilIfEmpty(input.ParentCommentID), input.Body.Value, actor, input.InlineSelection, input.InlineMatchCount, input.InlineMatchIndex).Scan(&input.ID); err != nil {
		return nil, err
	}
	input.InlineMarkerRef = "inline-comment-" + input.ID
	if _, err := tx.Exec(ctx, `UPDATE wiki_footer_comments SET inline_marker_ref=$2 WHERE id::text=$1`, input.ID, input.InlineMarkerRef); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO wiki_footer_comment_versions(comment_id,version,body,author_id,message) VALUES($1::bigint,1,$2,$3,$4)`, input.ID, input.Body.Value, actor, input.Version.Message); err != nil {
		return nil, err
	}
	if err := notifyCommentMentions(ctx, tx, ws, actor, input.ID, "", input.Body.Value); err != nil {
		return nil, err
	}
	comment, err := scanWikiFooterComment(tx.QueryRow(ctx, wikiCommentSelect+` WHERE c.id::text=$1`, input.ID))
	if err != nil {
		return nil, err
	}
	if err := wikiCommentAction(ctx, tx, ws, actor, "wiki_inline_comment", comment, models.OpUpsert); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return comment, nil
}

func (s *Store) UpdateWikiInlineComment(ctx context.Context, ws, actor string, input models.WikiFooterComment, resolved *bool) (*models.WikiFooterComment, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	old, err := scanWikiFooterComment(tx.QueryRow(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanUpdateComment+` AND `+wikiCommentVisible+` AND c.comment_type='inline' AND c.id::text=$3 FOR UPDATE OF c`, ws, actor, input.ID))
	if err != nil {
		return nil, err
	}
	if old.ResolutionStatus == "dangling" {
		return nil, ErrWikiValidation
	}
	if err := wikiCommentAuthorOrAdmin(ctx, tx, ws, actor, old.AuthorID); err != nil {
		return nil, err
	}
	if input.Version.Number != old.Version.Number+1 {
		return nil, ErrWikiCommentConflict
	}
	status, modifier := old.ResolutionStatus, old.ResolutionModifierID
	var changedResolution bool
	if resolved != nil && *resolved && status != "resolved" {
		status, modifier, changedResolution = "resolved", actor, true
	} else if resolved != nil && !*resolved && status == "resolved" {
		status, modifier, changedResolution = "reopened", actor, true
	}
	if input.Body.Value == "" {
		input.Body = old.Body
	}
	if _, err := tx.Exec(ctx, `UPDATE wiki_footer_comments SET body=$2,version=$3,updated_at=now(),resolution_status=$4,resolution_modifier_id=NULLIF($5,''),resolution_modified_at=CASE WHEN $6 THEN now() ELSE resolution_modified_at END WHERE id::text=$1`, input.ID, input.Body.Value, input.Version.Number, status, modifier, changedResolution); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO wiki_footer_comment_versions(comment_id,version,body,author_id,message) VALUES($1::bigint,$2,$3,$4,$5)`, input.ID, input.Version.Number, input.Body.Value, actor, input.Version.Message); err != nil {
		return nil, err
	}
	if err := notifyCommentMentions(ctx, tx, ws, actor, input.ID, old.Body.Value, input.Body.Value); err != nil {
		return nil, err
	}
	comment, err := scanWikiFooterComment(tx.QueryRow(ctx, wikiCommentSelect+` WHERE c.id::text=$1`, input.ID))
	if err != nil {
		return nil, err
	}
	if err := wikiCommentAction(ctx, tx, ws, actor, "wiki_inline_comment", comment, models.OpUpsert); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return comment, nil
}

func (s *Store) DeleteWikiInlineComment(ctx context.Context, ws, actor, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	comment, err := scanWikiFooterComment(tx.QueryRow(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanDeleteComment+` AND `+wikiCommentVisible+` AND c.comment_type='inline' AND c.id::text=$3 FOR UPDATE OF c`, ws, actor, id))
	if err != nil {
		return err
	}
	if err := wikiCommentAuthorOrAdmin(ctx, tx, ws, actor, comment.AuthorID); err != nil {
		return err
	}
	if err := wikiCommentAction(ctx, tx, ws, actor, "wiki_inline_comment", comment, models.OpDelete); err != nil {
		return err
	}
	if tag, err := tx.Exec(ctx, `DELETE FROM wiki_footer_comments WHERE id::text=$1 AND comment_type='inline'`, id); err != nil {
		return err
	} else if tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return tx.Commit(ctx)
}

func (s *Store) WikiInlineCommentVersions(ctx context.Context, ws, user, id string) ([]models.WikiFooterCommentVersion, error) {
	if _, err := s.WikiInlineComment(ctx, ws, user, id); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT version,message,author_id,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),body FROM wiki_footer_comment_versions WHERE comment_id::text=$1 ORDER BY version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := []models.WikiFooterCommentVersion{}
	for rows.Next() {
		v := models.WikiFooterCommentVersion{Body: models.WikiBody{Representation: "storage"}}
		if err := rows.Scan(&v.Number, &v.Message, &v.AuthorID, &v.CreatedAt, &v.Body.Value); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func (s *Store) WikiInlineCommentVersion(ctx context.Context, ws, user, id string, number int) (*models.WikiFooterCommentVersion, error) {
	if _, err := s.WikiInlineComment(ctx, ws, user, id); err != nil {
		return nil, err
	}
	v := &models.WikiFooterCommentVersion{Body: models.WikiBody{Representation: "storage"}}
	err := s.Pool.QueryRow(ctx, `SELECT version,message,author_id,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),body FROM wiki_footer_comment_versions WHERE comment_id::text=$1 AND version=$2`, id, number).Scan(&v.Number, &v.Message, &v.AuthorID, &v.CreatedAt, &v.Body.Value)
	return v, err
}

func (s *Store) WikiInlineCommentLikes(ctx context.Context, ws, user, id string) ([]string, error) {
	if _, err := s.WikiInlineComment(ctx, ws, user, id); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT l.user_id FROM wiki_footer_comment_likes l JOIN users u ON u.id=l.user_id AND u.active WHERE l.comment_id::text=$1 ORDER BY l.created_at,l.user_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var users []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		users = append(users, id)
	}
	return users, rows.Err()
}

// relocateInlineComments keeps a page's or blog post's inline comments on the
// passage they were left on after the body changes. A passage that still
// appears keeps its comment, and the comment's match count and index follow
// the new body. A passage that no longer appears leaves its comment dangling,
// as Confluence marks it, and a dangling comment whose passage comes back is
// anchored again.
func relocateInlineComments(ctx context.Context, tx pgx.Tx, ws, actor, contentType, contentID, body string) error {
	column := "page_id"
	if contentType == "blogpost" {
		column = "blog_post_id"
	}
	rows, err := tx.Query(ctx, `SELECT id::text,inline_selection,inline_match_count,inline_match_index,resolution_status
		FROM wiki_footer_comments WHERE comment_type='inline' AND parent_id IS NULL AND `+column+`::text=$1
		ORDER BY id FOR UPDATE`, contentID)
	if err != nil {
		return err
	}
	type anchor struct {
		id, selection, status string
		count, index          int
	}
	anchors := []anchor{}
	for rows.Next() {
		var a anchor
		if err := rows.Scan(&a.id, &a.selection, &a.count, &a.index, &a.status); err != nil {
			rows.Close()
			return err
		}
		anchors = append(anchors, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, a := range anchors {
		matches := strings.Count(body, a.selection)
		status, count, index := a.status, a.count, a.index
		if matches == 0 {
			status = "dangling"
		} else {
			if status == "dangling" {
				status = "open"
			}
			count = matches
			if index >= matches {
				index = matches - 1
			}
		}
		if status == a.status && count == a.count && index == a.index {
			continue
		}
		if _, err := tx.Exec(ctx, `UPDATE wiki_footer_comments SET resolution_status=$2,inline_match_count=$3,inline_match_index=$4,
			resolution_modified_at=CASE WHEN resolution_status<>$2 THEN now() ELSE resolution_modified_at END
			WHERE id::text=$1`, a.id, status, count, index); err != nil {
			return err
		}
		comment, err := scanWikiFooterComment(tx.QueryRow(ctx, wikiCommentSelect+` WHERE c.id::text=$1`, a.id))
		if err != nil {
			return err
		}
		if err := wikiCommentAction(ctx, tx, ws, actor, "wiki_inline_comment", comment, models.OpUpsert); err != nil {
			return err
		}
	}
	return nil
}
