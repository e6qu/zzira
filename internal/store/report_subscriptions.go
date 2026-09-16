package store

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// ErrReportSubscriptionValidation is a report email that cannot be scheduled.
var ErrReportSubscriptionValidation = errors.New("invalid report email")

// subscriptionRecipients de-duplicates the people a scheduled email goes to
// and names the problem when one of them cannot receive it.
func (s *Store) subscriptionRecipients(ctx context.Context, ws string, recipients []string) ([]string, string, error) {
	if len(recipients) > 50 {
		return nil, "at most 50 recipients are supported", nil
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
	for _, recipient := range clean {
		var active bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM memberships m JOIN users u ON u.id=m.user_id AND u.active WHERE m.workspace_id=$1 AND m.user_id=$2)`, ws, recipient).Scan(&active); err != nil {
			return nil, "", err
		}
		if !active {
			return nil, "every recipient must be an active member of this site", nil
		}
	}
	return clean, "", nil
}

// subscriptionRecipientEmail is the address of a recipient who is still an
// active member of the site, and the name to report a problem with.
func (s *Store) subscriptionRecipientEmail(ctx context.Context, ws, recipient string) (email, name string, active bool, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT u.email,u.display_name,u.active AND EXISTS(SELECT 1 FROM memberships m WHERE m.workspace_id=$1 AND m.user_id=u.id) FROM users u WHERE u.id=$2`, ws, recipient).Scan(&email, &name, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "A removed user", false, nil
	}
	return email, name, active, err
}

// SaveReportSubscription schedules an email of a report, identified by its
// path and choices. The caller checks that the subscriber and every recipient
// can open the report; leaving recipients empty sends it to the subscriber.
// reportSubscriptionHTML is the HTML alternative to a report email: the rows
// of the CSV it carries, drawn as a table, in the style the notification mail
// already uses.
func reportSubscriptionHTML(title, reportPath string, records [][]string) string {
	escape := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><body style="margin:0;padding:24px;background:#f7f8f9;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Arial,sans-serif;color:#172b4d"><table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:600px;margin:0 auto;background:#ffffff;border:1px solid #dfe1e6;border-radius:8px"><tr><td style="padding:24px">`)
	b.WriteString(`<h1 style="margin:0 0 16px;font-size:20px;line-height:1.3"><a href="` + escape(reportPath) + `" style="color:#172b4d;text-decoration:none">` + escape(title) + `</a></h1>`)
	if len(records) == 0 {
		b.WriteString(`<p style="margin:0;font-size:14px">This report has no data yet.</p>`)
	} else {
		b.WriteString(`<table role="presentation" cellpadding="6" cellspacing="0" style="border-collapse:collapse;font-size:13px">`)
		for index, record := range records {
			cell, style := "td", `style="border:1px solid #dfe1e6"`
			if index == 0 {
				cell, style = "th", `style="border:1px solid #dfe1e6;background:#f4f5f7;text-align:left"`
			}
			b.WriteString("<tr>")
			for _, value := range record {
				b.WriteString("<" + cell + " " + style + ">" + escape(value) + "</" + cell + ">")
			}
			b.WriteString("</tr>")
		}
		b.WriteString("</table>")
	}
	b.WriteString(`</td></tr></table><p style="max-width:600px;margin:16px auto 0;font-size:12px;color:#626f86">You receive this because you subscribed to this report. <a href="` + escape(reportPath) + `" style="color:#0c66e4">Open the report</a></p></body></html>`)
	return b.String()
}

func (s *Store) SaveReportSubscription(ctx context.Context, ws, user, report, expression, timezone string, recipients []string) (*models.ReportSubscription, error) {
	if report == "" || len(report) > 2000 {
		return nil, fmt.Errorf("%w: choose a report", ErrReportSubscriptionValidation)
	}
	next, err := nextSubscriptionRun(expression, timezone, time.Now())
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrReportSubscriptionValidation, strings.TrimPrefix(err.Error(), ErrFilterValidation.Error()+": "))
	}
	clean, problem, err := s.subscriptionRecipients(ctx, ws, recipients)
	if err != nil {
		return nil, err
	}
	if problem != "" {
		return nil, fmt.Errorf("%w: %s", ErrReportSubscriptionValidation, problem)
	}
	encoded, _ := json.Marshal(clean)
	subscription := &models.ReportSubscription{Report: report, UserID: user, CronExpression: expression, Timezone: timezone, Recipients: clean, Enabled: true, NextRunAt: next.Format(time.RFC3339)}
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO report_subscriptions(workspace_id,user_id,report,cron_expression,timezone,recipients,enabled,next_run_at)
		VALUES($1,$2,$3,$4,$5,$6,true,$7)
		ON CONFLICT(workspace_id,user_id,report,cron_expression) DO UPDATE SET timezone=EXCLUDED.timezone,recipients=EXCLUDED.recipients,enabled=true,next_run_at=EXCLUDED.next_run_at,last_error=''
		RETURNING id`, ws, user, report, expression, timezone, encoded, next).Scan(&subscription.ID)
	if err != nil {
		return nil, err
	}
	return subscription, nil
}

// DeleteReportSubscription removes one of the caller's emails of a report.
func (s *Store) DeleteReportSubscription(ctx context.Context, ws, user, report string, subscriptionID int64) error {
	result, err := s.Pool.Exec(ctx, `DELETE FROM report_subscriptions WHERE id=$1 AND workspace_id=$2 AND user_id=$3 AND report=$4`, subscriptionID, ws, user, report)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ReportSubscriptions lists the caller's emails of a report.
func (s *Store) ReportSubscriptions(ctx context.Context, ws, user, report string) ([]models.ReportSubscription, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,report,user_id,cron_expression,timezone,recipients,enabled,
		COALESCE(to_char(next_run_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),COALESCE(to_char(last_run_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),last_error,last_result_count
		FROM report_subscriptions WHERE workspace_id=$1 AND user_id=$2 AND report=$3 ORDER BY id`, ws, user, report)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.ReportSubscription{}
	for rows.Next() {
		var subscription models.ReportSubscription
		var raw []byte
		if err := rows.Scan(&subscription.ID, &subscription.Report, &subscription.UserID, &subscription.CronExpression, &subscription.Timezone, &raw, &subscription.Enabled, &subscription.NextRunAt, &subscription.LastRunAt, &subscription.LastError, &subscription.LastResultCount); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &subscription.Recipients); err != nil {
			return nil, err
		}
		out = append(out, subscription)
	}
	return out, rows.Err()
}

// ReportSubscriptionRunner delivers due report emails, one claimed run at a
// time, retrying a failed run with backoff. Render draws a report's CSV as a
// member sees it and names the report; the web layer provides it, so an email
// always carries the same data as the report's CSV download.
type ReportSubscriptionRunner struct {
	Store   *Store
	BaseURL string
	Now     func() time.Time
	Render  func(ctx context.Context, userID, report string) (title, data string, err error)
}

type claimedReportSubscription struct {
	RunID, SubscriptionID       int64
	WorkspaceID, Report, UserID string
	Recipients                  []string
}

func (r *ReportSubscriptionRunner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// Run delivers due report emails until ctx ends.
func (r *ReportSubscriptionRunner) Run(ctx context.Context, workspaceID string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.DrainOnce(ctx, workspaceID); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("report subscription runner: %v", err)
			}
		}
	}
}

// DrainOnce schedules the next due subscription and delivers one run.
func (r *ReportSubscriptionRunner) DrainOnce(ctx context.Context, workspaceID string) error {
	if r.Store == nil || r.Render == nil {
		return errors.New("report subscription runner is not configured")
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

func (r *ReportSubscriptionRunner) enqueueDue(ctx context.Context, workspaceID string) error {
	tx, err := r.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id int64
	var expression, timezone string
	var scheduled time.Time
	err = tx.QueryRow(ctx, `SELECT id,cron_expression,timezone,next_run_at FROM report_subscriptions
		WHERE workspace_id=$1 AND enabled AND next_run_at<=$2 ORDER BY next_run_at,id FOR UPDATE SKIP LOCKED LIMIT 1`, workspaceID, r.now()).Scan(&id, &expression, &timezone, &scheduled)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	next, err := nextSubscriptionRun(expression, timezone, scheduled)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO report_subscription_runs(subscription_id,scheduled_for,available_at) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, scheduled, r.now()); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE report_subscriptions SET next_run_at=$2 WHERE id=$1`, id, next); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *ReportSubscriptionRunner) claim(ctx context.Context, workspaceID string) (*claimedReportSubscription, error) {
	run := &claimedReportSubscription{}
	var raw []byte
	err := r.Store.Pool.QueryRow(ctx, `WITH candidate AS (
			SELECT rr.id FROM report_subscription_runs rr JOIN report_subscriptions rs ON rs.id=rr.subscription_id
			WHERE rs.workspace_id=$1 AND rs.enabled AND rr.attempts<8
			  AND ((rr.state IN ('PENDING','FAILED') AND rr.available_at<=$2) OR (rr.state='RUNNING' AND rr.claimed_at<$2-interval '5 minutes'))
			ORDER BY rr.scheduled_for,rr.id FOR UPDATE OF rr SKIP LOCKED LIMIT 1),
		claimed AS (UPDATE report_subscription_runs rr SET state='RUNNING',attempts=attempts+1,claimed_at=$2 FROM candidate WHERE rr.id=candidate.id RETURNING rr.*)
		SELECT c.id,c.subscription_id,rs.workspace_id,rs.report,rs.user_id,rs.recipients
		FROM claimed c JOIN report_subscriptions rs ON rs.id=c.subscription_id`, workspaceID, r.now()).
		Scan(&run.RunID, &run.SubscriptionID, &run.WorkspaceID, &run.Report, &run.UserID, &raw)
	if err == nil {
		err = json.Unmarshal(raw, &run.Recipients)
	}
	return run, err
}

// execute emails each recipient the report's data as they see it. A recipient
// who can no longer open the report fails the run, so it is retried and
// reported, while recipients already sent are not sent twice.
func (r *ReportSubscriptionRunner) execute(ctx context.Context, run *claimedReportSubscription) (int, error) {
	recipients := run.Recipients
	if len(recipients) == 0 {
		recipients = []string{run.UserID}
	}
	link := strings.TrimRight(r.BaseURL, "/") + run.Report
	rows := 0
	var failures []string
	for _, recipient := range recipients {
		email, name, active, err := r.Store.subscriptionRecipientEmail(ctx, run.WorkspaceID, recipient)
		if err != nil {
			return rows, err
		}
		if !active {
			failures = append(failures, name+" is no longer an active member of this site")
			continue
		}
		title, data, err := r.Render(ctx, recipient, run.Report)
		if err != nil {
			failures = append(failures, name+" can no longer open the report")
			continue
		}
		records, parseErr := csv.NewReader(strings.NewReader(data)).ReadAll()
		if parseErr != nil {
			records = nil
		}
		if len(records) > 0 {
			rows = len(records) - 1
		}
		subject := strings.NewReplacer("\r", " ", "\n", " ").Replace("ZZIRA report: " + title)
		body := strings.Join([]string{title, link, "", "The report's data, as in its CSV download:", "", data}, "\n")
		dedupe := fmt.Sprintf("report-subscription:%d:%s", run.RunID, recipient)
		if _, err = r.Store.Pool.Exec(ctx, `INSERT INTO email_outbox(workspace_id,recipient,subject,body,html_body,dedupe_key) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`, run.WorkspaceID, email, subject, body, reportSubscriptionHTML(title, run.Report, records), dedupe); err != nil {
			return rows, err
		}
	}
	if len(failures) > 0 {
		return rows, errors.New(strings.Join(failures, "; "))
	}
	return rows, nil
}

func (r *ReportSubscriptionRunner) finish(ctx context.Context, run *claimedReportSubscription, count int, runErr error) error {
	message, state := "", "SUCCEEDED"
	if runErr != nil {
		message, state = runErr.Error(), "FAILED"
		if len(message) > 2000 {
			message = message[:2000]
		}
	}
	_, err := r.Store.Pool.Exec(ctx, `WITH finished AS (
			UPDATE report_subscription_runs SET state=$2,completed_at=$3::timestamptz,result_count=$4,error=$5,
			  available_at=CASE WHEN $2='FAILED' THEN $3::timestamptz+make_interval(secs=>LEAST(3600,power(2,attempts)::int*5)) ELSE available_at END
			WHERE id=$1 AND state='RUNNING' RETURNING subscription_id)
		UPDATE report_subscriptions rs SET last_run_at=$3::timestamptz,last_result_count=$4,last_error=$5 FROM finished WHERE rs.id=finished.subscription_id`,
		run.RunID, state, r.now(), count, message)
	return err
}
