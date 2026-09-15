package store

import (
	"context"

	"github.com/e6qu/zzira/internal/models"
)

// Starring a page keeps it close at hand; the page reports it as favourited by
// the reader, and the wiki lists what each reader has starred.

// WikiPageCollaborators lists everyone who has written a version of a page, in
// the order they first did.
func (s *Store) WikiPageCollaborators(ctx context.Context, ws, user, pageID string) ([]string, error) {
	if _, err := s.WikiPage(ctx, ws, user, pageID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT author_id FROM wiki_page_versions WHERE page_id::text=$1
		GROUP BY author_id ORDER BY min(version)`, pageID)
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

// WikiAttachmentCollaborators lists everyone who has uploaded a version of an
// attachment, in the order they first did.
func (s *Store) WikiAttachmentCollaborators(ctx context.Context, ws, user, attachmentID string) ([]string, error) {
	if _, err := s.WikiAttachment(ctx, ws, user, attachmentID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT author_id FROM wiki_attachment_versions WHERE attachment_id::text=$1
		GROUP BY author_id ORDER BY min(version)`, attachmentID)
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

// A star is Confluence's favourite relation: a link from the user to the
// content, which the relations API reads and CQL's favourite field searches.

func (s *Store) setFavouriteRelation(ctx context.Context, ws, user, contentID string, favourite bool) error {
	if favourite {
		_, err := s.Pool.Exec(ctx, `INSERT INTO wiki_relations(workspace_id,name,source_type,source_key,source_status,source_version,target_type,target_key,target_status,target_version,created_by)
			VALUES($1,'favourite','user',$2,'current',0,'content',$3,'current',0,$2)
			ON CONFLICT (workspace_id,name,source_type,source_key,source_status,source_version,target_type,target_key,target_status,target_version) DO NOTHING`,
			ws, user, contentID)
		return err
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM wiki_relations WHERE workspace_id=$1 AND name='favourite'
		AND source_type='user' AND source_key=$2 AND target_type='content' AND target_key=$3`, ws, user, contentID)
	return err
}

func (s *Store) isFavouriteRelation(ctx context.Context, ws, user, contentID string) (bool, error) {
	var favourite bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_relations WHERE workspace_id=$1 AND name='favourite'
		AND source_type='user' AND source_key=$2 AND target_type='content' AND target_key=$3)`, ws, user, contentID).Scan(&favourite)
	return favourite, err
}

// SetWikiPageFavourite stars or unstars a page the reader can see.
func (s *Store) SetWikiPageFavourite(ctx context.Context, ws, user, pageID string, favourite bool) error {
	if _, err := s.WikiPage(ctx, ws, user, pageID); err != nil {
		return err
	}
	return s.setFavouriteRelation(ctx, ws, user, pageID, favourite)
}

// IsWikiPageFavourite reports whether the reader has starred a page.
func (s *Store) IsWikiPageFavourite(ctx context.Context, ws, user, pageID string) (bool, error) {
	return s.isFavouriteRelation(ctx, ws, user, pageID)
}

// WikiFavouritePages lists the current pages a reader has starred and can
// still see, most recently starred first.
func (s *Store) WikiFavouritePages(ctx context.Context, ws, user string) ([]*models.WikiPage, error) {
	rows, err := s.Pool.Query(ctx, wikiPageSelect+` JOIN wiki_relations f ON f.target_key=p.id::text AND f.name='favourite'
		AND f.source_type='user' AND f.source_key=$2 AND f.target_type='content'
		WHERE s.workspace_id=$1 AND f.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND p.status='current'
		ORDER BY f.created_at DESC, p.id`, ws, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pages := []*models.WikiPage{}
	for rows.Next() {
		page, scanErr := scanWikiPage(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		pages = append(pages, page)
	}
	return pages, rows.Err()
}

// SetWikiBlogPostFavourite stars or unstars a blog post the reader can see.
func (s *Store) SetWikiBlogPostFavourite(ctx context.Context, ws, user, id string, favourite bool) error {
	if _, err := s.WikiBlogPost(ctx, ws, user, id); err != nil {
		return err
	}
	return s.setFavouriteRelation(ctx, ws, user, id, favourite)
}

// IsWikiBlogPostFavourite reports whether the reader starred a blog post.
func (s *Store) IsWikiBlogPostFavourite(ctx context.Context, ws, user, id string) (bool, error) {
	return s.isFavouriteRelation(ctx, ws, user, id)
}

// WikiFavouriteBlogPosts lists the current blog posts a reader starred.
func (s *Store) WikiFavouriteBlogPosts(ctx context.Context, ws, user string) ([]*models.WikiBlogPost, error) {
	rows, err := s.Pool.Query(ctx, wikiBlogPostSelect+` JOIN wiki_relations f ON f.target_key=b.id::text AND f.name='favourite'
		AND f.source_type='user' AND f.source_key=$2 AND f.target_type='content'
		WHERE s.workspace_id=$1 AND f.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiBlogPostVisible+` AND b.status='current'
		ORDER BY f.created_at DESC, b.id`, ws, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	posts := []*models.WikiBlogPost{}
	for rows.Next() {
		post, scanErr := scanWikiBlogPost(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		posts = append(posts, post)
	}
	return posts, rows.Err()
}
