package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

func TestDashboardSubscriptionsEmailEachRecipientTheirView(t *testing.T) {
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
	workspaceID, ownerID, viewerID, projectID := NewID("ws"), NewID("usr"), NewID("usr"), NewID("prj")
	projectKey := strings.ToUpper("DS" + projectID[len(projectID)-5:])
	exec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := st.Pool.Exec(ctx, query, args...); execErr != nil {
			t.Fatal(execErr)
		}
	}
	exec(`INSERT INTO workspaces(id,slug,name) VALUES($1,$1,'Dashboard email test')`, workspaceID)
	for _, id := range []string{ownerID, viewerID} {
		exec(`INSERT INTO users(id,email,password_hash,display_name) VALUES($1,$2,'test',$1)`, id, id+"@example.test")
		exec(`INSERT INTO memberships(workspace_id,user_id,role) VALUES($1,$2,'member')`, workspaceID, id)
	}
	exec(`INSERT INTO projects(id,workspace_id,key,name,workflow_id,lead_account_id) VALUES($1,$2,$3,'Dashboard project','wf_default',$4)`, projectID, workspaceID, projectKey, ownerID)
	t.Cleanup(func() {
		exec(`DELETE FROM dashboards WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM issues WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM email_outbox WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM actions WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM projects WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM memberships WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM custom_fields WHERE workspace_id=$1`, workspaceID)
		exec(`DELETE FROM workspaces WHERE id=$1`, workspaceID)
		for _, id := range []string{ownerID, viewerID} {
			exec(`DELETE FROM users WHERE id=$1`, id)
		}
	})
	issue, _, err := st.CreateIssue(ctx, ownerID, projectID, "Ship the dashboard email", json.RawMessage(`{"type":"doc","version":1,"content":[]}`), "st_todo", "it_task", "", "", nil, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	shared, err := st.SaveDashboard(ctx, workspaceID, ownerID, "", DashboardDetails{Name: "Delivery", SharePermissions: []models.DashboardShare{{Type: "loggedin"}}})
	if err != nil {
		t.Fatal(err)
	}
	private, err := st.SaveDashboard(ctx, workspaceID, ownerID, "", DashboardDetails{Name: "Private notes"})
	if err != nil {
		t.Fatal(err)
	}
	addGadget := func(moduleKey string, config models.GadgetConfig) {
		t.Helper()
		gadget, err := st.SaveDashboardGadget(ctx, workspaceID, ownerID, shared.ID, 0, GadgetUpdate{ModuleKey: moduleKey})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(config)
		if _, err := st.SetDashboardProperty(ctx, workspaceID, ownerID, shared.ID, gadget.ID, "zzira.config", raw); err != nil {
			t.Fatal(err)
		}
	}
	addGadget("com.zzira:filter-results", models.GadgetConfig{JQL: "project = " + projectKey, Limit: 5})
	addGadget("com.zzira:created-vs-resolved", models.GadgetConfig{ProjectKey: projectKey, Days: 7})
	addGadget("com.zzira:velocity", models.GadgetConfig{})

	// Recipients must be members who can view the dashboard.
	if _, err := st.SaveDashboardSubscription(ctx, workspaceID, ownerID, private.ID, "0 8 * * *", []string{viewerID}); !errors.Is(err, ErrDashboardValidation) {
		t.Fatalf("private dashboard recipient error = %v", err)
	}
	if _, err := st.SaveDashboardSubscription(ctx, workspaceID, ownerID, shared.ID, "0 8 * * 9", nil); !errors.Is(err, ErrDashboardValidation) {
		t.Fatalf("bad schedule error = %v", err)
	}
	subscription, err := st.SaveDashboardSubscription(ctx, workspaceID, viewerID, shared.ID, "0 8 * * 1", []string{ownerID, viewerID})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := st.DashboardSubscriptions(ctx, workspaceID, viewerID, shared.ID)
	if err != nil || len(listed) != 1 || listed[0].CronExpression != "0 8 * * 1" || len(listed[0].Recipients) != 2 {
		t.Fatalf("subscriptions = %+v, %v", listed, err)
	}

	now := time.Date(2026, time.September, 9, 10, 0, 0, 0, time.UTC)
	// The report gadget counts from the runner's clock, so the work item is
	// created inside its seven day window.
	exec(`UPDATE issues SET created_at=$2 WHERE id=$1`, issue.ID, now.Add(-24*time.Hour))
	exec(`UPDATE dashboard_subscriptions SET next_run_at=$2 WHERE id=$1`, subscription.ID, now.Add(-time.Minute))
	runner := &DashboardSubscriptionRunner{Store: st, BaseURL: "https://zzira.test", Now: func() time.Time { return now }}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	var state string
	var count int
	if err := st.Pool.QueryRow(ctx, `SELECT state,result_count FROM dashboard_subscription_runs WHERE subscription_id=$1`, subscription.ID).Scan(&state, &count); err != nil || state != "SUCCEEDED" || count != 3 {
		t.Fatalf("run = %s %d, %v", state, count, err)
	}
	// Read every email before asserting: failing with the rows still open
	// would leave cleanup waiting for a connection instead of reporting.
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
	emails := len(sent)
	for _, email := range sent {
		if email.subject != "ZZIRA dashboard: Delivery" || !strings.Contains(email.body, "https://zzira.test/dashboards/"+shared.ID) ||
			!strings.Contains(email.body, "Filter results: 1 work items") || !strings.Contains(email.body, issue.Key+"  Ship the dashboard email") ||
			!strings.Contains(email.body, "Created vs. resolved chart: 1 created and 0 resolved in "+projectKey+" over the last 7 days") ||
			!strings.Contains(email.body, "Velocity chart: open the dashboard to choose a scrum board.") {
			t.Fatalf("email to %s = %q / %q", email.recipient, email.subject, email.body)
		}
	}
	if emails != 2 {
		t.Fatalf("emails = %d", emails)
	}
	// Nothing else is due, so nothing is sent twice.
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM email_outbox WHERE workspace_id=$1`, workspaceID).Scan(&emails); err != nil || emails != 2 {
		t.Fatalf("emails after second drain = %d, %v", emails, err)
	}

	// A recipient who can no longer view the dashboard fails the run.
	if _, err := st.SaveDashboard(ctx, workspaceID, ownerID, shared.ID, DashboardDetails{Name: "Delivery", SharePermissions: []models.DashboardShare{}, EditPermissions: []models.DashboardShare{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Dashboard(ctx, workspaceID, viewerID, shared.ID); err == nil {
		t.Fatal("the viewer still sees the dashboard after its sharing was removed")
	}
	// A later scheduled time is a new run; the first one already delivered.
	exec(`UPDATE dashboard_subscriptions SET next_run_at=$2 WHERE id=$1`, subscription.ID, now.Add(-30*time.Second))
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	var lastError string
	if err := st.Pool.QueryRow(ctx, `SELECT last_error FROM dashboard_subscriptions WHERE id=$1`, subscription.ID).Scan(&lastError); err != nil || !strings.Contains(lastError, "can no longer view the dashboard") {
		t.Fatalf("last error = %q, %v", lastError, err)
	}
	if err := st.DeleteDashboardSubscription(ctx, workspaceID, viewerID, shared.ID, subscription.ID); err != nil {
		t.Fatal(err)
	}
	if listed, err = st.DashboardSubscriptions(ctx, workspaceID, viewerID, shared.ID); err != nil || len(listed) != 0 {
		t.Fatalf("after removal = %+v, %v", listed, err)
	}
}
