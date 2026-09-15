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

// WikiLiveSelection is where an editor's caret or selection is, in UTF-16
// code units of the text it sent.
type WikiLiveSelection struct {
	Position int `json:"position"`
	End      int `json:"end"`
}

// WikiLiveCursor is where someone else editing the live document is working.
type WikiLiveCursor struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Position    int    `json:"position"`
	End         int    `json:"end"`
}

// WikiLiveDocument is the shared text of a page or blog post everyone editing
// it works on.
// Body is set when the editor has to load the whole document; otherwise
// Changes are the changes after the revision the editor asked from.
type WikiLiveDocument struct {
	Session  string           `json:"session"`
	Revision int64            `json:"revision"`
	Version  int              `json:"version"`
	Title    string           `json:"title"`
	Body     *string          `json:"body,omitempty"`
	Changes  []WikiLiveChange `json:"changes"`
	// Cursors are where the others editing are, in the current revision.
	Cursors []WikiLiveCursor `json:"cursors"`
}

// lockWikiLiveDocument opens, or restarts, the live document of a published
// page or blog post the actor may edit and locks it for the rest of the
// transaction. A document left behind by a version published since it opened
// restarts from the content, so no one keeps editing text already out of date.
func (s *Store) lockWikiLiveDocument(ctx context.Context, tx pgx.Tx, ws, actor, kind, id string) (liveDocumentRow, error) {
	row := liveDocumentRow{kind: kind}
	var (
		allowed     bool
		err         error
		contentID   string
		status      string
		title, body string
		published   int
	)
	switch kind {
	case "page":
		if allowed, err = s.CanUpdateWikiPage(ctx, ws, actor, id); err != nil {
			return row, err
		}
		page, pageErr := s.WikiPage(ctx, ws, actor, id)
		if pageErr != nil {
			return row, pageErr
		}
		contentID, status, title, body, published = page.ID, page.Status, page.Title, page.Body.Value, page.Version.Number
	case "blogpost":
		if allowed, err = s.CanUpdateWikiBlogPost(ctx, ws, actor, id); err != nil {
			return row, err
		}
		post, postErr := s.WikiBlogPost(ctx, ws, actor, id)
		if postErr != nil {
			return row, postErr
		}
		contentID, status, title, body, published = post.ID, post.Status, post.Title, post.Body.Value, post.Version.Number
	default:
		return row, fmt.Errorf("%w: live editing is for pages and blog posts", ErrWikiValidation)
	}
	if !allowed {
		return row, fmt.Errorf("%w: you cannot edit this content", ErrProjectPermission)
	}
	if status != "current" {
		return row, fmt.Errorf("%w: live editing is for published content", ErrWikiValidation)
	}
	if draft, draftErr := s.WikiContentDraft(ctx, ws, actor, kind, contentID); draftErr == nil {
		title, body = draft.Title, draft.Body.Value
	} else if !errors.Is(draftErr, pgx.ErrNoRows) {
		return row, draftErr
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_live_documents(workspace_id,content_type,content_id,session_id,base_version,title,body)
		VALUES($1,$2,$3::bigint,$4,$5,$6,$7) ON CONFLICT (content_type,content_id) DO NOTHING`, ws, kind, contentID, NewID("live"), published, title, body); err != nil {
		return row, err
	}
	if err = tx.QueryRow(ctx, `SELECT content_id::text,session_id,revision,base_version,title,body FROM wiki_live_documents
		WHERE content_type=$1 AND content_id=$2::bigint FOR UPDATE`, kind, contentID).
		Scan(&row.contentID, &row.session, &row.revision, &row.version, &row.title, &row.body); err != nil {
		return row, err
	}
	if row.version != published {
		row.session, row.version, row.title, row.body, row.revision = NewID("live"), published, title, body, 0
		if _, err = tx.Exec(ctx, `DELETE FROM wiki_live_changes WHERE content_type=$1 AND content_id=$2::bigint`, kind, contentID); err != nil {
			return row, err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM wiki_live_cursors WHERE content_type=$1 AND content_id=$2::bigint`, kind, contentID); err != nil {
			return row, err
		}
		if _, err = tx.Exec(ctx, `UPDATE wiki_live_documents SET session_id=$3,base_version=$4,title=$5,body=$6,revision=0,updated_at=now()
			WHERE content_type=$1 AND content_id=$2::bigint`, kind, contentID, row.session, row.version, row.title, row.body); err != nil {
			return row, err
		}
	}
	return row, nil
}

type liveDocumentRow struct {
	kind, contentID, session, title, body string
	revision                              int64
	version                               int
}

func (row liveDocumentRow) document() WikiLiveDocument {
	return WikiLiveDocument{Session: row.session, Revision: row.revision, Version: row.version, Title: row.title, Changes: []WikiLiveChange{}, Cursors: []WikiLiveCursor{}}
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
		WHERE c.content_type=$1 AND c.content_id=$2::bigint AND c.revision>$3 ORDER BY c.revision`, row.kind, row.contentID, revision)
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
// catch up with the live document of a page or blog post, opening it if it
// is not.
func (s *Store) WikiLiveDocument(ctx context.Context, ws, actor, kind, id, session string, revision int64, selection *WikiLiveSelection) (WikiLiveDocument, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return WikiLiveDocument{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.lockWikiLiveDocument(ctx, tx, ws, actor, kind, id)
	if err != nil {
		return WikiLiveDocument{}, err
	}
	document, err := wikiLiveCatchUp(ctx, tx, row, session, revision)
	if err != nil {
		return document, err
	}
	// Only an editor holding the current text can say where it is in it.
	if session != row.session || revision != row.revision {
		selection = nil
	}
	if document.Cursors, err = keepWikiLiveCursor(ctx, tx, row, actor, selection); err != nil {
		return document, err
	}
	return document, tx.Commit(ctx)
}

// keepWikiLiveCursor records where the actor is in the live document's
// current text, when it says, and returns where everyone else who synced in
// the last half minute is.
func keepWikiLiveCursor(ctx context.Context, tx pgx.Tx, row liveDocumentRow, actor string, selection *WikiLiveSelection) ([]WikiLiveCursor, error) {
	if selection != nil {
		length := len(utf16.Encode([]rune(row.body)))
		start, end := min(max(selection.Position, 0), length), min(max(selection.End, 0), length)
		if end < start {
			start, end = end, start
		}
		if _, err := tx.Exec(ctx, `INSERT INTO wiki_live_cursors(content_type,content_id,user_id,position,selection_end) VALUES($1,$2::bigint,$3,$4,$5)
			ON CONFLICT (content_type,content_id,user_id) DO UPDATE SET position=EXCLUDED.position,selection_end=EXCLUDED.selection_end,updated_at=now()`,
			row.kind, row.contentID, actor, start, end); err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT c.user_id,u.display_name,c.position,c.selection_end FROM wiki_live_cursors c JOIN users u ON u.id=c.user_id
		WHERE c.content_type=$1 AND c.content_id=$2::bigint AND c.user_id<>$3 AND c.updated_at>now()-interval '30 seconds'
		ORDER BY u.display_name,c.user_id`, row.kind, row.contentID, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cursors := []WikiLiveCursor{}
	for rows.Next() {
		var cursor WikiLiveCursor
		if err := rows.Scan(&cursor.AccountID, &cursor.DisplayName, &cursor.Position, &cursor.End); err != nil {
			return nil, err
		}
		cursors = append(cursors, cursor)
	}
	return cursors, rows.Err()
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
// missed. Whenever the merged text is valid storage it becomes the content's
// draft, so publishing and reopening the editor keep it.
func (s *Store) ApplyWikiLiveChanges(ctx context.Context, ws, actor, kind, id, session string, revision int64, changes []WikiLiveChange, selection *WikiLiveSelection) (WikiLiveDocument, error) {
	if len(changes) > 100 {
		return WikiLiveDocument{}, fmt.Errorf("%w: send at most 100 changes at a time", ErrWikiValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return WikiLiveDocument{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row, err := s.lockWikiLiveDocument(ctx, tx, ws, actor, kind, id)
	if err != nil {
		return WikiLiveDocument{}, err
	}
	if session != row.session || revision != row.revision {
		document, err := wikiLiveCatchUp(ctx, tx, row, session, revision)
		if err != nil {
			return document, err
		}
		if document.Cursors, err = keepWikiLiveCursor(ctx, tx, row, actor, nil); err != nil {
			return document, err
		}
		if err := tx.Commit(ctx); err != nil {
			return document, err
		}
		return document, ErrWikiLiveStale
	}
	if len(changes) == 0 {
		document := row.document()
		if document.Cursors, err = keepWikiLiveCursor(ctx, tx, row, actor, selection); err != nil {
			return document, err
		}
		return document, tx.Commit(ctx)
	}
	units := utf16.Encode([]rune(row.body))
	document := row.document()
	for _, change := range changes {
		if units, err = applyWikiLiveChange(units, change); err != nil {
			return WikiLiveDocument{}, err
		}
		row.revision++
		if _, err = tx.Exec(ctx, `INSERT INTO wiki_live_changes(content_type,content_id,revision,author_id,position,delete_count,insert_text) VALUES($1,$2::bigint,$3,$4,$5,$6,$7)`,
			row.kind, row.contentID, row.revision, actor, change.Position, change.Delete, change.Insert); err != nil {
			return WikiLiveDocument{}, err
		}
		// Everyone's caret moves with the change: one before it stays, one
		// after it moves by its length, and one inside what it deleted lands
		// after what it inserted.
		inserted := len(utf16.Encode([]rune(change.Insert)))
		if _, err = tx.Exec(ctx, `UPDATE wiki_live_cursors SET
			position=CASE WHEN position<=$3::int THEN position WHEN position>=$3::int+$4::int THEN position-$4::int+$5::int ELSE $3::int+$5::int END,
			selection_end=CASE WHEN selection_end<=$3::int THEN selection_end WHEN selection_end>=$3::int+$4::int THEN selection_end-$4::int+$5::int ELSE $3::int+$5::int END
			WHERE content_type=$1 AND content_id=$2::bigint`, row.kind, row.contentID, change.Position, change.Delete, inserted); err != nil {
			return WikiLiveDocument{}, err
		}
		document.Changes = append(document.Changes, WikiLiveChange{Revision: row.revision, AuthorID: actor, Position: change.Position, Delete: change.Delete, Insert: change.Insert})
	}
	row.body = string(utf16.Decode(units))
	if len(row.body) > 1<<20 {
		return WikiLiveDocument{}, fmt.Errorf("%w: page body must be at most 1 MiB", ErrWikiValidation)
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_live_documents SET revision=$3,body=$4,updated_at=now() WHERE content_type=$1 AND content_id=$2::bigint`, row.kind, row.contentID, row.revision, row.body); err != nil {
		return WikiLiveDocument{}, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_live_changes WHERE content_type=$1 AND content_id=$2::bigint AND revision <= $3`, row.kind, row.contentID, row.revision-wikiLiveKeptChanges); err != nil {
		return WikiLiveDocument{}, err
	}
	if document.Cursors, err = keepWikiLiveCursor(ctx, tx, row, actor, selection); err != nil {
		return WikiLiveDocument{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return WikiLiveDocument{}, err
	}
	document.Revision = row.revision
	if _, renderErr := wikimarkup.Render(row.body); renderErr == nil {
		if _, err := s.SaveWikiContentDraft(ctx, ws, actor, row.kind, row.contentID, row.title, models.WikiBody{Representation: "storage", Value: row.body}); err != nil {
			return document, err
		}
	}
	return document, nil
}

// CloseWikiLiveDocument ends the live editing session of a page or blog post
// once its edits are discarded; editors still open reload the content's text.
func (s *Store) CloseWikiLiveDocument(ctx context.Context, ws, kind, id string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM wiki_live_documents WHERE workspace_id=$1 AND content_type=$2 AND content_id::text=$3`, ws, kind, id)
	return err
}
