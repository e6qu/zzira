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
	metrics, err := s.ServiceSLAMetricsForWorkspace(ctx, workspaceID)
	if err != nil {
		return jql.FieldResolver{}, err
	}
	slaNames := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		slaNames = append(slaNames, metric.Name)
	}
	known, err := s.jqlKnownValues(ctx, workspaceID)
	if err != nil {
		return jql.FieldResolver{}, err
	}
	resolver := jql.WithSLAFields(jql.WithEntityProperties(jql.WithCustomFields(jql.DefaultResolver(), fields), indexes), slaNames)
	resolver.KnownValues = known
	// A query may name a saved filter, which is read when the query is
	// compiled and only for the person compiling it.
	return jql.WithFilterJQL(resolver, func(userID, nameOrID string) (string, bool) {
		return s.FilterJQLForSearch(ctx, workspaceID, userID, nameOrID)
	}), nil
}

// jqlKnownValuesQuery lists every value the fields a search validates can
// hold: the name each entity answers to -- an override where the site set one,
// as the search columns read them -- and its numeric id, which Jira also
// accepts.
const jqlKnownValuesQuery = `
SELECT 'status', lower(s.name) FROM statuses s WHERE s.workspace_id IS NULL OR s.workspace_id=$1
UNION ALL SELECT 'status', s.jira_id::text FROM statuses s WHERE s.workspace_id IS NULL OR s.workspace_id=$1
UNION ALL SELECT 'priority', lower(COALESCE(o.name,p.name)) FROM priorities p
  LEFT JOIN issue_metadata_overrides o ON o.workspace_id=$1 AND o.entity_type='priority' AND o.entity_id=p.id
  WHERE p.workspace_id IS NULL OR p.workspace_id=$1
UNION ALL SELECT 'priority', p.jira_id::text FROM priorities p WHERE p.workspace_id IS NULL OR p.workspace_id=$1
UNION ALL SELECT 'resolution', lower(COALESCE(o.name,r.name)) FROM resolutions r
  LEFT JOIN issue_metadata_overrides o ON o.workspace_id=$1 AND o.entity_type='resolution' AND o.entity_id=r.id
  WHERE r.workspace_id IS NULL OR r.workspace_id=$1
UNION ALL SELECT 'resolution', r.jira_id::text FROM resolutions r WHERE r.workspace_id IS NULL OR r.workspace_id=$1
UNION ALL SELECT 'issuetype', lower(COALESCE(o.name,t.name)) FROM issue_types t
  LEFT JOIN issue_metadata_overrides o ON o.workspace_id=$1 AND o.entity_type='issuetype' AND o.entity_id=t.id
  WHERE t.workspace_id IS NULL OR t.workspace_id=$1
UNION ALL SELECT 'issuetype', t.jira_id::text FROM issue_types t WHERE t.workspace_id IS NULL OR t.workspace_id=$1`

// jqlKnownValues reads the catalogue a search checks its values against.
func (s *Store) jqlKnownValues(ctx context.Context, workspaceID string) (map[string]map[string]bool, error) {
	rows, err := s.Pool.Query(ctx, jqlKnownValuesQuery, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := map[string]map[string]bool{}
	for rows.Next() {
		var field, value string
		if err := rows.Scan(&field, &value); err != nil {
			return nil, err
		}
		if values[field] == nil {
			values[field] = map[string]bool{}
		}
		values[field][value] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// "Unresolved" is how Jira writes "no resolution", and the compiler reads
	// it as the absence of one rather than as a resolution by that name.
	if values["resolution"] != nil {
		values["resolution"]["unresolved"] = true
	}
	return values, rows.Err()
}
