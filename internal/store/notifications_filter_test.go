package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestNotificationSyncFilter(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	const demoID = "usr_a17298024363"
	const anaID = "usr_075354b352eb"

	head, err := st.Head(ctx, "ws_default")
	if err != nil {
		t.Fatal(err)
	}

	// two targeted notifications: one per user
	for _, target := range []string{demoID, anaID} {
		if _, err := st.CreateNotification(ctx, "ws_default", &models.Notification{
			ID: NewID("ntf"), WorkspaceID: "ws_default", TargetUser: target,
			ActorID: demoID, ActorName: "Demo", Kind: "test",
			EntityType: "issue", EntityID: "iss_x", Message: "filter probe " + target,
		}); err != nil {
			t.Fatal(err)
		}
	}

	demoActions, err := st.ActionsSince(ctx, "ws_default", demoID, head, 100)
	if err != nil {
		t.Fatal(err)
	}
	anaActions, err := st.ActionsSince(ctx, "ws_default", anaID, head, 100)
	if err != nil {
		t.Fatal(err)
	}
	var demoNotifs, anaNotifs int
	for _, a := range demoActions {
		if a.EntityType == "notification" {
			demoNotifs++
		}
	}
	for _, a := range anaActions {
		if a.EntityType == "notification" {
			anaNotifs++
		}
	}
	if demoNotifs != 1 || anaNotifs != 1 {
		t.Fatalf("targeted filtering broken: demo=%d ana=%d", demoNotifs, anaNotifs)
	}
}

func TestNotificationReadStateIsPrivateIdempotentAndSynced(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	const demoID = "usr_a17298024363"
	targetID := NewID("usr_notification_test")
	before, err := st.UnreadNotificationCount(ctx, "ws_default", targetID)
	if err != nil {
		t.Fatal(err)
	}
	notification := &models.Notification{
		ID: NewID("ntf"), TargetUser: targetID, ActorID: demoID, ActorName: "Demo User",
		Kind: "assigned", EntityType: models.EntityIssue, EntityID: "iss_x", Message: "assigned you ZZ-1",
	}
	createdAction, err := st.CreateNotification(ctx, "ws_default", notification)
	if err != nil {
		t.Fatal(err)
	}
	if notification.Created == "" {
		t.Fatal("create notification did not return its canonical timestamp")
	}
	var createdPayload models.NotificationPayload
	if err := json.Unmarshal(createdAction.Payload, &createdPayload); err != nil {
		t.Fatal(err)
	}
	if createdPayload.Notification.Created == "" || createdPayload.Notification.Read {
		t.Fatalf("created payload = %+v", createdPayload.Notification)
	}
	if count, err := st.UnreadNotificationCount(ctx, "ws_default", targetID); err != nil || count != before+1 {
		t.Fatalf("unread count = %d, %v; want %d", count, err, before+1)
	}
	if _, err := st.NotificationByIDForUser(ctx, "ws_default", demoID, notification.ID); err == nil {
		t.Fatal("notification was visible to a different user")
	}

	updated, action, err := st.SetNotificationRead(ctx, "ws_default", targetID, notification.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Read || updated.ReadAt == "" || action == nil {
		t.Fatalf("read update = %+v, action=%v", updated, action)
	}
	var updatePayload models.NotificationPayload
	if err := json.Unmarshal(action.Payload, &updatePayload); err != nil {
		t.Fatal(err)
	}
	if !updatePayload.Notification.Read || updatePayload.Notification.ReadAt == "" {
		t.Fatalf("read payload = %+v", updatePayload.Notification)
	}
	if count, err := st.UnreadNotificationCount(ctx, "ws_default", targetID); err != nil || count != before {
		t.Fatalf("unread count after read = %d, %v; want %d", count, err, before)
	}

	head, err := st.Head(ctx, "ws_default")
	if err != nil {
		t.Fatal(err)
	}
	if _, action, err := st.SetNotificationRead(ctx, "ws_default", targetID, notification.ID, true); err != nil || action != nil {
		t.Fatalf("idempotent update action=%v err=%v", action, err)
	}
	if after, err := st.Head(ctx, "ws_default"); err != nil || after != head {
		t.Fatalf("idempotent update changed head from %d to %d (%v)", head, after, err)
	}
	if _, _, err := st.SetNotificationRead(ctx, "ws_default", demoID, notification.ID, true); err == nil {
		t.Fatal("different user updated private notification")
	}

	if _, _, err := st.SetNotificationRead(ctx, "ws_default", targetID, notification.ID, false); err != nil {
		t.Fatal(err)
	}
	head, err = st.Head(ctx, "ws_default")
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := st.MarkAllNotificationsRead(ctx, "ws_default", targetID); err != nil || changed != 1 {
		t.Fatalf("mark all changed=%d err=%v", changed, err)
	}
	actions, err := st.ActionsSince(ctx, "ws_default", targetID, head, 10)
	if err != nil || len(actions) != 1 {
		t.Fatalf("mark all actions=%d err=%v", len(actions), err)
	}
	if current, err := st.NotificationByIDForUser(ctx, "ws_default", targetID, notification.ID); err != nil || !current.Read {
		t.Fatalf("notification after mark all = %+v, %v", current, err)
	}
	head, err = st.Head(ctx, "ws_default")
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := st.MarkAllNotificationsRead(ctx, "ws_default", targetID); err != nil || changed != 0 {
		t.Fatalf("idempotent mark all changed=%d err=%v", changed, err)
	}
	if after, err := st.Head(ctx, "ws_default"); err != nil || after != head {
		t.Fatalf("idempotent mark all changed head from %d to %d (%v)", head, after, err)
	}
}
