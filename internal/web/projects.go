package web

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
	"github.com/jackc/pgx/v5"
)

type projectSettingsData struct {
	Project  *models.Project
	Members  []*models.User
	Boards   []*models.Board
	Values   commands.CreateProjectInput
	Errors   map[string]string
	Creating bool
	Saved    bool
}

func (h *Handler) NewProject(w http.ResponseWriter, r *http.Request) { h.projectSettings(w, r, "") }
func (h *Handler) ProjectSettings(w http.ResponseWriter, r *http.Request) {
	h.projectSettings(w, r, r.PathValue("key"))
}

func (h *Handler) projectSettings(w http.ResponseWriter, r *http.Request, key string) {
	user, wsID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	data := projectSettingsData{Creating: key == "", Saved: r.URL.Query().Get("saved") == "1"}
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
	} else {
		data.Values = commands.CreateProjectInput{LeadAccountID: user.ID, AssigneeType: "UNASSIGNED", ProjectTemplateKey: "com.pyxis.greenhopper.jira:gh-simplified-scrum-classic"}
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		data.Saved = false
		data.Values = commands.CreateProjectInput{Key: r.PostFormValue("key"), Name: r.PostFormValue("name"), Description: r.PostFormValue("description"), URL: r.PostFormValue("url"), LeadAccountID: r.PostFormValue("leadAccountId"), AssigneeType: r.PostFormValue("assigneeType"), ProjectTypeKey: "software", ProjectTemplateKey: r.PostFormValue("projectTemplateKey")}
		var p *models.Project
		var err error
		if data.Creating {
			p, err = h.Commands.CreateProject(r.Context(), user.ID, wsID, data.Values)
		} else {
			p, err = h.Commands.UpdateProject(r.Context(), user.ID, wsID, key, store.ProjectUpdate{Name: &data.Values.Name, Description: &data.Values.Description, URL: &data.Values.URL, LeadAccountID: &data.Values.LeadAccountID, AssigneeType: &data.Values.AssigneeType})
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
	}
	active := "project-settings"
	if data.Creating {
		active = "projects"
	}
	h.writeWorkspacePageStatus(w, r, "page_project_settings", user, wsID, data, active, key, status)
}
