package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type securityMemberView struct {
	Member models.SecurityLevelMember
	Label  string
}

type securityLevelView struct {
	Level   models.SecurityLevel
	Members []securityMemberView
}

type securitySchemeCard struct {
	Scheme   *models.SecurityScheme
	Levels   []securityLevelView
	Projects []*models.Project
}

type issueSecuritySchemesData struct {
	Schemes  []securitySchemeCard
	Projects []*models.Project
	Members  []*models.User
	Groups   []*models.Group
	Roles    []*models.ProjectRole
	Fields   []*models.CustomField
	Notice   string
	Error    string
}

type projectIssueSecurityData struct {
	Project *models.Project
	Scheme  *securitySchemeCard
}

func issueSecurityMutationMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrIssueSecurityValidation), errors.Is(err, store.ErrIssueSecurityConflict),
		errors.Is(err, store.ErrIssueSecurityNotFound), errors.Is(err, store.ErrIssueSecurityTaskConflict),
		errors.Is(err, store.ErrProjectPermission):
		return err.Error()
	default:
		return "Could not update issue security schemes."
	}
}

func securityMemberLabel(member models.SecurityLevelMember, users []*models.User, groups []*models.Group, roles []*models.ProjectRole, fields []*models.CustomField) string {
	switch member.HolderType {
	case "reporter":
		return "Reporter"
	case "assignee":
		return "Current assignee"
	case "projectLead":
		return "Project lead"
	case "applicationRole":
		return "Product access: " + member.HolderValue
	case "user":
		for _, user := range users {
			if user.ID == member.HolderValue {
				return user.DisplayName
			}
		}
	case "group":
		for _, group := range groups {
			if group.ID == member.HolderValue {
				return group.Name
			}
		}
	case "projectRole":
		for _, role := range roles {
			if strconv.FormatInt(role.ID, 10) == member.HolderValue {
				return role.Name + " project role"
			}
		}
	case "userCustomField", "groupCustomField":
		for _, field := range fields {
			if field.ID == member.HolderValue {
				return field.Name + " (" + field.ID + ")"
			}
		}
	}
	if member.HolderParameter != "" {
		return member.HolderParameter
	}
	return member.HolderType
}

func issueSecurityCards(schemes []*models.SecurityScheme, projects []*models.Project, users []*models.User, groups []*models.Group, roles []*models.ProjectRole, fields []*models.CustomField) []securitySchemeCard {
	cards := make([]securitySchemeCard, 0, len(schemes))
	for _, scheme := range schemes {
		card := securitySchemeCard{Scheme: scheme}
		for _, level := range scheme.Levels {
			levelView := securityLevelView{Level: level}
			for _, member := range level.Grants {
				levelView.Members = append(levelView.Members, securityMemberView{Member: member, Label: securityMemberLabel(member, users, groups, roles, fields)})
			}
			card.Levels = append(card.Levels, levelView)
		}
		for _, project := range projects {
			if project.SecuritySchemeID == scheme.ID {
				card.Projects = append(card.Projects, project)
			}
		}
		cards = append(cards, card)
	}
	return cards
}

func (h *Handler) loadIssueSecuritySchemesPage(r *http.Request, workspaceID string) (issueSecuritySchemesData, error) {
	data := issueSecuritySchemesData{Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	var err error
	data.Projects, err = h.Store.ProjectsByWorkspace(r.Context(), workspaceID)
	if err == nil {
		data.Members, err = h.Store.MembersByWorkspace(r.Context(), workspaceID)
	}
	if err == nil {
		data.Groups, err = h.Store.GroupsByWorkspace(r.Context(), workspaceID)
	}
	if err == nil {
		data.Roles, err = h.Store.ProjectRoles(r.Context(), workspaceID)
	}
	if err == nil {
		data.Fields, err = h.Store.CustomFieldsForWorkspace(r.Context(), workspaceID)
	}
	var schemes []*models.SecurityScheme
	if err == nil {
		schemes, err = h.Store.IssueSecuritySchemes(r.Context(), workspaceID, true)
	}
	if err == nil {
		data.Schemes = issueSecurityCards(schemes, data.Projects, data.Members, data.Groups, data.Roles, data.Fields)
	}
	return data, err
}

func (h *Handler) IssueSecuritySchemesPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		_, err := h.Store.CreateIssueSecurityScheme(r.Context(), workspaceID, user.ID, r.PostFormValue("name"), r.PostFormValue("description"), nil)
		if err != nil {
			redirectLocal(w, r, "/settings/issue-security-schemes?error="+url.QueryEscape(issueSecurityMutationMessage(err)))
			return
		}
		redirectLocal(w, r, "/settings/issue-security-schemes?notice="+url.QueryEscape("Issue security scheme created."))
		return
	}
	data, err := h.loadIssueSecuritySchemesPage(r, workspaceID)
	if err != nil {
		http.Error(w, "Could not load issue security schemes.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_issue_security_schemes", user, workspaceID, data, "issue-security-schemes", "")
}

func securityMemberInputFromForm(r *http.Request) store.SecurityLevelMemberInput {
	kind := r.PostFormValue("holderType")
	parameter := r.PostFormValue("parameter")
	switch kind {
	case "user":
		parameter = r.PostFormValue("userValue")
	case "group":
		parameter = r.PostFormValue("groupValue")
	case "projectRole":
		parameter = r.PostFormValue("roleValue")
	case "userCustomField", "groupCustomField":
		parameter = r.PostFormValue("fieldValue")
	case "applicationRole":
		parameter = r.PostFormValue("applicationValue")
	}
	return store.SecurityLevelMemberInput{Type: kind, Parameter: parameter}
}

func (h *Handler) IssueSecuritySchemeMutation(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	schemeID := r.PathValue("id")
	if schemeID == "" {
		http.NotFound(w, r)
		return
	}
	notice := "Issue security scheme updated."
	var err error
	switch r.PostFormValue("action") {
	case "update":
		name, description := r.PostFormValue("name"), r.PostFormValue("description")
		err = h.Store.UpdateIssueSecurityScheme(r.Context(), workspaceID, user.ID, schemeID, &name, &description)
	case "delete":
		err = h.Store.DeleteIssueSecurityScheme(r.Context(), workspaceID, user.ID, schemeID)
		notice = "Issue security scheme deleted."
	case "add-level":
		err = h.Store.AddIssueSecurityLevels(r.Context(), workspaceID, user.ID, schemeID, []store.SecurityLevelInput{{Name: r.PostFormValue("name"), Description: r.PostFormValue("description"), Default: r.PostFormValue("default") == "true"}})
		notice = "Security level added."
	case "update-level":
		name, description := r.PostFormValue("name"), r.PostFormValue("description")
		err = h.Store.UpdateIssueSecurityLevel(r.Context(), workspaceID, user.ID, schemeID, r.PostFormValue("levelId"), &name, &description)
	case "set-default":
		err = h.Store.SetIssueSecurityDefaults(r.Context(), workspaceID, user.ID, map[string]string{schemeID: r.PostFormValue("levelId")})
		notice = "Default security level updated."
	case "add-member":
		err = h.Store.AddIssueSecurityMembers(r.Context(), workspaceID, user.ID, schemeID, r.PostFormValue("levelId"), []store.SecurityLevelMemberInput{securityMemberInputFromForm(r)})
		notice = "Level member added."
	case "remove-member":
		var memberID int64
		memberID, err = strconv.ParseInt(r.PostFormValue("memberId"), 10, 64)
		if err == nil {
			err = h.Store.DeleteIssueSecurityMember(r.Context(), workspaceID, user.ID, schemeID, r.PostFormValue("levelId"), memberID)
		}
		notice = "Level member removed."
	case "remove-level":
		_, err = h.Store.EnqueueRemoveIssueSecurityLevel(r.Context(), workspaceID, user.ID, schemeID, r.PostFormValue("levelId"), r.PostFormValue("replaceWith"))
		notice = "Security level removal queued."
	case "assign-project":
		projectID := r.PostFormValue("project")
		_, err = h.Store.EnqueueAssignIssueSecurityScheme(r.Context(), workspaceID, user.ID, projectID, schemeID, nil)
		notice = "Project assignment queued."
	default:
		err = store.ErrIssueSecurityValidation
	}
	target := "/settings/issue-security-schemes"
	if err != nil {
		redirectLocal(w, r, target+"?error="+url.QueryEscape(issueSecurityMutationMessage(err)))
		return
	}
	redirectLocal(w, r, target+"?notice="+url.QueryEscape(notice))
}

func (h *Handler) ProjectIssueSecurityPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, project, ok := h.requireProjectAdminPage(w, r)
	if !ok {
		return
	}
	scheme, _, err := h.Store.AssignedIssueSecurityScheme(r.Context(), workspaceID, project.ID, true)
	if err != nil {
		http.Error(w, "Could not load project issue security.", http.StatusInternalServerError)
		return
	}
	data := projectIssueSecurityData{Project: project}
	if scheme != nil {
		users, loadErr := h.Store.MembersByWorkspace(r.Context(), workspaceID)
		if loadErr != nil {
			http.Error(w, "Could not load project issue security.", http.StatusInternalServerError)
			return
		}
		groups, loadErr := h.Store.GroupsByWorkspace(r.Context(), workspaceID)
		if loadErr != nil {
			http.Error(w, "Could not load project issue security.", http.StatusInternalServerError)
			return
		}
		roles, loadErr := h.Store.ProjectRoles(r.Context(), workspaceID)
		if loadErr != nil {
			http.Error(w, "Could not load project issue security.", http.StatusInternalServerError)
			return
		}
		fields, loadErr := h.Store.CustomFieldsForWorkspace(r.Context(), workspaceID)
		if loadErr != nil {
			http.Error(w, "Could not load project issue security.", http.StatusInternalServerError)
			return
		}
		cards := issueSecurityCards([]*models.SecurityScheme{scheme}, []*models.Project{project}, users, groups, roles, fields)
		data.Scheme = &cards[0]
	}
	h.writeWorkspacePage(w, r, "page_project_issue_security", user, workspaceID, data, "project-issue-security", project.Key)
}
