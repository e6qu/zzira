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

var ErrWikiValidation = errors.New("invalid wiki content")

var ErrWikiConflict = errors.New("the page changed; reload the latest version before saving")

// ErrWikiContentConflict is the same rule for content that is not a page, so
// the message does not tell a caller a page changed when none did.
var ErrWikiContentConflict = errors.New("the content changed; reload the latest version before saving")

var ErrWikiCommentConflict = errors.New("the comment changed; reload the latest version before saving")

var ErrWikiPropertyConflict = errors.New("the property changed; reload the latest version before saving")

const wikiActiveMember = `EXISTS (
  SELECT 1 FROM memberships wm JOIN users wu ON wu.id=wm.user_id AND wu.active
  WHERE wm.workspace_id=s.workspace_id AND wm.user_id=$2 AND EXISTS (
    SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id
    JOIN directory_users du ON du.directory_id=d.id AND du.user_id=wm.user_id
    WHERE si.workspace_id=wm.workspace_id AND d.active AND du.active
  )
)`

const wikiSpaceRolePrincipalMatches = `(
  (wra.principal_type='USER' AND wra.principal_id=$2)
  OR (wra.principal_type='GROUP' AND EXISTS (
    SELECT 1 FROM group_members gm WHERE gm.group_id::text=wra.principal_id AND gm.user_id=$2
  ))
  OR (wra.principal_type='ACCESS_CLASS' AND wra.principal_id IN ('authenticated-users','all-licensed-users'))
  OR (
    wra.principal_type='ACCESS_CLASS'
    AND wra.principal_id IN ('all-product-admins','jsm-project-admins')
    AND EXISTS (
      SELECT 1 FROM memberships ram
      WHERE ram.workspace_id=s.workspace_id AND ram.user_id=$2 AND ram.role='admin'
    )
  )
)`

func wikiSpacePermissionAllowed(permission string) string {
	literal := "'" + strings.ReplaceAll(permission, "'", "''") + "'"
	systemRoleIDs := []string{}
	for _, role := range systemWikiSpaceRoles {
		for _, allowed := range role.SpacePermissions {
			if allowed == permission || allowed == "administer/space" {
				systemRoleIDs = append(systemRoleIDs, "'"+strings.ReplaceAll(role.ID, "'", "''")+"'")
				break
			}
		}
	}
	systemRoles := "FALSE"
	if len(systemRoleIDs) > 0 {
		systemRoles = "wra.role_id IN (" + strings.Join(systemRoleIDs, ",") + ")"
	}
	return `(
  NOT EXISTS (SELECT 1 FROM wiki_space_role_assignments wra0 WHERE wra0.space_id=s.id)
  OR EXISTS (
    SELECT 1
    FROM wiki_space_role_assignments wra
    LEFT JOIN wiki_space_roles wrr ON wrr.id::text=wra.role_id AND wrr.workspace_id=s.workspace_id
    WHERE wra.space_id=s.id
      AND ` + wikiSpaceRolePrincipalMatches + `
      AND (` + systemRoles + ` OR ` + literal + `=ANY(wrr.space_permissions) OR 'administer/space'=ANY(wrr.space_permissions))
  )
)`
}

// wikiSpaceAdmin accepts workspace administrators and principals explicitly
// assigned a role with administer/space in this space. The explicit-assignment
// check is required because legacy spaces without role assignments keep their
// historical member content access, which must not imply space administration.
func wikiSpaceAdmin(ctx context.Context, tx pgx.Tx, ws, actor, spaceID string) error {
	if err := projectAdmin(ctx, tx, ws, actor); err == nil {
		return nil
	} else if !errors.Is(err, ErrProjectPermission) {
		return err
	}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM wiki_spaces s
		WHERE s.workspace_id=$1 AND s.id::text=$3
		  AND `+wikiActiveMember+`
		  AND EXISTS(SELECT 1 FROM wiki_space_role_assignments wra0 WHERE wra0.space_id=s.id)
		  AND `+wikiSpacePermissionAllowed("administer/space")+`
	)`, ws, actor, spaceID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrProjectPermission
	}
	return nil
}

func (s *Store) CanAdministerWikiSpace(ctx context.Context, ws, actor, spaceID string) (bool, error) {
	var allowed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM wiki_spaces s
		WHERE s.workspace_id=$1 AND s.id::text=$3 AND (
		  EXISTS(SELECT 1 FROM memberships am WHERE am.workspace_id=s.workspace_id AND am.user_id=$2 AND am.role='admin')
		  OR (`+wikiActiveMember+`
		    AND EXISTS(SELECT 1 FROM wiki_space_role_assignments wra0 WHERE wra0.space_id=s.id)
		    AND `+wikiSpacePermissionAllowed("administer/space")+`)
		)
	)`, ws, actor, spaceID).Scan(&allowed)
	return allowed, err
}

// Stored role assignments replace the legacy public/private rule. Spaces
// without stored assignments retain their historical member/author behavior.
var wikiSpaceVisible = `(` + wikiActiveMember + `) AND (` + wikiSpacePermissionAllowed("read/space") + `) AND (
  NOT s.private OR s.author_id=$2 OR EXISTS (
    SELECT 1 FROM wiki_space_role_assignments wra1 WHERE wra1.space_id=s.id
  )
)`

var wikiSpaceCanCreatePage = wikiSpacePermissionAllowed("create/page")
var wikiSpaceCanUpdatePage = wikiSpacePermissionAllowed("update/page")
var wikiSpaceCanDeletePage = wikiSpacePermissionAllowed("delete/page")
var wikiSpaceCanCreateBlogPost = wikiSpacePermissionAllowed("create/blogpost")
var wikiSpaceCanUpdateBlogPost = wikiSpacePermissionAllowed("update/blogpost")
var wikiSpaceCanDeleteBlogPost = wikiSpacePermissionAllowed("delete/blogpost")
var wikiSpaceCanCreateAttachment = wikiSpacePermissionAllowed("create/attachment")
var wikiSpaceCanUpdateAttachment = wikiSpacePermissionAllowed("update/attachment")
var wikiSpaceCanDeleteAttachment = wikiSpacePermissionAllowed("delete/attachment")
var wikiSpaceCanCreateComment = wikiSpacePermissionAllowed("create/comment")
var wikiSpaceCanUpdateComment = wikiSpacePermissionAllowed("update/comment")
var wikiSpaceCanDeleteComment = wikiSpacePermissionAllowed("delete/comment")

var wikiPageVisible = `(` + wikiSpacePermissionAllowed("read/page") + `) AND (p.published OR p.author_id=$2) AND (
  p.author_id=$2
  OR EXISTS (SELECT 1 FROM memberships am WHERE am.workspace_id=s.workspace_id AND am.user_id=$2 AND am.role='admin')
  OR NOT EXISTS (SELECT 1 FROM wiki_page_restrictions wr WHERE wr.page_id=p.id AND wr.operation='read')
  OR EXISTS (SELECT 1 FROM wiki_page_restrictions wr WHERE wr.page_id=p.id AND wr.operation='read' AND wr.subject_type='user' AND wr.subject_id=$2)
  OR EXISTS (SELECT 1 FROM wiki_page_restrictions wr JOIN group_members gm ON wr.subject_type='group' AND gm.group_id::text=wr.subject_id WHERE wr.page_id=p.id AND wr.operation='read' AND gm.user_id=$2)
)`

const wikiPageRestrictionWritable = `(
  p.author_id=$2
  OR EXISTS (SELECT 1 FROM memberships am WHERE am.workspace_id=s.workspace_id AND am.user_id=$2 AND am.role='admin')
  OR NOT EXISTS (SELECT 1 FROM wiki_page_restrictions wr WHERE wr.page_id=p.id AND wr.operation='update')
  OR EXISTS (SELECT 1 FROM wiki_page_restrictions wr WHERE wr.page_id=p.id AND wr.operation='update' AND wr.subject_type='user' AND wr.subject_id=$2)
  OR EXISTS (SELECT 1 FROM wiki_page_restrictions wr JOIN group_members gm ON wr.subject_type='group' AND gm.group_id::text=wr.subject_id WHERE wr.page_id=p.id AND wr.operation='update' AND gm.user_id=$2)
)`

var wikiPageWritable = `(` + wikiSpaceVisible + `) AND (` + wikiSpaceCanUpdatePage + `) AND (` + wikiPageRestrictionWritable + `)`

const wikiSpaceSelect = `SELECT s.id::text,s.workspace_id,s.key,s.name,s.description,s.author_id,s.private,s.default_classification_level,to_char(s.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_spaces s`
const wikiPageSelect = `SELECT p.id::text,s.workspace_id,p.space_id::text,COALESCE(p.parent_id::text,''),p.title,p.status,p.published,p.classification_level,p.body,p.author_id,to_char(p.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),v.version,v.message,v.minor_edit,v.author_id,to_char(v.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id JOIN wiki_page_versions v ON v.page_id=p.id AND v.version=p.version`
const wikiCommentSelect = `SELECT c.id::text,COALESCE(p.id::text,''),COALESCE(bp.id::text,''),COALESCE(p.space_id,bp.space_id)::text,COALESCE(c.attachment_id::text,''),COALESCE(c.parent_id::text,''),c.body,c.author_id,u.display_name,c.version,v.message,to_char(c.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),to_char(c.updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),v.author_id,to_char(v.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),c.comment_type,c.inline_selection,c.inline_match_count,c.inline_match_index,c.inline_marker_ref,c.resolution_status,COALESCE(c.resolution_modifier_id,''),COALESCE(to_char(c.resolution_modified_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),COALESCE(bp.author_id,''),COALESCE(bp.private,false),COALESCE(bp.published,false) FROM wiki_footer_comments c LEFT JOIN wiki_attachments ca ON ca.id=c.attachment_id LEFT JOIN wiki_pages p ON p.id=COALESCE(c.page_id,ca.page_id) LEFT JOIN wiki_blog_posts bp ON bp.id=COALESCE(c.blog_post_id,ca.blog_post_id) JOIN wiki_spaces s ON s.id=COALESCE(p.space_id,bp.space_id) JOIN users u ON u.id=c.author_id JOIN wiki_footer_comment_versions v ON v.comment_id=c.id AND v.version=c.version`

var wikiCommentVisible = `(` + wikiSpacePermissionAllowed("read/comment") + `) AND ((p.id IS NOT NULL AND p.status='current' AND ` + wikiPageVisible + `) OR (bp.id IS NOT NULL AND bp.status='current' AND ` + wikiSpacePermissionAllowed("read/blogpost") + ` AND (bp.published OR bp.author_id=$2) AND (NOT bp.private OR bp.author_id=$2)))`

func scanWikiSpace(row pgx.Row) (*models.WikiSpace, error) {
	s := &models.WikiSpace{}
	err := row.Scan(&s.ID, &s.WorkspaceID, &s.Key, &s.Name, &s.Description, &s.AuthorID, &s.Private, &s.DefaultClassificationLevel, &s.CreatedAt)
	return s, err
}
func scanWikiPage(row pgx.Row) (*models.WikiPage, error) {
	p := &models.WikiPage{Body: models.WikiBody{Representation: "storage"}}
	err := row.Scan(&p.ID, &p.WorkspaceID, &p.SpaceID, &p.ParentID, &p.Title, &p.Status, &p.Published, &p.ClassificationLevel, &p.Body.Value, &p.AuthorID, &p.CreatedAt, &p.Version.Number, &p.Version.Message, &p.Version.MinorEdit, &p.Version.AuthorID, &p.Version.CreatedAt)
	return p, err
}

func scanWikiFooterComment(row pgx.Row) (*models.WikiFooterComment, error) {
	c := &models.WikiFooterComment{Body: models.WikiBody{Representation: "storage"}}
	err := row.Scan(&c.ID, &c.PageID, &c.BlogPostID, &c.SpaceID, &c.AttachmentID, &c.ParentCommentID, &c.Body.Value, &c.AuthorID, &c.AuthorName, &c.Version.Number, &c.Version.Message, &c.CreatedAt, &c.UpdatedAt, &c.Version.AuthorID, &c.Version.CreatedAt, &c.CommentType, &c.InlineSelection, &c.InlineMatchCount, &c.InlineMatchIndex, &c.InlineMarkerRef, &c.ResolutionStatus, &c.ResolutionModifierID, &c.ResolutionModifiedAt, &c.ParentAuthorID, &c.ParentPrivate, &c.ParentPublished)
	return c, err
}

func (s *Store) WikiSpace(ctx context.Context, ws, user, id string) (*models.WikiSpace, error) {
	return scanWikiSpace(s.Pool.QueryRow(ctx, wikiSpaceSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND s.id::text=$3`, ws, user, id))
}
func (s *Store) WikiSpaces(ctx context.Context, ws, user string) ([]*models.WikiSpace, error) {
	rows, err := s.Pool.Query(ctx, wikiSpaceSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` ORDER BY s.id`, ws, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.WikiSpace{}
	for rows.Next() {
		item, err := scanWikiSpace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Store) WikiPage(ctx context.Context, ws, user, id string) (*models.WikiPage, error) {
	return scanWikiPage(s.Pool.QueryRow(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND p.id::text=$3`, ws, user, id))
}

func (s *Store) WikiPageAtVersion(ctx context.Context, ws, user, id string, version int) (*models.WikiPage, error) {
	page, err := s.WikiPage(ctx, ws, user, id)
	if err != nil {
		return nil, err
	}
	err = s.Pool.QueryRow(ctx, `SELECT title,status,body,version,message,minor_edit,author_id,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_page_versions WHERE page_id::text=$1 AND version=$2 AND (status<>'draft' OR author_id=$3)`, id, version, user).Scan(&page.Title, &page.Status, &page.Body.Value, &page.Version.Number, &page.Version.Message, &page.Version.MinorEdit, &page.Version.AuthorID, &page.Version.CreatedAt)
	return page, err
}
func (s *Store) WikiPages(ctx context.Context, ws, user, space, status, title string) ([]*models.WikiPage, error) {
	rows, err := s.Pool.Query(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND ($3='' OR s.id::text=$3) AND p.status=$4 AND ($5='' OR p.title=$5) ORDER BY p.position,p.id`, ws, user, space, status, title)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.WikiPage{}
	for rows.Next() {
		p, err := scanWikiPage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func wikiAction(ctx context.Context, tx pgx.Tx, ws, actor, entity, id, spaceID string, value any) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": spaceID, entity: value})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: entity, EntityID: id, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func wikiCommentAction(ctx context.Context, tx pgx.Tx, ws, actor, entity string, comment *models.WikiFooterComment, op string) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": comment.SpaceID, entity: comment, "blogAuthorId": comment.ParentAuthorID, "blogPrivate": comment.ParentPrivate, "blogPublished": comment.ParentPublished})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: entity, EntityID: comment.ID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func (s *Store) CreateWikiSpace(ctx context.Context, ws, actor, key, name, description string, private bool) (*models.WikiSpace, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return nil, err
	}
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO wiki_spaces(workspace_id,key,name,description,author_id,private) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id::text`, ws, key, name, description, actor, private).Scan(&id); err != nil {
		return nil, err
	}
	space, err := scanWikiSpace(tx.QueryRow(ctx, wikiSpaceSelect+` WHERE s.id::text=$1`, id))
	if err != nil {
		return nil, err
	}
	if err := wikiAction(ctx, tx, ws, actor, "wiki_space", id, id, space); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return space, nil
}

func (s *Store) SetWikiSpaceDefaultClassification(ctx context.Context, ws, actor, id, levelID string) (*models.WikiSpace, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := wikiSpaceAdmin(ctx, tx, ws, actor, id); err != nil {
		return nil, err
	}
	space, err := scanWikiSpace(tx.QueryRow(ctx, wikiSpaceSelect+` WHERE s.workspace_id=$1 AND s.id::text=$2 FOR UPDATE`, ws, id))
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE wiki_spaces SET default_classification_level=$2 WHERE id::text=$1`, id, levelID); err != nil {
		return nil, err
	}
	space.DefaultClassificationLevel = levelID
	if err := wikiAction(ctx, tx, ws, actor, "wiki_space", id, id, space); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return space, nil
}

// SaveWikiPage serializes writes within a space, validates parent membership
// and cycles, then writes the page, immutable version and action atomically.
func (s *Store) SaveWikiPage(ctx context.Context, ws, actor string, input models.WikiPage) (*models.WikiPage, error) {
	isNew := input.ID == ""
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	spacePermission := wikiSpaceCanCreatePage
	if !isNew {
		spacePermission = wikiSpaceCanUpdatePage
		if input.Status == "trashed" {
			spacePermission = wikiSpaceCanDeletePage
		}
	}
	var spaceID, defaultClassification string
	err = tx.QueryRow(ctx, `SELECT s.id::text,s.default_classification_level FROM wiki_spaces s WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+spacePermission+` AND s.id::text=$3 FOR UPDATE`, ws, actor, input.SpaceID).Scan(&spaceID, &defaultClassification)
	if err != nil {
		return nil, err
	}
	if input.ID != "" {
		old, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+spacePermission+` AND `+wikiPageVisible+` AND `+wikiPageRestrictionWritable+` AND p.id::text=$3`, ws, actor, input.ID))
		if err != nil {
			return nil, err
		}
		if old.SpaceID != input.SpaceID {
			return nil, fmt.Errorf("%w: moving pages between spaces is not supported", ErrWikiValidation)
		}
		if input.Version.Number != old.Version.Number+1 {
			return nil, ErrWikiConflict
		}
		if input.Status == "draft" && old.Published {
			return nil, fmt.Errorf("%w: a published page cannot be converted to a draft", ErrWikiValidation)
		}
		if old.Status == "trashed" && input.Status == "current" {
			input.Title = old.Title
			input.Body = old.Body
			input.ParentID = old.ParentID
		}
		input.AuthorID = old.AuthorID
	} else {
		input.Version.Number = 1
		input.AuthorID = actor
		input.ClassificationLevel = defaultClassification
	}
	if input.ParentID != "" {
		var valid bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_pages WHERE id::text=$1 AND space_id::text=$2 AND status='current')`, input.ParentID, spaceID).Scan(&valid)
		if err != nil {
			return nil, err
		}
		if !valid {
			return nil, fmt.Errorf("%w: choose a published parent page from this space", ErrWikiValidation)
		}
		if input.ID != "" {
			var cycle bool
			err = tx.QueryRow(ctx, `WITH RECURSIVE ancestors AS (SELECT id,parent_id FROM wiki_pages WHERE id::text=$1 UNION SELECT p.id,p.parent_id FROM wiki_pages p JOIN ancestors a ON p.id=a.parent_id) SELECT EXISTS(SELECT 1 FROM ancestors WHERE id::text=$2)`, input.ParentID, input.ID).Scan(&cycle)
			if err != nil {
				return nil, err
			}
			if cycle {
				return nil, fmt.Errorf("%w: a page cannot be its own ancestor", ErrWikiValidation)
			}
		}
	}
	if input.Status == "trashed" {
		var children bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_pages WHERE parent_id::text=$1 AND status<>'trashed') OR EXISTS(SELECT 1 FROM wiki_content WHERE parent_page_id::text=$1 AND status='current')`, input.ID).Scan(&children); err != nil {
			return nil, err
		}
		if children {
			return nil, fmt.Errorf("%w: move or trash child content before deleting this page", ErrWikiValidation)
		}
	}
	if input.ID == "" {
		err = tx.QueryRow(ctx, `INSERT INTO wiki_pages(space_id,parent_id,title,status,body,author_id,published,classification_level) VALUES ($1::bigint,$2::bigint,$3,$4,$5,$6,$4='current',$7) RETURNING id::text`, spaceID, nilIfEmpty(input.ParentID), input.Title, input.Status, input.Body.Value, actor, input.ClassificationLevel).Scan(&input.ID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE wiki_pages SET parent_id=$2::bigint,title=$3,status=$4,body=$5,version=$6,published=(published OR $4='current') WHERE id::text=$1`, input.ID, nilIfEmpty(input.ParentID), input.Title, input.Status, input.Body.Value, input.Version.Number)
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO wiki_page_versions(page_id,version,title,body,status,author_id,message,minor_edit) VALUES ($1::bigint,$2,$3,$4,$5,$6,$7,$8)`, input.ID, input.Version.Number, input.Title, input.Body.Value, input.Status, actor, input.Version.Message, input.Version.MinorEdit)
	if err != nil {
		return nil, err
	}
	page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE p.id::text=$1`, input.ID))
	if err != nil {
		return nil, err
	}
	if err := wikiAction(ctx, tx, ws, actor, "wiki_page", page.ID, spaceID, page); err != nil {
		return nil, err
	}
	if page.Status == "current" && !page.Version.MinorEdit {
		if err := wikiWatchNotifications(ctx, tx, ws, actor, page, isNew); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return page, nil
}

func (s *Store) WikiVersions(ctx context.Context, ws, user, id string) ([]models.WikiVersion, error) {
	return s.WikiVersionsSorted(ctx, ws, user, id, "")
}

func (s *Store) WikiVersionsSorted(ctx context.Context, ws, user, id, order string) ([]models.WikiVersion, error) {
	if _, err := s.WikiPage(ctx, ws, user, id); err != nil {
		return nil, err
	}
	orderSQL := "created_at,version"
	if order == "-modified-date" {
		orderSQL = "created_at DESC,version DESC"
	} else if order != "" && order != "modified-date" {
		return nil, fmt.Errorf("%w: unsupported version sort order", ErrWikiValidation)
	}
	rows, err := s.Pool.Query(ctx, `SELECT version,message,minor_edit,author_id,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_page_versions WHERE page_id::text=$1 AND (status<>'draft' OR author_id=$2) ORDER BY `+orderSQL, id, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.WikiVersion{}
	for rows.Next() {
		var v models.WikiVersion
		if err := rows.Scan(&v.Number, &v.Message, &v.MinorEdit, &v.AuthorID, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) WikiPageVersion(ctx context.Context, ws, user, id string, version int) (*models.WikiVersion, error) {
	if _, err := s.WikiPage(ctx, ws, user, id); err != nil {
		return nil, err
	}
	value := &models.WikiVersion{}
	err := s.Pool.QueryRow(ctx, `SELECT version,message,minor_edit,author_id,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_page_versions WHERE page_id::text=$1 AND version=$2 AND (status<>'draft' OR author_id=$3)`, id, version, user).Scan(&value.Number, &value.Message, &value.MinorEdit, &value.AuthorID, &value.CreatedAt)
	return value, err
}

func (s *Store) WikiFooterComment(ctx context.Context, ws, user, id string) (*models.WikiFooterComment, error) {
	return scanWikiFooterComment(s.Pool.QueryRow(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='footer' AND c.id::text=$3`, ws, user, id))
}

func (s *Store) CanUpdateWikiComment(ctx context.Context, ws, user, id, commentType string) (bool, error) {
	return s.canWikiComment(ctx, ws, user, id, commentType, wikiSpaceCanUpdateComment)
}

func (s *Store) CanDeleteWikiComment(ctx context.Context, ws, user, id, commentType string) (bool, error) {
	return s.canWikiComment(ctx, ws, user, id, commentType, wikiSpaceCanDeleteComment)
}

func (s *Store) canWikiComment(ctx context.Context, ws, user, id, commentType, permission string) (bool, error) {
	var allowed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS(`+wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+permission+` AND `+wikiCommentVisible+` AND (c.author_id=$2 OR EXISTS (
		SELECT 1 FROM memberships cam WHERE cam.workspace_id=s.workspace_id AND cam.user_id=$2 AND cam.role='admin'
	)) AND c.comment_type=$4 AND c.id::text=$3)`, ws, user, id, commentType).Scan(&allowed)
	return allowed, err
}

// WikiFooterComments returns every visible footer comment when pageID is
// empty, and the top-level comments for a page otherwise.
func (s *Store) WikiFooterComments(ctx context.Context, ws, user, pageID string) ([]*models.WikiFooterComment, error) {
	rows, err := s.Pool.Query(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='footer' AND ($3='' OR (c.page_id::text=$3 AND c.parent_id IS NULL)) ORDER BY c.created_at,c.id`, ws, user, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiFooterComments(rows)
}

func (s *Store) WikiFooterCommentThread(ctx context.Context, ws, user, pageID string) ([]*models.WikiFooterComment, error) {
	if _, err := s.WikiPage(ctx, ws, user, pageID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='footer' AND c.page_id::text=$3 ORDER BY c.created_at,c.id`, ws, user, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiFooterComments(rows)
}

func (s *Store) WikiBlogFooterComments(ctx context.Context, ws, user, blogPostID string) ([]*models.WikiFooterComment, error) {
	if _, err := s.WikiBlogPost(ctx, ws, user, blogPostID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='footer' AND c.blog_post_id::text=$3 AND c.parent_id IS NULL ORDER BY c.created_at,c.id`, ws, user, blogPostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiFooterComments(rows)
}

func (s *Store) WikiBlogFooterCommentThread(ctx context.Context, ws, user, blogPostID string) ([]*models.WikiFooterComment, error) {
	if _, err := s.WikiBlogPost(ctx, ws, user, blogPostID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='footer' AND c.blog_post_id::text=$3 ORDER BY c.created_at,c.id`, ws, user, blogPostID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiFooterComments(rows)
}

func (s *Store) WikiAttachmentFooterComments(ctx context.Context, ws, user, attachmentID string) ([]*models.WikiFooterComment, error) {
	if _, err := s.WikiAttachment(ctx, ws, user, attachmentID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='footer' AND c.attachment_id::text=$3 AND c.parent_id IS NULL ORDER BY c.created_at,c.id`, ws, user, attachmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiFooterComments(rows)
}

func (s *Store) WikiAttachmentFooterCommentThread(ctx context.Context, ws, user, attachmentID string) ([]*models.WikiFooterComment, error) {
	if _, err := s.WikiAttachment(ctx, ws, user, attachmentID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='footer' AND c.attachment_id::text=$3 ORDER BY c.created_at,c.id`, ws, user, attachmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiFooterComments(rows)
}

func (s *Store) WikiFooterCommentChildren(ctx context.Context, ws, user, parentID string) ([]*models.WikiFooterComment, error) {
	if _, err := s.WikiFooterComment(ctx, ws, user, parentID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='footer' AND c.parent_id::text=$3 ORDER BY c.created_at,c.id`, ws, user, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWikiFooterComments(rows)
}

func scanWikiFooterComments(rows pgx.Rows) ([]*models.WikiFooterComment, error) {
	out := []*models.WikiFooterComment{}
	for rows.Next() {
		comment, err := scanWikiFooterComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, comment)
	}
	return out, rows.Err()
}

func (s *Store) CreateWikiFooterComment(ctx context.Context, ws, actor string, input models.WikiFooterComment) (*models.WikiFooterComment, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if input.ParentCommentID != "" {
		parent, err := scanWikiFooterComment(tx.QueryRow(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanCreateComment+` AND `+wikiCommentVisible+` AND c.comment_type='footer' AND c.id::text=$3 FOR SHARE OF c`, ws, actor, input.ParentCommentID))
		if err != nil {
			return nil, err
		}
		if parent.AttachmentID != "" {
			input.PageID = ""
			input.AttachmentID = parent.AttachmentID
		} else if parent.BlogPostID != "" {
			input.PageID = ""
			input.BlogPostID = parent.BlogPostID
		} else {
			input.PageID = parent.PageID
		}
	} else if input.AttachmentID != "" {
		_, err := scanWikiAttachment(tx.QueryRow(ctx, wikiAttachmentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanCreateComment+` AND `+wikiAttachmentVisible+` AND a.status='current' AND a.id::text=$3 FOR SHARE OF a`, ws, actor, input.AttachmentID))
		if err != nil {
			return nil, err
		}
	} else if input.BlogPostID != "" {
		var id string
		err := tx.QueryRow(ctx, `SELECT b.id::text FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanCreateComment+` AND `+wikiBlogPostVisible+` AND b.status='current' AND b.id::text=$3 FOR SHARE OF b`, ws, actor, input.BlogPostID).Scan(&id)
		if err != nil {
			return nil, err
		}
	} else {
		var id string
		err := tx.QueryRow(ctx, `SELECT p.id::text FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanCreateComment+` AND `+wikiPageVisible+` AND p.status='current' AND p.id::text=$3 FOR SHARE OF p`, ws, actor, input.PageID).Scan(&id)
		if err != nil {
			return nil, err
		}
	}
	input.Version.Number = 1
	input.AuthorID = actor
	if err := tx.QueryRow(ctx, `INSERT INTO wiki_footer_comments(page_id,attachment_id,blog_post_id,parent_id,body,author_id) VALUES ($1::bigint,$2::bigint,$3::bigint,$4::bigint,$5,$6) RETURNING id::text`, nilIfEmpty(input.PageID), nilIfEmpty(input.AttachmentID), nilIfEmpty(input.BlogPostID), nilIfEmpty(input.ParentCommentID), input.Body.Value, actor).Scan(&input.ID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO wiki_footer_comment_versions(comment_id,version,body,author_id,message) VALUES ($1::bigint,1,$2,$3,$4)`, input.ID, input.Body.Value, actor, input.Version.Message); err != nil {
		return nil, err
	}
	comment, err := scanWikiFooterComment(tx.QueryRow(ctx, wikiCommentSelect+` WHERE c.id::text=$1`, input.ID))
	if err != nil {
		return nil, err
	}
	if err := wikiCommentAction(ctx, tx, ws, actor, "wiki_footer_comment", comment, models.OpUpsert); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return comment, nil
}

func wikiCommentAuthorOrAdmin(ctx context.Context, tx pgx.Tx, ws, actor, author string) error {
	if actor == author {
		return nil
	}
	return projectAdmin(ctx, tx, ws, actor)
}

func (s *Store) UpdateWikiFooterComment(ctx context.Context, ws, actor string, input models.WikiFooterComment) (*models.WikiFooterComment, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	old, err := scanWikiFooterComment(tx.QueryRow(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanUpdateComment+` AND `+wikiCommentVisible+` AND c.comment_type='footer' AND c.id::text=$3 FOR UPDATE OF c`, ws, actor, input.ID))
	if err != nil {
		return nil, err
	}
	if err := wikiCommentAuthorOrAdmin(ctx, tx, ws, actor, old.AuthorID); err != nil {
		return nil, err
	}
	if input.Version.Number != old.Version.Number+1 {
		return nil, ErrWikiCommentConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE wiki_footer_comments SET body=$2,version=$3,updated_at=now() WHERE id::text=$1`, input.ID, input.Body.Value, input.Version.Number); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO wiki_footer_comment_versions(comment_id,version,body,author_id,message) VALUES ($1::bigint,$2,$3,$4,$5)`, input.ID, input.Version.Number, input.Body.Value, actor, input.Version.Message); err != nil {
		return nil, err
	}
	comment, err := scanWikiFooterComment(tx.QueryRow(ctx, wikiCommentSelect+` WHERE c.id::text=$1`, input.ID))
	if err != nil {
		return nil, err
	}
	if err := wikiCommentAction(ctx, tx, ws, actor, "wiki_footer_comment", comment, models.OpUpsert); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return comment, nil
}

func (s *Store) DeleteWikiFooterComment(ctx context.Context, ws, actor, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	comment, err := scanWikiFooterComment(tx.QueryRow(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanDeleteComment+` AND `+wikiCommentVisible+` AND c.comment_type='footer' AND c.id::text=$3 FOR UPDATE OF c`, ws, actor, id))
	if err != nil {
		return err
	}
	if err := wikiCommentAuthorOrAdmin(ctx, tx, ws, actor, comment.AuthorID); err != nil {
		return err
	}
	if err := wikiCommentAction(ctx, tx, ws, actor, "wiki_footer_comment", comment, models.OpDelete); err != nil {
		return err
	}
	if tag, err := tx.Exec(ctx, `DELETE FROM wiki_footer_comments WHERE id::text=$1`, id); err != nil {
		return err
	} else if tag.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	return tx.Commit(ctx)
}

func (s *Store) WikiFooterCommentVersions(ctx context.Context, ws, user, id string) ([]models.WikiFooterCommentVersion, error) {
	if _, err := s.WikiFooterComment(ctx, ws, user, id); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT version,message,author_id,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),body FROM wiki_footer_comment_versions WHERE comment_id::text=$1 ORDER BY version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := []models.WikiFooterCommentVersion{}
	for rows.Next() {
		version := models.WikiFooterCommentVersion{Body: models.WikiBody{Representation: "storage"}}
		if err := rows.Scan(&version.Number, &version.Message, &version.AuthorID, &version.CreatedAt, &version.Body.Value); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (s *Store) WikiFooterCommentVersion(ctx context.Context, ws, user, id string, number int) (*models.WikiFooterCommentVersion, error) {
	if _, err := s.WikiFooterComment(ctx, ws, user, id); err != nil {
		return nil, err
	}
	version := &models.WikiFooterCommentVersion{Body: models.WikiBody{Representation: "storage"}}
	err := s.Pool.QueryRow(ctx, `SELECT version,message,author_id,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),body FROM wiki_footer_comment_versions WHERE comment_id::text=$1 AND version=$2`, id, number).Scan(&version.Number, &version.Message, &version.AuthorID, &version.CreatedAt, &version.Body.Value)
	return version, err
}

func (s *Store) WikiFooterCommentLikes(ctx context.Context, ws, user, id string) ([]string, error) {
	if _, err := s.WikiFooterComment(ctx, ws, user, id); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT l.user_id FROM wiki_footer_comment_likes l JOIN users u ON u.id=l.user_id AND u.active WHERE l.comment_id::text=$1 ORDER BY l.created_at,l.user_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []string{}
	for rows.Next() {
		var accountID string
		if err := rows.Scan(&accountID); err != nil {
			return nil, err
		}
		users = append(users, accountID)
	}
	return users, rows.Err()
}

func (s *Store) WikiFooterCommentLikesForPage(ctx context.Context, ws, user, pageID string) (map[string][]string, error) {
	if _, err := s.WikiPage(ctx, ws, user, pageID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT l.comment_id::text,l.user_id FROM wiki_footer_comment_likes l JOIN wiki_footer_comments c ON c.id=l.comment_id JOIN users u ON u.id=l.user_id AND u.active WHERE c.page_id::text=$1 ORDER BY l.created_at,l.user_id`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	likes := map[string][]string{}
	for rows.Next() {
		var commentID, accountID string
		if err := rows.Scan(&commentID, &accountID); err != nil {
			return nil, err
		}
		likes[commentID] = append(likes[commentID], accountID)
	}
	return likes, rows.Err()
}

func (s *Store) WikiFooterCommentLikesForAttachment(ctx context.Context, ws, user, attachmentID string) (map[string][]string, error) {
	if _, err := s.WikiAttachment(ctx, ws, user, attachmentID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT l.comment_id::text,l.user_id FROM wiki_footer_comment_likes l JOIN wiki_footer_comments c ON c.id=l.comment_id JOIN users u ON u.id=l.user_id AND u.active WHERE c.attachment_id::text=$1 ORDER BY l.created_at,l.user_id`, attachmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	likes := map[string][]string{}
	for rows.Next() {
		var commentID, accountID string
		if err := rows.Scan(&commentID, &accountID); err != nil {
			return nil, err
		}
		likes[commentID] = append(likes[commentID], accountID)
	}
	return likes, rows.Err()
}

func (s *Store) WikiFooterCommentVersionsForPage(ctx context.Context, ws, user, pageID string) (map[string][]models.WikiFooterCommentVersion, error) {
	if _, err := s.WikiPage(ctx, ws, user, pageID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT v.comment_id::text,v.version,v.message,v.author_id,to_char(v.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),v.body FROM wiki_footer_comment_versions v JOIN wiki_footer_comments c ON c.id=v.comment_id WHERE c.page_id::text=$1 ORDER BY v.comment_id,v.version`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := map[string][]models.WikiFooterCommentVersion{}
	for rows.Next() {
		var commentID string
		version := models.WikiFooterCommentVersion{Body: models.WikiBody{Representation: "storage"}}
		if err := rows.Scan(&commentID, &version.Number, &version.Message, &version.AuthorID, &version.CreatedAt, &version.Body.Value); err != nil {
			return nil, err
		}
		versions[commentID] = append(versions[commentID], version)
	}
	return versions, rows.Err()
}

func (s *Store) WikiFooterCommentVersionsForAttachment(ctx context.Context, ws, user, attachmentID string) (map[string][]models.WikiFooterCommentVersion, error) {
	if _, err := s.WikiAttachment(ctx, ws, user, attachmentID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT v.comment_id::text,v.version,v.message,v.author_id,to_char(v.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),v.body FROM wiki_footer_comment_versions v JOIN wiki_footer_comments c ON c.id=v.comment_id WHERE c.attachment_id::text=$1 ORDER BY v.comment_id,v.version`, attachmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := map[string][]models.WikiFooterCommentVersion{}
	for rows.Next() {
		var commentID string
		version := models.WikiFooterCommentVersion{Body: models.WikiBody{Representation: "storage"}}
		if err := rows.Scan(&commentID, &version.Number, &version.Message, &version.AuthorID, &version.CreatedAt, &version.Body.Value); err != nil {
			return nil, err
		}
		versions[commentID] = append(versions[commentID], version)
	}
	return versions, rows.Err()
}

func (s *Store) SetWikiFooterCommentLike(ctx context.Context, ws, actor, id string, liked bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	comment, err := scanWikiFooterComment(tx.QueryRow(ctx, wikiCommentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiCommentVisible+` AND c.comment_type='footer' AND c.id::text=$3 FOR SHARE OF c`, ws, actor, id))
	if err != nil {
		return err
	}
	var changed int64
	if liked {
		tag, execErr := tx.Exec(ctx, `INSERT INTO wiki_footer_comment_likes(comment_id,user_id) VALUES ($1::bigint,$2) ON CONFLICT DO NOTHING`, id, actor)
		err, changed = execErr, tag.RowsAffected()
	} else {
		tag, execErr := tx.Exec(ctx, `DELETE FROM wiki_footer_comment_likes WHERE comment_id::text=$1 AND user_id=$2`, id, actor)
		err, changed = execErr, tag.RowsAffected()
	}
	if err != nil {
		return err
	}
	if changed == 0 {
		return tx.Commit(ctx)
	}
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": comment.SpaceID, "wiki_footer_comment_like": map[string]any{"pageId": comment.PageID, "blogPostId": comment.BlogPostID, "commentId": id, "userId": actor, "liked": liked}, "blogAuthorId": comment.ParentAuthorID, "blogPrivate": comment.ParentPrivate, "blogPublished": comment.ParentPublished})
	if err != nil {
		return err
	}
	if err := appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: "wiki_footer_comment_like", EntityID: id + ":" + actor, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
