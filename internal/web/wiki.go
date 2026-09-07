package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

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
	BlogPosts                             []*models.WikiBlogPost
	Folders                               []*models.WikiContent
	SmartLinks                            []*models.WikiContent
	Databases                             []*models.WikiContent
	Whiteboards                           []*models.WikiContent
	Tree                                  []wikiTreeNode
	Page                                  *models.WikiPage
	BlogPost                              *models.WikiBlogPost
	Versions                              []models.WikiVersion
	Comments                              []wikiCommentNode
	InlineComments                        []wikiCommentNode
	Tasks                                 []*models.WikiTask
	TaskAssignees                         []*models.User
	Labels                                []models.WikiLabel
	BlogProperties                        []models.WikiContentProperty
	BlogLikeCount                         int
	BlogLiked                             bool
	Attachments                           []*models.WikiAttachment
	AttachmentComments                    map[string][]wikiCommentNode
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
	WatchingSpace                         bool
	WatchingPage                          bool
	WatchedLabels                         map[string]bool
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
	case errors.Is(err, store.ErrWikiBlogPostConflict):
		return 409, err.Error()
	case errors.Is(err, store.ErrWikiPropertyConflict):
		return 409, err.Error()
	case errors.Is(err, store.ErrWikiValidation):
		return 400, err.Error()
	case errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "wiki_attachment_properties_attachment_id_key_key":
		return 400, "An attachment property with this key already exists."
	case errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "wiki_content_properties_content_id_key_key":
		return 400, "A content property with this key already exists."
	case errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "wiki_blog_post_properties_blog_post_id_key_key":
		return 400, "A blog post property with this key already exists."
	case errors.As(err, &pgerr) && pgerr.Code == "23505":
		return 400, "A space with this key or published content with this title already exists."
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
	blogPosts, err := h.Store.WikiBlogPosts(r.Context(), ws, user.ID, space.ID, status, "", "-created-date")
	if err != nil {
		http.Error(w, "Could not load blog posts.", 500)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	filtered := make([]*models.WikiPage, 0, len(pages))
	for _, page := range pages {
		if query == "" || strings.Contains(strings.ToLower(page.Title), strings.ToLower(query)) {
			filtered = append(filtered, page)
		}
	}
	filteredBlogs := make([]*models.WikiBlogPost, 0, len(blogPosts))
	for _, post := range blogPosts {
		if query == "" || strings.Contains(strings.ToLower(post.Title), strings.ToLower(query)) {
			filteredBlogs = append(filteredBlogs, post)
		}
	}
	folders := []*models.WikiContent{}
	smartLinks := []*models.WikiContent{}
	databases := []*models.WikiContent{}
	whiteboards := []*models.WikiContent{}
	if status == "current" {
		folders, err = h.Store.WikiContents(r.Context(), ws, user.ID, space.ID, "folder")
		if err != nil {
			http.Error(w, "Could not load folders.", 500)
			return
		}
		if query != "" {
			visible := folders[:0]
			for _, folder := range folders {
				if strings.Contains(strings.ToLower(folder.Title), strings.ToLower(query)) {
					visible = append(visible, folder)
				}
			}
			folders = visible
		}
		smartLinks, err = h.Store.WikiContents(r.Context(), ws, user.ID, space.ID, "embed")
		if err != nil {
			http.Error(w, "Could not load Smart Links.", 500)
			return
		}
		if query != "" {
			visible := smartLinks[:0]
			for _, link := range smartLinks {
				if strings.Contains(strings.ToLower(link.Title), strings.ToLower(query)) || strings.Contains(strings.ToLower(link.EmbedURL), strings.ToLower(query)) {
					visible = append(visible, link)
				}
			}
			smartLinks = visible
		}
		databases, err = h.Store.WikiContents(r.Context(), ws, user.ID, space.ID, "database")
		if err != nil {
			http.Error(w, "Could not load databases.", 500)
			return
		}
		if query != "" {
			visible := databases[:0]
			for _, database := range databases {
				if strings.Contains(strings.ToLower(database.Title), strings.ToLower(query)) {
					visible = append(visible, database)
				}
			}
			databases = visible
		}
		whiteboards, err = h.Store.WikiContents(r.Context(), ws, user.ID, space.ID, "whiteboard")
		if err != nil {
			http.Error(w, "Could not load whiteboards.", 500)
			return
		}
		if query != "" {
			visible := whiteboards[:0]
			for _, whiteboard := range whiteboards {
				if strings.Contains(strings.ToLower(whiteboard.Title), strings.ToLower(query)) || strings.Contains(strings.ToLower(whiteboard.TemplateKey), strings.ToLower(query)) {
					visible = append(visible, whiteboard)
				}
			}
			whiteboards = visible
		}
	}
	watching, err := h.Store.WikiWatchStatus(r.Context(), ws, user.ID, user.ID, "space", space.Key)
	if err != nil {
		http.Error(w, "Could not load space watch status.", 500)
		return
	}
	h.writeWorkspacePage(w, r, "page_wiki_space", user, ws, wikiData{Space: space, Pages: filtered, BlogPosts: filteredBlogs, Folders: folders, SmartLinks: smartLinks, Databases: databases, Whiteboards: whiteboards, Tree: wikiPageTree(filtered), Query: query, Status: status, WatchingSpace: watching}, "wiki", "")
}

func (h *Handler) WikiBlogPostNew(w http.ResponseWriter, r *http.Request) {
	h.wikiBlogPost(w, r, true)
}

func (h *Handler) WikiBlogPostPage(w http.ResponseWriter, r *http.Request) {
	h.wikiBlogPost(w, r, false)
}

func (h *Handler) wikiBlogPost(w http.ResponseWriter, r *http.Request, creating bool) {
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
	post := &models.WikiBlogPost{SpaceID: space.ID, Status: "current", Body: models.WikiBody{Representation: "storage"}, Version: models.WikiVersion{Number: 1}}
	if !creating {
		post, err = h.Store.WikiBlogPost(r.Context(), ws, user.ID, r.PathValue("blogpost"))
		if err != nil {
			status, message := wikiWebError(err)
			http.Error(w, message, status)
			return
		}
		if post.SpaceID != space.ID {
			http.NotFound(w, r)
			return
		}
	}
	editing := creating || r.URL.Query().Get("edit") == "true"
	pageStatus := 200
	errorMessage := ""
	if r.Method == "POST" {
		if !parseForm(w, r) {
			return
		}
		post.Title = r.PostFormValue("title")
		post.Body = models.WikiBody{Representation: "storage", Value: r.PostFormValue("body")}
		post.Status = r.PostFormValue("status")
		post.Version.Message = r.PostFormValue("message")
		post.Version.MinorEdit = r.PostFormValue("minorEdit") == "true"
		if creating {
			post.Private = r.PostFormValue("private") == "true"
		} else {
			post.Version.Number++
		}
		saved, saveErr := h.Commands.SaveWikiBlogPost(r.Context(), ws, user.ID, *post)
		if saveErr == nil {
			redirectLocal(w, r, "/wiki/spaces/"+space.ID+"/blogposts/"+saved.ID)
			return
		}
		pageStatus, errorMessage = wikiWebError(saveErr)
		editing = true
	}
	versions := []models.WikiVersion{}
	labels := []models.WikiLabel{}
	properties := []models.WikiContentProperty{}
	likeCount, liked := 0, false
	if post.ID != "" {
		versions, err = h.Store.WikiBlogPostVersions(r.Context(), ws, user.ID, post.ID, "-modified-date")
		if err != nil {
			http.Error(w, "Could not load blog post history.", 500)
			return
		}
		if post.Status == "current" {
			labels, err = h.Store.WikiBlogPostLabels(r.Context(), ws, user.ID, post.ID)
			if err != nil {
				http.Error(w, "Could not load blog post labels.", 500)
				return
			}
			properties, err = h.Store.WikiBlogPostProperties(r.Context(), ws, user.ID, post.ID, "")
			if err != nil {
				http.Error(w, "Could not load blog post properties.", 500)
				return
			}
			for i := range properties {
				properties[i].NextVersion = properties[i].Version.Number + 1
			}
			likes, likeErr := h.Store.WikiBlogPostLikes(r.Context(), ws, user.ID, post.ID)
			if likeErr != nil {
				http.Error(w, "Could not load blog post likes.", 500)
				return
			}
			likeCount = len(likes)
			for _, accountID := range likes {
				if accountID == user.ID {
					liked = true
					break
				}
			}
		}
	}
	h.writeWorkspacePageStatus(w, r, "page_wiki_blogpost", user, ws, wikiData{Space: space, BlogPost: post, Versions: versions, Labels: labels, BlogProperties: properties, BlogLikeCount: likeCount, BlogLiked: liked, Editing: editing, CanEdit: true, Error: errorMessage}, "wiki", "", pageStatus)
}

func (h *Handler) WikiBlogPostMetadata(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	post, err := h.Store.WikiBlogPost(r.Context(), ws, user.ID, r.PathValue("blogpost"))
	if err != nil || post.SpaceID != space.ID || post.Status != "current" {
		http.NotFound(w, r)
		return
	}
	action := r.PostFormValue("action")
	switch action {
	case "like":
		liked, parseErr := strconv.ParseBool(r.PostFormValue("liked"))
		if parseErr != nil {
			err = fmt.Errorf("%w: liked must be true or false", store.ErrWikiValidation)
		} else {
			err = h.Store.SetWikiBlogPostLike(r.Context(), ws, user.ID, post.ID, liked)
		}
	case "add-label":
		_, err = h.Commands.AddWikiBlogPostLabels(r.Context(), ws, user.ID, post.ID, []models.WikiLabel{{Name: r.PostFormValue("label"), Prefix: "global"}})
	case "remove-label":
		err = h.Commands.RemoveWikiBlogPostLabel(r.Context(), ws, user.ID, post.ID, r.PostFormValue("prefix"), r.PostFormValue("label"))
	case "classify":
		_, err = h.Commands.SetWikiBlogPostClassification(r.Context(), ws, user.ID, post.ID, r.PostFormValue("level"))
	case "create-property":
		_, err = h.Commands.CreateWikiBlogPostProperty(r.Context(), ws, user.ID, post.ID, r.PostFormValue("key"), json.RawMessage(r.PostFormValue("value")))
	case "update-property":
		version, parseErr := strconv.Atoi(r.PostFormValue("version"))
		if parseErr != nil {
			err = fmt.Errorf("%w: property version must be an integer", store.ErrWikiValidation)
		} else {
			_, err = h.Commands.UpdateWikiBlogPostProperty(r.Context(), ws, user.ID, post.ID, r.PostFormValue("propertyId"), r.PostFormValue("key"), json.RawMessage(r.PostFormValue("value")), version, r.PostFormValue("message"))
		}
	case "delete-property":
		err = h.Commands.DeleteWikiBlogPostProperty(r.Context(), ws, user.ID, post.ID, r.PostFormValue("propertyId"))
	default:
		err = fmt.Errorf("%w: choose a blog post metadata action", store.ErrWikiValidation)
	}
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID+"/blogposts/"+post.ID)
}

func (h *Handler) WikiBlogPostLifecycle(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	post, err := h.Store.WikiBlogPost(r.Context(), ws, user.ID, r.PathValue("blogpost"))
	if err != nil || post.SpaceID != space.ID {
		http.NotFound(w, r)
		return
	}
	action := r.PostFormValue("action")
	if action == "purge" {
		err = h.Commands.PurgeWikiBlogPost(r.Context(), ws, user.ID, post.ID)
	} else {
		post.Version.Number++
		if action == "restore" {
			post.Status, post.Version.Message = "current", "Restored"
		} else {
			post.Status, post.Version.Message = "trashed", "Moved to trash"
		}
		_, err = h.Commands.SaveWikiBlogPost(r.Context(), ws, user.ID, *post)
	}
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	if action == "restore" {
		redirectLocal(w, r, "/wiki/spaces/"+space.ID+"/blogposts/"+post.ID)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID+"?status=trashed")
}

func (h *Handler) WikiFolderCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	folder, err := h.Commands.CreateWikiContent(r.Context(), ws, user.ID, models.WikiContent{Type: "folder", SpaceID: space.ID, Title: r.PostFormValue("title"), ParentID: r.PostFormValue("parentId")})
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID+"#folder-"+folder.ID)
}

func (h *Handler) WikiFolderDelete(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	folder, err := h.Store.WikiContent(r.Context(), ws, user.ID, r.PathValue("folder"), "folder")
	if err != nil || folder.SpaceID != space.ID {
		http.NotFound(w, r)
		return
	}
	if err = h.Commands.DeleteWikiContent(r.Context(), ws, user.ID, folder.ID, "folder"); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID)
}

func (h *Handler) WikiSmartLinkCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	link, err := h.Commands.CreateWikiContent(r.Context(), ws, user.ID, models.WikiContent{Type: "embed", SpaceID: space.ID, Title: r.PostFormValue("title"), ParentID: r.PostFormValue("parentId"), EmbedURL: r.PostFormValue("embedUrl")})
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID+"#embed-"+link.ID)
}

func (h *Handler) WikiSmartLinkDelete(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	link, err := h.Store.WikiContent(r.Context(), ws, user.ID, r.PathValue("embed"), "embed")
	if err != nil || link.SpaceID != space.ID {
		http.NotFound(w, r)
		return
	}
	if err = h.Commands.DeleteWikiContent(r.Context(), ws, user.ID, link.ID, "embed"); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID)
}

func (h *Handler) WikiDatabaseCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	database, err := h.Commands.CreateWikiContent(r.Context(), ws, user.ID, models.WikiContent{
		Type: "database", SpaceID: space.ID, Title: r.PostFormValue("title"),
		ParentID: r.PostFormValue("parentId"), Private: r.PostFormValue("private") == "true",
	})
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID+"#database-"+database.ID)
}

func (h *Handler) WikiDatabaseDelete(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	database, err := h.Store.WikiContent(r.Context(), ws, user.ID, r.PathValue("database"), "database")
	if err != nil || database.SpaceID != space.ID {
		http.NotFound(w, r)
		return
	}
	if err = h.Commands.DeleteWikiContent(r.Context(), ws, user.ID, database.ID, "database"); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID)
}

func (h *Handler) WikiDatabaseClassification(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	database, err := h.Store.WikiContent(r.Context(), ws, user.ID, r.PathValue("database"), "database")
	if err != nil || database.SpaceID != space.ID {
		http.NotFound(w, r)
		return
	}
	if _, err = h.Commands.SetWikiContentClassification(r.Context(), ws, user.ID, database.ID, "database", r.PostFormValue("level")); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID+"#database-"+database.ID)
}

func (h *Handler) WikiWhiteboardCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	whiteboard, err := h.Commands.CreateWikiContent(r.Context(), ws, user.ID, models.WikiContent{
		Type: "whiteboard", SpaceID: space.ID, Title: r.PostFormValue("title"), ParentID: r.PostFormValue("parentId"),
		Private: r.PostFormValue("private") == "true", TemplateKey: r.PostFormValue("templateKey"), Locale: r.PostFormValue("locale"),
	})
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID+"#whiteboard-"+whiteboard.ID)
}

func (h *Handler) WikiWhiteboardDelete(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	whiteboard, err := h.Store.WikiContent(r.Context(), ws, user.ID, r.PathValue("whiteboard"), "whiteboard")
	if err != nil || whiteboard.SpaceID != space.ID {
		http.NotFound(w, r)
		return
	}
	if err = h.Commands.DeleteWikiContent(r.Context(), ws, user.ID, whiteboard.ID, "whiteboard"); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID)
}

func (h *Handler) WikiWhiteboardClassification(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	whiteboard, err := h.Store.WikiContent(r.Context(), ws, user.ID, r.PathValue("whiteboard"), "whiteboard")
	if err != nil || whiteboard.SpaceID != space.ID {
		http.NotFound(w, r)
		return
	}
	if _, err = h.Commands.SetWikiContentClassification(r.Context(), ws, user.ID, whiteboard.ID, "whiteboard", r.PostFormValue("level")); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID+"#whiteboard-"+whiteboard.ID)
}

func (h *Handler) WikiPage(w http.ResponseWriter, r *http.Request) { h.wikiPage(w, r, false) }
func (h *Handler) WikiEdit(w http.ResponseWriter, r *http.Request) { h.wikiPage(w, r, true) }

func (h *Handler) WikiPageRedirect(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	page, err := h.Store.WikiPage(r.Context(), ws, user.ID, r.PathValue("page"))
	if err != nil || page.Status != "current" {
		http.NotFound(w, r)
		return
	}
	redirectLocal(w, r, wikiPageURL(page))
}

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
		if page.Status == "current" {
			data.WatchingPage, err = h.Store.WikiWatchStatus(r.Context(), ws, user.ID, user.ID, "content", page.ID)
			if err != nil {
				http.Error(w, "Could not load page watch status.", 500)
				return
			}
			data.WatchedLabels = map[string]bool{}
			for _, label := range data.Labels {
				if _, seen := data.WatchedLabels[label.Name]; seen {
					continue
				}
				data.WatchedLabels[label.Name], err = h.Store.WikiWatchStatus(r.Context(), ws, user.ID, user.ID, "label", label.Name)
				if err != nil {
					http.Error(w, "Could not load label watch status.", 500)
					return
				}
			}
		}
		data.Attachments, err = h.Store.WikiAttachments(r.Context(), ws, user.ID, page.ID, "", "", "current")
		if err != nil {
			http.Error(w, "Could not load page attachments.", 500)
			return
		}
		wikiAdmin, adminErr := h.Store.IsAdmin(r.Context(), ws, user.ID)
		if adminErr != nil {
			http.Error(w, "Could not load comment permissions.", 500)
			return
		}
		data.AttachmentComments = map[string][]wikiCommentNode{}
		for _, attachment := range data.Attachments {
			attachment.Labels, err = h.Store.WikiAttachmentLabels(r.Context(), ws, user.ID, attachment.ID)
			if err != nil {
				http.Error(w, "Could not load attachment labels.", 500)
				return
			}
			attachment.Properties, err = h.Store.WikiAttachmentProperties(r.Context(), ws, user.ID, attachment.ID, "")
			if err != nil {
				http.Error(w, "Could not load attachment properties.", 500)
				return
			}
			for i := range attachment.Properties {
				attachment.Properties[i].NextVersion = attachment.Properties[i].Version.Number + 1
			}
			attachmentComments, commentErr := h.Store.WikiAttachmentFooterCommentThread(r.Context(), ws, user.ID, attachment.ID)
			if commentErr != nil {
				http.Error(w, "Could not load attachment comments.", 500)
				return
			}
			attachmentLikes, commentErr := h.Store.WikiFooterCommentLikesForAttachment(r.Context(), ws, user.ID, attachment.ID)
			if commentErr != nil {
				http.Error(w, "Could not load attachment comment likes.", 500)
				return
			}
			attachmentVersions, commentErr := h.Store.WikiFooterCommentVersionsForAttachment(r.Context(), ws, user.ID, attachment.ID)
			if commentErr != nil {
				http.Error(w, "Could not load attachment comment history.", 500)
				return
			}
			data.AttachmentComments[attachment.ID] = wikiCommentTree(attachmentComments, attachmentLikes, attachmentVersions, user.ID, wikiAdmin)
		}
		comments, commentErr := h.Store.WikiFooterCommentThread(r.Context(), ws, user.ID, page.ID)
		if commentErr != nil {
			http.Error(w, "Could not load page comments.", 500)
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
		data.Comments = wikiCommentTree(comments, likes, versions, user.ID, wikiAdmin)
		inlineComments, inlineErr := h.Store.WikiInlineCommentThread(r.Context(), ws, user.ID, page.ID)
		if inlineErr != nil {
			http.Error(w, "Could not load inline comments.", 500)
			return
		}
		inlineLikes := map[string][]string{}
		inlineVersions := map[string][]models.WikiFooterCommentVersion{}
		for _, comment := range inlineComments {
			inlineLikes[comment.ID], inlineErr = h.Store.WikiInlineCommentLikes(r.Context(), ws, user.ID, comment.ID)
			if inlineErr == nil {
				inlineVersions[comment.ID], inlineErr = h.Store.WikiInlineCommentVersions(r.Context(), ws, user.ID, comment.ID)
			}
			if inlineErr != nil {
				http.Error(w, "Could not load inline comment details.", 500)
				return
			}
		}
		data.InlineComments = wikiCommentTree(inlineComments, inlineLikes, inlineVersions, user.ID, wikiAdmin)
		data.Tasks, err = h.Store.WikiTasks(r.Context(), ws, user.ID, store.WikiTaskFilter{PageIDs: []string{page.ID}, IncludeBlank: true})
		if err != nil {
			http.Error(w, "Could not load page tasks.", 500)
			return
		}
		if canEdit && page.Status == "current" {
			data.TaskAssignees, err = h.Store.MembersByWorkspace(r.Context(), ws)
			if err != nil {
				http.Error(w, "Could not load task assignees.", 500)
				return
			}
		}
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

func (h *Handler) WikiAttachmentMetadata(w http.ResponseWriter, r *http.Request) {
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
	attachmentID := r.PathValue("attachment")
	attachment, err := h.Store.WikiAttachment(r.Context(), ws, user.ID, attachmentID)
	if err != nil || attachment.PageID != page.ID {
		http.NotFound(w, r)
		return
	}
	switch r.PostFormValue("action") {
	case "add-labels":
		values := strings.Split(r.PostFormValue("labels"), ",")
		labels := make([]models.WikiLabel, 0, len(values))
		for _, value := range values {
			labels = append(labels, models.WikiLabel{Name: value, Prefix: "global"})
		}
		_, err = h.Commands.AddWikiAttachmentLabels(r.Context(), ws, user.ID, attachmentID, labels)
	case "remove-label":
		err = h.Commands.RemoveWikiAttachmentLabel(r.Context(), ws, user.ID, attachmentID, r.PostFormValue("prefix"), r.PostFormValue("name"))
	case "create-property":
		_, err = h.Commands.CreateWikiAttachmentProperty(r.Context(), ws, user.ID, attachmentID, r.PostFormValue("key"), json.RawMessage(r.PostFormValue("value")))
	case "update-property":
		version, versionErr := strconv.Atoi(r.PostFormValue("version"))
		if versionErr != nil {
			http.Error(w, "Invalid property version.", 400)
			return
		}
		_, err = h.Commands.UpdateWikiAttachmentProperty(r.Context(), ws, user.ID, attachmentID, r.PostFormValue("propertyId"), r.PostFormValue("key"), json.RawMessage(r.PostFormValue("value")), version, r.PostFormValue("message"))
	case "delete-property":
		err = h.Commands.DeleteWikiAttachmentProperty(r.Context(), ws, user.ID, attachmentID, r.PostFormValue("propertyId"))
	default:
		http.Error(w, "Unknown attachment metadata action.", 400)
		return
	}
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page)+"#attachment-"+attachmentID)
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
	} else if attachmentID := r.PostFormValue("attachmentId"); attachmentID != "" {
		attachment, attachmentErr := h.Store.WikiAttachment(r.Context(), ws, user.ID, attachmentID)
		if attachmentErr != nil || attachment.PageID != page.ID {
			http.NotFound(w, r)
			return
		}
		comment.PageID = ""
		comment.AttachmentID = attachmentID
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

func (h *Handler) WikiInlineCommentCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	comment := models.WikiFooterComment{PageID: page.ID, Body: models.WikiBody{Representation: "storage", Value: r.PostFormValue("body")}}
	if parentID := r.PostFormValue("parentId"); parentID != "" {
		comment.PageID, comment.ParentCommentID = "", parentID
	} else {
		comment.InlineSelection = r.PostFormValue("selection")
		comment.InlineMatchCount = strings.Count(page.Body.Value, comment.InlineSelection)
	}
	created, err := h.Commands.CreateWikiInlineComment(r.Context(), ws, user.ID, comment)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page)+"#inline-comment-"+created.ID)
}

func (h *Handler) WikiInlineCommentUpdate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	comment, err := h.Store.WikiInlineComment(r.Context(), ws, user.ID, r.PathValue("comment"))
	if err != nil || comment.PageID != page.ID {
		http.NotFound(w, r)
		return
	}
	resolved := r.PostFormValue("resolved") == "true"
	comment.Version.Number++
	if _, err = h.Commands.UpdateWikiInlineComment(r.Context(), ws, user.ID, *comment, &resolved); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page)+"#inline-comment-"+comment.ID)
}

func (h *Handler) WikiInlineCommentDelete(w http.ResponseWriter, r *http.Request) {
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
	comment, err := h.Store.WikiInlineComment(r.Context(), ws, user.ID, r.PathValue("comment"))
	if err != nil || comment.PageID != page.ID {
		http.NotFound(w, r)
		return
	}
	if err = h.Store.DeleteWikiInlineComment(r.Context(), ws, user.ID, comment.ID); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page)+"#wiki-inline-comments")
}

func (h *Handler) WikiTaskCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	text := strings.TrimSpace(r.PostFormValue("body"))
	if text == "" {
		http.Error(w, "Task text is required.", 400)
		return
	}
	dueAt := ""
	if raw := r.PostFormValue("dueAt"); raw != "" {
		due, parseErr := time.Parse("2006-01-02", raw)
		if parseErr != nil {
			http.Error(w, "Choose a valid task due date.", 400)
			return
		}
		dueAt = due.UTC().Format(time.RFC3339)
	}
	task, err := h.Commands.CreateWikiTask(r.Context(), ws, user.ID, models.WikiTask{
		PageID: page.ID, AssignedTo: r.PostFormValue("assignedTo"), DueAt: dueAt,
		Body: models.WikiBody{Representation: "storage", Value: "<p>" + html.EscapeString(text) + "</p>"},
	})
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page)+"#wiki-task-"+task.ID)
}

func (h *Handler) WikiTaskUpdate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	task, err := h.Store.WikiTask(r.Context(), ws, user.ID, r.PathValue("task"))
	if err != nil || task.PageID != page.ID {
		http.NotFound(w, r)
		return
	}
	if _, err = h.Commands.UpdateWikiTask(r.Context(), ws, user.ID, task.ID, r.PostFormValue("status")); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page)+"#wiki-task-"+task.ID)
}

func (h *Handler) WikiSpaceWatch(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err == nil {
		err = h.Commands.SetWikiWatch(r.Context(), ws, user.ID, user.ID, "space", space.Key, r.PostFormValue("watching") == "true")
	}
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID)
}

func (h *Handler) WikiPageWatch(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err == nil {
		err = h.Commands.SetWikiWatch(r.Context(), ws, user.ID, user.ID, "content", page.ID, r.PostFormValue("watching") == "true")
	}
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page))
}

func (h *Handler) WikiLabelWatch(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err == nil {
		err = h.Commands.SetWikiWatch(r.Context(), ws, user.ID, user.ID, "label", r.PathValue("label"), r.PostFormValue("watching") == "true")
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
