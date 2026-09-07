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
	Comments                              []wikiCommentNode
	Labels                                []models.WikiLabel
	Attachments                           []*models.WikiAttachment
	Restrictions                          []models.WikiPageRestriction
	RestrictionUsers                      []wikiRestrictionOption
	RestrictionGroups                     []wikiRestrictionOption
	Error                                 string
	CanAdmin                              bool
	CanEdit                               bool
	CanRestrict                           bool
	Editing                               bool
	SourceMode                            bool
	Query                                 string
	Status                                string
	SpaceName, SpaceKey, SpaceDescription string
	Private                               bool
}

type wikiRestrictionOption struct {
	ID, Name     string
	Read, Update bool
}

type wikiTreeNode struct {
	Page     *models.WikiPage
	Children []wikiTreeNode
}

type wikiCommentNode struct {
	Comment     *models.WikiFooterComment
	Replies     []wikiCommentNode
	CanManage   bool
	NextVersion int
	LikeCount   int
	Liked       bool
	Versions    []models.WikiFooterCommentVersion
}

func wikiCommentTree(comments []*models.WikiFooterComment, likes map[string][]string, versions map[string][]models.WikiFooterCommentVersion, userID string, admin bool) []wikiCommentNode {
	children := map[string][]*models.WikiFooterComment{}
	for _, comment := range comments {
		children[comment.ParentCommentID] = append(children[comment.ParentCommentID], comment)
	}
	var branch func(string) []wikiCommentNode
	branch = func(parent string) []wikiCommentNode {
		nodes := []wikiCommentNode{}
		for _, comment := range children[parent] {
			liked := false
			for _, accountID := range likes[comment.ID] {
				if accountID == userID {
					liked = true
					break
				}
			}
			nodes = append(nodes, wikiCommentNode{Comment: comment, Replies: branch(comment.ID), CanManage: admin || comment.AuthorID == userID, NextVersion: comment.Version.Number + 1, LikeCount: len(likes[comment.ID]), Liked: liked, Versions: versions[comment.ID]})
		}
		return nodes
	}
	return branch("")
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
	canEdit := true
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
		allowed, permissionErr := h.Store.CanUpdateWikiPage(r.Context(), ws, user.ID, page.ID)
		if permissionErr != nil {
			http.Error(w, "Could not load page permissions.", 500)
			return
		}
		canEdit = allowed
		if edit && !canEdit {
			http.Error(w, "You do not have permission to edit this page.", 403)
			return
		}
		if edit {
			page.Version.Number++
		}
	}
	data := wikiData{Space: space, Page: page, Editing: edit, CanEdit: canEdit}
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
		data.Restrictions, err = h.Store.WikiPageRestrictions(r.Context(), ws, user.ID, page.ID)
		if err != nil {
			http.Error(w, "Could not load page access.", 500)
			return
		}
		data.CanRestrict, err = h.Store.CanRestrictWikiPage(r.Context(), ws, user.ID, page.ID)
		if err != nil {
			http.Error(w, "Could not load page access.", 500)
			return
		}
		if data.CanRestrict {
			members, memberErr := h.Store.MembersByWorkspace(r.Context(), ws)
			groups, groupErr := h.Store.WikiRestrictionGroups(r.Context(), ws)
			if memberErr != nil || groupErr != nil {
				http.Error(w, "Could not load page access choices.", 500)
				return
			}
			readUsers, updateUsers, readGroups, updateGroups := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
			for _, restriction := range data.Restrictions {
				for _, subject := range restriction.Users {
					if restriction.Operation == "read" {
						readUsers[subject.ID] = true
					} else {
						updateUsers[subject.ID] = true
					}
				}
				for _, subject := range restriction.Groups {
					if restriction.Operation == "read" {
						readGroups[subject.ID] = true
					} else {
						updateGroups[subject.ID] = true
					}
				}
			}
			for _, member := range members {
				data.RestrictionUsers = append(data.RestrictionUsers, wikiRestrictionOption{ID: member.ID, Name: member.DisplayName, Read: readUsers[member.ID], Update: updateUsers[member.ID]})
			}
			for _, group := range groups {
				data.RestrictionGroups = append(data.RestrictionGroups, wikiRestrictionOption{ID: group.ID, Name: group.Name, Read: readGroups[group.ID], Update: updateGroups[group.ID]})
			}
		}
		data.Versions, err = h.Store.WikiVersions(r.Context(), ws, user.ID, page.ID)
		if err != nil {
			http.Error(w, "Could not load page history.", 500)
			return
		}
		data.Labels, err = h.Store.WikiPageLabels(r.Context(), ws, user.ID, page.ID)
		if err != nil {
			http.Error(w, "Could not load page labels.", 500)
			return
		}
		data.Attachments, err = h.Store.WikiAttachments(r.Context(), ws, user.ID, page.ID, "", "", "current")
		if err != nil {
			http.Error(w, "Could not load page attachments.", 500)
			return
		}
		comments, commentErr := h.Store.WikiFooterCommentThread(r.Context(), ws, user.ID, page.ID)
		if commentErr != nil {
			http.Error(w, "Could not load page comments.", 500)
			return
		}
		admin, adminErr := h.Store.IsAdmin(r.Context(), ws, user.ID)
		if adminErr != nil {
			http.Error(w, "Could not load comment permissions.", 500)
			return
		}
		likes, likesErr := h.Store.WikiFooterCommentLikesForPage(r.Context(), ws, user.ID, page.ID)
		if likesErr != nil {
			http.Error(w, "Could not load comment likes.", 500)
			return
		}
		versions, versionsErr := h.Store.WikiFooterCommentVersionsForPage(r.Context(), ws, user.ID, page.ID)
		if versionsErr != nil {
			http.Error(w, "Could not load comment history.", 500)
			return
		}
		data.Comments = wikiCommentTree(comments, likes, versions, user.ID, admin)
	}
	h.writeWorkspacePageStatus(w, r, "page_wiki_page", user, ws, data, "wiki", "", status)
}

func (h *Handler) WikiAttachmentCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, (100<<20)+1)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		http.Error(w, "Invalid attachment upload.", 400)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Choose a file to attach.", 400)
		return
	}
	defer file.Close()
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	a, err := h.Commands.SaveWikiAttachment(r.Context(), ws, user.ID, page.ID, r.PostFormValue("attachmentId"), header.Filename, header.Header.Get("Content-Type"), r.PostFormValue("comment"), r.PostFormValue("message"), false, file)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page)+"#attachment-"+a.ID)
}

func (h *Handler) WikiAttachmentDelete(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	a, err := h.Store.WikiAttachment(r.Context(), ws, user.ID, r.PathValue("attachment"))
	if err != nil || a.PageID != page.ID {
		http.NotFound(w, r)
		return
	}
	if err = h.Commands.DeleteWikiAttachment(r.Context(), ws, user.ID, a.ID); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page)+"#wiki-attachments")
}

func (h *Handler) WikiPageRestrictions(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	input := []models.WikiPageRestriction{{Operation: "read", Users: []models.WikiRestrictionSubject{}, Groups: []models.WikiRestrictionSubject{}}, {Operation: "update", Users: []models.WikiRestrictionSubject{}, Groups: []models.WikiRestrictionSubject{}}}
	for _, id := range r.PostForm["readUsers"] {
		input[0].Users = append(input[0].Users, models.WikiRestrictionSubject{Type: "user", ID: id, AccountID: id})
	}
	for _, id := range r.PostForm["readGroups"] {
		input[0].Groups = append(input[0].Groups, models.WikiRestrictionSubject{Type: "group", ID: id})
	}
	for _, id := range r.PostForm["updateUsers"] {
		input[1].Users = append(input[1].Users, models.WikiRestrictionSubject{Type: "user", ID: id, AccountID: id})
	}
	for _, id := range r.PostForm["updateGroups"] {
		input[1].Groups = append(input[1].Groups, models.WikiRestrictionSubject{Type: "group", ID: id})
	}
	if _, err = h.Commands.SetWikiPageRestrictions(r.Context(), ws, user.ID, page.ID, "replace", input); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page)+"#wiki-access")
}

func (h *Handler) WikiCommentCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	if parentID := r.PostFormValue("parentId"); parentID != "" {
		parent, parentErr := h.Store.WikiFooterComment(r.Context(), ws, user.ID, parentID)
		if parentErr != nil || parent.PageID != page.ID {
			http.NotFound(w, r)
			return
		}
	}
	comment := models.WikiFooterComment{PageID: page.ID, Body: models.WikiBody{Representation: "storage", Value: r.PostFormValue("body")}}
	if parentID := r.PostFormValue("parentId"); parentID != "" {
		comment.PageID = ""
		comment.ParentCommentID = parentID
	}
	created, err := h.Commands.CreateWikiFooterComment(r.Context(), ws, user.ID, comment)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page)+"#comment-"+created.ID)
}

func (h *Handler) WikiPageLabels(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	switch r.PostFormValue("action") {
	case "add":
		values := strings.Split(r.PostFormValue("labels"), ",")
		labels := make([]models.WikiLabel, 0, len(values))
		for _, value := range values {
			labels = append(labels, models.WikiLabel{Name: value, Prefix: "global"})
		}
		_, err = h.Commands.AddWikiPageLabels(r.Context(), ws, user.ID, page.ID, labels)
	case "remove":
		err = h.Commands.RemoveWikiPageLabel(r.Context(), ws, user.ID, page.ID, r.PostFormValue("prefix"), r.PostFormValue("name"))
	default:
		http.Error(w, "Unknown label action.", 400)
		return
	}
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page)+"#wiki-labels")
}

func (h *Handler) WikiCommentUpdate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	version, err := strconv.Atoi(r.PostFormValue("version"))
	if err != nil || version < 2 {
		http.Error(w, "Invalid comment version.", 400)
		return
	}
	existing, err := h.Store.WikiFooterComment(r.Context(), ws, user.ID, r.PathValue("comment"))
	if err != nil || existing.PageID != page.ID {
		http.NotFound(w, r)
		return
	}
	comment, err := h.Commands.UpdateWikiFooterComment(r.Context(), ws, user.ID, models.WikiFooterComment{ID: r.PathValue("comment"), Body: models.WikiBody{Representation: "storage", Value: r.PostFormValue("body")}, Version: models.WikiVersion{Number: version, Message: r.PostFormValue("message")}})
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	if comment.PageID != page.ID {
		http.NotFound(w, r)
		return
	}
	redirectLocal(w, r, wikiPageURL(page)+"#comment-"+comment.ID)
}

func (h *Handler) WikiCommentDelete(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	comment, err := h.Store.WikiFooterComment(r.Context(), ws, user.ID, r.PathValue("comment"))
	if err != nil || comment.PageID != page.ID {
		if err == nil {
			err = pgx.ErrNoRows
		}
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	if err := h.Store.DeleteWikiFooterComment(r.Context(), ws, user.ID, comment.ID); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page)+"#wiki-discussion")
}

func (h *Handler) WikiCommentLike(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	comment, err := h.Store.WikiFooterComment(r.Context(), ws, user.ID, r.PathValue("comment"))
	if err != nil || comment.PageID != page.ID {
		http.NotFound(w, r)
		return
	}
	liked := r.PostFormValue("liked") == "true"
	if err := h.Store.SetWikiFooterCommentLike(r.Context(), ws, user.ID, comment.ID, liked); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page)+"#comment-"+comment.ID)
}

func (h *Handler) wikiPageForComment(r *http.Request, ws, userID string) (*models.WikiPage, error) {
	page, err := h.Store.WikiPage(r.Context(), ws, userID, r.PathValue("page"))
	if err != nil {
		return nil, err
	}
	if page.SpaceID != r.PathValue("space") || page.Status != "current" {
		return nil, pgx.ErrNoRows
	}
	return page, nil
}

func wikiPageURL(page *models.WikiPage) string {
	return "/wiki/spaces/" + page.SpaceID + "/pages/" + page.ID
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
