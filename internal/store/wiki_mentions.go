package store

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/wikimarkup"
	"github.com/jackc/pgx/v5"
)

// Mentioning someone tells them. A page, blog post or comment that names a
// person in a mention notifies that person the first time the mention
// appears, provided they can see what mentioned them; writing your own name
// notifies nobody.

// These check whether $2 can see a page, blog post or comment ($3).
var wikiPageReadableBy = `SELECT ` + wikiPageReadableExpr("$3")

var wikiBlogPostReadableBy = `SELECT ` + wikiBlogPostReadableExpr("$3")

var wikiCommentReadableBy = `SELECT EXISTS(` + wikiCommentReadable + `)`

// wikiPageReadableExpr and wikiBlogPostReadableExpr are the same questions
// asked of an id a query already has in hand rather than of a parameter, so
// a place that reads content alongside something else -- an inbox, a sync
// stream -- asks exactly what a read asks.
func wikiPageReadableExpr(idExpr string) string {
	return `EXISTS(SELECT 1 FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
	WHERE s.workspace_id=$1 AND ` + wikiSpaceVisible + ` AND ` + wikiPageVisible + ` AND p.id::text=` + idExpr + `)`
}

// wikiCustomContentReadableExpr is the same question for custom content,
// which a comment notification can also lead to.
func wikiCustomContentReadableExpr(idExpr string) string {
	return `EXISTS(SELECT 1 FROM wiki_content c JOIN wiki_spaces s ON s.id=c.space_id
	LEFT JOIN wiki_pages p ON p.id=c.root_page_id
	WHERE s.workspace_id=$1 AND c.type='custom' AND c.status='current' AND ` + wikiContentVisibleFor("custom") + ` AND c.id::text=` + idExpr + `)`
}

func wikiBlogPostReadableExpr(idExpr string) string {
	return `EXISTS(SELECT 1 FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id
	WHERE s.workspace_id=$1 AND ` + wikiSpaceVisible + ` AND ` + wikiBlogPostVisible + ` AND b.id::text=` + idExpr + `)`
}

// notificationSubjectReadable is whether the person a notification belongs to
// can still see what it is about. Losing access to a page takes back what
// its notifications said about it -- the title is in the message -- so an
// inbox and a replica must both stop showing one. Anything this does not
// know how to check is left alone.
func notificationSubjectReadable(entityTypeExpr, entityIDExpr, userParam string) string {
	predicate := `(CASE ` + entityTypeExpr + `
		WHEN 'wiki_page' THEN ` + wikiPageReadableExpr(entityIDExpr) + `
		WHEN 'wiki_blogpost' THEN ` + wikiBlogPostReadableExpr(entityIDExpr) + `
		WHEN 'wiki_content' THEN ` + wikiCustomContentReadableExpr(entityIDExpr) + `
		ELSE TRUE END)`
	return forWikiUserParam(predicate, userParam)
}

// forWikiUserParam moves a wiki predicate from the $2 it is written with to
// the placeholder a query holds the reader in. A predicate that grew a $2x
// placeholder would be rewritten wrongly and silently, so it stops here
// instead.
func forWikiUserParam(sql, userParam string) string {
	if userParam == "$2" {
		return sql
	}
	if wikiTwoDigitParameter.MatchString(sql) {
		panic("wiki predicate uses a placeholder beginning with $2 and cannot be renumbered")
	}
	return strings.ReplaceAll(sql, "$2", userParam)
}

var wikiTwoDigitParameter = regexp.MustCompile(`\$2\d`)

// insertWikiNotification puts one Confluence notification in someone's inbox
// and marks it to be emailed.
func insertWikiNotification(ctx context.Context, tx pgx.Tx, ws, actor, userID, kind, entityType, entityID, message string) error {
	n := models.Notification{ID: NewID("ntf"), WorkspaceID: ws, TargetUser: userID, ActorID: actor, Kind: kind, EntityType: entityType, EntityID: entityID, Message: message}
	if err := tx.QueryRow(ctx, `SELECT display_name FROM users WHERE id=$1`, actor).Scan(&n.ActorName); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `INSERT INTO notifications(id,workspace_id,user_id,actor_id,kind,entity_type,entity_id,message,email_state)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,'pending') RETURNING to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		n.ID, ws, userID, actor, n.Kind, n.EntityType, n.EntityID, n.Message).Scan(&n.Created); err != nil {
		return err
	}
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(models.NotificationPayload{Notification: n})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: models.EntityNotification, EntityID: n.ID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func notifyWikiMentions(ctx context.Context, tx pgx.Tx, ws, actor, entityType, entityID, title, visibleSQL, visibleID, previousBody, body string) error {
	before := map[string]bool{}
	for _, accountID := range wikimarkup.MentionedAccounts(previousBody) {
		before[accountID] = true
	}
	for _, accountID := range wikimarkup.MentionedAccounts(body) {
		if before[accountID] || accountID == actor {
			continue
		}
		var visible bool
		if err := tx.QueryRow(ctx, visibleSQL, ws, accountID, visibleID).Scan(&visible); err != nil {
			return err
		}
		if !visible {
			continue
		}
		if err := insertWikiNotification(ctx, tx, ws, actor, accountID, "mentioned", entityType, entityID, fmt.Sprintf("Mentioned you in %q.", title)); err != nil {
			return err
		}
	}
	return nil
}

// wikiCommentContainer is what a comment is on, directly or through an
// attachment: a page, a blog post or custom content.
type wikiCommentContainer struct {
	PageID, BlogPostID, CustomContentID, SpaceID, Title string
}

func commentContainer(ctx context.Context, tx pgx.Tx, commentID string) (wikiCommentContainer, error) {
	var c wikiCommentContainer
	err := tx.QueryRow(ctx, `SELECT COALESCE(p.id::text,''),COALESCE(bp.id::text,''),COALESCE(cc.id::text,''),
		COALESCE(p.space_id,bp.space_id,cc.space_id)::text,COALESCE(p.title,bp.title,cc.title,'')
		FROM wiki_footer_comments c
		LEFT JOIN wiki_attachments ca ON ca.id=c.attachment_id
		LEFT JOIN wiki_pages p ON p.id=COALESCE(c.page_id,ca.page_id)
		LEFT JOIN wiki_blog_posts bp ON bp.id=COALESCE(c.blog_post_id,ca.blog_post_id)
		LEFT JOIN wiki_content cc ON cc.id=c.custom_content_id
		WHERE c.id::text=$1`, commentID).Scan(&c.PageID, &c.BlogPostID, &c.CustomContentID, &c.SpaceID, &c.Title)
	return c, err
}

// destination is where a notification about the comment leads.
func (c wikiCommentContainer) destination() (string, string) {
	switch {
	case c.BlogPostID != "":
		return "wiki_blogpost", c.BlogPostID
	case c.PageID != "":
		return "wiki_page", c.PageID
	default:
		return "wiki_content", c.CustomContentID
	}
}

// notifyCommentMentions notifies the people a comment mentions. The
// notification leads to the page or blog post the comment is on.
func notifyCommentMentions(ctx context.Context, tx pgx.Tx, ws, actor, commentID, previousBody, body string) error {
	if len(wikimarkup.MentionedAccounts(body)) == 0 {
		return nil
	}
	container, err := commentContainer(ctx, tx, commentID)
	if err != nil {
		return err
	}
	entityType, entityID := container.destination()
	return notifyWikiMentions(ctx, tx, ws, actor, entityType, entityID, container.Title, wikiCommentReadableBy, commentID, previousBody, body)
}

// notifyCommentWatchers tells the watchers of what a new comment is on.
func notifyCommentWatchers(ctx context.Context, tx pgx.Tx, ws, actor, commentID string) error {
	container, err := commentContainer(ctx, tx, commentID)
	if err != nil {
		return err
	}
	entityType, entityID := container.destination()
	event := wikiWatchEvent{ContentID: entityID, SpaceID: container.SpaceID, EntityType: entityType, EntityID: entityID,
		VisibleSQL: wikiCommentReadableBy, VisibleID: commentID, Message: fmt.Sprintf("Commented on %q.", container.Title)}
	switch entityType {
	case "wiki_page":
		event.Labels, err = wikiLabelNames(ctx, tx, `SELECT l.name FROM wiki_page_labels pl JOIN wiki_labels l ON l.id=pl.label_id WHERE pl.page_id::text=$1`, entityID)
	case "wiki_blogpost":
		event.Labels, err = wikiLabelNames(ctx, tx, `SELECT l.name FROM wiki_blog_post_labels bl JOIN wiki_labels l ON l.id=bl.label_id WHERE bl.blog_post_id::text=$1`, entityID)
	}
	if err != nil {
		return err
	}
	return notifyWikiWatchers(ctx, tx, ws, actor, event)
}
