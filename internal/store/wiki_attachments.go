package store

import (
	"context"
	"encoding/json"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

const wikiAttachmentSelect = `SELECT a.id::text,COALESCE(a.page_id::text,''),COALESCE(a.blog_post_id::text,''),COALESCE(p.space_id,b.space_id)::text,a.file_id,a.filename,a.media_type,a.comment,a.size,a.status,a.author_id,to_char(a.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),a.version,v.message,v.minor_edit,v.author_id,to_char(v.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),COALESCE(b.author_id,''),COALESCE(b.private,false),COALESCE(b.published,false) FROM wiki_attachments a LEFT JOIN wiki_pages p ON p.id=a.page_id LEFT JOIN wiki_blog_posts b ON b.id=a.blog_post_id JOIN wiki_spaces s ON s.id=COALESCE(p.space_id,b.space_id) JOIN wiki_attachment_versions v ON v.attachment_id=a.id AND v.version=a.version`

const wikiAttachmentVisible = `((a.page_id IS NOT NULL AND ` + wikiPageVisible + `) OR (a.blog_post_id IS NOT NULL AND ` + wikiBlogPostVisible + `))`
const wikiAttachmentWritable = `((a.page_id IS NOT NULL AND ` + wikiPageWritable + `) OR (a.blog_post_id IS NOT NULL AND ` + wikiBlogPostWritable + `))`

func scanWikiAttachment(row pgx.Row) (*models.WikiAttachment, error) {
	a := &models.WikiAttachment{}
	err := row.Scan(&a.ID, &a.PageID, &a.BlogPostID, &a.SpaceID, &a.FileID, &a.Filename, &a.MediaType, &a.Comment, &a.Size, &a.Status, &a.AuthorID, &a.CreatedAt, &a.Version.Number, &a.Version.Message, &a.Version.MinorEdit, &a.Version.AuthorID, &a.Version.CreatedAt, &a.ParentAuthorID, &a.ParentPrivate, &a.ParentPublished)
	return a, err
}

func wikiAttachmentAction(ctx context.Context, tx pgx.Tx, ws, actor string, a *models.WikiAttachment, op string) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": a.SpaceID, "wiki_attachment": a, "blogAuthorId": a.ParentAuthorID, "blogPrivate": a.ParentPrivate, "blogPublished": a.ParentPublished})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: "wiki_attachment", EntityID: a.ID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func (s *Store) WikiAttachment(ctx context.Context, ws, user, id string) (*models.WikiAttachment, error) {
	return scanWikiAttachment(s.Pool.QueryRow(ctx, wikiAttachmentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiAttachmentVisible+` AND a.id::text=$3`, ws, user, id))
}

func (s *Store) WikiAttachmentByFilename(ctx context.Context, ws, user, pageID, filename string) (*models.WikiAttachment, error) {
	return scanWikiAttachment(s.Pool.QueryRow(ctx, wikiAttachmentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND a.page_id::text=$3 AND a.filename=$4`, ws, user, pageID, filename))
}

func (s *Store) WikiAttachments(ctx context.Context, ws, user, pageID, mediaType, filename, status string) ([]*models.WikiAttachment, error) {
	rows, err := s.Pool.Query(ctx, wikiAttachmentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiAttachmentVisible+` AND ($3='' OR a.page_id::text=$3) AND ($4='' OR a.media_type=$4) AND ($5='' OR a.filename=$5) AND ($6='' OR a.status=$6) ORDER BY a.id`, ws, user, pageID, mediaType, filename, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.WikiAttachment{}
	for rows.Next() {
		a, err := scanWikiAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) WikiBlogAttachments(ctx context.Context, ws, user, blogPostID, mediaType, filename, status string) ([]*models.WikiAttachment, error) {
	blog, err := s.WikiBlogPost(ctx, ws, user, blogPostID)
	if err != nil {
		return nil, err
	}
	if blog.Status != "current" {
		return nil, pgx.ErrNoRows
	}
	rows, err := s.Pool.Query(ctx, wikiAttachmentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiBlogPostVisible+` AND a.blog_post_id::text=$3 AND ($4='' OR a.media_type=$4) AND ($5='' OR a.filename=$5) AND ($6='' OR a.status=$6) ORDER BY a.id`, ws, user, blogPostID, mediaType, filename, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*models.WikiAttachment{}
	for rows.Next() {
		a, err := scanWikiAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) SaveWikiAttachment(ctx context.Context, ws, actor, pageID, attachmentID, filename, mediaType, comment, message string, minor bool, size int64, blobRef string) (*models.WikiAttachment, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var spaceID string
	if err = tx.QueryRow(ctx, `SELECT p.space_id::text FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND p.id::text=$3 AND p.status='current' FOR SHARE OF p`, ws, actor, pageID).Scan(&spaceID); err != nil {
		return nil, err
	}
	version := 1
	if attachmentID == "" {
		err = tx.QueryRow(ctx, `INSERT INTO wiki_attachments(page_id,file_id,filename,media_type,comment,size,author_id) VALUES($1::bigint,$2,$3,$4,$5,$6,$7) RETURNING id::text`, pageID, NewID("file"), filename, mediaType, comment, size, actor).Scan(&attachmentID)
	} else {
		if err = tx.QueryRow(ctx, `SELECT version+1 FROM wiki_attachments WHERE id::text=$1 AND page_id::text=$2 FOR UPDATE`, attachmentID, pageID).Scan(&version); err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `UPDATE wiki_attachments SET filename=$2,media_type=$3,comment=$4,size=$5,version=$6,updated_at=now() WHERE id::text=$1`, attachmentID, filename, mediaType, comment, size, version)
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_attachment_versions(attachment_id,version,filename,media_type,comment,size,blob_ref,author_id,message,minor_edit) VALUES($1::bigint,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, attachmentID, version, filename, mediaType, comment, size, blobRef, actor, message, minor); err != nil {
		return nil, err
	}
	a, err := scanWikiAttachment(tx.QueryRow(ctx, wikiAttachmentSelect+` WHERE a.id::text=$1`, attachmentID))
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

func (s *Store) SaveWikiBlogAttachment(ctx context.Context, ws, actor, blogPostID, attachmentID, filename, mediaType, comment, message string, minor bool, size int64, blobRef string) (*models.WikiAttachment, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var spaceID string
	if err = tx.QueryRow(ctx, `SELECT b.space_id::text FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiBlogPostVisible+` AND `+wikiBlogPostWritable+` AND b.id::text=$3 AND b.status='current' FOR SHARE OF b`, ws, actor, blogPostID).Scan(&spaceID); err != nil {
		return nil, err
	}
	version := 1
	if attachmentID == "" {
		err = tx.QueryRow(ctx, `INSERT INTO wiki_attachments(blog_post_id,file_id,filename,media_type,comment,size,author_id) VALUES($1::bigint,$2,$3,$4,$5,$6,$7) RETURNING id::text`, blogPostID, NewID("file"), filename, mediaType, comment, size, actor).Scan(&attachmentID)
	} else {
		if err = tx.QueryRow(ctx, `SELECT version+1 FROM wiki_attachments WHERE id::text=$1 AND blog_post_id::text=$2 FOR UPDATE`, attachmentID, blogPostID).Scan(&version); err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `UPDATE wiki_attachments SET filename=$2,media_type=$3,comment=$4,size=$5,version=$6,updated_at=now() WHERE id::text=$1`, attachmentID, filename, mediaType, comment, size, version)
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wiki_attachment_versions(attachment_id,version,filename,media_type,comment,size,blob_ref,author_id,message,minor_edit) VALUES($1::bigint,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, attachmentID, version, filename, mediaType, comment, size, blobRef, actor, message, minor); err != nil {
		return nil, err
	}
	a, err := scanWikiAttachment(tx.QueryRow(ctx, wikiAttachmentSelect+` WHERE a.id::text=$1`, attachmentID))
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

func (s *Store) UpdateWikiAttachmentProperties(ctx context.Context, ws, actor, pageID, id, filename, mediaType, comment, message string, minor bool, version int) (*models.WikiAttachment, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	old, err := scanWikiAttachment(tx.QueryRow(ctx, wikiAttachmentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND a.page_id::text=$3 AND a.id::text=$4 FOR UPDATE OF a`, ws, actor, pageID, id))
	if err != nil {
		return nil, err
	}
	if version != old.Version.Number+1 {
		return nil, ErrWikiConflict
	}
	if filename == "" {
		filename = old.Filename
	}
	if mediaType == "" {
		mediaType = old.MediaType
	}
	if comment == "" {
		comment = old.Comment
	}
	_, err = tx.Exec(ctx, `UPDATE wiki_attachments SET filename=$2,media_type=$3,comment=$4,version=$5,updated_at=now() WHERE id::text=$1`, id, filename, mediaType, comment, version)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO wiki_attachment_versions(attachment_id,version,filename,media_type,comment,size,blob_ref,author_id,message,minor_edit) SELECT a.id,$2,$3,$4,$5,a.size,v.blob_ref,$6,$7,$8 FROM wiki_attachments a JOIN wiki_attachment_versions v ON v.attachment_id=a.id AND v.version=$2-1 WHERE a.id::text=$1`, id, version, filename, mediaType, comment, actor, message, minor)
	if err != nil {
		return nil, err
	}
	a, err := scanWikiAttachment(tx.QueryRow(ctx, wikiAttachmentSelect+` WHERE a.id::text=$1`, id))
	if err != nil {
		return nil, err
	}
	var spaceID string
	if err = tx.QueryRow(ctx, `SELECT space_id::text FROM wiki_pages WHERE id::text=$1`, old.PageID).Scan(&spaceID); err != nil {
		return nil, err
	}
	if err = wikiAction(ctx, tx, ws, actor, "wiki_attachment", id, spaceID, a); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Store) WikiAttachmentVersions(ctx context.Context, ws, user, id string) ([]models.WikiAttachmentVersion, error) {
	if _, err := s.WikiAttachment(ctx, ws, user, id); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT version,message,minor_edit,author_id,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),filename,media_type,comment,size FROM wiki_attachment_versions WHERE attachment_id::text=$1 ORDER BY version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.WikiAttachmentVersion{}
	for rows.Next() {
		var v models.WikiAttachmentVersion
		if err := rows.Scan(&v.Number, &v.Message, &v.MinorEdit, &v.AuthorID, &v.CreatedAt, &v.Filename, &v.MediaType, &v.Comment, &v.Size); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) WikiAttachmentVersion(ctx context.Context, ws, user, id string, version int) (*models.WikiAttachmentVersion, error) {
	vs, err := s.WikiAttachmentVersions(ctx, ws, user, id)
	if err != nil {
		return nil, err
	}
	for i := range vs {
		if vs[i].Number == version {
			return &vs[i], nil
		}
	}
	return nil, pgx.ErrNoRows
}

func (s *Store) WikiAttachmentBlob(ctx context.Context, ws, user, id string, version int) (string, string, string, error) {
	a, err := s.WikiAttachment(ctx, ws, user, id)
	if err != nil {
		return "", "", "", err
	}
	if version == 0 {
		version = a.Version.Number
	}
	var ref, name, mime string
	err = s.Pool.QueryRow(ctx, `SELECT blob_ref,filename,media_type FROM wiki_attachment_versions WHERE attachment_id::text=$1 AND version=$2`, id, version).Scan(&ref, &name, &mime)
	return ref, name, mime, err
}

func (s *Store) DeleteWikiAttachment(ctx context.Context, ws, actor, id string) ([]string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	a, err := scanWikiAttachment(tx.QueryRow(ctx, wikiAttachmentSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiAttachmentVisible+` AND `+wikiAttachmentWritable+` AND a.id::text=$3 FOR UPDATE OF a`, ws, actor, id))
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT blob_ref FROM wiki_attachment_versions WHERE attachment_id::text=$1`, id)
	if err != nil {
		return nil, err
	}
	refs := []string{}
	for rows.Next() {
		var ref string
		if err = rows.Scan(&ref); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, ref)
	}
	rows.Close()
	if _, err = tx.Exec(ctx, `DELETE FROM wiki_attachments WHERE id::text=$1`, id); err != nil {
		return nil, err
	}
	if err = wikiAttachmentAction(ctx, tx, ws, actor, a, models.OpDelete); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return refs, nil
}
