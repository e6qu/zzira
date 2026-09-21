package store

import (
	"errors"

	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFilterSubscriptionSchedule(t *testing.T) {
	wednesday := time.Date(2026, time.September, 9, 9, 30, 0, 0, time.UTC)
	for expression, want := range map[string]time.Time{
		"0 8 * * *": time.Date(2026, time.September, 10, 8, 0, 0, 0, time.UTC),
		"0 8 * * 1": time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC),
	} {
		got, err := nextSubscriptionRun(expression, "", wednesday)
		if err != nil || !got.Equal(want) {
			t.Fatalf("next %q = %s, want %s (%v)", expression, got, want, err)
		}
	}
	for _, invalid := range []string{"* * * * *", "0 25 * * *", "0 8 * * 7", "0 8 1 * *"} {
		if _, err := nextSubscriptionRun(invalid, "", wednesday); err == nil {
			t.Fatalf("accepted schedule %q", invalid)
		}
	}
	// A cron expression runs in the subscription's zone: 09:00 in Bucharest is
	// 06:00 UTC, and the five-field schedules follow the zone as well.
	bucharest, err := nextSubscriptionRun("0 0 9 ? * *", "Europe/Bucharest", wednesday)
	if err != nil || !bucharest.Equal(time.Date(2026, time.September, 10, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("cron in a zone = %s (%v)", bucharest, err)
	}
	daily, err := nextSubscriptionRun("0 8 * * *", "Europe/Bucharest", wednesday)
	if err != nil || !daily.Equal(time.Date(2026, time.September, 10, 5, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily in a zone = %s (%v)", daily, err)
	}
	if _, err := nextSubscriptionRun("0 8 * * *", "Mars/Olympus", wednesday); err == nil {
		t.Fatal("accepted a zone that does not exist")
	}
	if _, err := nextSubscriptionRun("0 0 9 ? * NOPE", "", wednesday); err == nil {
		t.Fatal("accepted an unparseable cron expression")
	}
}

func TestFilterSubscriptionRunnerQueuesOnePermissionScopedEmail(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err = Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, userID, projectID := NewID("ws"), NewID("usr"), NewID("prj")
	projectKey := strings.ToUpper("FS" + projectID[len(projectID)-5:])
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Subscription test')`, workspaceID)
	exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test','Subscription owner')`, userID, userID+"@example.test")
	exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, userID)
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,$3,'Subscription project','wf_default',$4)`, projectID, workspaceID, projectKey, userID)
	t.Cleanup(func() {
		exec(`DELETE FROM filters WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM email_outbox WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		exec(`DELETE FROM users WHERE id=$1`, userID)
	})
	issue, _, err := st.CreateIssue(ctx, userID, projectID, "Ready for subscription", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	filter, err := st.CreateManagedFilter(ctx, NewID("flt"), workspaceID, userID, FilterDetails{Name: "Release readiness", JQL: "project = " + projectKey})
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := st.SaveFilterSubscription(ctx, workspaceID, userID, filter.ID, FilterSubscriptionInput{Expression: "0 8 * * *"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.September, 9, 10, 0, 0, 0, time.UTC)
	exec(`UPDATE filter_subscriptions SET next_run_at=$2 WHERE id=$1`, subscription.ID, now.Add(-time.Minute))
	runner := &FilterSubscriptionRunner{Store: st, BaseURL: "https://zzira.test", Now: func() time.Time { return now }}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	var state, recipient, subject, body string
	var count int
	var htmlBody string
	if err := st.Pool.QueryRow(ctx, `SELECT r.state,r.result_count,e.recipient,e.subject,e.body,e.html_body FROM filter_subscription_runs r JOIN email_outbox e ON e.dedupe_key='filter-subscription:'||r.id||':'||$2 WHERE r.subscription_id=$1`, subscription.ID, userID).Scan(&state, &count, &recipient, &subject, &body, &htmlBody); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(htmlBody, "<!doctype html>") || !strings.Contains(htmlBody, `href="/browse/`+issue.Key) {
		t.Fatalf("filter email carries no HTML alternative linking its work: %q", htmlBody)
	}
	if state != "SUCCEEDED" || count != 1 || recipient != userID+"@example.test" || !strings.Contains(subject, filter.Name) || !strings.Contains(body, issue.Key) {
		t.Fatalf("delivery state=%s count=%d recipient=%q subject=%q body=%q", state, count, recipient, subject, body)
	}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	var deliveries int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE workspace_id=$1`, workspaceID).Scan(&deliveries); err != nil || deliveries != 1 {
		t.Fatalf("deliveries=%d err=%v", deliveries, err)
	}
	loaded, err := st.FilterByID(ctx, workspaceID, userID, filter.ID)
	if err != nil || len(loaded.Subscriptions) != 1 || loaded.Subscriptions[0].LastResultCount == nil || *loaded.Subscriptions[0].LastResultCount != 1 {
		t.Fatalf("loaded subscriptions=%+v err=%v", loaded.Subscriptions, err)
	}

	// An administrator sees every filter email the site sends and can stop one
	// without owning it: the site is the only thing the delete is scoped to.
	all, err := st.WorkspaceFilterSubscriptions(ctx, workspaceID)
	if err != nil || len(all) != 1 {
		t.Fatalf("site subscriptions=%+v err=%v", all, err)
	}
	if all[0].FilterName != filter.Name || all[0].OwnerID != userID || all[0].OwnerName != "Subscription owner" || all[0].CronExpression != "0 8 * * *" {
		t.Fatalf("site subscription=%+v", all[0])
	}
	if err := st.DeleteWorkspaceFilterSubscription(ctx, workspaceID, all[0].ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := st.WorkspaceFilterSubscriptions(ctx, workspaceID)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("subscriptions after an administrator stopped one=%+v err=%v", remaining, err)
	}
	if err := st.DeleteWorkspaceFilterSubscription(ctx, workspaceID, all[0].ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stopping it twice = %v, want no rows", err)
	}
}

// Jira lets anyone who can see a filter subscribe to it, sends a group's
// members the result when the subscription names one, and leaves an empty
// result unsent unless it was told otherwise.
func TestFilterSubscriptionsReachViewersAndGroups(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err = Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, ownerID, viewerID, memberID, projectID := NewID("ws"), NewID("usr"), NewID("usr"), NewID("usr"), NewID("prj")
	projectKey := strings.ToUpper("FG" + projectID[len(projectID)-5:])
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Group subscription test')`, workspaceID)
	for _, id := range []string{ownerID, viewerID, memberID} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, id, id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, id)
	}
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,$3,'Group subscriptions','wf_default',$4)`, projectID, workspaceID, projectKey, ownerID)
	var organizationID, directoryID, groupID string
	if err = st.Pool.QueryRow(ctx, `SELECT organization_id::text,id::text FROM sites WHERE workspace_id=$1`, workspaceID).Scan(&organizationID, new(string)); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT id::text FROM directories WHERE organization_id=$1::uuid AND active ORDER BY created_at LIMIT 1`, organizationID).Scan(&directoryID); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `INSERT INTO groups(directory_id,name) VALUES($1::uuid,'Release watchers') RETURNING id::text`, directoryID).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO group_members(group_id,user_id) VALUES($1::uuid,$2)`, groupID, memberID)
	t.Cleanup(func() {
		exec(`DELETE FROM filters WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM email_outbox WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM group_members WHERE group_id=$1::uuid`, groupID)
		exec(`DELETE FROM groups WHERE id=$1::uuid`, groupID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{ownerID, viewerID, memberID} {
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	if _, _, err = st.CreateIssue(ctx, ownerID, projectID, "Watched work", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "", "", nil, nil, "", ""); err != nil {
		t.Fatal(err)
	}
	filter, err := st.CreateManagedFilter(ctx, NewID("flt"), workspaceID, ownerID, FilterDetails{Name: "Everything", JQL: "project = " + projectKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.AddFilterPermission(ctx, workspaceID, ownerID, filter.ID, FilterPermissionInput{Type: "global", Rights: 1}); err != nil {
		t.Fatal(err)
	}

	// Someone who can see the filter but does not own it may subscribe.
	if _, err = st.SaveFilterSubscription(ctx, workspaceID, viewerID, filter.ID, FilterSubscriptionInput{Expression: "0 8 * * *"}); err != nil {
		t.Fatalf("a viewer could not subscribe: %v", err)
	}
	// ...and may stop their own subscription, which they do not own the
	// filter of.
	viewerFilters, err := st.FilterByID(ctx, workspaceID, viewerID, filter.ID)
	if err != nil || len(viewerFilters.Subscriptions) != 1 {
		t.Fatalf("viewer subscriptions=%+v err=%v", viewerFilters.Subscriptions, err)
	}
	if err = st.DeleteFilterSubscription(ctx, workspaceID, viewerID, filter.ID, viewerFilters.Subscriptions[0].ID); err != nil {
		t.Fatalf("a viewer could not stop their own subscription: %v", err)
	}

	// A group subscription needs the global permission that governs it.
	if _, err = st.SaveFilterSubscription(ctx, workspaceID, viewerID, filter.ID, FilterSubscriptionInput{Expression: "0 8 * * *", GroupID: groupID}); !errors.Is(err, ErrFilterPermission) {
		t.Fatalf("group subscription without the permission = %v", err)
	}
	exec(`INSERT INTO global_permission_grants(workspace_id,permission_key,group_id) VALUES($1,'MANAGE_GROUP_FILTER_SUBSCRIPTIONS',$2::uuid)`, workspaceID, groupID)
	exec(`INSERT INTO group_members(group_id,user_id) VALUES($1::uuid,$2)`, groupID, ownerID)
	subscription, err := st.SaveFilterSubscription(ctx, workspaceID, ownerID, filter.ID, FilterSubscriptionInput{Expression: "0 8 * * *", GroupID: groupID})
	if err != nil {
		t.Fatalf("group subscription with the permission: %v", err)
	}

	now := time.Date(2026, time.September, 9, 10, 0, 0, 0, time.UTC)
	exec(`UPDATE filter_subscriptions SET next_run_at=$2 WHERE id=$1`, subscription.ID, now.Add(-time.Minute))
	runner := &FilterSubscriptionRunner{Store: st, BaseURL: "https://zzira.test", Now: func() time.Time { return now }}
	if err = runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	var recipients []string
	rows, err := st.Pool.Query(ctx, `SELECT recipient FROM email_outbox WHERE workspace_id=$1 ORDER BY recipient`, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var recipient string
		if err := rows.Scan(&recipient); err != nil {
			t.Fatal(err)
		}
		recipients = append(recipients, recipient)
	}
	rows.Close()
	// The group's members received it; nobody else did.
	if len(recipients) != 2 || recipients[0] != memberID+"@example.test" && recipients[1] != memberID+"@example.test" {
		t.Fatalf("group email recipients = %v", recipients)
	}

	// A filter that matches nothing is not emailed unless it was told to.
	exec(`DELETE FROM email_outbox WHERE workspace_id=$1`, workspaceID)
	empty, err := st.CreateManagedFilter(ctx, NewID("flt"), workspaceID, ownerID, FilterDetails{Name: "Nothing", JQL: "project = " + projectKey + " AND summary ~ nosuchwork"})
	if err != nil {
		t.Fatal(err)
	}
	quiet, err := st.SaveFilterSubscription(ctx, workspaceID, ownerID, empty.ID, FilterSubscriptionInput{Expression: "0 8 * * *"})
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE filter_subscriptions SET next_run_at=$2 WHERE id=$1`, quiet.ID, now.Add(-time.Minute))
	if err = runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	var sent int
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE workspace_id=$1`, workspaceID).Scan(&sent); err != nil || sent != 0 {
		t.Fatalf("an empty filter sent %d emails (%v)", sent, err)
	}
	loud, err := st.SaveFilterSubscription(ctx, workspaceID, ownerID, empty.ID, FilterSubscriptionInput{Expression: "0 9 * * *", EmailWhenEmpty: true})
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE filter_subscriptions SET next_run_at=$2 WHERE id=$1`, loud.ID, now.Add(-time.Minute))
	if err = runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if err = st.Pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE workspace_id=$1`, workspaceID).Scan(&sent); err != nil || sent != 1 {
		t.Fatalf("a subscription that asked for empty results sent %d emails (%v)", sent, err)
	}
}
