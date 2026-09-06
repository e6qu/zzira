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

type ServiceSLARunner struct {
	Store *Store
	Now   func() time.Time
	Logf  func(string, ...any)
}

func (r *ServiceSLARunner) Run(ctx context.Context, workspaceID string) {
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
				logger("service SLA runner: %v", err)
			}
		}
	}
}

func (r *ServiceSLARunner) DrainOnce(ctx context.Context, workspaceID string) error {
	if r.Store == nil {
		return fmt.Errorf("service SLA runner is not configured")
	}
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	requestIDs, err := r.Store.OpenServiceSLARequestIDs(ctx, workspaceID)
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
	for _, requestID := range requestIDs {
		request, err := r.Store.ServiceRequest(ctx, workspaceID, actorID, requestID, true)
		if err != nil {
			return err
		}
		recipients, err := r.Store.ServiceSLARecipients(ctx, workspaceID, request.ServiceDesk.ID, request.Issue.Assignee, actorID)
		if err != nil {
			return err
		}
		slas, err := r.Store.ServiceSLAs(ctx, workspaceID, requestID, now)
		if err != nil {
			return err
		}
		for _, sla := range slas {
			if sla.OngoingCycle == nil || sla.OngoingCycle.RemainingMillis > sla.GoalMillis/4 {
				continue
			}
			stage := "warning"
			message := sla.Name + " is approaching its goal on " + request.Issue.Key
			if sla.OngoingCycle.Breached {
				stage = "breached"
				message = sla.Name + " breached its goal on " + request.Issue.Key
			}
			for _, recipientID := range recipients {
				if _, err := r.Store.RecordServiceSLAEscalation(ctx, workspaceID, actorID, actor.DisplayName, recipientID, request.Issue.ID, sla.OngoingCycle.ID, stage, message); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Store) OpenServiceSLARequestIDs(ctx context.Context, workspaceID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT sr.issue_id FROM service_requests sr
		JOIN issues i ON i.id=sr.issue_id JOIN statuses st ON st.id=i.status_id
		JOIN service_sla_cycles c ON c.request_issue_id=sr.issue_id AND c.stopped_at IS NULL
		WHERE sr.workspace_id=$1 AND st.category<>'done' ORDER BY sr.issue_id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) ServiceSLARecipients(ctx context.Context, workspaceID, serviceDeskID string, assignee *models.User, adminID string) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT DISTINCT a.user_id FROM service_desk_agents a
		JOIN service_desks sd ON sd.id=a.service_desk_id
		WHERE sd.workspace_id=$1 AND sd.id=$2`, workspaceID, serviceDeskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := map[string]bool{adminID: true}
	if assignee != nil {
		candidates[assignee.ID] = true
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		candidates[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	ids := make([]string, 0, len(candidates))
	for id := range candidates {
		active, err := s.IsMember(ctx, workspaceID, id)
		if err != nil {
			return nil, err
		}
		if active {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (s *Store) RecordServiceSLAEscalation(ctx context.Context, workspaceID, actorID, actorName, targetUserID, issueID, cycleID, stage, message string) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var escalationID string
	err = tx.QueryRow(ctx, `
		INSERT INTO service_sla_escalations(cycle_id,user_id,stage) VALUES($1,$2,$3)
		ON CONFLICT(cycle_id,user_id,stage) DO NOTHING RETURNING id`, cycleID, targetUserID, stage).Scan(&escalationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, tx.Commit(ctx)
	}
	if err != nil {
		return false, err
	}
	notification := models.Notification{
		ID: "ntf_sla_" + escalationID, WorkspaceID: workspaceID, TargetUser: targetUserID,
		ActorID: actorID, ActorName: actorName, Kind: "service_sla_" + stage,
		EntityType: models.EntityIssue, EntityID: issueID, Message: message,
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO notifications(id,workspace_id,user_id,actor_id,kind,entity_type,entity_id,message)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"')`,
		notification.ID, workspaceID, targetUserID, actorID, notification.Kind, notification.EntityType, issueID, message).Scan(&notification.Created); err != nil {
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
	if err := appendAction(ctx, tx, &models.Action{
		WorkspaceID: workspaceID, Seq: sequence, EntityType: models.EntityNotification,
		EntityID: notification.ID, Op: models.OpUpsert, SchemaV: models.SchemaVersion,
		Payload: payload, ActorID: actorID,
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
