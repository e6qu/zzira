package store

import "testing"

// Built-in events keep Jira's EventType ids, which notification schemes,
// workflow transitions and REST clients refer to.
func TestNotificationEventsUseJiraIDs(t *testing.T) {
	want := map[int64]string{
		1: "Issue created", 2: "Issue updated", 3: "Issue assigned", 4: "Issue resolved",
		5: "Issue closed", 6: "Issue commented", 7: "Issue reopened", 8: "Issue deleted",
		9: "Issue moved", 10: "Work logged on issue", 11: "Work started on issue",
		12: "Work stopped on issue", 13: "Generic event", 14: "Issue comment edited",
		15: "Issue worklog updated", 16: "Issue worklog deleted", 17: "Issue comment deleted",
	}
	if len(NotificationEvents()) != len(want) {
		t.Fatalf("built-in events = %d, want %d", len(NotificationEvents()), len(want))
	}
	for id, name := range want {
		if event, ok := NotificationEvent(id); !ok || event.Name != name {
			t.Errorf("event %d = %q, want %q", id, event.Name, name)
		}
	}
}
