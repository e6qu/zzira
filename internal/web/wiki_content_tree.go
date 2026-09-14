package web

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// A space's pages, folders, whiteboards, databases and Smart Links form one
// content tree. The space view lists it in tree order, and each node can be
// moved, archived and restored from there; content other than pages can also
// be renamed there.

// wikiContentRow is one node of the tree, flattened in tree order with its
// depth.
type wikiContentRow struct {
	ID, Type, Title, Status, URL string
	Private                      bool
	Depth                        int
}

func (r wikiContentRow) TypeName() string { return wikiContentTypeName(r.Type) }

func (r wikiContentRow) DepthClass() string {
	return "wiki-tree-depth-" + strconv.Itoa(min(r.Depth, 8))
}

func wikiContentTypeName(contentType string) string {
	switch contentType {
	case "page":
		return "Page"
	case "folder":
		return "Folder"
	case "whiteboard":
		return "Whiteboard"
	case "database":
		return "Database"
	case "embed":
		return "Smart Link"
	}
	return contentType
}

// wikiMoveTarget is a place a node can be moved beside or beneath.
type wikiMoveTarget struct {
	ID, Label string
}

type wikiTreeEntry struct {
	row      wikiContentRow
	parent   string
	position int
	order    int64
}

// wikiContentTreeRows arranges pages and content into tree order. A node whose
// parent is not among them is shown at the top level.
func wikiContentTreeRows(pages []*models.WikiPage, contents []*models.WikiContent) []wikiContentRow {
	entries := map[string]wikiTreeEntry{}
	for _, page := range pages {
		order, _ := strconv.ParseInt(page.ID, 10, 64)
		entries[page.ID] = wikiTreeEntry{
			row:    wikiContentRow{ID: page.ID, Type: "page", Title: page.Title, Status: page.Status, URL: "/wiki/spaces/" + page.SpaceID + "/pages/" + page.ID},
			parent: page.ParentID, position: page.Position, order: order,
		}
	}
	for _, content := range contents {
		url := ""
		if content.Status == "current" {
			switch content.Type {
			case "database":
				url = "/wiki/spaces/" + content.SpaceID + "/databases/" + content.ID
			case "whiteboard":
				url = "/wiki/spaces/" + content.SpaceID + "/whiteboards/" + content.ID
			case "embed":
				url = content.EmbedURL
			}
		}
		order, _ := strconv.ParseInt(content.ID, 10, 64)
		entries[content.ID] = wikiTreeEntry{
			row:    wikiContentRow{ID: content.ID, Type: content.Type, Title: content.Title, Status: content.Status, URL: url, Private: content.Private},
			parent: content.ParentID, position: content.Position, order: order,
		}
	}
	children := map[string][]wikiTreeEntry{}
	for _, entry := range entries {
		parent := entry.parent
		if _, known := entries[parent]; !known {
			parent = ""
		}
		children[parent] = append(children[parent], entry)
	}
	for parent := range children {
		siblings := children[parent]
		sort.Slice(siblings, func(i, j int) bool {
			if siblings[i].position != siblings[j].position {
				return siblings[i].position < siblings[j].position
			}
			return siblings[i].order < siblings[j].order
		})
	}
	rows := []wikiContentRow{}
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, entry := range children[parent] {
			row := entry.row
			row.Depth = depth
			rows = append(rows, row)
			walk(row.ID, depth+1)
		}
	}
	walk("", 0)
	return rows
}

// wikiMoveTargetLabel names a target in a picker: its place in the tree, its
// title, and its kind when it is not a page.
func wikiMoveTargetLabel(row wikiContentRow) string {
	label := strings.Repeat("— ", row.Depth) + row.Title
	if row.Type != "page" {
		label += " · " + row.TypeName()
	}
	return label
}

func (h *Handler) wikiSpaceTreeContent(r *http.Request, ws, userID, spaceID string) ([]*models.WikiContent, error) {
	contents := []*models.WikiContent{}
	for _, contentType := range []string{"folder", "whiteboard", "database", "embed"} {
		found, err := h.Store.WikiContents(r.Context(), ws, userID, spaceID, contentType)
		if err != nil {
			return nil, err
		}
		contents = append(contents, found...)
	}
	return contents, nil
}

// wikiTreeNodeAction loads what a content tree action needs. The node must be
// in the space the address names.
func (h *Handler) wikiTreeNodeAction(w http.ResponseWriter, r *http.Request) (string, string, string, string, bool) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return "", "", "", "", false
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return "", "", "", "", false
	}
	nodeID := r.PathValue("node")
	nodeSpace, err := h.Store.WikiTreeNodeSpace(r.Context(), ws, nodeID)
	if err != nil || nodeSpace != space.ID {
		http.NotFound(w, r)
		return "", "", "", "", false
	}
	return user.ID, ws, space.ID, nodeID, true
}

func (h *Handler) finishWikiTreeAction(w http.ResponseWriter, r *http.Request, err error, location string) {
	if err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	redirectLocal(w, r, location)
}

// WikiContentMove moves a node beside or beneath another, or to the top level
// of a space; the address follows it to the space it lands in.
func (h *Handler) WikiContentMove(w http.ResponseWriter, r *http.Request) {
	userID, ws, _, nodeID, ok := h.wikiTreeNodeAction(w, r)
	if !ok {
		return
	}
	targetID := r.PostFormValue("targetId")
	var err error
	switch spaceID, top := strings.CutPrefix(targetID, "space-"); {
	case targetID == "":
		err = fmt.Errorf("%w: choose where to move the content", store.ErrWikiMoveValidation)
	case top:
		_, err = h.Commands.MoveWikiTreeNodeToSpace(r.Context(), ws, userID, nodeID, spaceID)
	default:
		_, err = h.Commands.MoveWikiTreeNode(r.Context(), ws, userID, nodeID, r.PostFormValue("position"), targetID)
	}
	if err != nil {
		h.finishWikiTreeAction(w, r, err, "")
		return
	}
	destination, err := h.Store.WikiTreeNodeSpace(r.Context(), ws, nodeID)
	h.finishWikiTreeAction(w, r, err, "/wiki/spaces/"+destination+"#tree-"+nodeID)
}

// WikiContentArchive archives a node, and what is inside it when asked; the
// space's Archived list is where it can then be found.
func (h *Handler) WikiContentArchive(w http.ResponseWriter, r *http.Request) {
	userID, ws, spaceID, nodeID, ok := h.wikiTreeNodeAction(w, r)
	if !ok {
		return
	}
	_, err := h.Commands.ArchiveWikiTreeNode(r.Context(), ws, userID, nodeID, r.PostFormValue("descendants") == "true")
	h.finishWikiTreeAction(w, r, err, "/wiki/spaces/"+spaceID+"?status=archived#tree-"+nodeID)
}

// WikiContentRestore returns an archived node to the content tree.
func (h *Handler) WikiContentRestore(w http.ResponseWriter, r *http.Request) {
	userID, ws, spaceID, nodeID, ok := h.wikiTreeNodeAction(w, r)
	if !ok {
		return
	}
	_, err := h.Commands.RestoreWikiTreeNode(r.Context(), ws, userID, nodeID, r.PostFormValue("descendants") == "true")
	h.finishWikiTreeAction(w, r, err, "/wiki/spaces/"+spaceID+"#tree-"+nodeID)
}

// WikiContentRename renames a folder, whiteboard, database or Smart Link.
func (h *Handler) WikiContentRename(w http.ResponseWriter, r *http.Request) {
	userID, ws, spaceID, nodeID, ok := h.wikiTreeNodeAction(w, r)
	if !ok {
		return
	}
	_, err := h.Commands.RenameWikiContent(r.Context(), ws, userID, nodeID, r.PostFormValue("title"))
	h.finishWikiTreeAction(w, r, err, "/wiki/spaces/"+spaceID+"#tree-"+nodeID)
}
