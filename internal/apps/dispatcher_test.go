package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/secretbox"
	"github.com/e6qu/zzira/internal/store"
)

func TestOutboundRunnerDeliversSignedLifecycleWebhookAndSchedule(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("outbound-runtime-secret-that-is-long-enough")
	box, err := secretbox.New(bytes.Repeat([]byte{11}, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	type receivedRequest struct {
		Path, Event string
		Body        []byte
	}
	received := []receivedRequest{}
	failInstalled := true
	remote := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Errorf("read callback: %v", readErr)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		timestamp, parseErr := strconv.ParseInt(request.Header.Get("X-Zzira-App-Timestamp"), 10, 64)
		requestID := request.Header.Get("X-Zzira-App-Request-Id")
		wantSignature := SignRequest(secret, timestamp, requestID, request.Method, request.URL.RequestURI(), body)
		if parseErr != nil || request.Header.Get("X-Zzira-App-Signature") != wantSignature {
			t.Errorf("invalid signed callback headers: timestamp=%q id=%q", request.Header.Get("X-Zzira-App-Timestamp"), requestID)
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		received = append(received, receivedRequest{Path: request.URL.Path, Event: request.Header.Get("X-Zzira-App-Event"), Body: body})
		if request.URL.Path == "/lifecycle/installed" && failInstalled {
			failInstalled = false
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer remote.Close()

	appKey := "outbound." + strings.ReplaceAll(strings.ToLower(store.NewID("test")), "_", "-")
	raw := []byte(fmt.Sprintf(`{"key":%q,"name":"Outbound test","baseUrl":%q,"version":"1.0.0","scopes":["read:jira-work","manage:webhooks"],"modules":[],"lifecycle":{"installed":"/lifecycle/installed"},"webhooks":[{"key":"issue-events","url":"/webhooks/issues?source=descriptor","events":["jira:issue_created"]}],"scheduledTriggers":[{"key":"hourly-sync","url":"/scheduled/hourly","interval":"hour"}]}`, appKey, remote.URL+"/app"))
	descriptor, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := box.Seal(secret, workspaceID+"/"+appKey)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := st.InstallApp(ctx, workspaceID, adminID, descriptor, raw, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM app_installations WHERE id=$1`, installation.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE user_id=$1`, installation.PrincipalID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, installation.PrincipalID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organization_audit_events WHERE target_type='app' AND target_id=$1`, appKey)
	})
	now = now.Add(2 * time.Second)
	runner := &OutboundRunner{Store: st, Secrets: box, Client: remote.Client(), Now: func() time.Time { return now }}
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	deliveries, err := st.AppOutboundDeliveries(ctx, installation.ID, 10)
	if err != nil || len(deliveries) != 1 || deliveries[0].State != "failed" || deliveries[0].Attempts != 1 {
		t.Fatalf("first lifecycle attempt = %+v, %v", deliveries, err)
	}

	issueID := store.NewID("app_issue")
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM actions WHERE workspace_id=$1 AND entity_id=$2`, workspaceID, issueID)
	})
	tx, err := st.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var seq int64
	if err := tx.QueryRow(ctx, `UPDATE workspaces SET seq=seq+1 WHERE id=$1 RETURNING seq`, workspaceID).Scan(&seq); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	payload, _ := json.Marshal(models.IssueUpdatePayload{Issue: models.Issue{ID: issueID, Key: "APP-1", Summary: "Created for an app webhook"}})
	if _, err := tx.Exec(ctx, `INSERT INTO actions(workspace_id,seq,entity_type,entity_id,op,schema_v,payload,actor_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, workspaceID, seq, models.EntityIssue, issueID, models.OpUpsert, models.SchemaVersion, payload, adminID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE app_scheduled_triggers SET next_run_at=$2 WHERE installation_id=$1`, installation.ID, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Second)
	if err := runner.DrainOnce(ctx, workspaceID); err != nil {
		t.Fatal(err)
	}
	deliveries, err = st.AppOutboundDeliveries(ctx, installation.ID, 10)
	if err != nil || len(deliveries) != 3 {
		t.Fatalf("outbound deliveries = %+v, %v", deliveries, err)
	}
	for _, delivery := range deliveries {
		if delivery.State != "delivered" {
			t.Fatalf("delivery did not recover: %+v", delivery)
		}
	}

	wantPaths := map[string]bool{"/lifecycle/installed": false, "/webhooks/issues": false, "/scheduled/hourly": false}
	for _, callback := range received {
		if _, exists := wantPaths[callback.Path]; exists && callback.Event != "" {
			wantPaths[callback.Path] = true
		}
		if callback.Path == "/webhooks/issues" && !bytes.Contains(callback.Body, []byte(`"jira:issue_created"`)) {
			t.Fatalf("webhook payload = %s", callback.Body)
		}
	}
	for path, found := range wantPaths {
		if !found {
			t.Errorf("missing signed callback %s: %+v", path, received)
		}
	}
}
