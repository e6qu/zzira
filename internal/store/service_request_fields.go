package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/aql"
	"github.com/e6qu/zzira/internal/models"
)

func (s *Store) ServiceRequestTypeFields(ctx context.Context, workspaceID, serviceDeskID, requestTypeID string) ([]models.ServiceRequestTypeField, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT f.field_id,f.request_type_id,
		  CASE f.field_id WHEN 'summary' THEN 'Summary' WHEN 'description' THEN 'Description' ELSE cf.name END,
		  CASE f.field_id WHEN 'summary' THEN 'text' WHEN 'description' THEN 'text' ELSE cf.type END,
		  CASE f.field_id WHEN 'summary' THEN rt.description WHEN 'description' THEN 'Describe the request.' ELSE COALESCE(cf.description,'') END,
		  f.help_text,f.required,(f.field_id LIKE 'customfield_%'),f.position,NOT f.visible,f.preset_value,COALESCE(f.condition_field_id,''),f.condition_option_ids,COALESCE(f.asset_schema_id::text,''),f.asset_filter,COALESCE((SELECT cx.assets_multiple FROM custom_field_contexts cx WHERE cx.id=jira_custom_field_context(cf.id,sd.project_id,NULL)),FALSE)
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
		if err := rows.Scan(&field.ID, &field.RequestTypeID, &field.Name, &field.Type, &field.Description, &field.HelpText, &field.Required, &field.Custom, &field.Position, &field.Hidden, &field.PresetValue, &field.ConditionFieldID, &field.ConditionOptionIDs, &field.AssetSchemaID, &field.AssetFilter, &field.AssetsMultiple); err != nil {
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
		// A filter the site cannot read would leave the form offering
		// everything or nothing without saying why, so it is read here and
		// refused now rather than when somebody opens the portal.
		filter := strings.TrimSpace(field.AssetFilter)
		if filter != "" {
			if _, err := aql.Parse(filter); err != nil {
				return fmt.Errorf("%s: the Assets filter %q: %w", field.Name, filter, err)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO service_request_type_fields(request_type_id,field_id,required,help_text,position,visible,preset_value,condition_field_id,condition_option_ids,asset_schema_id,asset_filter) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9,NULLIF($10,'')::uuid,$11)`, requestTypeID, field.ID, field.Required, field.HelpText, position, !field.Hidden, preset, field.ConditionFieldID, conditionOptions, field.AssetSchemaID, filter); err != nil {
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
func (s *Store) ServicePortalPickerChoices(ctx context.Context, workspaceID, serviceDeskID, userID, fieldType, assetSchemaID string) ([]ServiceRequestFieldOption, error) {
	return s.ServicePortalPickerChoicesFiltered(ctx, workspaceID, serviceDeskID, userID, fieldType, assetSchemaID, "")
}

// ServicePortalPickerChoicesFiltered is the same, with an Assets object field
// narrowed by the AQL filter its form carries.
func (s *Store) ServicePortalPickerChoicesFiltered(ctx context.Context, workspaceID, serviceDeskID, userID, fieldType, assetSchemaID, assetFilter string) ([]ServiceRequestFieldOption, error) {
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
	case models.CustomFieldAsset:
		return s.ServicePortalFilteredAssetObjects(ctx, workspaceID, serviceDeskID, assetSchemaID, assetFilter)
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

// ServicePortalAssetObjects lists a service desk's Assets objects for a portal
// field, newest schemas' objects by label. Customers choose from them, so this
// read does not ask for agent access; the objects belong to the desk they are
// shown on.
// ServicePortalAssetObjects lists the Assets objects a portal field offers:
// those of the schema the field is scoped to, or the desk's whole inventory
// when it names no schema.
func (s *Store) ServicePortalAssetObjects(ctx context.Context, workspaceID, serviceDeskID, assetSchemaID string) ([]ServiceRequestFieldOption, error) {
	return s.servicePortalAssetObjects(ctx, workspaceID, serviceDeskID, assetSchemaID, "")
}

// ServicePortalFilteredAssetObjects is the same, narrowed by the field's own
// AQL filter: the laptops of one team rather than every laptop. A filter the
// site cannot read offers nothing rather than everything, because a form that
// quietly ignores its filter offers a customer objects they should not see.
func (s *Store) ServicePortalFilteredAssetObjects(ctx context.Context, workspaceID, serviceDeskID, assetSchemaID, filter string) ([]ServiceRequestFieldOption, error) {
	return s.servicePortalAssetObjects(ctx, workspaceID, serviceDeskID, assetSchemaID, filter)
}

func (s *Store) servicePortalAssetObjects(ctx context.Context, workspaceID, serviceDeskID, assetSchemaID, filter string) ([]ServiceRequestFieldOption, error) {
	where, args := "TRUE", []any{workspaceID, serviceDeskID, assetSchemaID}
	if strings.TrimSpace(filter) != "" {
		query, err := aql.Parse(filter)
		if err != nil {
			return nil, fmt.Errorf("the Assets filter %q: %w", filter, err)
		}
		compiled := query.Compile(aql.DefaultColumns(), len(args)+1)
		where, args = compiled.Where, append(args, compiled.Args...)
	}
	rows, err := s.Pool.Query(ctx, `SELECT o.id::text,o.label,s.name
		FROM service_asset_objects o JOIN service_asset_schemas s ON s.id=o.schema_id JOIN service_desks sd ON sd.id=s.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND ($3='' OR s.id::text=$3) AND (`+where+`)
		ORDER BY lower(s.name),lower(o.label),o.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	objects := []ServiceRequestFieldOption{}
	for rows.Next() {
		var option ServiceRequestFieldOption
		var schema string
		if err := rows.Scan(&option.ID, &option.Value, &schema); err != nil {
			return nil, err
		}
		option.Value += " (" + schema + ")"
		objects = append(objects, option)
	}
	return objects, rows.Err()
}

// ServiceAssetObjectInProject finds an Assets object of the service desk of a
// project by its id or object key, and answers its label.
func (s *Store) ServiceAssetObjectInProject(ctx context.Context, workspaceID, projectID, reference, assetSchemaID string) (string, string, error) {
	var id, label string
	err := s.Pool.QueryRow(ctx, `SELECT o.id::text,o.label
		FROM service_asset_objects o JOIN service_asset_schemas s ON s.id=o.schema_id JOIN service_desks sd ON sd.id=s.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.project_id=$2 AND (o.id::text=$3 OR upper(o.object_key)=upper($3))
		  AND ($4='' OR s.id::text=$4) LIMIT 1`, workspaceID, projectID, reference, assetSchemaID).Scan(&id, &label)
	return id, label, err
}

// IsServicePortalPicker reports a field type whose portal choices come from
// the site's groups, projects, versions or teams rather than configured options.
func IsServicePortalPicker(fieldType string) bool {
	switch fieldType {
	case models.CustomFieldGroup, models.CustomFieldMultiGroup, models.CustomFieldProject, models.CustomFieldVersion, models.CustomFieldMultiVersion, models.CustomFieldTeam, models.CustomFieldAsset:
		return true
	}
	return false
}

// ServiceDeskAssetSchemas names a service desk's Assets schemas, so a request
// type form can scope an Assets object field to one of them.
func (s *Store) ServiceDeskAssetSchemas(ctx context.Context, workspaceID, serviceDeskID string) ([]models.ServiceAssetSchema, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT sc.id::text,sc.schema_key,sc.name
		FROM service_asset_schemas sc JOIN service_desks sd ON sd.id=sc.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 ORDER BY lower(sc.name),sc.id`, workspaceID, serviceDeskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	schemas := []models.ServiceAssetSchema{}
	for rows.Next() {
		var schema models.ServiceAssetSchema
		if err := rows.Scan(&schema.ID, &schema.Key, &schema.Name); err != nil {
			return nil, err
		}
		schema.ServiceDeskID = serviceDeskID
		schemas = append(schemas, schema)
	}
	return schemas, rows.Err()
}
