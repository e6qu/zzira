package web

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/wikimarkup"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type wikiData struct {
	Spaces                                []*models.WikiSpace
	Space                                 *models.WikiSpace
	Pages                                 []*models.WikiPage
	Tree                                  []wikiTreeNode
	Page                                  *models.WikiPage
	Versions                              []models.WikiVersion
	Error                                 string
	CanAdmin                              bool
	Editing                               bool
	SourceMode                            bool
	Query                                 string
	Status                                string
	SpaceName, SpaceKey, SpaceDescription string
	Private                               bool
}

type wikiTreeNode struct {
	Page     *models.WikiPage
	Children []wikiTreeNode
}

func wikiPageTree(pages []*models.WikiPage) []wikiTreeNode {
	byID := map[string]bool{}
	for _, page := range pages {
		byID[page.ID] = true
	}
	children := map[string][]*models.WikiPage{}
	for _, page := range pages {
		parent := page.ParentID
		if !byID[parent] {
			parent = ""
		}
		children[parent] = append(children[parent], page)
	}
	var branch func(string) []wikiTreeNode
	branch = func(parent string) []wikiTreeNode {
		nodes := []wikiTreeNode{}
		for _, page := range children[parent] {
			nodes = append(nodes, wikiTreeNode{Page: page, Children: branch(page.ID)})
		}
		return nodes
	}
	return branch("")
}

func wikiWebError(err error) (int, string) {
	var pgerr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 404, "Wiki content does not exist or you do not have permission to view it."
	case errors.Is(err, store.ErrProjectPermission):
		return 403, err.Error()
	case errors.Is(err, store.ErrWikiConflict):
		return 409, err.Error()
	case errors.Is(err, store.ErrWikiValidation):
		return 400, err.Error()
	case errors.As(err, &pgerr) && pgerr.Code == "23505":
		return 400, "A space with this key or a published page with this title already exists."
	default:
		log.Print("wiki: ", strconv.Quote(err.Error()))
		return 500, "Could not complete the wiki operation."
	}
}

func (h *Handler) WikiHome(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	data := wikiData{}
	admin, err := h.Store.IsAdmin(r.Context(), ws, user.ID)
	if err != nil {
		http.Error(w, "Could not load wiki.", 500)
		return
	}
	data.CanAdmin = admin
	status := 200
	if r.Method == "POST" {
		if !parseForm(w, r) {
			return
		}
		data.SpaceName = r.PostFormValue("name")
		data.SpaceKey = r.PostFormValue("key")
		data.SpaceDescription = r.PostFormValue("description")
		data.Private = r.PostFormValue("private") == "true"
		space, err := h.Commands.CreateWikiSpace(r.Context(), ws, user.ID, data.SpaceKey, data.SpaceName, data.SpaceDescription, data.Private)
		if err == nil {
			redirectLocal(w, r, "/wiki/spaces/"+space.ID)
			return
		}
		status, data.Error = wikiWebError(err)
	}
	data.Spaces, err = h.Store.WikiSpaces(r.Context(), ws, user.ID)
	if err != nil {
		http.Error(w, "Could not load wiki spaces.", 500)
		return
	}
	h.writeWorkspacePageStatus(w, r, "page_wiki_spaces", user, ws, data, "wiki", "", status)
}

func (h *Handler) WikiSpacePage(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "current"
	}
	if status != "current" && status != "draft" && status != "trashed" {
		http.Error(w, "Unknown page status.", 400)
		return
	}
	pages, err := h.Store.WikiPages(r.Context(), ws, user.ID, space.ID, status, "")
	if err != nil {
		http.Error(w, "Could not load pages.", 500)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	filtered := make([]*models.WikiPage, 0, len(pages))
	for _, page := range pages {
		if query == "" || strings.Contains(strings.ToLower(page.Title), strings.ToLower(query)) {
			filtered = append(filtered, page)
		}
	}
	h.writeWorkspacePage(w, r, "page_wiki_space", user, ws, wikiData{Space: space, Pages: filtered, Tree: wikiPageTree(filtered), Query: query, Status: status}, "wiki", "")
}

func (h *Handler) WikiPage(w http.ResponseWriter, r *http.Request) { h.wikiPage(w, r, false) }
func (h *Handler) WikiEdit(w http.ResponseWriter, r *http.Request) { h.wikiPage(w, r, true) }

func (h *Handler) wikiPage(w http.ResponseWriter, r *http.Request, edit bool) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	page := &models.WikiPage{SpaceID: space.ID, ParentID: r.URL.Query().Get("parent"), Status: "current", Body: models.WikiBody{Representation: "storage"}, Version: models.WikiVersion{Number: 1}}
	if id := r.PathValue("page"); id != "" {
		page, err = h.Store.WikiPage(r.Context(), ws, user.ID, id)
		if err != nil {
			status, msg := wikiWebError(err)
			http.Error(w, msg, status)
			return
		}
		if page.SpaceID != space.ID {
			http.NotFound(w, r)
			return
		}
		if edit {
			page.Version.Number++
		}
	}
	data := wikiData{Space: space, Page: page, Editing: edit}
	status := 200
	if r.Method == "POST" {
		if !parseForm(w, r) {
			return
		}
		page.Title = r.PostFormValue("title")
		page.Body.Value = r.PostFormValue("body")
		page.ParentID = r.PostFormValue("parentId")
		page.Status = r.PostFormValue("status")
		page.Version.Message = r.PostFormValue("message")
		version, err := strconv.Atoi(r.PostFormValue("version"))
		if err != nil || version < 1 {
			http.Error(w, "Invalid page version.", 400)
			return
		}
		page.Version.Number = version
		saved, err := h.Commands.SaveWikiPage(r.Context(), ws, user.ID, *page)
		if err == nil {
			redirectLocal(w, r, "/wiki/spaces/"+space.ID+"/pages/"+saved.ID)
			return
		}
		status, data.Error = wikiWebError(err)
	}
	if edit {
		data.Pages, err = h.Store.WikiPages(r.Context(), ws, user.ID, space.ID, "current", "")
		if err != nil {
			http.Error(w, "Could not load parent pages.", 500)
			return
		}
		_, err = wikimarkup.Render(page.Body.Value)
		data.SourceMode = err != nil
	} else {
		data.Versions, err = h.Store.WikiVersions(r.Context(), ws, user.ID, page.ID)
		if err != nil {
			http.Error(w, "Could not load page history.", 500)
			return
		}
	}
	h.writeWorkspacePageStatus(w, r, "page_wiki_page", user, ws, data, "wiki", "", status)
}

func (h *Handler) WikiTrash(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	page, err := h.Store.WikiPage(r.Context(), ws, user.ID, r.PathValue("page"))
	if err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	if page.SpaceID != r.PathValue("space") {
		http.NotFound(w, r)
		return
	}
	if page.Status == "trashed" {
		page.Status = "current"
		if !page.Published {
			page.Status = "draft"
		}
		page.Version.Message = "Restored from trash"
	} else {
		page.Status = "trashed"
		page.Version.Message = "Moved to trash"
	}
	page.Version.Number++
	if _, err := h.Commands.SaveWikiPage(r.Context(), ws, user.ID, *page); err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+page.SpaceID)
}
