package store

import (
	"context"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// WikiAttachmentMetadataInput is the non-binary data of an attachment that can
// change: its name, media type and comment, and the page or blog post it
// belongs to.
type WikiAttachmentMetadataInput struct {
	Filename, MediaType, Comment, Message string
	MinorEdit                             bool
	Version                               int
	// ContainerID and ContainerType move the attachment to another page or
	// blog post when they name one.
	ContainerID, ContainerType string
}

// UpdateWikiAttachmentMetadata changes an attachment's metadata as a new
// version holding the same file. Moving it takes permission to add
// attachments to where it goes.
func (s *Store) UpdateWikiAttachmentMetadata(ctx context.Context, ws, actor, containerID, id string, input WikiAttachmentMetadataInput) (*models.WikiAttachment, error) {
	canUpdate, err := s.CanUpdateWikiAttachment(ctx, ws, actor, id)
	if err != nil {
		return nil, err
	}
	if !canUpdate {
		return nil, ErrProjectPermission
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	old, err := scanWikiAttachment(tx.QueryRow(ctx, wikiAttachmentSelect+` WHERE a.id::text=$1 AND (a.page_id::text=$2 OR a.blog_post_id::text=$2) FOR UPDATE OF a`, id, containerID))
	if err != nil {
		return nil, err
	}
	if old.Status != "current" {
		return nil, fmt.Errorf("%w: only a current attachment can be changed", ErrWikiValidation)
	}
	if input.Version != old.Version.Number+1 {
		return nil, ErrWikiConflict
	}
	filename, mediaType, comment := old.Filename, old.MediaType, old.Comment
	if input.Filename != "" {
		filename = input.Filename
	}
	if input.MediaType != "" {
		mediaType = input.MediaType
	}
	if input.Comment != "" {
		comment = input.Comment
	}
	pageID, blogPostID := old.PageID, old.BlogPostID
	if input.ContainerID != "" && input.ContainerID != old.PageID && input.ContainerID != old.BlogPostID {
		var targetSpace string
		switch input.ContainerType {
		case "page":
			err = tx.QueryRow(ctx, `SELECT p.space_id::text FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanCreateAttachment+` AND `+wikiPageVisible+` AND `+wikiPageRestrictionWritable+` AND p.id::text=$3 AND p.status='current' FOR SHARE OF p`, ws, actor, input.ContainerID).Scan(&targetSpace)
			pageID, blogPostID = input.ContainerID, ""
		case "blogpost":
			err = tx.QueryRow(ctx, `SELECT b.space_id::text FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanCreateAttachment+` AND `+wikiBlogPostVisible+` AND `+wikiBlogPostAuthorWritable+` AND b.id::text=$3 AND b.status='current' FOR SHARE OF b`, ws, actor, input.ContainerID).Scan(&targetSpace)
			pageID, blogPostID = "", input.ContainerID
		default:
			return nil, fmt.Errorf("%w: an attachment's container is a page or a blog post", ErrWikiValidation)
		}
		if err != nil {
			return nil, err
		}
	}
	version := input.Version
	_, err = tx.Exec(ctx, `UPDATE wiki_attachments SET filename=$2,media_type=$3,comment=$4,version=$5,
		page_id=NULLIF($6,'')::bigint,blog_post_id=NULLIF($7,'')::bigint,updated_at=now() WHERE id::text=$1`,
		id, filename, mediaType, comment, version, pageID, blogPostID)
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: the destination already has an attachment with that name", ErrWikiValidation)
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_attachment_versions(attachment_id,version,filename,media_type,comment,size,blob_ref,author_id,message,minor_edit)
		SELECT a.id,$2,$3,$4,$5,a.size,v.blob_ref,$6,$7,$8 FROM wiki_attachments a JOIN wiki_attachment_versions v ON v.attachment_id=a.id AND v.version=$2-1 WHERE a.id::text=$1`,
		id, version, filename, mediaType, comment, actor, input.Message, input.MinorEdit); err != nil {
		return nil, err
	}
	a, err := scanWikiAttachment(tx.QueryRow(ctx, wikiAttachmentSelect+` WHERE a.id::text=$1`, id))
	if err != nil {
		return nil, err
	}
	if err = wikiAttachmentAction(ctx, tx, ws, actor, a, models.OpUpsert); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

// WikiBlogAttachmentByFilename finds a blog post's current attachment by name.
func (s *Store) WikiBlogAttachmentByFilename(ctx context.Context, ws, actor, blogPostID, filename string) (*models.WikiAttachment, error) {
	attachments, err := s.WikiBlogAttachments(ctx, ws, actor, blogPostID, "", filename, "current")
	if err != nil {
		return nil, err
	}
	for _, a := range attachments {
		if a.Filename == filename {
			return a, nil
		}
	}
	return nil, pgx.ErrNoRows
}
