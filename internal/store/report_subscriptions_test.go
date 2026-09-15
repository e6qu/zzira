package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestReportSubscriptionsEmailEachRecipientTheReportAsTheySeeIt(t *testing.T) {
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
	workspaceID, ownerID, viewerID, leaverID := NewID("ws"), NewID("usr"), NewID("usr"), NewID("usr")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Report email test')`, workspaceID)
	for _, id := range []string{ownerID, viewerID, leaverID} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$3)`, id, id+"@example.test", "Person "+id)
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, id)
	}
	t.Cleanup(func() {
		exec(`DELETE FROM report_subscriptions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM email_outbox WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{ownerID, viewerID, leaverID} {
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	const report = "/projects/RS/reports/created-vs-resolved?days=7"

	if _, err := st.SaveReportSubscription(ctx, workspaceID, ownerID, report, "0 8 * * 9", nil); !errors.Is(err, ErrReportSubscriptionValidation) {
		t.Fatalf("bad schedule error = %v", err)
	}
	if _, err := st.SaveReportSubscription(ctx, workspaceID, ownerID, report, "0 8 * * 1", []string{NewID("usr")}); !errors.Is(err, ErrReportSubscriptionValidation) {
		t.Fatalf("stranger recipient error = %v", err)
	}
	subscription, err := st.SaveReportSubscription(ctx, workspaceID, ownerID, report, "0 8 * * 1", []string{ownerID, viewerID, viewerID})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := st.ReportSubscriptions(ctx, workspaceID, ownerID, report)
	if err != nil || len(listed) != 1 || listed[0].CronExpression != "0 8 * * 1" || len(listed[0].Recipients) != 2 {
		t.Fatalf("subscriptions = %+v, %v", listed, err)
	}
	if others, err := st.ReportSubscriptions(ctx, workspaceID, ownerID, "/projects/RS/reports/created-vs-resolved?days=30"); err != nil || len(others) != 0 {
		t.Fatalf("another window's subscriptions = %+v, %v", others, err)
	}

	// Each recipient's report is drawn with their own access.
	blocked := map[string]bool{}
	var rendered []string
	now := time.Date(2026, time.September, 14, 9, 0, 0, 0, time.UTC)
	runner := &ReportSubscriptionRunner{Store: st, BaseURL: "https://zzira.test/", Now: func() time.Time { return now },
		Render: func(_ context.Context, userID, target string) (string, string, error) {
			rendered = append(rendered, userID+" "+target)
			if blocked[userID] {
				return "", "", errors.New("forbidden")
			}
			return "RS created vs resolved", "Date,Created\n2026-09-13,2\n2026-09-14,1\n", nil
		}}
	exec(`UPDATE report_subscriptions SET next_run_at=$2 WHERE id=$1`, subscription.ID, now.Add(-time.Minute))
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if len(rendered) != 2 || rendered[0] != ownerID+" "+report || rendered[1] != viewerID+" "+report {
		t.Fatalf("rendered = %v", rendered)
	}
	var state string
	var count int
	if err := st.Pool.QueryRow(ctx, `SELECT state,result_count FROM report_subscription_runs WHERE subscription_id=$1`, subscription.ID).Scan(&state, &count); err != nil || state != "SUCCEEDED" || count != 2 {
		t.Fatalf("run = %s %d, %v", state, count, err)
	}
	// Read every email before asserting, so a failure does not leave the rows
	// holding the connection cleanup needs.
	type sentEmail struct{ recipient, subject, body string }
	var sent []sentEmail
	rows, err := st.Pool.Query(ctx, `SELECT recipient,subject,body FROM email_outbox WHERE workspace_id=$1 ORDER BY recipient`, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var email sentEmail
		if err := rows.Scan(&email.recipient, &email.subject, &email.body); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		sent = append(sent, email)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 {
		t.Fatalf("emails = %+v", sent)
	}
	for _, email := range sent {
		if email.subject != "ZZIRA report: RS created vs resolved" || !strings.Contains(email.body, "https://zzira.test"+report) || !strings.HasSuffix(email.body, "Date,Created\n2026-09-13,2\n2026-09-14,1\n") {
			t.Fatalf("email to %s = %q / %q", email.recipient, email.subject, email.body)
		}
	}
	// Nothing else is due, so nothing is sent twice.
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	var emails int
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE workspace_id=$1`, workspaceID).Scan(&emails); err != nil || emails != 2 {
		t.Fatalf("emails after second drain = %d, %v", emails, err)
	}

	// A recipient who can no longer open the report, or who left the site,
	// fails the run and is named in its error.
	if _, err := st.SaveReportSubscription(ctx, workspaceID, ownerID, report, "0 8 * * 1", []string{viewerID, leaverID}); err != nil {
		t.Fatal(err)
	}
	blocked[viewerID] = true
	exec(`UPDATE users SET active=false WHERE id=$1`, leaverID)
	exec(`UPDATE report_subscriptions SET next_run_at=$2 WHERE id=$1`, subscription.ID, now.Add(-30*time.Second))
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	var lastError string
	if err := st.Pool.QueryRow(ctx, `SELECT last_error FROM report_subscriptions WHERE id=$1`, subscription.ID).Scan(&lastError); err != nil ||
		lastError != "Person "+viewerID+" can no longer open the report; Person "+leaverID+" is no longer an active member of this site" {
		t.Fatalf("last error = %q, %v", lastError, err)
	}
	if err := st.DeleteReportSubscription(ctx, workspaceID, viewerID, report, subscription.ID); err == nil {
		t.Fatal("someone else removed the owner's report email")
	}
	if err := st.DeleteReportSubscription(ctx, workspaceID, ownerID, report, subscription.ID); err != nil {
		t.Fatal(err)
	}
	if listed, err = st.ReportSubscriptions(ctx, workspaceID, ownerID, report); err != nil || len(listed) != 0 {
		t.Fatalf("after removal = %+v, %v", listed, err)
	}
}
