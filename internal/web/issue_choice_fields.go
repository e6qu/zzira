package web

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

func (h *Handler) issueChoiceFields(ctx context.Context, ws string, issue *models.Issue, rules map[string]models.FieldBehaviour, editFields map[string]bool, permissions map[string]bool) ([]models.IssueChoiceFieldView, error) {
	components, err := h.Store.Components(ctx, ws, issue.ProjectID, "", "name")
	if err != nil {
		return nil, err
	}
	versions, err := h.Store.ProjectVersions(ctx, issue.ProjectID)
	if err != nil {
		return nil, err
	}
	componentOptions := []models.CreateFieldOption{}
	for _, component := range components {
		componentOptions = append(componentOptions, models.CreateFieldOption{ID: component.ID, Name: component.Name})
	}
	versionOptions := []models.CreateFieldOption{}
	for _, version := range versions {
		versionOptions = append(versionOptions, models.CreateFieldOption{ID: version.ID, Name: version.Name})
	}
	out := []models.IssueChoiceFieldView{}
	for _, definition := range []struct{ id, name, kind string }{{"components", "Components", "components"}, {"fixVersions", "Fix versions", "versions"}, {"versions", "Affects versions", "versions"}} {
		if rules[definition.id].IsHidden {
			continue
		}
		options := versionOptions
		if definition.id == "components" {
			options = componentOptions
		}
		names := map[string]string{}
		for _, option := range options {
			names[option.ID] = option.Name
		}
		var refs []struct{ ID, Name string }
		if raw := issue.Fields[definition.id]; len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &refs); err != nil {
				return nil, err
			}
		}
		ids, display := []string{}, []string{}
		for _, ref := range refs {
			ids = append(ids, ref.ID)
			name := names[ref.ID]
			if name == "" {
				name = ref.Name
			}
			if name == "" {
				name = ref.ID
			}
			display = append(display, name)
		}
		out = append(out, models.IssueChoiceFieldView{
			Field: models.CreateFieldMeta{ID: definition.id, Name: definition.name, Type: definition.kind, Required: rules[definition.id].IsRequired, Description: rules[definition.id].Description, Options: options},
			Value: strings.Join(ids, ","), Display: strings.Join(display, ", "),
			ReadOnly: !editFields[definition.id] || (definition.id == "fixVersions" && !permissions["RESOLVE_ISSUES"]),
		})
	}
	return out, nil
}

// A hidden empty form value makes clearing a multiple picker explicit. An
// absent form key leaves the field unchanged, as off-screen controls must.
func issueChoiceRefs(values []string) json.RawMessage {
	refs := []map[string]string{}
	seen := map[string]bool{}
	for _, value := range values {
		id := strings.TrimSpace(value)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		refs = append(refs, map[string]string{"id": id})
	}
	encoded, _ := json.Marshal(refs)
	return encoded
}
