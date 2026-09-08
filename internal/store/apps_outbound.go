package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

func enqueueAppLifecycle(ctx context.Context, tx pgx.Tx, installationID, appKey, version, event string) error {
	if event == "" {
		return nil
	}
	var path, workspaceID, principalID, storedVersion string
	err := tx.QueryRow(ctx, `SELECT c.path,i.workspace_id,i.principal_id,i.version FROM app_lifecycle_callbacks c JOIN app_installations i ON i.id=c.installation_id WHERE c.installation_id=$1 AND c.event=$2`, installationID, event).Scan(&path, &workspaceID, &principalID, &storedVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if version == "" {
		version = storedVersion
	}
	payload, err := json.Marshal(map[string]any{"event": event, "key": appKey, "clientKey": workspaceID, "principalId": principalID, "version": version})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO app_outbound_deliveries(installation_id,kind,event,path,payload,dedupe_key) VALUES($1,'lifecycle',$2,$3,$4,$2 || ':' || gen_random_uuid()::text)`, installationID, event, path, payload)
	return err
}

func deleteAppOutboundConfig(ctx context.Context, tx pgx.Tx, installationID string) error {
	for _, table := range []string{"app_lifecycle_callbacks", "app_webhook_modules", "app_scheduled_triggers"} {
		if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE installation_id=$1`, installationID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ActiveAppWebhooks(ctx context.Context, workspaceID string) ([]models.AppWebhook, error) {
	rows, err := s.Pool.Query(ctx, `SELECT w.id::text,w.installation_id,i.app_key,w.module_key,w.path,w.events,w.jql,w.last_seq FROM app_webhook_modules w JOIN app_installations i ON i.id=w.installation_id WHERE i.workspace_id=$1 AND i.status='active' ORDER BY w.id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.AppWebhook{}
	for rows.Next() {
		var value models.AppWebhook
		if err := rows.Scan(&value.ID, &value.InstallationID, &value.AppKey, &value.Key, &value.Path, &value.Events, &value.JQL, &value.LastSeq); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (s *Store) AdvanceAppWebhook(ctx context.Context, webhook models.AppWebhook, seq int64, event string, payload json.RawMessage, deliver bool) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE app_webhook_modules SET last_seq=$3 WHERE id::text=$1 AND last_seq=$2`, webhook.ID, webhook.LastSeq, seq)
	if err != nil || tag.RowsAffected() == 0 {
		return false, err
	}
	if deliver {
		_, err = tx.Exec(ctx, `INSERT INTO app_outbound_deliveries(installation_id,kind,module_key,event,path,payload,dedupe_key) VALUES($1,'webhook',$2,$3,$4,$5,$2 || ':' || ($6::bigint)::text) ON CONFLICT DO NOTHING`, webhook.InstallationID, webhook.Key, event, webhook.Path, payload, seq)
		if err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}

func (s *Store) EnqueueDueAppSchedule(ctx context.Context, workspaceID string, now time.Time) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id, installationID, moduleKey, path, intervalName string
	var scheduledFor time.Time
	err = tx.QueryRow(ctx, `SELECT t.id::text,t.installation_id,t.module_key,t.path,t.interval_name,t.next_run_at FROM app_scheduled_triggers t JOIN app_installations i ON i.id=t.installation_id WHERE i.workspace_id=$1 AND i.status='active' AND t.next_run_at<=$2 ORDER BY t.next_run_at,t.id FOR UPDATE OF t SKIP LOCKED LIMIT 1`, workspaceID, now).Scan(&id, &installationID, &moduleKey, &path, &intervalName, &scheduledFor)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	payload, err := json.Marshal(map[string]any{"context": map[string]any{"cloudId": workspaceID, "moduleKey": moduleKey}, "scheduledFor": scheduledFor.UTC().Format(time.RFC3339)})
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO app_outbound_deliveries(installation_id,kind,module_key,event,path,payload,dedupe_key) VALUES($1,'scheduled',$2,'scheduled_trigger',$3,$4,$2 || ':' || $5) ON CONFLICT DO NOTHING`, installationID, moduleKey, path, payload, scheduledFor.UTC().Format(time.RFC3339Nano)); err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE app_scheduled_triggers SET next_run_at=$2::timestamptz + make_interval(secs=>interval_seconds) WHERE id::text=$1`, id, now); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *Store) ClaimAppOutboundDelivery(ctx context.Context, workspaceID string, now time.Time) (*models.AppOutboundDelivery, error) {
	value := &models.AppOutboundDelivery{}
	err := s.Pool.QueryRow(ctx, `WITH candidate AS (
SELECT d.id FROM app_outbound_deliveries d JOIN app_installations i ON i.id=d.installation_id
WHERE i.workspace_id=$1 AND d.available_at<=$2 AND d.attempts<5
AND (d.state='pending' OR (d.state='failed' AND d.kind<>'scheduled') OR (d.state='delivering' AND d.claimed_at<$2-interval '1 minute'))
ORDER BY d.available_at,d.created_at,d.id FOR UPDATE OF d SKIP LOCKED LIMIT 1)
UPDATE app_outbound_deliveries d SET state='delivering',attempts=d.attempts+1,claimed_at=$2 FROM candidate c,app_installations i
WHERE d.id=c.id AND i.id=d.installation_id
RETURNING d.id::text,d.installation_id,i.app_key,i.base_url,i.descriptor_format,i.secret_ciphertext,d.kind,d.module_key,d.event,d.path,d.payload,d.state,d.attempts,d.available_at,d.created_at`, workspaceID, now).Scan(&value.ID, &value.InstallationID, &value.AppKey, &value.BaseURL, &value.Format, &value.SecretCiphertext, &value.Kind, &value.ModuleKey, &value.Event, &value.Path, &value.Payload, &value.State, &value.Attempts, &value.AvailableAt, &value.CreatedAt)
	return value, err
}

func (s *Store) CompleteAppOutboundDelivery(ctx context.Context, id string, responseCode int, deliveryErr error, now time.Time) error {
	if deliveryErr == nil {
		_, err := s.Pool.Exec(ctx, `UPDATE app_outbound_deliveries SET state='delivered',response_code=$2,last_error='',completed_at=$3 WHERE id::text=$1 AND state='delivering'`, id, responseCode, now)
		return err
	}
	message := deliveryErr.Error()
	if len(message) > 2000 {
		message = message[:2000]
	}
	_, err := s.Pool.Exec(ctx, `UPDATE app_outbound_deliveries SET state='failed',response_code=NULLIF($2,0),last_error=$3,available_at=$4::timestamptz + make_interval(secs=>LEAST(300,1 << LEAST(attempts,8))),completed_at=CASE WHEN kind='scheduled' OR attempts>=5 THEN $4::timestamptz ELSE NULL END WHERE id::text=$1 AND state='delivering'`, id, responseCode, message, now)
	return err
}

func (s *Store) AppOutboundDeliveries(ctx context.Context, installationID string, limit int) ([]models.AppOutboundDelivery, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("delivery limit must be between 1 and 100")
	}
	rows, err := s.Pool.Query(ctx, `SELECT id::text,installation_id,kind,module_key,event,path,payload,state,attempts,COALESCE(response_code,0),last_error,available_at,created_at FROM app_outbound_deliveries WHERE installation_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2`, installationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []models.AppOutboundDelivery{}
	for rows.Next() {
		var value models.AppOutboundDelivery
		if err := rows.Scan(&value.ID, &value.InstallationID, &value.Kind, &value.ModuleKey, &value.Event, &value.Path, &value.Payload, &value.State, &value.Attempts, &value.ResponseCode, &value.LastError, &value.AvailableAt, &value.CreatedAt); err != nil {
			return nil, err
		}
		value.AvailableAt = value.AvailableAt.UTC()
		value.CreatedAt = value.CreatedAt.UTC()
		values = append(values, value)
	}
	return values, rows.Err()
}
