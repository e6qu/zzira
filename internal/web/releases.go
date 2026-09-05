package web

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/authz"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

type releaseRow struct {
	Version  *models.Version
	Progress models.VersionProgress
	Overdue  bool
}
type releasesData struct {
	Project  *models.Project
	Rows     []releaseRow
	Version  *models.Version
	Issues   []*models.Issue
	Progress models.VersionProgress
	Admin    bool
	Editing  bool
	Error    string
	Query    string
	Status   string
}

func releaseWebError(err error) (int, string) {
	switch {
	case errors.Is(err, store.ErrVersionValidation):
		return 400, err.Error()
	case errors.Is(err, store.ErrProjectPermission):
		return 403, err.Error()
	case errors.Is(err, pgx.ErrNoRows):
		return 404, "Release or project does not exist."
	default:
		log.Print("release operation: ", strconv.Quote(err.Error()))
		return 500, "Could not complete the release operation."
	}
}
func releaseForm(r *http.Request) store.VersionUpdate {
	name, description, start, release := r.PostFormValue("name"), r.PostFormValue("description"), r.PostFormValue("startDate"), r.PostFormValue("releaseDate")
	return store.VersionUpdate{Name: &name, Description: &description, StartDate: &start, ReleaseDate: &release}
}
func (h *Handler) Releases(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), ws, r.PathValue("key"))
	if err != nil {
		status, msg := releaseWebError(err)
		http.Error(w, msg, status)
		return
	}
	admin, err := h.Store.IsAdmin(r.Context(), ws, user.ID)
	if err != nil {
		http.Error(w, "Could not load releases.", 500)
		return
	}
	data := releasesData{Project: project, Admin: admin, Status: r.URL.Query().Get("status"), Query: strings.TrimSpace(r.URL.Query().Get("q")), Version: &models.Version{}}
	status := 200
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		up := releaseForm(r)
		data.Version = &models.Version{Name: *up.Name, Description: *up.Description, StartDate: *up.StartDate, ReleaseDate: *up.ReleaseDate}
		saved, err := h.Store.SaveVersion(r.Context(), ws, user.ID, project.ID, "", up)
		if err == nil {
			redirectLocal(w, r, "/projects/"+project.Key+"/releases/"+saved.ID)
			return
		}
		status, data.Error = releaseWebError(err)
	}
	versions, err := h.Store.ProjectVersions(r.Context(), project.ID)
	if err != nil {
		http.Error(w, "Could not load releases.", 500)
		return
	}
	for _, v := range versions {
		if data.Status != "" && strings.ToLower(v.State()) != data.Status {
			continue
		}
		if data.Query != "" && !strings.Contains(strings.ToLower(v.Name+" "+v.Description), strings.ToLower(data.Query)) {
			continue
		}
		issues, err := h.Store.VersionIssues(r.Context(), ws, user.ID, project.ID, v.ID, "fixVersions")
		if err != nil {
			http.Error(w, "Could not load release progress.", 500)
			return
		}
		data.Rows = append(data.Rows, releaseRow{Version: v, Progress: store.VersionProgress(issues), Overdue: !v.Released && v.ReleaseDate != "" && v.ReleaseDate < time.Now().UTC().Format("2006-01-02")})
	}
	h.writeWorkspacePageStatus(w, r, "page_releases", user, ws, data, "releases", project.Key, status)
}
func (h *Handler) Release(w http.ResponseWriter, r *http.Request) {
	user, ws, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), ws, r.PathValue("key"))
	if err != nil {
		status, msg := releaseWebError(err)
		http.Error(w, msg, status)
		return
	}
	version, err := h.Store.Version(r.Context(), ws, r.PathValue("version"))
	if err != nil || version.ProjectID != project.ID {
		http.NotFound(w, r)
		return
	}
	admin, err := h.Store.IsAdmin(r.Context(), ws, user.ID)
	if err != nil {
		http.Error(w, "Could not load release.", 500)
		return
	}
	data := releasesData{Project: project, Version: version, Admin: admin, Editing: r.URL.Query().Get("edit") == "1"}
	status := 200
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		action := r.PostFormValue("action")
		var opErr error
		switch action {
		case "save":
			up := releaseForm(r)
			data.Version = &models.Version{ID: version.ID, ProjectID: project.ID, Name: *up.Name, Description: *up.Description, StartDate: *up.StartDate, ReleaseDate: *up.ReleaseDate, Released: version.Released, Archived: version.Archived}
			data.Editing = true
			_, opErr = h.Store.SaveVersion(r.Context(), ws, user.ID, project.ID, version.ID, up)
		case "release", "unrelease":
			released := action == "release"
			date := r.PostFormValue("releaseDate")
			up := store.VersionUpdate{Released: &released}
			if released {
				if date == "" {
					date = time.Now().UTC().Format("2006-01-02")
				}
				up.ReleaseDate = &date
			}
			_, opErr = h.Store.SaveVersion(r.Context(), ws, user.ID, project.ID, version.ID, up)
		case "archive", "unarchive":
			archived := action == "archive"
			_, opErr = h.Store.SaveVersion(r.Context(), ws, user.ID, project.ID, version.ID, store.VersionUpdate{Archived: &archived})
		case "delete":
			opErr = h.Store.DeleteVersion(r.Context(), ws, user.ID, version.ID, "", "")
			if opErr == nil {
				redirectLocal(w, r, "/projects/"+project.Key+"/releases")
				return
			}
		case "add", "remove":
			issue, e := h.Store.IssueByIDOrKey(r.Context(), ws, strings.TrimSpace(r.PostFormValue("issue")))
			if e != nil || issue.ProjectID != project.ID {
				http.Error(w, "Choose a work item from this project.", 400)
				return
			}
			visible, e := authz.CanSeeIssue(r.Context(), h.Store, ws, project.ID, user.ID, issue.SecurityLevelID)
			if e != nil || !visible {
				http.NotFound(w, r)
				return
			}
			ref, _ := json.Marshal(map[string]string{"id": version.ID})
			_, _, opErr = h.Commands.UpdateIssue(r.Context(), commands.UpdateIssueInput{ActorID: user.ID, WorkspaceID: ws, IssueIDOrKey: issue.ID, VersionOperations: map[string][]map[string]json.RawMessage{"fixVersions": {{action: ref}}}})
		default:
			http.Error(w, "Unknown release action.", 400)
			return
		}
		if opErr == nil {
			redirectLocal(w, r, "/projects/"+project.Key+"/releases/"+version.ID)
			return
		}
		status, data.Error = releaseWebError(opErr)
	}
	data.Issues, err = h.Store.VersionIssues(r.Context(), ws, user.ID, project.ID, version.ID, "fixVersions")
	if err != nil {
		http.Error(w, "Could not load release work items.", 500)
		return
	}
	data.Progress = store.VersionProgress(data.Issues)
	h.writeWorkspacePageStatus(w, r, "page_release", user, ws, data, "releases", project.Key, status)
}
