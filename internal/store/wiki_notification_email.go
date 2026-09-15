package store

import (
	"context"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// WikiNotificationPath is where a Confluence notification leads.
func WikiNotificationPath(entityType, entityID string) string {
	switch {
	case entityID == "":
		return "/notifications"
	case entityType == "wiki_page":
		return "/wiki/pages/" + url.PathEscape(entityID)
	case entityType == "wiki_blogpost":
		return "/wiki/blogposts/" + url.PathEscape(entityID)
	default:
		return "/notifications"
	}
}

// WikiNotificationEmailRunner emails Confluence notifications as well as
// showing them in the inbox: each mention or watched change becomes one
// message to the person it is for, linking to what it is about.
type WikiNotificationEmailRunner struct {
	Store   *Store
	BaseURL string
	Poll    time.Duration
	// WorkspaceID limits the runner to one workspace when set.
	WorkspaceID string
}

func (r *WikiNotificationEmailRunner) Run(ctx context.Context) {
	poll := r.Poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	for {
		queued, err := r.QueueEmails(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("queue wiki notification email: %v", err)
		}
		if err == nil && queued > 0 {
			continue
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

type pendingWikiEmail struct {
	id, workspaceID, email, actorName, message, entityType, entityID string
	active                                                           bool
}

// QueueEmails moves a batch of notifications waiting to be emailed into the
// email outbox and reports how many it handled.
func (r *WikiNotificationEmailRunner) QueueEmails(ctx context.Context) (int, error) {
	tx, err := r.Store.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `SELECT n.id,n.workspace_id,u.email,COALESCE(a.display_name,''),n.message,n.entity_type,n.entity_id,u.active
		FROM notifications n JOIN users u ON u.id=n.user_id LEFT JOIN users a ON a.id=n.actor_id
		WHERE n.email_state='pending' AND ($1='' OR n.workspace_id=$1) ORDER BY n.created_at,n.id LIMIT 100 FOR UPDATE OF n SKIP LOCKED`, r.WorkspaceID)
	if err != nil {
		return 0, err
	}
	pending, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (pendingWikiEmail, error) {
		var p pendingWikiEmail
		err := row.Scan(&p.id, &p.workspaceID, &p.email, &p.actorName, &p.message, &p.entityType, &p.entityID, &p.active)
		return p, err
	})
	if err != nil {
		return 0, err
	}
	for _, p := range pending {
		state := "skipped"
		if p.active && p.email != "" {
			subject, body := wikiNotificationEmail(r.BaseURL, p)
			if _, err := tx.Exec(ctx, `INSERT INTO email_outbox(workspace_id,recipient,subject,body,dedupe_key) VALUES($1,$2,$3,$4,$5)
				ON CONFLICT(dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`, p.workspaceID, p.email, subject, body, "wiki-notification:"+p.id); err != nil {
				return 0, err
			}
			state = "queued"
		}
		if _, err := tx.Exec(ctx, `UPDATE notifications SET email_state=$2 WHERE id=$1`, p.id, state); err != nil {
			return 0, err
		}
	}
	return len(pending), tx.Commit(ctx)
}

func wikiNotificationEmail(baseURL string, p pendingWikiEmail) (string, string) {
	summary := strings.TrimSuffix(p.message, ".")
	subject := summary
	if p.actorName != "" && summary != "" {
		subject = p.actorName + " " + strings.ToLower(summary[:1]) + summary[1:]
	}
	subject = strings.NewReplacer("\r", " ", "\n", " ").Replace(subject)
	link := strings.TrimRight(baseURL, "/") + WikiNotificationPath(p.entityType, p.entityID)
	return subject, subject + ".\n\n" + link + "\n"
}
