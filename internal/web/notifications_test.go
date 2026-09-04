package web

import (
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

func TestNotificationDestinationStaysOnFixedApplicationRoutes(t *testing.T) {
	notification := &models.Notification{EntityType: models.EntityIssue, EntityID: "../outside?next=https://example.test"}
	if got, want := notificationDestination(notification), "/browse/..%2Foutside%3Fnext=https:%2F%2Fexample.test"; got != want {
		t.Fatalf("notification destination = %q, want %q", got, want)
	}
	notification.EntityType = "unknown"
	if got := notificationDestination(notification); got != "/notifications" {
		t.Fatalf("unknown notification destination = %q", got)
	}
}

func TestNotificationTimeLabel(t *testing.T) {
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		created string
		want    string
	}{
		{"2026-09-04T11:59:45Z", "Just now"},
		{"2026-09-04T11:42:00Z", "18m ago"},
		{"2026-09-04T09:00:00Z", "3h ago"},
		{"2026-09-03T08:00:00Z", "Yesterday"},
		{"2026-08-28T08:00:00Z", "Aug 28"},
		{"2025-08-28T08:00:00Z", "Aug 28, 2025"},
		{"not-a-time", "not-a-time"},
	} {
		if got := notificationTimeLabel(test.created, now); got != test.want {
			t.Errorf("notificationTimeLabel(%q) = %q, want %q", test.created, got, test.want)
		}
	}
}

func TestNotificationsPageURLPreservesView(t *testing.T) {
	if got := notificationsPageURL("all", 1); got != "/notifications" {
		t.Fatalf("first all page URL = %q", got)
	}
	if got := notificationsPageURL("unread", 3); got != "/notifications?page=3&view=unread" {
		t.Fatalf("unread page URL = %q", got)
	}
}
