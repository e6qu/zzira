package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// Confluence relocates and duplicates whole page trees. Moving and copying one
// page answer immediately; copying a hierarchy, archiving and trashing a tree
// can touch a great many pages, so they are queued and watched through the
// long task reads, as Confluence does.

var ErrWikiMoveValidation = errors.New("invalid page move")

const (
	apiTaskWikiCopyHierarchy = "wiki-copy-hierarchy"
	apiTaskWikiArchivePages  = "wiki-archive-pages"
	apiTaskWikiTrashPageTree = "wiki-trash-page-tree"
)

// WikiCopyHierarchyRequest is what a queued hierarchy copy was asked to do.
type WikiCopyHierarchyRequest struct {
	PageID            string `json:"pageId"`
	DestinationPageID string `json:"destinationPageId"`
	CopyAttachments   bool   `json:"copyAttachments"`
	CopyProperties    bool   `json:"copyProperties"`
	CopyLabels        bool   `json:"copyLabels"`
	CopyDescendants   bool   `json:"copyDescendants"`
	TitlePrefix       string `json:"titlePrefix"`
	TitleSearch       string `json:"titleSearch"`
	TitleReplace      string `json:"titleReplace"`
}

type wikiPageIDsPayload struct {
	PageIDs            []string `json:"pageIds"`
	IncludeDescendants bool     `json:"includeDescendants,omitempty"`
}

// MoveWikiPage places a page before or after a sibling, or under a new parent.
// Confluence's four positions are the whole vocabulary for this, so an unknown
// one is refused rather than guessed at. The target may be in another space,
// and then the page takes everything beneath it along.
func (s *Store) MoveWikiPage(ctx context.Context, ws, actor, pageID, position, targetID string) (string, error) {
	switch position {
	case "before", "after", "append", "above":
	default:
		return "", fmt.Errorf("%w: position must be before, after, append or above", ErrWikiMoveValidation)
	}
	if pageID == targetID {
		return "", fmt.Errorf("%w: a page cannot be moved relative to itself", ErrWikiMoveValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	page, err := lockWritablePage(ctx, tx, ws, actor, pageID, "current")
	if err != nil {
		return "", err
	}
	target, err := lockWritablePage(ctx, tx, ws, actor, targetID, "current")
	if err != nil {
		return "", err
	}
	var pageSpace, targetSpace string
	var targetParent *string
	var targetPosition int
	if err = tx.QueryRow(ctx, `SELECT space_id::text FROM wiki_pages WHERE id::text=$1`, page.ID).Scan(&pageSpace); err != nil {
		return "", err
	}
	if err = tx.QueryRow(ctx, `SELECT space_id::text,parent_id::text,position FROM wiki_pages WHERE id::text=$1`,
		target.ID).Scan(&targetSpace, &targetParent, &targetPosition); err != nil {
		return "", err
	}
	// A page cannot be moved inside its own subtree: the tree would have no
	// root and the page would disappear from the space.
	descendant, err := pageIsDescendant(ctx, tx, target.ID, page.ID)
	if err != nil {
		return "", err
	}
	if descendant {
		return "", fmt.Errorf("%w: a page cannot be moved beneath itself", ErrWikiMoveValidation)
	}
	moved := []string{page.ID}
	if pageSpace != targetSpace {
		if moved, err = relocatePageTree(ctx, tx, ws, actor, page.ID, pageSpace, targetSpace); err != nil {
			return "", err
		}
	}
	var newParent any
	newPosition := 0
	switch position {
	case "append", "above":
		// `append` makes the target the parent; `above` is Confluence's name
		// for the same relocation with the page first among the children.
		newParent = target.ID
		if position == "append" {
			if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM wiki_pages
				WHERE parent_id::text=$1 AND status='current' AND id::text<>$2`, target.ID, page.ID).Scan(&newPosition); err != nil {
				return "", err
			}
		} else {
			newPosition = 1
			if _, err = tx.Exec(ctx, `UPDATE wiki_pages SET position=position+1
				WHERE parent_id::text=$1 AND status='current' AND id::text<>$2`, target.ID, page.ID); err != nil {
				return "", err
			}
		}
	default:
		if targetParent != nil {
			newParent = *targetParent
		}
		newPosition = targetPosition
		if position == "after" {
			newPosition = targetPosition + 1
		}
		if _, err = tx.Exec(ctx, `UPDATE wiki_pages SET position=position+1
			WHERE space_id::text=$1 AND parent_id IS NOT DISTINCT FROM $2::bigint
			AND status='current' AND position>=$3 AND id::text<>$4`,
			targetSpace, targetParent, newPosition, page.ID); err != nil {
			return "", err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_pages SET parent_id=$2::bigint,position=$3 WHERE id::text=$1`,
		page.ID, newParent, newPosition); err != nil {
		return "", err
	}
	for _, id := range moved {
		if err = wikiPageSnapshotAction(ctx, tx, ws, actor, id); err != nil {
			return "", err
		}
	}
	return page.ID, tx.Commit(ctx)
}

// relocatePageTree carries a page and everything beneath it into another
// space, with the folders, whiteboards, databases and Smart Links that hang off
// those pages. Confluence asks for permission to delete pages where the tree is
// and to add pages where it is going; it will not take a space's homepage out
// of its space, and a destination already showing one of the titles refuses
// the move rather than holding two current pages of the same name.
func relocatePageTree(ctx context.Context, tx pgx.Tx, ws, actor, pageID, fromSpace, toSpace string) ([]string, error) {
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM wiki_spaces s WHERE s.workspace_id=$1 AND s.id::text=$3 AND `+wikiSpaceCanDeletePage+`)
		AND EXISTS(SELECT 1 FROM wiki_spaces s WHERE s.workspace_id=$1 AND s.id::text=$4 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanCreatePage+`)`,
		ws, actor, fromSpace, toSpace).Scan(&allowed); err != nil {
		return nil, err
	}
	if !allowed {
		return nil, fmt.Errorf("%w: moving a page to another space needs permission to delete pages here and to add pages there", ErrProjectPermission)
	}
	var homepage bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_spaces WHERE id::text=$1 AND homepage_id::text=$2)`,
		fromSpace, pageID).Scan(&homepage); err != nil {
		return nil, err
	}
	if homepage {
		return nil, fmt.Errorf("%w: a space homepage cannot be moved to another space", ErrWikiMoveValidation)
	}
	ids, err := queryPageIDs(ctx, tx, `WITH RECURSIVE tree AS (
			SELECT id FROM wiki_pages WHERE id::text=$1
			UNION
			SELECT p.id FROM wiki_pages p JOIN tree ON p.parent_id=tree.id
		)
		SELECT id::text FROM tree`, pageID)
	if err != nil {
		return nil, err
	}
	var clash string
	err = tx.QueryRow(ctx, `SELECT moving.title FROM wiki_pages moving
		JOIN wiki_pages there ON there.title=moving.title AND there.space_id::text=$2 AND there.status='current'
		WHERE moving.id::text=ANY($1) AND moving.status='current' ORDER BY moving.id LIMIT 1`, ids, toSpace).Scan(&clash)
	if err == nil {
		return nil, fmt.Errorf("%w: a page titled %q already exists in the destination space", ErrWikiMoveValidation, clash)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_pages SET space_id=$2::bigint WHERE id::text=ANY($1)`, ids, toSpace); err != nil {
		return nil, err
	}
	contentIDs, err := queryPageIDs(ctx, tx, `WITH RECURSIVE owned AS (
			SELECT id FROM wiki_content WHERE parent_page_id::text=ANY($1) OR root_page_id::text=ANY($1)
			UNION
			SELECT c.id FROM wiki_content c JOIN owned ON c.parent_content_id=owned.id
		)
		SELECT id::text FROM owned ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_content SET space_id=$2::bigint WHERE id::text=ANY($1)`, contentIDs, toSpace); err != nil {
		return nil, err
	}
	for _, id := range contentIDs {
		content, scanErr := scanWikiContent(tx.QueryRow(ctx, wikiContentSelect+` WHERE c.id::text=$1`, id))
		if scanErr != nil {
			return nil, scanErr
		}
		if err = wikiContentAction(ctx, tx, ws, actor, content, models.OpUpsert); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func queryPageIDs(ctx context.Context, tx pgx.Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func pageIsDescendant(ctx context.Context, tx pgx.Tx, candidateID, ancestorID string) (bool, error) {
	var descendant bool
	err := tx.QueryRow(ctx, `
		WITH RECURSIVE tree AS (
			SELECT id, parent_id FROM wiki_pages WHERE id::text=$1
			UNION ALL
			SELECT p.id, p.parent_id FROM wiki_pages p JOIN tree ON p.id=tree.parent_id
		)
		SELECT EXISTS(SELECT 1 FROM tree WHERE id::text=$2)`, candidateID, ancestorID).Scan(&descendant)
	return descendant, err
}

// wikiPageSnapshotAction records a page as it now is. Replicas apply a page
// whole and receive it only when its space and publication say they may, so
// the action carries the page rather than a description of what happened.
func wikiPageSnapshotAction(ctx context.Context, tx pgx.Tx, ws, actor, pageID string) error {
	page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE p.id::text=$1`, pageID))
	if err != nil {
		return err
	}
	return wikiAction(ctx, tx, ws, actor, "wiki_page", page.ID, page.SpaceID, page)
}

// WikiPageCopyOptions says what travels with a copy.
type WikiPageCopyOptions struct {
	Title           string
	DestinationType string
	DestinationID   string
	Body            string
	CopyAttachments bool
	CopyProperties  bool
	CopyLabels      bool
}

// CopyWikiPage duplicates one page. What travels with it is the caller's
// choice, because a copy made to start a new document wants the text and not
// last quarter's attachments.
func (s *Store) CopyWikiPage(ctx context.Context, ws, actor, pageID string, options WikiPageCopyOptions) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	source, spaceID, parentID, err := readablePageForCopy(ctx, tx, ws, actor, pageID)
	if err != nil {
		return "", err
	}
	targetSpace, targetParent, err := resolveCopyDestination(ctx, tx, ws, actor, options, spaceID, parentID)
	if err != nil {
		return "", err
	}
	title := strings.TrimSpace(options.Title)
	if title == "" {
		title = source.Title
	}
	body := source.Body
	if options.Body != "" {
		body = options.Body
	}
	copyID, err := insertCopiedPage(ctx, tx, actor, targetSpace, targetParent, title, body)
	if err != nil {
		return "", err
	}
	if err = copyPageBelongings(ctx, tx, source.ID, copyID, options.CopyAttachments, options.CopyProperties, options.CopyLabels); err != nil {
		return "", err
	}
	if err = wikiPageSnapshotAction(ctx, tx, ws, actor, copyID); err != nil {
		return "", err
	}
	return copyID, tx.Commit(ctx)
}

func readablePageForCopy(ctx context.Context, tx pgx.Tx, ws, actor, pageID string) (lockedPage, string, *string, error) {
	var page lockedPage
	var spaceID string
	var parentID *string
	err := tx.QueryRow(ctx, `SELECT p.id::text,p.title,p.body,p.version,p.status,p.space_id::text,p.parent_id::text
		FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
		WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+`
		AND p.id::text=$3 AND p.status='current'`, ws, actor, pageID).
		Scan(&page.ID, &page.Title, &page.Body, &page.Version, &page.Status, &spaceID, &parentID)
	return page, spaceID, parentID, err
}

// resolveCopyDestination reads Confluence's destination object. Copying into a
// space puts the page at the top level there; copying onto a parent puts it
// beneath that page; and an existing page is the target to overwrite by name.
func resolveCopyDestination(ctx context.Context, tx pgx.Tx, ws, actor string, options WikiPageCopyOptions, sourceSpace string, sourceParent *string) (string, any, error) {
	switch options.DestinationType {
	case "", "existing_page":
		if options.DestinationID == "" {
			var parent any
			if sourceParent != nil {
				parent = *sourceParent
			}
			return sourceSpace, parent, nil
		}
		fallthrough
	case "parent_page":
		var parentID, parentSpace string
		if err := tx.QueryRow(ctx, `SELECT p.id::text,p.space_id::text FROM wiki_pages p
			JOIN wiki_spaces s ON s.id=p.space_id
			WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+`
			AND `+wikiSpacePermissionAllowed("create/page")+`
			AND p.id::text=$3 AND p.status='current'`, ws, actor, options.DestinationID).Scan(&parentID, &parentSpace); err != nil {
			return "", nil, fmt.Errorf("%w: the destination page does not exist", ErrWikiMoveValidation)
		}
		return parentSpace, parentID, nil
	case "space_key", "space":
		var spaceID string
		if err := tx.QueryRow(ctx, `SELECT s.id::text FROM wiki_spaces s
			WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpacePermissionAllowed("create/page")+`
			AND (s.key=$3 OR s.id::text=$3)`, ws, actor, options.DestinationID).Scan(&spaceID); err != nil {
			return "", nil, fmt.Errorf("%w: the destination space does not exist", ErrWikiMoveValidation)
		}
		return spaceID, nil, nil
	default:
		return "", nil, fmt.Errorf("%w: the destination type must be space_key, parent_page or existing_page", ErrWikiMoveValidation)
	}
}

func insertCopiedPage(ctx context.Context, tx pgx.Tx, actor, spaceID string, parentID any, title, body string) (string, error) {
	// A space shows one current page per title, so a copy landing beside its
	// source takes a distinguishable name rather than failing.
	unique, err := availablePageTitle(ctx, tx, spaceID, title)
	if err != nil {
		return "", err
	}
	var position int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM wiki_pages
		WHERE space_id::text=$1 AND parent_id IS NOT DISTINCT FROM $2::bigint AND status='current'`,
		spaceID, parentID).Scan(&position); err != nil {
		return "", err
	}
	var copyID string
	if err = tx.QueryRow(ctx, `INSERT INTO wiki_pages(space_id,parent_id,title,status,body,author_id,version,published,position)
		VALUES($1::bigint,$2::bigint,$3,'current',$4,$5,1,TRUE,$6) RETURNING id::text`,
		spaceID, parentID, unique, body, actor, position).Scan(&copyID); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_page_versions(page_id,version,title,body,status,author_id,message)
		VALUES($1::bigint,1,$2,$3,'current',$4,'Copied')`, copyID, unique, body, actor); err != nil {
		return "", err
	}
	return copyID, nil
}

func availablePageTitle(ctx context.Context, tx pgx.Tx, spaceID, title string) (string, error) {
	candidate := title
	for attempt := 2; attempt < 200; attempt++ {
		var taken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_pages
			WHERE space_id::text=$1 AND title=$2 AND status='current')`, spaceID, candidate).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s (%d)", title, attempt)
	}
	return "", fmt.Errorf("%w: too many pages share this title", ErrWikiMoveValidation)
}

func copyPageBelongings(ctx context.Context, tx pgx.Tx, sourceID, copyID string, attachments, properties, labels bool) error {
	if labels {
		if _, err := tx.Exec(ctx, `INSERT INTO wiki_page_labels(page_id,label_id)
			SELECT $2::bigint,label_id FROM wiki_page_labels WHERE page_id::text=$1
			ON CONFLICT DO NOTHING`, sourceID, copyID); err != nil {
			return err
		}
	}
	if properties {
		if _, err := tx.Exec(ctx, `INSERT INTO wiki_page_properties(page_id,key,value,version,author_id)
			SELECT $2::bigint,key,value,1,author_id FROM wiki_page_properties WHERE page_id::text=$1`,
			sourceID, copyID); err != nil {
			return err
		}
	}
	if attachments {
		// The copy points at the same stored blob; an attachment's bytes are
		// content-addressed, so duplicating the row is enough.
		if _, err := tx.Exec(ctx, `INSERT INTO wiki_attachments(page_id,file_id,filename,media_type,comment,size,version,status,author_id)
			SELECT $2::bigint,file_id,filename,media_type,comment,size,1,status,author_id
			FROM wiki_attachments WHERE page_id::text=$1 AND status='current'`, sourceID, copyID); err != nil {
			return err
		}
	}
	return nil
}

// EnqueueWikiCopyHierarchy queues a hierarchy copy, which Confluence runs in
// the background because a tree can be large.
func (s *Store) EnqueueWikiCopyHierarchy(ctx context.Context, ws, actor string, payload WikiCopyHierarchyRequest) (APITask, error) {
	task, err := queuedAPITask(ws, actor, "Copy page hierarchy", apiTaskWikiCopyHierarchy, payload)
	if err != nil {
		return APITask{}, err
	}
	if err := s.enqueueAPITask(ctx, &task); err != nil {
		return APITask{}, err
	}
	return task, nil
}

// EnqueueWikiArchivePages and EnqueueWikiTrashPageTree are the other two
// background page operations.
func (s *Store) EnqueueWikiArchivePages(ctx context.Context, ws, actor string, pageIDs []string) (APITask, error) {
	task, err := queuedAPITask(ws, actor, "Archive pages", apiTaskWikiArchivePages, wikiPageIDsPayload{PageIDs: pageIDs})
	if err != nil {
		return APITask{}, err
	}
	if err := s.enqueueAPITask(ctx, &task); err != nil {
		return APITask{}, err
	}
	return task, nil
}

func (s *Store) EnqueueWikiTrashPageTree(ctx context.Context, ws, actor, pageID string) (APITask, error) {
	task, err := queuedAPITask(ws, actor, "Trash page tree", apiTaskWikiTrashPageTree, wikiPageIDsPayload{PageIDs: []string{pageID}})
	if err != nil {
		return APITask{}, err
	}
	if err := s.enqueueAPITask(ctx, &task); err != nil {
		return APITask{}, err
	}
	return task, nil
}

func (s *Store) executeWikiCopyHierarchy(ctx context.Context, task APITask) error {
	var payload WikiCopyHierarchyRequest
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode page hierarchy copy: %w", err)
	}
	copied, err := s.copyHierarchy(ctx, task.WorkspaceID, task.SubmittedBy, payload)
	if err != nil {
		return err
	}
	return s.CompleteAPITask(ctx, task, "Copied the page hierarchy.", map[string]any{"copiedPages": copied})
}

func (s *Store) copyHierarchy(ctx context.Context, ws, actor string, payload WikiCopyHierarchyRequest) (int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	source, _, _, err := readablePageForCopy(ctx, tx, ws, actor, payload.PageID)
	if err != nil {
		return 0, err
	}
	targetSpace, targetParent, err := resolveCopyDestination(ctx, tx, ws, actor,
		WikiPageCopyOptions{DestinationType: "parent_page", DestinationID: payload.DestinationPageID}, "", nil)
	if err != nil {
		return 0, err
	}
	descendant, err := pageIsDescendant(ctx, tx, payload.DestinationPageID, source.ID)
	if err != nil {
		return 0, err
	}
	if descendant {
		return 0, fmt.Errorf("%w: a hierarchy cannot be copied into itself", ErrWikiMoveValidation)
	}
	copied, err := copySubtree(ctx, tx, ws, actor, source.ID, targetSpace, targetParent, payload, true)
	if err != nil {
		return 0, err
	}
	return copied, tx.Commit(ctx)
}

// copySubtree copies one page and, when asked, everything beneath it. The
// recursion follows the tree rather than a flat list so a child lands under its
// own copied parent instead of the destination.
func copySubtree(ctx context.Context, tx pgx.Tx, ws, actor, pageID, spaceID string, parentID any, payload WikiCopyHierarchyRequest, root bool) (int, error) {
	var title, body string
	if err := tx.QueryRow(ctx, `SELECT title,body FROM wiki_pages WHERE id::text=$1 AND status='current'`,
		pageID).Scan(&title, &body); err != nil {
		return 0, err
	}
	copyID, err := insertCopiedPage(ctx, tx, actor, spaceID, parentID, applyTitleOptions(title, payload, root), body)
	if err != nil {
		return 0, err
	}
	if err = copyPageBelongings(ctx, tx, pageID, copyID, payload.CopyAttachments, payload.CopyProperties, payload.CopyLabels); err != nil {
		return 0, err
	}
	if err = wikiPageSnapshotAction(ctx, tx, ws, actor, copyID); err != nil {
		return 0, err
	}
	copied := 1
	if !payload.CopyDescendants {
		return copied, nil
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM wiki_pages
		WHERE parent_id::text=$1 AND status='current' ORDER BY position,id`, pageID)
	if err != nil {
		return 0, err
	}
	children := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		children = append(children, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for _, child := range children {
		childCount, childErr := copySubtree(ctx, tx, ws, actor, child, spaceID, copyID, payload, false)
		if childErr != nil {
			return 0, childErr
		}
		copied += childCount
	}
	return copied, nil
}

// applyTitleOptions renames a copy the way Confluence's titleOptions ask. They
// apply to every page in the tree, which is what makes a copied hierarchy
// distinguishable from the original.
func applyTitleOptions(title string, payload WikiCopyHierarchyRequest, root bool) string {
	if payload.TitleSearch != "" {
		title = strings.ReplaceAll(title, payload.TitleSearch, payload.TitleReplace)
	}
	if payload.TitlePrefix != "" {
		title = payload.TitlePrefix + title
	}
	_ = root
	return title
}

func (s *Store) executeWikiArchivePages(ctx context.Context, task APITask) error {
	var payload wikiPageIDsPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode page archive: %w", err)
	}
	archived, err := s.ArchiveWikiPages(ctx, task.WorkspaceID, task.SubmittedBy, payload.PageIDs, payload.IncludeDescendants)
	if err != nil {
		return err
	}
	return s.CompleteAPITask(ctx, task, "Archived the pages.", map[string]any{"archivedPages": archived})
}

func (s *Store) executeWikiTrashPageTree(ctx context.Context, task APITask) error {
	var payload wikiPageIDsPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		return fmt.Errorf("decode page tree trash: %w", err)
	}
	trashed := 0
	for _, pageID := range payload.PageIDs {
		changed, err := s.changePageStatus(ctx, task.WorkspaceID, task.SubmittedBy, pageID, "current", "trashed", true)
		if err != nil {
			return err
		}
		trashed += changed
	}
	return s.CompleteAPITask(ctx, task, "Trashed the page tree.", map[string]any{"trashedPages": trashed})
}

// ArchiveWikiPages archives pages straight away, which is what a page's own
// Archive action does; the REST operation queues the same work. Each page is
// its own change, so one that cannot be archived leaves the earlier ones
// archived, as Confluence's bulk archive does.
func (s *Store) ArchiveWikiPages(ctx context.Context, ws, actor string, pageIDs []string, withDescendants bool) (int, error) {
	archived := 0
	for _, pageID := range pageIDs {
		changed, err := s.changePageStatus(ctx, ws, actor, pageID, "current", "archived", withDescendants)
		if err != nil {
			return archived, err
		}
		archived += changed
	}
	return archived, nil
}

// RestoreWikiPage brings an archived page, and optionally the archived pages
// beneath it, back into the space's current content.
func (s *Store) RestoreWikiPage(ctx context.Context, ws, actor, pageID string, withDescendants bool) (int, error) {
	return s.changePageStatus(ctx, ws, actor, pageID, "archived", "current", withDescendants)
}

// changePageStatus moves a page, and optionally the pages beneath it that share
// its status, to another status.
//
// Archiving a page without its children lifts them a level so they stay in the
// page tree, as Confluence does. Restoring puts a page back under its parent
// when that parent is still current and at the top of the space otherwise, and
// refuses when the space already shows a current page with one of the titles.
func (s *Store) changePageStatus(ctx context.Context, ws, actor, pageID, from, to string, withDescendants bool) (int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = lockWritablePage(ctx, tx, ws, actor, pageID, from); err != nil {
		return 0, err
	}
	ids := []string{pageID}
	if withDescendants {
		if ids, err = queryPageIDs(ctx, tx, `
			WITH RECURSIVE tree AS (
				SELECT id FROM wiki_pages WHERE id::text=$1
				UNION
				SELECT p.id FROM wiki_pages p JOIN tree ON p.parent_id=tree.id WHERE p.status=$2
			)
			SELECT id::text FROM tree ORDER BY id`, pageID, from); err != nil {
			return 0, err
		}
	}
	touched := append([]string{}, ids...)
	switch {
	case to == "archived" && !withDescendants:
		lifted, liftErr := queryPageIDs(ctx, tx, `UPDATE wiki_pages child SET parent_id=archived.parent_id
			FROM wiki_pages archived
			WHERE archived.id::text=$1 AND child.parent_id=archived.id AND child.status='current'
			RETURNING child.id::text`, pageID)
		if liftErr != nil {
			return 0, liftErr
		}
		touched = append(touched, lifted...)
	case to == "current":
		var clash string
		err = tx.QueryRow(ctx, `SELECT restoring.title FROM wiki_pages restoring
			JOIN wiki_pages there ON there.space_id=restoring.space_id AND there.title=restoring.title AND there.status='current'
			WHERE restoring.id::text=ANY($1) ORDER BY restoring.id LIMIT 1`, ids).Scan(&clash)
		if err == nil {
			return 0, fmt.Errorf("%w: a current page titled %q already exists in this space", ErrWikiMoveValidation, clash)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
		if _, err = tx.Exec(ctx, `UPDATE wiki_pages p SET parent_id=NULL
			WHERE p.id::text=$1 AND p.parent_id IS NOT NULL AND NOT EXISTS(
				SELECT 1 FROM wiki_pages parent WHERE parent.id=p.parent_id AND parent.status='current')`, pageID); err != nil {
			return 0, err
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE wiki_pages SET status=$2 WHERE id::text = ANY($1) AND status=$3`, ids, to, from)
	if err != nil {
		return 0, err
	}
	for _, id := range touched {
		if err = wikiPageSnapshotAction(ctx, tx, ws, actor, id); err != nil {
			return 0, err
		}
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}

// WikiLongTasks reports the background page operations, which is what
// Confluence's long task reads answer.
func (s *Store) WikiLongTasks(ctx context.Context, ws, actor, taskID string) ([]APITask, error) {
	member, err := s.IsMember(ctx, ws, actor)
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrProjectPermission
	}
	query := `SELECT ` + prefixedAPITaskColumns("task") + ` FROM api_tasks task
		WHERE task.workspace_id=$1 AND task.kind = ANY($2)`
	// Deleting a space answers with a long task too, so it has to be one the
	// long task reads will report.
	args := []any{ws, []string{apiTaskWikiCopyHierarchy, apiTaskWikiArchivePages, apiTaskWikiTrashPageTree, apiTaskWikiDeleteSpace}}
	if taskID != "" {
		query += ` AND (task.id=$3 OR task.jira_id::text=$3)`
		args = append(args, taskID)
	}
	query += ` ORDER BY task.submitted_at DESC, task.id DESC`
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []APITask{}
	for rows.Next() {
		task, scanErr := scanAPITask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		tasks = append(tasks, task)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if taskID != "" && len(tasks) == 0 {
		return nil, pgx.ErrNoRows
	}
	return tasks, nil
}
