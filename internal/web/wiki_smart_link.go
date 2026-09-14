package web

import (
	"net/http"
	"net/url"

	"github.com/e6qu/zzira/internal/models"
)

// A Smart Link is shown as a card: its title, where it points, and a preview
// of the destination when the destination is served over HTTPS.

type wikiSmartLinkPageData struct {
	Space       *models.WikiSpace
	Link        *models.WikiContent
	Host        string
	ParentTitle string
	Previewable bool
	CanRestore  bool
}

func (h *Handler) WikiSmartLinkPage(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	link, err := h.Store.WikiTreeContent(r.Context(), ws, user.ID, r.PathValue("embed"), "embed")
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	if link.SpaceID != space.ID {
		http.NotFound(w, r)
		return
	}
	data := wikiSmartLinkPageData{Space: space, Link: link}
	if parsed, parseErr := url.Parse(link.EmbedURL); parseErr == nil && link.EmbedURL != "" {
		data.Host = parsed.Host
		data.Previewable = parsed.Scheme == "https" && link.Status == "current"
	}
	if link.ParentID != "" {
		ancestors, ancestorErr := h.Store.WikiTreeAncestors(r.Context(), ws, user.ID, link.ID, "embed")
		if ancestorErr != nil {
			status, message := wikiWebError(ancestorErr)
			http.Error(w, message, status)
			return
		}
		if len(ancestors) > 0 {
			data.ParentTitle = ancestors[len(ancestors)-1].Title()
		}
	}
	if link.Status == "archived" {
		if data.CanRestore, err = h.Store.CanCreateWikiPage(r.Context(), ws, user.ID, space.ID); err != nil {
			status, message := wikiWebError(err)
			http.Error(w, message, status)
			return
		}
	}
	h.writeWorkspacePage(w, r, "page_wiki_smart_link", user, ws, data, "wiki", "")
}

// WikiPageFavourite stars or unstars a page.
func (h *Handler) WikiPageFavourite(w http.ResponseWriter, r *http.Request) {
	page, userID, ws, ok := h.wikiPageAction(w, r)
	if !ok {
		return
	}
	err := h.Commands.SetWikiPageFavourite(r.Context(), ws, userID, page.ID, r.PostFormValue("favourite") == "true")
	h.finishWikiTreeAction(w, r, err, "/wiki/spaces/"+page.SpaceID+"/pages/"+page.ID)
}

// WikiPageOwner hands a page to another member.
func (h *Handler) WikiPageOwner(w http.ResponseWriter, r *http.Request) {
	page, userID, ws, ok := h.wikiPageAction(w, r)
	if !ok {
		return
	}
	_, err := h.Commands.TransferWikiPageOwnership(r.Context(), ws, userID, page.ID, r.PostFormValue("ownerId"))
	h.finishWikiTreeAction(w, r, err, "/wiki/spaces/"+page.SpaceID+"/pages/"+page.ID)
}

// wikiReadableContent loads a whiteboard or database for reading. An archived
// one can still be opened; it is read-only there.
func (h *Handler) wikiReadableContent(w http.ResponseWriter, r *http.Request, ws, actor, pathKey, contentType string) (*models.WikiSpace, *models.WikiContent, bool) {
	space, err := h.Store.WikiSpace(r.Context(), ws, actor, r.PathValue("space"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return nil, nil, false
	}
	content, err := h.Store.WikiTreeContent(r.Context(), ws, actor, r.PathValue(pathKey), contentType)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return nil, nil, false
	}
	if content.SpaceID != space.ID {
		http.NotFound(w, r)
		return nil, nil, false
	}
	return space, content, true
}
