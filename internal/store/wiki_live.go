package store

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf16"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/wikimarkup"
	"github.com/jackc/pgx/v5"
)

// ErrWikiLiveStale refuses changes made against an older revision or session
// of a live document; the document that comes back carries what the editor
// missed.
var ErrWikiLiveStale = errors.New("the live document has changed since this revision")

// wikiLiveKeptChanges is how many recent changes a live document keeps for
// editors catching up; an editor further behind reloads the document.
const wikiLiveKeptChanges = 2000

// WikiLiveChange replaces Delete UTF-16 code units at Position with Insert,
// measured the way a browser measures a string.
type WikiLiveChange struct {
	Revision int64  `json:"revision,omitempty"`
	AuthorID string `json:"authorId,omitempty"`
	Position int    `json:"position"`
	Delete   int    `json:"delete"`
	Insert   string `json:"insert"`
}

// WikiLiveDocument is the shared text of a page everyone editing it works on.
// Body is set when the editor has to load the whole document; otherwise
// Changes are the changes after the revision the editor asked from.
type WikiLiveDocument struct {
	Session  string           `json:"session"`
	Revision int64            `json:"revision"`
	Version  int              `json:"version"`
	Title    string           `json:"title"`
	Body     *string          `json:"body,omitempty"`
	Changes  []WikiLiveChange `json:"changes"`
}

// lockWikiLiveDocument opens, or restarts, the live document of a published
// page the actor may edit and locks it for the rest of the transaction. A
// document left behind by a version published since it opened restarts from
// the page, so no one keeps editing text that is already out of date.
func (s *Store) lockWikiLiveDocument(ctx context.Context, tx pgx.Tx, ws, actor, pageID string) (liveDocumentRow, error) {
	var row liveDocumentRow
	allowed, err := s.CanUpdateWikiPage(ctx, ws, actor, pageID)
	if err != nil {
		return row, err
	}
	page, err := s.WikiPage(ctx, ws, actor, pageID)
	if err != nil {
		return row, err
	}
	if !allowed {
		return row, fmt.Errorf("%w: you cannot edit this page", ErrProjectPermission)
	}
	if page.Status != "current" {
		return row, fmt.Errorf("%w: live editing is for published pages", ErrWikiValidation)
	}
	title, body := page.Title, page.Body.Value
	if draft, draftErr := s.WikiContentDraft(ctx, ws, actor, "page", page.ID); draftErr == nil {
		title, body = draft.Title, draft.Body.Value
	} else if !errors.Is(draftErr, pgx.ErrNoRows) {
		return row, draftErr
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_live_documents(workspace_id,page_id,session_id,base_version,title,body)
		VALUES($1,$2::bigint,$3,$4,$5,$6) ON CONFLICT (page_id) DO NOTHING`, ws, page.ID, NewID("live"), page.Version.Number, title, body); err != nil {
		return row, err
	}
	if err = tx.QueryRow(ctx, `SELECT page_id::text,session_id,revision,base_version,title,body FROM wiki_live_documents WHERE page_id=$1::bigint FOR UPDATE`, page.ID).
		Scan(&row.pageID, &row.session, &row.revision, &row.version, &row.title, &row.body); err != nil {
		return row, err
	}
	if row.version != page.Version.Number {
		row.session, row.version, row.title, row.body, row.revision = NewID("live"), page.Version.Number, title, body, 0
		if _, err = tx.Exec(ctx, `DELETE FROM wiki_live_changes WHERE page_id=$1::bigint`, page.ID); err != nil {
			return row, err
		}
		if _, err = tx.Exec(ctx, `UPDATE wiki_live_documents SET session_id=$2,base_version=$3,title=$4,body=$5,revision=0,updated_at=now() WHERE page_id=$1::bigint`,
			page.ID, row.session, row.version, row.title, row.body); err != nil {
			return row, err
		}
	}
	return row, nil
}

type liveDocumentRow struct {
	pageID, session, title, body string
	revision                     int64
	version                      int
}

func (row liveDocumentRow) document() WikiLiveDocument {
	return WikiLiveDocument{Session: row.session, Revision: row.revision, Version: row.version, Title: row.title, Changes: []WikiLiveChange{}}
}

// wikiLiveCatchUp fills in what an editor at session and revision missed: the
// changes since, or the whole document when it is from another session or
// too far behind.
func wikiLiveCatchUp(ctx context.Context, tx pgx.Tx, row liveDocumentRow, session string, revision int64) (WikiLiveDocument, error) {
	document := row.document()
	if session != row.session || revision < 0 || revision > row.revision || row.revision-revision > wikiLiveKeptChanges {
		body := row.body
		document.Body = &body
		return document, nil
	}
	rows, err := tx.Query(ctx, `SELECT c.revision,c.author_id,c.position,c.delete_count,c.insert_text FROM wiki_live_changes c
		WHERE c.page_id=$1::bigint AND c.revision>$2 ORDER BY c.revision`, row.pageID, revision)
	if err != nil {
		return document, err
	}
	defer rows.Close()
	for rows.Next() {
		var change WikiLiveChange
		if err := rows.Scan(&change.Revision, &change.AuthorID, &change.Position, &change.Delete, &change.Insert); err != nil {
			return document, err
		}
		document.Changes = append(document.Changes, change)
	}
	if err := rows.Err(); err != nil {
		return document, err
	}
	// Pruned history cannot bring the editor up to date.
	if int64(len(document.Changes)) != row.revision-revision {
		body := row.body
		document.Body, document.Changes = &body, []WikiLiveChange{}
	}
	return document, nil
}

// WikiLiveDocument returns what an editor at session and revision needs to
// catch up with a page's live document, opening the document if it is not.
func (s *Store) WikiLiveDocument(ctx context.Context, ws, actor, pageID, session string, revision int64) (WikiLiveDocument, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return WikiLiveDocument{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.lockWikiLiveDocument(ctx, tx, ws, actor, pageID)
	if err != nil {
		return WikiLiveDocument{}, err
	}
	document, err := wikiLiveCatchUp(ctx, tx, row, session, revision)
	if err != nil {
		return document, err
	}
	return document, tx.Commit(ctx)
}

// applyWikiLiveChange splices one change into UTF-16 text, refusing one that
// reaches past the text or splits a character in two.
func applyWikiLiveChange(units []uint16, change WikiLiveChange) ([]uint16, error) {
	if change.Position < 0 || change.Delete < 0 || change.Position+change.Delete > len(units) {
		return nil, fmt.Errorf("%w: the change reaches past the document", ErrWikiValidation)
	}
	splits := func(at int) bool {
		return at > 0 && at < len(units) && utf16.IsSurrogate(rune(units[at-1])) && units[at-1] < 0xDC00 && units[at] >= 0xDC00 && units[at] <= 0xDFFF
	}
	if splits(change.Position) || splits(change.Position+change.Delete) {
		return nil, fmt.Errorf("%w: the change splits a character", ErrWikiValidation)
	}
	insert := utf16.Encode([]rune(change.Insert))
	out := make([]uint16, 0, len(units)-change.Delete+len(insert))
	out = append(out, units[:change.Position]...)
	out = append(out, insert...)
	return append(out, units[change.Position+change.Delete:]...), nil
}

// ApplyWikiLiveChanges applies changes an editor made at session and
// revision, in order, when that is the document's latest revision. Otherwise
// it applies nothing and returns ErrWikiLiveStale with what the editor
// missed. Whenever the merged text is valid storage it becomes the page's
// draft, so publishing and reopening the editor keep it.
func (s *Store) ApplyWikiLiveChanges(ctx context.Context, ws, actor, pageID, session string, revision int64, changes []WikiLiveChange) (WikiLiveDocument, error) {
	if len(changes) > 100 {
		return WikiLiveDocument{}, fmt.Errorf("%w: send at most 100 changes at a time", ErrWikiValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return WikiLiveDocument{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.lockWikiLiveDocument(ctx, tx, ws, actor, pageID)
	if err != nil {
		return WikiLiveDocument{}, err
	}
	if session != row.session || revision != row.revision {
		document, err := wikiLiveCatchUp(ctx, tx, row, session, revision)
		if err != nil {
			return document, err
		}
		if err := tx.Commit(ctx); err != nil {
			return document, err
		}
		return document, ErrWikiLiveStale
	}
	if len(changes) == 0 {
		return row.document(), tx.Commit(ctx)
	}
	units := utf16.Encode([]rune(row.body))
	document := row.document()
	for _, change := range changes {
		if units, err = applyWikiLiveChange(units, change); err != nil {
			return WikiLiveDocument{}, err
		}
		row.revision++
		if _, err = tx.Exec(ctx, `INSERT INTO wiki_live_changes(page_id,revision,author_id,position,delete_count,insert_text) VALUES($1::bigint,$2,$3,$4,$5,$6)`,
			row.pageID, row.revision, actor, change.Position, change.Delete, change.Insert); err != nil {
			return WikiLiveDocument{}, err
		}
		document.Changes = append(document.Changes, WikiLiveChange{Revision: row.revision, AuthorID: actor, Position: change.Position, Delete: change.Delete, Insert: change.Insert})
	}
	row.body = string(utf16.Decode(units))
	if len(row.body) > 1<<20 {
		return WikiLiveDocument{}, fmt.Errorf("%w: page body must be at most 1 MiB", ErrWikiValidation)
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_live_documents SET revision=$2,body=$3,updated_at=now() WHERE page_id=$1::bigint`, row.pageID, row.revision, row.body); err != nil {
		return WikiLiveDocument{}, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_live_changes WHERE page_id=$1::bigint AND revision <= $2`, row.pageID, row.revision-wikiLiveKeptChanges); err != nil {
		return WikiLiveDocument{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return WikiLiveDocument{}, err
	}
	document.Revision = row.revision
	if _, renderErr := wikimarkup.Render(row.body); renderErr == nil {
		if _, err := s.SaveWikiContentDraft(ctx, ws, actor, "page", row.pageID, row.title, models.WikiBody{Representation: "storage", Value: row.body}); err != nil {
			return document, err
		}
	}
	return document, nil
}

// CloseWikiLiveDocument ends a page's live editing session once its edits are
// published or discarded; editors still open reload the page's text.
func (s *Store) CloseWikiLiveDocument(ctx context.Context, ws, pageID string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM wiki_live_documents WHERE workspace_id=$1 AND page_id::text=$2`, ws, pageID)
	return err
}
