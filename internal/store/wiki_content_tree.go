package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// Confluence keeps pages, folders, whiteboards, databases and Smart Links in
// one content tree. Any of them can hold any other, siblings of every kind
// share one order, and moving, archiving or restoring a node works the same
// whatever it is. wiki_tree_nodes is that tree across the two tables that hold
// it.

var wikiTreeContentTypes = []string{"folder", "whiteboard", "database", "embed"}

// WikiTreeRelation is one node of the content tree as a reader may see it:
// exactly one of Page and Content is set.
type WikiTreeRelation struct {
	Page          *models.WikiPage
	Content       *models.WikiContent
	Depth         int
	ChildPosition int
}

func (r WikiTreeRelation) ID() string {
	if r.Page != nil {
		return r.Page.ID
	}
	return r.Content.ID
}

func (r WikiTreeRelation) Type() string {
	if r.Page != nil {
		return "page"
	}
	return r.Content.Type
}

func (r WikiTreeRelation) Title() string {
	if r.Page != nil {
		return r.Page.Title
	}
	return r.Content.Title
}

func (r WikiTreeRelation) Status() string {
	if r.Page != nil {
		return r.Page.Status
	}
	return r.Content.Status
}

func (r WikiTreeRelation) SpaceID() string {
	if r.Page != nil {
		return r.Page.SpaceID
	}
	return r.Content.SpaceID
}

func (r WikiTreeRelation) ParentID() string {
	if r.Page != nil {
		return r.Page.ParentID
	}
	return r.Content.ParentID
}

func (r WikiTreeRelation) CreatedAt() string {
	if r.Page != nil {
		return r.Page.CreatedAt
	}
	return r.Content.CreatedAt
}

func (r WikiTreeRelation) ModifiedAt() string {
	if r.Page != nil {
		return r.Page.Version.CreatedAt
	}
	return r.Content.Version.CreatedAt
}

type wikiTreeRow struct {
	ID, Type, ParentID string
	Position, Depth    int
}

// WikiTreeNodeType says what kind of node an id names, for callers given an id
// without its type.
func (s *Store) WikiTreeNodeType(ctx context.Context, ws, id string) (string, error) {
	var nodeType string
	err := s.Pool.QueryRow(ctx, `SELECT n.type FROM wiki_tree_nodes n JOIN wiki_spaces s ON s.id=n.space_id
		WHERE s.workspace_id=$1 AND n.id::text=$2`, ws, id).Scan(&nodeType)
	return nodeType, err
}

// WikiTreeNodeSpace says which space a node of the content tree is in.
func (s *Store) WikiTreeNodeSpace(ctx context.Context, ws, id string) (string, error) {
	var spaceID string
	err := s.Pool.QueryRow(ctx, `SELECT n.space_id::text FROM wiki_tree_nodes n JOIN wiki_spaces s ON s.id=n.space_id
		WHERE s.workspace_id=$1 AND n.id::text=$2`, ws, id).Scan(&spaceID)
	return spaceID, err
}

// CanCreateWikiPage says whether someone may add pages to a space, which is
// what arranging its content tree asks of them.
func (s *Store) CanCreateWikiPage(ctx context.Context, ws, actor, spaceID string) (bool, error) {
	var allowed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_spaces s WHERE s.workspace_id=$1 AND s.id::text=$3
		AND `+wikiSpaceVisible+` AND `+wikiSpaceCanCreatePage+`)`, ws, actor, spaceID).Scan(&allowed)
	return allowed, err
}

// WikiCustomContentChildren lists the current custom content filed directly
// beneath custom content. Custom content is not part of the page tree, so its
// children come from its own parent column.
func (s *Store) WikiCustomContentChildren(ctx context.Context, ws, user, id string) ([]*models.WikiContent, error) {
	if _, err := s.WikiContent(ctx, ws, user, id, "custom"); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiContentSelect+` WHERE s.workspace_id=$1 AND `+wikiContentVisibleFor("custom")+`
		AND c.type='custom' AND c.parent_content_id::text=$3 AND c.status='current' ORDER BY c.position,c.id`, ws, user, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	children := []*models.WikiContent{}
	for rows.Next() {
		child, scanErr := scanWikiContent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		children = append(children, child)
	}
	return children, rows.Err()
}

// WikiTreeContent reads a folder, whiteboard, database or Smart Link that is
// current or archived; an archived one is still read back, as Confluence does.
func (s *Store) WikiTreeContent(ctx context.Context, ws, user, id, contentType string) (*models.WikiContent, error) {
	return scanWikiContent(s.Pool.QueryRow(ctx, wikiContentSelect+` WHERE s.workspace_id=$1 AND `+wikiContentVisibleFor(contentType)+`
		AND c.id::text=$3 AND c.type=$4 AND c.status IN ('current','archived')`, ws, user, id, contentType))
}

// WikiContentsWithStatus lists one kind of content in a space in tree order.
func (s *Store) WikiContentsWithStatus(ctx context.Context, ws, user, spaceID, contentType, status string) ([]*models.WikiContent, error) {
	rows, err := s.Pool.Query(ctx, wikiContentSelect+` WHERE s.workspace_id=$1 AND `+wikiContentVisibleFor(contentType)+`
		AND c.space_id::text=$3 AND c.type=$4 AND c.status=$5 ORDER BY c.position,c.id`, ws, user, spaceID, contentType, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	contents := []*models.WikiContent{}
	for rows.Next() {
		content, scanErr := scanWikiContent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		contents = append(contents, content)
	}
	return contents, rows.Err()
}

func (s *Store) readableTreeRoot(ctx context.Context, ws, user, id, nodeType string) error {
	if nodeType == "page" {
		// An archived page is still read back, and so is the tree beneath it.
		page, err := s.WikiPage(ctx, ws, user, id)
		if err != nil {
			return err
		}
		if page.Status != "current" && page.Status != "archived" {
			return pgx.ErrNoRows
		}
		return nil
	}
	_, err := s.WikiTreeContent(ctx, ws, user, id, nodeType)
	return err
}

// visibleTreeNodes loads the nodes a reader may see, keyed by id.
func (s *Store) visibleTreeNodes(ctx context.Context, ws, user string, found []wikiTreeRow) (map[string]WikiTreeRelation, error) {
	pageIDs := []string{}
	contentIDs := map[string][]string{}
	for _, row := range found {
		if row.Type == "page" {
			pageIDs = append(pageIDs, row.ID)
		} else {
			contentIDs[row.Type] = append(contentIDs[row.Type], row.ID)
		}
	}
	visible := map[string]WikiTreeRelation{}
	if len(pageIDs) > 0 {
		rows, err := s.Pool.Query(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+`
			AND p.id::text=ANY($3)`, ws, user, pageIDs)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			page, scanErr := scanWikiPage(rows)
			if scanErr != nil {
				rows.Close()
				return nil, scanErr
			}
			visible[page.ID] = WikiTreeRelation{Page: page}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}
	for _, contentType := range wikiTreeContentTypes {
		ids := contentIDs[contentType]
		if len(ids) == 0 {
			continue
		}
		rows, err := s.Pool.Query(ctx, wikiContentSelect+` WHERE s.workspace_id=$1 AND `+wikiContentVisibleFor(contentType)+`
			AND c.id::text=ANY($3) AND c.type=$4`, ws, user, ids, contentType)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			content, scanErr := scanWikiContent(rows)
			if scanErr != nil {
				rows.Close()
				return nil, scanErr
			}
			visible[content.ID] = WikiTreeRelation{Content: content}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, err
		}
	}
	return visible, nil
}

func scanTreeRows(rows pgx.Rows) ([]wikiTreeRow, error) {
	defer rows.Close()
	found := []wikiTreeRow{}
	for rows.Next() {
		var row wikiTreeRow
		if err := rows.Scan(&row.ID, &row.Type, &row.ParentID, &row.Position, &row.Depth); err != nil {
			return nil, err
		}
		found = append(found, row)
	}
	return found, rows.Err()
}

// WikiTreeDescendants lists the current and archived nodes beneath a node, of
// every kind, down to maxDepth. A node the reader cannot see hides everything
// beneath it too.
func (s *Store) WikiTreeDescendants(ctx context.Context, ws, user, rootID, rootType string, maxDepth int) ([]WikiTreeRelation, error) {
	if err := s.readableTreeRoot(ctx, ws, user, rootID, rootType); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `WITH RECURSIVE tree(id,depth) AS (
			SELECT n.id,1 FROM wiki_tree_nodes n WHERE n.parent_id::text=$1 AND n.status IN ('current','archived')
			UNION ALL
			SELECT n.id,t.depth+1 FROM wiki_tree_nodes n JOIN tree t ON n.parent_id=t.id
			WHERE t.depth<$2 AND n.status IN ('current','archived')
		)
		SELECT n.id::text,n.type,COALESCE(n.parent_id::text,''),n.position,t.depth
		FROM tree t JOIN wiki_tree_nodes n ON n.id=t.id
		ORDER BY t.depth,n.parent_id,n.position,n.id`, rootID, maxDepth)
	if err != nil {
		return nil, err
	}
	found, err := scanTreeRows(rows)
	if err != nil {
		return nil, err
	}
	visible, err := s.visibleTreeNodes(ctx, ws, user, found)
	if err != nil {
		return nil, err
	}
	included := map[string]bool{rootID: true}
	relations := []WikiTreeRelation{}
	for _, row := range found {
		relation, ok := visible[row.ID]
		if !ok || !included[row.ParentID] {
			continue
		}
		included[row.ID] = true
		relation.Depth, relation.ChildPosition = row.Depth, row.Position
		relations = append(relations, relation)
	}
	return relations, nil
}

// WikiTreeAncestors lists the nodes above a node from the top of the space
// down, leaving out the ones the reader cannot see.
func (s *Store) WikiTreeAncestors(ctx context.Context, ws, user, id, nodeType string) ([]WikiTreeRelation, error) {
	if err := s.readableTreeRoot(ctx, ws, user, id, nodeType); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `WITH RECURSIVE up(id,depth) AS (
			SELECT parent_id,1 FROM wiki_tree_nodes WHERE id::text=$1 AND parent_id IS NOT NULL
			UNION ALL
			SELECT n.parent_id,u.depth+1 FROM wiki_tree_nodes n JOIN up u ON n.id=u.id
			WHERE n.parent_id IS NOT NULL AND u.depth<1000
		)
		SELECT n.id::text,n.type,COALESCE(n.parent_id::text,''),n.position,u.depth
		FROM up u JOIN wiki_tree_nodes n ON n.id=u.id
		WHERE n.status IN ('current','archived')
		ORDER BY u.depth DESC`, id)
	if err != nil {
		return nil, err
	}
	found, err := scanTreeRows(rows)
	if err != nil {
		return nil, err
	}
	visible, err := s.visibleTreeNodes(ctx, ws, user, found)
	if err != nil {
		return nil, err
	}
	relations := []WikiTreeRelation{}
	for _, row := range found {
		if relation, ok := visible[row.ID]; ok {
			relation.Depth, relation.ChildPosition = row.Depth, row.Position
			relations = append(relations, relation)
		}
	}
	return relations, nil
}

// lockedTreeNode is a node the caller may change, locked for the transaction.
type lockedTreeNode struct {
	ID, Type, SpaceID, ParentID, ParentType, Title, Status string
	Private                                                bool
	Position                                               int
}

func lockWritableTreeNode(ctx context.Context, tx pgx.Tx, ws, actor, id, status string) (lockedTreeNode, error) {
	var node lockedTreeNode
	if err := tx.QueryRow(ctx, `SELECT n.type FROM wiki_tree_nodes n JOIN wiki_spaces s ON s.id=n.space_id
		WHERE s.workspace_id=$1 AND n.id::text=$2`, ws, id).Scan(&node.Type); err != nil {
		return node, err
	}
	if node.Type == "page" {
		page, err := lockWritablePage(ctx, tx, ws, actor, id, status)
		if err != nil {
			return node, err
		}
		node.ID, node.Title, node.Status = page.ID, page.Title, page.Status
	} else if err := tx.QueryRow(ctx, `SELECT c.id::text,c.title,c.status,c.private
		FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id LEFT JOIN wiki_pages p ON p.id=c.root_page_id
		WHERE s.workspace_id=$1 AND `+wikiContentWritableFor(node.Type)+` AND c.id::text=$3 AND c.type=$4 AND c.status=$5
		FOR UPDATE OF c`, ws, actor, id, node.Type, status).Scan(&node.ID, &node.Title, &node.Status, &node.Private); err != nil {
		return node, err
	}
	err := tx.QueryRow(ctx, `SELECT n.space_id::text,COALESCE(n.parent_id::text,''),COALESCE(parent.type,''),n.position
		FROM wiki_tree_nodes n LEFT JOIN wiki_tree_nodes parent ON parent.id=n.parent_id WHERE n.id::text=$1`,
		node.ID).Scan(&node.SpaceID, &node.ParentID, &node.ParentType, &node.Position)
	return node, err
}

// placeTreeNodes gives nodes a new parent. Which column holds it depends on
// what kind of node the parent is.
func placeTreeNodes(ctx context.Context, tx pgx.Tx, ids []string, parentID, parentType string) error {
	if _, err := tx.Exec(ctx, `UPDATE wiki_pages SET
			parent_id=CASE WHEN $3::text='page' THEN NULLIF($2::text,'')::bigint END,
			parent_content_id=CASE WHEN $3::text NOT IN ('page','') THEN NULLIF($2::text,'')::bigint END
		WHERE id::text=ANY($1)`, ids, parentID, parentType); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE wiki_content SET
			parent_page_id=CASE WHEN $3::text='page' THEN NULLIF($2::text,'')::bigint END,
			parent_content_id=CASE WHEN $3::text NOT IN ('page','') THEN NULLIF($2::text,'')::bigint END
		WHERE id::text=ANY($1)`, ids, parentID, parentType)
	return err
}

func setTreePosition(ctx context.Context, tx pgx.Tx, id string, position int) error {
	if _, err := tx.Exec(ctx, `UPDATE wiki_pages SET position=$2 WHERE id::text=$1`, id, position); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE wiki_content SET position=$2 WHERE id::text=$1`, id, position)
	return err
}

func nextTreePosition(ctx context.Context, tx pgx.Tx, spaceID, parentID string) (int, error) {
	var position int
	err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(position),0)+1 FROM wiki_tree_nodes
		WHERE space_id::text=$1 AND parent_id IS NOT DISTINCT FROM NULLIF($2::text,'')::bigint`, spaceID, parentID).Scan(&position)
	return position, err
}

// shiftTreeSiblings makes room at a position among the children of one
// parent, whichever table each child lives in.
func shiftTreeSiblings(ctx context.Context, tx pgx.Tx, spaceID, parentID string, from int, exceptID string) error {
	if _, err := tx.Exec(ctx, `UPDATE wiki_pages SET position=position+1
		WHERE space_id::text=$1 AND COALESCE(parent_id,parent_content_id) IS NOT DISTINCT FROM NULLIF($2::text,'')::bigint
		AND position>=$3 AND id::text<>$4`, spaceID, parentID, from, exceptID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE wiki_content SET position=position+1
		WHERE space_id::text=$1 AND COALESCE(parent_content_id,parent_page_id) IS NOT DISTINCT FROM NULLIF($2::text,'')::bigint
		AND type IN ('folder','whiteboard','database','embed') AND position>=$3 AND id::text<>$4`, spaceID, parentID, from, exceptID)
	return err
}

func treeSubtreeIDs(ctx context.Context, tx pgx.Tx, id, status string) ([]string, error) {
	return queryPageIDs(ctx, tx, `WITH RECURSIVE tree(id) AS (
			SELECT id FROM wiki_tree_nodes WHERE id::text=$1
			UNION
			SELECT n.id FROM wiki_tree_nodes n JOIN tree t ON n.parent_id=t.id WHERE $2::text='' OR n.status=$2::text
		)
		SELECT id::text FROM tree ORDER BY id`, id, status)
}

func treeNodeIsDescendant(ctx context.Context, tx pgx.Tx, candidateID, ancestorID string) (bool, error) {
	var descendant bool
	err := tx.QueryRow(ctx, `WITH RECURSIVE up(id,parent_id) AS (
			SELECT id,parent_id FROM wiki_tree_nodes WHERE id::text=$1
			UNION
			SELECT n.id,n.parent_id FROM wiki_tree_nodes n JOIN up ON n.id=up.parent_id
		)
		SELECT EXISTS(SELECT 1 FROM up WHERE id::text=$2)`, candidateID, ancestorID).Scan(&descendant)
	return descendant, err
}

// refreshContentRoots points content at the nearest page above it, which is
// the page whose restrictions decide who may see it.
func refreshContentRoots(ctx context.Context, tx pgx.Tx, ids []string) error {
	_, err := tx.Exec(ctx, `WITH RECURSIVE up(start_id,id,type,depth) AS (
			SELECT c.id,n.parent_id,parent.type,1
			FROM wiki_content c JOIN wiki_tree_nodes n ON n.id=c.id JOIN wiki_tree_nodes parent ON parent.id=n.parent_id
			WHERE c.id::text=ANY($1)
			UNION ALL
			SELECT up.start_id,n.parent_id,parent.type,up.depth+1
			FROM up JOIN wiki_tree_nodes n ON n.id=up.id JOIN wiki_tree_nodes parent ON parent.id=n.parent_id
			WHERE up.type<>'page'
		)
		UPDATE wiki_content c SET root_page_id=(
			SELECT up.id FROM up WHERE up.start_id=c.id AND up.type='page' ORDER BY up.depth LIMIT 1)
		WHERE c.id::text=ANY($1) AND c.type IN ('folder','whiteboard','database','embed')`, ids)
	return err
}

// treeSnapshotActions records each node as it now is.
func treeSnapshotActions(ctx context.Context, tx pgx.Tx, ws, actor string, ids []string) error {
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		var nodeType string
		if err := tx.QueryRow(ctx, `SELECT type FROM wiki_tree_nodes WHERE id::text=$1`, id).Scan(&nodeType); err != nil {
			return err
		}
		if nodeType == "page" {
			if err := wikiPageSnapshotAction(ctx, tx, ws, actor, id); err != nil {
				return err
			}
			continue
		}
		content, err := scanWikiContent(tx.QueryRow(ctx, wikiContentSelect+` WHERE c.id::text=$1`, id))
		if err != nil {
			return err
		}
		if err = wikiContentAction(ctx, tx, ws, actor, content, models.OpUpsert); err != nil {
			return err
		}
	}
	return nil
}

// MoveWikiTreeNode moves a page, folder, whiteboard, database or Smart Link
// before or after another node, or beneath it (`append` last, `above` first).
// The target may be in another space; the node then takes everything beneath
// it along.
func (s *Store) MoveWikiTreeNode(ctx context.Context, ws, actor, nodeID, position, targetID string) (string, error) {
	return s.moveTreeNode(ctx, ws, actor, nodeID, position, targetID, "")
}

// MoveWikiTreeNodeToSpace moves a node to the top level of a space, after what
// is already there.
func (s *Store) MoveWikiTreeNodeToSpace(ctx context.Context, ws, actor, nodeID, spaceID string) (string, error) {
	return s.moveTreeNode(ctx, ws, actor, nodeID, "top", "", spaceID)
}

func (s *Store) moveTreeNode(ctx context.Context, ws, actor, nodeID, position, targetID, spaceID string) (string, error) {
	switch position {
	case "before", "after", "append", "above":
		if targetID == "" {
			return "", fmt.Errorf("%w: a target is required", ErrWikiMoveValidation)
		}
	case "top":
	default:
		return "", fmt.Errorf("%w: position must be before, after, append or above", ErrWikiMoveValidation)
	}
	if nodeID == targetID {
		return "", fmt.Errorf("%w: content cannot be moved relative to itself", ErrWikiMoveValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	node, err := lockWritableTreeNode(ctx, tx, ws, actor, nodeID, "current")
	if err != nil {
		return "", err
	}
	var target lockedTreeNode
	var newParent, newParentType, toSpace string
	if position == "top" {
		var allowed bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_spaces s WHERE s.workspace_id=$1 AND s.id::text=$3
			AND `+wikiSpaceVisible+` AND `+wikiSpacePermissionAllowed("create/"+node.Type)+`)`, ws, actor, spaceID).Scan(&allowed); err != nil {
			return "", err
		}
		if !allowed {
			return "", pgx.ErrNoRows
		}
		toSpace = spaceID
	} else {
		if target, err = lockWritableTreeNode(ctx, tx, ws, actor, targetID, "current"); err != nil {
			return "", err
		}
		toSpace = target.SpaceID
		newParent, newParentType = target.ParentID, target.ParentType
		if position == "append" || position == "above" {
			newParent, newParentType = target.ID, target.Type
		}
		descendant, descendantErr := treeNodeIsDescendant(ctx, tx, target.ID, node.ID)
		if descendantErr != nil {
			return "", descendantErr
		}
		if descendant {
			return "", fmt.Errorf("%w: content cannot be moved beneath itself", ErrWikiMoveValidation)
		}
	}
	if newParentType != "" && newParentType != "page" && !node.Private {
		var private bool
		if err = tx.QueryRow(ctx, `SELECT private FROM wiki_content WHERE id::text=$1`, newParent).Scan(&private); err != nil {
			return "", err
		}
		if private {
			return "", fmt.Errorf("%w: only private content can be placed beneath private content", ErrWikiMoveValidation)
		}
	}
	subtree, err := treeSubtreeIDs(ctx, tx, node.ID, "")
	if err != nil {
		return "", err
	}
	if node.SpaceID != toSpace {
		if err = relocateTree(ctx, tx, ws, actor, node, subtree, toSpace); err != nil {
			return "", err
		}
	}
	var newPosition int
	switch position {
	case "top", "append":
		if newPosition, err = nextTreePosition(ctx, tx, toSpace, newParent); err != nil {
			return "", err
		}
	case "above":
		newPosition = 1
		err = shiftTreeSiblings(ctx, tx, toSpace, newParent, newPosition, node.ID)
	default:
		newPosition = target.Position
		if position == "after" {
			newPosition++
		}
		err = shiftTreeSiblings(ctx, tx, toSpace, newParent, newPosition, node.ID)
	}
	if err != nil {
		return "", err
	}
	if err = placeTreeNodes(ctx, tx, []string{node.ID}, newParent, newParentType); err != nil {
		return "", err
	}
	if err = setTreePosition(ctx, tx, node.ID, newPosition); err != nil {
		return "", err
	}
	if err = refreshContentRoots(ctx, tx, subtree); err != nil {
		return "", err
	}
	if err = treeSnapshotActions(ctx, tx, ws, actor, subtree); err != nil {
		return "", err
	}
	return node.ID, tx.Commit(ctx)
}

// relocateTree carries a node and everything beneath it into another space,
// together with the custom content that hangs off those nodes. Confluence asks
// for permission to delete the node's kind of content where the tree is and to
// add it where it is going; it will not take a space's homepage out of its
// space, and a destination already showing one of the page titles refuses the
// move rather than holding two current pages of the same name.
func relocateTree(ctx context.Context, tx pgx.Tx, ws, actor string, node lockedTreeNode, subtree []string, toSpace string) error {
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM wiki_spaces s WHERE s.workspace_id=$1 AND s.id::text=$3 AND `+wikiSpacePermissionAllowed("delete/"+node.Type)+`)
		AND EXISTS(SELECT 1 FROM wiki_spaces s WHERE s.workspace_id=$1 AND s.id::text=$4 AND `+wikiSpaceVisible+` AND `+wikiSpacePermissionAllowed("create/"+node.Type)+`)`,
		ws, actor, node.SpaceID, toSpace).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("%w: moving content to another space needs permission to delete it here and to add it there", ErrProjectPermission)
	}
	var homepage bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_spaces WHERE id::text=$1 AND homepage_id::text=$2)`,
		node.SpaceID, node.ID).Scan(&homepage); err != nil {
		return err
	}
	if homepage {
		return fmt.Errorf("%w: a space homepage cannot be moved to another space", ErrWikiMoveValidation)
	}
	var clash string
	err := tx.QueryRow(ctx, `SELECT moving.title FROM wiki_pages moving
		JOIN wiki_pages there ON there.title=moving.title AND there.space_id::text=$2 AND there.status='current'
		WHERE moving.id::text=ANY($1) AND moving.status='current' ORDER BY moving.id LIMIT 1`, subtree, toSpace).Scan(&clash)
	if err == nil {
		return fmt.Errorf("%w: a page titled %q already exists in the destination space", ErrWikiMoveValidation, clash)
	}
	if err != pgx.ErrNoRows {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_pages SET space_id=$2::bigint WHERE id::text=ANY($1)`, subtree, toSpace); err != nil {
		return err
	}
	owned, err := queryPageIDs(ctx, tx, `WITH RECURSIVE owned AS (
			SELECT id FROM wiki_content
			WHERE id::text=ANY($1) OR parent_page_id::text=ANY($1) OR root_page_id::text=ANY($1) OR parent_content_id::text=ANY($1)
			UNION
			SELECT c.id FROM wiki_content c JOIN owned ON c.parent_content_id=owned.id
		)
		SELECT id::text FROM owned ORDER BY id`, subtree)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_content SET space_id=$2::bigint WHERE id::text=ANY($1)`, owned, toSpace); err != nil {
		return err
	}
	inTree := map[string]bool{}
	for _, id := range subtree {
		inTree[id] = true
	}
	for _, id := range owned {
		if inTree[id] {
			continue
		}
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

// ArchiveWikiTreeNode and RestoreWikiTreeNode archive and restore any node of
// the content tree, the way a page's own actions do.
func (s *Store) ArchiveWikiTreeNode(ctx context.Context, ws, actor, id string, withDescendants bool) (int, error) {
	return s.changeTreeStatus(ctx, ws, actor, id, "current", "archived", withDescendants)
}

func (s *Store) RestoreWikiTreeNode(ctx context.Context, ws, actor, id string, withDescendants bool) (int, error) {
	return s.changeTreeStatus(ctx, ws, actor, id, "archived", "current", withDescendants)
}

// changeTreeStatus moves a node, and optionally the nodes beneath it that
// share its status, to another status.
//
// Archiving a node without its children lifts them a level so they stay in
// the tree, as Confluence does. Restoring puts a node back under its parent
// when that parent is still current and at the top of the space otherwise,
// and refuses when the space already shows a current page with one of the
// page titles.
func (s *Store) changeTreeStatus(ctx context.Context, ws, actor, nodeID, from, to string, withDescendants bool) (int, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	node, err := lockWritableTreeNode(ctx, tx, ws, actor, nodeID, from)
	if err != nil {
		return 0, err
	}
	ids := []string{node.ID}
	if withDescendants {
		if ids, err = treeSubtreeIDs(ctx, tx, node.ID, from); err != nil {
			return 0, err
		}
	}
	touched := append([]string{}, ids...)
	switch {
	case to == "archived" && !withDescendants:
		children, childErr := queryPageIDs(ctx, tx, `SELECT id::text FROM wiki_tree_nodes
			WHERE parent_id::text=$1 AND status='current' ORDER BY position,id`, node.ID)
		if childErr != nil {
			return 0, childErr
		}
		if len(children) > 0 {
			if err = placeTreeNodes(ctx, tx, children, node.ParentID, node.ParentType); err != nil {
				return 0, err
			}
			for _, child := range children {
				lifted, liftErr := treeSubtreeIDs(ctx, tx, child, "")
				if liftErr != nil {
					return 0, liftErr
				}
				if err = refreshContentRoots(ctx, tx, lifted); err != nil {
					return 0, err
				}
				touched = append(touched, lifted...)
			}
		}
	case to == "current":
		var clash string
		err = tx.QueryRow(ctx, `SELECT restoring.title FROM wiki_pages restoring
			JOIN wiki_pages there ON there.space_id=restoring.space_id AND there.title=restoring.title AND there.status='current'
			WHERE restoring.id::text=ANY($1) ORDER BY restoring.id LIMIT 1`, ids).Scan(&clash)
		if err == nil {
			return 0, fmt.Errorf("%w: a current page titled %q already exists in this space", ErrWikiMoveValidation, clash)
		}
		if err != pgx.ErrNoRows {
			return 0, err
		}
		if node.ParentID != "" {
			var parentStatus string
			if err = tx.QueryRow(ctx, `SELECT status FROM wiki_tree_nodes WHERE id::text=$1`, node.ParentID).Scan(&parentStatus); err != nil {
				return 0, err
			}
			if parentStatus != "current" {
				position, positionErr := nextTreePosition(ctx, tx, node.SpaceID, "")
				if positionErr != nil {
					return 0, positionErr
				}
				if err = placeTreeNodes(ctx, tx, []string{node.ID}, "", ""); err != nil {
					return 0, err
				}
				if err = setTreePosition(ctx, tx, node.ID, position); err != nil {
					return 0, err
				}
				subtree, subtreeErr := treeSubtreeIDs(ctx, tx, node.ID, "")
				if subtreeErr != nil {
					return 0, subtreeErr
				}
				if err = refreshContentRoots(ctx, tx, subtree); err != nil {
					return 0, err
				}
				touched = append(touched, subtree...)
			}
		}
	}
	pages, err := tx.Exec(ctx, `UPDATE wiki_pages SET status=$2 WHERE id::text=ANY($1) AND status=$3`, ids, to, from)
	if err != nil {
		return 0, err
	}
	contents, err := tx.Exec(ctx, `UPDATE wiki_content SET status=$2,updated_at=now()
		WHERE id::text=ANY($1) AND status=$3 AND type IN ('folder','whiteboard','database','embed')`, ids, to, from)
	if err != nil {
		return 0, err
	}
	if err = treeSnapshotActions(ctx, tx, ws, actor, touched); err != nil {
		return 0, err
	}
	return int(pages.RowsAffected() + contents.RowsAffected()), tx.Commit(ctx)
}

// RenameWikiContent gives a folder, whiteboard, database or Smart Link a new
// title, recorded as a new version.
func (s *Store) RenameWikiContent(ctx context.Context, ws, actor, id, title string) (*models.WikiContent, error) {
	title = strings.TrimSpace(title)
	if title == "" || len([]rune(title)) > 255 {
		return nil, fmt.Errorf("%w: a title of 1 to 255 characters is required", ErrWikiValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	node, err := lockWritableTreeNode(ctx, tx, ws, actor, id, "current")
	if err != nil {
		return nil, err
	}
	if node.Type == "page" {
		return nil, fmt.Errorf("%w: a page is renamed by editing its title", ErrWikiValidation)
	}
	var version int
	var embedURL string
	if err = tx.QueryRow(ctx, `UPDATE wiki_content SET title=$2,version=version+1,updated_at=now()
		WHERE id::text=$1 RETURNING version,embed_url`, node.ID, title).Scan(&version, &embedURL); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_content_versions(content_id,version,title,status,embed_url,author_id,message)
		VALUES($1::bigint,$2,$3,'current',$4,$5,'Renamed')`, node.ID, version, title, embedURL, actor); err != nil {
		return nil, err
	}
	content, err := scanWikiContent(tx.QueryRow(ctx, wikiContentSelect+` WHERE c.id::text=$1`, node.ID))
	if err != nil {
		return nil, err
	}
	if err = wikiContentAction(ctx, tx, ws, actor, content, models.OpUpsert); err != nil {
		return nil, err
	}
	return content, tx.Commit(ctx)
}
