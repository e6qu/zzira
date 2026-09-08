package api3

import (
	"testing"
	"time"
)

func TestEnhancedSearchCursorBindsQueryWorkspaceUserAndExpiry(t *testing.T) {
	now := time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC)
	token := encodeEnhancedSearchCursor(75, " project = ZZ ", "ws_one", "usr_one", now)
	offset, err := decodeEnhancedSearchCursor(token, "project = ZZ", "ws_one", "usr_one", now.Add(6*24*time.Hour))
	if err != nil || offset != 75 {
		t.Fatalf("round trip offset=%d err=%v", offset, err)
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
		if _, err := decodeEnhancedSearchCursor(token, mismatch.query, mismatch.workspace, mismatch.user, mismatch.now); err == nil {
			t.Fatalf("cursor accepted mismatch %+v", mismatch)
		}
	}
	if _, err := decodeEnhancedSearchCursor("LTE", "project = ZZ", "ws_one", "usr_one", now); err == nil {
		t.Fatal("legacy raw offset token was accepted")
	}
}
