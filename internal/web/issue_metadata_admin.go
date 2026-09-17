package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// workTypesPageData is the work types settings page: the site's work types
// with their hierarchy level, and the work type schemes that decide which
// projects offer them.
type workTypesPageData struct {
	WorkTypes []models.IssueType
	Levels    []store.HierarchyLevel
	Schemes   []store.IssueTypeScheme
	Projects  []*models.Project
	Notice    string
	Error     string
}

// prioritiesPageData is the priorities settings page.
type prioritiesPageData struct {
	Priorities []models.Priority
	Icons      []priorityIcon
	Schemes    []store.PriorityScheme
	Notice     string
	Error      string
}

// resolutionsPageData is the resolutions settings page.
type resolutionsPageData struct {
	Resolutions []models.Resolution
	Notice      string
	Error       string
}

// WorkTypesPage lists the site's work types and work type schemes.
func (h *Handler) WorkTypesPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	data := workTypesPageData{Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	var err error
	if data.WorkTypes, err = h.Store.IssueTypesForWorkspace(r.Context(), workspaceID); err != nil {
		http.Error(w, "Could not load work types.", http.StatusInternalServerError)
		return
	}
	if data.Levels, err = h.Store.HierarchyLevels(r.Context(), workspaceID); err != nil {
		http.Error(w, "Could not load the work type hierarchy.", http.StatusInternalServerError)
		return
	}
	if data.Schemes, err = h.Store.IssueTypeSchemes(r.Context(), workspaceID); err != nil {
		http.Error(w, "Could not load work type schemes.", http.StatusInternalServerError)
		return
	}
	if data.Projects, err = h.Store.ProjectsByWorkspace(r.Context(), workspaceID); err != nil {
		http.Error(w, "Could not load projects.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_work_types", user, workspaceID, data, "work-types", "")
}

// WorkTypesMutation creates, edits and deletes work types and their schemes.
func (h *Handler) WorkTypesMutation(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	notice, err := "", error(nil)
	value := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	switch r.PostFormValue("action") {
	case "create":
		var created models.IssueType
		created, err = h.Store.CreateIssueType(r.Context(), workspaceID, value("name"), value("description"), value("kind"), nil)
		if err == nil {
			notice = created.Name + " created."
		}
	case "update":
		name, description := value("name"), r.PostFormValue("description")
		if _, err = h.Store.UpdateIssueType(r.Context(), workspaceID, value("workType"), &name, &description, nil); err == nil {
			notice = name + " saved."
		}
	case "delete":
		if err = h.Store.DeleteIssueType(r.Context(), workspaceID, value("workType"), value("alternative")); err == nil {
			notice = "Work type deleted."
		}
	case "create-scheme":
		types := formValues(r, "workType")
		if _, err = h.Store.CreateIssueTypeScheme(r.Context(), workspaceID, value("name"), value("description"), value("defaultWorkType"), types); err == nil {
			notice = "Work type scheme created."
		}
	case "assign-scheme":
		if err = h.Store.AssignIssueTypeScheme(r.Context(), workspaceID, value("scheme"), value("project")); err == nil {
			notice = "Work type scheme assigned."
		}
	case "add-to-scheme":
		if err = h.Store.AddIssueTypesToScheme(r.Context(), workspaceID, value("scheme"), []string{value("workType")}); err == nil {
			notice = "Work type added to the scheme."
		}
	case "remove-from-scheme":
		if err = h.Store.RemoveIssueTypeFromScheme(r.Context(), workspaceID, value("scheme"), value("workType")); err == nil {
			notice = "Work type removed from the scheme."
		}
	case "delete-scheme":
		if err = h.Store.DeleteIssueTypeScheme(r.Context(), workspaceID, value("scheme")); err == nil {
			notice = "Work type scheme deleted."
		}
	default:
		http.Error(w, "Unknown work type action.", http.StatusBadRequest)
		return
	}
	h.redirectMetadata(w, r, "/settings/work-types", notice, err)
}

// priorityIcon is one of Jira's built-in priority icons, for the icon picker.
type priorityIcon struct {
	Name string
	URL  string
}

// priorityIcons lists the built-in icons a priority can use.
func priorityIcons() []priorityIcon {
	icons := make([]priorityIcon, 0, len(store.PriorityIconNames))
	for _, name := range store.PriorityIconNames {
		icons = append(icons, priorityIcon{Name: strings.ToUpper(name[:1]) + name[1:], URL: "/images/icons/priorities/" + name + ".svg"})
	}
	return icons
}

// PrioritiesPage lists the site's priorities and priority schemes.
func (h *Handler) PrioritiesPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	data := prioritiesPageData{Icons: priorityIcons(), Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	var err error
	if data.Priorities, err = h.Store.PrioritiesForWorkspace(r.Context(), workspaceID); err != nil {
		http.Error(w, "Could not load priorities.", http.StatusInternalServerError)
		return
	}
	if data.Schemes, err = h.Store.PrioritySchemes(r.Context(), workspaceID); err != nil {
		http.Error(w, "Could not load priority schemes.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_priorities", user, workspaceID, data, "priorities", "")
}

// PrioritiesMutation creates, edits, reorders and deletes priorities.
func (h *Handler) PrioritiesMutation(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	notice, err := "", error(nil)
	value := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	switch r.PostFormValue("action") {
	case "create":
		name, color, description, icon := value("name"), value("statusColor"), r.PostFormValue("description"), value("iconUrl")
		var created models.Priority
		created, err = h.Store.CreatePriority(r.Context(), workspaceID, store.PriorityInput{Name: &name, StatusColor: &color, Description: &description, IconURL: &icon})
		if err == nil {
			notice = created.Name + " created."
		}
	case "update":
		name, color, description, icon := value("name"), value("statusColor"), r.PostFormValue("description"), value("iconUrl")
		if err = h.Store.UpdatePriority(r.Context(), workspaceID, value("priority"), store.PriorityInput{Name: &name, StatusColor: &color, Description: &description, IconURL: &icon}); err == nil {
			notice = name + " saved."
		}
	case "default":
		if err = h.Store.SetDefaultPriority(r.Context(), workspaceID, value("priority")); err == nil {
			notice = "Default priority set."
		}
	case "move":
		position := "First"
		after := value("after")
		if after != "" {
			position = ""
		}
		if err = h.Store.MovePriorities(r.Context(), workspaceID, []string{value("priority")}, after, position); err == nil {
			notice = "Priority moved."
		}
	case "delete":
		if _, err = h.Store.EnqueuePriorityDeletion(r.Context(), workspaceID, user.ID, value("priority")); err == nil {
			notice = "Priority deletion started; its work items take the default priority."
		}
	default:
		http.Error(w, "Unknown priority action.", http.StatusBadRequest)
		return
	}
	h.redirectMetadata(w, r, "/settings/priorities", notice, err)
}

// ResolutionsPage lists the site's resolutions.
func (h *Handler) ResolutionsPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	data := resolutionsPageData{Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	var err error
	if data.Resolutions, err = h.Store.ResolutionsForWorkspace(r.Context(), workspaceID); err != nil {
		http.Error(w, "Could not load resolutions.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_resolutions", user, workspaceID, data, "resolutions", "")
}

// ResolutionsMutation creates, edits, reorders and deletes resolutions.
func (h *Handler) ResolutionsMutation(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	notice, err := "", error(nil)
	value := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	switch r.PostFormValue("action") {
	case "create":
		var created models.Resolution
		created, err = h.Store.CreateResolution(r.Context(), workspaceID, value("name"), r.PostFormValue("description"))
		if err == nil {
			notice = created.Name + " created."
		}
	case "update":
		name := value("name")
		if err = h.Store.UpdateResolution(r.Context(), workspaceID, value("resolution"), name, r.PostFormValue("description"), true); err == nil {
			notice = name + " saved."
		}
	case "default":
		if err = h.Store.SetDefaultResolution(r.Context(), workspaceID, value("resolution")); err == nil {
			notice = "Default resolution set."
		}
	case "move":
		position := "First"
		after := value("after")
		if after != "" {
			position = ""
		}
		if err = h.Store.MoveResolutions(r.Context(), workspaceID, []string{value("resolution")}, after, position); err == nil {
			notice = "Resolution moved."
		}
	case "delete":
		if _, err = h.Store.EnqueueResolutionDeletion(r.Context(), workspaceID, user.ID, value("resolution"), value("replacement")); err == nil {
			notice = "Resolution deletion started; its work items take the replacement."
		}
	default:
		http.Error(w, "Unknown resolution action.", http.StatusBadRequest)
		return
	}
	h.redirectMetadata(w, r, "/settings/resolutions", notice, err)
}

// redirectMetadata returns to a metadata settings page with what happened.
func (h *Handler) redirectMetadata(w http.ResponseWriter, r *http.Request, path, notice string, err error) {
	if err == nil {
		redirectLocal(w, r, path+"?notice="+url.QueryEscape(notice))
		return
	}
	message := err.Error()
	for _, known := range []error{store.ErrIssueMetadataValidation, store.ErrIssueMetadataConflict, store.ErrIssueMetadataNotFound, store.ErrHierarchyValidation} {
		if errors.Is(err, known) {
			message = strings.TrimPrefix(message, known.Error()+": ")
			break
		}
	}
	redirectLocal(w, r, path+"?error="+url.QueryEscape(message))
}
