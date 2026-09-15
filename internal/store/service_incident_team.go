package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// ErrServiceIncidentValidation marks an incident team change that cannot be made.
var ErrServiceIncidentValidation = errors.New("invalid incident team change")

func serviceIncidentRoleName(role string) string {
	for _, definition := range models.ServiceIncidentRoleDefinitions {
		if definition.Key == role {
			return definition.Name
		}
	}
	return ""
}

// agentOfMajorIncident confirms someone manages a request that is a major
// incident now.
func (s *Store) agentOfMajorIncident(ctx context.Context, workspaceID, actorID, issueID string) error {
	canManage, err := s.CanManageServiceRequest(ctx, workspaceID, actorID, issueID)
	if err != nil {
		return err
	}
	if !canManage {
		return ErrProjectPermission
	}
	var major bool
	err = s.Pool.QueryRow(ctx, `SELECT o.kind='incident' AND o.major_incident FROM service_request_operations o JOIN service_requests r ON r.issue_id=o.request_issue_id WHERE r.workspace_id=$1 AND o.request_issue_id=$2`, workspaceID, issueID).Scan(&major)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !major {
		return fmt.Errorf("%w: declare this request as a major incident first", ErrServiceIncidentValidation)
	}
	return err
}

func auditServiceIncident(ctx context.Context, tx pgx.Tx, workspaceID, actorID, issueID, action string, detail map[string]any) error {
	encoded, _ := json.Marshal(detail)
	_, err := tx.Exec(ctx, `INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail) SELECT organization_id,$2,$3,'service_request',$4,$5::jsonb FROM sites WHERE workspace_id=$1`, workspaceID, actorID, action, issueID, encoded)
	return err
}

// ServiceIncidentRoles lists who holds each incident role, for agents of the
// request.
func (s *Store) ServiceIncidentRoles(ctx context.Context, workspaceID, actorID, issueID string) ([]models.ServiceIncidentRole, error) {
	canManage, err := s.CanManageServiceRequest(ctx, workspaceID, actorID, issueID)
	if err != nil {
		return nil, err
	}
	if !canManage {
		return nil, ErrProjectPermission
	}
	rows, err := s.Pool.Query(ctx, `SELECT ir.role,ir.user_id,u.display_name,ir.assigned_at FROM service_incident_roles ir JOIN users u ON u.id=ir.user_id JOIN service_requests r ON r.issue_id=ir.request_issue_id WHERE r.workspace_id=$1 AND ir.request_issue_id=$2`, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	held := map[string]models.ServiceIncidentRole{}
	for rows.Next() {
		var role models.ServiceIncidentRole
		var assignedAt time.Time
		if err := rows.Scan(&role.Role, &role.UserID, &role.UserName, &assignedAt); err != nil {
			return nil, err
		}
		assignedAt = assignedAt.UTC()
		role.AssignedAt = &assignedAt
		held[role.Role] = role
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	roles := make([]models.ServiceIncidentRole, 0, len(models.ServiceIncidentRoleDefinitions))
	for _, definition := range models.ServiceIncidentRoleDefinitions {
		role := held[definition.Key]
		role.Role, role.Name = definition.Key, definition.Name
		roles = append(roles, role)
	}
	return roles, nil
}

// SetServiceIncidentRole gives an incident role to someone who manages the
// request, or clears it when userID is empty.
func (s *Store) SetServiceIncidentRole(ctx context.Context, workspaceID, actorID, issueID, role, userID string) error {
	if serviceIncidentRoleName(role) == "" {
		return fmt.Errorf("%w: choose an incident commander, communications lead or technical lead role", ErrServiceIncidentValidation)
	}
	if err := s.agentOfMajorIncident(ctx, workspaceID, actorID, issueID); err != nil {
		return err
	}
	if userID != "" {
		holder, err := s.CanManageServiceRequest(ctx, workspaceID, userID, issueID)
		if err != nil {
			return err
		}
		if !holder {
			return fmt.Errorf("%w: incident roles go to agents of this service desk", ErrServiceIncidentValidation)
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if userID == "" {
		_, err = tx.Exec(ctx, `DELETE FROM service_incident_roles WHERE request_issue_id=$1 AND role=$2`, issueID, role)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO service_incident_roles(request_issue_id,role,user_id,assigned_by) VALUES($1,$2,$3,$4)
			ON CONFLICT (request_issue_id,role) DO UPDATE SET user_id=EXCLUDED.user_id,assigned_by=EXCLUDED.assigned_by,assigned_at=now()`, issueID, role, userID, actorID)
	}
	if err != nil {
		return err
	}
	if err := auditServiceIncident(ctx, tx, workspaceID, actorID, issueID, "service_major_incident_role_assigned", map[string]any{"role": role, "userId": userID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ServiceIncidentStakeholders lists a request's stakeholders for its agents.
func (s *Store) ServiceIncidentStakeholders(ctx context.Context, workspaceID, actorID, issueID string) ([]models.ServiceIncidentStakeholder, error) {
	canManage, err := s.CanManageServiceRequest(ctx, workspaceID, actorID, issueID)
	if err != nil {
		return nil, err
	}
	if !canManage {
		return nil, ErrProjectPermission
	}
	rows, err := s.Pool.Query(ctx, `SELECT st.id::text,COALESCE(st.user_id,''),COALESCE(u.display_name,''),COALESCE(u.email,st.email,''),st.added_at
		FROM service_incident_stakeholders st LEFT JOIN users u ON u.id=st.user_id JOIN service_requests r ON r.issue_id=st.request_issue_id
		WHERE r.workspace_id=$1 AND st.request_issue_id=$2 ORDER BY st.added_at,st.id`, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stakeholders := []models.ServiceIncidentStakeholder{}
	for rows.Next() {
		var stakeholder models.ServiceIncidentStakeholder
		if err := rows.Scan(&stakeholder.ID, &stakeholder.UserID, &stakeholder.Name, &stakeholder.Email, &stakeholder.AddedAt); err != nil {
			return nil, err
		}
		stakeholder.AddedAt = stakeholder.AddedAt.UTC()
		stakeholders = append(stakeholders, stakeholder)
	}
	return stakeholders, rows.Err()
}

// AddServiceIncidentStakeholder adds a site member or an email address as a
// stakeholder of a major incident. Adding one twice changes nothing.
func (s *Store) AddServiceIncidentStakeholder(ctx context.Context, workspaceID, actorID, issueID, userID, email string) error {
	email = strings.TrimSpace(email)
	if (userID == "") == (email == "") {
		return fmt.Errorf("%w: choose a site member or enter an email address", ErrServiceIncidentValidation)
	}
	if email != "" {
		address, err := mail.ParseAddress(email)
		if err != nil || address.Address != email || len(email) > 320 {
			return fmt.Errorf("%w: enter a valid email address", ErrServiceIncidentValidation)
		}
	} else if _, err := s.MemberByID(ctx, workspaceID, userID); err != nil {
		return fmt.Errorf("%w: stakeholders chosen by name must be active members of this site", ErrServiceIncidentValidation)
	}
	if err := s.agentOfMajorIncident(ctx, workspaceID, actorID, issueID); err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `INSERT INTO service_incident_stakeholders(request_issue_id,user_id,email,added_by) VALUES($1,NULLIF($2,''),NULLIF($3,''),$4) ON CONFLICT DO NOTHING`, issueID, userID, email, actorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if err := auditServiceIncident(ctx, tx, workspaceID, actorID, issueID, "service_major_incident_stakeholder_added", map[string]any{"userId": userID, "email": email}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemoveServiceIncidentStakeholder removes a stakeholder from a request.
func (s *Store) RemoveServiceIncidentStakeholder(ctx context.Context, workspaceID, actorID, issueID, stakeholderID string) error {
	canManage, err := s.CanManageServiceRequest(ctx, workspaceID, actorID, issueID)
	if err != nil {
		return err
	}
	if !canManage {
		return ErrProjectPermission
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `DELETE FROM service_incident_stakeholders WHERE id::text=$1 AND request_issue_id=$2`, stakeholderID, issueID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if err := auditServiceIncident(ctx, tx, workspaceID, actorID, issueID, "service_major_incident_stakeholder_removed", map[string]any{"stakeholderId": stakeholderID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ServiceIncidentStakeholderEmails lists each distinct address a stakeholder
// update goes to: active member stakeholders' emails and external addresses.
func (s *Store) ServiceIncidentStakeholderEmails(ctx context.Context, issueID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT ON (lower(address)) address FROM (
		SELECT COALESCE(u.email,st.email) AS address FROM service_incident_stakeholders st LEFT JOIN users u ON u.id=st.user_id
		WHERE st.request_issue_id=$1 AND (st.user_id IS NULL OR u.active)) addresses
		WHERE address IS NOT NULL AND address<>'' ORDER BY lower(address)`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	emails := []string{}
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			return nil, err
		}
		emails = append(emails, email)
	}
	return emails, rows.Err()
}
