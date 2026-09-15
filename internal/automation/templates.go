package automation

import (
	"encoding/json"
	"net/http"
	"strings"
)

// TemplateParameter is a value a rule template asks for.
type TemplateParameter struct {
	Type, Key string
	Required  bool
}

// TemplateSummary is a rule template as the browser gallery shows it, with
// its categories' display names.
type TemplateSummary struct {
	ID, Name, Description string
	Categories            []string
	Parameters            []TemplateParameter
}

// Templates lists the rule template catalog.
func Templates() []TemplateSummary {
	out := make([]TemplateSummary, 0, len(ruleTemplates))
	for _, template := range ruleTemplates {
		summary := TemplateSummary{ID: template.ID, Name: template.Name, Description: template.Description}
		for _, key := range template.Categories {
			summary.Categories = append(summary.Categories, templateCategoryNames[key])
		}
		for _, parameter := range template.Parameters {
			summary.Parameters = append(summary.Parameters, TemplateParameter{Type: parameter.Type, Key: parameter.Key, Required: parameter.Required})
		}
		out = append(out, summary)
	}
	return out
}

// SiteARI names the site as a rule's home.
func SiteARI(cloudID string) string { return siteARI(cloudID) }

// ProjectARI names a project as a rule's home.
func ProjectARI(cloudID, projectID string) string { return projectARI(cloudID, projectID) }

// TemplateValue is a parameter value given for a template, with its type.
type TemplateValue struct {
	Type  string
	Value any
}

// TemplateError is why a rule cannot be built from a template, in the
// Automation API's error terms.
type TemplateError struct {
	Status             int
	Code, Title, Field string
}

func (e *TemplateError) Error() string { return e.Title }

func invalidTemplate(code, title, field string) *TemplateError {
	return &TemplateError{Status: http.StatusBadRequest, Code: code, Title: title, Field: field}
}

// BuildTemplateRule builds the rule a template creates in a rule home: the
// site or one of its projects. Required, typed and known parameters are
// checked; a blank name uses the template's.
func BuildTemplateRule(cloudID, templateID, ruleHome, state, name string, parameters map[string]TemplateValue) (json.RawMessage, *TemplateError) {
	template, ok := templateByID(templateID)
	if !ok {
		return nil, invalidTemplate("automation.template.invalid", "The template does not exist", "templateId")
	}
	if !validRuleHome(cloudID, ruleHome) {
		return nil, invalidTemplate("automation.rule_home.invalid", "The rule home must be the site or one of its projects", "ruleHome")
	}
	if state == "" {
		state = "ENABLED"
	}
	if state != "ENABLED" && state != "DISABLED" {
		return nil, invalidTemplate("automation.state.invalid", "The state must be ENABLED or DISABLED", "state")
	}
	values := map[string]any{}
	for _, parameter := range template.Parameters {
		supplied, present := parameters[parameter.Key]
		if !present {
			if parameter.Required {
				return nil, invalidTemplate("automation.parameter.required", "The parameter "+parameter.Key+" is required", "parameters."+parameter.Key)
			}
			continue
		}
		valid := false
		switch parameter.Type {
		case "TEXT":
			text, isText := supplied.Value.(string)
			valid = isText && len([]rune(text)) <= 5000
		case "NUMBER":
			_, valid = supplied.Value.(float64)
		case "BOOLEAN":
			_, valid = supplied.Value.(bool)
		}
		if !valid || (supplied.Type != "" && supplied.Type != parameter.Type) {
			return nil, invalidTemplate("automation.parameter.invalid", "The parameter "+parameter.Key+" must be a "+parameter.Type+" value", "parameters."+parameter.Key)
		}
		values[parameter.Key] = supplied.Value
	}
	for key := range parameters {
		found := false
		for _, parameter := range template.Parameters {
			found = found || parameter.Key == key
		}
		if !found {
			return nil, invalidTemplate("automation.parameter.unknown", "The template does not accept the parameter "+key, "parameters."+key)
		}
	}
	trigger, components := template.Build(values)
	scope := []string{}
	if ruleHome != siteARI(cloudID) {
		scope = []string{ruleHome}
	}
	if name = strings.TrimSpace(name); name == "" {
		name = template.Name
	}
	body, err := json.Marshal(map[string]any{"rule": map[string]any{
		"name": name, "description": template.Description, "state": state,
		"trigger": trigger, "components": components, "ruleScopeARIs": scope,
	}})
	if err != nil {
		return nil, &TemplateError{Status: http.StatusInternalServerError, Code: "automation.template.failed", Title: "The rule could not be built"}
	}
	return body, nil
}
