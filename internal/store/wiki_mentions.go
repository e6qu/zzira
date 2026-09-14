package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/wikimarkup"
	"github.com/jackc/pgx/v5"
)

// Mentioning someone tells them. A page, blog post or comment that names a
// person in a mention notifies that person the first time the mention
// appears, provided they can see what mentioned them; writing your own name
// notifies nobody.

var wikiPageMentionVisible = `SELECT EXISTS(SELECT 1 FROM wiki_pages p JOIN wiki_spaces s ON s.id=p.space_id
	WHERE s.workspace_id=$1 AND ` + wikiSpaceVisible + ` AND ` + wikiPageVisible + ` AND p.id::text=$3)`

var wikiBlogPostMentionVisible = `SELECT EXISTS(SELECT 1 FROM wiki_blog_posts b JOIN wiki_spaces s ON s.id=b.space_id
	WHERE s.workspace_id=$1 AND ` + wikiSpaceVisible + ` AND ` + wikiBlogPostVisible + ` AND b.id::text=$3)`

var wikiCommentMentionVisible = `SELECT EXISTS(` + wikiCommentReadable + `)`

func notifyWikiMentions(ctx context.Context, tx pgx.Tx, ws, actor, entityType, entityID, title, visibleSQL, visibleID, previousBody, body string) error {
	before := map[string]bool{}
	for _, accountID := range wikimarkup.MentionedAccounts(previousBody) {
		before[accountID] = true
	}
	mentioned := wikimarkup.MentionedAccounts(body)
	if len(mentioned) == 0 {
		return nil
	}
	var actorName string
	if err := tx.QueryRow(ctx, `SELECT display_name FROM users WHERE id=$1`, actor).Scan(&actorName); err != nil {
		return err
	}
	for _, accountID := range mentioned {
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
		n := models.Notification{ID: NewID("ntf"), WorkspaceID: ws, TargetUser: accountID, ActorID: actor, ActorName: actorName,
			Kind: "mentioned", EntityType: entityType, EntityID: entityID, Message: fmt.Sprintf("Mentioned you in %q.", title)}
		if err := tx.QueryRow(ctx, `INSERT INTO notifications(id,workspace_id,user_id,actor_id,kind,entity_type,entity_id,message)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
			n.ID, ws, accountID, actor, n.Kind, n.EntityType, n.EntityID, n.Message).Scan(&n.Created); err != nil {
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
		if err := appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: models.EntityNotification, EntityID: n.ID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor}); err != nil {
			return err
		}
	}
	return nil
}

// notifyCommentMentions notifies the people a comment mentions. The
// notification leads to the page or blog post the comment is on.
func notifyCommentMentions(ctx context.Context, tx pgx.Tx, ws, actor, commentID, previousBody, body string) error {
	if len(wikimarkup.MentionedAccounts(body)) == 0 {
		return nil
	}
	var pageID, blogPostID, title string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(p.id::text,''),COALESCE(bp.id::text,''),COALESCE(p.title,bp.title,cc.title,'')
		FROM wiki_footer_comments c
		LEFT JOIN wiki_attachments ca ON ca.id=c.attachment_id
		LEFT JOIN wiki_pages p ON p.id=COALESCE(c.page_id,ca.page_id)
		LEFT JOIN wiki_blog_posts bp ON bp.id=COALESCE(c.blog_post_id,ca.blog_post_id)
		LEFT JOIN wiki_content cc ON cc.id=c.custom_content_id
		WHERE c.id::text=$1`, commentID).Scan(&pageID, &blogPostID, &title); err != nil {
		return err
	}
	entityType, entityID := "wiki_page", pageID
	switch {
	case blogPostID != "":
		entityType, entityID = "wiki_blogpost", blogPostID
	case pageID == "":
		entityType, entityID = "wiki_comment", commentID
	}
	return notifyWikiMentions(ctx, tx, ws, actor, entityType, entityID, title, wikiCommentMentionVisible, commentID, previousBody, body)
}
