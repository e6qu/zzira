package automation

import (
	"context"
	"encoding/json"

	"github.com/e6qu/zzira/internal/models"
)

// Rules also run on what happens to the wiki: a page written, changed,
// commented on or labelled, and a blog post published. None of those is a
// work item, so the run carries what it happened to and acts without one --
// most usefully by raising the work the page asks for.

// Entity types in the action log that a wiki trigger reads.
const (
	entityWikiPage          = "wiki_page"
	entityWikiBlogPost      = "wiki_blogpost"
	entityWikiFooterComment = "wiki_footer_comment"
	entityWikiInlineComment = "wiki_inline_comment"
	entityWikiLabel         = "wiki_label"
)

// wikiPagePayload is what the log holds when a page is written.
type wikiPagePayload struct {
	Page *models.WikiPage `json:"wiki_page"`
	// SpaceID is on the payload as well as the page, because a label or a
	// comment carries the space without carrying the page.
	SpaceID string `json:"wikiSpaceId"`
}

type wikiBlogPostPayload struct {
	BlogPost *models.WikiBlogPost `json:"wiki_blogpost"`
	SpaceID  string               `json:"wikiSpaceId"`
}

type wikiCommentPayload struct {
	Footer  *models.WikiFooterComment `json:"wiki_footer_comment"`
	Inline  *models.WikiFooterComment `json:"wiki_inline_comment"`
	SpaceID string                    `json:"wikiSpaceId"`
}

// comment is whichever kind of comment the payload carried.
func (p wikiCommentPayload) comment() *models.WikiFooterComment {
	if p.Footer != nil {
		return p.Footer
	}
	return p.Inline
}

type wikiLabelPayload struct {
	Label struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Prefix   string `json:"prefix"`
		PageID   string `json:"pageId"`
		Attached bool   `json:"attached"`
	} `json:"wiki_label"`
	SpaceID string `json:"wikiSpaceId"`
}

// wikiActionEvent reads a wiki action as the event a rule triggers on, or ""
// when nothing in the catalog happened.
func wikiActionEvent(action loggedAction) string {
	switch action.EntityType {
	case entityWikiPage:
		var payload wikiPagePayload
		if json.Unmarshal(action.Payload, &payload) != nil || payload.Page == nil {
			return ""
		}
		// A draft is not a page anybody can read yet, and a trashed one is
		// not a change to act on.
		if !payload.Page.Published || payload.Page.Status != "current" {
			return ""
		}
		if action.First {
			return "page_created"
		}
		return "page_updated"
	case entityWikiBlogPost:
		var payload wikiBlogPostPayload
		if json.Unmarshal(action.Payload, &payload) != nil || payload.BlogPost == nil {
			return ""
		}
		if !action.First || !payload.BlogPost.Published || payload.BlogPost.Status != "current" {
			return ""
		}
		return "blogpost_created"
	case entityWikiFooterComment, entityWikiInlineComment:
		var payload wikiCommentPayload
		if json.Unmarshal(action.Payload, &payload) != nil {
			return ""
		}
		comment := payload.comment()
		// A comment on a blog post or on custom content is not a page
		// comment, and an edit is not a new one.
		if !action.First || comment == nil || comment.PageID == "" {
			return ""
		}
		return "page_commented"
	case entityWikiLabel:
		var payload wikiLabelPayload
		if json.Unmarshal(action.Payload, &payload) != nil {
			return ""
		}
		// Removing a label is not labelling, and a label on an attachment is
		// not a label on a page.
		if !payload.Label.Attached || payload.Label.PageID == "" {
			return ""
		}
		return "page_labelled"
	}
	return ""
}

// wikiTriggerData is what a wiki run carries: the page, and whatever else the
// event was about, for {{page.title}} and the rest.
func wikiTriggerData(event string, action loggedAction) json.RawMessage {
	var data map[string]any
	switch event {
	case "page_created", "page_updated":
		var payload wikiPagePayload
		if json.Unmarshal(action.Payload, &payload) != nil || payload.Page == nil {
			return nil
		}
		data = map[string]any{"page": payload.Page}
	case "blogpost_created":
		var payload wikiBlogPostPayload
		if json.Unmarshal(action.Payload, &payload) != nil || payload.BlogPost == nil {
			return nil
		}
		data = map[string]any{"blogPost": payload.BlogPost}
	case "page_commented":
		var payload wikiCommentPayload
		comment := payload.comment()
		if json.Unmarshal(action.Payload, &payload) != nil || payload.comment() == nil {
			return nil
		}
		comment = payload.comment()
		data = map[string]any{"comment": comment, "pageId": comment.PageID}
	case "page_labelled":
		var payload wikiLabelPayload
		if json.Unmarshal(action.Payload, &payload) != nil {
			return nil
		}
		data = map[string]any{"label": map[string]string{
			"id": payload.Label.ID, "name": payload.Label.Name, "prefix": payload.Label.Prefix,
		}, "pageId": payload.Label.PageID}
	default:
		return nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	return raw
}

// wikiPageID is the page a wiki run is about, which is the page itself for a
// page event and the page it happened on for a comment or a label.
func wikiPageID(data json.RawMessage) string {
	var carried struct {
		Page struct {
			ID string `json:"id"`
		} `json:"page"`
		PageID string `json:"pageId"`
	}
	if json.Unmarshal(data, &carried) != nil {
		return carried.PageID
	}
	if carried.Page.ID != "" {
		return carried.Page.ID
	}
	return carried.PageID
}

// wikiContentVisible reports whether the rule actor may read what the event
// happened to. A rule runs as its actor, so a page the actor cannot see must
// not reach a rule's actions -- not as a title in a comment, and not as the
// reason work was raised.
func (r *Runner) wikiContentVisible(ctx context.Context, run *claimedRun) (bool, error) {
	// A blog post event carries no page; the blog post itself is read.
	if pageID := wikiPageID(run.TriggerData); pageID != "" {
		page, err := r.Service.Store.WikiPage(ctx, run.WorkspaceID, run.ActorID, pageID)
		if err != nil {
			return false, nil
		}
		return page != nil, nil
	}
	var carried struct {
		BlogPost struct {
			ID string `json:"id"`
		} `json:"blogPost"`
	}
	if json.Unmarshal(run.TriggerData, &carried) != nil || carried.BlogPost.ID == "" {
		return false, nil
	}
	post, err := r.Service.Store.WikiBlogPost(ctx, run.WorkspaceID, run.ActorID, carried.BlogPost.ID)
	if err != nil {
		return false, nil
	}
	return post != nil, nil
}
