package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var ErrSearchSnapshot = errors.New("search snapshot is invalid or expired")

type SearchSnapshotPage struct {
	Issues       []*models.Issue
	NextPosition int64
	HasMore      bool
}

// CreateSearchSnapshot materializes the ordered IDs for one enhanced search.
// Current issue values stay live, but membership and ordering cannot drift as
// callers move through the result set.
func (s *Store) CreateSearchSnapshot(ctx context.Context, workspaceID, userID, queryHash string, compiled jql.Compiled, expiresAt time.Time) (string, error) {
	if compiled.Err != nil {
		return "", compiled.Err
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, `DELETE FROM jira_search_snapshots WHERE expires_at <= now()`); err != nil {
		return "", err
	}
	snapshotID := NewID("search")
	if _, err = tx.Exec(ctx, `
		INSERT INTO jira_search_snapshots(id, workspace_id, user_id, query_hash, expires_at)
		VALUES($1,$2,$3,$4,$5)`, snapshotID, workspaceID, userID, queryHash, expiresAt); err != nil {
		return "", err
	}
	where := "i.workspace_id = $1"
	args := []any{workspaceID}
	if compiled.Where != "" {
		where += " AND (" + compiled.Where + ")"
		args = append(args, compiled.Args...)
	}
	userPlaceholder := fmt.Sprintf("$%d", len(args)+1)
	args = append(args, userID)
	where += " AND " + VisibleIssuePredicate("i", userPlaceholder)
	snapshotPlaceholder := fmt.Sprintf("$%d", len(args)+1)
	args = append(args, snapshotID)
	_, err = tx.Exec(ctx, `
		INSERT INTO jira_search_snapshot_items(snapshot_id, position, issue_id)
		SELECT `+snapshotPlaceholder+`, row_number() OVER (ORDER BY `+compiled.OrderSQL+`)-1, i.id
		`+searchJoin+`
		WHERE `+where, args...)
	if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return snapshotID, nil
}

// SearchSnapshotPage reads the next visible positions from a materialized
// search. Visibility is evaluated again for every page so a permission change
// cannot expose an issue through an older token.
func (s *Store) SearchSnapshotPage(ctx context.Context, snapshotID, workspaceID, userID, queryHash string, position int64, limit int) (*SearchSnapshotPage, error) {
	if position < 0 || limit < 1 {
		return nil, ErrSearchSnapshot
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var valid bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM jira_search_snapshots
		  WHERE id=$1 AND workspace_id=$2 AND user_id=$3 AND query_hash=$4 AND expires_at > now()
		)`, snapshotID, workspaceID, userID, queryHash).Scan(&valid)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, ErrSearchSnapshot
	}
	rows, err := tx.Query(ctx, `
		SELECT item.position, item.issue_id
		FROM jira_search_snapshot_items item
		JOIN issues i ON i.id=item.issue_id
		WHERE item.snapshot_id=$1 AND item.position >= $2
		  AND i.workspace_id=$3 AND `+VisibleIssuePredicate("i", "$4")+`
		ORDER BY item.position
		LIMIT $5`, snapshotID, position, workspaceID, userID, limit+1)
	if err != nil {
		return nil, err
	}
	type snapshotItem struct {
		position int64
		issueID  string
	}
	items := make([]snapshotItem, 0, limit+1)
	for rows.Next() {
		var item snapshotItem
		if err = rows.Scan(&item.position, &item.issueID); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	page := &SearchSnapshotPage{HasMore: len(items) > limit}
	if page.HasMore {
		items = items[:limit]
	}
	if len(items) == 0 {
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return page, nil
	}
	page.NextPosition = items[len(items)-1].position + 1
	ids := make([]string, len(items))
	for index, item := range items {
		ids[index] = item.issueID
	}
	issueRows, err := tx.Query(ctx, issueJoin+`
		WHERE i.workspace_id=$1 AND i.id=ANY($2::TEXT[]) AND `+VisibleIssuePredicate("i", "$3"), workspaceID, ids, userID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*models.Issue, len(ids))
	for issueRows.Next() {
		issue, scanErr := scanIssue(issueRows)
		if scanErr != nil {
			issueRows.Close()
			return nil, scanErr
		}
		byID[issue.ID] = issue
	}
	if err = issueRows.Err(); err != nil {
		issueRows.Close()
		return nil, err
	}
	issueRows.Close()
	for _, id := range ids {
		if issue := byID[id]; issue != nil {
			page.Issues = append(page.Issues, issue)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return page, nil
}
