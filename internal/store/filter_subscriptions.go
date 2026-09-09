package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

func nextFilterSubscriptionRun(expression string, after time.Time) (time.Time, error) {
	parts := strings.Fields(expression)
	if len(parts) != 5 || parts[2] != "*" || parts[3] != "*" || parts[0] != "0" || (parts[4] != "*" && len(parts[4]) != 1) {
		return time.Time{}, fmt.Errorf("%w: schedule must be hourly UTC as daily or weekly", ErrFilterValidation)
	}
	hour, err := strconv.Atoi(parts[1])
	if err != nil || hour < 0 || hour > 23 {
		return time.Time{}, fmt.Errorf("%w: schedule hour must be between 0 and 23", ErrFilterValidation)
	}
	after = after.UTC()
	candidate := time.Date(after.Year(), after.Month(), after.Day(), hour, 0, 0, 0, time.UTC)
	if parts[4] == "*" {
		if !candidate.After(after) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate, nil
	}
	weekday, err := strconv.Atoi(parts[4])
	if err != nil || weekday < 0 || weekday > 6 {
		return time.Time{}, fmt.Errorf("%w: schedule weekday must be between 0 and 6", ErrFilterValidation)
	}
	days := (weekday - int(candidate.Weekday()) + 7) % 7
	candidate = candidate.AddDate(0, 0, days)
	if !candidate.After(after) {
		candidate = candidate.AddDate(0, 0, 7)
	}
	return candidate, nil
}

func (s *Store) SaveFilterSubscription(ctx context.Context, workspaceID, userID, filterID, expression string, recipients []string) (*models.FilterSubscription, error) {
	filter, err := s.FilterByID(ctx, workspaceID, userID, filterID)
	if err != nil {
		return nil, err
	}
	if filter.OwnerID != userID {
		return nil, ErrFilterPermission
	}
	next, err := nextFilterSubscriptionRun(expression, time.Now())
	if err != nil {
		return nil, err
	}
	if len(recipients) > 50 {
		return nil, fmt.Errorf("%w: at most 50 recipients are supported", ErrFilterValidation)
	}
	seen := map[string]bool{}
	clean := make([]string, 0, len(recipients))
	for _, id := range recipients {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		clean = append(clean, id)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var valid int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM memberships m JOIN users u ON u.id=m.user_id AND u.active WHERE m.workspace_id=$1 AND m.user_id=ANY($2::text[])`, workspaceID, clean).Scan(&valid); err != nil {
		return nil, err
	}
	if valid != len(clean) {
		return nil, fmt.Errorf("%w: every recipient must be an active workspace member", ErrFilterValidation)
	}
	encoded, _ := json.Marshal(clean)
	subscription := &models.FilterSubscription{FilterID: filterID, UserID: userID, CronExpression: expression, Recipients: clean, Enabled: true, NextRunAt: next.Format(time.RFC3339)}
	err = tx.QueryRow(ctx, `
		INSERT INTO filter_subscriptions(filter_id,user_id,cron_expression,recipients,enabled,next_run_at)
		VALUES($1,$2,$3,$4,true,$5)
		ON CONFLICT(filter_id,user_id,cron_expression) DO UPDATE SET recipients=EXCLUDED.recipients,enabled=true,next_run_at=EXCLUDED.next_run_at,last_error=''
		RETURNING id`, filterID, userID, expression, encoded, next).Scan(&subscription.ID)
	if err != nil {
		return nil, err
	}
	if err = filterAudit(ctx, tx, workspaceID, userID, "filter.subscription-saved", filterID, expression); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return subscription, nil
}

func (s *Store) DeleteFilterSubscription(ctx context.Context, workspaceID, userID, filterID string, subscriptionID int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `DELETE FROM filter_subscriptions fs USING filters f WHERE fs.id=$1 AND fs.filter_id=$2 AND fs.user_id=$3 AND f.id=fs.filter_id AND f.workspace_id=$4 AND f.owner_id=$3`, subscriptionID, filterID, userID, workspaceID)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrFilterPermission
	}
	if err = filterAudit(ctx, tx, workspaceID, userID, "filter.subscription-deleted", filterID, fmt.Sprint(subscriptionID)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) loadFilterSubscriptions(ctx context.Context, filters []*models.Filter, userID string) error {
	if len(filters) == 0 {
		return nil
	}
	ids, byID := make([]string, 0, len(filters)), map[string]*models.Filter{}
	for _, filter := range filters {
		ids, byID[filter.ID] = append(ids, filter.ID), filter
		filter.Subscriptions = []models.FilterSubscription{}
	}
	rows, err := s.Pool.Query(ctx, `SELECT id,filter_id,user_id,cron_expression,recipients,enabled,COALESCE(to_char(next_run_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),COALESCE(to_char(last_run_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),last_error,last_result_count FROM filter_subscriptions WHERE filter_id=ANY($1::text[]) AND user_id=$2 ORDER BY id`, ids, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var subscription models.FilterSubscription
		var raw []byte
		if err := rows.Scan(&subscription.ID, &subscription.FilterID, &subscription.UserID, &subscription.CronExpression, &raw, &subscription.Enabled, &subscription.NextRunAt, &subscription.LastRunAt, &subscription.LastError, &subscription.LastResultCount); err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &subscription.Recipients); err != nil {
			return err
		}
		if filter := byID[subscription.FilterID]; filter != nil {
			filter.Subscriptions = append(filter.Subscriptions, subscription)
		}
	}
	return rows.Err()
}

type FilterSubscriptionRunner struct {
	Store   *Store
	BaseURL string
	Now     func() time.Time
}

type claimedFilterSubscription struct {
	RunID, SubscriptionID                          int64
	WorkspaceID, FilterID, FilterName, JQL, UserID string
	Recipients                                     []string
	ScheduledFor                                   time.Time
}

func (r *FilterSubscriptionRunner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *FilterSubscriptionRunner) Run(ctx context.Context, workspaceID string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.DrainOnce(ctx, workspaceID); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("filter subscription runner: %v", err)
			}
		}
	}
}

func (r *FilterSubscriptionRunner) DrainOnce(ctx context.Context, workspaceID string) error {
	if r.Store == nil {
		return errors.New("filter subscription runner is not configured")
	}
	if err := r.enqueueDue(ctx, workspaceID); err != nil {
		return err
	}
	run, err := r.claim(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	count, runErr := r.execute(ctx, run)
	return r.finish(ctx, run, count, runErr)
}

func (r *FilterSubscriptionRunner) enqueueDue(ctx context.Context, workspaceID string) error {
	tx, err := r.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id int64
	var expression string
	var scheduled time.Time
	err = tx.QueryRow(ctx, `SELECT fs.id,fs.cron_expression,fs.next_run_at FROM filter_subscriptions fs JOIN filters f ON f.id=fs.filter_id WHERE f.workspace_id=$1 AND fs.enabled AND fs.next_run_at<=$2 ORDER BY fs.next_run_at,fs.id FOR UPDATE OF fs SKIP LOCKED LIMIT 1`, workspaceID, r.now()).Scan(&id, &expression, &scheduled)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	next, err := nextFilterSubscriptionRun(expression, scheduled)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO filter_subscription_runs(subscription_id,scheduled_for) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, scheduled); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE filter_subscriptions SET next_run_at=$2 WHERE id=$1`, id, next); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *FilterSubscriptionRunner) claim(ctx context.Context, workspaceID string) (*claimedFilterSubscription, error) {
	run := &claimedFilterSubscription{}
	var raw []byte
	err := r.Store.Pool.QueryRow(ctx, `WITH candidate AS (SELECT fr.id FROM filter_subscription_runs fr JOIN filter_subscriptions fs ON fs.id=fr.subscription_id JOIN filters f ON f.id=fs.filter_id WHERE f.workspace_id=$1 AND fs.enabled AND fr.attempts<8 AND ((fr.state IN ('PENDING','FAILED') AND fr.available_at<=$2) OR (fr.state='RUNNING' AND fr.claimed_at<$2-interval '5 minutes')) ORDER BY fr.scheduled_for,fr.id FOR UPDATE OF fr SKIP LOCKED LIMIT 1), claimed AS (UPDATE filter_subscription_runs fr SET state='RUNNING',attempts=attempts+1,claimed_at=$2 FROM candidate WHERE fr.id=candidate.id RETURNING fr.*) SELECT c.id,c.subscription_id,c.scheduled_for,f.workspace_id,f.id,f.name,f.jql,fs.user_id,fs.recipients FROM claimed c JOIN filter_subscriptions fs ON fs.id=c.subscription_id JOIN filters f ON f.id=fs.filter_id`, workspaceID, r.now()).Scan(&run.RunID, &run.SubscriptionID, &run.ScheduledFor, &run.WorkspaceID, &run.FilterID, &run.FilterName, &run.JQL, &run.UserID, &raw)
	if err == nil {
		err = json.Unmarshal(raw, &run.Recipients)
	}
	return run, err
}

func (r *FilterSubscriptionRunner) execute(ctx context.Context, run *claimedFilterSubscription) (int, error) {
	query, err := jql.Parse(run.JQL)
	if err != nil {
		return 0, err
	}
	if err = r.Store.ExpandAppJQL(ctx, run.WorkspaceID, query); err != nil {
		return 0, err
	}
	fields, err := r.Store.CustomFieldsForWorkspace(ctx, run.WorkspaceID)
	if err != nil {
		return 0, err
	}
	compiled := jql.CompileAt(query, run.UserID, jql.WithCustomFields(jql.DefaultResolver(), fields), 2)
	if compiled.Err != nil {
		return 0, compiled.Err
	}
	issues, total, err := r.Store.Search(ctx, run.WorkspaceID, run.UserID, compiled, 100, 0)
	if err != nil {
		return 0, err
	}
	recipients := run.Recipients
	if len(recipients) == 0 {
		recipients = []string{run.UserID}
	}
	lines := []string{fmt.Sprintf("%d work items match %s.", total, run.FilterName), ""}
	for _, issue := range issues {
		lines = append(lines, issue.Key+"  "+issue.Summary+"  "+strings.TrimRight(r.BaseURL, "/")+"/browse/"+issue.Key)
	}
	if total > len(issues) {
		lines = append(lines, "", fmt.Sprintf("Showing the first %d results.", len(issues)))
	}
	subject := strings.NewReplacer("\r", " ", "\n", " ").Replace("ZZIRA filter: " + run.FilterName)
	for _, id := range recipients {
		var email string
		err = r.Store.Pool.QueryRow(ctx, `SELECT u.email FROM users u JOIN memberships m ON m.user_id=u.id WHERE m.workspace_id=$1 AND u.id=$2 AND u.active`, run.WorkspaceID, id).Scan(&email)
		if err != nil {
			return total, fmt.Errorf("recipient %s is unavailable", id)
		}
		dedupe := fmt.Sprintf("filter-subscription:%d:%s", run.RunID, id)
		if _, err = r.Store.Pool.Exec(ctx, `INSERT INTO email_outbox(workspace_id,recipient,subject,body,dedupe_key) VALUES($1,$2,$3,$4,$5) ON CONFLICT(dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`, run.WorkspaceID, email, subject, strings.Join(lines, "\n"), dedupe); err != nil {
			return total, err
		}
	}
	return total, nil
}

func (r *FilterSubscriptionRunner) finish(ctx context.Context, run *claimedFilterSubscription, count int, runErr error) error {
	message, state := "", "SUCCEEDED"
	if runErr != nil {
		message, state = runErr.Error(), "FAILED"
		if len(message) > 2000 {
			message = message[:2000]
		}
	}
	_, err := r.Store.Pool.Exec(ctx, `WITH finished AS (UPDATE filter_subscription_runs SET state=$2,completed_at=$3::timestamptz,result_count=$4,error=$5,available_at=CASE WHEN $2='FAILED' THEN $3::timestamptz+make_interval(secs=>LEAST(3600,power(2,attempts)::int*5)) ELSE available_at END WHERE id=$1 AND state='RUNNING' RETURNING subscription_id) UPDATE filter_subscriptions fs SET last_run_at=$3::timestamptz,last_result_count=$4,last_error=$5 FROM finished WHERE fs.id=finished.subscription_id`, run.RunID, state, r.now(), count, message)
	return err
}
