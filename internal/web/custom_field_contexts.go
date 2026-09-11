package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

type fieldContextView struct {
	Context  *models.CustomFieldContext
	Projects []*models.Project
	Types    []string
	Options  []models.CustomFieldOption
	IsSelect bool
}

type customFieldCard struct {
	Field    *models.CustomField
	Contexts []fieldContextView
}

type customFieldsData struct {
	Fields     []customFieldCard
	Projects   []*models.Project
	IssueTypes []models.IssueType
	Notice     string
	Error      string
}

func fieldContextMutationMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrFieldContextValidation), errors.Is(err, store.ErrFieldContextConflict),
		errors.Is(err, store.ErrFieldContextNotFound), errors.Is(err, store.ErrProjectPermission):
		return err.Error()
	default:
		return "Could not update custom field contexts."
	}
}

func (h *Handler) loadCustomFieldsPage(r *http.Request, workspaceID string) (customFieldsData, error) {
	data := customFieldsData{Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	var err error
	if data.Projects, err = h.Store.ProjectsByWorkspace(r.Context(), workspaceID); err != nil {
		return data, err
	}
	if data.IssueTypes, err = h.Store.IssueTypes(r.Context()); err != nil {
		return data, err
	}
	projectsByID := map[string]*models.Project{}
	for _, project := range data.Projects {
		projectsByID[project.ID] = project
	}
	typeNames := map[string]string{}
	for _, issueType := range data.IssueTypes {
		typeNames[issueType.ID] = issueType.Name
	}
	fields, err := h.Store.CustomFieldsForWorkspace(r.Context(), workspaceID)
	if err != nil {
		return data, err
	}
	for _, field := range fields {
		contexts, contextErr := h.Store.CustomFieldContexts(r.Context(), workspaceID, field.ID, nil)
		if contextErr != nil {
			return data, contextErr
		}
		card := customFieldCard{Field: field}
		for _, found := range contexts {
			view := fieldContextView{Context: found}
			for _, projectID := range found.ProjectIDs {
				if project, ok := projectsByID[projectID]; ok {
					view.Projects = append(view.Projects, project)
				}
			}
			for _, issueTypeID := range found.IssueTypeIDs {
				view.Types = append(view.Types, typeNames[issueTypeID])
			}
			if field.Type == models.CustomFieldSelect {
				view.IsSelect = true
				options, optionErr := h.Store.CustomFieldOptions(r.Context(), workspaceID, field.ID, found.ID)
				if optionErr != nil {
					return data, optionErr
				}
				view.Options = options
			}
			card.Contexts = append(card.Contexts, view)
		}
		data.Fields = append(data.Fields, card)
	}
	return data, nil
}

func (h *Handler) CustomFieldsPage(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	data, err := h.loadCustomFieldsPage(r, workspaceID)
	if err != nil {
		http.Error(w, "Could not load custom fields.", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_custom_fields", user, workspaceID, data, "custom-fields", "")
}

func (h *Handler) CustomFieldContextMutation(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	fieldID := r.PathValue("id")
	if fieldID == "" {
		http.NotFound(w, r)
		return
	}
	contextID := r.PostFormValue("contextId")
	notice := "Custom field context updated."
	var err error
	switch r.PostFormValue("action") {
	case "create":
		projects, types := formValues(r, "project"), formValues(r, "issueType")
		_, err = h.Store.CreateCustomFieldContext(r.Context(), workspaceID, user.ID, fieldID,
			r.PostFormValue("name"), r.PostFormValue("description"), projects, types)
		notice = "Custom field context created."
	case "delete":
		err = h.Store.DeleteCustomFieldContext(r.Context(), workspaceID, user.ID, fieldID, contextID)
		notice = "Custom field context deleted."
	case "add-project":
		err = h.Store.ChangeCustomFieldContextScope(r.Context(), workspaceID, user.ID, fieldID, contextID,
			[]string{r.PostFormValue("project")}, nil, false)
		notice = "Project added to the context."
	case "remove-project":
		err = h.Store.ChangeCustomFieldContextScope(r.Context(), workspaceID, user.ID, fieldID, contextID,
			[]string{r.PostFormValue("project")}, nil, true)
		notice = "Project removed from the context."
	case "add-issue-type":
		err = h.Store.ChangeCustomFieldContextScope(r.Context(), workspaceID, user.ID, fieldID, contextID,
			nil, []string{r.PostFormValue("issueType")}, false)
		notice = "Work type added to the context."
	case "remove-issue-type":
		err = h.Store.ChangeCustomFieldContextScope(r.Context(), workspaceID, user.ID, fieldID, contextID,
			nil, []string{r.PostFormValue("issueType")}, true)
		notice = "Work type removed from the context."
	case "set-default":
		// The form takes plain text; the store keeps the Jira wire value, which
		// is JSON.
		value := strings.TrimSpace(r.PostFormValue("defaultValue"))
		if value != "" {
			encoded, encodeErr := json.Marshal(value)
			if encodeErr != nil {
				redirectLocal(w, r, "/settings/custom-fields?error="+url.QueryEscape("Could not encode the default value."))
				return
			}
			value = string(encoded)
		}
		err = h.Store.SetCustomFieldContextDefault(r.Context(), workspaceID, user.ID, fieldID, contextID, value)
		notice = "Default value saved."
	case "add-option":
		_, err = h.Store.CreateCustomFieldOptions(r.Context(), workspaceID, user.ID, fieldID, contextID,
			[]string{r.PostFormValue("value")})
		notice = "Option added."
	case "disable-option":
		err = h.Store.UpdateCustomFieldOptions(r.Context(), workspaceID, user.ID, fieldID, contextID,
			[]models.CustomFieldOption{{
				ID: r.PostFormValue("optionId"), Value: r.PostFormValue("value"),
				Disabled: r.PostFormValue("disabled") == "true"}})
		notice = "Option updated."
	case "move-option":
		err = h.Store.ReorderCustomFieldOptions(r.Context(), workspaceID, user.ID, fieldID, contextID,
			[]string{r.PostFormValue("optionId")}, "", "First")
		notice = "Option moved."
	case "delete-option":
		err = h.Store.DeleteCustomFieldOption(r.Context(), workspaceID, user.ID, fieldID, contextID,
			r.PostFormValue("optionId"), r.PostFormValue("replaceWith"))
		notice = "Option removed."
	default:
		err = store.ErrFieldContextValidation
	}
	target := "/settings/custom-fields"
	if err != nil {
		redirectLocal(w, r, target+"?error="+url.QueryEscape(fieldContextMutationMessage(err)))
		return
	}
	redirectLocal(w, r, target+"?notice="+url.QueryEscape(notice))
}

func formValues(r *http.Request, name string) []string {
	values := []string{}
	for _, value := range r.PostForm[name] {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}
