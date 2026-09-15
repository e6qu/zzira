package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
)

// dynamicModuleTranslation is what a dynamic module was translated into when
// it was registered, kept so an upgrade can restore it.
type dynamicModuleTranslation struct {
	Module           models.AppModule                `json:"module"`
	EntityProperties []models.AppEntityPropertyIndex `json:"entityProperties,omitempty"`
}

// writeEntityPropertyIndexes stores an app's entity property extractions. A
// static module's extraction replaces nothing; a dynamic module cannot claim a
// property path the app already indexes.
func writeEntityPropertyIndexes(ctx context.Context, tx pgx.Tx, installationID string, indexes []models.AppEntityPropertyIndex, dynamic bool) error {
	for _, index := range indexes {
		command, err := tx.Exec(ctx, `INSERT INTO app_entity_property_indexes(installation_id,module_key,name,entity_type,property_key,object_name,extraction_type,alias,dynamic)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT DO NOTHING`,
			installationID, index.ModuleKey, index.Name, index.EntityType, index.PropertyKey, index.ObjectName, index.Type, index.Alias, dynamic)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 0 && dynamic {
			return ErrAdminConflict
		}
	}
	return nil
}

// EntityPropertyIndexes lists the entity property extractions the site's
// active apps declare, oldest installation first, static modules before
// dynamic ones, so an earlier alias keeps its name.
func (s *Store) EntityPropertyIndexes(ctx context.Context, workspaceID string) ([]models.AppEntityPropertyIndex, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT x.module_key,x.name,x.entity_type,x.property_key,x.object_name,x.extraction_type,x.alias
		FROM app_entity_property_indexes x JOIN app_installations i ON i.id=x.installation_id
		WHERE i.workspace_id=$1 AND i.status='active'
		ORDER BY i.installed_at,i.id,x.dynamic,x.module_key,x.property_key,x.object_name`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	indexes := []models.AppEntityPropertyIndex{}
	for rows.Next() {
		var index models.AppEntityPropertyIndex
		if err := rows.Scan(&index.ModuleKey, &index.Name, &index.EntityType, &index.PropertyKey, &index.ObjectName, &index.Type, &index.Alias); err != nil {
			return nil, err
		}
		indexes = append(indexes, index)
	}
	return indexes, rows.Err()
}

// JQLResolver is the field resolver a site's queries compile with: Jira's
// fields, the site's custom fields and the issue property values apps index.
func (s *Store) JQLResolver(ctx context.Context, workspaceID string) (jql.FieldResolver, error) {
	fields, err := s.CustomFieldsForWorkspace(ctx, workspaceID)
	if err != nil {
		return jql.FieldResolver{}, err
	}
	indexes, err := s.EntityPropertyIndexes(ctx, workspaceID)
	if err != nil {
		return jql.FieldResolver{}, err
	}
	return jql.WithEntityProperties(jql.WithCustomFields(jql.DefaultResolver(), fields), indexes), nil
}
