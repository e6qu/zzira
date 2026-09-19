package web

import (
	"bytes"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// wikiTemplateView is a template on a space's templates page; Editable is true
// for the space's own templates when the reader administers the space.
type wikiTemplateView struct {
	store.WikiTemplate
	LabelsText string
	Editable   bool
}

// wikiAnalyticsRow is content in a space's view analytics.
type wikiAnalyticsRow struct {
	Title, URL, Type string
	Views, Viewers   int
}

// WikiSpaceTemplates lists the templates a space can use: its own, the site's
// and the blueprints'. The space's administrators keep its own templates.
func (h *Handler) WikiSpaceTemplates(w http.ResponseWriter, r *http.Request) {
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
	canManage, err := h.Store.CanAdministerWikiSpace(r.Context(), ws, user.ID, space.ID)
	if err != nil {
		http.Error(w, "Could not load space permissions.", http.StatusInternalServerError)
		return
	}
	data := wikiData{Space: space, CanManageSpace: canManage}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		if !canManage {
			http.Error(w, "Space administrator access is required.", http.StatusForbidden)
			return
		}
		if !parseForm(w, r) {
			return
		}
		templateID := r.PostFormValue("templateId")
		if templateID != "" {
			// Only the space's own templates are changed from its page.
			existing, err := h.Store.WikiTemplate(r.Context(), ws, user.ID, templateID)
			if err != nil || existing.SpaceKey != space.Key || existing.BlueprintModuleKey != "" {
				http.NotFound(w, r)
				return
			}
		}
		if r.PostFormValue("action") == "delete" {
			err = h.Store.DeleteWikiTemplate(r.Context(), ws, user.ID, templateID)
		} else {
			labels := strings.FieldsFunc(r.PostFormValue("labels"), func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
			_, err = h.Store.SaveWikiTemplate(r.Context(), ws, user.ID, templateID, r.PostFormValue("name"), r.PostFormValue("description"), r.PostFormValue("templateType"), r.PostFormValue("body"), space.Key, labels)
		}
		if err == nil {
			redirectLocal(w, r, "/wiki/spaces/"+space.ID+"/templates")
			return
		}
		status, data.Error = wikiWebError(err)
	}
	templates, err := h.Store.WikiTemplates(r.Context(), ws, user.ID, space.Key)
	if err != nil {
		http.Error(w, "Could not load templates.", http.StatusInternalServerError)
		return
	}
	for _, template := range templates {
		view := wikiTemplateView{WikiTemplate: template, LabelsText: strings.Join(template.Labels, ", ")}
		if template.SpaceKey == space.Key {
			view.Editable = canManage
			data.SpaceTemplates = append(data.SpaceTemplates, view)
		} else {
			data.SiteTemplates = append(data.SiteTemplates, view)
		}
	}
	blueprints, err := h.Store.WikiBlueprintTemplates(r.Context(), ws, user.ID, space.Key)
	if err != nil {
		http.Error(w, "Could not load blueprints.", http.StatusInternalServerError)
		return
	}
	for _, template := range blueprints {
		data.BlueprintTemplates = append(data.BlueprintTemplates, wikiTemplateView{WikiTemplate: template, LabelsText: strings.Join(template.Labels, ", ")})
	}
	if editing := r.URL.Query().Get("edit"); editing != "" && canManage {
		for index := range data.SpaceTemplates {
			if data.SpaceTemplates[index].ID == editing {
				data.EditingTemplate = &data.SpaceTemplates[index]
			}
		}
	}
	h.writeWorkspacePageStatus(w, r, "page_wiki_space_templates", user, ws, data, "wiki", "", status)
}

// WikiSpaceAnalytics shows how often the space's pages and blog posts that the
// reader can see were viewed, and by how many people, over 7, 30 or 90 days.
func (h *Handler) WikiSpaceAnalytics(w http.ResponseWriter, r *http.Request) {
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
	days := 30
	switch r.URL.Query().Get("days") {
	case "", "30":
	case "7":
		days = 7
	case "90":
		days = 90
	default:
		http.Error(w, "Choose a 7, 30 or 90 day window.", http.StatusBadRequest)
		return
	}
	pages, err := h.Store.WikiPages(r.Context(), ws, user.ID, space.ID, "current", "")
	if err != nil {
		http.Error(w, "Could not load pages.", http.StatusInternalServerError)
		return
	}
	posts, err := h.Store.WikiBlogPosts(r.Context(), ws, user.ID, space.ID, "current", "", "-created-date")
	if err != nil {
		http.Error(w, "Could not load blog posts.", http.StatusInternalServerError)
		return
	}
	from := time.Now().UTC().AddDate(0, 0, -days)
	pageIDs := make([]string, 0, len(pages))
	for _, page := range pages {
		pageIDs = append(pageIDs, page.ID)
	}
	postIDs := make([]string, 0, len(posts))
	for _, post := range posts {
		postIDs = append(postIDs, post.ID)
	}
	pageCounts, err := h.Store.WikiContentViewCounts(r.Context(), ws, "page", pageIDs, from)
	if err != nil {
		http.Error(w, "Could not load page views.", http.StatusInternalServerError)
		return
	}
	postCounts, err := h.Store.WikiContentViewCounts(r.Context(), ws, "blogpost", postIDs, from)
	if err != nil {
		http.Error(w, "Could not load blog post views.", http.StatusInternalServerError)
		return
	}
	rows := []wikiAnalyticsRow{}
	for _, page := range pages {
		if count := pageCounts[page.ID]; count.Views > 0 {
			rows = append(rows, wikiAnalyticsRow{Title: page.Title, URL: wikiPageURL(page), Type: "Page", Views: count.Views, Viewers: count.Viewers})
		}
	}
	for _, post := range posts {
		if count := postCounts[post.ID]; count.Views > 0 {
			rows = append(rows, wikiAnalyticsRow{Title: post.Title, URL: "/wiki/spaces/" + space.ID + "/blogposts/" + post.ID, Type: "Blog post", Views: count.Views, Viewers: count.Viewers})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Views != rows[j].Views {
			return rows[i].Views > rows[j].Views
		}
		return rows[i].Title < rows[j].Title
	})
	data := wikiData{Space: space, AnalyticsDays: days, AnalyticsWindows: []int{7, 30, 90}, ViewedContent: len(rows)}
	for _, row := range rows {
		data.TotalViews += row.Views
	}
	if len(rows) > 50 {
		rows = rows[:50]
	}
	data.Analytics = rows
	h.writeWorkspacePage(w, r, "page_wiki_space_analytics", user, ws, data, "wiki", "")
}

// WikiSpaceDetails renames a space, rewrites its description and chooses the
// page it opens on -- Confluence's space details form, which until now was
// only reachable through the REST API.
func (h *Handler) WikiSpaceDetails(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpaceForAdministration(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	name := r.PostFormValue("name")
	description := r.PostFormValue("description")
	input := store.UpdateWikiSpaceInput{Name: &name, Description: &description}
	// An empty homepage field means "leave the home page as it is": the store
	// requires a current page in this space, so there is no way to clear it,
	// and Confluence has none either.
	if homepage := r.PostFormValue("homepageId"); homepage != "" {
		input.HomepageID = &homepage
	}
	if _, err := h.Store.UpdateWikiSpace(r.Context(), ws, user.ID, space.Key, input); err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID)
}

// WikiSpaceStatus archives a space or restores it, for its administrators.
func (h *Handler) WikiSpaceStatus(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpaceForAdministration(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	target := r.PostFormValue("status")
	if target != "archived" && target != "current" {
		http.Error(w, "Choose to archive or restore the space.", http.StatusBadRequest)
		return
	}
	// A space in the trash leaves it only through the trash, which is a site
	// administrator's decision, so archiving does not reach into it.
	if space.Status == "trashed" {
		http.Error(w, "Restore this space from the trash before archiving it.", http.StatusBadRequest)
		return
	}
	if _, err := h.Store.UpdateWikiSpace(r.Context(), ws, user.ID, space.Key, store.UpdateWikiSpaceInput{Status: &target}); err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID)
}

// WikiSpaceTrash is the browser's delete: Confluence sends a deleted space to
// the trash rather than removing it, and its administrators may do so.
func (h *Handler) WikiSpaceTrash(w http.ResponseWriter, r *http.Request) {
	user, ws, space, ok := h.wikiSpaceLifecycleTarget(w, r)
	if !ok {
		return
	}
	if _, err := h.Store.TrashWikiSpace(r.Context(), ws, user.ID, space.Key); err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	redirectLocal(w, r, "/wiki")
}

// WikiSpaceRestore brings a space back from the trash, which only a site
// administrator may do.
func (h *Handler) WikiSpaceRestore(w http.ResponseWriter, r *http.Request) {
	user, ws, space, ok := h.wikiSpaceLifecycleTarget(w, r)
	if !ok {
		return
	}
	if _, err := h.Store.RestoreWikiSpace(r.Context(), ws, user.ID, space.Key); err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID)
}

// WikiSpacePurge permanently deletes a space that is already in the trash. It
// runs as the same long task the API's delete queues, so the browser returns to
// the trash while the content goes.
func (h *Handler) WikiSpacePurge(w http.ResponseWriter, r *http.Request) {
	user, ws, space, ok := h.wikiSpaceLifecycleTarget(w, r)
	if !ok {
		return
	}
	if _, err := h.Store.PurgeWikiSpace(r.Context(), ws, user.ID, space.Key); err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	redirectLocal(w, r, "/wiki?status=trashed")
}

// wikiSpaceLifecycleTarget resolves the space a lifecycle form names. The
// permission belongs to the store call that follows, which is where Confluence
// puts a different rule on trashing a space and on acting on the trash.
func (h *Handler) wikiSpaceLifecycleTarget(w http.ResponseWriter, r *http.Request) (*models.User, string, *models.WikiSpace, bool) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return nil, "", nil, false
	}
	space, err := h.Store.WikiSpaceForAdministration(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return nil, "", nil, false
	}
	return user, ws, space, true
}

// WikiSpaceExportCreate queues an HTML export of a space for its administrator.
func (h *Handler) WikiSpaceExportCreate(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	space, err := h.Store.WikiSpaceForAdministration(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	if _, err := h.Store.EnqueueWikiSpaceExport(r.Context(), ws, user.ID, space.Key); err != nil {
		status, msg := wikiWebError(err)
		http.Error(w, msg, status)
		return
	}
	redirectLocal(w, r, "/wiki/spaces/"+space.ID+"#wiki-space-exports")
}

// WikiSpaceExportFile serves a space export to the person who asked for it.
func (h *Handler) WikiSpaceExportFile(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	space, err := h.Store.WikiSpace(r.Context(), ws, user.ID, r.PathValue("space"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	content, err := h.Store.WikiSpaceExport(r.Context(), ws, space.ID, strings.TrimSuffix(r.PathValue("file"), ".zip"), user.ID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+space.Key+`-export.zip"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, space.Key+"-export.zip", time.Time{}, bytes.NewReader(content))
}
