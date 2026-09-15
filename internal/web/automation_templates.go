package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/automation"
	"github.com/e6qu/zzira/internal/models"
)

// automationTemplateParameterView is a template parameter as a form field;
// Kind is text, number, member or status.
type automationTemplateParameterView struct {
	Key, Label, Kind string
	Required         bool
}

type automationTemplateView struct {
	automation.TemplateSummary
	Parameters []automationTemplateParameterView
}

type automationTemplatesData struct {
	Templates []automationTemplateView
	Projects  []*models.Project
	Members   []*models.User
	Statuses  []models.Status
	// Error, Selected and Name keep a refused form's template and rule name.
	Error, Selected, Name string
}

// automationTemplateParameterLabels name template parameters for people.
var automationTemplateParameterLabels = map[string]string{"label": "Label to add", "days": "Days without updates", "assigneeAccountId": "Assignee", "statusId": "Status"}

func automationTemplateParameterLabel(key string) string {
	if label := automationTemplateParameterLabels[key]; label != "" {
		return label
	}
	return key
}

func (h *Handler) automationTemplatesData(r *http.Request, workspaceID, userID string) (automationTemplatesData, error) {
	data := automationTemplatesData{}
	var err error
	if data.Members, err = h.Store.MembersByWorkspace(r.Context(), workspaceID); err != nil {
		return data, err
	}
	if data.Statuses, err = h.Store.StatusesForWorkspace(r.Context(), workspaceID); err != nil {
		return data, err
	}
	if data.Projects, err = h.Store.ProjectsWithPermissions(r.Context(), workspaceID, userID, []string{"BROWSE_PROJECTS"}); err != nil {
		return data, err
	}
	for _, template := range automation.Templates() {
		view := automationTemplateView{TemplateSummary: template}
		for _, parameter := range template.Parameters {
			field := automationTemplateParameterView{Key: parameter.Key, Label: automationTemplateParameterLabel(parameter.Key), Kind: "text", Required: parameter.Required}
			switch {
			case parameter.Key == "assigneeAccountId":
				field.Kind = "member"
			case parameter.Key == "statusId":
				field.Kind = "status"
			case parameter.Type == "NUMBER":
				field.Kind = "number"
			}
			view.Parameters = append(view.Parameters, field)
		}
		data.Templates = append(data.Templates, view)
	}
	return data, nil
}

// AutomationTemplates shows the rule template gallery.
func (h *Handler) AutomationTemplates(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok {
		return
	}
	data, err := h.automationTemplatesData(r, workspaceID, user.ID)
	if err != nil {
		http.Error(w, "could not load rule templates", http.StatusInternalServerError)
		return
	}
	h.writeWorkspacePage(w, r, "page_automation_templates", user, workspaceID, data, "automation", "")
}

// AutomationCreateFromTemplate creates a rule from a gallery template, checked
// as the Automation API checks it.
func (h *Handler) AutomationCreateFromTemplate(w http.ResponseWriter, r *http.Request) {
	user, workspaceID, ok := h.requireAdminPage(w, r)
	if !ok || !parseForm(w, r) {
		return
	}
	cloudID, err := h.Automation.WorkspaceCloudID(r.Context(), workspaceID)
	if err != nil {
		http.Error(w, "could not load cloud ID", http.StatusInternalServerError)
		return
	}
	templateID := r.PostFormValue("template_id")
	home := automation.SiteARI(cloudID)
	if project := strings.TrimSpace(r.PostFormValue("rule_home")); project != "" {
		home = automation.ProjectARI(cloudID, project)
	}
	parameters, problem := automationTemplateForm(r, templateID)
	var body json.RawMessage
	if problem == "" {
		built, templateErr := automation.BuildTemplateRule(cloudID, templateID, home, r.PostFormValue("state"), r.PostFormValue("name"), parameters)
		switch {
		case templateErr == nil:
			body = built
		case templateErr.Code == "automation.parameter.required":
			problem = automationTemplateParameterLabel(strings.TrimPrefix(templateErr.Field, "parameters.")) + " is required."
		default:
			problem = templateErr.Title + "."
		}
	}
	if problem == "" {
		uuid, err := h.Automation.CreateRule(r.Context(), workspaceID, user.ID, body)
		if err == nil {
			redirectAutomationRule(w, uuid)
			return
		}
		problem = err.Error()
		if strings.Contains(problem, "automation_rules_workspace_id_name_key") {
			problem = "A rule with this name already exists."
		}
	}
	data, err := h.automationTemplatesData(r, workspaceID, user.ID)
	if err != nil {
		http.Error(w, "could not load rule templates", http.StatusInternalServerError)
		return
	}
	data.Error, data.Selected, data.Name = problem, templateID, r.PostFormValue("name")
	h.writeWorkspacePageStatus(w, r, "page_automation_templates", user, workspaceID, data, "automation", "", http.StatusBadRequest)
}

// automationTemplateForm reads a template's parameters from the gallery form,
// typed as the template declares them. Blank fields are left out.
func automationTemplateForm(r *http.Request, templateID string) (map[string]automation.TemplateValue, string) {
	parameters := map[string]automation.TemplateValue{}
	for _, template := range automation.Templates() {
		if template.ID != templateID {
			continue
		}
		for _, parameter := range template.Parameters {
			raw := strings.TrimSpace(r.PostFormValue("param_" + parameter.Key))
			if raw == "" {
				continue
			}
			switch parameter.Type {
			case "NUMBER":
				number, err := strconv.ParseFloat(raw, 64)
				if err != nil {
					return nil, automationTemplateParameterLabel(parameter.Key) + " must be a number."
				}
				parameters[parameter.Key] = automation.TemplateValue{Type: "NUMBER", Value: number}
			case "BOOLEAN":
				parameters[parameter.Key] = automation.TemplateValue{Type: "BOOLEAN", Value: raw == "true"}
			default:
				parameters[parameter.Key] = automation.TemplateValue{Type: "TEXT", Value: raw}
			}
		}
	}
	return parameters, ""
}
