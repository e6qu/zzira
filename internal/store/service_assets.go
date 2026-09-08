package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func serviceAssetAction(ctx context.Context, tx pgx.Tx, ws, actor, entity, id, op string, value any) error {
	seq, err := nextSeq(ctx, tx, ws)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{entity: value})
	if err != nil {
		return err
	}
	return appendAction(ctx, tx, &models.Action{WorkspaceID: ws, Seq: seq, EntityType: entity, EntityID: id, Op: op, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actor})
}

func (s *Store) ServiceAssetInventory(ctx context.Context, ws, actor, deskID string) (*models.ServiceAssetInventory, error) {
	allowed, err := s.IsServiceAgent(ctx, ws, deskID, actor)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrProjectPermission
	}
	inventory := &models.ServiceAssetInventory{}
	rows, err := s.Pool.Query(ctx, `SELECT s.id::text,s.assets_workspace_id::text,s.service_desk_id,s.schema_key,s.name,s.description,s.attributes FROM service_asset_schemas s JOIN service_desks sd ON sd.id=s.service_desk_id WHERE sd.workspace_id=$1 AND s.service_desk_id=$2 ORDER BY lower(s.name),s.id`, ws, deskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var schema models.ServiceAssetSchema
		var attributes []byte
		if err := rows.Scan(&schema.ID, &schema.AssetsWorkspaceID, &schema.ServiceDeskID, &schema.Key, &schema.Name, &schema.Description, &attributes); err != nil {
			rows.Close()
			return nil, err
		}
		if err := json.Unmarshal(attributes, &schema.Attributes); err != nil {
			rows.Close()
			return nil, fmt.Errorf("decode asset schema attributes: %w", err)
		}
		inventory.Schemas = append(inventory.Schemas, schema)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = s.Pool.Query(ctx, `SELECT o.id::text,o.schema_id::text,s.schema_key,s.name,o.object_key,o.label,o.values,o.x,o.y FROM service_asset_objects o JOIN service_asset_schemas s ON s.id=o.schema_id JOIN service_desks sd ON sd.id=s.service_desk_id WHERE sd.workspace_id=$1 AND s.service_desk_id=$2 ORDER BY lower(o.label),o.id`, ws, deskID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		object, err := scanServiceAssetObject(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		inventory.Objects = append(inventory.Objects, *object)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	rows, err = s.Pool.Query(ctx, `SELECT r.id::text,r.relationship,
f.id::text,f.schema_id::text,fs.schema_key,fs.name,f.object_key,f.label,f.values,f.x,f.y,
t.id::text,t.schema_id::text,ts.schema_key,ts.name,t.object_key,t.label,t.values,t.x,t.y
FROM service_asset_relationships r JOIN service_desks sd ON sd.id=r.service_desk_id
JOIN service_asset_objects f ON f.id=r.from_object_id JOIN service_asset_schemas fs ON fs.id=f.schema_id
JOIN service_asset_objects t ON t.id=r.to_object_id JOIN service_asset_schemas ts ON ts.id=t.schema_id
WHERE sd.workspace_id=$1 AND r.service_desk_id=$2 ORDER BY r.id`, ws, deskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var relation models.ServiceAssetRelationship
		var fromValues, toValues []byte
		if err := rows.Scan(&relation.ID, &relation.Relationship,
			&relation.From.ID, &relation.From.SchemaID, &relation.From.SchemaKey, &relation.From.SchemaName, &relation.From.Key, &relation.From.Label, &fromValues, &relation.From.X, &relation.From.Y,
			&relation.To.ID, &relation.To.SchemaID, &relation.To.SchemaKey, &relation.To.SchemaName, &relation.To.Key, &relation.To.Label, &toValues, &relation.To.X, &relation.To.Y); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(fromValues, &relation.From.Values); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(toValues, &relation.To.Values); err != nil {
			return nil, err
		}
		inventory.Relationships = append(inventory.Relationships, relation)
	}
	return inventory, rows.Err()
}

func scanServiceAssetObject(row pgx.Row) (*models.ServiceAssetObject, error) {
	object := &models.ServiceAssetObject{}
	var values []byte
	if err := row.Scan(&object.ID, &object.SchemaID, &object.SchemaKey, &object.SchemaName, &object.Key, &object.Label, &values, &object.X, &object.Y); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(values, &object.Values); err != nil {
		return nil, fmt.Errorf("decode asset values: %w", err)
	}
	return object, nil
}

func (s *Store) CreateServiceAssetSchema(ctx context.Context, ws, actor, deskID string, schema models.ServiceAssetSchema) (*models.ServiceAssetSchema, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(schema.Attributes)
	if err != nil {
		return nil, err
	}
	schema.ServiceDeskID = deskID
	err = tx.QueryRow(ctx, `INSERT INTO service_asset_schemas(assets_workspace_id,service_desk_id,schema_key,name,description,attributes)
SELECT aw.id,sd.id,$3,$4,$5,$6 FROM service_desks sd JOIN service_assets_workspaces aw ON aw.workspace_id=sd.workspace_id WHERE sd.workspace_id=$1 AND sd.id=$2 RETURNING id::text,assets_workspace_id::text`, ws, deskID, schema.Key, schema.Name, schema.Description, raw).Scan(&schema.ID, &schema.AssetsWorkspaceID)
	if err != nil {
		return nil, err
	}
	if err := serviceAssetAction(ctx, tx, ws, actor, "service_asset_schema", schema.ID, models.OpUpsert, schema); err != nil {
		return nil, err
	}
	return &schema, tx.Commit(ctx)
}

func (s *Store) DeleteServiceAssetSchema(ctx context.Context, ws, actor, deskID, schemaID string) error {
	return s.deleteServiceAssetEntity(ctx, ws, actor, deskID, "schema", schemaID)
}

func (s *Store) SaveServiceAssetObject(ctx context.Context, ws, actor, deskID string, object models.ServiceAssetObject) (*models.ServiceAssetObject, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(object.Values)
	if err != nil {
		return nil, err
	}
	if object.ID == "" {
		err = tx.QueryRow(ctx, `INSERT INTO service_asset_objects(schema_id,object_key,label,values,x,y) SELECT s.id,$4,$5,$6,$7,$8 FROM service_asset_schemas s JOIN service_desks sd ON sd.id=s.service_desk_id WHERE sd.workspace_id=$1 AND s.service_desk_id=$2 AND s.id::text=$3 RETURNING id::text`, ws, deskID, object.SchemaID, object.Key, object.Label, raw, object.X, object.Y).Scan(&object.ID)
	} else {
		err = tx.QueryRow(ctx, `UPDATE service_asset_objects o SET object_key=$5,label=$6,values=$7,x=$8,y=$9,updated_at=now() FROM service_asset_schemas s JOIN service_desks sd ON sd.id=s.service_desk_id WHERE o.schema_id=s.id AND sd.workspace_id=$1 AND s.service_desk_id=$2 AND s.id::text=$3 AND o.id::text=$4 RETURNING o.id::text`, ws, deskID, object.SchemaID, object.ID, object.Key, object.Label, raw, object.X, object.Y).Scan(&object.ID)
	}
	if err != nil {
		return nil, err
	}
	if err := serviceAssetAction(ctx, tx, ws, actor, "service_asset_object", object.ID, models.OpUpsert, object); err != nil {
		return nil, err
	}
	return &object, tx.Commit(ctx)
}

func (s *Store) DeleteServiceAssetObject(ctx context.Context, ws, actor, deskID, objectID string) error {
	return s.deleteServiceAssetEntity(ctx, ws, actor, deskID, "object", objectID)
}

func (s *Store) CreateServiceAssetRelationship(ctx context.Context, ws, actor, deskID string, relation models.ServiceAssetRelationship) (*models.ServiceAssetRelationship, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx, `INSERT INTO service_asset_relationships(service_desk_id,from_object_id,to_object_id,relationship)
SELECT sd.id,$3::uuid,$4::uuid,$5 FROM service_desks sd WHERE sd.workspace_id=$1 AND sd.id=$2
AND EXISTS(SELECT 1 FROM service_asset_objects o JOIN service_asset_schemas s ON s.id=o.schema_id WHERE s.service_desk_id=sd.id AND o.id::text=$3::text)
AND EXISTS(SELECT 1 FROM service_asset_objects o JOIN service_asset_schemas s ON s.id=o.schema_id WHERE s.service_desk_id=sd.id AND o.id::text=$4::text)
RETURNING id::text`, ws, deskID, relation.From.ID, relation.To.ID, relation.Relationship).Scan(&relation.ID)
	if err != nil {
		return nil, err
	}
	if err := serviceAssetAction(ctx, tx, ws, actor, "service_asset_relationship", relation.ID, models.OpUpsert, relation); err != nil {
		return nil, err
	}
	return &relation, tx.Commit(ctx)
}

func (s *Store) DeleteServiceAssetRelationship(ctx context.Context, ws, actor, deskID, relationID string) error {
	return s.deleteServiceAssetEntity(ctx, ws, actor, deskID, "relationship", relationID)
}

func (s *Store) deleteServiceAssetEntity(ctx context.Context, ws, actor, deskID, kind, id string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := projectAdmin(ctx, tx, ws, actor); err != nil {
		return err
	}
	queries := map[string]string{
		"schema":       `DELETE FROM service_asset_schemas s USING service_desks sd WHERE s.service_desk_id=sd.id AND sd.workspace_id=$1 AND s.service_desk_id=$2 AND s.id::text=$3`,
		"object":       `DELETE FROM service_asset_objects o USING service_asset_schemas s,service_desks sd WHERE o.schema_id=s.id AND s.service_desk_id=sd.id AND sd.workspace_id=$1 AND s.service_desk_id=$2 AND o.id::text=$3`,
		"relationship": `DELETE FROM service_asset_relationships r USING service_desks sd WHERE r.service_desk_id=sd.id AND sd.workspace_id=$1 AND r.service_desk_id=$2 AND r.id::text=$3`,
	}
	query, ok := queries[kind]
	if !ok {
		return fmt.Errorf("unknown asset entity kind")
	}
	tag, err := tx.Exec(ctx, query, ws, deskID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	entity := "service_asset_" + kind
	if err := serviceAssetAction(ctx, tx, ws, actor, entity, id, models.OpDelete, map[string]string{"id": id}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetServiceRequestAsset(ctx context.Context, ws, actor, issueID, objectID, role string, linked bool) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var deskID string
	if err := tx.QueryRow(ctx, `SELECT sr.service_desk_id FROM service_requests sr WHERE sr.workspace_id=$1 AND sr.issue_id=$2`, ws, issueID).Scan(&deskID); err != nil {
		return err
	}
	allowed, err := s.IsServiceAgent(ctx, ws, deskID, actor)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrProjectPermission
	}
	if linked {
		var tag pgconn.CommandTag
		tag, err = tx.Exec(ctx, `INSERT INTO service_request_assets(request_issue_id,object_id,role) SELECT $1,$2::uuid,$3 WHERE EXISTS(SELECT 1 FROM service_asset_objects o JOIN service_asset_schemas s ON s.id=o.schema_id WHERE s.service_desk_id=$4 AND o.id::text=$2::text) ON CONFLICT(request_issue_id,object_id) DO UPDATE SET role=EXCLUDED.role`, issueID, objectID, role, deskID)
		if err == nil && tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
	} else {
		var tag pgconn.CommandTag
		tag, err = tx.Exec(ctx, `DELETE FROM service_request_assets WHERE request_issue_id=$1 AND object_id::text=$2`, issueID, objectID)
		if err == nil && tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
	}
	if err != nil {
		return err
	}
	value := map[string]any{"issueId": issueID, "objectId": objectID, "role": role, "linked": linked}
	op := models.OpUpsert
	if !linked {
		op = models.OpDelete
	}
	if err := serviceAssetAction(ctx, tx, ws, actor, "service_request_asset", issueID+":"+objectID, op, value); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ServiceRequestAssetImpact(ctx context.Context, ws, actor, issueID string) ([]models.ServiceRequestAsset, error) {
	var deskID string
	if err := s.Pool.QueryRow(ctx, `SELECT service_desk_id FROM service_requests WHERE workspace_id=$1 AND issue_id=$2`, ws, issueID).Scan(&deskID); err != nil {
		return nil, err
	}
	allowed, err := s.IsServiceAgent(ctx, ws, deskID, actor)
	if err != nil || !allowed {
		if err != nil {
			return nil, err
		}
		return nil, ErrProjectPermission
	}
	inventory, err := s.ServiceAssetInventory(ctx, ws, actor, deskID)
	if err != nil {
		return nil, err
	}
	roles := map[string]string{}
	rows, err := s.Pool.Query(ctx, `SELECT object_id::text,role FROM service_request_assets WHERE request_issue_id=$1`, issueID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, role string
		if err := rows.Scan(&id, &role); err != nil {
			rows.Close()
			return nil, err
		}
		roles[id] = role
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	objects := map[string]models.ServiceAssetObject{}
	for _, object := range inventory.Objects {
		objects[object.ID] = object
	}
	depth := map[string]int{}
	queue := []string{}
	result := []models.ServiceRequestAsset{}
	for id, role := range roles {
		if object, ok := objects[id]; ok {
			depth[id], queue = 0, append(queue, id)
			result = append(result, models.ServiceRequestAsset{Object: object, Role: role, Direct: true})
		}
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if depth[current] >= 8 {
			continue
		}
		for _, relation := range inventory.Relationships {
			if relation.To.ID != current {
				continue
			}
			id := relation.From.ID
			if _, seen := depth[id]; seen {
				continue
			}
			depth[id] = depth[current] + 1
			queue = append(queue, id)
			result = append(result, models.ServiceRequestAsset{Object: objects[id], Role: "impacted", Depth: depth[id]})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Depth != result[j].Depth {
			return result[i].Depth < result[j].Depth
		}
		if result[i].Object.Label != result[j].Object.Label {
			return result[i].Object.Label < result[j].Object.Label
		}
		return result[i].Object.Key < result[j].Object.Key
	})
	return result, nil
}
