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

func (s *Store) validateWikiWatchTarget(ctx context.Context, workspaceID, actorID, targetType, targetID string) (string, error) {
	switch targetType {
	case "content":
		page, err := s.WikiPage(ctx, workspaceID, actorID, targetID)
		if err != nil {
			return "", err
		}
		if page.Status != "current" {
			return "", pgx.ErrNoRows
		}
		return page.ID, nil
	case "space":
		space, err := s.WikiSpaceByKey(ctx, workspaceID, actorID, targetID)
		if err != nil {
			return "", err
		}
		return space.Key, nil
	case "label":
		targetID = strings.ToLower(strings.TrimSpace(targetID))
		labels, err := s.WikiLabels(ctx, workspaceID, actorID)
		if err != nil {
			return "", err
		}
		for _, label := range labels {
			if label.Name == targetID {
				return targetID, nil
			}
		}
		return "", pgx.ErrNoRows
	default:
		return "", fmt.Errorf("%w: unsupported wiki watch target", ErrWikiValidation)
	}
}

func (s *Store) WikiWatchStatus(ctx context.Context, workspaceID, actorID, userID, targetType, targetID string) (bool, error) {
	if err := s.canManageWikiWatch(ctx, workspaceID, actorID, userID); err != nil {
		return false, err
	}
	if _, err := s.MemberByID(ctx, workspaceID, userID); err != nil {
		return false, err
	}
	canonical, err := s.validateWikiWatchTarget(ctx, workspaceID, actorID, targetType, targetID)
	if err != nil {
		return false, err
	}
	var watching bool
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_watches WHERE workspace_id=$1 AND user_id=$2 AND target_type=$3 AND target_id=$4)`, workspaceID, userID, targetType, canonical).Scan(&watching)
	return watching, err
}

func (s *Store) canManageWikiWatch(ctx context.Context, workspaceID, actorID, userID string) error {
	if actorID == userID {
		return nil
	}
	admin, err := s.IsAdmin(ctx, workspaceID, actorID)
	if err != nil {
		return err
	}
	if !admin {
		return ErrProjectPermission
	}
	return nil
}

// SetWikiWatch changes a watch idempotently and emits a private sync action
// only when stored state changes.
func (s *Store) SetWikiWatch(ctx context.Context, workspaceID, actorID, userID, targetType, targetID string, watching bool) error {
	if err := s.canManageWikiWatch(ctx, workspaceID, actorID, userID); err != nil {
		return err
	}
	if _, err := s.MemberByID(ctx, workspaceID, userID); err != nil {
		return err
	}
	canonical, err := s.validateWikiWatchTarget(ctx, workspaceID, actorID, targetType, targetID)
	if err != nil {
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
	canonical, err := s.validateWikiWatchTarget(ctx, workspaceID, actorID, targetType, targetID)
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

// wikiWatchNotifications adds one inbox item per visible watcher even when a
// user watches the page through several matching subscriptions.
func wikiWatchNotifications(ctx context.Context, tx pgx.Tx, workspaceID, actorID string, page *models.WikiPage, isNew bool) error {
	var actorName, spaceKey string
	if err := tx.QueryRow(ctx, `SELECT u.display_name,s.key FROM users u CROSS JOIN wiki_spaces s WHERE u.id=$1 AND s.id::text=$2`, actorID, page.SpaceID).Scan(&actorName, &spaceKey); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT w.user_id
		FROM wiki_watches w
		JOIN memberships m ON m.workspace_id=w.workspace_id AND m.user_id=w.user_id
		JOIN users u ON u.id=w.user_id AND u.active
		JOIN wiki_spaces s ON s.workspace_id=w.workspace_id AND s.id::text=$3
		JOIN wiki_pages p ON p.space_id=s.id AND p.id::text=$4
		WHERE w.workspace_id=$1 AND w.user_id<>$2
		  AND (
		    (w.target_type='content' AND w.target_id=$4)
		    OR (w.target_type='space' AND w.target_id=$5)
		    OR (w.target_type='label' AND EXISTS (
		      SELECT 1 FROM wiki_page_labels pl JOIN wiki_labels l ON l.id=pl.label_id
		      WHERE pl.page_id=p.id AND l.name=w.target_id))
		    OR ($6::boolean AND w.target_type='content' AND w.target_id=NULLIF($7::text,''))
		  )
		  AND EXISTS (SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id AND d.active JOIN directory_users du ON du.directory_id=d.id AND du.user_id=w.user_id AND du.active WHERE si.workspace_id=m.workspace_id)
		  AND (NOT s.private OR s.author_id=w.user_id)
		  AND (
		    p.author_id=w.user_id
		    OR EXISTS (SELECT 1 FROM memberships am WHERE am.workspace_id=w.workspace_id AND am.user_id=w.user_id AND am.role='admin')
		    OR NOT EXISTS (SELECT 1 FROM wiki_page_restrictions r WHERE r.page_id=p.id AND r.operation='read')
		    OR EXISTS (SELECT 1 FROM wiki_page_restrictions r WHERE r.page_id=p.id AND r.operation='read' AND r.subject_type='user' AND r.subject_id=w.user_id)
		    OR EXISTS (SELECT 1 FROM wiki_page_restrictions r JOIN group_members gm ON r.subject_type='group' AND gm.group_id::text=r.subject_id WHERE r.page_id=p.id AND r.operation='read' AND gm.user_id=w.user_id)
		  )
		ORDER BY w.user_id`, workspaceID, actorID, page.SpaceID, page.ID, spaceKey, isNew, page.ParentID)
	if err != nil {
		return err
	}
	var userIDs []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return err
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	message := fmt.Sprintf("Updated wiki page %q.", page.Title)
	if isNew {
		message = fmt.Sprintf("Created wiki page %q.", page.Title)
	}
	for _, userID := range userIDs {
		n := models.Notification{ID: NewID("ntf"), WorkspaceID: workspaceID, TargetUser: userID, ActorID: actorID, ActorName: actorName, Kind: "watched", EntityType: "wiki_page", EntityID: page.ID, Message: message}
		if err := tx.QueryRow(ctx, `INSERT INTO notifications(id,workspace_id,user_id,actor_id,kind,entity_type,entity_id,message) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"')`, n.ID, workspaceID, userID, actorID, n.Kind, n.EntityType, n.EntityID, n.Message).Scan(&n.Created); err != nil {
			return err
		}
		seq, err := nextSeq(ctx, tx, workspaceID)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(models.NotificationPayload{Notification: n})
		if err != nil {
			return err
		}
		if err := appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: seq, EntityType: models.EntityNotification, EntityID: n.ID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}); err != nil {
			return err
		}
	}
	return nil
}
