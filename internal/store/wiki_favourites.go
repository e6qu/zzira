package store

import (
	"context"

	"github.com/e6qu/zzira/internal/models"
)

// Starring a page keeps it close at hand; the page reports it as favourited by
// the reader, and the wiki lists what each reader has starred.

// SetWikiPageFavourite stars or unstars a page the reader can see.
func (s *Store) SetWikiPageFavourite(ctx context.Context, ws, user, pageID string, favourite bool) error {
	if _, err := s.WikiPage(ctx, ws, user, pageID); err != nil {
		return err
	}
	if favourite {
		_, err := s.Pool.Exec(ctx, `INSERT INTO wiki_favourites(workspace_id,user_id,page_id) VALUES($1,$2,$3::bigint)
			ON CONFLICT DO NOTHING`, ws, user, pageID)
		return err
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM wiki_favourites WHERE workspace_id=$1 AND user_id=$2 AND page_id::text=$3`, ws, user, pageID)
	return err
}

// IsWikiPageFavourite reports whether the reader has starred a page.
func (s *Store) IsWikiPageFavourite(ctx context.Context, ws, user, pageID string) (bool, error) {
	var favourite bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_favourites WHERE workspace_id=$1 AND user_id=$2 AND page_id::text=$3)`,
		ws, user, pageID).Scan(&favourite)
	return favourite, err
}

// WikiFavouritePages lists the current pages a reader has starred and can
// still see, most recently starred first.
func (s *Store) WikiFavouritePages(ctx context.Context, ws, user string) ([]*models.WikiPage, error) {
	rows, err := s.Pool.Query(ctx, wikiPageSelect+` JOIN wiki_favourites f ON f.page_id=p.id AND f.user_id=$2
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
