package api3

import (
	"context"
	"encoding/json"
	"net/url"

	"github.com/e6qu/zzira/internal/models"
)

// decodedValue reads a custom field value from an issue bean, which carries
// the stored JSON.
func decodedValue(value any) any {
	raw, ok := value.(json.RawMessage)
	if !ok {
		return value
	}
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return nil
	}
	return decoded
}

func stringList(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

// decorateCustomFieldValues describes option, cascading, user and group custom
// field values in issue beans as Jira does: option objects with self, value
// and id, a cascading option with its child, user beans and group beans.
func (h *Handler) decorateCustomFieldValues(ctx context.Context, workspaceID string, beans []map[string]any) error {
	definitions, err := h.Store.CustomFieldsForWorkspace(ctx, workspaceID)
	if err != nil {
		return err
	}
	types := map[string]string{}
	for _, definition := range definitions {
		switch definition.Type {
		case models.CustomFieldSelect, models.CustomFieldMultiSelect, models.CustomFieldCascadingSelect,
			models.CustomFieldUser, models.CustomFieldMultiUser, models.CustomFieldGroup, models.CustomFieldMultiGroup,
			models.CustomFieldProject, models.CustomFieldVersion, models.CustomFieldMultiVersion:
			types[definition.ID] = definition.Type
		}
	}
	if len(types) == 0 {
		return nil
	}
	var optionIDs, userIDs, groupIDs, projectIDs, versionIDs []string
	for _, bean := range beans {
		fields, _ := bean["fields"].(map[string]any)
		for fieldID, fieldType := range types {
			value, present := fields[fieldID]
			if !present {
				continue
			}
			decoded := decodedValue(value)
			switch fieldType {
			case models.CustomFieldSelect:
				if id, ok := decoded.(string); ok {
					optionIDs = append(optionIDs, id)
				}
			case models.CustomFieldMultiSelect:
				optionIDs = append(optionIDs, stringList(decoded)...)
			case models.CustomFieldCascadingSelect:
				if object, ok := decoded.(map[string]any); ok {
					for _, key := range []string{"parent", "child"} {
						if id, ok := object[key].(string); ok {
							optionIDs = append(optionIDs, id)
						}
					}
				}
			case models.CustomFieldUser:
				if id, ok := decoded.(string); ok {
					userIDs = append(userIDs, id)
				}
			case models.CustomFieldMultiUser:
				userIDs = append(userIDs, stringList(decoded)...)
			case models.CustomFieldGroup:
				if id, ok := decoded.(string); ok {
					groupIDs = append(groupIDs, id)
				}
			case models.CustomFieldMultiGroup:
				groupIDs = append(groupIDs, stringList(decoded)...)
			case models.CustomFieldProject:
				if id, ok := decoded.(string); ok {
					projectIDs = append(projectIDs, id)
				}
			case models.CustomFieldVersion:
				if id, ok := decoded.(string); ok {
					versionIDs = append(versionIDs, id)
				}
			case models.CustomFieldMultiVersion:
				versionIDs = append(versionIDs, stringList(decoded)...)
			}
		}
	}
	catalog, err := h.Store.LoadCustomFieldValueCatalog(ctx, workspaceID, optionIDs, userIDs, groupIDs)
	if err != nil {
		return err
	}
	projects := map[string]map[string]any{}
	for _, id := range projectIDs {
		if _, done := projects[id]; done {
			continue
		}
		if found, err := h.Store.ProjectByIDOrKey(ctx, workspaceID, id); err == nil {
			projects[id] = h.projectBean(found)
		} else {
			projects[id] = map[string]any{"id": id}
		}
	}
	versions := map[string]map[string]any{}
	for _, id := range versionIDs {
		if _, done := versions[id]; done {
			continue
		}
		if found, err := h.Store.Version(ctx, workspaceID, id); err == nil {
			versions[id] = h.versionBean(found)
		} else {
			versions[id] = map[string]any{"id": id}
		}
	}
	version := func(id string) map[string]any { return versions[id] }
	option := func(id string) map[string]any {
		bean := map[string]any{"self": h.BaseURL + "/rest/api/3/customFieldOption/" + id, "id": id}
		if found, ok := catalog.Options[id]; ok {
			bean["value"] = found.Value
		}
		return bean
	}
	user := func(id string) map[string]any {
		if found, ok := catalog.Users[id]; ok {
			return h.userBeanFor(ctx, found)
		}
		return map[string]any{"accountId": id}
	}
	group := func(id string) map[string]any {
		bean := map[string]any{"groupId": id, "self": h.BaseURL + "/rest/api/3/group?groupId=" + url.QueryEscape(id)}
		if found, ok := catalog.Groups[id]; ok {
			bean["name"] = found.Name
		}
		return bean
	}
	each := func(ids []string, describe func(string) map[string]any) []map[string]any {
		out := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			out = append(out, describe(id))
		}
		return out
	}
	for _, bean := range beans {
		fields, _ := bean["fields"].(map[string]any)
		for fieldID, fieldType := range types {
			value, present := fields[fieldID]
			if !present {
				continue
			}
			decoded := decodedValue(value)
			if decoded == nil {
				fields[fieldID] = nil
				continue
			}
			switch fieldType {
			case models.CustomFieldSelect:
				if id, ok := decoded.(string); ok {
					fields[fieldID] = option(id)
				}
			case models.CustomFieldMultiSelect:
				fields[fieldID] = each(stringList(decoded), option)
			case models.CustomFieldCascadingSelect:
				if object, ok := decoded.(map[string]any); ok {
					parent, _ := object["parent"].(string)
					described := option(parent)
					if child, ok := object["child"].(string); ok && child != "" {
						described["child"] = option(child)
					}
					fields[fieldID] = described
				}
			case models.CustomFieldUser:
				if id, ok := decoded.(string); ok {
					fields[fieldID] = user(id)
				}
			case models.CustomFieldMultiUser:
				fields[fieldID] = each(stringList(decoded), user)
			case models.CustomFieldGroup:
				if id, ok := decoded.(string); ok {
					fields[fieldID] = group(id)
				}
			case models.CustomFieldMultiGroup:
				fields[fieldID] = each(stringList(decoded), group)
			case models.CustomFieldProject:
				if id, ok := decoded.(string); ok {
					fields[fieldID] = projects[id]
				}
			case models.CustomFieldVersion:
				if id, ok := decoded.(string); ok {
					fields[fieldID] = version(id)
				}
			case models.CustomFieldMultiVersion:
				fields[fieldID] = each(stringList(decoded), version)
			}
		}
	}
	return nil
}
