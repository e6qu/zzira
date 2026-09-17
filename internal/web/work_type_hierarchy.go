package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// hierarchyPageData is the work type hierarchy settings page: the site's
// levels from the top down, and the work types that can move between them.
type hierarchyPageData struct {
	Levels    []store.HierarchyLevel
	WorkTypes []models.IssueType
	CanEdit   bool
	Notice    string
	Error     string
}

// HierarchyPage shows the site's work type hierarchy. Any member may read it;
// only a site administrator may change it.
func (h *Handler) HierarchyPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	levels, err := h.Store.HierarchyLevels(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load the work type hierarchy.", http.StatusInternalServerError)
		return
	}
	workTypes, err := h.Store.IssueTypesForWorkspace(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load the work types.", http.StatusInternalServerError)
		return
	}
	movable := make([]models.IssueType, 0, len(workTypes))
	for _, workType := range workTypes {
		if !workType.Subtask {
			movable = append(movable, workType)
		}
	}
	admin, _ := h.Store.IsAdmin(r.Context(), workspaceID, user.ID)
	h.writeWorkspacePage(w, r, "page_hierarchy", user, workspaceID, hierarchyPageData{
		Levels: levels, WorkTypes: movable, CanEdit: admin,
		Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error"),
	}, "hierarchy", "")
}

// HierarchyMutation adds, renames and removes levels, and moves a work type
// to another level.
func (h *Handler) HierarchyMutation(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	level, levelErr := strconv.Atoi(strings.TrimSpace(r.PostFormValue("level")))
	notice := ""
	var err error
	switch r.PostFormValue("action") {
	case "add-level":
		var added store.HierarchyLevel
		added, err = h.Store.AddHierarchyLevel(r.Context(), workspaceID, user.ID, r.PostFormValue("name"))
		if err == nil {
			notice = added.Name + " added above the levels below it."
		}
	case "rename-level":
		if levelErr != nil {
			err = levelErr
			break
		}
		if err = h.Store.RenameHierarchyLevel(r.Context(), workspaceID, user.ID, level, r.PostFormValue("name")); err == nil {
			notice = "Level renamed."
		}
	case "remove-level":
		if levelErr != nil {
			err = levelErr
			break
		}
		if err = h.Store.DeleteHierarchyLevel(r.Context(), workspaceID, user.ID, level); err == nil {
			notice = "Level removed."
		}
	case "move-work-type":
		if levelErr != nil {
			err = levelErr
			break
		}
		var moved models.IssueType
		moved, err = h.Store.SetWorkTypeHierarchyLevel(r.Context(), workspaceID, user.ID, r.PostFormValue("workType"), level)
		if err == nil {
			notice = moved.Name + " moved."
		}
	default:
		http.Error(w, "Unknown hierarchy action.", http.StatusBadRequest)
		return
	}
	if err != nil {
		message := "The change could not be saved."
		switch {
		case errors.Is(err, store.ErrHierarchyValidation), errors.Is(err, store.ErrIssueMetadataNotFound):
			message = strings.TrimPrefix(err.Error(), store.ErrHierarchyValidation.Error()+": ")
		case errors.Is(err, store.ErrProjectPermission):
			http.Error(w, "Only a site administrator can change the work type hierarchy.", http.StatusForbidden)
			return
		}
		redirectLocal(w, r, "/settings/hierarchy?error="+url.QueryEscape(message))
		return
	}
	redirectLocal(w, r, "/settings/hierarchy?notice="+url.QueryEscape(notice))
}
