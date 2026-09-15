package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/jackc/pgx/v5"
)

// SaveDashboardSubscription schedules a dashboard email for someone who can
// view the dashboard. Every recipient must be an active member of the site
// who can view it too; leaving recipients empty sends it to the subscriber.
func (s *Store) SaveDashboardSubscription(ctx context.Context, ws, user, dashboardID, expression string, recipients []string) (*models.DashboardSubscription, error) {
	if _, err := s.Dashboard(ctx, ws, user, dashboardID); err != nil {
		return nil, err
	}
	next, err := nextFilterSubscriptionRun(expression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrDashboardValidation, strings.TrimPrefix(err.Error(), ErrFilterValidation.Error()+": "))
	}
	clean, problem, err := s.subscriptionRecipients(ctx, ws, recipients)
	if err != nil {
		return nil, err
	}
	if problem != "" {
		return nil, fmt.Errorf("%w: %s", ErrDashboardValidation, problem)
	}
	for _, recipient := range clean {
		if _, err := s.Dashboard(ctx, ws, recipient, dashboardID); errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: every recipient must be able to view this dashboard", ErrDashboardValidation)
		} else if err != nil {
			return nil, err
		}
	}
	encoded, _ := json.Marshal(clean)
	subscription := &models.DashboardSubscription{DashboardID: dashboardID, UserID: user, CronExpression: expression, Recipients: clean, Enabled: true, NextRunAt: next.Format(time.RFC3339)}
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO dashboard_subscriptions(dashboard_id,user_id,cron_expression,recipients,enabled,next_run_at)
		VALUES($1,$2,$3,$4,true,$5)
		ON CONFLICT(dashboard_id,user_id,cron_expression) DO UPDATE SET recipients=EXCLUDED.recipients,enabled=true,next_run_at=EXCLUDED.next_run_at,last_error=''
		RETURNING id`, dashboardID, user, expression, encoded, next).Scan(&subscription.ID)
	if err != nil {
		return nil, err
	}
	return subscription, nil
}

// DeleteDashboardSubscription removes one of the caller's dashboard emails.
func (s *Store) DeleteDashboardSubscription(ctx context.Context, ws, user, dashboardID string, subscriptionID int64) error {
	result, err := s.Pool.Exec(ctx, `DELETE FROM dashboard_subscriptions ds USING dashboards d
		WHERE ds.id=$1 AND ds.dashboard_id=$2 AND ds.user_id=$3 AND d.id=ds.dashboard_id AND d.workspace_id=$4`, subscriptionID, dashboardID, user, ws)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// DashboardSubscriptions lists the caller's emails of a dashboard.
func (s *Store) DashboardSubscriptions(ctx context.Context, ws, user, dashboardID string) ([]models.DashboardSubscription, error) {
	rows, err := s.Pool.Query(ctx, `SELECT ds.id,ds.dashboard_id,ds.user_id,ds.cron_expression,ds.recipients,ds.enabled,
		COALESCE(to_char(ds.next_run_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),COALESCE(to_char(ds.last_run_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'),''),ds.last_error,ds.last_result_count
		FROM dashboard_subscriptions ds JOIN dashboards d ON d.id=ds.dashboard_id
		WHERE d.workspace_id=$1 AND ds.user_id=$2 AND ds.dashboard_id=$3 ORDER BY ds.id`, ws, user, dashboardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.DashboardSubscription{}
	for rows.Next() {
		var subscription models.DashboardSubscription
		var raw []byte
		if err := rows.Scan(&subscription.ID, &subscription.DashboardID, &subscription.UserID, &subscription.CronExpression, &raw, &subscription.Enabled, &subscription.NextRunAt, &subscription.LastRunAt, &subscription.LastError, &subscription.LastResultCount); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &subscription.Recipients); err != nil {
			return nil, err
		}
		out = append(out, subscription)
	}
	return out, rows.Err()
}

// DashboardSubscriptionRunner delivers due dashboard emails, one claimed run
// at a time, retrying a failed run with backoff.
type DashboardSubscriptionRunner struct {
	Store   *Store
	BaseURL string
	Now     func() time.Time
}

type claimedDashboardSubscription struct {
	RunID, SubscriptionID                           int64
	WorkspaceID, DashboardID, DashboardName, UserID string
	Recipients                                      []string
}

func (r *DashboardSubscriptionRunner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// Run delivers due dashboard emails until ctx ends.
func (r *DashboardSubscriptionRunner) Run(ctx context.Context, workspaceID string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.DrainOnce(ctx, workspaceID); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("dashboard subscription runner: %v", err)
			}
		}
	}
}

// DrainOnce schedules the next due subscription and delivers one run.
func (r *DashboardSubscriptionRunner) DrainOnce(ctx context.Context, workspaceID string) error {
	if r.Store == nil {
		return errors.New("dashboard subscription runner is not configured")
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

func (r *DashboardSubscriptionRunner) enqueueDue(ctx context.Context, workspaceID string) error {
	tx, err := r.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id int64
	var expression string
	var scheduled time.Time
	err = tx.QueryRow(ctx, `SELECT ds.id,ds.cron_expression,ds.next_run_at FROM dashboard_subscriptions ds JOIN dashboards d ON d.id=ds.dashboard_id
		WHERE d.workspace_id=$1 AND NOT d.deleted AND ds.enabled AND ds.next_run_at<=$2 ORDER BY ds.next_run_at,ds.id FOR UPDATE OF ds SKIP LOCKED LIMIT 1`, workspaceID, r.now()).Scan(&id, &expression, &scheduled)
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
	if _, err = tx.Exec(ctx, `INSERT INTO dashboard_subscription_runs(subscription_id,scheduled_for,available_at) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, scheduled, r.now()); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE dashboard_subscriptions SET next_run_at=$2 WHERE id=$1`, id, next); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *DashboardSubscriptionRunner) claim(ctx context.Context, workspaceID string) (*claimedDashboardSubscription, error) {
	run := &claimedDashboardSubscription{}
	var raw []byte
	err := r.Store.Pool.QueryRow(ctx, `WITH candidate AS (
			SELECT dr.id FROM dashboard_subscription_runs dr JOIN dashboard_subscriptions ds ON ds.id=dr.subscription_id JOIN dashboards d ON d.id=ds.dashboard_id
			WHERE d.workspace_id=$1 AND NOT d.deleted AND ds.enabled AND dr.attempts<8
			  AND ((dr.state IN ('PENDING','FAILED') AND dr.available_at<=$2) OR (dr.state='RUNNING' AND dr.claimed_at<$2-interval '5 minutes'))
			ORDER BY dr.scheduled_for,dr.id FOR UPDATE OF dr SKIP LOCKED LIMIT 1),
		claimed AS (UPDATE dashboard_subscription_runs dr SET state='RUNNING',attempts=attempts+1,claimed_at=$2 FROM candidate WHERE dr.id=candidate.id RETURNING dr.*)
		SELECT c.id,c.subscription_id,d.workspace_id,d.id,d.name,ds.user_id,ds.recipients
		FROM claimed c JOIN dashboard_subscriptions ds ON ds.id=c.subscription_id JOIN dashboards d ON d.id=ds.dashboard_id`, workspaceID, r.now()).
		Scan(&run.RunID, &run.SubscriptionID, &run.WorkspaceID, &run.DashboardID, &run.DashboardName, &run.UserID, &raw)
	if err == nil {
		err = json.Unmarshal(raw, &run.Recipients)
	}
	return run, err
}

// gadgetSummary is one line of a dashboard email about a gadget, drawn with
// the recipient's access.
func (r *DashboardSubscriptionRunner) gadgetSummary(ctx context.Context, ws, recipient, dashboardID string, gadget models.DashboardGadget) []string {
	base := strings.TrimRight(r.BaseURL, "/")
	title := gadget.Title
	if strings.HasPrefix(gadget.ModuleKey, "app:") {
		return []string{title + ": open the dashboard to see this app gadget."}
	}
	results, err := r.Store.DashboardGadgetResults(ctx, ws, recipient, dashboardID, gadget)
	if err != nil {
		return []string{title + ": this gadget could not load its query."}
	}
	config := results.Config
	switch gadget.ModuleKey {
	case "com.zzira:filter-results", "com.zzira:assigned-to-me", "com.zzira:watched-issues", "com.zzira:voted-issues", "com.zzira:in-progress":
		lines := []string{fmt.Sprintf("%s: %d work items", title, results.Total)}
		for index, issue := range results.Issues {
			if index == 5 {
				break
			}
			lines = append(lines, "  "+issue.Key+"  "+issue.Summary+"  "+base+"/browse/"+issue.Key)
		}
		return lines
	case "com.zzira:two-dimensional-statistics":
		rows, columns := strings.ToLower(config.YGroupLabel()), strings.ToLower(config.GroupLabel())
		if results.Grid == nil || len(results.Grid.Rows) == 0 {
			return []string{fmt.Sprintf("%s: no work items by %s and %s", title, rows, columns)}
		}
		parts := []string{}
		for index, row := range results.Grid.Rows {
			if index == 5 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s %d", row.Name, row.Total))
		}
		return []string{fmt.Sprintf("%s: %d work items by %s and %s — %s", title, results.Total, rows, columns, strings.Join(parts, ", "))}
	case "com.zzira:issue-statistics", "com.zzira:pie-chart", "com.zzira:heat-map":
		parts := []string{}
		for index, count := range results.Counts {
			if index == 5 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s %d", count.Name, count.Count))
		}
		return []string{fmt.Sprintf("%s: %d work items by %s — %s", title, results.Total, strings.ToLower(config.GroupLabel()), strings.Join(parts, ", "))}
	}
	reportsOn := func(projectID string) bool {
		enabled, err := r.Store.ProjectFeatureEnabled(ctx, projectID, "jsw.classic.reports")
		return err == nil && enabled
	}
	now := r.now()
	switch gadget.ModuleKey {
	case "com.zzira:created-vs-resolved", "com.zzira:resolution-time", "com.zzira:recently-created", "com.zzira:average-age", "com.zzira:time-since":
		if config.ProjectKey == "" {
			return []string{title + ": open the dashboard to choose a project."}
		}
		project, err := r.Store.ProjectByIDOrKey(ctx, ws, config.ProjectKey)
		if err != nil || !reportsOn(project.ID) {
			return []string{title + ": this project's reports are not available."}
		}
		if gadget.ModuleKey == "com.zzira:created-vs-resolved" {
			report, err := r.Store.CreatedVsResolved(ctx, ws, recipient, project.ID, config.Days, now)
			if err != nil {
				return []string{title + ": this report could not be calculated."}
			}
			return []string{fmt.Sprintf("%s: %d created and %d resolved in %s over the last %d days", title, report.CreatedTotal, report.ResolvedTotal, project.Key, config.Days)}
		}
		switch gadget.ModuleKey {
		case "com.zzira:recently-created":
			report, err := r.Store.RecentlyCreated(ctx, ws, recipient, project.ID, config.Days, now)
			if err != nil {
				return []string{title + ": this report could not be calculated."}
			}
			return []string{fmt.Sprintf("%s: %d created in %s over the last %d days, %d of them since resolved", title, report.Created, project.Key, config.Days, report.Resolved)}
		case "com.zzira:average-age":
			report, err := r.Store.AverageAge(ctx, ws, recipient, project.ID, config.Days, now)
			if err != nil || len(report.Days) == 0 {
				return []string{title + ": this report could not be calculated."}
			}
			today := report.Days[len(report.Days)-1]
			if today.Unresolved == 0 {
				return []string{fmt.Sprintf("%s: nothing unresolved in %s", title, project.Key)}
			}
			return []string{fmt.Sprintf("%s: %d unresolved in %s, %s old on average", title, today.Unresolved, project.Key, summaryDuration(today.AverageSeconds))}
		case "com.zzira:time-since":
			report, err := r.Store.TimeSince(ctx, ws, recipient, project.ID, config.DateField, config.Days, now)
			if err != nil {
				return []string{title + ": this report could not be calculated."}
			}
			return []string{fmt.Sprintf("%s: %d work items %s in %s over the last %d days", title, report.Total, strings.ToLower(models.TimeSinceFieldName(report.Field)), project.Key, config.Days)}
		}
		report, err := r.Store.ResolutionTime(ctx, ws, recipient, project.ID, config.Days, now)
		if err != nil {
			return []string{title + ": this report could not be calculated."}
		}
		if report.Resolved == 0 {
			return []string{fmt.Sprintf("%s: nothing resolved in %s over the last %d days", title, project.Key, config.Days)}
		}
		return []string{fmt.Sprintf("%s: %d resolved in %s over the last %d days, taking %s on average", title, report.Resolved, project.Key, config.Days, summaryDuration(report.AverageSeconds))}
	case "com.zzira:velocity", "com.zzira:sprint-burndown", "com.zzira:days-remaining", "com.zzira:sprint-health":
		if config.BoardID == "" {
			return []string{title + ": open the dashboard to choose a scrum board."}
		}
		board, err := r.Store.BoardByIDInWorkspace(ctx, ws, config.BoardID)
		if err != nil || board.Type != "scrum" || !reportsOn(board.ProjectID) {
			return []string{title + ": this board's reports are not available."}
		}
		if gadget.ModuleKey == "com.zzira:velocity" {
			report, err := r.Store.VelocityReport(ctx, ws, recipient, board)
			if err != nil {
				return []string{title + ": this report could not be calculated."}
			}
			if len(report.Sprints) == 0 {
				return []string{fmt.Sprintf("%s: no completed sprints on %s yet", title, board.Name)}
			}
			completed := 0.0
			for _, sprint := range report.Sprints {
				completed += sprint.Completed
			}
			return []string{fmt.Sprintf("%s: the last %d sprints on %s completed %s %s on average", title, len(report.Sprints), board.Name, formatEstimate(floatPointer(completed/float64(len(report.Sprints)))), report.Statistic)}
		}
		sprints, err := r.Store.SprintsByBoard(ctx, board.ID)
		if err != nil {
			return []string{title + ": this report could not be calculated."}
		}
		for _, sprint := range sprints {
			if sprint.State != "active" {
				continue
			}
			if gadget.ModuleKey == "com.zzira:days-remaining" {
				days, overdue, ok := models.SprintDaysRemaining(*sprint, now)
				switch {
				case !ok:
					return []string{fmt.Sprintf("%s: %s on %s has no end date", title, sprint.Name, board.Name)}
				case overdue:
					return []string{fmt.Sprintf("%s: %s on %s is %d days overdue", title, sprint.Name, board.Name, days)}
				}
				return []string{fmt.Sprintf("%s: %d days remaining in %s on %s", title, days, sprint.Name, board.Name)}
			}
			report, err := r.Store.SprintReport(ctx, ws, recipient, board, sprint, now)
			if gadget.ModuleKey == "com.zzira:sprint-health" {
				if err != nil {
					return []string{title + ": this report could not be calculated."}
				}
				health := models.NewSprintHealth(report, now)
				elapsed := "no end date"
				if health.HasEnd {
					elapsed = fmt.Sprintf("%d%% of its time elapsed", health.Elapsed)
				}
				return []string{fmt.Sprintf("%s: %s on %s has %s, %d%% of its work complete and a %d%% scope change", title, sprint.Name, board.Name, elapsed, health.Complete, health.ScopeChange)}
			}
			if err != nil || len(report.Burndown) == 0 {
				return []string{title + ": this report could not be calculated."}
			}
			remaining := report.Burndown[len(report.Burndown)-1].Remaining
			return []string{fmt.Sprintf("%s: %s on %s has %s %s remaining", title, sprint.Name, board.Name, formatEstimate(floatPointer(remaining)), report.Statistic)}
		}
		return []string{fmt.Sprintf("%s: no active sprint on %s", title, board.Name)}
	}
	return []string{title}
}

func floatPointer(value float64) *float64 {
	rounded := float64(int64(value*100+0.5)) / 100
	return &rounded
}

// summaryDuration writes a duration in days and hours for an email.
func summaryDuration(seconds int64) string {
	hours := seconds / 3600
	switch days, rest := hours/24, hours%24; {
	case days > 0 && rest > 0:
		return fmt.Sprintf("%dd %dh", days, rest)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", seconds/60)
}

// execute emails each recipient the dashboard as they see it. A recipient who
// can no longer view the dashboard fails the run, so it is retried and
// reported, while recipients already sent are not sent twice.
func (r *DashboardSubscriptionRunner) execute(ctx context.Context, run *claimedDashboardSubscription) (int, error) {
	recipients := run.Recipients
	if len(recipients) == 0 {
		recipients = []string{run.UserID}
	}
	base := strings.TrimRight(r.BaseURL, "/")
	subject := strings.NewReplacer("\r", " ", "\n", " ").Replace("ZZIRA dashboard: " + run.DashboardName)
	gadgets := 0
	var failures []string
	for _, recipient := range recipients {
		email, name, active, err := r.Store.subscriptionRecipientEmail(ctx, run.WorkspaceID, recipient)
		if err != nil {
			return gadgets, err
		}
		if !active {
			failures = append(failures, name+" is no longer an active member of this site")
			continue
		}
		dashboard, err := r.Store.Dashboard(ctx, run.WorkspaceID, recipient, run.DashboardID)
		if err != nil {
			failures = append(failures, name+" can no longer view the dashboard")
			continue
		}
		list, err := r.Store.DashboardGadgets(ctx, run.WorkspaceID, recipient, run.DashboardID)
		if err != nil {
			return gadgets, err
		}
		lines := []string{dashboard.Name, base + "/dashboards/" + dashboard.ID, ""}
		if len(list) == 0 {
			lines = append(lines, "This dashboard has no gadgets yet.")
		}
		for _, gadget := range list {
			lines = append(lines, r.gadgetSummary(ctx, run.WorkspaceID, recipient, run.DashboardID, gadget)...)
		}
		gadgets = len(list)
		dedupe := fmt.Sprintf("dashboard-subscription:%d:%s", run.RunID, recipient)
		if _, err = r.Store.Pool.Exec(ctx, `INSERT INTO email_outbox(workspace_id,recipient,subject,body,dedupe_key) VALUES($1,$2,$3,$4,$5) ON CONFLICT(dedupe_key) WHERE dedupe_key IS NOT NULL DO NOTHING`, run.WorkspaceID, email, subject, strings.Join(lines, "\n"), dedupe); err != nil {
			return gadgets, err
		}
	}
	if len(failures) > 0 {
		return gadgets, errors.New(strings.Join(failures, "; "))
	}
	return gadgets, nil
}

func (r *DashboardSubscriptionRunner) finish(ctx context.Context, run *claimedDashboardSubscription, count int, runErr error) error {
	message, state := "", "SUCCEEDED"
	if runErr != nil {
		message, state = runErr.Error(), "FAILED"
		if len(message) > 2000 {
			message = message[:2000]
		}
	}
	_, err := r.Store.Pool.Exec(ctx, `WITH finished AS (
			UPDATE dashboard_subscription_runs SET state=$2,completed_at=$3::timestamptz,result_count=$4,error=$5,
			  available_at=CASE WHEN $2='FAILED' THEN $3::timestamptz+make_interval(secs=>LEAST(3600,power(2,attempts)::int*5)) ELSE available_at END
			WHERE id=$1 AND state='RUNNING' RETURNING subscription_id)
		UPDATE dashboard_subscriptions ds SET last_run_at=$3::timestamptz,last_result_count=$4,last_error=$5 FROM finished WHERE ds.id=finished.subscription_id`,
		run.RunID, state, r.now(), count, message)
	return err
}
