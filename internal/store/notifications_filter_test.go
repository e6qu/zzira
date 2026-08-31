package store

import (
	"context"
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
