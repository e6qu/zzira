package api3

import (
	"net/http"

	"github.com/e6qu/zzira/internal/store"
)

// Jira reports what the caller calls a thing beside what the site calls it:
// `name` stays the site's own word, and `translatedName` is the one in the
// caller's language. The site's word is what JQL searches and what the rest of
// the API takes, so a translation changes what a person reads and nothing else.

// callerMetadataNames is what this site's work types, priorities, resolutions
// and statuses are called in the caller's language: the one they chose, or the
// one their client asked for. It is read once per request, not once per row.
func (h *Handler) callerMetadataNames(r *http.Request, workspaceID, userID string) map[string]store.MetadataTranslation {
	locale := h.Store.LocaleForUser(r.Context(), workspaceID, userID)
	if locale == "" {
		locale = store.NormalizeLocale(acceptLanguageLocale(r))
	}
	if locale == "" {
		return nil
	}
	names, err := h.Store.IssueMetadataNamesInLocale(r.Context(), workspaceID, locale)
	if err != nil {
		return nil
	}
	return names
}

// translatedBean adds the caller's own name for one piece of metadata. A site
// with no translation for it, or a caller with no language, reads the bean it
// was given.
func translatedBean(bean map[string]any, names map[string]store.MetadataTranslation, kind, id string) map[string]any {
	translation, ok := names[kind+":"+id]
	if !ok {
		return bean
	}
	bean["translatedName"] = translation.Name
	if translation.Description != "" {
		bean["translatedDescription"] = translation.Description
	}
	return bean
}
