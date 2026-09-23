package web

import (
	"context"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// A site that has been translated shows a person the work type, priority,
// resolution and status in the language they chose, as Jira does. The site's
// own name is unchanged underneath: it is what JQL searches, what a form
// submits and what the REST API takes, so a translation changes the word on
// the page and nothing else.

// readerMetadataNames is what this site's work item metadata is called in the
// reader's language, read once for a page rather than once per row. A reader
// who chose no language, or a site with no translation, reads the site's own
// names.
func (h *Handler) readerMetadataNames(ctx context.Context, workspaceID, userID string) map[string]store.MetadataTranslation {
	locale := h.Store.LocaleForUser(ctx, workspaceID, userID)
	if locale == "" {
		return nil
	}
	names, err := h.Store.IssueMetadataNamesInLocale(ctx, workspaceID, locale)
	if err != nil {
		return nil
	}
	return names
}

func translatedName(names map[string]store.MetadataTranslation, kind, id, name string) string {
	if translation, ok := names[kind+":"+id]; ok && translation.Name != "" {
		return translation.Name
	}
	return name
}

// translateIssue renames one work item's metadata into the reader's language.
func translateIssue(names map[string]store.MetadataTranslation, issue *models.Issue) {
	if len(names) == 0 || issue == nil {
		return
	}
	issue.IssueType.Name = translatedName(names, "issuetype", issue.IssueType.ID, issue.IssueType.Name)
	issue.Status.Name = translatedName(names, "status", issue.Status.ID, issue.Status.Name)
	if issue.Priority != nil {
		issue.Priority.Name = translatedName(names, "priority", issue.Priority.ID, issue.Priority.Name)
	}
	if issue.Resolution != nil {
		issue.Resolution.Name = translatedName(names, "resolution", issue.Resolution.ID, issue.Resolution.Name)
	}
}

// translateIssueView renames everything a work item's page names: the item
// itself, the work under and beside it, and the choices its fields offer.
func translateIssueView(names map[string]store.MetadataTranslation, view *models.IssueView) {
	if len(names) == 0 || view == nil {
		return
	}
	translateIssue(names, &view.Issue)
	for i := range view.Children {
		translateIssue(names, &view.Children[i])
	}
	for i := range view.Links {
		view.Links[i].Status.Name = translatedName(names, "status", view.Links[i].Status.ID, view.Links[i].Status.Name)
	}
	for i := range view.Priorities {
		view.Priorities[i].Name = translatedName(names, "priority", view.Priorities[i].ID, view.Priorities[i].Name)
	}
	for i := range view.Resolutions {
		view.Resolutions[i].Name = translatedName(names, "resolution", view.Resolutions[i].ID, view.Resolutions[i].Name)
	}
}
