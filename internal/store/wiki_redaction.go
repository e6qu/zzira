package store

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/wikimarkup"
	"github.com/jackc/pgx/v5"
)

// Redaction replaces sensitive text in a page or blog post with a marker. In a
// body the marker is a redaction macro carrying the redaction's id, so a space
// administrator can later restore what it replaced; text redacted inside a
// code block is replaced with plain text and cannot be restored. A redaction
// can target the current version, which makes a new version, or one earlier
// version alone. Ranges are given against the stored text, or against a text
// node of the body in the document format.

const redactedText = "[REDACTED]"

type redactionTarget struct {
	kind, noun                                 string
	contentTable, versionsTable, redactionsTbl string
	column, entity, audit                      string
	conflict                                   error
}

var (
	pageRedactions = redactionTarget{kind: "page", noun: "page", contentTable: "wiki_pages", versionsTable: "wiki_page_versions",
		redactionsTbl: "wiki_page_redactions", column: "page_id", entity: "wiki_page", audit: "wiki.page", conflict: ErrWikiConflict}
	blogRedactions = redactionTarget{kind: "blogpost", noun: "blog post", contentTable: "wiki_blog_posts", versionsTable: "wiki_blog_post_versions",
		redactionsTbl: "wiki_blog_post_redactions", column: "blog_post_id", entity: "wiki_blogpost", audit: "wiki.blogpost", conflict: ErrWikiBlogPostConflict}
)

func redactionTargetFor(kind string) (redactionTarget, error) {
	switch kind {
	case "page":
		return pageRedactions, nil
	case "blogpost":
		return blogRedactions, nil
	}
	return redactionTarget{}, fmt.Errorf("%w: redactions belong to pages and blog posts", ErrWikiValidation)
}

type redactable struct {
	ID, SpaceID, Title, Body, VersionCreatedAt string
	Version                                    int
}

// lockRedactable reads current content the caller may edit and locks it.
func lockRedactable(ctx context.Context, tx pgx.Tx, ws, actor string, t redactionTarget, id string) (redactable, error) {
	if t.kind == "page" {
		page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE s.workspace_id=$1 AND `+wikiSpaceVisible+` AND `+wikiPageVisible+` AND `+wikiPageWritable+` AND p.id::text=$3 AND p.status='current' FOR UPDATE OF p`, ws, actor, id))
		if err != nil {
			return redactable{}, err
		}
		return redactable{ID: page.ID, SpaceID: page.SpaceID, Title: page.Title, Body: page.Body.Value, Version: page.Version.Number, VersionCreatedAt: page.Version.CreatedAt}, nil
	}
	blog, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE s.workspace_id=$1 AND `+wikiBlogPostVisible+` AND `+wikiBlogPostWritable+` AND b.id::text=$3 AND b.status='current' FOR UPDATE OF b`, ws, actor, id))
	if err != nil {
		return redactable{}, err
	}
	return redactable{ID: blog.ID, SpaceID: blog.SpaceID, Title: blog.Title, Body: blog.Body.Value, Version: blog.Version.Number, VersionCreatedAt: blog.Version.CreatedAt}, nil
}

type redactionRange struct {
	from, to        int
	order           int
	pointer, reason string
	original        string
	code            bool
	id              string
}

var (
	titlePointers = map[string]bool{"": true, "/": true, "/title": true, "/value": true}
	bodyPointers  = map[string]bool{"": true, "/": true, "/value": true, "/storage/value": true, "/body/storage/value": true}
)

// normalizeRedactions resolves pointers to ranges of the stored text, merges
// ranges that overlap and records the text each one removes. With a body,
// pointers into its document-format content are accepted too.
func normalizeRedactions(value string, pointers []models.WikiRedactionPointer, allowed map[string]bool, body bool) ([]redactionRange, error) {
	runes := []rune(value)
	ranges := make([]redactionRange, 0, len(pointers))
	for index, pointer := range pointers {
		if (pointer.From == nil) != (pointer.To == nil) {
			return nil, fmt.Errorf("%w: redaction from and to must be supplied together", ErrWikiValidation)
		}
		var from, to int
		switch {
		case body && strings.HasPrefix(pointer.Pointer, "/content/"):
			resolvedFrom, resolvedTo, err := documentPointerRange(value, pointer.Pointer, pointer.From, pointer.To)
			if err != nil {
				return nil, err
			}
			from, to = resolvedFrom, resolvedTo
		case allowed[pointer.Pointer]:
			from, to = 0, len(runes)
			if pointer.From != nil {
				from, to = *pointer.From, *pointer.To
			}
		default:
			return nil, fmt.Errorf("%w: unsupported redaction pointer %q", ErrWikiValidation, pointer.Pointer)
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
		ranges = append(ranges, redactionRange{from: from, to: to, order: index, pointer: pointer.Pointer, reason: reason})
	}
	sort.SliceStable(ranges, func(i, j int) bool {
		if ranges[i].from == ranges[j].from {
			return ranges[i].to < ranges[j].to
		}
		return ranges[i].from < ranges[j].from
	})
	merged := make([]redactionRange, 0, len(ranges))
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

// storageTextSegment is one run of text in a storage body: where it lies, in
// runes of the stored text, what it reads as, and whether it is code.
type storageTextSegment struct {
	start, end int
	raw, text  string
	code       bool
}

// storageTextSegments lists a storage body's text runs in document order.
// Code is text inside a preformatted block, inline code, or a macro's plain
// body or parameters.
func storageTextSegments(value string) []storageTextSegment {
	const root = "<root>"
	wrapped := root + value + "</root>"
	decoder := xml.NewDecoder(strings.NewReader(wrapped))
	var segments []storageTextSegment
	code, hidden := 0, 0
	start := 0
	isCode := func(name xml.Name) bool {
		return name.Space == "" && (name.Local == "pre" || name.Local == "code") ||
			name.Space == "ac" && (name.Local == "plain-text-body" || name.Local == "parameter")
	}
	isHidden := func(name xml.Name) bool {
		return name.Space == "ac" && (name.Local == "link" || name.Local == "task-id" || name.Local == "task-status" || name.Local == "task-uuid")
	}
	for {
		token, err := decoder.Token()
		if err == io.EOF || err != nil {
			break
		}
		end := int(decoder.InputOffset())
		switch t := token.(type) {
		case xml.StartElement:
			if isCode(t.Name) {
				code++
			}
			if isHidden(t.Name) {
				hidden++
			}
		case xml.EndElement:
			if isCode(t.Name) {
				code--
			}
			if isHidden(t.Name) {
				hidden--
			}
		case xml.CharData:
			if start >= len(root) && end <= len(root)+len(value) {
				raw := wrapped[start:end]
				segments = append(segments, storageTextSegment{
					start: utf8.RuneCountInString(value[:start-len(root)]), end: utf8.RuneCountInString(value[:end-len(root)]),
					raw: raw, text: string(t), code: code > 0 || hidden > 0,
				})
			}
		}
		start = end
	}
	return segments
}

// documentPointerRange resolves a JSON pointer to a text node of a body in the
// document format, and a range of that node's text, to the range of the
// stored text it came from.
func documentPointerRange(storage, pointer string, from, to *int) (int, int, error) {
	invalid := fmt.Errorf("%w: redaction pointer %q does not name a text node of the body", ErrWikiValidation, pointer)
	var doc any
	if err := json.Unmarshal(adf.FromHTML(storage), &doc); err != nil {
		return 0, 0, invalid
	}
	segments := strings.Split(strings.TrimPrefix(strings.TrimSuffix(pointer, "/text"), "/"), "/")
	node := doc
	for _, segment := range segments {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		switch current := node.(type) {
		case map[string]any:
			node = current[segment]
		case []any:
			index, err := strconv.Atoi(segment)
			if err != nil || index < 0 || index >= len(current) {
				return 0, 0, invalid
			}
			node = current[index]
		default:
			return 0, 0, invalid
		}
	}
	target, ok := node.(map[string]any)
	if !ok || target["type"] != "text" {
		return 0, 0, invalid
	}
	text, _ := target["text"].(string)
	// The same text can appear in more than one node; the pointer's node is
	// the nth of them, and so is its run in the stored text.
	occurrence := 0
	var walk func(any) bool
	walk = func(value any) bool {
		switch current := value.(type) {
		case map[string]any:
			if &current == &target || fmt.Sprintf("%p", current) == fmt.Sprintf("%p", target) {
				return true
			}
			if current["type"] == "text" && current["text"] == text {
				occurrence++
			}
			if content, ok := current["content"].([]any); ok {
				for _, child := range content {
					if walk(child) {
						return true
					}
				}
			}
		case []any:
			for _, child := range current {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	walk(doc)
	runes := []rune(text)
	start, end := 0, len(runes)
	if from != nil {
		start, end = *from, *to
	}
	if start < 0 || end <= start || end > len(runes) {
		return 0, 0, fmt.Errorf("%w: redaction range must select existing text", ErrWikiValidation)
	}
	seen := 0
	for _, segment := range storageTextSegments(storage) {
		at := strings.Index(segment.text, text)
		if at < 0 {
			continue
		}
		if seen < occurrence {
			seen++
			continue
		}
		prefix := utf8.RuneCountInString(segment.text[:at])
		rawFrom, rawTo := rawRuneOffset(segment.raw, prefix+start), rawRuneOffset(segment.raw, prefix+end)
		return segment.start + rawFrom, segment.start + rawTo, nil
	}
	return 0, 0, invalid
}

// rawRuneOffset finds where the nth character of escaped text lies in its raw
// form, where an entity is several runes that read as one.
func rawRuneOffset(raw string, n int) int {
	offset, read := 0, 0
	runes := []rune(raw)
	for offset < len(runes) && read < n {
		if runes[offset] == '&' {
			if end := strings.IndexRune(string(runes[offset:]), ';'); end > 0 {
				offset += utf8.RuneCountInString(string(runes[offset:])[:end]) + 1
				read++
				continue
			}
		}
		offset++
		read++
	}
	return offset
}

func redactionMacro(id string) string {
	return `<ac:structured-macro ac:name="redacted" ac:macro-id="` + id + `"><ac:plain-text-body>` + redactedText + `</ac:plain-text-body></ac:structured-macro>`
}

// applyRedactions writes the markers. In a body a restorable redaction is a
// macro naming it; code and titles take plain text.
func applyRedactions(value string, ranges []redactionRange, body bool) string {
	runes := []rune(value)
	ordered := append([]redactionRange(nil), ranges...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].from > ordered[j].from })
	for _, item := range ordered {
		marker := redactedText
		if body && !item.code {
			marker = redactionMacro(item.id)
		}
		runes = append(append(append([]rune{}, runes[:item.from]...), []rune(marker)...), runes[item.to:]...)
	}
	return string(runes)
}

func scrubRedactedHistory(value string, ranges []redactionRange) string {
	for _, item := range ranges {
		value = strings.ReplaceAll(value, item.original, redactedText)
	}
	return value
}

// redactWikiContent redacts a page or blog post.
func (s *Store) redactWikiContent(ctx context.Context, ws, actor string, t redactionTarget, id, createdAt string, version int, cleanHistory bool, titlePointerList, bodyPointerList []models.WikiRedactionPointer) ([]models.WikiRedactionResult, []models.WikiRedactionResult, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	content, err := lockRedactable(ctx, tx, ws, actor, t, id)
	if err != nil {
		return nil, nil, err
	}
	requestedTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: createdAt must be an RFC 3339 timestamp", ErrWikiValidation)
	}
	historical := version != 0 && version < content.Version
	title, body, versionCreatedAt := content.Title, content.Body, content.VersionCreatedAt
	switch {
	case version < 0 || version > content.Version:
		return nil, nil, t.conflict
	case historical:
		if err := tx.QueryRow(ctx, `SELECT title,body,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"') FROM `+t.versionsTable+`
			WHERE `+t.column+`::text=$1 AND version=$2 FOR UPDATE`, id, version).Scan(&title, &body, &versionCreatedAt); err != nil {
			return nil, nil, t.conflict
		}
	}
	targetTime, _ := time.Parse(time.RFC3339, versionCreatedAt)
	if !requestedTime.Equal(targetTime) {
		return nil, nil, t.conflict
	}
	if len(titlePointerList)+len(bodyPointerList) == 0 || len(titlePointerList)+len(bodyPointerList) > 100 {
		return nil, nil, fmt.Errorf("%w: provide between 1 and 100 redaction pointers", ErrWikiValidation)
	}
	titleRanges, err := normalizeRedactions(title, titlePointerList, titlePointers, false)
	if err != nil {
		return nil, nil, err
	}
	bodyRanges, err := normalizeRedactions(body, bodyPointerList, bodyPointers, true)
	if err != nil {
		return nil, nil, err
	}
	segments := storageTextSegments(body)
	for i := range bodyRanges {
		for _, segment := range segments {
			if segment.code && bodyRanges[i].from < segment.end && bodyRanges[i].to > segment.start {
				bodyRanges[i].code = true
			}
		}
	}
	for _, ranges := range [][]redactionRange{titleRanges, bodyRanges} {
		for i := range ranges {
			if !ranges[i].code {
				if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&ranges[i].id); err != nil {
					return nil, nil, err
				}
			}
		}
	}
	redactedTitle, redactedBody := applyRedactions(title, titleRanges, false), applyRedactions(body, bodyRanges, true)
	if strings.TrimSpace(redactedTitle) == "" || strings.TrimSpace(redactedTitle) == redactedText && len(titleRanges) > 0 && titleRanges[0].from == 0 && titleRanges[0].to == utf8.RuneCountInString(title) {
		return nil, nil, fmt.Errorf("%w: redaction cannot remove the complete %s title", ErrWikiValidation, t.noun)
	}
	if _, err := wikimarkup.Render(redactedBody); err != nil {
		return nil, nil, fmt.Errorf("%w: redaction ranges must select body text without markup", ErrWikiValidation)
	}
	if cleanHistory {
		rows, err := tx.Query(ctx, `SELECT version,title,body FROM `+t.versionsTable+` WHERE `+t.column+`::text=$1 FOR UPDATE`, id)
		if err != nil {
			return nil, nil, err
		}
		type prior struct {
			version     int
			title, body string
		}
		versions, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (prior, error) {
			var item prior
			err := row.Scan(&item.version, &item.title, &item.body)
			return item, err
		})
		if err != nil {
			return nil, nil, err
		}
		for _, item := range versions {
			if _, err := tx.Exec(ctx, `UPDATE `+t.versionsTable+` SET title=$3,body=$4 WHERE `+t.column+`::text=$1 AND version=$2`, id, item.version, scrubRedactedHistory(item.title, titleRanges), scrubRedactedHistory(item.body, bodyRanges)); err != nil {
				return nil, nil, err
			}
		}
	}
	recordedVersion := version
	if historical {
		if _, err := tx.Exec(ctx, `UPDATE `+t.versionsTable+` SET title=$3,body=$4 WHERE `+t.column+`::text=$1 AND version=$2`, id, version, redactedTitle, redactedBody); err != nil {
			return nil, nil, err
		}
	} else {
		recordedVersion = content.Version + 1
		if _, err := tx.Exec(ctx, `UPDATE `+t.contentTable+` SET title=$2,body=$3,version=$4 WHERE id::text=$1`, id, redactedTitle, redactedBody, recordedVersion); err != nil {
			return nil, nil, err
		}
		if err := wikiBodyChanged(ctx, tx, ws, actor, t.kind, id, redactedBody); err != nil {
			return nil, nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+t.versionsTable+`(`+t.column+`,version,title,body,status,author_id,message) VALUES($1::bigint,$2,$3,$4,'current',$5,'Sensitive content redacted')`, id, recordedVersion, redactedTitle, redactedBody, actor); err != nil {
			return nil, nil, err
		}
	}
	record := func(section string, ranges []redactionRange) ([]models.WikiRedactionResult, error) {
		ordered := append([]redactionRange(nil), ranges...)
		sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].order < ordered[j].order })
		results := make([]models.WikiRedactionResult, 0, len(ordered))
		for _, item := range ordered {
			var original any
			if !item.code {
				original = item.original
			}
			id := item.id
			if item.code {
				if err := tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
					return nil, err
				}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO `+t.redactionsTbl+`(id,`+t.column+`,version,section,pointer,from_index,to_index,reason,actor_id,original_text,restorable)
				VALUES($1::uuid,$2::bigint,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, content.ID, recordedVersion, section, item.pointer, item.from, item.to, item.reason, actor, original, !item.code); err != nil {
				return nil, err
			}
			results = append(results, models.WikiRedactionResult{Pointer: item.pointer, From: item.from, To: item.to, Reason: item.reason, RedactionID: item.id})
		}
		return results, nil
	}
	titleResults, err := record("title", titleRanges)
	if err != nil {
		return nil, nil, err
	}
	bodyResults, err := record("body", bodyRanges)
	if err != nil {
		return nil, nil, err
	}
	if !historical {
		if err := redactableAction(ctx, tx, ws, actor, t, id); err != nil {
			return nil, nil, err
		}
	}
	detail, _ := json.Marshal(map[string]any{"cleanHistory": cleanHistory, "previousVersion": content.Version, "version": recordedVersion, "historical": historical, "titleRedactions": len(titleResults), "bodyRedactions": len(bodyResults)})
	if _, err := tx.Exec(ctx, `INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail) SELECT organization_id,$2,$3,$4,$5,$6::jsonb FROM sites WHERE workspace_id=$1`, ws, actor, t.audit+".redacted", t.entity, id, detail); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return titleResults, bodyResults, nil
}

// redactableAction records the content's new state for replicas.
func redactableAction(ctx context.Context, tx pgx.Tx, ws, actor string, t redactionTarget, id string) error {
	if t.kind == "page" {
		page, err := scanWikiPage(tx.QueryRow(ctx, wikiPageSelect+` WHERE p.id::text=$1`, id))
		if err != nil {
			return err
		}
		return wikiAction(ctx, tx, ws, actor, "wiki_page", page.ID, page.SpaceID, page)
	}
	post, err := scanWikiBlogPost(tx.QueryRow(ctx, wikiBlogPostSelect+` WHERE b.id::text=$1`, id))
	if err != nil {
		return err
	}
	return wikiAction(ctx, tx, ws, actor, "wiki_blogpost", post.ID, post.SpaceID, post)
}

func (s *Store) RedactWikiPage(ctx context.Context, ws, actor, id, createdAt string, version int, cleanHistory bool, titlePointerList, bodyPointerList []models.WikiRedactionPointer) (*models.WikiPage, []models.WikiRedactionResult, []models.WikiRedactionResult, error) {
	title, body, err := s.redactWikiContent(ctx, ws, actor, pageRedactions, id, createdAt, version, cleanHistory, titlePointerList, bodyPointerList)
	if err != nil {
		return nil, nil, nil, err
	}
	page, err := s.WikiPage(ctx, ws, actor, id)
	return page, title, body, err
}

func (s *Store) RedactWikiBlogPost(ctx context.Context, ws, actor, id, createdAt string, version int, cleanHistory bool, titlePointerList, bodyPointerList []models.WikiRedactionPointer) (*models.WikiBlogPost, []models.WikiRedactionResult, []models.WikiRedactionResult, error) {
	title, body, err := s.redactWikiContent(ctx, ws, actor, blogRedactions, id, createdAt, version, cleanHistory, titlePointerList, bodyPointerList)
	if err != nil {
		return nil, nil, nil, err
	}
	post, err := s.WikiBlogPost(ctx, ws, actor, id)
	return post, title, body, err
}

// WikiRedaction is one recorded redaction as a space sees it.
type WikiRedaction struct {
	ID, Section, Reason, ActorID, CreatedAt string
	Version                                 int
	Restorable, Restored                    bool
}

// WikiRedactions lists the redactions made to a page or blog post the caller
// can see, newest first. What was removed is not part of the list.
func (s *Store) WikiRedactions(ctx context.Context, ws, actor, kind, id string) ([]WikiRedaction, error) {
	t, err := redactionTargetFor(kind)
	if err != nil {
		return nil, err
	}
	if kind == "page" {
		_, err = s.WikiPage(ctx, ws, actor, id)
	} else {
		_, err = s.WikiBlogPost(ctx, ws, actor, id)
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text,section,reason,actor_id,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),version,restorable,restored_at IS NOT NULL
		FROM `+t.redactionsTbl+` WHERE `+t.column+`::text=$1 ORDER BY created_at DESC,id`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (WikiRedaction, error) {
		var r WikiRedaction
		err := row.Scan(&r.ID, &r.Section, &r.Reason, &r.ActorID, &r.CreatedAt, &r.Version, &r.Restorable, &r.Restored)
		return r, err
	})
}

// RestoreWikiRedaction puts back what a redaction removed. Only an
// administrator of the space can, and only while the marker is still there:
// in the current version, which makes a new version, or else in the earlier
// version that was redacted.
func (s *Store) RestoreWikiRedaction(ctx context.Context, ws, actor, kind, id, redactionID string) error {
	t, err := redactionTargetFor(kind)
	if err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	content, err := lockRedactable(ctx, tx, ws, actor, t, id)
	if err != nil {
		return err
	}
	if err := wikiSpaceAdmin(ctx, tx, ws, actor, content.SpaceID); err != nil {
		return err
	}
	var section string
	var version, from int
	var original *string
	var restorable, restored bool
	if err := tx.QueryRow(ctx, `SELECT section,version,from_index,original_text,restorable,restored_at IS NOT NULL FROM `+t.redactionsTbl+`
		WHERE id::text=$1 AND `+t.column+`::text=$2 FOR UPDATE`, redactionID, id).Scan(&section, &version, &from, &original, &restorable, &restored); err != nil {
		return err
	}
	switch {
	case restored:
		return fmt.Errorf("%w: this redaction has already been restored", ErrWikiValidation)
	case !restorable || original == nil:
		return fmt.Errorf("%w: this redaction cannot be restored", ErrWikiValidation)
	}
	restoreTitle := func(title string) (string, bool) {
		runes := []rune(title)
		marker := []rune(redactedText)
		if from+len(marker) <= len(runes) && string(runes[from:from+len(marker)]) == redactedText {
			return string(runes[:from]) + *original + string(runes[from+len(marker):]), true
		}
		return title, false
	}
	title, body, changed := content.Title, content.Body, false
	if section == "body" && strings.Contains(body, redactionMacro(redactionID)) {
		body, changed = strings.Replace(body, redactionMacro(redactionID), *original, 1), true
	} else if section == "title" {
		title, changed = restoreTitle(title)
	}
	if changed {
		next := content.Version + 1
		if _, err := tx.Exec(ctx, `UPDATE `+t.contentTable+` SET title=$2,body=$3,version=$4 WHERE id::text=$1`, id, title, body, next); err != nil {
			return err
		}
		if err := wikiBodyChanged(ctx, tx, ws, actor, t.kind, id, body); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+t.versionsTable+`(`+t.column+`,version,title,body,status,author_id,message) VALUES($1::bigint,$2,$3,$4,'current',$5,'Redacted content restored')`, id, next, title, body, actor); err != nil {
			return err
		}
		if err := redactableAction(ctx, tx, ws, actor, t, id); err != nil {
			return err
		}
	} else {
		var priorTitle, priorBody string
		if err := tx.QueryRow(ctx, `SELECT title,body FROM `+t.versionsTable+` WHERE `+t.column+`::text=$1 AND version=$2 FOR UPDATE`, id, version).Scan(&priorTitle, &priorBody); err != nil {
			return err
		}
		if section == "body" && strings.Contains(priorBody, redactionMacro(redactionID)) {
			priorBody, changed = strings.Replace(priorBody, redactionMacro(redactionID), *original, 1), true
		} else if section == "title" {
			priorTitle, changed = restoreTitle(priorTitle)
		}
		if !changed {
			return fmt.Errorf("%w: the redacted text is no longer in the %s", ErrWikiValidation, t.noun)
		}
		if _, err := tx.Exec(ctx, `UPDATE `+t.versionsTable+` SET title=$3,body=$4 WHERE `+t.column+`::text=$1 AND version=$2`, id, version, priorTitle, priorBody); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE `+t.redactionsTbl+` SET restored_at=now(),restored_by=$2,original_text=NULL WHERE id::text=$1`, redactionID, actor); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"redactionId": redactionID, "section": section})
	if _, err := tx.Exec(ctx, `INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail) SELECT organization_id,$2,$3,$4,$5,$6::jsonb FROM sites WHERE workspace_id=$1`, ws, actor, t.audit+".redaction_restored", t.entity, id, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
