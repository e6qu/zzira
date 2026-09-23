package web

import (
	"net/http"
	"strings"

	"github.com/e6qu/zzira/internal/store"
)

// A site speaks the languages its people do. An administrator names a work
// type, a priority, a resolution or a status once per language here, beside
// the name the site itself uses, and each person reads the one in theirs.

// metadataTranslationCard is one row's translations as a settings page shows
// them: what the site calls the thing, what each language calls it, and where
// the forms that change that post to.
type metadataTranslationCard struct {
	Kind         string
	EntityID     string
	Name         string
	Path         string
	Translations []store.MetadataTranslation
}

// metadataTranslationCards builds one card per thing being translated, so a
// page reads every translation of its kind in one query rather than one each.
func metadataTranslationCards(kind, path string, named map[string]string, held map[string][]store.MetadataTranslation) map[string]metadataTranslationCard {
	cards := make(map[string]metadataTranslationCard, len(named))
	for id, name := range named {
		cards[id] = metadataTranslationCard{Kind: kind, EntityID: id, Name: name, Path: path, Translations: held[id]}
	}
	return cards
}

// metadataTranslationAction handles the two actions every metadata settings
// page shares: naming something in a language, and taking that name away. It
// reports whether the request was one of them.
func (h *Handler) metadataTranslationAction(r *http.Request, workspaceID, actorID, kind string) (handled bool, notice string, err error) {
	value := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	switch r.PostFormValue("action") {
	case "translate":
		err = h.Store.SaveIssueMetadataTranslation(r.Context(), workspaceID, actorID, store.MetadataTranslation{
			EntityType: kind, EntityID: value("entityId"), Locale: value("locale"),
			Name: value("translatedName"), Description: value("translatedDescription"),
		})
		return true, "Translation saved.", err
	case "remove-translation":
		err = h.Store.DeleteIssueMetadataTranslation(r.Context(), workspaceID, actorID, kind, value("entityId"), value("locale"))
		return true, "Translation removed.", err
	}
	return false, "", nil
}
