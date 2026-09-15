package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

const EntityWikiWatch = "wiki_watch"

// ResolveWikiWatchUser resolves both Cloud account IDs and the deprecated
// username/key selectors, while retaining the workspace directory boundary.
func (s *Store) ResolveWikiWatchUser(ctx context.Context, workspaceID, value string) (*models.User, error) {
	value = strings.TrimSpace(value)
	u := &models.User{Active: true, AccountType: "atlassian"}
	err := s.Pool.QueryRow(ctx, `
		SELECT u.id,u.email,u.display_name,u.time_zone,COALESCE(u.username,split_part(u.email,'@',1))
		FROM memberships m JOIN users u ON u.id=m.user_id
		WHERE m.workspace_id=$1 AND u.active
		  AND (u.id=$2 OR lower(COALESCE(u.username,''))=lower($2) OR lower(u.email)=lower($2) OR lower(split_part(u.email,'@',1))=lower($2))
		  AND EXISTS (
		    SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id AND d.active
		    JOIN directory_users du ON du.directory_id=d.id AND du.user_id=u.id AND du.active
		    WHERE si.workspace_id=m.workspace_id)
		ORDER BY CASE WHEN u.id=$2 THEN 0 ELSE 1 END
		LIMIT 1`, workspaceID, value).Scan(&u.ID, &u.Email, &u.DisplayName, &u.TimeZone, &u.Username)
	return u, err
}

// WikiWatchTarget resolves what a watch points at as the caller sees it: the
// canonical target id and the id of the space it belongs to, empty for label
// watches, which span spaces.
func (s *Store) WikiWatchTarget(ctx context.Context, workspaceID, actorID, targetType, targetID string) (string, string, error) {
	switch targetType {
	case "content":
		kind, err := s.WikiContentKindByID(ctx, workspaceID, targetID)
		if err != nil {
			return "", "", err
		}
		switch kind.Type {
		case "page":
			page, err := s.WikiPage(ctx, workspaceID, actorID, targetID)
			if err != nil {
				return "", "", err
			}
			if page.Status != "current" {
				return "", "", pgx.ErrNoRows
			}
			return page.ID, page.SpaceID, nil
		case "blogpost":
			post, err := s.WikiBlogPost(ctx, workspaceID, actorID, targetID)
			if err != nil {
				return "", "", err
			}
			if post.Status != "current" {
				return "", "", pgx.ErrNoRows
			}
			return post.ID, post.SpaceID, nil
		case "footer-comment", "inline-comment":
			var visible bool
			if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(`+wikiCommentReadable+`)`, workspaceID, actorID, targetID).Scan(&visible); err != nil {
				return "", "", err
			}
			if !visible {
				return "", "", pgx.ErrNoRows
			}
			return "", "", fmt.Errorf("%w: watch the page or blog post a comment is on", ErrWikiValidation)
		case "attachment":
			if _, err := s.WikiAttachment(ctx, workspaceID, actorID, targetID); err != nil {
				return "", "", err
			}
			return "", "", fmt.Errorf("%w: watch the page or blog post an attachment is on", ErrWikiValidation)
		default:
			content, err := s.WikiContent(ctx, workspaceID, actorID, targetID, kind.Type)
			if err != nil {
				return "", "", err
			}
			return content.ID, content.SpaceID, nil
		}
	case "space":
		space, err := s.WikiSpaceByKey(ctx, workspaceID, actorID, targetID)
		if err != nil {
			return "", "", err
		}
		return space.Key, space.ID, nil
	case "label":
		targetID = strings.ToLower(strings.TrimSpace(targetID))
		labels, err := s.WikiLabels(ctx, workspaceID, actorID)
		if err != nil {
			return "", "", err
		}
		for _, label := range labels {
			if label.Name == targetID {
				return targetID, "", nil
			}
		}
		return "", "", pgx.ErrNoRows
	default:
		return "", "", fmt.Errorf("%w: unsupported wiki watch target", ErrWikiValidation)
	}
}

func (s *Store) WikiWatchStatus(ctx context.Context, workspaceID, actorID, userID, targetType, targetID string) (bool, error) {
	canonical, spaceID, err := s.WikiWatchTarget(ctx, workspaceID, actorID, targetType, targetID)
	if err != nil {
		return false, err
	}
	if err := s.canManageWikiWatch(ctx, workspaceID, actorID, userID, spaceID); err != nil {
		return false, err
	}
	if _, err := s.MemberByID(ctx, workspaceID, userID); err != nil {
		return false, err
	}
	var watching bool
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_watches WHERE workspace_id=$1 AND user_id=$2 AND target_type=$3 AND target_id=$4)`, workspaceID, userID, targetType, canonical).Scan(&watching)
	return watching, err
}

// canManageWikiWatch decides who may read or change someone else's watch: a
// site administrator anywhere, or an administrator of the space the watched
// space or content belongs to. Label watches span spaces, so only site
// administrators manage them for others.
func (s *Store) canManageWikiWatch(ctx context.Context, workspaceID, actorID, userID, spaceID string) error {
	if actorID == userID {
		return nil
	}
	admin, err := s.IsAdmin(ctx, workspaceID, actorID)
	if err != nil {
		return err
	}
	if admin {
		return nil
	}
	if spaceID != "" {
		allowed, err := s.CanAdministerWikiSpace(ctx, workspaceID, actorID, spaceID)
		if err != nil {
			return err
		}
		if allowed {
			return nil
		}
	}
	return ErrProjectPermission
}

// SetWikiWatch changes a watch idempotently and emits a private sync action
// only when stored state changes.
func (s *Store) SetWikiWatch(ctx context.Context, workspaceID, actorID, userID, targetType, targetID string, watching bool) error {
	canonical, spaceID, err := s.WikiWatchTarget(ctx, workspaceID, actorID, targetType, targetID)
	if err != nil {
		return err
	}
	if err := s.canManageWikiWatch(ctx, workspaceID, actorID, userID, spaceID); err != nil {
		return err
	}
	if _, err := s.MemberByID(ctx, workspaceID, userID); err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var changed bool
	if watching {
		tag, execErr := tx.Exec(ctx, `INSERT INTO wiki_watches(workspace_id,user_id,target_type,target_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, workspaceID, userID, targetType, canonical)
		err, changed = execErr, tag.RowsAffected() > 0
	} else {
		tag, execErr := tx.Exec(ctx, `DELETE FROM wiki_watches WHERE workspace_id=$1 AND user_id=$2 AND target_type=$3 AND target_id=$4`, workspaceID, userID, targetType, canonical)
		err, changed = execErr, tag.RowsAffected() > 0
	}
	if err != nil {
		return err
	}
	if changed {
		seq, seqErr := nextSeq(ctx, tx, workspaceID)
		if seqErr != nil {
			return seqErr
		}
		payload, marshalErr := json.Marshal(map[string]any{"wiki_watch": map[string]any{"userId": userID, "targetType": targetType, "targetId": canonical, "watching": watching}})
		if marshalErr != nil {
			return marshalErr
		}
		op := models.OpUpsert
		if !watching {
			op = models.OpDelete
		}
		entityID := userID + ":" + targetType + ":" + canonical
		if err := appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: EntityWikiWatch, EntityID: entityID, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) WikiWatchers(ctx context.Context, workspaceID, actorID, targetType, targetID string) ([]*models.User, error) {
	canonical, _, err := s.WikiWatchTarget(ctx, workspaceID, actorID, targetType, targetID)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT u.id,u.email,u.display_name,u.time_zone,COALESCE(u.username,split_part(u.email,'@',1))
		FROM wiki_watches w JOIN memberships m ON m.workspace_id=w.workspace_id AND m.user_id=w.user_id
		JOIN users u ON u.id=w.user_id
		WHERE w.workspace_id=$1 AND w.target_type=$2 AND w.target_id=$3 AND u.active
		  AND EXISTS (SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id AND d.active JOIN directory_users du ON du.directory_id=d.id AND du.user_id=u.id AND du.active WHERE si.workspace_id=m.workspace_id)
		ORDER BY w.created_at,u.id`, workspaceID, targetType, canonical)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []*models.User{}
	for rows.Next() {
		u := &models.User{Active: true, AccountType: "atlassian"}
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.TimeZone, &u.Username); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// wikiWatchEvent is a change watchers hear about: the content that changed,
// the space and labels it is found through, and what the notification leads
// to. ParentID names a page whose watchers hear about a new child.
type wikiWatchEvent struct {
	ContentID, SpaceID, ParentID string
	Labels                       []string
	EntityType, EntityID         string
	VisibleSQL, VisibleID        string
	Message                      string
}

// notifyWikiWatchers adds one inbox item per watcher who can see the change,
// however many of their watches match it, and none for someone this change
// already notified, such as a watcher it mentions.
func notifyWikiWatchers(ctx context.Context, tx pgx.Tx, workspaceID, actorID string, event wikiWatchEvent) error {
	if event.Labels == nil {
		event.Labels = []string{}
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT w.user_id
		FROM wiki_watches w
		JOIN wiki_spaces s ON s.workspace_id=w.workspace_id AND s.id::text=$3
		WHERE w.workspace_id=$1 AND w.user_id<>$2
		  AND ((w.target_type='content' AND (w.target_id=$4 OR w.target_id=NULLIF($5::text,'')))
		    OR (w.target_type='space' AND w.target_id=s.key)
		    OR (w.target_type='label' AND w.target_id=ANY($6::text[])))
		  AND NOT EXISTS (SELECT 1 FROM notifications n WHERE n.workspace_id=w.workspace_id AND n.user_id=w.user_id
		    AND n.entity_type=$7 AND n.entity_id=$8 AND n.created_at=now())
		ORDER BY w.user_id`, workspaceID, actorID, event.SpaceID, event.ContentID, event.ParentID, event.Labels, event.EntityType, event.EntityID)
	if err != nil {
		return err
	}
	userIDs, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, userID := range userIDs {
		var visible bool
		if err := tx.QueryRow(ctx, event.VisibleSQL, workspaceID, userID, event.VisibleID).Scan(&visible); err != nil {
			return err
		}
		if !visible {
			continue
		}
		if err := insertWikiNotification(ctx, tx, workspaceID, actorID, userID, "watched", event.EntityType, event.EntityID, event.Message); err != nil {
			return err
		}
	}
	return nil
}

func wikiLabelNames(ctx context.Context, tx pgx.Tx, sql, contentID string) ([]string, error) {
	rows, err := tx.Query(ctx, sql, contentID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func wikiWatchNotifications(ctx context.Context, tx pgx.Tx, workspaceID, actorID string, page *models.WikiPage, isNew bool) error {
	labels, err := wikiLabelNames(ctx, tx, `SELECT l.name FROM wiki_page_labels pl JOIN wiki_labels l ON l.id=pl.label_id WHERE pl.page_id::text=$1`, page.ID)
	if err != nil {
		return err
	}
	event := wikiWatchEvent{ContentID: page.ID, SpaceID: page.SpaceID, Labels: labels, EntityType: "wiki_page", EntityID: page.ID,
		VisibleSQL: wikiPageReadableBy, VisibleID: page.ID, Message: fmt.Sprintf("Updated wiki page %q.", page.Title)}
	if isNew {
		event.ParentID, event.Message = page.ParentID, fmt.Sprintf("Created wiki page %q.", page.Title)
	}
	return notifyWikiWatchers(ctx, tx, workspaceID, actorID, event)
}

func wikiBlogWatchNotifications(ctx context.Context, tx pgx.Tx, workspaceID, actorID string, post *models.WikiBlogPost, firstPublished bool) error {
	labels, err := wikiLabelNames(ctx, tx, `SELECT l.name FROM wiki_blog_post_labels bl JOIN wiki_labels l ON l.id=bl.label_id WHERE bl.blog_post_id::text=$1`, post.ID)
	if err != nil {
		return err
	}
	event := wikiWatchEvent{ContentID: post.ID, SpaceID: post.SpaceID, Labels: labels, EntityType: "wiki_blogpost", EntityID: post.ID,
		VisibleSQL: wikiBlogPostReadableBy, VisibleID: post.ID, Message: fmt.Sprintf("Updated blog post %q.", post.Title)}
	if firstPublished {
		event.Message = fmt.Sprintf("Published blog post %q.", post.Title)
	}
	return notifyWikiWatchers(ctx, tx, workspaceID, actorID, event)
}
