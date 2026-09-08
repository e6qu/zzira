package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

var ErrWikiBlogPostConflict = errors.New("the blog post changed; reload the latest version before saving")

var wikiBlogPostVisible = `(` + wikiSpacePermissionAllowed("read/blogpost") + `) AND (b.published OR b.author_id=$2) AND (NOT b.private OR b.author_id=$2)`

const wikiBlogPostAuthorWritable = `(b.author_id=$2 OR NOT b.private)`

var wikiBlogPostWritable = `(` + wikiSpaceVisible + `) AND (` + wikiSpaceCanUpdateBlogPost + `) AND (` + wikiBlogPostAuthorWritable + `)`

const wikiBlogPostSelect = `SELECT b.id::text,s.workspace_id,b.space_id::text,b.title,b.status,b.published,b.private,b.classification_level,b.body,b.author_id,to_char(b.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),v.version,v.message,v.minor_edit,v.author_id,to_char(v.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id JOIN wiki_blog_post_versions v ON v.blog_post_id=b.id AND v.version=b.version`

func scanWikiBlogPost(row pgx.Row) (*models.WikiBlogPost, error) {
	b := &models.WikiBlogPost{Body: models.WikiBody{Representation: "storage"}}
	err := row.Scan(&b.ID, &b.WorkspaceID, &b.SpaceID, &b.Title, &b.Status, &b.Published, &b.Private, &b.ClassificationLevel, &b.Body.Value, &b.AuthorID, &b.CreatedAt, &b.Version.Number, &b.Version.Message, &b.Version.MinorEdit, &b.Version.AuthorID, &b.Version.CreatedAt)
	return b, err
}

func (s *Store) WikiBlogPost(ctx context.Context, ws, actor, id string) (*models.WikiBlogPost, error) {
	return scanWikiBlogPost(s.Pool.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiBlogPostVisible+` AND b.id::text=$3`, ws, actor, id))
}

func (s *Store) WikiBlogPosts(ctx context.Context, ws, actor, spaceID, status, title, order string) ([]*models.WikiBlogPost, error) {
	orders := map[string]string{"": "b.id", "id": "b.id", "-id": "b.id DESC", "created-date": "b.created_at,b.id", "-created-date": "b.created_at DESC,b.id DESC", "modified-date": "v.created_at,b.id", "-modified-date": "v.created_at DESC,b.id DESC"}
	orderSQL, ok := orders[order]
	if !ok {
		return nil, fmt.Errorf("%w: unsupported blog post sort order", ErrWikiValidation)
	}
	rows, err := s.Pool.Query(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiBlogPostVisible+` AND ($3='' OR b.space_id::text=$3) AND b.status=$4 AND ($5='' OR b.title=$5) ORDER BY `+orderSQL, ws, actor, spaceID, status, title)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []*models.WikiBlogPost{}
	for rows.Next() {
		value, err := scanWikiBlogPost(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) SaveWikiBlogPost(ctx context.Context, ws, actor string, input models.WikiBlogPost) (*models.WikiBlogPost, error) {
	isNew := input.ID == ""
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	spacePermission := wikiSpaceCanCreateBlogPost
	if !isNew {
		spacePermission = wikiSpaceCanUpdateBlogPost
		if input.Status == "trashed" {
			spacePermission = wikiSpaceCanDeleteBlogPost
		}
	}
	var spaceID, defaultClassification string
	if err := tx.QueryRow(ctx, `SELECT s.id::text,s.default_classification_level FROM wiki_spaces s WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+spacePermission+` AND s.id::text=$3 FOR UPDATE`, ws, actor, input.SpaceID).Scan(&spaceID, &defaultClassification); err != nil {
		return nil, err
	}
	if !isNew {
		old, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+spacePermission+` AND `+wikiBlogPostVisible+` AND `+wikiBlogPostAuthorWritable+` AND b.id::text=$3 FOR UPDATE OF b`, ws, actor, input.ID))
		if err != nil {
			return nil, err
		}
		if old.SpaceID != input.SpaceID {
			return nil, fmt.Errorf("%w: moving blog posts between spaces is not supported", ErrWikiValidation)
		}
		if input.Version.Number != old.Version.Number+1 {
			return nil, ErrWikiBlogPostConflict
		}
		if input.Status == "draft" && old.Published {
			return nil, fmt.Errorf("%w: a published blog post cannot be converted to a draft", ErrWikiValidation)
		}
		if old.Status == "trashed" && input.Status == "current" {
			input.Title, input.Body = old.Title, old.Body
		}
		input.AuthorID, input.Private, input.CreatedAt = old.AuthorID, old.Private, old.CreatedAt
	} else {
		input.Version.Number, input.AuthorID = 1, actor
		input.ClassificationLevel = defaultClassification
	}
	if isNew {
		var createdAt *time.Time
		if input.CreatedAt != "" {
			value, err := time.Parse(time.RFC3339, input.CreatedAt)
			if err != nil {
				return nil, fmt.Errorf("%w: createdAt must be an RFC 3339 timestamp", ErrWikiValidation)
			}
			createdAt = &value
		}
		err = tx.QueryRow(ctx, `INSERT INTO wiki_blog_posts(space_id,title,status,body,author_id,private,published,created_at,classification_level) VALUES($1::bigint,$2,$3,$4,$5,$6,$3='current',COALESCE($7::timestamptz,now()),$8) RETURNING id::text`, spaceID, input.Title, input.Status, input.Body.Value, actor, input.Private, createdAt, input.ClassificationLevel).Scan(&input.ID)
	} else {
		_, err = tx.Exec(ctx, `UPDATE wiki_blog_posts SET title=$2,status=$3,body=$4,version=$5,published=(published OR $3='current') WHERE id::text=$1`, input.ID, input.Title, input.Status, input.Body.Value, input.Version.Number)
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO wiki_blog_post_versions(blog_post_id,version,title,body,status,author_id,message,minor_edit) VALUES($1::bigint,$2,$3,$4,$5,$6,$7,$8)`, input.ID, input.Version.Number, input.Title, input.Body.Value, input.Status, actor, input.Version.Message, input.Version.MinorEdit); err != nil {
		return nil, err
	}
	blog, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE b.id::text=$1`, input.ID))
	if err != nil {
		return nil, err
	}
	if err := wikiAction(ctx, tx, ws, actor, "wiki_blogpost", blog.ID, blog.SpaceID, blog); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return blog, nil
}

func (s *Store) PurgeWikiBlogPost(ctx context.Context, ws, actor, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	blog, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiSpaceCanDeleteBlogPost+` AND `+wikiBlogPostVisible+` AND `+wikiBlogPostAuthorWritable+` AND b.id::text=$3 AND b.status='trashed' FOR UPDATE OF b`, ws, actor, id))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM wiki_blog_posts WHERE id::text=$1`, id); err != nil {
		return err
	}
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"wikiSpaceId": blog.SpaceID, "wiki_blogpost": blog})
	if err != nil {
		return err
	}
	if err := appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: "wiki_blogpost", EntityID: id, Op: models.OpDelete, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) WikiBlogPostVersions(ctx context.Context, ws, actor, id, order string) ([]models.WikiVersion, error) {
	if _, err := s.WikiBlogPost(ctx, ws, actor, id); err != nil {
		return nil, err
	}
	orderSQL := "created_at,version"
	if order == "-modified-date" {
		orderSQL = "created_at DESC,version DESC"
	} else if order != "" && order != "modified-date" {
		return nil, fmt.Errorf("%w: unsupported version sort order", ErrWikiValidation)
	}
	rows, err := s.Pool.Query(ctx, `SELECT version,message,minor_edit,author_id,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_blog_post_versions WHERE blog_post_id::text=$1 AND (status<>'draft' OR author_id=$2) ORDER BY `+orderSQL, id, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.WikiVersion{}
	for rows.Next() {
		var value models.WikiVersion
		if err := rows.Scan(&value.Number, &value.Message, &value.MinorEdit, &value.AuthorID, &value.CreatedAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) WikiBlogPostVersion(ctx context.Context, ws, actor, id string, version int) (*models.WikiVersion, error) {
	if _, err := s.WikiBlogPost(ctx, ws, actor, id); err != nil {
		return nil, err
	}
	value := &models.WikiVersion{}
	err := s.Pool.QueryRow(ctx, `SELECT version,message,minor_edit,author_id,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_blog_post_versions WHERE blog_post_id::text=$1 AND version=$2 AND (status<>'draft' OR author_id=$3)`, id, version, actor).Scan(&value.Number, &value.Message, &value.MinorEdit, &value.AuthorID, &value.CreatedAt)
	return value, err
}
