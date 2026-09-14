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
	CopyPermissions   bool   `json:"copyPermissions"`
	CopyCustomContent bool   `json:"copyCustomContents"`
	CopyDescendants   bool   `json:"copyDescendants"`
	TitlePrefix       string `json:"titlePrefix"`
	TitleSearch       string `json:"titleSearch"`
	TitleReplace      string `json:"titleReplace"`
}

type wikiPageIDsPayload struct {
	PageIDs            []string `json:"pageIds"`
	IncludeDescendants bool     `json:"includeDescendants,omitempty"`
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

// MoveWikiPage is Confluence's page move. The page may go beside or beneath any
// node of the content tree, in this space or another.
func (s *Store) MoveWikiPage(ctx context.Context, ws, actor, pageID, position, targetID string) (string, error) {
	if err := s.requirePageNode(ctx, ws, pageID); err != nil {
		return "", err
	}
	return s.MoveWikiTreeNode(ctx, ws, actor, pageID, position, targetID)
}

func (s *Store) requirePageNode(ctx context.Context, ws, id string) error {
	nodeType, err := s.WikiTreeNodeType(ctx, ws, id)
	if err != nil {
		return err
	}
	if nodeType != "page" {
		return pgx.ErrNoRows
	}
	return nil
}

// ArchiveWikiPages archives pages straight away, which is what a page's own
// Archive action does; the REST operation queues the same work. Each page is
// its own change, so one that cannot be archived leaves the earlier ones
// archived, as Confluence's bulk archive does.
func (s *Store) ArchiveWikiPages(ctx context.Context, ws, actor string, pageIDs []string, withDescendants bool) (int, error) {
	archived := 0
	for _, pageID := range pageIDs {
		if err := s.requirePageNode(ctx, ws, pageID); err != nil {
			return archived, err
		}
		changed, err := s.changeTreeStatus(ctx, ws, actor, pageID, "current", "archived", withDescendants)
		if err != nil {
			return archived, err
		}
		archived += changed
	}
	return archived, nil
}

// RestoreWikiPage brings an archived page, and optionally the archived content
// beneath it, back into the space's current content.
func (s *Store) RestoreWikiPage(ctx context.Context, ws, actor, pageID string, withDescendants bool) (int, error) {
	if err := s.requirePageNode(ctx, ws, pageID); err != nil {
		return 0, err
	}
	return s.changeTreeStatus(ctx, ws, actor, pageID, "archived", "current", withDescendants)
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
	// CopyPermissions carries the page's view and edit restrictions, and
	// CopyCustomContent the custom content filed directly under it.
	CopyPermissions   bool
	CopyCustomContent bool
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
	if err = copyPageBelongings(ctx, tx, ws, actor, source.ID, copyID, pageBelongings{
		Attachments: options.CopyAttachments, Properties: options.CopyProperties, Labels: options.CopyLabels,
		Permissions: options.CopyPermissions, CustomContent: options.CopyCustomContent,
	}); err != nil {
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
	parentText, _ := parentID.(string)
	position, err := nextTreePosition(ctx, tx, spaceID, parentText)
	if err != nil {
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

type pageBelongings struct {
	Attachments, Properties, Labels, Permissions, CustomContent bool
}

func copyPageBelongings(ctx context.Context, tx pgx.Tx, ws, actor, sourceID, copyID string, what pageBelongings) error {
	if what.Labels {
		if _, err := tx.Exec(ctx, `INSERT INTO wiki_page_labels(page_id,label_id)
			SELECT $2::bigint,label_id FROM wiki_page_labels WHERE page_id::text=$1
			ON CONFLICT DO NOTHING`, sourceID, copyID); err != nil {
			return err
		}
	}
	if what.Properties {
		if _, err := tx.Exec(ctx, `INSERT INTO wiki_page_properties(page_id,key,value,version,author_id)
			SELECT $2::bigint,key,value,1,author_id FROM wiki_page_properties WHERE page_id::text=$1`,
			sourceID, copyID); err != nil {
			return err
		}
	}
	if what.Attachments {
		// The copy points at the same stored blob; an attachment's bytes are
		// content-addressed, so duplicating the row is enough.
		if _, err := tx.Exec(ctx, `INSERT INTO wiki_attachments(page_id,file_id,filename,media_type,comment,size,version,status,author_id)
			SELECT $2::bigint,file_id,filename,media_type,comment,size,1,status,author_id
			FROM wiki_attachments WHERE page_id::text=$1 AND status='current'`, sourceID, copyID); err != nil {
			return err
		}
	}
	if what.Permissions {
		// Restrictions travel as they are, so a page only some people could
		// see is not copied into one everybody can.
		if _, err := tx.Exec(ctx, `INSERT INTO wiki_page_restrictions(page_id,operation,subject_type,subject_id,author_id)
			SELECT $2::bigint,operation,subject_type,subject_id,$3 FROM wiki_page_restrictions WHERE page_id::text=$1
			ON CONFLICT DO NOTHING`, sourceID, copyID, actor); err != nil {
			return err
		}
	}
	if what.CustomContent {
		if err := copyPageCustomContent(ctx, tx, ws, actor, sourceID, copyID); err != nil {
			return err
		}
	}
	return nil
}

// copyPageCustomContent copies the current custom content filed directly under
// a page. Each copy starts its own history at version 1 with the source's
// latest title and body, the way a copied page does.
func copyPageCustomContent(ctx context.Context, tx pgx.Tx, ws, actor, sourceID, copyID string) error {
	copies, err := queryPageIDs(ctx, tx, `WITH source AS (
			SELECT c.id,c.space_id,c.custom_type,c.title,c.body FROM wiki_content c
			WHERE c.parent_page_id::text=$1 AND c.type='custom' AND c.status='current' ORDER BY c.id
		), copied AS (
			INSERT INTO wiki_content(space_id,parent_page_id,root_page_id,type,custom_type,title,body,author_id,owner_id)
			SELECT copy.space_id,copy.id,copy.id,'custom',source.custom_type,source.title,source.body,$3,$3
			FROM source CROSS JOIN wiki_pages copy WHERE copy.id::text=$2
			RETURNING id,title,body
		), versions AS (
			INSERT INTO wiki_content_versions(content_id,version,title,status,author_id,body,message)
			SELECT id,1,title,'current',$3,body,'Copied' FROM copied
		)
		SELECT id::text FROM copied ORDER BY id`, sourceID, copyID, actor)
	if err != nil {
		return err
	}
	for _, id := range copies {
		content, scanErr := scanWikiContent(tx.QueryRow(ctx, wikiContentSelect+` WHERE c.id::text=$1`, id))
		if scanErr != nil {
			return scanErr
		}
		if err = wikiContentAction(ctx, tx, ws, actor, content, models.OpUpsert); err != nil {
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
	descendant, err := treeNodeIsDescendant(ctx, tx, payload.DestinationPageID, source.ID)
	if err != nil {
		return 0, err
	}
	if descendant {
		return 0, fmt.Errorf("%w: a hierarchy cannot be copied into itself", ErrWikiMoveValidation)
	}
	copied, err := copySubtree(ctx, tx, ws, actor, source.ID, targetSpace, targetParent, payload)
	if err != nil {
		return 0, err
	}
	return copied, tx.Commit(ctx)
}

// copySubtree copies one page and, when asked, everything beneath it. The
// recursion follows the tree rather than a flat list so a child lands under its
// own copied parent instead of the destination.
func copySubtree(ctx context.Context, tx pgx.Tx, ws, actor, pageID, spaceID string, parentID any, payload WikiCopyHierarchyRequest) (int, error) {
	var title, body string
	if err := tx.QueryRow(ctx, `SELECT title,body FROM wiki_pages WHERE id::text=$1 AND status='current'`,
		pageID).Scan(&title, &body); err != nil {
		return 0, err
	}
	copyID, err := insertCopiedPage(ctx, tx, actor, spaceID, parentID, applyTitleOptions(title, payload), body)
	if err != nil {
		return 0, err
	}
	if err = copyPageBelongings(ctx, tx, ws, actor, pageID, copyID, pageBelongings{
		Attachments: payload.CopyAttachments, Properties: payload.CopyProperties, Labels: payload.CopyLabels,
		Permissions: payload.CopyPermissions, CustomContent: payload.CopyCustomContent,
	}); err != nil {
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
		childCount, childErr := copySubtree(ctx, tx, ws, actor, child, spaceID, copyID, payload)
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
func applyTitleOptions(title string, payload WikiCopyHierarchyRequest) string {
	if payload.TitleSearch != "" {
		title = strings.ReplaceAll(title, payload.TitleSearch, payload.TitleReplace)
	}
	if payload.TitlePrefix != "" {
		title = payload.TitlePrefix + title
	}
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
		changed, err := s.changeTreeStatus(ctx, task.WorkspaceID, task.SubmittedBy, pageID, "current", "trashed", true)
		if err != nil {
			return err
		}
		trashed += changed
	}
	return s.CompleteAPITask(ctx, task, "Trashed the page tree.", map[string]any{"trashedPages": trashed})
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
	args := []any{ws, []string{apiTaskWikiCopyHierarchy, apiTaskWikiArchivePages, apiTaskWikiTrashPageTree, apiTaskWikiDeleteSpace, apiTaskWikiSpaceRoleUpdate, apiTaskWikiSpaceRoleDelete}}
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
