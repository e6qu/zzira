package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Options  []optionView
	IsSelect bool
	// Cascading is true for a cascading select, whose options may be children
	// of a first-level option.
	Cascading bool
	// Parents are the options a cascading child may be added under.
	Parents []models.CustomFieldOption
}

// optionView is one option with the name of the parent it belongs to, so a
// cascading select's second level reads as a pair rather than a bare value.
type optionView struct {
	models.CustomFieldOption
	ParentValue string
}

type customFieldCard struct {
	Field    *models.CustomField
	Contexts []fieldContextView
	// Translations are what this field is called in each language the site
	// has been given a name for.
	Translations []store.FieldTranslation
}

type customFieldsData struct {
	Fields     []customFieldCard
	Trashed    []*models.CustomField
	FieldTypes []customFieldTypeOption
	Projects   []*models.Project
	IssueTypes []models.IssueType
	Notice     string
	Error      string
}

// customFieldTypeOption is one custom field type an administrator can create.
type customFieldTypeOption struct {
	Type string
	Name string
}

// customFieldTypes are the field types the forms render, in the order Jira
// lists them.
func customFieldTypes() []customFieldTypeOption {
	names := []struct{ kind, label string }{
		{models.CustomFieldText, "Text"},
		{models.CustomFieldNumber, "Number"},
		{models.CustomFieldDate, "Date"},
		{models.CustomFieldDatetime, "Date and time"},
		{models.CustomFieldURL, "URL"},
		{models.CustomFieldLabels, "Labels"},
		{models.CustomFieldSelect, "Select list (single choice)"},
		{models.CustomFieldMultiSelect, "Select list (multiple choices)"},
		{models.CustomFieldCascadingSelect, "Select list (cascading)"},
		{models.CustomFieldUser, "Person"},
		{models.CustomFieldMultiUser, "People"},
		{models.CustomFieldGroup, "Group"},
		{models.CustomFieldMultiGroup, "Groups"},
		{models.CustomFieldProject, "Project"},
		{models.CustomFieldVersion, "Version"},
		{models.CustomFieldMultiVersion, "Versions"},
	}
	options := make([]customFieldTypeOption, 0, len(names))
	for _, entry := range names {
		options = append(options, customFieldTypeOption{Type: entry.kind, Name: entry.label})
	}
	return options
}

func fieldContextMutationMessage(err error) string {
	switch {
	case errors.Is(err, store.ErrFieldContextValidation), errors.Is(err, store.ErrFieldContextConflict),
		errors.Is(err, store.ErrFieldContextNotFound), errors.Is(err, store.ErrProjectPermission),
		errors.Is(err, store.ErrFieldTranslation):
		return err.Error()
	default:
		return "Could not update custom field contexts."
	}
}

func (h *Handler) loadCustomFieldsPage(r *http.Request, workspaceID string) (customFieldsData, error) {
	data := customFieldsData{FieldTypes: customFieldTypes(), Notice: r.URL.Query().Get("notice"), Error: r.URL.Query().Get("error")}
	trashed, err := h.Store.SearchCustomFields(r.Context(), workspaceID, store.FieldSearch{Trashed: true, MaxResults: 100})
	if err != nil {
		return data, err
	}
	data.Trashed = trashed.Fields
	if data.Projects, err = h.Store.ProjectsByWorkspace(r.Context(), workspaceID); err != nil {
		return data, err
	}
	if data.IssueTypes, err = h.Store.IssueTypes(r.Context(), workspaceID); err != nil {
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
		if card.Translations, err = h.Store.FieldTranslations(r.Context(), workspaceID, field.ID); err != nil {
			return data, err
		}
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
			// A cascading select's options are managed here too: it is a
			// select whose options may carry a parent.
			if field.Type == models.CustomFieldSelect || field.Type == models.CustomFieldMultiSelect ||
				field.Type == models.CustomFieldCascadingSelect {
				view.IsSelect = true
				options, optionErr := h.Store.CustomFieldOptions(r.Context(), workspaceID, field.ID, found.ID)
				if optionErr != nil {
					return data, optionErr
				}
				view.Cascading = field.Type == models.CustomFieldCascadingSelect
				values := make(map[string]string, len(options))
				for _, option := range options {
					values[option.ID] = option.Value
				}
				for _, option := range options {
					view.Options = append(view.Options, optionView{CustomFieldOption: option, ParentValue: values[option.ParentID]})
					if view.Cascading && option.ParentID == "" {
						view.Parents = append(view.Parents, option)
					}
				}
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
	case "translate":
		err = h.Store.SaveFieldTranslation(r.Context(), workspaceID, user.ID, fieldID, store.FieldTranslation{
			Locale: r.PostFormValue("locale"), Name: r.PostFormValue("translatedName"), Description: r.PostFormValue("translatedDescription"),
		})
		notice = "Field translation saved."
	case "remove-translation":
		err = h.Store.DeleteFieldTranslation(r.Context(), workspaceID, user.ID, fieldID, r.PostFormValue("locale"))
		notice = "Field translation removed."
	case "assets-multiple":
		err = h.Store.SetCustomFieldContextAssetsMultiple(r.Context(), workspaceID, user.ID, fieldID, contextID, r.PostFormValue("assetsMultiple") == "on")
		notice = "Assets objects saved."
	case "add-option":
		// A cascading select's second level is an option with a parent; every
		// other select has one level, and the form posts no parent.
		_, err = h.Store.CreateCustomFieldOptionsWithParents(r.Context(), workspaceID, user.ID, fieldID, contextID,
			[]models.CustomFieldOption{{Value: r.PostFormValue("value"), ParentID: r.PostFormValue("parentId")}})
		notice = "Option added."
	case "disable-option":
		err = h.Store.UpdateCustomFieldOptions(r.Context(), workspaceID, user.ID, fieldID, contextID,
			[]models.CustomFieldOption{{
				ID: r.PostFormValue("optionId"), Value: r.PostFormValue("value"),
				Disabled: r.PostFormValue("disabled") == "true"}})
		notice = "Option updated."
	case "move-option":
		// Jira's move takes an option to sit after, or First/Last. The page
		// offers the four moves an administrator actually makes, and works
		// out which option "up" and "down" land after.
		var after, position string
		after, position, err = h.optionMoveTarget(r.Context(), workspaceID, fieldID, contextID,
			r.PostFormValue("optionId"), r.PostFormValue("direction"))
		if err == nil {
			err = h.Store.ReorderCustomFieldOptions(r.Context(), workspaceID, user.ID, fieldID, contextID,
				[]string{r.PostFormValue("optionId")}, after, position)
		}
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

// optionMoveTarget turns a direction from the settings page into the move the
// store takes. Up and down are expressed as the option to sit after, because
// that is the only relative move Jira's API has: "up" is after the option two
// places above, and "down" is after the option below.
func (h *Handler) optionMoveTarget(ctx context.Context, workspaceID, fieldID, contextID, optionID, direction string) (after, position string, err error) {
	switch direction {
	case "first", "":
		return "", "First", nil
	case "last":
		return "", "Last", nil
	case "up", "down":
	default:
		return "", "", store.ErrFieldContextValidation
	}
	options, err := h.Store.CustomFieldOptions(ctx, workspaceID, fieldID, contextID)
	if err != nil {
		return "", "", err
	}
	// A cascading select orders each parent's children among themselves, so
	// only the option's own siblings decide where it can go.
	var parentID string
	index := -1
	for _, option := range options {
		if option.ID == optionID {
			parentID = option.ParentID
		}
	}
	siblings := make([]models.CustomFieldOption, 0, len(options))
	for _, option := range options {
		if option.ParentID == parentID {
			if option.ID == optionID {
				index = len(siblings)
			}
			siblings = append(siblings, option)
		}
	}
	if index < 0 {
		return "", "", store.ErrFieldContextNotFound
	}
	if direction == "up" {
		switch index {
		case 0:
			return "", "First", nil
		case 1:
			return "", "First", nil
		default:
			return siblings[index-2].ID, "", nil
		}
	}
	if index >= len(siblings)-1 {
		return "", "Last", nil
	}
	return siblings[index+1].ID, "", nil
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

// CustomFieldMutation creates a custom field and moves one through its
// lifecycle: rename, trash, restore, delete.
func (h *Handler) CustomFieldMutation(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	if !parseForm(w, r) {
		return
	}
	value := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	fieldID := value("field")
	notice, err := "", error(nil)
	switch r.PostFormValue("action") {
	case "create":
		name, kind := value("name"), value("type")
		if name == "" {
			err = fmt.Errorf("a field name is required")
			break
		}
		if _, known := models.CustomFieldTypeKeys[kind]; !known {
			err = fmt.Errorf("choose a field type")
			break
		}
		var seq int
		if seq, err = h.Store.NextCustomFieldNumber(r.Context()); err != nil {
			break
		}
		id := fmt.Sprintf("customfield_%d", seq)
		if _, err = h.Store.CreateWorkspaceCustomFieldOfKind(r.Context(), workspaceID, id, name,
			kind, models.CustomFieldTypeKeys[kind], r.PostFormValue("description")); err == nil {
			notice = name + " created."
		}
	case "update":
		name, description := value("name"), r.PostFormValue("description")
		if _, err = h.Store.UpdateCustomField(r.Context(), workspaceID, fieldID, &name, &description, nil); err == nil {
			notice = name + " saved."
		}
	case "trash":
		if err = h.Store.SetCustomFieldTrashed(r.Context(), workspaceID, fieldID, true); err == nil {
			notice = "Field moved to the trash; it no longer appears on forms."
		}
	case "restore":
		if err = h.Store.SetCustomFieldTrashed(r.Context(), workspaceID, fieldID, false); err == nil {
			notice = "Field restored."
		}
	case "delete":
		if err = h.Store.DeleteCustomField(r.Context(), workspaceID, fieldID); err == nil {
			notice = "Field deleted with its values."
		}
	default:
		http.Error(w, "Unknown custom field action.", http.StatusBadRequest)
		return
	}
	if err != nil {
		redirectLocal(w, r, "/settings/custom-fields?error="+url.QueryEscape(err.Error()))
		return
	}
	redirectLocal(w, r, "/settings/custom-fields?notice="+url.QueryEscape(notice))
}
