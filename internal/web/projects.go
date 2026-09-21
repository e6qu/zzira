package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

type projectSettingsData struct {
	Project *models.Project
	Members []*models.User
	Boards  []*models.Board
	// BoardNotice and BoardError report what happened to a board, so a
	// refused create or delete says so rather than looking like a dead button.
	BoardNotice        string
	BoardError         string
	Components         []*models.ProjectComponent
	Categories         []*models.ProjectCategory
	Features           []models.ProjectFeature
	Properties         []projectPropertyView
	SelectedCategoryID string
	Values             commands.CreateProjectInput
	Errors             map[string]string
	ComponentNotice    string
	ComponentError     string
	GovernanceNotice   string
	GovernanceError    string
	Templates          []store.ProjectTemplate
	TemplateNotice     string
	TemplateError      string
	Creating           bool
	Saved              bool
	// SiteAdmin carries the site-level actions this page also hosts, so a
	// project administrator is not shown a control their role cannot use.
	SiteAdmin bool
	// ProjectWorkflow reports that the project routes through a workflow of
	// its own, which its administrators may edit. A shared workflow is a
	// site administrator's to change, so the page offers to start one.
	ProjectWorkflow bool
}

type projectPropertyView struct{ Key, Value string }

func (h *Handler) NewProject(w http.ResponseWriter, r *http.Request) { h.projectSettings(w, r, "") }
func (h *Handler) ProjectSettings(w http.ResponseWriter, r *http.Request) {
	h.projectSettings(w, r, r.PathValue("key"))
}

func (h *Handler) ProjectLifecycleSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	project, err := h.Store.ProjectByIDOrKeyAnyState(r.Context(), workspaceID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	action := r.PostFormValue("action")
	target := "/projects"
	notice := "Project updated."
	switch action {
	case "archive":
		_, err = h.Store.ArchiveProject(r.Context(), workspaceID, user.ID, project.ID)
		target, notice = "/projects?status=archived", "Project archived."
	case "trash":
		_, err = h.Store.TrashProject(r.Context(), workspaceID, user.ID, project.ID)
		target, notice = "/projects?status=trash", "Project moved to trash."
	case "restore":
		_, err = h.Store.RestoreProject(r.Context(), workspaceID, user.ID, project.ID)
		notice = "Project restored."
	case "delete":
		if project.LifecycleState != store.ProjectLifecycleTrashed {
			err = store.ErrProjectLifecycleConflict
		} else {
			err = h.Store.PermanentDeleteProject(r.Context(), workspaceID, user.ID, project.ID, nil)
		}
		target, notice = "/projects?status=trash", "Project permanently deleted."
	default:
		err = store.ErrProjectLifecycleConflict
	}
	separator := "?"
	if strings.Contains(target, "?") {
		separator = "&"
	}
	if err != nil {
		redirectLocal(w, r, target+separator+"error="+url.QueryEscape(err.Error()))
		return
	}
	redirectLocal(w, r, target+separator+"notice="+url.QueryEscape(notice))
}

func (h *Handler) projectSettings(w http.ResponseWriter, r *http.Request, key string) {
	// Creating a project needs site administration; a project's administrators
	// edit its details, components, properties and features.
	var (
		user *models.User
		wsID string
		ok   bool
	)
	if key == "" {
		user, wsID, ok = h.requireAdminPage(w, r)
	} else if user, wsID, ok = h.pageContext(w, r); ok {
		project, err := h.Store.ProjectByIDOrKey(r.Context(), wsID, key)
		if err == nil {
			allowed, permissionErr := h.Store.CanAdministerProject(r.Context(), wsID, user.ID, project.ID)
			if permissionErr != nil || !allowed {
				http.Error(w, "forbidden", http.StatusForbidden)
				ok = false
			}
		}
	}
	if !ok {
		return
	}
	data := projectSettingsData{Creating: key == "", Saved: r.URL.Query().Get("saved") == "1"}
	data.ComponentNotice, data.ComponentError = r.URL.Query().Get("component"), r.URL.Query().Get("componentError")
	data.GovernanceNotice, data.GovernanceError = r.URL.Query().Get("governance"), r.URL.Query().Get("governanceError")
	data.TemplateNotice, data.TemplateError = r.URL.Query().Get("template"), r.URL.Query().Get("templateError")
	data.BoardNotice, data.BoardError = r.URL.Query().Get("board"), r.URL.Query().Get("boardError")
	var err error
	data.Categories, err = h.Store.ProjectCategories(r.Context(), wsID)
	if err != nil {
		http.Error(w, "Could not load project categories.", 500)
		return
	}
	if key != "" {
		p, err := h.Store.ProjectByIDOrKey(r.Context(), wsID, key)
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Could not load project.", 500)
			return
		}
		data.Project = p
		if data.Templates, err = h.Store.ProjectTemplates(r.Context(), wsID); err != nil {
			http.Error(w, "Could not load project templates.", 500)
			return
		}
		data.Values = commands.CreateProjectInput{Key: p.Key, Name: p.Name, Description: p.Description, URL: p.URL, LeadAccountID: p.LeadAccountID, AssigneeType: p.AssigneeType}
		data.SelectedCategoryID = p.CategoryID
	} else {
		data.Values = commands.CreateProjectInput{LeadAccountID: user.ID, AssigneeType: "UNASSIGNED", ProjectTemplateKey: "com.pyxis.greenhopper.jira:gh-simplified-scrum-classic"}
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		data.Saved = false
		projectTemplateKey := r.PostFormValue("projectTemplateKey")
		projectTypeKey := "software"
		switch projectTemplateKey {
		case "com.atlassian.servicedesk:simplified-it-service-management":
			projectTypeKey = "service_desk"
		case "com.atlassian.jira-core-project-templates:jira-core-simplified-project-management":
			projectTypeKey = "business"
		}
		data.SelectedCategoryID = r.PostFormValue("categoryId")
		var categoryID int64
		if data.SelectedCategoryID != "" {
			categoryID, err = strconv.ParseInt(data.SelectedCategoryID, 10, 64)
			if err != nil {
				data.Errors = map[string]string{"categoryId": "Choose a valid project category."}
			}
		}
		data.Values = commands.CreateProjectInput{Key: r.PostFormValue("key"), Name: r.PostFormValue("name"), Description: r.PostFormValue("description"), URL: r.PostFormValue("url"), LeadAccountID: r.PostFormValue("leadAccountId"), AssigneeType: r.PostFormValue("assigneeType"), ProjectTypeKey: projectTypeKey, ProjectTemplateKey: projectTemplateKey, CategoryID: categoryID}
		var p *models.Project
		if data.Errors != nil {
			err = &commands.ProjectValidationError{Fields: data.Errors}
		} else if data.Creating {
			p, err = h.Commands.CreateProject(r.Context(), user.ID, wsID, data.Values)
		} else {
			category := categoryID
			if data.SelectedCategoryID == "" {
				category = -1
			}
			p, err = h.Commands.UpdateProject(r.Context(), user.ID, wsID, key, store.ProjectUpdate{Name: &data.Values.Name, Description: &data.Values.Description, URL: &data.Values.URL, LeadAccountID: &data.Values.LeadAccountID, AssigneeType: &data.Values.AssigneeType, CategoryID: &category})
		}
		if err == nil {
			target := "/projects/" + p.Key
			if !data.Creating {
				target += "/settings?saved=1"
			}
			redirectLocal(w, r, target)
			return
		}
		var validation *commands.ProjectValidationError
		if !errors.As(err, &validation) {
			log.Print("save project settings: ", strconv.Quote(err.Error()))
			http.Error(w, "Could not save project.", 500)
			return
		}
		data.Errors = validation.Fields
		status = http.StatusBadRequest
	}
	members, err := h.Store.MembersByWorkspace(r.Context(), wsID)
	if err != nil {
		http.Error(w, "Could not load project members.", 500)
		return
	}
	data.Members = members
	if data.Project != nil {
		boards, err := h.Store.BoardsByWorkspace(r.Context(), wsID)
		if err != nil {
			http.Error(w, "Could not load boards.", 500)
			return
		}
		for _, board := range boards {
			if board.ProjectID == data.Project.ID {
				data.Boards = append(data.Boards, board)
			}
		}
		data.Components, err = h.Store.Components(r.Context(), wsID, data.Project.ID, "", "name")
		if err != nil {
			http.Error(w, "Could not load components.", 500)
			return
		}
		properties, propertyErr := h.Store.ProjectProperties(r.Context(), wsID, data.Project.ID)
		err = propertyErr
		if err != nil {
			http.Error(w, "Could not load project properties.", 500)
			return
		}
		for _, property := range properties {
			data.Properties = append(data.Properties, projectPropertyView{Key: property.Key, Value: string(property.Value)})
		}
		if data.Project.ProjectTypeKey == "software" {
			data.Features, err = h.Store.ProjectFeatures(r.Context(), wsID, data.Project.ID)
			if err != nil {
				http.Error(w, "Could not load project features.", 500)
				return
			}
		}
		if data.Project.WorkflowID != "" {
			wf, workflowErr := h.Store.WorkflowByID(r.Context(), wsID, data.Project.WorkflowID)
			if workflowErr == nil {
				data.ProjectWorkflow = wf.ProjectID == data.Project.ID
			}
		}
	}
	data.SiteAdmin, err = h.Store.IsAdmin(r.Context(), wsID, user.ID)
	if err != nil {
		http.Error(w, "Could not read site administration.", 500)
		return
	}
	active := "project-settings"
	if data.Creating {
		active = "projects"
	}
	h.writeWorkspacePageStatus(w, r, "page_project_settings", user, wsID, data, active, key, status)
}

// ProjectTemplateSettings saves a template from the project, renames one, or
// removes one. Each refusal says why, the way the page's other sections do.
func (h *Handler) ProjectTemplateSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !parseForm(w, r) {
		return
	}
	notice := "Template saved."
	switch r.PostFormValue("action") {
	case "save":
		templateType := r.PostFormValue("templateType")
		if templateType != "LIVE" && templateType != "SNAPSHOT" {
			err = fmt.Errorf("%w: The template type must be LIVE or SNAPSHOT.", store.ErrProjectTemplateValidation)
			break
		}
		_, err = h.Store.SaveProjectTemplate(r.Context(), workspaceID, user.ID, project.ID, templateType,
			r.PostFormValue("templateName"), r.PostFormValue("templateDescription"), nil)
	case "edit":
		name, description := r.PostFormValue("templateName"), r.PostFormValue("templateDescription")
		err = h.Store.EditProjectTemplate(r.Context(), workspaceID, r.PostFormValue("templateKey"), &name, &description, nil)
		notice = "Template renamed."
	case "remove":
		err = h.Store.RemoveProjectTemplate(r.Context(), workspaceID, r.PostFormValue("templateKey"))
		notice = "Template removed."
	default:
		http.Error(w, "action must be save, edit or remove", http.StatusBadRequest)
		return
	}
	target := "/projects/" + url.PathEscape(project.Key) + "/settings"
	if err != nil {
		message := err.Error()
		for _, prefix := range []string{store.ErrProjectTemplateValidation.Error() + ": ", store.ErrProjectTemplateNotFound.Error() + ": "} {
			message = strings.TrimPrefix(message, prefix)
		}
		if errors.Is(err, store.ErrProjectTemplateNotFound) && message == store.ErrProjectTemplateNotFound.Error() {
			message = "That template no longer exists."
		}
		http.Redirect(w, r, target+"?templateError="+url.QueryEscape(message), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, target+"?template="+url.QueryEscape(notice), http.StatusSeeOther)
}

// ProjectGovernanceSettings saves the sender, features and app properties a
// project's administrators own, which is what the settings page promises and
// what the store enforces on each of these calls.
func (h *Handler) ProjectGovernanceSettings(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, project, ok := h.requireProjectAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	var err error
	action := r.PostFormValue("action")
	notice := "Project configuration saved."
	switch action {
	case "email":
		email := strings.TrimSpace(r.PostFormValue("emailAddress"))
		if email != "" {
			address, parseErr := mail.ParseAddress(email)
			if parseErr != nil || address.Address != email || len(email) > 254 {
				err = errors.New("enter a valid sender email address")
			}
		}
		if err == nil {
			err = h.Store.SetProjectEmail(r.Context(), workspaceID, user.ID, project.ID, email)
		}
		notice = "Sender email saved."
	case "feature":
		state := r.PostFormValue("state")
		if state != "ENABLED" && state != "DISABLED" {
			err = errors.New("feature state must be enabled or disabled")
		} else {
			_, err = h.Store.SetProjectFeature(r.Context(), workspaceID, user.ID, project.ID, r.PostFormValue("featureKey"), state)
		}
		notice = "Project feature saved."
	case "property":
		key := strings.TrimSpace(r.PostFormValue("propertyKey"))
		raw := []byte(r.PostFormValue("value"))
		if key == "" || len(key) > 255 || len(raw) == 0 || len(raw) > 32768 || !json.Valid(raw) {
			err = errors.New("property key and JSON value are required")
		} else {
			_, _, err = h.Store.SetProjectProperty(r.Context(), workspaceID, user.ID, project.ID, key, json.RawMessage(raw))
		}
		notice = "Project property saved."
	case "delete-property":
		err = h.Store.DeleteProjectProperty(r.Context(), workspaceID, user.ID, project.ID, r.PostFormValue("propertyKey"))
		notice = "Project property deleted."
	default:
		err = errors.New("unknown project configuration action")
	}
	target := "/projects/" + project.Key + "/settings"
	if err != nil {
		redirectLocal(w, r, target+"?governanceError="+url.QueryEscape(err.Error())+"#project-governance")
		return
	}
	redirectLocal(w, r, target+"?governance="+url.QueryEscape(notice)+"#project-governance")
}

func (h *Handler) ProjectComponentSettings(w http.ResponseWriter, r *http.Request) {
	// Project administrators manage their project's components.
	user, workspaceID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), workspaceID, r.PathValue("key"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if allowed, permissionErr := h.Store.CanAdministerProject(r.Context(), workspaceID, user.ID, project.ID); permissionErr != nil || !allowed {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !parseForm(w, r) {
		return
	}
	action := r.PostFormValue("action")
	input := store.ComponentInput{
		ProjectIDOrKey: project.ID,
		Name:           r.PostFormValue("name"), Description: r.PostFormValue("description"),
		LeadAccountID: r.PostFormValue("leadAccountId"), AssigneeType: r.PostFormValue("assigneeType"),
	}
	switch action {
	case "create":
		_, err = h.Store.CreateComponent(r.Context(), workspaceID, user.ID, input)
	case "update":
		_, err = h.Store.UpdateComponent(r.Context(), workspaceID, user.ID, r.PathValue("id"), input)
	case "delete":
		err = h.Store.DeleteComponent(r.Context(), workspaceID, user.ID, r.PathValue("id"), r.PostFormValue("moveIssuesTo"))
	default:
		err = store.ErrComponentValidation
	}
	target := "/projects/" + project.Key + "/settings"
	if err != nil {
		redirectLocal(w, r, target+"?componentError="+url.QueryEscape(err.Error()))
		return
	}
	notice := map[string]string{"create": "created", "update": "updated", "delete": "deleted"}[action]
	redirectLocal(w, r, target+"?component="+notice)
}

// ProjectBoards creates and deletes a project's boards from the settings page
// that lists them. It is its own route rather than another action on the
// project form, because that form saves the project itself.
func (h *Handler) ProjectBoards(w http.ResponseWriter, r *http.Request) {
	user, wsID, ok := h.pageContext(w, r)
	if !ok {
		return
	}
	project, err := h.Store.ProjectByIDOrKey(r.Context(), wsID, r.PathValue("key"))
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Could not load project.", 500)
		return
	}
	allowed, err := h.Store.CanAdministerProject(r.Context(), wsID, user.ID, project.ID)
	if err != nil || !allowed {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !parseForm(w, r) {
		return
	}
	settings := "/projects/" + project.Key + "/settings"
	notice := ""
	switch r.PostFormValue("action") {
	case "create":
		_, err = h.Store.CreateBoard(r.Context(), user.ID, wsID, store.BoardCreate{
			Name: r.PostFormValue("name"), Type: r.PostFormValue("type"), ProjectID: project.ID,
		})
		notice = "Board created."
	case "delete":
		// The board is found through the project's own boards, so one from
		// another project cannot be deleted by naming its id here.
		boards, listErr := h.Store.BoardsByWorkspace(r.Context(), wsID)
		if listErr != nil {
			http.Error(w, "Could not load boards.", 500)
			return
		}
		id, found := strings.TrimSpace(r.PostFormValue("board")), false
		for _, board := range boards {
			if board.ID == id && board.ProjectID == project.ID {
				found = true
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}
		err = h.Store.DeleteBoard(r.Context(), user.ID, wsID, id)
		notice = "Board deleted. Its sprints went with it; the work items stayed."
	default:
		http.Error(w, "Unknown board action.", http.StatusBadRequest)
		return
	}
	if err != nil {
		message := "Could not change this project's boards."
		if errors.Is(err, store.ErrBoardValidation) {
			message = err.Error()
		}
		redirectLocal(w, r, settings+"?boardError="+url.QueryEscape(message))
		return
	}
	redirectLocal(w, r, settings+"?board="+url.QueryEscape(notice))
}

type projectConfigurationData struct {
	Project *models.Project
	Entries []store.ProjectConfigurationEntry
	// Lead, Category and Sender repeat the project's own details, so the page
	// answers "how is this project configured" without a second stop.
	Lead     string
	Category string
	Sender   string
}

// ProjectConfigurationPage names every scheme the project routes through and
// where each one is changed. Jira calls this a project's summary; without it
// a manager has to open each site directory and look for their project.
func (h *Handler) ProjectConfigurationPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, project, ok := h.requireProjectAdminPage(w, r)
	if !ok {
		return
	}
	entries, err := h.Store.ProjectConfigurationSummary(r.Context(), workspaceID, project.ID)
	if err != nil {
		http.Error(w, "Could not load the project configuration.", http.StatusInternalServerError)
		return
	}
	data := projectConfigurationData{Project: project, Entries: entries, Sender: project.SenderEmail}
	if project.LeadAccountID != "" {
		if lead, err := h.Store.UserByID(r.Context(), project.LeadAccountID); err == nil && lead != nil {
			data.Lead = lead.DisplayName
		}
	}
	categories, err := h.Store.ProjectCategories(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "Could not load project categories.", http.StatusInternalServerError)
		return
	}
	for _, category := range categories {
		if category.ID == project.CategoryID {
			data.Category = category.Name
		}
	}
	h.writeWorkspacePage(w, r, "page_project_configuration", user, workspaceID, data, "project-configuration", project.Key)
}
