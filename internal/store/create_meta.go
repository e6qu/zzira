package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// IssueTypes lists the site's issue types.
func (s *Store) IssueTypes(ctx context.Context, workspaceID string) ([]models.IssueType, error) {
	return s.IssueTypesForWorkspace(ctx, workspaceID)
}

// ProjectByIDOrKey resolves a project without allowing it to escape the
// caller's workspace.
func (s *Store) ProjectByIDOrKey(ctx context.Context, workspaceID, idOrKey string) (*models.Project, error) {
	return scanProject(s.Pool.QueryRow(ctx, `SELECT `+projectSelectColumns+` FROM projects WHERE workspace_id=$1 AND lifecycle_state='ACTIVE' AND (id=$2 OR upper(key)=upper($2))`, workspaceID, idOrKey))
}

// IssueCreateMetadata builds the sole schema used by create UI and API
// metadata responses. Keeping this at the store boundary prevents either edge
// from inventing a field the command path cannot persist.
func (s *Store) IssueCreateMetadata(ctx context.Context, workspaceID, userID string) (*models.IssueCreateMetadata, error) {
	projects, err := s.ProjectsByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	issueTypes, err := s.IssueTypes(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	createScreenTabs, err := s.ResolveScreenTabsByProject(ctx, workspaceID, "create")
	if err != nil {
		return nil, err
	}
	createScreenFields, err := s.ResolveScreenFieldsByProject(ctx, workspaceID, "create")
	if err != nil {
		return nil, err
	}
	fieldBehaviour, err := s.ResolveFieldBehaviourByProject(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	customFieldContexts, err := s.CustomFieldContextsByProject(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	priorities, err := s.Priorities(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	members, err := s.MembersByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	admin, err := s.IsAdmin(ctx, workspaceID, userID)
	if err != nil {
		return nil, err
	}
	siteConfiguration, err := s.JiraSiteConfiguration(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	projectOptions := make([]models.CreateFieldOption, 0, len(projects))
	for _, project := range projects {
		projectOptions = append(projectOptions, models.CreateFieldOption{ID: project.ID, Key: project.Key, Name: project.Name})
	}
	priorityOptions := make([]models.CreateFieldOption, 0, len(priorities))
	for _, priority := range priorities {
		priorityOptions = append(priorityOptions, models.CreateFieldOption{ID: priority.ID, Name: priority.Name})
	}
	// A project offers the priorities of the priority scheme assigned to it, or
	// of the site's default scheme when it has none of its own.
	prioritySchemes, err := s.PrioritySchemes(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	schemeByProject, defaultScheme := map[string]PriorityScheme{}, PriorityScheme{}
	for _, scheme := range prioritySchemes {
		if scheme.IsDefault {
			defaultScheme = scheme
		}
		for _, projectID := range scheme.ProjectIDs {
			schemeByProject[projectID] = scheme
		}
	}
	memberOptions := make([]models.CreateFieldOption, 0, len(members))
	for _, member := range members {
		memberOptions = append(memberOptions, models.CreateFieldOption{ID: member.ID, Name: member.DisplayName})
	}
	groupOptions := []models.CreateFieldOption{}
	if groups, groupErr := s.SiteGroups(ctx, workspaceID); groupErr == nil {
		for _, group := range groups {
			groupOptions = append(groupOptions, models.CreateFieldOption{ID: group.ID, Name: group.Name})
		}
	}

	meta := &models.IssueCreateMetadata{Projects: make([]models.CreateProjectMeta, 0, len(projects))}
	for _, project := range projects {
		parentOptions := []models.CreateFieldOption{}
		parentRows, err := s.Pool.Query(ctx, `
			SELECT i.jira_id::text, i.key, i.summary, COALESCE(o.hierarchy_level,t.hierarchy_level)
			FROM issues i JOIN issue_types t ON t.id=i.issuetype_id
			LEFT JOIN issue_metadata_overrides o ON o.workspace_id=$2 AND o.entity_type='issuetype' AND o.entity_id=t.id
			WHERE i.project_id=$1 AND NOT t.subtask
			ORDER BY i.updated_seq DESC, i.key LIMIT 200`, project.ID, workspaceID)
		if err != nil {
			return nil, err
		}
		for parentRows.Next() {
			var option models.CreateFieldOption
			if err := parentRows.Scan(&option.ID, &option.Key, &option.Name, &option.HierarchyLevel); err != nil {
				parentRows.Close()
				return nil, err
			}
			option.Name = option.Key + " — " + option.Name
			parentOptions = append(parentOptions, option)
		}
		if err := parentRows.Err(); err != nil {
			parentRows.Close()
			return nil, err
		}
		parentRows.Close()
		// A project offers the issue types of its issue type scheme, with the
		// scheme's default type first, which is the type Jira's create form
		// starts on.
		projectTypes, err := s.createMetaProjectIssueTypes(ctx, workspaceID, project.ID, issueTypes)
		if err != nil {
			return nil, err
		}
		projectTypeOptions := make([]models.CreateFieldOption, 0, len(projectTypes))
		for _, issueType := range projectTypes {
			projectTypeOptions = append(projectTypeOptions, models.CreateFieldOption{ID: issueType.ID, Name: issueType.Name})
		}
		priorityScheme, ok := schemeByProject[project.ID]
		if !ok {
			priorityScheme = defaultScheme
		}
		projectPriorityOptions := priorityOptions
		if len(priorityScheme.PriorityIDs) > 0 {
			projectPriorityOptions = make([]models.CreateFieldOption, 0, len(priorityScheme.PriorityIDs))
			for _, option := range priorityOptions {
				if slices.Contains(priorityScheme.PriorityIDs, option.ID) {
					projectPriorityOptions = append(projectPriorityOptions, option)
				}
			}
		}
		fields := []models.CreateFieldMeta{
			{ID: "project", Name: "Project", Type: "project", Required: true, Section: "context", Options: projectOptions},
			{ID: "issuetype", Name: "Issue type", Type: "issuetype", Required: true, Section: "context", Options: projectTypeOptions},
			{ID: "summary", Name: "Summary", Type: "string", Required: true, Section: "primary"},
			{ID: "description", Name: "Description", Type: "doc", Section: "primary"},
			{ID: "assignee", Name: "Assignee", Type: "user", Section: "details", Options: memberOptions},
			{ID: "priority", Name: "Priority", Type: "priority", Section: "details", Options: projectPriorityOptions},
			{ID: "labels", Name: "Labels", Type: "array", Description: "Separate labels with commas. Spaces are not allowed inside a label.", Section: "details"},
			{ID: "duedate", Name: "Due date", Type: "date", Section: "details"},
			{ID: "parent", Name: "Parent", Type: "parent", Description: "Required for sub-tasks. Choose a work item one level above this work type.", Section: "details", Options: parentOptions},
		}
		if siteConfiguration.TimeTrackingEnabled {
			// The original estimate a work item starts with; the remaining
			// estimate starts from it.
			fields = append(fields, models.CreateFieldMeta{ID: "timetracking", Name: "Time tracking", Type: "timetracking",
				Description: "Original estimate, such as 2w 4d 6h 45m.", Section: "details"})
		}

		versions, err := s.ProjectVersions(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		versionOptions := []models.CreateFieldOption{}
		for _, version := range versions {
			if !version.Archived {
				versionOptions = append(versionOptions, models.CreateFieldOption{ID: version.ID, Name: version.Name})
			}
		}
		fields = append(fields,
			models.CreateFieldMeta{ID: "fixVersions", Name: "Fix versions", Type: "versions", Section: "details", Options: versionOptions},
			models.CreateFieldMeta{ID: "versions", Name: "Affects versions", Type: "versions", Section: "details", Options: versionOptions})
		components, err := s.Components(ctx, workspaceID, project.ID, "", "name")
		if err != nil {
			return nil, err
		}
		componentOptions := make([]models.CreateFieldOption, 0, len(components))
		for _, component := range components {
			componentOptions = append(componentOptions, models.CreateFieldOption{ID: component.ID, Name: component.Name})
		}
		fields = append(fields, models.CreateFieldMeta{ID: "components", Name: "Components", Type: "components", Section: "details", Options: componentOptions})

		scheme, err := s.SecuritySchemeForProject(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		if scheme != nil {
			options := make([]models.CreateFieldOption, 0, len(scheme.Levels))
			for _, level := range scheme.Levels {
				allowed, visibilityErr := s.CanUseIssueSecurityLevel(ctx, workspaceID, project.ID, "", userID, level.ID)
				if visibilityErr != nil {
					return nil, visibilityErr
				}
				if admin || allowed {
					options = append(options, models.CreateFieldOption{ID: level.ID, Name: level.Name})
				}
			}
			if len(options) > 0 {
				fields = append(fields, models.CreateFieldMeta{ID: "security", Name: "Restrict to", Type: "securitylevel", Section: "details", Options: options})
			}
		}

		customFields, err := s.CustomFieldsForProject(ctx, project.ID)
		if err != nil {
			return nil, err
		}
		var teamOptions []models.CreateFieldOption
		for _, field := range customFields {
			fieldType, err := createFieldType(field.Type)
			if err != nil {
				return nil, fmt.Errorf("custom field %q: %w", field.ID, err)
			}
			typeKey := field.TypeKey
			if typeKey == "" {
				typeKey = models.CustomFieldTypeKeys[field.Type]
			}
			if field.AppKey != "" {
				typeKey = field.AppKey + "__" + field.AppModuleKey
			}
			fieldMeta := models.CreateFieldMeta{
				ID: field.ID, Name: field.Name, Type: fieldType, Description: field.Description,
				Custom: true, Section: "details", TypeKey: typeKey,
			}
			switch field.Type {
			case models.CustomFieldUser, models.CustomFieldMultiUser:
				fieldMeta.Options = memberOptions
			case models.CustomFieldGroup, models.CustomFieldMultiGroup:
				fieldMeta.Options = groupOptions
			case models.CustomFieldProject:
				fieldMeta.Options = projectOptions
			case models.CustomFieldVersion, models.CustomFieldMultiVersion:
				fieldMeta.Options = versionOptions
			case models.CustomFieldTeam:
				if teamOptions == nil {
					teams, err := s.AtlassianTeams(ctx, workspaceID)
					if err != nil {
						return nil, err
					}
					teamOptions = []models.CreateFieldOption{}
					for _, team := range teams {
						teamOptions = append(teamOptions, models.CreateFieldOption{ID: team.ID, Name: team.Name})
					}
				}
				fieldMeta.Options = teamOptions
			}
			fields = append(fields, fieldMeta)
		}
		// The project's screen scheme decides which of these fields each work
		// type's create form actually shows.
		meta.Projects = append(meta.Projects, models.CreateProjectMeta{
			Project: *project, IssueTypes: projectTypes, Fields: fields,
			ScreenFields: createScreenFields[project.ID], ScreenTabs: createScreenTabs[project.ID], FieldBehaviour: fieldBehaviour[project.ID],
			DefaultPriorityID: priorityScheme.DefaultPriorityID, CustomFieldContexts: customFieldContexts[project.ID]})
	}
	// Every person reads a field by the name their language gives it.
	if err := s.translateCreateMetadata(ctx, workspaceID, userID, meta); err != nil {
		return nil, err
	}
	return meta, nil
}

func createFieldType(fieldType string) (string, error) {
	switch strings.ToLower(fieldType) {
	case models.CustomFieldText:
		return "string", nil
	case models.CustomFieldNumber:
		return "number", nil
	case models.CustomFieldDatetime:
		return "datetime", nil
	case models.CustomFieldSelect:
		return "option", nil
	case models.CustomFieldMultiSelect:
		return "options", nil
	case models.CustomFieldCascadingSelect:
		return "option-with-child", nil
	case models.CustomFieldDate:
		return "date", nil
	case models.CustomFieldURL:
		return "url", nil
	case models.CustomFieldUser:
		return "user", nil
	case models.CustomFieldMultiUser:
		return "users", nil
	case models.CustomFieldGroup:
		return "group", nil
	case models.CustomFieldMultiGroup:
		return "groups", nil
	case models.CustomFieldLabels:
		return "array", nil
	case models.CustomFieldProject:
		return "projectpicker", nil
	case models.CustomFieldVersion:
		return "version", nil
	case models.CustomFieldMultiVersion:
		return "versions", nil
	case models.CustomFieldTeam:
		return "team", nil
	case models.CustomFieldAsset:
		// Jira reports CMDB object fields by their custom key, not a schema type.
		return "any", nil
	default:
		return "", fmt.Errorf("unsupported type %q", fieldType)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// createMetaProjectIssueTypes lists a project's issue types in its scheme's
// order with the scheme's default type first. A site without schemes yet offers
// all its types.
func (s *Store) createMetaProjectIssueTypes(ctx context.Context, workspaceID, projectID string, all []models.IssueType) ([]models.IssueType, error) {
	scheme, err := s.ProjectIssueTypeScheme(ctx, workspaceID, projectID)
	if err != nil {
		if errors.Is(err, ErrIssueMetadataNotFound) {
			return all, nil
		}
		return nil, err
	}
	byID := make(map[string]models.IssueType, len(all))
	for _, issueType := range all {
		byID[issueType.ID] = issueType
	}
	ordered := make([]models.IssueType, 0, len(scheme.IssueTypeIDs))
	if t, ok := byID[scheme.DefaultIssueTypeID]; ok {
		ordered = append(ordered, t)
	}
	for _, id := range scheme.IssueTypeIDs {
		if t, ok := byID[id]; ok && id != scheme.DefaultIssueTypeID {
			ordered = append(ordered, t)
		}
	}
	return ordered, nil
}

// translateCreateMetadata renames the custom fields of a create form into the
// caller's language, leaving the ones nobody translated as the site names
// them.
func (s *Store) translateCreateMetadata(ctx context.Context, workspaceID, userID string, meta *models.IssueCreateMetadata) error {
	names, err := s.FieldNamesInLocale(ctx, workspaceID, s.LocaleForUser(ctx, workspaceID, userID))
	if err != nil || len(names) == 0 {
		return err
	}
	for projectIndex := range meta.Projects {
		fields := meta.Projects[projectIndex].Fields
		for fieldIndex := range fields {
			translation, ok := names[fields[fieldIndex].ID]
			if !ok {
				continue
			}
			fields[fieldIndex].Name = translation.Name
			if translation.Description != "" {
				fields[fieldIndex].Description = translation.Description
			}
		}
	}
	return nil
}
