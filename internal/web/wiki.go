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
	"unicode/utf8"

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
	ContentTree                           []wikiContentRow
	TreeTitles                            map[string]string
	TreeTargets                           []wikiMoveTarget
	CanEditTree                           bool
	ParentContent                         []wikiMoveTarget
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
	PageLikeCount                         int
	PageLiked                             bool
	PageProperties                        []models.WikiContentProperty
	AppByline                             []models.AppModule
	SpaceProperties                       []models.WikiContentProperty
	SpaceRoles                            []*models.WikiSpaceRole
	SpaceRoleAssignments                  []models.WikiSpaceRoleAssignment
	SpaceRoleUsers                        []*models.User
	SpaceRoleGroups                       []models.WikiRestrictionSubject
	SpaceRoleNames                        map[string]string
	SpaceRolePrincipalNames               map[string]string
	Attachments                           []*models.WikiAttachment
	AttachmentComments                    map[string][]wikiCommentNode
	Restrictions                          []models.WikiPageRestriction
	RestrictionUsers                      []wikiRestrictionOption
	RestrictionGroups                     []wikiRestrictionOption
	Error                                 string
	CanAdmin                              bool
	CanManageSpace                        bool
	CanEdit                               bool
	CanRestrict                           bool
	Editing                               bool
	SourceMode                            bool
	MentionPeople                         []*models.User
	ClassificationLevels                  []models.DataClassificationLevel
	ClassificationNames                   map[string]string
	PublishedClassification               map[string]bool
	Query                                 string
	Status                                string
	SpaceName, SpaceKey, SpaceDescription string
	Private                               bool
	WatchingSpace                         bool
	WatchingPage                          bool
	WatchingBlogPost                      bool
	WatchedLabels                         map[string]bool
	MoveTargets                           []wikiMoveGroup
	Starred                               []*models.WikiPage
	PageFavourite                         bool
	PageOwnerName                         string
	OwnerChoices                          []*models.User
	Draft                                 *store.WikiContentDraft
	CanPurge                              bool
	ChildPageCount                        int
	ArchivedChildCount                    int
}

// wikiMoveGroup is one space's worth of pages a page can be moved beside or
// beneath.
type wikiMoveGroup struct {
	Space   *models.WikiSpace
	Targets []wikiMoveTarget
}

type wikiRestrictionOption struct {
	ID, Name     string
	Read, Update bool
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
	case errors.Is(err, store.ErrWikiValidation), errors.Is(err, store.ErrWikiMoveValidation):
		return 400, err.Error()
	case errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "wiki_attachment_properties_attachment_id_key_key":
		return 400, "An attachment property with this key already exists."
	case errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "wiki_content_properties_content_id_key_key":
		return 400, "A content property with this key already exists."
	case errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "wiki_blog_post_properties_blog_post_id_key_key":
		return 400, "A blog post property with this key already exists."
	case errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "wiki_database_columns_database_id_column_key_key":
		return 400, "A database column with this key already exists."
	case errors.As(err, &pgerr) && pgerr.Code == "23505" && pgerr.ConstraintName == "wiki_database_views_database_id_name_key":
		return 400, "A saved database view with this name already exists."
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
	data.Starred, err = h.Store.WikiFavouritePages(r.Context(), ws, user.ID)
	if err != nil {
		http.Error(w, "Could not load starred pages.", 500)
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
	if status != "current" && status != "draft" && status != "archived" && status != "trashed" {
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
	treeContents := []*models.WikiContent{}
	switch status {
	case "current":
		treeContents = append(treeContents, folders...)
		treeContents = append(treeContents, smartLinks...)
		treeContents = append(treeContents, databases...)
		treeContents = append(treeContents, whiteboards...)
	case "archived":
		for _, contentType := range []string{"folder", "whiteboard", "database", "embed"} {
			archived, archivedErr := h.Store.WikiContentsWithStatus(r.Context(), ws, user.ID, space.ID, contentType, "archived")
			if archivedErr != nil {
				http.Error(w, "Could not load archived content.", 500)
				return
			}
			for _, content := range archived {
				if query == "" || strings.Contains(strings.ToLower(content.Title), strings.ToLower(query)) {
					treeContents = append(treeContents, content)
				}
			}
		}
	}
	contentTree := wikiContentTreeRows(filtered, treeContents)
	treeTitles := map[string]string{}
	for _, page := range pages {
		treeTitles[page.ID] = page.Title
	}
	treeTargets := []wikiMoveTarget{}
	for _, row := range contentTree {
		treeTitles[row.ID] = row.Title
		if row.Status == "current" {
			treeTargets = append(treeTargets, wikiMoveTarget{ID: row.ID, Label: wikiMoveTargetLabel(row)})
		}
	}
	canEditTree, err := h.Store.CanCreateWikiPage(r.Context(), ws, user.ID, space.ID)
	if err != nil {
		http.Error(w, "Could not load space permissions.", 500)
		return
	}
	watching, err := h.Store.WikiWatchStatus(r.Context(), ws, user.ID, user.ID, "space", space.Key)
	if err != nil {
		http.Error(w, "Could not load space watch status.", 500)
		return
	}
	admin, err := h.Store.IsAdmin(r.Context(), ws, user.ID)
	if err != nil {
		http.Error(w, "Could not load space permissions.", 500)
		return
	}
	canManageSpace, err := h.Store.CanAdministerWikiSpace(r.Context(), ws, user.ID, space.ID)
	if err != nil {
		http.Error(w, "Could not load space permissions.", 500)
		return
	}
	properties, err := h.Store.WikiSpaceProperties(r.Context(), ws, user.ID, space.ID, "")
	if err != nil {
		http.Error(w, "Could not load space properties.", 500)
		return
	}
	roles, err := h.Store.WikiSpaceRoles(r.Context(), ws, user.ID)
	if err != nil {
		http.Error(w, "Could not load space roles.", 500)
		return
	}
	assignments, err := h.Store.WikiSpaceRoleAssignments(r.Context(), ws, user.ID, space.ID)
	if err != nil {
		http.Error(w, "Could not load space role assignments.", 500)
		return
	}
	roleNames := make(map[string]string, len(roles))
	for _, role := range roles {
		roleNames[role.ID] = role.Name
	}
	principalNames := map[string]string{
		"authenticated-users": "Authenticated users",
		"all-licensed-users":  "All licensed users",
		"all-product-admins":  "All product administrators",
		"jsm-project-admins":  "JSM project administrators",
		"anonymous-users":     "Anonymous users",
	}
	roleUsers := []*models.User{}
	roleGroups := []models.WikiRestrictionSubject{}
	roleUsers, err = h.Store.MembersByWorkspace(r.Context(), ws)
	if err != nil {
		http.Error(w, "Could not load space role users.", 500)
		return
	}
	roleGroups, err = h.Store.WikiRestrictionGroups(r.Context(), ws)
	if err != nil {
		http.Error(w, "Could not load space role groups.", 500)
		return
	}
	for _, member := range roleUsers {
		principalNames[member.ID] = member.DisplayName
	}
	for _, group := range roleGroups {
		principalNames[group.ID] = group.Name
	}
	classLevels, classNames, classPublished, err := h.classificationChoices(r, ws)
	if err != nil {
		http.Error(w, "Could not load classification levels.", 500)
		return
	}
	h.writeWorkspacePage(w, r, "page_wiki_space", user, ws, wikiData{Space: space, Pages: filtered, BlogPosts: filteredBlogs, Folders: folders, SmartLinks: smartLinks, Databases: databases, Whiteboards: whiteboards, ContentTree: contentTree, TreeTitles: treeTitles, TreeTargets: treeTargets, CanEditTree: canEditTree, Query: query, Status: status, WatchingSpace: watching, CanAdmin: admin, CanManageSpace: canManageSpace, SpaceProperties: properties, SpaceRoles: roles, SpaceRoleAssignments: assignments, SpaceRoleUsers: roleUsers, SpaceRoleGroups: roleGroups, SpaceRoleNames: roleNames, SpaceRolePrincipalNames: principalNames, ClassificationLevels: classLevels, ClassificationNames: classNames, PublishedClassification: classPublished}, "wiki", "")
}

func (h *Handler) WikiSpaceClassification(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Commands.SetWikiSpaceDefaultClassification(r.Context(), ws, user.ID, r.PathValue("space"), r.PostFormValue("level"))
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID)
}

func (h *Handler) WikiSpaceProperty(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	spaceID := r.PathValue("space")
	var err error
	switch r.PostFormValue("action") {
	case "create":
		_, err = h.Commands.CreateWikiSpaceProperty(r.Context(), ws, user.ID, spaceID, r.PostFormValue("key"), json.RawMessage(r.PostFormValue("value")))
	case "update":
		version, parseErr := strconv.Atoi(r.PostFormValue("version"))
		if parseErr != nil {
			err = fmt.Errorf("%w: property version must be an integer", store.ErrWikiValidation)
		} else {
			_, err = h.Commands.UpdateWikiSpaceProperty(r.Context(), ws, user.ID, spaceID, r.PostFormValue("propertyId"), r.PostFormValue("key"), json.RawMessage(r.PostFormValue("value")), version, r.PostFormValue("message"))
		}
	case "delete":
		err = h.Commands.DeleteWikiSpaceProperty(r.Context(), ws, user.ID, spaceID, r.PostFormValue("propertyId"))
	default:
		err = fmt.Errorf("%w: choose a space property action", store.ErrWikiValidation)
	}
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+spaceID+"#wiki-space-properties")
}

func (h *Handler) WikiSpaceRoleCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	if _, err := h.Commands.CreateWikiSpaceRole(r.Context(), ws, user.ID, r.PostFormValue("name"), r.PostFormValue("description"), r.PostForm["permissions"]); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+r.PathValue("space")+"#wiki-space-roles")
}

func (h *Handler) WikiSpaceRoleAssignments(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	spaceID := r.PathValue("space")
	assignments, err := h.Store.WikiSpaceRoleAssignments(r.Context(), ws, user.ID, spaceID)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	principalType := r.PostFormValue("principalType")
	if principalType == "" {
		principalType = "ACCESS_CLASS"
	}
	target := models.WikiSpaceRoleAssignment{SpaceID: spaceID, RoleID: r.PostFormValue("roleId"), PrincipalType: principalType, PrincipalID: r.PostFormValue("principalId")}
	action := r.PostFormValue("action")
	if action == "" {
		action = "add"
	}
	switch action {
	case "add":
		found := false
		for _, assignment := range assignments {
			if assignment.RoleID == target.RoleID && assignment.PrincipalType == target.PrincipalType && assignment.PrincipalID == target.PrincipalID {
				found = true
				break
			}
		}
		if !found {
			assignments = append(assignments, target)
		}
	case "remove":
		kept := assignments[:0]
		for _, assignment := range assignments {
			if assignment.RoleID != target.RoleID || assignment.PrincipalType != target.PrincipalType || assignment.PrincipalID != target.PrincipalID {
				kept = append(kept, assignment)
			}
		}
		assignments = kept
	default:
		err = fmt.Errorf("%w: choose a role assignment action", store.ErrWikiValidation)
	}
	if err == nil {
		err = h.Commands.SetWikiSpaceRoleAssignments(r.Context(), ws, user.ID, spaceID, assignments)
	}
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+spaceID+"#wiki-space-roles")
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
	attachments := []*models.WikiAttachment{}
	blogComments, blogInlineComments := []wikiCommentNode{}, []wikiCommentNode{}
	likeCount, liked, watching := 0, false, false
	blogTasks := []*models.WikiTask{}
	if post.ID != "" {
		versions, err = h.Store.WikiBlogPostVersions(r.Context(), ws, user.ID, post.ID, "-modified-date")
		if err != nil {
			http.Error(w, "Could not load blog post history.", 500)
			return
		}
		if post.Status == "current" {
			attachments, err = h.Store.WikiBlogAttachments(r.Context(), ws, user.ID, post.ID, "", "", "current")
			if err != nil {
				http.Error(w, "Could not load blog post attachments.", 500)
				return
			}
			footerThread, commentErr := h.Store.WikiBlogFooterCommentThread(r.Context(), ws, user.ID, post.ID)
			inlineThread, inlineErr := h.Store.WikiBlogInlineCommentThread(r.Context(), ws, user.ID, post.ID)
			wikiAdmin, adminErr := h.Store.IsAdmin(r.Context(), ws, user.ID)
			if commentErr != nil || inlineErr != nil || adminErr != nil {
				http.Error(w, "Could not load blog post discussions.", 500)
				return
			}
			footerLikes, footerVersions := map[string][]string{}, map[string][]models.WikiFooterCommentVersion{}
			for _, comment := range footerThread {
				footerLikes[comment.ID], commentErr = h.Store.WikiFooterCommentLikes(r.Context(), ws, user.ID, comment.ID)
				if commentErr == nil {
					footerVersions[comment.ID], commentErr = h.Store.WikiFooterCommentVersions(r.Context(), ws, user.ID, comment.ID)
				}
				if commentErr != nil {
					http.Error(w, "Could not load blog comment details.", 500)
					return
				}
			}
			inlineLikes, inlineVersions := map[string][]string{}, map[string][]models.WikiFooterCommentVersion{}
			for _, comment := range inlineThread {
				inlineLikes[comment.ID], inlineErr = h.Store.WikiInlineCommentLikes(r.Context(), ws, user.ID, comment.ID)
				if inlineErr == nil {
					inlineVersions[comment.ID], inlineErr = h.Store.WikiInlineCommentVersions(r.Context(), ws, user.ID, comment.ID)
				}
				if inlineErr != nil {
					http.Error(w, "Could not load blog inline comment details.", 500)
					return
				}
			}
			blogComments = wikiCommentTree(footerThread, footerLikes, footerVersions, user.ID, wikiAdmin)
			blogInlineComments = wikiCommentTree(inlineThread, inlineLikes, inlineVersions, user.ID, wikiAdmin)
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
			watching, err = h.Store.WikiWatchStatus(r.Context(), ws, user.ID, user.ID, "content", post.ID)
			if err != nil {
				http.Error(w, "Could not load blog post watch.", 500)
				return
			}
			blogTasks, err = h.Store.WikiTasks(r.Context(), ws, user.ID, store.WikiTaskFilter{BlogPostIDs: []string{post.ID}, IncludeBlank: true})
			if err != nil {
				http.Error(w, "Could not load blog post tasks.", 500)
				return
			}
			for _, accountID := range likes {
				if accountID == user.ID {
					liked = true
					break
				}
			}
		}
	}
	if !editing && r.Method == http.MethodGet && post.ID != "" && post.Status == "current" {
		h.recordWikiView(r, ws, user.ID, "blogpost", post.ID)
	}
	people, err := h.wikiMentions(r, ws, &post.Body, blogComments, blogInlineComments)
	if err != nil {
		http.Error(w, "Could not load people to mention.", 500)
		return
	}
	classLevels, classNames, classPublished, err := h.classificationChoices(r, ws)
	if err != nil {
		http.Error(w, "Could not load classification levels.", 500)
		return
	}
	h.writeWorkspacePageStatus(w, r, "page_wiki_blogpost", user, ws, wikiData{Space: space, BlogPost: post, Versions: versions, Labels: labels, BlogProperties: properties, BlogLikeCount: likeCount, BlogLiked: liked, Attachments: attachments, Comments: blogComments, InlineComments: blogInlineComments, Editing: editing, CanEdit: true, Error: errorMessage, MentionPeople: people, WatchingBlogPost: watching, Tasks: blogTasks, ClassificationLevels: classLevels, ClassificationNames: classNames, PublishedClassification: classPublished}, "wiki", "", pageStatus)
}

func (h *Handler) wikiBlogForDiscussion(w http.ResponseWriter, r *http.Request, ws, userID string) (*models.WikiBlogPost, bool) {
	post, err := h.Store.WikiBlogPost(r.Context(), ws, userID, r.PathValue("blogpost"))
	if err != nil || post.SpaceID != r.PathValue("space") || post.Status != "current" {
		http.NotFound(w, r)
		return nil, false
	}
	return post, true
}

func (h *Handler) WikiBlogCommentCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	post, ok := h.wikiBlogForDiscussion(w, r, ws, user.ID)
	if !ok {
		return
	}
	comment := models.WikiFooterComment{BlogPostID: post.ID, ParentCommentID: r.PostFormValue("parentId"), Body: models.WikiBody{Representation: "storage", Value: r.PostFormValue("body")}}
	if comment.ParentCommentID != "" {
		comment.BlogPostID = ""
	}
	created, err := h.Commands.CreateWikiFooterComment(r.Context(), ws, user.ID, comment)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+post.SpaceID+"/blogposts/"+post.ID+"#comment-"+created.ID)
}

func (h *Handler) WikiBlogInlineCommentCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	post, ok := h.wikiBlogForDiscussion(w, r, ws, user.ID)
	if !ok {
		return
	}
	parentID, selection := r.PostFormValue("parentId"), r.PostFormValue("selection")
	comment := models.WikiFooterComment{BlogPostID: post.ID, ParentCommentID: parentID, Body: models.WikiBody{Representation: "storage", Value: r.PostFormValue("body")}, InlineSelection: selection, InlineMatchCount: strings.Count(post.Body.Value, selection)}
	if parentID != "" {
		comment.BlogPostID, comment.InlineSelection, comment.InlineMatchCount = "", "", 0
	}
	created, err := h.Commands.CreateWikiInlineComment(r.Context(), ws, user.ID, comment)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+post.SpaceID+"/blogposts/"+post.ID+"#inline-comment-"+created.ID)
}

func (h *Handler) WikiBlogInlineCommentUpdate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	post, ok := h.wikiBlogForDiscussion(w, r, ws, user.ID)
	if !ok {
		return
	}
	comment, err := h.Store.WikiInlineComment(r.Context(), ws, user.ID, r.PathValue("comment"))
	if err != nil || comment.BlogPostID != post.ID {
		http.NotFound(w, r)
		return
	}
	resolved, parseErr := strconv.ParseBool(r.PostFormValue("resolved"))
	if parseErr != nil {
		http.Error(w, "Choose whether to resolve or reopen the discussion.", 400)
		return
	}
	_, err = h.Commands.UpdateWikiInlineComment(r.Context(), ws, user.ID, models.WikiFooterComment{ID: comment.ID, Body: comment.Body, Version: models.WikiVersion{Number: comment.Version.Number + 1, Message: "Resolution changed"}}, &resolved)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+post.SpaceID+"/blogposts/"+post.ID+"#inline-comment-"+comment.ID)
}

func (h *Handler) WikiBlogAttachmentCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, (100<<20)+1)
	if err := r.ParseMultipartForm(4 << 20); err != nil { // #nosec G120 -- body capped by MaxBytesReader above
		http.Error(w, "Invalid attachment upload.", 400)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Choose a file to attach.", 400)
		return
	}
	defer file.Close()
	post, err := h.Store.WikiBlogPost(r.Context(), ws, user.ID, r.PathValue("blogpost"))
	if err != nil || post.SpaceID != r.PathValue("space") || post.Status != "current" {
		http.NotFound(w, r)
		return
	}
	a, err := h.Commands.SaveWikiBlogAttachment(r.Context(), ws, user.ID, post.ID, r.PostFormValue("attachmentId"), header.Filename, header.Header.Get("Content-Type"), r.PostFormValue("comment"), r.PostFormValue("message"), false, file)
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+post.SpaceID+"/blogposts/"+post.ID+"#attachment-"+a.ID)
}

func (h *Handler) WikiBlogAttachmentDelete(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	post, err := h.Store.WikiBlogPost(r.Context(), ws, user.ID, r.PathValue("blogpost"))
	if err != nil || post.SpaceID != r.PathValue("space") || post.Status != "current" {
		http.NotFound(w, r)
		return
	}
	a, err := h.Store.WikiAttachment(r.Context(), ws, user.ID, r.PathValue("attachment"))
	if err != nil || a.BlogPostID != post.ID {
		http.NotFound(w, r)
		return
	}
	if err = h.Commands.DeleteWikiAttachment(r.Context(), ws, user.ID, a.ID); err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+post.SpaceID+"/blogposts/"+post.ID+"#wiki-blog-attachments")
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
	case "task-status":
		task, taskErr := h.Store.WikiTask(r.Context(), ws, user.ID, r.PostFormValue("task"))
		if taskErr != nil || task.BlogPostID != post.ID {
			http.NotFound(w, r)
			return
		}
		_, err = h.Commands.UpdateWikiTask(r.Context(), ws, user.ID, task.ID, r.PostFormValue("status"))
	case "watch":
		watching, parseErr := strconv.ParseBool(r.PostFormValue("watching"))
		if parseErr != nil {
			err = fmt.Errorf("%w: watching must be true or false", store.ErrWikiValidation)
		} else {
			err = h.Commands.SetWikiWatch(r.Context(), ws, user.ID, user.ID, "content", post.ID, watching)
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
	case "redact":
		section, target := r.PostFormValue("section"), r.PostFormValue("text")
		value := post.Body.Value
		pointer := "/body/storage/value"
		if section == "title" {
			value, pointer = post.Title, "/title"
		} else if section != "body" {
			err = fmt.Errorf("%w: choose title or body redaction", store.ErrWikiValidation)
			break
		}
		byteIndex := strings.Index(value, target)
		if target == "" || byteIndex < 0 {
			err = fmt.Errorf("%w: the exact text to redact was not found", store.ErrWikiValidation)
			break
		}
		from, to := utf8.RuneCountInString(value[:byteIndex]), utf8.RuneCountInString(value[:byteIndex])+utf8.RuneCountInString(target)
		reason := r.PostFormValue("reason")
		redaction := models.WikiRedactionPointer{Pointer: pointer, From: &from, To: &to, Reason: &reason}
		var title, body []models.WikiRedactionPointer
		if section == "title" {
			title = []models.WikiRedactionPointer{redaction}
		} else {
			body = []models.WikiRedactionPointer{redaction}
		}
		_, _, _, err = h.Commands.RedactWikiBlogPost(r.Context(), ws, user.ID, post.ID, post.Version.CreatedAt, post.Version.Number, r.PostFormValue("cleanHistory") == "true", title, body)
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
	redirectLocal(w, r, "/wiki/spaces/"+space.ID+"/databases/"+database.ID)
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
	redirectLocal(w, r, "/wiki/spaces/"+space.ID+"/whiteboards/"+whiteboard.ID)
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

// WikiBlogPostRedirect resolves a blog post id, the destination of blog post
// notifications, to the post in its space.
func (h *Handler) WikiBlogPostRedirect(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	post, err := h.Store.WikiBlogPost(r.Context(), ws, user.ID, r.PathValue("blogpost"))
	if err != nil || post.Status != "current" {
		http.NotFound(w, r)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+post.SpaceID+"/blogposts/"+post.ID)
}

// classificationChoices loads the organization's classification levels for a
// page: the published ones to choose from, every level's name, and which are
// published, so content keeping an archived level still shows it.
func (h *Handler) classificationChoices(r *http.Request, ws string) ([]models.DataClassificationLevel, map[string]string, map[string]bool, error) {
	levels, err := h.Store.DataClassificationLevels(r.Context(), ws)
	if err != nil {
		return nil, nil, nil, err
	}
	choices := []models.DataClassificationLevel{}
	names, published := map[string]string{}, map[string]bool{}
	for _, level := range levels {
		names[level.ID] = level.Name
		if level.Status == "PUBLISHED" {
			choices = append(choices, level)
			published[level.ID] = true
		}
	}
	return choices, names, published, nil
}

// wikiMentions loads the people who can be mentioned and names the mentions
// in a body and its comments that carry no label of their own.
func (h *Handler) wikiMentions(r *http.Request, ws string, body *models.WikiBody, threads ...[]wikiCommentNode) ([]*models.User, error) {
	people, err := h.Store.MembersByWorkspace(r.Context(), ws)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(people))
	for _, person := range people {
		names[person.ID] = person.DisplayName
	}
	body.Value = wikimarkup.LabelMentions(body.Value, names)
	var label func([]wikiCommentNode)
	label = func(nodes []wikiCommentNode) {
		for _, node := range nodes {
			node.Comment.Body.Value = wikimarkup.LabelMentions(node.Comment.Body.Value, names)
			label(node.Replies)
		}
	}
	for _, thread := range threads {
		label(thread)
	}
	return people, nil
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
		if page.ID == "" {
			page.Subtype = r.PostFormValue("subtype")
		}
		page.Version.Message = r.PostFormValue("message")
		version, err := strconv.Atoi(r.PostFormValue("version"))
		if err != nil || version < 1 {
			http.Error(w, "Invalid page version.", 400)
			return
		}
		page.Version.Number = version
		if r.PostFormValue("saveAs") == "draft" && page.ID != "" && page.Published {
			// A published page saved as a draft keeps its published version.
			if _, draftErr := h.Commands.SaveWikiContentDraft(r.Context(), ws, user.ID, "page", page.ID, page.Title, page.Body); draftErr != nil {
				status, data.Error = wikiWebError(draftErr)
			} else {
				redirectLocal(w, r, "/wiki/spaces/"+space.ID+"/pages/"+page.ID)
				return
			}
		} else {
			saved, err := h.Commands.SaveWikiPage(r.Context(), ws, user.ID, *page)
			if err == nil {
				redirectLocal(w, r, "/wiki/spaces/"+space.ID+"/pages/"+saved.ID)
				return
			}
			status, data.Error = wikiWebError(err)
		}
	}
	if page.ID != "" && page.Status == "current" && canEdit {
		draft, draftErr := h.Store.WikiContentDraft(r.Context(), ws, user.ID, "page", page.ID)
		switch {
		case draftErr == nil:
			data.Draft = draft
			// Editing a page with a waiting draft picks up the draft.
			if edit && r.Method != "POST" {
				page.Title, page.Body = draft.Title, draft.Body
			}
		case !errors.Is(draftErr, pgx.ErrNoRows):
			http.Error(w, "Could not load the page draft.", 500)
			return
		}
	}
	if page.ID != "" && page.Status == "trashed" {
		if data.CanPurge, err = h.Store.CanAdministerWikiSpace(r.Context(), ws, user.ID, space.ID); err != nil {
			http.Error(w, "Could not load space permissions.", 500)
			return
		}
	}
	if edit {
		data.Pages, err = h.Store.WikiPages(r.Context(), ws, user.ID, space.ID, "current", "")
		if err != nil {
			http.Error(w, "Could not load parent pages.", 500)
			return
		}
		parentContent, contentErr := h.wikiSpaceTreeContent(r, ws, user.ID, space.ID)
		if contentErr != nil {
			http.Error(w, "Could not load parent content.", 500)
			return
		}
		for _, content := range parentContent {
			if !content.Private {
				data.ParentContent = append(data.ParentContent, wikiMoveTarget{ID: content.ID, Label: content.Title + " · " + wikiContentTypeName(content.Type)})
			}
		}
		data.SourceMode = !wikimarkup.RichEditable(page.Body.Value)
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
		if canEdit && (page.Status == "current" || page.Status == "archived") {
			if err = h.loadWikiPageLifecycle(r, ws, user.ID, &data); err != nil {
				http.Error(w, "Could not load page locations.", 500)
				return
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
			data.AppByline, err = h.Store.AppModulesByLocation(r.Context(), ws, "confluence.content.byline")
			if err != nil {
				http.Error(w, "Could not load page apps.", 500)
				return
			}
			data.PageProperties, err = h.Store.WikiPageProperties(r.Context(), ws, user.ID, page.ID, "")
			if err != nil {
				http.Error(w, "Could not load page properties.", 500)
				return
			}
			for i := range data.PageProperties {
				data.PageProperties[i].NextVersion = data.PageProperties[i].Version.Number + 1
			}
			pageLikes, likeErr := h.Store.WikiPageLikes(r.Context(), ws, user.ID, page.ID)
			if likeErr != nil {
				http.Error(w, "Could not load page likes.", 500)
				return
			}
			data.PageLikeCount = len(pageLikes)
			for _, accountID := range pageLikes {
				if accountID == user.ID {
					data.PageLiked = true
					break
				}
			}
			data.PageFavourite, err = h.Store.IsWikiPageFavourite(r.Context(), ws, user.ID, page.ID)
			if err != nil {
				http.Error(w, "Could not load page star.", 500)
				return
			}
			members, memberErr := h.Store.MembersByWorkspace(r.Context(), ws)
			if memberErr != nil {
				http.Error(w, "Could not load page owner.", 500)
				return
			}
			for _, member := range members {
				if member.ID == page.OwnerID {
					data.PageOwnerName = member.DisplayName
				}
			}
			if canEdit {
				data.OwnerChoices = members
			}
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
		attachmentStatus := "current"
		if page.Status == "archived" {
			attachmentStatus = "archived"
		}
		data.Attachments, err = h.Store.WikiAttachments(r.Context(), ws, user.ID, page.ID, "", "", attachmentStatus)
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
	if !edit && r.Method == http.MethodGet && page.ID != "" && page.Status == "current" {
		h.recordWikiView(r, ws, user.ID, "page", page.ID)
	}
	data.MentionPeople, err = h.wikiMentions(r, ws, &page.Body, data.Comments, data.InlineComments)
	if err != nil {
		http.Error(w, "Could not load people to mention.", 500)
		return
	}
	data.ClassificationLevels, data.ClassificationNames, data.PublishedClassification, err = h.classificationChoices(r, ws)
	if err != nil {
		http.Error(w, "Could not load classification levels.", 500)
		return
	}
	h.writeWorkspacePageStatus(w, r, "page_wiki_page", user, ws, data, "wiki", "", status)
}

func (h *Handler) WikiPageMetadata(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	page, err := h.wikiPageForComment(r, ws, user.ID)
	if err != nil || page.Status != "current" {
		http.NotFound(w, r)
		return
	}
	switch r.PostFormValue("action") {
	case "like":
		liked, parseErr := strconv.ParseBool(r.PostFormValue("liked"))
		if parseErr != nil {
			err = fmt.Errorf("%w: liked must be true or false", store.ErrWikiValidation)
		} else {
			err = h.Store.SetWikiPageLike(r.Context(), ws, user.ID, page.ID, liked)
		}
	case "classify":
		_, err = h.Commands.SetWikiPageClassification(r.Context(), ws, user.ID, page.ID, r.PostFormValue("level"))
	case "create-property":
		_, err = h.Commands.CreateWikiPageProperty(r.Context(), ws, user.ID, page.ID, r.PostFormValue("key"), json.RawMessage(r.PostFormValue("value")))
	case "update-property":
		version, parseErr := strconv.Atoi(r.PostFormValue("version"))
		if parseErr != nil {
			err = fmt.Errorf("%w: property version must be an integer", store.ErrWikiValidation)
		} else {
			_, err = h.Commands.UpdateWikiPageProperty(r.Context(), ws, user.ID, page.ID, r.PostFormValue("propertyId"), r.PostFormValue("key"), json.RawMessage(r.PostFormValue("value")), version, r.PostFormValue("message"))
		}
	case "delete-property":
		err = h.Commands.DeleteWikiPageProperty(r.Context(), ws, user.ID, page.ID, r.PostFormValue("propertyId"))
	case "redact":
		section, target := r.PostFormValue("section"), r.PostFormValue("text")
		value := page.Body.Value
		pointer := "/body/storage/value"
		if section == "title" {
			value, pointer = page.Title, "/title"
		} else if section != "body" {
			err = fmt.Errorf("%w: choose title or body redaction", store.ErrWikiValidation)
			break
		}
		byteIndex := strings.Index(value, target)
		if target == "" || byteIndex < 0 {
			err = fmt.Errorf("%w: the exact text to redact was not found", store.ErrWikiValidation)
			break
		}
		from := utf8.RuneCountInString(value[:byteIndex])
		to := from + utf8.RuneCountInString(target)
		reason := r.PostFormValue("reason")
		redaction := models.WikiRedactionPointer{Pointer: pointer, From: &from, To: &to, Reason: &reason}
		var title, body []models.WikiRedactionPointer
		if section == "title" {
			title = []models.WikiRedactionPointer{redaction}
		} else {
			body = []models.WikiRedactionPointer{redaction}
		}
		_, _, _, err = h.Commands.RedactWikiPage(r.Context(), ws, user.ID, page.ID, page.Version.CreatedAt, page.Version.Number, r.PostFormValue("cleanHistory") == "true", title, body)
	default:
		err = fmt.Errorf("%w: choose a page metadata action", store.ErrWikiValidation)
	}
	if err != nil {
		status, message := wikiWebError(err)
		http.Error(w, message, status)
		return
	}
	redirectLocal(w, r, wikiPageURL(page))
}

func (h *Handler) WikiAttachmentCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, (100<<20)+1)
	if err := r.ParseMultipartForm(4 << 20); err != nil { // #nosec G120 -- body capped by MaxBytesReader above
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

// loadWikiPageLifecycle gathers what a page's Archive, Restore and Move actions
// offer: how many child pages would go along, and every current page in a
// current space that the page could be moved beside or beneath. The page's own
// subtree is left out, because a page cannot be moved beneath itself.
func (h *Handler) loadWikiPageLifecycle(r *http.Request, ws, userID string, data *wikiData) error {
	page := data.Page
	if page.Status == "archived" {
		archived, err := h.Store.WikiPages(r.Context(), ws, userID, page.SpaceID, "archived", "")
		if err != nil {
			return err
		}
		for _, candidate := range archived {
			if candidate.ParentID == page.ID {
				data.ArchivedChildCount++
			}
		}
		return nil
	}
	spaces, err := h.Store.WikiSpaces(r.Context(), ws, userID)
	if err != nil {
		return err
	}
	pages, err := h.Store.WikiPages(r.Context(), ws, userID, "", "current", "")
	if err != nil {
		return err
	}
	for _, candidate := range pages {
		if candidate.ParentID == page.ID {
			data.ChildPageCount++
		}
	}
	for _, space := range spaces {
		if space.Status != "current" {
			continue
		}
		contents, contentErr := h.wikiSpaceTreeContent(r, ws, userID, space.ID)
		if contentErr != nil {
			return contentErr
		}
		spacePages := []*models.WikiPage{}
		for _, candidate := range pages {
			if candidate.SpaceID == space.ID {
				spacePages = append(spacePages, candidate)
			}
		}
		// A page cannot go beneath itself or beneath private content, so its
		// own subtree and private content are left out.
		group := wikiMoveGroup{Space: space}
		skipBelow := -1
		for _, row := range wikiContentTreeRows(spacePages, contents) {
			if skipBelow >= 0 && row.Depth > skipBelow {
				continue
			}
			skipBelow = -1
			if row.ID == page.ID {
				skipBelow = row.Depth
				continue
			}
			if row.Private {
				continue
			}
			group.Targets = append(group.Targets, wikiMoveTarget{ID: row.ID, Label: wikiMoveTargetLabel(row)})
		}
		data.MoveTargets = append(data.MoveTargets, group)
	}
	return nil
}

// wikiPageAction loads the page a page action names, refusing one that is not
// in the space the address says.
func (h *Handler) wikiPageAction(w http.ResponseWriter, r *http.Request) (*models.WikiPage, string, string, bool) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return nil, "", "", false
	}
	page, err := h.Store.WikiPage(r.Context(), ws, user.ID, r.PathValue("page"))
	if err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return nil, "", "", false
	}
	if page.SpaceID != r.PathValue("space") {
		http.NotFound(w, r)
		return nil, "", "", false
	}
	if !parseForm(w, r) {
		return nil, "", "", false
	}
	return page, user.ID, ws, true
}

// WikiPageArchive archives a page from its own actions. Its child pages go
// with it when asked, and otherwise move up a level.
func (h *Handler) WikiPageArchive(w http.ResponseWriter, r *http.Request) {
	page, userID, ws, ok := h.wikiPageAction(w, r)
	if !ok {
		return
	}
	if _, err := h.Commands.ArchiveWikiPages(r.Context(), ws, userID, []string{page.ID}, r.PostFormValue("descendants") == "true"); err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+page.SpaceID+"/pages/"+page.ID)
}

// WikiPageRestore returns an archived page to the page tree.
func (h *Handler) WikiPageRestore(w http.ResponseWriter, r *http.Request) {
	page, userID, ws, ok := h.wikiPageAction(w, r)
	if !ok {
		return
	}
	if _, err := h.Commands.RestoreWikiPage(r.Context(), ws, userID, page.ID, r.PostFormValue("descendants") == "true"); err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+page.SpaceID+"/pages/"+page.ID)
}

// WikiPageMove moves a page beside or beneath another, which may be in another
// space; the page's address follows it there.
func (h *Handler) WikiPageMove(w http.ResponseWriter, r *http.Request) {
	page, userID, ws, ok := h.wikiPageAction(w, r)
	if !ok {
		return
	}
	targetID := r.PostFormValue("targetId")
	if targetID == "" {
		http.Error(w, "Choose the page to move this page beside or beneath.", 400)
		return
	}
	var err error
	if spaceID, top := strings.CutPrefix(targetID, "space-"); top {
		_, err = h.Commands.MoveWikiTreeNodeToSpace(r.Context(), ws, userID, page.ID, spaceID)
	} else {
		_, err = h.Commands.MoveWikiPage(r.Context(), ws, userID, page.ID, r.PostFormValue("position"), targetID)
	}
	if err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	moved, err := h.Store.WikiPage(r.Context(), ws, userID, page.ID)
	if err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+moved.SpaceID+"/pages/"+moved.ID)
}

// WikiPageDraftDiscard throws away the draft waiting beside a published page.
func (h *Handler) WikiPageDraftDiscard(w http.ResponseWriter, r *http.Request) {
	page, userID, ws, ok := h.wikiPageAction(w, r)
	if !ok {
		return
	}
	err := h.Commands.DiscardWikiContentDraft(r.Context(), ws, userID, "page", page.ID)
	h.finishWikiTreeAction(w, r, err, "/wiki/spaces/"+page.SpaceID+"/pages/"+page.ID)
}

// WikiPagePurge takes a trashed page out of the trash.
func (h *Handler) WikiPagePurge(w http.ResponseWriter, r *http.Request) {
	page, userID, ws, ok := h.wikiPageAction(w, r)
	if !ok {
		return
	}
	err := h.Commands.PurgeWikiPage(r.Context(), ws, userID, page.ID)
	h.finishWikiTreeAction(w, r, err, "/wiki/spaces/"+page.SpaceID+"?status=trashed")
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
