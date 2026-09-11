package store

import (
	"context"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// Custom content is content an app defines: it lives in a space and may hang
// off a page, a blog post or other custom content. It is stored with every
// other kind of space content rather than in a table of its own, so the
// hierarchy, permissions, versions and properties are the ones that already
// govern a space.

// WikiCustomContentQuery narrows a custom content listing.
type WikiCustomContentQuery struct {
	SpaceIDs   []string
	IDs        []string
	CustomType string
	Status     string
	Sort       string
}

var customContentSorts = map[string]string{
	"": "c.id", "id": "c.id", "-id": "c.id DESC",
	"created-date": "c.created_at,c.id", "-created-date": "c.created_at DESC,c.id DESC",
	"modified-date": "c.updated_at,c.id", "-modified-date": "c.updated_at DESC,c.id DESC",
	"title": "c.title,c.id", "-title": "c.title DESC,c.id DESC",
}

// WikiCustomContentTypeRepresentation reports the body representation a custom
// content type declares, and whether the type is registered at all.
func (s *Store) WikiCustomContentTypeRepresentation(ctx context.Context, customType string) (string, error) {
	var representation string
	err := s.Pool.QueryRow(ctx, `SELECT body_representation FROM wiki_custom_content_types WHERE type=$1`,
		customType).Scan(&representation)
	return representation, err
}

// WikiCustomContents lists custom content the user may read.
func (s *Store) WikiCustomContents(ctx context.Context, ws, user string, query WikiCustomContentQuery) ([]*models.WikiContent, error) {
	order, ok := customContentSorts[query.Sort]
	if !ok {
		return nil, fmt.Errorf("%w: unsupported custom content sort order", ErrWikiValidation)
	}
	status := query.Status
	if status == "" {
		status = "current"
	}
	if status != "current" && status != "archived" && status != "trashed" {
		return nil, fmt.Errorf("%w: status must be current, archived or trashed", ErrWikiValidation)
	}
	rows, err := s.Pool.Query(ctx, wikiContentSelect+` WHERE s.workspace_id=$1 AND `+wikiContentVisibleFor("custom")+`
		AND c.type='custom' AND c.status=$3
		AND ($4::text[] IS NULL OR c.space_id::text = ANY($4))
		AND ($5::text[] IS NULL OR c.id::text = ANY($5))
		AND ($6='' OR c.custom_type=$6)
		ORDER BY `+order,
		ws, user, status, nullableIDs(query.SpaceIDs), nullableIDs(query.IDs), query.CustomType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []*models.WikiContent{}
	for rows.Next() {
		content, scanErr := scanWikiContent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, content)
	}
	return values, rows.Err()
}

// CreateWikiCustomContent adds custom content of a registered type. The parent
// may be a page, a blog post, other custom content, or nothing at all, which
// is what places it directly in the space.
func (s *Store) CreateWikiCustomContent(ctx context.Context, ws, actor string, input models.WikiContent) (*models.WikiContent, error) {
	if input.CustomType == "" {
		return nil, fmt.Errorf("%w: a custom content type is required", ErrWikiValidation)
	}
	if input.Title == "" || len(input.Title) > 255 {
		return nil, fmt.Errorf("%w: a title of 1 to 255 characters is required", ErrWikiValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var registered bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_custom_content_types WHERE type=$1)`,
		input.CustomType).Scan(&registered); err != nil {
		return nil, err
	}
	if !registered {
		return nil, pgx.ErrNoRows
	}
	createPermission := wikiSpacePermissionAllowed("create/custom")
	var spaceID string
	parentPage, parentContent, parentBlog, rootPage, err := resolveCustomContentParent(ctx, tx, ws, actor, input, &spaceID)
	if err != nil {
		return nil, err
	}
	if spaceID == "" {
		if err = tx.QueryRow(ctx, `SELECT s.id::text FROM wiki_spaces s
			WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+createPermission+` AND s.id::text=$3 FOR SHARE OF s`,
			ws, actor, input.SpaceID).Scan(&spaceID); err != nil {
			return nil, err
		}
	}
	if err = tx.QueryRow(ctx, `INSERT INTO wiki_content(space_id,parent_page_id,parent_content_id,parent_blog_post_id,root_page_id,type,custom_type,title,body,author_id,owner_id)
		VALUES($1::bigint,$2::bigint,$3::bigint,$4::bigint,$5::bigint,'custom',$6,$7,$8,$9,$9) RETURNING id::text`,
		spaceID, parentPage, parentContent, parentBlog, rootPage, input.CustomType, input.Title, input.Body, actor).Scan(&input.ID); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_content_versions(content_id,version,title,status,author_id,body,message)
		VALUES($1::bigint,1,$2,'current',$3,$4,$5)`, input.ID, input.Title, actor, input.Body, input.Version.Message); err != nil {
		return nil, err
	}
	content, err := scanWikiContent(tx.QueryRow(ctx, wikiContentSelect+` WHERE c.id::text=$1`, input.ID))
	if err != nil {
		return nil, err
	}
	if err = wikiContentAction(ctx, tx, ws, actor, content, models.OpUpsert); err != nil {
		return nil, err
	}
	return content, tx.Commit(ctx)
}

// resolveCustomContentParent finds which of the three parent kinds was named,
// and the space that parent belongs to. Confluence takes the space from the
// parent when there is one, so a caller cannot file content under a page in one
// space while claiming it belongs to another.
func resolveCustomContentParent(ctx context.Context, tx pgx.Tx, ws, actor string, input models.WikiContent, spaceID *string) (parentPage, parentContent, parentBlog, rootPage any, err error) {
	if input.ParentID == "" {
		return nil, nil, nil, nil, nil
	}
	createPermission := wikiSpacePermissionAllowed("create/custom")
	switch input.ParentType {
	case "page", "":
		var pageID, pageSpace string
		pageErr := tx.QueryRow(ctx, `SELECT p.id::text,p.space_id::text FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
			WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+createPermission+` AND `+wikiPageVisible+`
			AND `+wikiPageRestrictionWritable+` AND p.id::text=$3 AND p.status='current' FOR SHARE OF p`,
			ws, actor, input.ParentID).Scan(&pageID, &pageSpace)
		if pageErr == nil {
			*spaceID = pageSpace
			return pageID, nil, nil, pageID, nil
		}
		if pageErr != pgx.ErrNoRows {
			return nil, nil, nil, nil, pageErr
		}
		if input.ParentType == "page" {
			return nil, nil, nil, nil, fmt.Errorf("%w: choose a visible page as the parent", ErrWikiValidation)
		}
		fallthrough
	case "blogpost":
		var blogID, blogSpace string
		blogErr := tx.QueryRow(ctx, `SELECT b.id::text,b.space_id::text FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id
			WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+createPermission+`
			AND b.id::text=$3 AND b.status='current' FOR SHARE OF b`, ws, actor, input.ParentID).Scan(&blogID, &blogSpace)
		if blogErr == nil {
			*spaceID = blogSpace
			return nil, nil, blogID, nil, nil
		}
		if blogErr != pgx.ErrNoRows {
			return nil, nil, nil, nil, blogErr
		}
		if input.ParentType == "blogpost" {
			return nil, nil, nil, nil, fmt.Errorf("%w: choose a visible blog post as the parent", ErrWikiValidation)
		}
		fallthrough
	default:
		var contentID, contentSpace, contentRoot string
		contentErr := tx.QueryRow(ctx, `SELECT c.id::text,c.space_id::text,COALESCE(c.root_page_id::text,'')
			FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id LEFT JOIN wiki_pages p ON p.id=c.root_page_id
			WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+createPermission+` AND `+wikiContentRestrictionWritable+`
			AND c.id::text=$3 AND c.status='current' FOR SHARE OF c`, ws, actor, input.ParentID).Scan(&contentID, &contentSpace, &contentRoot)
		if contentErr != nil {
			return nil, nil, nil, nil, fmt.Errorf("%w: choose a visible page, blog post or custom content as the parent", ErrWikiValidation)
		}
		*spaceID = contentSpace
		if contentRoot != "" {
			rootPage = contentRoot
		}
		return nil, contentID, nil, rootPage, nil
	}
}

// UpdateWikiCustomContent replaces the title and body, recording a version.
// Confluence requires the caller to state the version it is replacing, which is
// what stops two writers silently overwriting one another.
func (s *Store) UpdateWikiCustomContent(ctx context.Context, ws, actor, id string, input models.WikiContent, expectedVersion int) (*models.WikiContent, error) {
	if input.Title == "" || len(input.Title) > 255 {
		return nil, fmt.Errorf("%w: a title of 1 to 255 characters is required", ErrWikiValidation)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The permission is part of the lookup, so a caller who may not write sees
	// the same answer as one asking about content that is not there.
	var current int
	var customType string
	if err = tx.QueryRow(ctx, `SELECT c.version, c.custom_type FROM wiki_content c
		JOIN wiki_spaces s ON s.id=c.space_id LEFT JOIN wiki_pages p ON p.id=c.root_page_id
		WHERE s.workspace_id=$1 AND `+wikiContentWritableFor("custom")+`
		AND c.id::text=$3 AND c.type='custom' AND c.status='current' FOR UPDATE OF c`,
		ws, actor, id).Scan(&current, &customType); err != nil {
		return nil, err
	}
	if expectedVersion != current+1 {
		return nil, fmt.Errorf("%w: the next version is %d", ErrWikiContentConflict, current+1)
	}
	if input.CustomType != "" && input.CustomType != customType {
		return nil, fmt.Errorf("%w: the custom content type cannot be changed", ErrWikiValidation)
	}
	if _, err = tx.Exec(ctx, `UPDATE wiki_content SET title=$2,body=$3,version=$4,updated_at=now() WHERE id::text=$1`,
		id, input.Title, input.Body, expectedVersion); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_content_versions(content_id,version,title,status,author_id,body,message)
		VALUES($1::bigint,$2,$3,'current',$4,$5,$6)`, id, expectedVersion, input.Title, actor, input.Body, input.Version.Message); err != nil {
		return nil, err
	}
	content, err := scanWikiContent(tx.QueryRow(ctx, wikiContentSelect+` WHERE c.id::text=$1`, id))
	if err != nil {
		return nil, err
	}
	if err = wikiContentAction(ctx, tx, ws, actor, content, models.OpUpsert); err != nil {
		return nil, err
	}
	return content, tx.Commit(ctx)
}

// WikiCustomContentVersions lists a piece of custom content's versions. Page
// versions live in their own table, so content cannot borrow that read.
func (s *Store) WikiCustomContentVersions(ctx context.Context, ws, actor, id, order string) ([]models.WikiVersion, error) {
	if _, err := s.WikiContent(ctx, ws, actor, id, "custom"); err != nil {
		return nil, err
	}
	orderSQL := "version"
	switch order {
	case "", "modified-date":
	case "-modified-date":
		orderSQL = "version DESC"
	default:
		return nil, fmt.Errorf("%w: unsupported version sort order", ErrWikiValidation)
	}
	rows, err := s.Pool.Query(ctx, `SELECT version,message,author_id,
		to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM wiki_content_versions WHERE content_id::text=$1 ORDER BY `+orderSQL, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := []models.WikiVersion{}
	for rows.Next() {
		var version models.WikiVersion
		if err = rows.Scan(&version.Number, &version.Message, &version.AuthorID, &version.CreatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

// WikiCustomContentVersion reads one earlier version, body included.
func (s *Store) WikiCustomContentVersion(ctx context.Context, ws, actor, id string, number int) (models.WikiVersion, string, error) {
	if _, err := s.WikiContent(ctx, ws, actor, id, "custom"); err != nil {
		return models.WikiVersion{}, "", err
	}
	var version models.WikiVersion
	var body string
	err := s.Pool.QueryRow(ctx, `SELECT version,message,author_id,
		to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),body
		FROM wiki_content_versions WHERE content_id::text=$1 AND version=$2`, id, number).
		Scan(&version.Number, &version.Message, &version.AuthorID, &version.CreatedAt, &body)
	return version, body, err
}

// WikiContentLabels lists the labels on one piece of content.
func (s *Store) WikiContentLabels(ctx context.Context, ws, actor, id string) ([]models.WikiLabel, error) {
	if _, err := s.WikiContent(ctx, ws, actor, id, "custom"); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT l.id::text,l.name,l.prefix FROM wiki_content_labels cl
		JOIN wiki_labels l ON l.id=cl.label_id WHERE cl.content_id::text=$1 ORDER BY l.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	labels := []models.WikiLabel{}
	for rows.Next() {
		var label models.WikiLabel
		if err = rows.Scan(&label.ID, &label.Name, &label.Prefix); err != nil {
			return nil, err
		}
		labels = append(labels, label)
	}
	return labels, rows.Err()
}
