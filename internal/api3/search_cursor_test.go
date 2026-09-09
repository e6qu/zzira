package api3

import (
	"testing"
	"time"
)

func TestEnhancedSearchCursorBindsQueryWorkspaceUserAndExpiry(t *testing.T) {
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	token := encodeEnhancedSearchCursor(enhancedSearchCursor{
		Version: 2, SnapshotID: "search_one", Position: 75,
		QueryHash: enhancedSearchQueryHash(" project = ZZ ", []int64{10002, 10001}),
		Workspace: "ws_one", User: "usr_one", ExpiresAt: now.Add(7 * 24 * time.Hour).Unix(),
	})
	cursor, err := decodeEnhancedSearchCursor(token, "project = ZZ", "ws_one", "usr_one", []int64{10001, 10002}, now.Add(6*24*time.Hour))
	if err != nil || cursor.Position != 75 || cursor.SnapshotID != "search_one" {
		t.Fatalf("round trip cursor=%+v err=%v", cursor, err)
	}
	for _, mismatch := range []struct {
		query, workspace, user string
		now                    time.Time
	}{
		{"project = OTHER", "ws_one", "usr_one", now},
		{"project = ZZ", "ws_two", "usr_one", now},
		{"project = ZZ", "ws_one", "usr_two", now},
		{"project = ZZ", "ws_one", "usr_one", now.Add(8 * 24 * time.Hour)},
	} {
		if _, err := decodeEnhancedSearchCursor(token, mismatch.query, mismatch.workspace, mismatch.user, []int64{10001, 10002}, mismatch.now); err == nil {
			t.Fatalf("cursor accepted mismatch %+v", mismatch)
		}
	}
	if _, err := decodeEnhancedSearchCursor(token, "project = ZZ", "ws_one", "usr_one", []int64{10003}, now); err == nil {
		t.Fatal("cursor accepted a different reconciliation set")
	}
	if _, err := decodeEnhancedSearchCursor("LTE", "project = ZZ", "ws_one", "usr_one", nil, now); err == nil {
		t.Fatal("legacy raw offset token was accepted")
	}
}
