package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/models"
)

type anonymousCollectionData struct {
	Title    string
	Projects []*models.Project
	Project  *models.Project
	Issues   []*models.Issue
	FirstURL string
	NextURL  string
}

func (h *Handler) serveAnonymousCollection(w http.ResponseWriter, r *http.Request, projectKey string) {
	w.Header().Set("Cache-Control", "private, no-store")
	r = r.WithContext(authn.WithAnonymous(r.Context()))
	ws, err := h.Store.WorkspaceBySlug(r.Context(), h.WorkspaceSlug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	after := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("after")))
	if len(after) > 255 {
		http.Error(w, "The page cursor is too long.", http.StatusBadRequest)
		return
	}
	for key := range r.URL.Query() {
		if key != "after" && !(projectKey == "" && key == "status") {
			http.Error(w, "Public lists support paging only. Log in to use other filters.", http.StatusBadRequest)
			return
		}
	}
	data := anonymousCollectionData{Title: "Public projects"}
	var next string
	path := "/projects"
	if projectKey == "" {
		// Archived and trashed collections remain administration pages.
		if state := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status"))); state != "" && state != "active" {
			http.NotFound(w, r)
			return
		}
		data.Projects, next, err = h.Store.PublicProjectsAfter(r.Context(), ws, after)
	} else {
		project, e := h.Store.ProjectByKey(r.Context(), ws, projectKey)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		allowed, e := h.Store.HasProjectPermission(r.Context(), ws, "", project.ID, "", "BROWSE_PROJECTS")
		if e != nil || !allowed {
			http.NotFound(w, r)
			return
		}
		data.Project = project
		data.Title = project.Name + " · Public work"
		path = "/issues/" + url.PathEscape(project.Key)
		data.Issues, next, err = h.Store.PublicIssuesAfter(r.Context(), ws, project.ID, after)
	}
	if err != nil {
		http.Error(w, "Could not load public work.", http.StatusInternalServerError)
		return
	}
	if after != "" {
		data.FirstURL = path
	}
	if next != "" {
		data.NextURL = path + "?" + url.Values{"after": {next}}.Encode()
	}
	look := h.siteLook(r, ws)
	writePage(w, "page_anonymous_collection", pageData{Data: data, Site: &look})
}
