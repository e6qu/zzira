package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/e6qu/zzira/internal/aql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// ErrAssetNotFound is a schema or object that is not in the site's Assets
// workspace, or not in a desk the actor agents.
var ErrAssetNotFound = errors.New("asset not found")

// serviceAssetDesk is the desk an Assets id belongs to, and it is where the
// Assets API gets its authorization: the desk's own agent check.
func (s *Store) serviceAssetDesk(ctx context.Context, ws, actor, table, id string) (string, error) {
	var deskID string
	query := `SELECT s.service_desk_id FROM service_asset_schemas s JOIN service_desks sd ON sd.id=s.service_desk_id WHERE sd.workspace_id=$1 AND s.id::text=$2`
	switch table {
	case "object":
		query = `SELECT s.service_desk_id FROM service_asset_objects o JOIN service_asset_schemas s ON s.id=o.schema_id JOIN service_desks sd ON sd.id=s.service_desk_id WHERE sd.workspace_id=$1 AND o.id::text=$2`
	case "schema":
	default:
		return "", fmt.Errorf("unknown asset entity %q", table)
	}
	if err := s.Pool.QueryRow(ctx, query, ws, id).Scan(&deskID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrAssetNotFound
		}
		return "", err
	}
	allowed, err := s.IsServiceAgent(ctx, ws, deskID, actor)
	if err != nil {
		return "", err
	}
	if !allowed {
		// An agent of another desk is told the same as anyone else, so the
		// API never confirms an id it will not serve.
		return "", ErrAssetNotFound
	}
	return deskID, nil
}

// ServiceAssetSchemas are every schema in the site's Assets workspace that the
// actor agents, newest desk first as the inventory pages order them.
func (s *Store) ServiceAssetSchemas(ctx context.Context, ws, actor string) ([]models.ServiceAssetSchema, error) {
	desks, err := s.ServiceDesksForAgent(ctx, ws, actor)
	if err != nil {
		return nil, err
	}
	schemas := []models.ServiceAssetSchema{}
	for _, desk := range desks {
		inventory, err := s.ServiceAssetInventory(ctx, ws, actor, desk.ID)
		if err != nil {
			return nil, err
		}
		schemas = append(schemas, inventory.Schemas...)
	}
	return schemas, nil
}

// ServiceAssetSchema is one schema and the desk it belongs to.
func (s *Store) ServiceAssetSchema(ctx context.Context, ws, actor, schemaID string) (*models.ServiceAssetSchema, string, error) {
	deskID, err := s.serviceAssetDesk(ctx, ws, actor, "schema", schemaID)
	if err != nil {
		return nil, "", err
	}
	inventory, err := s.ServiceAssetInventory(ctx, ws, actor, deskID)
	if err != nil {
		return nil, "", err
	}
	for i := range inventory.Schemas {
		if inventory.Schemas[i].ID == schemaID {
			return &inventory.Schemas[i], deskID, nil
		}
	}
	return nil, "", ErrAssetNotFound
}

// ServiceAssetObject is one object and the desk it belongs to.
func (s *Store) ServiceAssetObject(ctx context.Context, ws, actor, objectID string) (*models.ServiceAssetObject, string, error) {
	deskID, err := s.serviceAssetDesk(ctx, ws, actor, "object", objectID)
	if err != nil {
		return nil, "", err
	}
	row := s.Pool.QueryRow(ctx, `SELECT o.id::text,o.schema_id::text,s.schema_key,s.name,o.object_key,o.label,o.values,o.x,o.y
		FROM service_asset_objects o JOIN service_asset_schemas s ON s.id=o.schema_id WHERE o.id::text=$1`, objectID)
	object, err := scanServiceAssetObject(row)
	if err != nil {
		return nil, "", err
	}
	return object, deskID, nil
}

// ServiceAssetObjectSearch is one page of the objects an AQL filter matches.
type ServiceAssetObjectSearch struct {
	Objects []models.ServiceAssetObject
	Total   int
}

// SearchServiceAssetObjects runs an AQL filter across the desks the actor
// agents, or one schema when a schema is named.
func (s *Store) SearchServiceAssetObjects(ctx context.Context, ws, actor, schemaID, filter string, start, limit int) (*ServiceAssetObjectSearch, error) {
	deskIDs := []string{}
	if schemaID != "" {
		deskID, err := s.serviceAssetDesk(ctx, ws, actor, "schema", schemaID)
		if err != nil {
			return nil, err
		}
		deskIDs = append(deskIDs, deskID)
	} else {
		desks, err := s.ServiceDesksForAgent(ctx, ws, actor)
		if err != nil {
			return nil, err
		}
		for _, desk := range desks {
			deskIDs = append(deskIDs, desk.ID)
		}
	}
	found := &ServiceAssetObjectSearch{Objects: []models.ServiceAssetObject{}}
	matched := []models.ServiceAssetObject{}
	for _, deskID := range deskIDs {
		objects, err := s.serviceAssetObjectsMatching(ctx, ws, deskID, schemaID, filter)
		if err != nil {
			return nil, err
		}
		matched = append(matched, objects...)
	}
	found.Total = len(matched)
	if start < 0 {
		start = 0
	}
	if start > len(matched) {
		start = len(matched)
	}
	matched = matched[start:]
	if limit > 0 && limit < len(matched) {
		matched = matched[:limit]
	}
	found.Objects = append(found.Objects, matched...)
	return found, nil
}

func (s *Store) serviceAssetObjectsMatching(ctx context.Context, ws, deskID, schemaID, filter string) ([]models.ServiceAssetObject, error) {
	where, args := "TRUE", []any{ws, deskID, schemaID}
	if strings.TrimSpace(filter) != "" {
		query, err := aql.Parse(filter)
		if err != nil {
			return nil, fmt.Errorf("the Assets filter %q: %w", filter, err)
		}
		columns := aql.DefaultColumns()
		if columns.Attributes, err = s.serviceAssetAttributeKeys(ctx, deskID); err != nil {
			return nil, err
		}
		compiled := query.Compile(columns, len(args)+1)
		where, args = compiled.Where, append(args, compiled.Args...)
	}
	rows, err := s.Pool.Query(ctx, `SELECT o.id::text,o.schema_id::text,s.schema_key,s.name,o.object_key,o.label,o.values,o.x,o.y
		FROM service_asset_objects o JOIN service_asset_schemas s ON s.id=o.schema_id JOIN service_desks sd ON sd.id=s.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND ($3='' OR s.id::text=$3) AND (`+where+`)
		ORDER BY lower(s.name),lower(o.label),o.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	objects := []models.ServiceAssetObject{}
	for rows.Next() {
		object, err := scanServiceAssetObject(rows)
		if err != nil {
			return nil, err
		}
		objects = append(objects, *object)
	}
	return objects, rows.Err()
}

// ServiceAssetObjectReferences are the relationships an object is either end
// of, so a caller can walk the topology one object at a time.
func (s *Store) ServiceAssetObjectReferences(ctx context.Context, ws, actor, objectID string) ([]models.ServiceAssetRelationship, error) {
	_, deskID, err := s.ServiceAssetObject(ctx, ws, actor, objectID)
	if err != nil {
		return nil, err
	}
	inventory, err := s.ServiceAssetInventory(ctx, ws, actor, deskID)
	if err != nil {
		return nil, err
	}
	references := []models.ServiceAssetRelationship{}
	for _, relation := range inventory.Relationships {
		if relation.From.ID == objectID || relation.To.ID == objectID {
			references = append(references, relation)
		}
	}
	return references, nil
}

// ServiceAssetObjectRequests are the requests linked to an object, directly or
// through what depends on it.
func (s *Store) ServiceAssetObjectRequests(ctx context.Context, ws, actor, objectID string) ([]models.ServiceAssetObjectRequest, error) {
	if _, _, err := s.ServiceAssetObject(ctx, ws, actor, objectID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT i.id::text,i.key,i.summary,a.role
		FROM service_request_assets a JOIN issues i ON i.id=a.request_issue_id
		WHERE a.object_id::text=$1 AND i.workspace_id=$2 ORDER BY i.id::bigint`, objectID, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requests := []models.ServiceAssetObjectRequest{}
	for rows.Next() {
		var request models.ServiceAssetObjectRequest
		if err := rows.Scan(&request.IssueID, &request.Key, &request.Summary, &request.Role); err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}
