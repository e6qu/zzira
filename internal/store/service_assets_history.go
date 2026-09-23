package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/e6qu/zzira/internal/models"
)

// ServiceAssetObjectHistory is what has happened to one object, oldest first.
// Every write to the inventory already appends an action with the object as
// it was left, so the history is read from those rather than kept twice.
func (s *Store) ServiceAssetObjectHistory(ctx context.Context, ws, actor, objectID string) ([]models.ServiceAssetObjectChange, error) {
	if _, _, err := s.ServiceAssetObject(ctx, ws, actor, objectID); err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT a.seq,a.op,a.payload,a.actor_id,COALESCE(u.display_name,''),to_char(a.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM actions a LEFT JOIN users u ON u.id=a.actor_id
		WHERE a.workspace_id=$1 AND a.entity_type='service_asset_object' AND a.entity_id=$2
		ORDER BY a.seq`, ws, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	history := []models.ServiceAssetObjectChange{}
	var previous *models.ServiceAssetObject
	for rows.Next() {
		var change models.ServiceAssetObjectChange
		var payload []byte
		if err := rows.Scan(&change.Seq, &change.Operation, &payload, &change.ActorID, &change.ActorName, &change.At); err != nil {
			return nil, err
		}
		var wrapper struct {
			Object models.ServiceAssetObject `json:"service_asset_object"`
		}
		if err := json.Unmarshal(payload, &wrapper); err != nil {
			return nil, fmt.Errorf("read the asset history: %w", err)
		}
		change.Object = wrapper.Object
		if previous != nil && change.Operation != models.OpDelete {
			change.Changed = changedAssetFields(*previous, change.Object)
		}
		if change.Operation != models.OpDelete {
			object := change.Object
			previous = &object
		}
		history = append(history, change)
	}
	return history, rows.Err()
}

// changedAssetFields names what one write altered, so a history reads as what
// somebody did rather than as the whole object again.
func changedAssetFields(before, after models.ServiceAssetObject) []string {
	changed := []string{}
	if before.Key != after.Key {
		changed = append(changed, "key")
	}
	if before.Label != after.Label {
		changed = append(changed, "label")
	}
	if before.X != after.X || before.Y != after.Y {
		changed = append(changed, "position")
	}
	seen := map[string]bool{}
	for key, value := range after.Values {
		if before.Values[key] != value {
			changed = append(changed, key)
		}
		seen[key] = true
	}
	for key := range before.Values {
		if !seen[key] {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}

// ServiceAssetInventoryHistory is the history of every object in one desk's
// inventory, by object id, so a page reads them in one query rather than one
// query per object.
func (s *Store) ServiceAssetInventoryHistory(ctx context.Context, ws, actor, deskID string) (map[string][]models.ServiceAssetObjectChange, error) {
	allowed, err := s.IsServiceAgent(ctx, ws, deskID, actor)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrProjectPermission
	}
	rows, err := s.Pool.Query(ctx, `SELECT a.entity_id,a.seq,a.op,a.payload,a.actor_id,COALESCE(u.display_name,''),to_char(a.created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM actions a LEFT JOIN users u ON u.id=a.actor_id
		WHERE a.workspace_id=$1 AND a.entity_type='service_asset_object'
		  AND a.entity_id IN (
			SELECT o.id::text FROM service_asset_objects o
			JOIN service_asset_schemas s ON s.id=o.schema_id WHERE s.service_desk_id=$2)
		ORDER BY a.entity_id,a.seq`, ws, deskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	history := map[string][]models.ServiceAssetObjectChange{}
	previous := map[string]models.ServiceAssetObject{}
	for rows.Next() {
		var objectID string
		var change models.ServiceAssetObjectChange
		var payload []byte
		if err := rows.Scan(&objectID, &change.Seq, &change.Operation, &payload, &change.ActorID, &change.ActorName, &change.At); err != nil {
			return nil, err
		}
		var wrapper struct {
			Object models.ServiceAssetObject `json:"service_asset_object"`
		}
		if err := json.Unmarshal(payload, &wrapper); err != nil {
			return nil, fmt.Errorf("read the asset history: %w", err)
		}
		change.Object = wrapper.Object
		if before, ok := previous[objectID]; ok && change.Operation != models.OpDelete {
			change.Changed = changedAssetFields(before, change.Object)
		}
		if change.Operation != models.OpDelete {
			previous[objectID] = change.Object
		}
		history[objectID] = append(history[objectID], change)
	}
	return history, rows.Err()
}
