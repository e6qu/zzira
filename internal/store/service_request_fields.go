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
		  f.help_text,f.required,(f.field_id LIKE 'customfield_%'),f.position
		FROM service_request_type_fields f
		JOIN service_request_types rt ON rt.id=f.request_type_id
		JOIN service_desks sd ON sd.id=rt.service_desk_id
		LEFT JOIN custom_fields cf ON cf.id=f.field_id
		WHERE sd.workspace_id=$1 AND sd.id=$2 AND rt.id=$3
		  AND (f.field_id IN ('summary','description') OR (cf.id IS NOT NULL AND (
		    NOT EXISTS (SELECT 1 FROM field_contexts fc0 WHERE fc0.field_id=cf.id)
		    OR EXISTS (
		      SELECT 1 FROM field_contexts fc
		      WHERE fc.field_id=cf.id AND (fc.project_id IS NULL OR fc.project_id=sd.project_id)
		    )
		  )))
		ORDER BY f.position,f.field_id`, workspaceID, serviceDeskID, requestTypeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := make([]models.ServiceRequestTypeField, 0)
	for rows.Next() {
		var field models.ServiceRequestTypeField
		if err := rows.Scan(&field.ID, &field.RequestTypeID, &field.Name, &field.Type, &field.Description, &field.HelpText, &field.Required, &field.Custom, &field.Position); err != nil {
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
		if _, err := tx.Exec(ctx, `INSERT INTO service_request_type_fields(request_type_id,field_id,required,help_text,position) VALUES($1,$2,$3,$4,$5)`, requestTypeID, field.ID, field.Required, field.HelpText, position); err != nil {
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
