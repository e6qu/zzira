package api3

import (
	"context"
	"encoding/json"
	"html"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/models"
)

type searchOptions struct {
	Fields       []string
	Expand       []string
	Properties   []string
	FieldsByKeys bool
	FailFast     bool
	Validate     string
}

type searchFieldDefinition struct {
	ID, Key, Name string
	Schema        map[string]any
}

func searchFieldDefinitions(customFields []*models.CustomField) []searchFieldDefinition {
	definitions := []searchFieldDefinition{
		{ID: "summary", Key: "summary", Name: "Summary", Schema: map[string]any{"type": "string", "system": "summary"}},
		{ID: "description", Key: "description", Name: "Description", Schema: map[string]any{"type": "doc", "system": "description"}},
		{ID: "labels", Key: "labels", Name: "Labels", Schema: map[string]any{"type": "array", "items": "string", "system": "labels"}},
		{ID: "fixVersions", Key: "fixVersions", Name: "Fix versions", Schema: map[string]any{"type": "array", "items": "version", "system": "fixVersions"}},
		{ID: "versions", Key: "versions", Name: "Affects versions", Schema: map[string]any{"type": "array", "items": "version", "system": "versions"}},
		{ID: "created", Key: "created", Name: "Created", Schema: map[string]any{"type": "datetime", "system": "created"}},
		{ID: "updated", Key: "updated", Name: "Updated", Schema: map[string]any{"type": "datetime", "system": "updated"}},
		{ID: "project", Key: "project", Name: "Project", Schema: map[string]any{"type": "project", "system": "project"}},
		{ID: "status", Key: "status", Name: "Status", Schema: map[string]any{"type": "status", "system": "status"}},
		{ID: "issuetype", Key: "issuetype", Name: "Issue Type", Schema: map[string]any{"type": "issuetype", "system": "issuetype"}},
		{ID: "parent", Key: "parent", Name: "Parent", Schema: map[string]any{"type": "issuelink", "system": "parent"}},
		{ID: "priority", Key: "priority", Name: "Priority", Schema: map[string]any{"type": "priority", "system": "priority"}},
		{ID: "assignee", Key: "assignee", Name: "Assignee", Schema: map[string]any{"type": "user", "system": "assignee"}},
		{ID: "reporter", Key: "reporter", Name: "Reporter", Schema: map[string]any{"type": "user", "system": "reporter"}},
		{ID: "security", Key: "security", Name: "Security Level", Schema: map[string]any{"type": "securitylevel", "system": "security"}},
	}
	for _, field := range customFields {
		key := field.ID
		custom := "com.zzira:" + field.Type
		if field.AppKey != "" {
			key = field.AppKey + "__" + field.AppModuleKey
			custom = key
		}
		fieldType := string(field.Type)
		if field.Type == models.CustomFieldText {
			fieldType = "string"
		}
		schema := map[string]any{"type": fieldType, "custom": custom}
		if number, err := strconv.ParseInt(strings.TrimPrefix(field.ID, "customfield_"), 10, 64); err == nil {
			schema["customId"] = number
		}
		definitions = append(definitions, searchFieldDefinition{ID: field.ID, Key: key, Name: field.Name, Schema: schema})
	}
	return definitions
}

func splitSearchValues(values []string) []string {
	result := []string{}
	for _, raw := range values {
		for _, value := range strings.Split(raw, ",") {
			if value = strings.TrimSpace(value); value != "" {
				result = append(result, value)
			}
		}
	}
	return result
}

func validateSearchOptions(options *searchOptions) *jerr {
	options.Fields = splitSearchValues(options.Fields)
	options.Expand = splitSearchValues(options.Expand)
	options.Properties = splitSearchValues(options.Properties)
	if options.Validate == "" {
		options.Validate = "strict"
	}
	switch options.Validate {
	case "strict", "warn", "none", "true", "false":
	default:
		return &jerr{status: 400, message: "validateQuery must be strict, warn, none, true, or false."}
	}
	allowedExpand := map[string]bool{
		"names": true, "schema": true, "renderedFields": true,
		"transitions": true, "operations": true, "editmeta": true,
		"changelog": true, "versionedRepresentations": true,
	}
	seenExpand := map[string]bool{}
	for _, value := range options.Expand {
		if !allowedExpand[value] {
			return &jerr{status: 400, message: "Unsupported search expansion: " + value}
		}
		seenExpand[value] = true
	}
	options.Expand = options.Expand[:0]
	for _, value := range []string{"renderedFields", "names", "schema", "transitions", "operations", "editmeta", "changelog", "versionedRepresentations"} {
		if seenExpand[value] {
			options.Expand = append(options.Expand, value)
		}
	}
	seenProperty := map[string]bool{}
	properties := options.Properties[:0]
	for _, value := range options.Properties {
		if !seenProperty[value] {
			seenProperty[value] = true
			properties = append(properties, value)
		}
	}
	options.Properties = properties
	if len(options.Properties) > 5 {
		return &jerr{status: 400, message: "A maximum of 5 issue property keys can be specified."}
	}
	return nil
}

func searchFieldOutput(definition searchFieldDefinition, fieldsByKeys bool) string {
	if fieldsByKeys {
		return definition.Key
	}
	return definition.ID
}

func normalizeSearchFields(requested []string, definitions []searchFieldDefinition, fieldsByKeys bool) []string {
	aliases := map[string]string{}
	for _, definition := range definitions {
		output := searchFieldOutput(definition, fieldsByKeys)
		aliases[strings.ToLower(definition.ID)] = output
		aliases[strings.ToLower(definition.Key)] = output
	}
	result := make([]string, 0, len(requested))
	for _, field := range requested {
		prefix := ""
		if strings.HasPrefix(field, "-") {
			prefix, field = "-", strings.TrimPrefix(field, "-")
		}
		if mapped := aliases[strings.ToLower(field)]; mapped != "" {
			field = mapped
		}
		result = append(result, prefix+field)
	}
	return result
}

func remapSearchIssueFields(bean map[string]any, definitions []searchFieldDefinition, fieldsByKeys bool) map[string]any {
	copyBean := map[string]any{}
	for key, value := range bean {
		copyBean[key] = value
	}
	fields, _ := bean["fields"].(map[string]any)
	remapped := make(map[string]any, len(fields))
	byID := map[string]searchFieldDefinition{}
	for _, definition := range definitions {
		byID[definition.ID] = definition
	}
	for key, value := range fields {
		if definition, ok := byID[key]; ok {
			key = searchFieldOutput(definition, fieldsByKeys)
		}
		remapped[key] = value
	}
	copyBean["fields"] = remapped
	return copyBean
}

func requestedFieldSet(requested []string, defaultAll bool) (bool, map[string]bool, map[string]bool) {
	all, positive := false, false
	include, exclude := map[string]bool{}, map[string]bool{}
	for _, field := range requested {
		if strings.HasPrefix(field, "-") {
			exclude[strings.TrimPrefix(field, "-")] = true
			continue
		}
		positive = true
		if field == "*all" || field == "*navigable" {
			all = true
		} else {
			include[field] = true
		}
	}
	if !positive && (defaultAll || len(requested) > 0) {
		all = true
	}
	return all, include, exclude
}

func projectSearchIssue(bean map[string]any, requested []string, defaultAll bool) map[string]any {
	if !defaultAll && (len(requested) == 0 || len(requested) == 1 && requested[0] == "id") {
		return map[string]any{"id": bean["id"]}
	}
	all, include, exclude := requestedFieldSet(requested, defaultAll)
	fields := map[string]any{}
	for field, value := range bean["fields"].(map[string]any) {
		if (all || include[field]) && !exclude[field] {
			fields[field] = value
		}
	}
	out := map[string]any{"id": bean["id"]}
	if defaultAll || all || include["key"] || len(fields) > 0 {
		out["key"], out["self"] = bean["key"], bean["self"]
	}
	if defaultAll || all || len(fields) > 0 {
		out["fields"] = fields
	}
	return out
}

func searchFieldMetadata(requested []string, defaultAll, fieldsByKeys bool, definitions []searchFieldDefinition) (map[string]string, map[string]any) {
	all, include, exclude := requestedFieldSet(requested, defaultAll)
	names, schemas := map[string]string{}, map[string]any{}
	for _, definition := range definitions {
		key := searchFieldOutput(definition, fieldsByKeys)
		if (all || include[key]) && !exclude[key] {
			names[key], schemas[key] = definition.Name, definition.Schema
		}
	}
	return names, schemas
}

func renderedSearchFields(fields map[string]any) map[string]any {
	result := map[string]any{}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := fields[key]
		switch key {
		case "description":
			if document, ok := value.(json.RawMessage); ok {
				result[key] = adf.ToHTML(document)
			} else {
				result[key] = nil
			}
		case "summary":
			if text, ok := value.(string); ok {
				result[key] = html.EscapeString(text)
			} else {
				result[key] = nil
			}
		case "created", "updated":
			result[key] = value
		default:
			result[key] = nil
		}
	}
	return result
}

func hasSearchExpand(options searchOptions, name string) bool {
	for _, value := range options.Expand {
		if value == name {
			return true
		}
	}
	return false
}

func (h *Handler) searchIssueBeans(ctx context.Context, workspaceID, userID string, issues []*models.Issue, options searchOptions, defaultAll bool, definitions []searchFieldDefinition) ([]map[string]any, error) {
	requested := normalizeSearchFields(options.Fields, definitions, options.FieldsByKeys)
	beans := make([]map[string]any, 0, len(issues))
	editMetadata := map[string]map[string]any{}
	for _, issue := range issues {
		bean := remapSearchIssueFields(h.issueBean(issue), definitions, options.FieldsByKeys)
		bean = projectSearchIssue(bean, requested, defaultAll)
		if len(options.Expand) > 0 && len(bean) > 1 {
			bean["expand"] = strings.Join(options.Expand, ",")
		}
		fields, _ := bean["fields"].(map[string]any)
		if hasSearchExpand(options, "renderedFields") {
			bean["renderedFields"] = renderedSearchFields(fields)
		}
		if hasSearchExpand(options, "transitions") {
			transitions, err := h.issueTransitionBeans(ctx, workspaceID, userID, issue)
			if err != nil {
				return nil, err
			}
			bean["transitions"] = transitions
		}
		if hasSearchExpand(options, "operations") {
			base := strings.TrimRight(h.BaseURL, "/") + "/browse/" + issue.Key
			bean["operations"] = map[string]any{"linkGroups": []any{map[string]any{
				"id": "opsbar-operations", "links": []any{
					map[string]any{"id": "edit-issue", "label": "Edit", "title": "Edit issue", "href": base + "?edit=true", "weight": 10},
					map[string]any{"id": "delete-issue", "label": "Delete", "title": "Delete issue", "href": base + "?delete=true", "weight": 20},
				},
			}}}
		}
		if hasSearchExpand(options, "editmeta") {
			cacheKey := issue.ProjectID + ":" + issue.IssueType.ID
			metadata := editMetadata[cacheKey]
			if metadata == nil {
				var err error
				metadata, err = h.issueEditMetadata(ctx, workspaceID, userID, issue)
				if err != nil {
					return nil, err
				}
				editMetadata[cacheKey] = metadata
			}
			bean["editmeta"] = metadata
		}
		if hasSearchExpand(options, "changelog") {
			page, err := h.issueChangelogPage(ctx, workspaceID, issue.ID, true)
			if err != nil {
				return nil, err
			}
			bean["changelog"] = page
		}
		if hasSearchExpand(options, "versionedRepresentations") {
			versions := map[string]any{}
			for field, value := range fields {
				versions[field] = map[string]any{"1": value}
			}
			bean["versionedRepresentations"] = versions
			delete(bean, "fields")
		}
		if len(options.Properties) > 0 {
			stored, err := h.Store.IssueProperties(ctx, issue.ID)
			if err != nil {
				return nil, err
			}
			properties := map[string]json.RawMessage{}
			for _, key := range options.Properties {
				if value, ok := stored[key]; ok {
					properties[key] = value
				}
			}
			bean["properties"] = properties
		}
		beans = append(beans, bean)
	}
	return beans, nil
}
