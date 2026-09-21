package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// A rule that the wiki can start should be able to answer in the wiki: say
// what it did under the page that asked, and label the page so people can
// find what the rule sorted.

const (
	// WikiCommentActionType comments on a page, as a person would.
	WikiCommentActionType = "confluence.page.comment"
	// WikiLabelActionType attaches a label to a page.
	WikiLabelActionType = "confluence.page.label"
)

// wikiPageActionValue is what both actions carry: which page, and what to
// write on it. A blank pageId means the page the run is about, which is what
// a rule the wiki started is usually answering.
type wikiPageActionValue struct {
	PageID  string `json:"pageId"`
	Comment string `json:"comment"`
	Label   string `json:"label"`
	Prefix  string `json:"prefix"`
}

// wikiActionPage is the page an action acts on: the one it names, else the one
// the run is about. A rule that has neither says so rather than guessing.
func (r *Runner) wikiActionPage(ctx context.Context, run *claimedRun, named string, render func(string) (string, error)) (*models.WikiPage, error) {
	id, err := render(named)
	if err != nil {
		return nil, err
	}
	if id = strings.TrimSpace(id); id == "" {
		id = wikiPageID(run.TriggerData)
	}
	if id == "" {
		return nil, errors.New("this action needs the page to act on, and the trigger was not about one")
	}
	page, err := r.Service.Store.WikiPage(ctx, run.WorkspaceID, run.ActorID, id)
	if err != nil {
		return nil, fmt.Errorf("no page %q the rule actor can see", id)
	}
	return page, nil
}

// commentOnWikiPage writes a comment as the rule actor, so a rule comments
// only where its actor may comment.
func (r *Runner) commentOnWikiPage(ctx context.Context, run *claimedRun, value wikiPageActionValue, render func(string) (string, error)) (bool, error) {
	page, err := r.wikiActionPage(ctx, run, value.PageID, render)
	if err != nil {
		return false, err
	}
	text, err := render(value.Comment)
	if err != nil {
		return false, err
	}
	if text = strings.TrimSpace(text); text == "" {
		return false, errors.New("comment on page action rendered an empty comment")
	}
	// Storage is the only representation the wiki takes, so the text is
	// escaped into it rather than trusted as markup.
	body := "<p>" + html.EscapeString(text) + "</p>"
	if _, err := r.Service.Commands.CreateWikiFooterComment(ctx, run.WorkspaceID, run.ActorID, models.WikiFooterComment{
		PageID: page.ID, Body: models.WikiBody{Representation: "storage", Value: body},
	}); err != nil {
		return false, err
	}
	return true, nil
}

// labelWikiPage attaches a label as the rule actor. A label the page already
// carries is not a change, as adding a label a work item already has is not.
func (r *Runner) labelWikiPage(ctx context.Context, run *claimedRun, value wikiPageActionValue, render func(string) (string, error)) (bool, error) {
	page, err := r.wikiActionPage(ctx, run, value.PageID, render)
	if err != nil {
		return false, err
	}
	name, err := render(value.Label)
	if err != nil {
		return false, err
	}
	if name = strings.TrimSpace(name); name == "" {
		return false, errors.New("label page action rendered an empty label")
	}
	prefix := strings.TrimSpace(value.Prefix)
	if prefix == "" {
		prefix = "global"
	}
	held, err := r.Service.Store.WikiPageLabels(ctx, run.WorkspaceID, run.ActorID, page.ID)
	if err != nil {
		return false, err
	}
	for _, label := range held {
		if label.Name == name && label.Prefix == prefix {
			return false, nil
		}
	}
	if _, err := r.Service.Commands.AddWikiPageLabels(ctx, run.WorkspaceID, run.ActorID, page.ID, []models.WikiLabel{{Name: name, Prefix: prefix}}); err != nil {
		return false, err
	}
	return true, nil
}

// wikiPageActionValueOf reads either action's value.
func wikiPageActionValueOf(raw json.RawMessage) (wikiPageActionValue, error) {
	var value wikiPageActionValue
	if err := json.Unmarshal(decodeComponentValue(raw), &value); err != nil {
		return value, errors.New("a wiki page action takes an object")
	}
	return value, nil
}
