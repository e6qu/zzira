package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

type ServiceIncidentEscalation struct {
	IssueID, IssueKey, StepID, TargetUserID, TargetUserName string
	Generation, DelayMinutes                                int
}

type ServiceIncidentEscalationRunner struct {
	Store *Store
	Now   func() time.Time
	Logf  func(string, ...any)
}

func (r *ServiceIncidentEscalationRunner) Run(ctx context.Context, workspaceID string) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.DrainOnce(ctx, workspaceID); err != nil && !errors.Is(err, context.Canceled) {
				logger := r.Logf
				if logger == nil {
					logger = log.Printf
				}
				logger("service incident escalation runner: %v", err)
			}
		}
	}
}

func (r *ServiceIncidentEscalationRunner) DrainOnce(ctx context.Context, workspaceID string) error {
	if r.Store == nil {
		return fmt.Errorf("service incident escalation runner is not configured")
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	due, err := r.Store.DueServiceIncidentEscalations(ctx, workspaceID, now)
	if err != nil {
		return err
	}
	actorID, err := r.Store.FirstAdminID(ctx, workspaceID)
	if err != nil {
		return err
	}
	actor, err := r.Store.UserByID(ctx, actorID)
	if err != nil {
		return err
	}
	for _, escalation := range due {
		message := fmt.Sprintf("Major incident %s reached its %d-minute escalation", escalation.IssueKey, escalation.DelayMinutes)
		if _, err := r.Store.RecordServiceIncidentEscalation(ctx, workspaceID, actorID, actor.DisplayName, escalation, message); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DueServiceIncidentEscalations(ctx context.Context, workspaceID string, now time.Time) ([]ServiceIncidentEscalation, error) {
	rows, err := s.Pool.Query(ctx, `SELECT r.issue_id,i.key,o.major_incident_generation,p.id,p.delay_minutes,p.target_user_id,u.display_name
		FROM service_request_operations o
		JOIN service_requests r ON r.issue_id=o.request_issue_id
		JOIN issues i ON i.id=r.issue_id JOIN statuses st ON st.id=i.status_id
		JOIN service_escalation_steps p ON p.service_desk_id=r.service_desk_id
		JOIN users u ON u.id=p.target_user_id AND u.active
		JOIN memberships m ON m.workspace_id=r.workspace_id AND m.user_id=u.id
		WHERE r.workspace_id=$1 AND o.kind='incident' AND o.major_incident AND o.major_incident_declared_at IS NOT NULL
		  AND st.category<>'done' AND o.major_incident_declared_at + make_interval(mins => p.delay_minutes) <= $2
		  AND EXISTS(SELECT 1 FROM sites si JOIN directories d ON d.organization_id=si.organization_id AND d.active JOIN directory_users du ON du.directory_id=d.id AND du.user_id=u.id AND du.active WHERE si.workspace_id=r.workspace_id)
		  AND NOT EXISTS(SELECT 1 FROM service_incident_escalations e WHERE e.request_issue_id=r.issue_id AND e.incident_generation=o.major_incident_generation AND e.step_id=p.id)
		ORDER BY o.major_incident_declared_at,p.position,p.id::bigint`, workspaceID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []ServiceIncidentEscalation{}
	for rows.Next() {
		var value ServiceIncidentEscalation
		if err := rows.Scan(&value.IssueID, &value.IssueKey, &value.Generation, &value.StepID, &value.DelayMinutes, &value.TargetUserID, &value.TargetUserName); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) RecordServiceIncidentEscalation(ctx context.Context, workspaceID, actorID, actorName string, value ServiceIncidentEscalation, message string) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var escalationID string
	err = tx.QueryRow(ctx, `INSERT INTO service_incident_escalations(request_issue_id,incident_generation,step_id,target_user_id,delay_minutes)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(request_issue_id,incident_generation,step_id) DO NOTHING RETURNING id`, value.IssueID, value.Generation, value.StepID, value.TargetUserID, value.DelayMinutes).Scan(&escalationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	notification := models.Notification{ID: "ntf_incident_escalation_" + escalationID, WorkspaceID: workspaceID, TargetUser: value.TargetUserID, ActorID: actorID, ActorName: actorName, Kind: "service_incident_escalation", EntityType: models.EntityServiceRequest, EntityID: value.IssueKey, Message: message}
	if err := tx.QueryRow(ctx, `INSERT INTO notifications(id,workspace_id,user_id,actor_id,kind,entity_type,entity_id,message) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`, notification.ID, workspaceID, value.TargetUserID, actorID, notification.Kind, notification.EntityType, notification.EntityID, message).Scan(&notification.Created); err != nil {
		return false, err
	}
	sequence, err := nextSeq(ctx, tx, workspaceID)
	if err != nil {
		return false, err
	}
	payload, err := json.Marshal(models.NotificationPayload{Notification: notification})
	if err != nil {
		return false, err
	}
	if err := appendAction(ctx, tx, &models.Action{WorkspaceID: workspaceID, Seq: sequence, EntityType: models.EntityNotification, EntityID: notification.ID, Op: models.OpUpsert, SchemaV: models.SchemaVersion, Payload: payload, ActorID: actorID}); err != nil {
		return false, err
	}
	detail, _ := json.Marshal(map[string]any{"stepId": value.StepID, "targetUserId": value.TargetUserID, "delayMinutes": value.DelayMinutes, "generation": value.Generation})
	if _, err := tx.Exec(ctx, `INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail) SELECT organization_id,$2,'service_incident_escalated','service_request',$3,$4::jsonb FROM sites WHERE workspace_id=$1`, workspaceID, actorID, value.IssueID, detail); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ServiceIncidentEscalationStatus(ctx context.Context, workspaceID, actorID, issueID string) ([]models.ServiceEscalationStep, error) {
	canManage, err := s.CanManageServiceRequest(ctx, workspaceID, actorID, issueID)
	if err != nil {
		return nil, err
	}
	if !canManage {
		return nil, ErrProjectPermission
	}
	rows, err := s.Pool.Query(ctx, `SELECT p.id,p.service_desk_id,p.position,p.delay_minutes,p.target_user_id,u.display_name,e.created_at
		FROM service_requests r JOIN service_request_operations o ON o.request_issue_id=r.issue_id
		JOIN service_escalation_steps p ON p.service_desk_id=r.service_desk_id JOIN users u ON u.id=p.target_user_id
		LEFT JOIN service_incident_escalations e ON e.request_issue_id=r.issue_id AND e.incident_generation=o.major_incident_generation AND e.step_id=p.id
		WHERE r.workspace_id=$1 AND r.issue_id=$2 AND o.kind='incident' ORDER BY p.position,p.id::bigint`, workspaceID, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.ServiceEscalationStep{}
	for rows.Next() {
		var value models.ServiceEscalationStep
		if err := rows.Scan(&value.ID, &value.ServiceDeskID, &value.Position, &value.DelayMinutes, &value.TargetUserID, &value.TargetUserName, &value.TriggeredAt); err != nil {
			return nil, err
		}
		if value.TriggeredAt != nil {
			created := value.TriggeredAt.UTC()
			value.TriggeredAt = &created
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
