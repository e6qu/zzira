package store

import (
	"context"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

func scanServiceDesk(row interface{ Scan(...any) error }) (*models.ServiceDesk, error) {
	desk := &models.ServiceDesk{}
	err := row.Scan(&desk.ID, &desk.WorkspaceID, &desk.ProjectID, &desk.ProjectKey, &desk.ProjectName, &desk.ProjectTypeKey, &desk.PortalName)
	return desk, err
}

const serviceDeskSelect = `SELECT sd.id,sd.workspace_id,p.id,p.key,p.name,p.project_type_key,sd.portal_name FROM service_desks sd JOIN projects p ON p.id=sd.project_id `

func (s *Store) ServiceDesks(ctx context.Context, workspaceID string) ([]models.ServiceDesk, error) {
	rows, err := s.Pool.Query(ctx, serviceDeskSelect+`WHERE sd.workspace_id=$1 ORDER BY sd.id::bigint`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]models.ServiceDesk, 0)
	for rows.Next() {
		desk, err := scanServiceDesk(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, *desk)
	}
	return values, rows.Err()
}

func (s *Store) ServiceDesk(ctx context.Context, workspaceID, id string) (*models.ServiceDesk, error) {
	return scanServiceDesk(s.Pool.QueryRow(ctx, serviceDeskSelect+`WHERE sd.workspace_id=$1 AND sd.id=$2`, workspaceID, id))
}

func scanServiceRequestType(row interface{ Scan(...any) error }) (*models.ServiceRequestType, error) {
	requestType := &models.ServiceRequestType{}
	err := row.Scan(&requestType.ID, &requestType.ServiceDeskID, &requestType.Name, &requestType.Description, &requestType.HelpText, &requestType.IssueTypeID, &requestType.GroupIDs)
	return requestType, err
}

const serviceRequestTypeSelect = `SELECT id,service_desk_id,name,description,help_text,issue_type_id,group_ids FROM service_request_types `

func (s *Store) ServiceRequestTypes(ctx context.Context, workspaceID, serviceDeskID, search string) ([]models.ServiceRequestType, error) {
	search = strings.TrimSpace(search)
	rows, err := s.Pool.Query(ctx, serviceRequestTypeSelect+`
		WHERE service_desk_id IN (SELECT id FROM service_desks WHERE workspace_id=$1)
		  AND ($2='' OR service_desk_id=$2) AND ($3='' OR name ILIKE '%'||$3||'%' OR description ILIKE '%'||$3||'%')
		ORDER BY service_desk_id::bigint,id::bigint`, workspaceID, serviceDeskID, search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]models.ServiceRequestType, 0)
	for rows.Next() {
		requestType, err := scanServiceRequestType(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, *requestType)
	}
	return values, rows.Err()
}

func (s *Store) ServiceRequestType(ctx context.Context, workspaceID, serviceDeskID, id string) (*models.ServiceRequestType, error) {
	return scanServiceRequestType(s.Pool.QueryRow(ctx, serviceRequestTypeSelect+`
		WHERE service_desk_id=$2 AND id=$3 AND service_desk_id IN (SELECT id FROM service_desks WHERE workspace_id=$1)`, workspaceID, serviceDeskID, id))
}

func (s *Store) CreateServiceRequestType(ctx context.Context, workspaceID, serviceDeskID, name, description, helpText, issueTypeID string) (*models.ServiceRequestType, error) {
	requestType := &models.ServiceRequestType{}
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO service_request_types(service_desk_id,name,description,help_text,issue_type_id)
		SELECT sd.id,$3,$4,$5,$6 FROM service_desks sd
		WHERE sd.workspace_id=$1 AND sd.id=$2
		RETURNING id,service_desk_id,name,description,help_text,issue_type_id,group_ids`, workspaceID, serviceDeskID, name, description, helpText, issueTypeID).Scan(
		&requestType.ID, &requestType.ServiceDeskID, &requestType.Name, &requestType.Description,
		&requestType.HelpText, &requestType.IssueTypeID, &requestType.GroupIDs)
	return requestType, err
}

func (s *Store) DeleteServiceRequestType(ctx context.Context, workspaceID, serviceDeskID, id string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM service_request_types WHERE id=$3 AND service_desk_id=$2 AND service_desk_id IN (SELECT id FROM service_desks WHERE workspace_id=$1)`, workspaceID, serviceDeskID, id)
	return err
}
