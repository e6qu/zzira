package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type screenCard struct {
	Screen    *models.Screen
	Available []models.ScreenField
}

type screensData struct {
	Screens []screenCard
	Notice  string
	Error   string
}

func screenMutationMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrScreenValidation), errors.Is(err, store.ErrScreenConflict),
		errors.Is(err, store.ErrScreenNotFound), errors.Is(err, store.ErrProjectPermission):
		return err.Error()
	default:
		return "Could not update screens."
	}
}

func (h *Handler) loadScreensPage(r *http.Request, workspaceID string) (screensData, error) {
	data := screensData{Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	screens, err := h.Store.Screens(r.Context(), workspaceID, store.ScreenFilter{})
	if err != nil {
		return data, err
	}
	for _, listed := range screens {
		expanded, expandErr := h.Store.ExpandedScreen(r.Context(), workspaceID, listed.ID)
		if expandErr != nil {
			return data, expandErr
		}
		available, availableErr := h.Store.AvailableScreenFields(r.Context(), workspaceID, listed.ID)
		if availableErr != nil {
			return data, availableErr
		}
		data.Screens = append(data.Screens, screenCard{Screen: expanded, Available: available})
	}
	return data, nil
}

func (h *Handler) ScreensPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		_, err := h.Store.CreateScreen(r.Context(), workspaceID, user.ID, r.PostFormValue("name"), r.PostFormValue("description"))
		if err != nil {
			redirectLocal(w, r, "/settings/screens?error="+url.QueryEscape(screenMutationMessage(err)))
			return
		}
		redirectLocal(w, r, "/settings/screens?notice="+url.QueryEscape("Screen created."))
		return
	}
	data, err := h.loadScreensPage(r, workspaceID)
	if err != nil {
		http.Error(w, "Could not load screens.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_screens", user, workspaceID, data, "screens", "")
}

func (h *Handler) ScreenMutation(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	screenID := r.PathValue("id")
	if screenID == "" {
		http.NotFound(w, r)
		return
	}
	tabID := r.PostFormValue("tabId")
	notice := "Screen updated."
	var err error
	switch r.PostFormValue("action") {
	case "update":
		name, description := r.PostFormValue("name"), r.PostFormValue("description")
		_, err = h.Store.UpdateScreen(r.Context(), workspaceID, user.ID, screenID, &name, &description)
	case "delete":
		err = h.Store.DeleteScreen(r.Context(), workspaceID, user.ID, screenID)
		notice = "Screen deleted."
	case "add-tab":
		_, err = h.Store.AddScreenTab(r.Context(), workspaceID, user.ID, screenID, r.PostFormValue("name"))
		notice = "Tab added."
	case "rename-tab":
		_, err = h.Store.RenameScreenTab(r.Context(), workspaceID, user.ID, screenID, tabID, r.PostFormValue("name"))
		notice = "Tab renamed."
	case "delete-tab":
		err = h.Store.DeleteScreenTab(r.Context(), workspaceID, user.ID, screenID, tabID)
		notice = "Tab removed."
	case "move-tab":
		var position int
		position, err = strconv.Atoi(r.PostFormValue("position"))
		if err != nil {
			err = store.ErrScreenValidation
		} else {
			err = h.Store.MoveScreenTab(r.Context(), workspaceID, user.ID, screenID, tabID, position)
		}
		notice = "Tab moved."
	case "add-field":
		_, err = h.Store.AddScreenTabField(r.Context(), workspaceID, user.ID, screenID, tabID, r.PostFormValue("fieldId"))
		notice = "Field added to the tab."
	case "remove-field":
		err = h.Store.RemoveScreenTabField(r.Context(), workspaceID, user.ID, screenID, tabID, r.PostFormValue("fieldId"))
		notice = "Field removed from the tab."
	case "move-field":
		err = h.Store.MoveScreenTabField(r.Context(), workspaceID, user.ID, screenID, tabID, r.PostFormValue("fieldId"), "", r.PostFormValue("position"))
		notice = "Field moved."
	default:
		err = store.ErrScreenValidation
	}
	target := "/settings/screens"
	if err != nil {
		redirectLocal(w, r, target+"?error="+url.QueryEscape(screenMutationMessage(err)))
		return
	}
	redirectLocal(w, r, target+"?notice="+url.QueryEscape(notice))
}
