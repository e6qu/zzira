package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
)

func (s *Store) ServiceRequestTypeFields(ctx context.Context, workspaceID, serviceDeskID, requestTypeID string) ([]models.ServiceRequestTypeField, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT f.field_id,f.request_type_id,
		  CASE f.field_id WHEN 'summary' THEN 'Summary' WHEN 'description' THEN 'Description' ELSE cf.name END,
		  CASE f.field_id WHEN 'summary' THEN 'text' WHEN 'description' THEN 'text' ELSE cf.type END,
		  CASE f.field_id WHEN 'summary' THEN rt.description WHEN 'description' THEN 'Describe the request.' ELSE COALESCE(cf.description,'') END,
		  f.help_text,f.required,(f.field_id LIKE 'customfield_%'),f.position,NOT f.visible,f.preset_value,COALESCE(f.condition_field_id,''),f.condition_option_ids
		FROM service_request_type_fields f
		JOIN service_request_types rt ON rt.id=f.request_type_id
		JOIN service_desks sd ON sd.id=rt.service_desk_id
		LEFT JOIN custom_fields cf ON cf.id=f.field_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND rt.id=$3
		  AND (f.field_id IN ('summary','description') OR (cf.id IS NOT NULL
		    AND jira_custom_field_context(cf.id,sd.project_id,NULL) IS NOT NULL))
		ORDER BY f.position,f.field_id`, workspaceID, serviceDeskID, requestTypeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := make([]models.ServiceRequestTypeField, 0)
	for rows.Next() {
		var field models.ServiceRequestTypeField
		if err := rows.Scan(&field.ID, &field.RequestTypeID, &field.Name, &field.Type, &field.Description, &field.HelpText, &field.Required, &field.Custom, &field.Position, &field.Hidden, &field.PresetValue, &field.ConditionFieldID, &field.ConditionOptionIDs); err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	return fields, rows.Err()
}

func (s *Store) SetServiceRequestTypeFields(ctx context.Context, workspaceID, actorID, serviceDeskID, requestTypeID string, fields []models.ServiceRequestTypeField) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM service_request_types rt JOIN service_desks sd ON sd.id=rt.service_desk_id WHERE sd.workspace_id=$1 AND sd.id=$2 AND rt.id=$3)`, workspaceID, serviceDeskID, requestTypeID).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("request type does not exist")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM service_request_type_fields WHERE request_type_id=$1`, requestTypeID); err != nil {
		return err
	}
	for position, field := range fields {
		var preset any
		if len(field.PresetValue) > 0 && string(field.PresetValue) != "null" {
			preset = field.PresetValue
		}
		conditionOptions := field.ConditionOptionIDs
		if conditionOptions == nil {
			conditionOptions = []string{}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO service_request_type_fields(request_type_id,field_id,required,help_text,position,visible,preset_value,condition_field_id,condition_option_ids) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9)`, requestTypeID, field.ID, field.Required, field.HelpText, position, !field.Hidden, preset, field.ConditionFieldID, conditionOptions); err != nil {
			return err
		}
	}
	detail, err := json.Marshal(map[string]any{"serviceDeskId": serviceDeskID, "fields": fields})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,'service.request_type.fields.updated','service_request_type',$3,$4::jsonb FROM sites WHERE workspace_id=$1`, workspaceID, actorID, requestTypeID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ServiceRequestFieldOption is an option a customer can choose for a request
// type field, with the child options of a cascading select.
type ServiceRequestFieldOption struct {
	ID, Value string
	Children  []ServiceRequestFieldOption
}

// ServiceRequestFieldOptions lists the enabled options of a custom field in
// the context that applies to a service desk's project, in their order.
func (s *Store) ServiceRequestFieldOptions(ctx context.Context, workspaceID, serviceDeskID, fieldID string) ([]ServiceRequestFieldOption, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT o.id::text,o.value,COALESCE(o.parent_id::text,'')
		FROM service_desks sd
		JOIN custom_field_options o ON o.context_id=jira_custom_field_context($3,sd.project_id,NULL)
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND NOT o.disabled
		ORDER BY o.parent_id NULLS FIRST,o.position,o.id`, workspaceID, serviceDeskID, fieldID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	options := []ServiceRequestFieldOption{}
	index := map[string]int{}
	for rows.Next() {
		var option ServiceRequestFieldOption
		var parentID string
		if err := rows.Scan(&option.ID, &option.Value, &parentID); err != nil {
			return nil, err
		}
		if parentID == "" {
			index[option.ID] = len(options)
			options = append(options, option)
		} else if position, ok := index[parentID]; ok {
			options[position].Children = append(options[position].Children, option)
		}
	}
	return options, rows.Err()
}

// ServicePortalPickerChoices lists what a group, project, version or team
// picker on a service desk's portal offers, in the option shape select fields
// use: the site's groups, the projects the requester can browse, the desk
// project's versions that are not archived, and the site's teams. Other field
// types offer nothing.
func (s *Store) ServicePortalPickerChoices(ctx context.Context, workspaceID, serviceDeskID, userID, fieldType string) ([]ServiceRequestFieldOption, error) {
	choices := []ServiceRequestFieldOption{}
	switch fieldType {
	case models.CustomFieldGroup, models.CustomFieldMultiGroup:
		groups, err := s.GroupsByWorkspace(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		for _, group := range groups {
			choices = append(choices, ServiceRequestFieldOption{ID: group.ID, Value: group.Name})
		}
	case models.CustomFieldProject:
		projects, err := s.ProjectsWithPermissions(ctx, workspaceID, userID, []string{"BROWSE_PROJECTS"})
		if err != nil {
			return nil, err
		}
		for _, project := range projects {
			choices = append(choices, ServiceRequestFieldOption{ID: project.ID, Value: project.Name})
		}
	case models.CustomFieldTeam:
		teams, err := s.AtlassianTeams(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		for _, team := range teams {
			choices = append(choices, ServiceRequestFieldOption{ID: team.ID, Value: team.Name})
		}
	case models.CustomFieldVersion, models.CustomFieldMultiVersion:
		var projectID string
		if err := s.Pool.QueryRow(ctx, `SELECT project_id FROM service_desks WHERE workspace_id=$1 AND id=$2`, workspaceID, serviceDeskID).Scan(&projectID); err != nil {
			return nil, err
		}
		versions, err := s.ProjectVersions(ctx, projectID)
		if err != nil {
			return nil, err
		}
		for _, version := range versions {
			if !version.Archived {
				choices = append(choices, ServiceRequestFieldOption{ID: version.ID, Value: version.Name})
			}
		}
	}
	return choices, nil
}

// IsServicePortalPicker reports a field type whose portal choices come from
// the site's groups, projects, versions or teams rather than configured options.
func IsServicePortalPicker(fieldType string) bool {
	switch fieldType {
	case models.CustomFieldGroup, models.CustomFieldMultiGroup, models.CustomFieldProject, models.CustomFieldVersion, models.CustomFieldMultiVersion, models.CustomFieldTeam:
		return true
	}
	return false
}
