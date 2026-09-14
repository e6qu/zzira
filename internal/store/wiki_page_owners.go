package store

import (
	"context"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
)

// TransferWikiPageOwnership hands a page to another member of the site. The
// page remembers who owned it before. Changing hands is not an edit, so no new
// version is written.
func (s *Store) TransferWikiPageOwnership(ctx context.Context, ws, actor, pageID, ownerID string) (*models.WikiPage, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = lockWritablePage(ctx, tx, ws, actor, pageID, "current"); err != nil {
		return nil, err
	}
	var member bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships WHERE workspace_id=$1 AND user_id=$2)`, ws, ownerID).Scan(&member); err != nil {
		return nil, err
	}
	if !member {
		return nil, fmt.Errorf("%w: the new owner must be a member of this site", ErrWikiValidation)
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_pages SET last_owner_id=COALESCE(owner_id,author_id),owner_id=$2
		WHERE id::text=$1 AND COALESCE(owner_id,author_id)<>$2`, pageID, ownerID); err != nil {
		return nil, err
	}
	if err = wikiPageSnapshotAction(ctx, tx, ws, actor, pageID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.WikiPage(ctx, ws, actor, pageID)
}
