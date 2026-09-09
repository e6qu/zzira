package store

import (
	"context"
	"encoding/json"
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
		got, err := nextFilterSubscriptionRun(expression, wednesday)
		if err != nil || !got.Equal(want) {
			t.Fatalf("next %q = %s, want %s (%v)", expression, got, want, err)
		}
	}
	for _, invalid := range []string{"* * * * *", "0 25 * * *", "0 8 * * 7", "0 8 1 * *"} {
		if _, err := nextFilterSubscriptionRun(invalid, wednesday); err == nil {
			t.Fatalf("accepted schedule %q", invalid)
		}
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
	subscription, err := st.SaveFilterSubscription(ctx, workspaceID, userID, filter.ID, "0 8 * * *", nil)
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
	if err := st.Pool.QueryRow(ctx, `SELECT r.state,r.result_count,e.recipient,e.subject,e.body FROM filter_subscription_runs r JOIN email_outbox e ON e.dedupe_key='filter-subscription:'||r.id||':'||$2 WHERE r.subscription_id=$1`, subscription.ID, userID).Scan(&state, &count, &recipient, &subject, &body); err != nil {
		t.Fatal(err)
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
}
