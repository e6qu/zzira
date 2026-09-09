package web

import (
	"encoding/json"
	"errors"
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
	Project            *models.Project
	Members            []*models.User
	Boards             []*models.Board
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
	Creating           bool
	Saved              bool
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
	user, wsID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	data := projectSettingsData{Creating: key == "", Saved: r.URL.Query().Get("saved") == "1"}
	data.ComponentNotice, data.ComponentError = r.URL.Query().Get("component"), r.URL.Query().Get("componentError")
	data.GovernanceNotice, data.GovernanceError = r.URL.Query().Get("governance"), r.URL.Query().Get("governanceError")
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
		if projectTemplateKey == "com.atlassian.servicedesk:simplified-it-service-management" {
			projectTypeKey = "service_desk"
		} else if projectTemplateKey == "com.atlassian.jira-core-project-templates:jira-core-simplified-project-management" {
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
	}
	active := "project-settings"
	if data.Creating {
		active = "projects"
	}
	h.writeWorkspacePageStatus(w, r, "page_project_settings", user, wsID, data, active, key, status)
}

func (h *Handler) ProjectGovernanceSettings(w http.ResponseWriter, r *http.Request) {
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
