package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/wikimarkup"
	"github.com/jackc/pgx/v5"
)

type blogRedactionRange struct {
	from, to int
	order    int
	pointer  string
	reason   string
	original string
}

func normalizeBlogRedactions(value string, pointers []models.WikiRedactionPointer, allowed map[string]bool) ([]blogRedactionRange, error) {
	runes := []rune(value)
	ranges := make([]blogRedactionRange, 0, len(pointers))
	for index, pointer := range pointers {
		if !allowed[pointer.Pointer] {
			return nil, fmt.Errorf("%w: unsupported redaction pointer %q", ErrWikiValidation, pointer.Pointer)
		}
		from, to := 0, len(runes)
		if (pointer.From == nil) != (pointer.To == nil) {
			return nil, fmt.Errorf("%w: redaction from and to must be supplied together", ErrWikiValidation)
		}
		if pointer.From != nil {
			from, to = *pointer.From, *pointer.To
		}
		if from < 0 || to <= from || to > len(runes) {
			return nil, fmt.Errorf("%w: redaction range must select existing text", ErrWikiValidation)
		}
		reason := ""
		if pointer.Reason != nil {
			reason = strings.TrimSpace(*pointer.Reason)
			if utf8.RuneCountInString(reason) > 1000 {
				return nil, fmt.Errorf("%w: redaction reason must be at most 1000 characters", ErrWikiValidation)
			}
		}
		ranges = append(ranges, blogRedactionRange{from: from, to: to, order: index, pointer: pointer.Pointer, reason: reason})
	}
	sort.SliceStable(ranges, func(i, j int) bool {
		if ranges[i].from == ranges[j].from {
			return ranges[i].to < ranges[j].to
		}
		return ranges[i].from < ranges[j].from
	})
	merged := make([]blogRedactionRange, 0, len(ranges))
	for _, candidate := range ranges {
		if len(merged) == 0 || candidate.from > merged[len(merged)-1].to {
			merged = append(merged, candidate)
			continue
		}
		current := &merged[len(merged)-1]
		if candidate.to > current.to {
			current.to = candidate.to
		}
		if candidate.order < current.order {
			current.order, current.pointer, current.reason = candidate.order, candidate.pointer, candidate.reason
		}
	}
	for i := range merged {
		merged[i].original = string(runes[merged[i].from:merged[i].to])
	}
	return merged, nil
}

func applyBlogRedactions(value string, ranges []blogRedactionRange) string {
	runes := []rune(value)
	ordered := append([]blogRedactionRange(nil), ranges...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].from > ordered[j].from })
	for _, item := range ordered {
		runes = append(append(append([]rune{}, runes[:item.from]...), []rune("[REDACTED]")...), runes[item.to:]...)
	}
	return string(runes)
}

func scrubBlogHistory(value string, ranges []blogRedactionRange) string {
	for _, item := range ranges {
		value = strings.ReplaceAll(value, item.original, "[REDACTED]")
	}
	return value
}

func (s *Store) RedactWikiBlogPost(ctx context.Context, ws, actor, id, createdAt string, version int, cleanHistory bool, titlePointers, bodyPointers []models.WikiRedactionPointer) (*models.WikiBlogPost, []models.WikiRedactionResult, []models.WikiRedactionResult, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	blog, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiBlogPostVisible+` AND `+wikiBlogPostWritable+` AND b.id::text=$3 AND b.status='current' FOR UPDATE OF b`, ws, actor, id))
	if err != nil {
		return nil, nil, nil, err
	}
	requestedTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: createdAt must be an RFC 3339 timestamp", ErrWikiValidation)
	}
	currentTime, _ := time.Parse(time.RFC3339, blog.Version.CreatedAt)
	if !requestedTime.Equal(currentTime) || (version != 0 && version != blog.Version.Number) {
		return nil, nil, nil, ErrWikiBlogPostConflict
	}
	if len(titlePointers)+len(bodyPointers) == 0 || len(titlePointers)+len(bodyPointers) > 100 {
		return nil, nil, nil, fmt.Errorf("%w: provide between 1 and 100 redaction pointers", ErrWikiValidation)
	}
	titleRanges, err := normalizeBlogRedactions(blog.Title, titlePointers, map[string]bool{"": true, "/": true, "/title": true, "/value": true})
	if err != nil {
		return nil, nil, nil, err
	}
	bodyRanges, err := normalizeBlogRedactions(blog.Body.Value, bodyPointers, map[string]bool{"": true, "/": true, "/value": true, "/storage/value": true, "/body/storage/value": true})
	if err != nil {
		return nil, nil, nil, err
	}
	redactedTitle, redactedBody := applyBlogRedactions(blog.Title, titleRanges), applyBlogRedactions(blog.Body.Value, bodyRanges)
	if strings.TrimSpace(redactedTitle) == "" {
		return nil, nil, nil, fmt.Errorf("%w: redaction cannot remove the complete blog post title", ErrWikiValidation)
	}
	if _, err := wikimarkup.Render(redactedBody); err != nil {
		return nil, nil, nil, fmt.Errorf("%w: redaction ranges must select body text without markup", ErrWikiValidation)
	}
	if cleanHistory {
		rows, err := tx.Query(ctx, `SELECT version,title,body FROM wiki_blog_post_versions WHERE blog_post_id::text=$1 FOR UPDATE`, id)
		if err != nil {
			return nil, nil, nil, err
		}
		type historical struct {
			version     int
			title, body string
		}
		versions := []historical{}
		for rows.Next() {
			var item historical
			if err := rows.Scan(&item.version, &item.title, &item.body); err != nil {
				rows.Close()
				return nil, nil, nil, err
			}
			versions = append(versions, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, nil, nil, err
		}
		rows.Close()
		for _, item := range versions {
			if _, err := tx.Exec(ctx, `UPDATE wiki_blog_post_versions SET title=$3,body=$4 WHERE blog_post_id::text=$1 AND version=$2`, id, item.version, scrubBlogHistory(item.title, titleRanges), scrubBlogHistory(item.body, bodyRanges)); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	newVersion := blog.Version.Number + 1
	if _, err := tx.Exec(ctx, `UPDATE wiki_blog_posts SET title=$2,body=$3,version=$4 WHERE id::text=$1`, id, redactedTitle, redactedBody, newVersion); err != nil {
		return nil, nil, nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO wiki_blog_post_versions(blog_post_id,version,title,body,status,author_id,message) VALUES($1::bigint,$2,$3,$4,'current',$5,'Sensitive content redacted')`, id, newVersion, redactedTitle, redactedBody, actor); err != nil {
		return nil, nil, nil, err
	}
	titleResults := make([]models.WikiRedactionResult, 0, len(titleRanges))
	bodyResults := make([]models.WikiRedactionResult, 0, len(bodyRanges))
	insert := func(section string, ranges []blogRedactionRange, results *[]models.WikiRedactionResult) error {
		ordered := append([]blogRedactionRange(nil), ranges...)
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].order < ordered[j].order })
		for _, item := range ordered {
			var redactionID string
			if err := tx.QueryRow(ctx, `INSERT INTO wiki_blog_post_redactions(blog_post_id,version,section,pointer,from_index,to_index,reason,actor_id) VALUES($1::bigint,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`, id, newVersion, section, item.pointer, item.from, item.to, item.reason, actor).Scan(&redactionID); err != nil {
				return err
			}
			*results = append(*results, models.WikiRedactionResult{Pointer: item.pointer, From: item.from, To: item.to, Reason: item.reason, RedactionID: redactionID})
		}
		return nil
	}
	if err := insert("title", titleRanges, &titleResults); err != nil {
		return nil, nil, nil, err
	}
	if err := insert("body", bodyRanges, &bodyResults); err != nil {
		return nil, nil, nil, err
	}
	updated, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE b.id::text=$1`, id))
	if err != nil {
		return nil, nil, nil, err
	}
	if err := wikiAction(ctx, tx, ws, actor, "wiki_blogpost", updated.ID, updated.SpaceID, updated); err != nil {
		return nil, nil, nil, err
	}
	detail, _ := json.Marshal(map[string]any{"cleanHistory": cleanHistory, "previousVersion": blog.Version.Number, "version": newVersion, "titleRedactions": len(titleResults), "bodyRedactions": len(bodyResults)})
	if _, err := tx.Exec(ctx, `INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail) SELECT organization_id,$2,'wiki.blogpost.redacted','wiki_blogpost',$3,$4::jsonb FROM sites WHERE workspace_id=$1`, ws, actor, id, detail); err != nil {
		return nil, nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, nil, err
	}
	return updated, titleResults, bodyResults, nil
}

func (s *Store) WikiBlogCustomContent(ctx context.Context, ws, actor, blogPostID, contentType, order string) ([]models.WikiBlogCustomContent, error) {
	blog, err := s.WikiBlogPost(ctx, ws, actor, blogPostID)
	if err != nil || blog.Status != "current" {
		if err == nil {
			err = pgx.ErrNoRows
		}
		return nil, err
	}
	var representation string
	if err := s.Pool.QueryRow(ctx, `SELECT body_representation FROM wiki_custom_content_types WHERE type=$1`, contentType).Scan(&representation); err != nil {
		return nil, err
	}
	orders := map[string]string{"": "cc.id", "id": "cc.id", "-id": "cc.id DESC", "created-date": "cc.created_at,cc.id", "-created-date": "cc.created_at DESC,cc.id DESC", "modified-date": "cc.created_at,cc.id", "-modified-date": "cc.created_at DESC,cc.id DESC", "title": "cc.title,cc.id", "-title": "cc.title DESC,cc.id DESC"}
	orderSQL, ok := orders[order]
	if !ok {
		return nil, fmt.Errorf("%w: unsupported custom content sort order", ErrWikiValidation)
	}
	rows, err := s.Pool.Query(ctx, `SELECT cc.id::text,cc.type,'current',cc.title,b.space_id::text,cc.blog_post_id::text,cc.author_id,to_char(cc.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),ct.body_representation,cc.body,cc.version,cc.author_id,to_char(cc.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM wiki_blog_custom_content cc JOIN wiki_custom_content_types ct ON ct.type=cc.type JOIN wiki_blog_posts b ON b.id=cc.blog_post_id WHERE cc.blog_post_id::text=$1 AND cc.type=$2 ORDER BY `+orderSQL, blogPostID, contentType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.WikiBlogCustomContent{}
	for rows.Next() {
		var value models.WikiBlogCustomContent
		if err := rows.Scan(&value.ID, &value.Type, &value.Status, &value.Title, &value.SpaceID, &value.BlogPostID, &value.AuthorID, &value.CreatedAt, &value.BodyRepresentation, &value.Body.Value, &value.Version.Number, &value.Version.AuthorID, &value.Version.CreatedAt); err != nil {
			return nil, err
		}
		value.Body.Representation = representation
		values = append(values, value)
	}
	return values, rows.Err()
}
