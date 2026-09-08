package web

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type wikiWhiteboardPageData struct {
	Space      *models.WikiSpace
	Whiteboard *models.WikiContent
	Objects    []models.WikiWhiteboardObject
	Connectors []models.WikiWhiteboardConnector
	CanEdit    bool
}

func (h *Handler) WikiWhiteboardPage(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	space, whiteboard, ok := h.wikiWhiteboardContext(w, r, ws, user.ID)
	if !ok {
		return
	}
	data, err := h.Store.WikiWhiteboardData(r.Context(), ws, user.ID, whiteboard.ID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	canEdit, err := h.Store.CanUpdateWikiContent(r.Context(), ws, user.ID, whiteboard.ID, "whiteboard")
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	h.writeWorkspacePage(w, r, "page_wiki_whiteboard", user, ws, wikiWhiteboardPageData{Space: space, Whiteboard: whiteboard, Objects: data.Objects, Connectors: data.Connectors, CanEdit: canEdit}, "wiki", "")
}

func (h *Handler) WikiWhiteboardObjectSave(w http.ResponseWriter, r *http.Request) {
	user, ws, whiteboard, ok := h.wikiWhiteboardMutationContext(w, r)
	if !ok {
		return
	}
	values := make([]int, 4)
	for index, name := range []string{"x", "y", "width", "height"} {
		value, err := strconv.Atoi(r.PostFormValue(name))
		if err != nil {
			h.finishWikiWhiteboardMutation(w, r, whiteboard, fmt.Errorf("%w: coordinates and dimensions must be integers", store.ErrWikiValidation))
			return
		}
		values[index] = value
	}
	_, err := h.Commands.SaveWikiWhiteboardObject(r.Context(), ws, user.ID, whiteboard.ID, models.WikiWhiteboardObject{
		ID: r.PathValue("object"), Type: r.PostFormValue("type"), Title: r.PostFormValue("title"), Body: r.PostFormValue("body"), Color: r.PostFormValue("color"), X: values[0], Y: values[1], Width: values[2], Height: values[3],
	})
	h.finishWikiWhiteboardMutation(w, r, whiteboard, err)
}

func (h *Handler) WikiWhiteboardObjectDelete(w http.ResponseWriter, r *http.Request) {
	user, ws, whiteboard, ok := h.wikiWhiteboardMutationContext(w, r)
	if !ok {
		return
	}
	err := h.Commands.DeleteWikiWhiteboardObject(r.Context(), ws, user.ID, whiteboard.ID, r.PathValue("object"))
	h.finishWikiWhiteboardMutation(w, r, whiteboard, err)
}

func (h *Handler) WikiWhiteboardConnectorSave(w http.ResponseWriter, r *http.Request) {
	user, ws, whiteboard, ok := h.wikiWhiteboardMutationContext(w, r)
	if !ok {
		return
	}
	_, err := h.Commands.SaveWikiWhiteboardConnector(r.Context(), ws, user.ID, whiteboard.ID, models.WikiWhiteboardConnector{FromObjectID: r.PostFormValue("fromObjectId"), ToObjectID: r.PostFormValue("toObjectId"), Label: r.PostFormValue("label"), Style: r.PostFormValue("style")})
	h.finishWikiWhiteboardMutation(w, r, whiteboard, err)
}

func (h *Handler) WikiWhiteboardConnectorDelete(w http.ResponseWriter, r *http.Request) {
	user, ws, whiteboard, ok := h.wikiWhiteboardMutationContext(w, r)
	if !ok {
		return
	}
	err := h.Commands.DeleteWikiWhiteboardConnector(r.Context(), ws, user.ID, whiteboard.ID, r.PathValue("connector"))
	h.finishWikiWhiteboardMutation(w, r, whiteboard, err)
}

func (h *Handler) wikiWhiteboardMutationContext(w http.ResponseWriter, r *http.Request) (*models.User, string, *models.WikiContent, bool) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return nil, "", nil, false
	}
	_, whiteboard, ok := h.wikiWhiteboardContext(w, r, ws, user.ID)
	return user, ws, whiteboard, ok
}

func (h *Handler) wikiWhiteboardContext(w http.ResponseWriter, r *http.Request, ws, actor string) (*models.WikiSpace, *models.WikiContent, bool) {
	space, err := h.Store.WikiSpace(r.Context(), ws, actor, r.PathValue("space"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return nil, nil, false
	}
	whiteboard, err := h.Store.WikiContent(r.Context(), ws, actor, r.PathValue("whiteboard"), "whiteboard")
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return nil, nil, false
	}
	if whiteboard.SpaceID != space.ID {
		http.NotFound(w, r)
		return nil, nil, false
	}
	return space, whiteboard, true
}

func (h *Handler) finishWikiWhiteboardMutation(w http.ResponseWriter, r *http.Request, whiteboard *models.WikiContent, err error) {
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+whiteboard.SpaceID+"/whiteboards/"+whiteboard.ID)
}
