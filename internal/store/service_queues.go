package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5/pgconn"
)

func serviceQueueWriteError(err error) error {
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		return fmt.Errorf("a queue with this name already exists")
	}
	return err
}

func (s *Store) CreateServiceQueue(ctx context.Context, workspaceID, actorID, serviceDeskID, name, query string) (*models.ServiceQueue, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queue := &models.ServiceQueue{ServiceDeskID: serviceDeskID, Name: name, JQL: query, Kind: "custom"}
	err = tx.QueryRow(ctx, `
		INSERT INTO service_queues(service_desk_id,name,jql,kind,position)
		SELECT sd.id,$3,$4,'custom',COALESCE((SELECT max(position)+1 FROM service_queues WHERE service_desk_id=sd.id),0)
		FROM service_desks sd WHERE sd.workspace_id=$1 AND sd.id=$2
		RETURNING id,position,fields`, workspaceID, serviceDeskID, name, query).Scan(&queue.ID, &queue.Position, &queue.Fields)
	if err != nil {
		return nil, serviceQueueWriteError(err)
	}
	detail, err := json.Marshal(map[string]any{"name": name, "jql": query, "serviceDeskId": serviceDeskID})
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,'service.queue.created','service_queue',$3,$4::jsonb FROM sites WHERE workspace_id=$1`, workspaceID, actorID, queue.ID, detail); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return queue, nil
}

func (s *Store) UpdateServiceQueue(ctx context.Context, workspaceID, actorID, serviceDeskID, queueID, name, query string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return serviceQueueWriteError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		UPDATE service_queues q SET name=$5,jql=$6
		FROM service_desks sd WHERE sd.id=q.service_desk_id AND sd.workspace_id=$1 AND sd.id=$2 AND q.id=$3 AND q.kind=$4`, workspaceID, serviceDeskID, queueID, "custom", name, query)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("custom queue does not exist")
	}
	detail, err := json.Marshal(map[string]any{"name": name, "jql": query, "serviceDeskId": serviceDeskID})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,'service.queue.updated','service_queue',$3,$4::jsonb FROM sites WHERE workspace_id=$1`, workspaceID, actorID, queueID, detail); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteServiceQueue(ctx context.Context, workspaceID, actorID, serviceDeskID, queueID string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		DELETE FROM service_queues q USING service_desks sd
		WHERE sd.id=q.service_desk_id AND sd.workspace_id=$1 AND sd.id=$2 AND q.id=$3 AND q.kind='custom'`, workspaceID, serviceDeskID, queueID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("custom queue does not exist")
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO organization_audit_events(organization_id,actor_id,action,target_type,target_id,detail)
		SELECT organization_id,$2,'service.queue.deleted','service_queue',$3,jsonb_build_object('serviceDeskId',$4::text) FROM sites WHERE workspace_id=$1`, workspaceID, actorID, queueID, serviceDeskID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
