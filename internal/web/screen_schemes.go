package web

import (
	"net/http"
	"net/url"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type screenSchemeCard struct {
	Scheme  *models.ScreenScheme
	Screens map[string]string
}

type issueTypeMappingView struct {
	IssueTypeName    string
	IssueTypeID      string
	ScreenSchemeName string
	ScreenSchemeID   string
	IsDefault        bool
}

type issueTypeSchemeCard struct {
	Scheme   *models.IssueTypeScreenScheme
	Mappings []issueTypeMappingView
	Projects []*models.Project
}

type screenSchemesData struct {
	ScreenSchemes    []screenSchemeCard
	IssueTypeSchemes []issueTypeSchemeCard
	Screens          []*models.Screen
	IssueTypes       []models.IssueType
	Projects         []*models.Project
	Operations       []string
	Notice           string
	Error            string
}

func (h *Handler) loadScreenSchemesPage(r *http.Request, workspaceID string) (screenSchemesData, error) {
	data := screenSchemesData{Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error"),
		Operations: []string{"default", "create", "edit", "view"}}
	var err error
	if data.Screens, err = h.Store.Screens(r.Context(), workspaceID, store.ScreenFilter{}); err != nil {
		return data, err
	}
	if data.IssueTypes, err = h.Store.IssueTypes(r.Context()); err != nil {
		return data, err
	}
	if data.Projects, err = h.Store.ProjectsByWorkspace(r.Context(), workspaceID); err != nil {
		return data, err
	}
	screenNames := map[string]string{}
	for _, screen := range data.Screens {
		screenNames[screen.ID] = screen.Name
	}
	schemes, err := h.Store.ScreenSchemes(r.Context(), workspaceID, nil)
	if err != nil {
		return data, err
	}
	schemeNames := map[string]string{}
	for _, scheme := range schemes {
		schemeNames[scheme.ID] = scheme.Name
		named := map[string]string{}
		for operation, screenID := range scheme.Screens {
			named[operation] = screenNames[screenID]
		}
		data.ScreenSchemes = append(data.ScreenSchemes, screenSchemeCard{Scheme: scheme, Screens: named})
	}
	typeNames := map[string]string{store.DefaultIssueTypeMapping: "Every other work type"}
	for _, issueType := range data.IssueTypes {
		typeNames[issueType.ID] = issueType.Name
	}
	issueTypeSchemes, err := h.Store.IssueTypeScreenSchemes(r.Context(), workspaceID, nil)
	if err != nil {
		return data, err
	}
	assignments, err := h.Store.IssueTypeScreenSchemeProjects(r.Context(), workspaceID)
	if err != nil {
		return data, err
	}
	projectsByID := map[string]*models.Project{}
	for _, project := range data.Projects {
		projectsByID[project.ID] = project
	}
	for _, scheme := range issueTypeSchemes {
		card := issueTypeSchemeCard{Scheme: scheme}
		for _, mapping := range scheme.Mappings {
			card.Mappings = append(card.Mappings, issueTypeMappingView{
				IssueTypeID: mapping.IssueTypeID, IssueTypeName: typeNames[mapping.IssueTypeID],
				ScreenSchemeID: mapping.ScreenSchemeID, ScreenSchemeName: schemeNames[mapping.ScreenSchemeID],
				IsDefault: mapping.IssueTypeID == store.DefaultIssueTypeMapping,
			})
		}
		for _, assignment := range assignments {
			if assignment.SchemeID == scheme.ID {
				if project, ok := projectsByID[assignment.ProjectID]; ok {
					card.Projects = append(card.Projects, project)
				}
			}
		}
		data.IssueTypeSchemes = append(data.IssueTypeSchemes, card)
	}
	return data, nil
}

func (h *Handler) ScreenSchemesPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		var err error
		notice := "Screen scheme created."
		switch r.PostFormValue("kind") {
		case "issue-type-scheme":
			_, err = h.Store.CreateIssueTypeScreenScheme(r.Context(), workspaceID, user.ID,
				r.PostFormValue("name"), r.PostFormValue("description"),
				[]models.IssueTypeScreenSchemeItem{{
					IssueTypeID: store.DefaultIssueTypeMapping, ScreenSchemeID: r.PostFormValue("screenSchemeId")}})
			notice = "Work type screen scheme created."
		default:
			_, err = h.Store.CreateScreenScheme(r.Context(), workspaceID, user.ID,
				r.PostFormValue("name"), r.PostFormValue("description"),
				map[string]string{"default": r.PostFormValue("defaultScreenId")})
		}
		if err != nil {
			redirectLocal(w, r, "/settings/screen-schemes?error="+url.QueryEscape(screenMutationMessage(err)))
			return
		}
		redirectLocal(w, r, "/settings/screen-schemes?notice="+url.QueryEscape(notice))
		return
	}
	data, err := h.loadScreenSchemesPage(r, workspaceID)
	if err != nil {
		http.Error(w, "Could not load screen schemes.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_screen_schemes", user, workspaceID, data, "screen-schemes", "")
}

func (h *Handler) ScreenSchemeMutation(w http.ResponseWriter, r *http.Request) {
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
	notice := "Screen scheme updated."
	var err error
	switch r.PostFormValue("action") {
	case "set-screen":
		_, err = h.Store.UpdateScreenScheme(r.Context(), workspaceID, user.ID, schemeID, nil, nil,
			map[string]string{r.PostFormValue("operation"): r.PostFormValue("screenId")})
		notice = "Screen scheme mapping saved."
	case "delete-screen-scheme":
		err = h.Store.DeleteScreenScheme(r.Context(), workspaceID, user.ID, schemeID)
		notice = "Screen scheme deleted."
	case "map-issue-type":
		err = h.Store.AppendIssueTypeScreenSchemeMappings(r.Context(), workspaceID, user.ID, schemeID,
			[]models.IssueTypeScreenSchemeItem{{
				IssueTypeID: r.PostFormValue("issueTypeId"), ScreenSchemeID: r.PostFormValue("screenSchemeId")}})
		notice = "Work type mapping saved."
	case "remove-issue-type":
		err = h.Store.RemoveIssueTypeScreenSchemeMappings(r.Context(), workspaceID, user.ID, schemeID,
			[]string{r.PostFormValue("issueTypeId")})
		notice = "Work type mapping removed."
	case "assign-project":
		err = h.Store.AssignIssueTypeScreenScheme(r.Context(), workspaceID, user.ID, r.PostFormValue("project"), schemeID)
		notice = "Project assigned to the work type screen scheme."
	case "delete-issue-type-scheme":
		err = h.Store.DeleteIssueTypeScreenScheme(r.Context(), workspaceID, user.ID, schemeID)
		notice = "Work type screen scheme deleted."
	default:
		err = store.ErrScreenValidation
	}
	target := "/settings/screen-schemes"
	if err != nil {
		redirectLocal(w, r, target+"?error="+url.QueryEscape(screenMutationMessage(err)))
		return
	}
	redirectLocal(w, r, target+"?notice="+url.QueryEscape(notice))
}
