package web

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type fieldConfigurationCard struct {
	Configuration *models.FieldConfiguration
	Items         []models.FieldConfigurationItem
	Available     []models.ScreenField
}

type fieldConfigSchemeMappingView struct {
	IssueTypeID       string
	IssueTypeName     string
	ConfigurationID   string
	ConfigurationName string
	IsDefault         bool
}

type fieldConfigSchemeCard struct {
	Scheme   *models.FieldConfigurationScheme
	Mappings []fieldConfigSchemeMappingView
	Projects []*models.Project
}

type fieldConfigurationsData struct {
	Configurations []fieldConfigurationCard
	Schemes        []fieldConfigSchemeCard
	IssueTypes     []models.IssueType
	Projects       []*models.Project
	Notice         string
	Error          string
}

func fieldConfigMutationMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrFieldConfigValidation), errors.Is(err, store.ErrFieldConfigConflict),
		errors.Is(err, store.ErrFieldConfigNotFound), errors.Is(err, store.ErrProjectPermission):
		return err.Error()
	default:
		return "Could not update field configurations."
	}
}

func (h *Handler) loadFieldConfigurationsPage(r *http.Request, workspaceID string) (fieldConfigurationsData, error) {
	data := fieldConfigurationsData{Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	var err error
	if data.IssueTypes, err = h.Store.IssueTypes(r.Context()); err != nil {
		return data, err
	}
	if data.Projects, err = h.Store.ProjectsByWorkspace(r.Context(), workspaceID); err != nil {
		return data, err
	}
	catalog, err := h.Store.ScreenFieldCatalog(r.Context(), workspaceID)
	if err != nil {
		return data, err
	}
	configurations, err := h.Store.FieldConfigurations(r.Context(), workspaceID, nil)
	if err != nil {
		return data, err
	}
	names := map[string]string{}
	for _, configuration := range configurations {
		names[configuration.ID] = configuration.Name
		items, itemErr := h.Store.FieldConfigurationItems(r.Context(), workspaceID, configuration.ID)
		if itemErr != nil {
			return data, itemErr
		}
		governed := map[string]bool{}
		for _, item := range items {
			governed[item.FieldID] = true
		}
		available := []models.ScreenField{}
		for _, field := range catalog {
			if !governed[field.ID] {
				available = append(available, field)
			}
		}
		data.Configurations = append(data.Configurations, fieldConfigurationCard{
			Configuration: configuration, Items: items, Available: available})
	}
	typeNames := map[string]string{store.DefaultIssueTypeMapping: "Every other work type"}
	for _, issueType := range data.IssueTypes {
		typeNames[issueType.ID] = issueType.Name
	}
	schemes, err := h.Store.FieldConfigurationSchemes(r.Context(), workspaceID, nil)
	if err != nil {
		return data, err
	}
	assignments, err := h.Store.FieldConfigurationSchemeProjects(r.Context(), workspaceID)
	if err != nil {
		return data, err
	}
	projectsByID := map[string]*models.Project{}
	for _, project := range data.Projects {
		projectsByID[project.ID] = project
	}
	for _, scheme := range schemes {
		card := fieldConfigSchemeCard{Scheme: scheme}
		for _, mapping := range scheme.Mappings {
			card.Mappings = append(card.Mappings, fieldConfigSchemeMappingView{
				IssueTypeID: mapping.IssueTypeID, IssueTypeName: typeNames[mapping.IssueTypeID],
				ConfigurationID: mapping.FieldConfigurationID, ConfigurationName: names[mapping.FieldConfigurationID],
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
		data.Schemes = append(data.Schemes, card)
	}
	return data, nil
}

func (h *Handler) FieldConfigurationsPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		if !parseForm(w, r) {
			return
		}
		var err error
		notice := "Field configuration created."
		if r.PostFormValue("kind") == "scheme" {
			_, err = h.Store.CreateFieldConfigurationScheme(r.Context(), workspaceID, user.ID,
				r.PostFormValue("name"), r.PostFormValue("description"))
			notice = "Field configuration scheme created."
		} else {
			_, err = h.Store.CreateFieldConfiguration(r.Context(), workspaceID, user.ID,
				r.PostFormValue("name"), r.PostFormValue("description"))
		}
		if err != nil {
			redirectLocal(w, r, "/settings/field-configurations?error="+url.QueryEscape(fieldConfigMutationMessage(err)))
			return
		}
		redirectLocal(w, r, "/settings/field-configurations?notice="+url.QueryEscape(notice))
		return
	}
	data, err := h.loadFieldConfigurationsPage(r, workspaceID)
	if err != nil {
		http.Error(w, "Could not load field configurations.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_field_configurations", user, workspaceID, data, "field-configurations", "")
}

func (h *Handler) FieldConfigurationMutation(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	notice := "Field configuration updated."
	var err error
	switch r.PostFormValue("action") {
	case "update":
		name, description := r.PostFormValue("name"), r.PostFormValue("description")
		err = h.Store.UpdateFieldConfiguration(r.Context(), workspaceID, user.ID, id, &name, &description)
	case "delete":
		err = h.Store.DeleteFieldConfiguration(r.Context(), workspaceID, user.ID, id)
		notice = "Field configuration deleted."
	case "set-field":
		behaviour := r.PostFormValue("behaviour")
		err = h.Store.SetFieldConfigurationItems(r.Context(), workspaceID, user.ID, id,
			[]models.FieldConfigurationItem{{
				FieldID:     r.PostFormValue("fieldId"),
				IsRequired:  behaviour == "required",
				IsHidden:    behaviour == "hidden",
				Description: r.PostFormValue("description"),
			}})
		notice = "Field rule saved."
	case "update-scheme":
		name, description := r.PostFormValue("name"), r.PostFormValue("description")
		err = h.Store.UpdateFieldConfigurationScheme(r.Context(), workspaceID, user.ID, id, &name, &description)
		notice = "Field configuration scheme updated."
	case "delete-scheme":
		err = h.Store.DeleteFieldConfigurationScheme(r.Context(), workspaceID, user.ID, id)
		notice = "Field configuration scheme deleted."
	case "map-issue-type":
		err = h.Store.SetFieldConfigurationSchemeMappings(r.Context(), workspaceID, user.ID, id,
			[]models.FieldConfigurationSchemeItem{{
				IssueTypeID: r.PostFormValue("issueTypeId"), FieldConfigurationID: r.PostFormValue("configurationId")}})
		notice = "Work type mapping saved."
	case "remove-issue-type":
		err = h.Store.RemoveFieldConfigurationSchemeMappings(r.Context(), workspaceID, user.ID, id,
			[]string{r.PostFormValue("issueTypeId")})
		notice = "Work type mapping removed."
	case "assign-project":
		err = h.Store.AssignFieldConfigurationScheme(r.Context(), workspaceID, user.ID, r.PostFormValue("project"), id)
		notice = "Project assigned to the field configuration scheme."
	default:
		err = store.ErrFieldConfigValidation
	}
	target := "/settings/field-configurations"
	if err != nil {
		redirectLocal(w, r, target+"?error="+url.QueryEscape(fieldConfigMutationMessage(err)))
		return
	}
	redirectLocal(w, r, target+"?notice="+url.QueryEscape(notice))
}
