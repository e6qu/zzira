package store

import (
	"context"
	"fmt"
)

// Confluence shows who else has a page or blog post open, and whether they are
// editing it, and brings new versions and comments to the people reading it.
// An open page reports itself every few seconds; presence that stops being
// reported is gone within a minute.

// wikiPresenceWindow is how long a report keeps someone present.
const wikiPresenceWindow = "45 seconds"

// WikiPresence is one other person with the content open.
type WikiPresence struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	Editing     bool   `json:"editing"`
}

// WikiLiveState is what an open page needs to stay current: who else is
// there, the content's version, and how many comments it has.
type WikiLiveState struct {
	Present       []WikiPresence `json:"present"`
	Version       int            `json:"version"`
	CommentCount  int            `json:"commentCount"`
	LastCommentAt string         `json:"lastCommentAt,omitempty"`
}

// WikiHeartbeat records that the caller has a page or blog post open, editing
// it or not, and reports the content's live state.
func (s *Store) WikiHeartbeat(ctx context.Context, ws, actor, kind, id string, editing bool) (WikiLiveState, error) {
	state := WikiLiveState{Present: []WikiPresence{}}
	column := "page_id"
	switch kind {
	case "page":
		page, err := s.WikiPage(ctx, ws, actor, id)
		if err != nil {
			return state, err
		}
		state.Version = page.Version.Number
	case "blogpost":
		post, err := s.WikiBlogPost(ctx, ws, actor, id)
		if err != nil {
			return state, err
		}
		state.Version, column = post.Version.Number, "blog_post_id"
	default:
		return state, fmt.Errorf("%w: presence is kept for pages and blog posts", ErrWikiValidation)
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM wiki_presence WHERE seen_at < now() - interval '`+wikiPresenceWindow+`'`); err != nil {
		return state, err
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO wiki_presence(workspace_id,content_type,content_id,user_id,editing,seen_at)
		VALUES($1,$2,$3::bigint,$4,$5,now())
		ON CONFLICT (content_type,content_id,user_id) DO UPDATE SET editing=EXCLUDED.editing,seen_at=now()`, ws, kind, id, actor, editing); err != nil {
		return state, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT u.id,u.display_name,p.editing FROM wiki_presence p JOIN users u ON u.id=p.user_id AND u.active
		WHERE p.workspace_id=$1 AND p.content_type=$2 AND p.content_id::text=$3 AND p.user_id<>$4
		ORDER BY p.editing DESC,u.display_name,u.id`, ws, kind, id, actor)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var present WikiPresence
		if err := rows.Scan(&present.AccountID, &present.DisplayName, &present.Editing); err != nil {
			rows.Close()
			return state, err
		}
		state.Present = append(state.Present, present)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return state, err
	}
	err = s.Pool.QueryRow(ctx, `SELECT count(*),COALESCE(to_char(max(updated_at) AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),'')
		FROM wiki_footer_comments WHERE `+column+`::text=$1`, id).Scan(&state.CommentCount, &state.LastCommentAt)
	return state, err
}

// LeaveWikiContent removes the caller's presence from a page or blog post.
func (s *Store) LeaveWikiContent(ctx context.Context, ws, actor, kind, id string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM wiki_presence WHERE workspace_id=$1 AND content_type=$2 AND content_id::text=$3 AND user_id=$4`, ws, kind, id, actor)
	return err
}
