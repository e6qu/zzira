package commands

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/e6qu/zzira/internal/store"
)

func TestNormalizeLabels(t *testing.T) {
	got, err := normalizeLabels([]string{" parity ", "frontend", "parity", ""})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"frontend", "parity"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeLabels() = %v, want %v", got, want)
	}
	for _, labels := range [][]string{{"two words"}, {string(make([]byte, 256))}} {
		if _, err := normalizeLabels(labels); err == nil {
			t.Fatalf("normalizeLabels(%q) succeeded, want validation error", labels)
		}
	}
}

func TestIssueTriageCommands(t *testing.T) {
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
	svc := &Service{Store: st}
	first, _, err := svc.CreateIssue(ctx, CreateIssueInput{
		ActorID: "usr_test", WorkspaceID: "ws_default", ProjectKey: "ZZ", Summary: "triage first", IssueTypeID: "it_task",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := svc.CreateIssue(ctx, CreateIssueInput{
		ActorID: "usr_test", WorkspaceID: "ws_default", ProjectKey: "ZZ", Summary: "triage second", IssueTypeID: "it_task",
	})
	if err != nil {
		t.Fatal(err)
	}

	if action, err := svc.SetWatching(ctx, "usr_test", "ws_default", first.Key, true); err != nil || action == nil {
		t.Fatalf("SetWatching add action=%v err=%v", action, err)
	}
	if action, err := svc.SetWatching(ctx, "usr_test", "ws_default", first.Key, true); err != nil || action != nil {
		t.Fatalf("SetWatching idempotent action=%v err=%v", action, err)
	}
	watchers, err := st.WatchersByIssue(ctx, first.ID)
	if err != nil || len(watchers) != 1 || watchers[0] != "usr_test" {
		t.Fatalf("WatchersByIssue() = %v, %v", watchers, err)
	}

	link, action, err := svc.LinkIssue(ctx, "usr_test", "ws_default", first.Key, "lt_blocks", second.Key)
	if err != nil || link == nil || action == nil {
		t.Fatalf("LinkIssue link=%v action=%v err=%v", link, action, err)
	}
	if link.OutwardID != first.ID || link.InwardID != second.ID {
		t.Fatalf("LinkIssue direction = inward %q outward %q", link.InwardID, link.OutwardID)
	}
	if _, err := svc.DeleteIssueLink(ctx, "usr_test", "ws_default", first.Key, link.ID); err != nil {
		t.Fatalf("DeleteIssueLink: %v", err)
	}
	if action, err := svc.SetWatching(ctx, "usr_test", "ws_default", first.Key, false); err != nil || action == nil {
		t.Fatalf("SetWatching remove action=%v err=%v", action, err)
	}
}
