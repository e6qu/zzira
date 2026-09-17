package automation

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// WikiPageActionType is Jira Automation's create page action, which raises a
// page in a space the rule names.
const WikiPageActionType = "confluence.page.create"

// createWikiPage raises a page as the rule actor, so a rule writes only where
// its actor may write and only in a space its actor can see.
func (r *Runner) createWikiPage(ctx context.Context, run *claimedRun, issue *models.Issue, spaceKey, title string, render func(string) (string, error)) (bool, error) {
	key, err := render(spaceKey)
	if err != nil {
		return false, err
	}
	key = strings.TrimSpace(key)
	space, err := r.Service.Store.WikiSpaceByKey(ctx, run.WorkspaceID, run.ActorID, key)
	if err != nil {
		return false, fmt.Errorf("no space %q the rule actor can see", key)
	}
	allowed, err := r.Service.Store.CanCreateWikiPage(ctx, run.WorkspaceID, run.ActorID, space.ID)
	if err != nil {
		return false, err
	}
	if !allowed {
		return false, errors.New("the rule actor may not create pages in that space")
	}

	heading := strings.TrimSpace(title)
	if heading == "" && issue != nil {
		heading = issue.Key + " " + issue.Summary
	}
	heading, err = render(heading)
	if err != nil {
		return false, err
	}
	if heading = strings.TrimSpace(heading); heading == "" {
		return false, errors.New("create page action rendered an empty title")
	}

	// The body names the work the page was raised for, as Jira's page carries
	// the work item it came from. Storage is the only representation the wiki
	// takes, so the text is escaped into it.
	body := "<p>Raised by " + html.EscapeString(run.RuleName) + ".</p>"
	if issue != nil {
		body = "<p>Raised by " + html.EscapeString(run.RuleName) + " for " + html.EscapeString(issue.Key) + ": " + html.EscapeString(issue.Summary) + ".</p>"
	}
	page := models.WikiPage{
		SpaceID: space.ID, Status: "current", Title: heading,
		Body:    models.WikiBody{Representation: "storage", Value: body},
		Version: models.WikiVersion{Number: 1},
	}
	saved, err := r.Service.Commands.SaveWikiPage(ctx, run.WorkspaceID, run.ActorID, page)
	if err != nil {
		return false, err
	}
	return saved != nil, nil
}
