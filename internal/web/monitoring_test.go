package web

import (
	"net/http/httptest"
	"testing"
)

func TestMonitoringRejectsMissingOrWrongToken(t *testing.T) {
	t.Setenv("ZZIRA_MONITORING_TOKEN", "correct-token-at-least-32-characters-long")
	h := &Handler{}

	for _, auth := range []string{"", "Bearer ", "Bearer wrong-token", "correct-token-at-least-32-characters-long"} {
		req := httptest.NewRequest("GET", "/monitoring/observation", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.Monitoring(rec, req)
		if rec.Code != 401 {
			t.Fatalf("Authorization %q: expected 401, got %d", auth, rec.Code)
		}
	}
}

func TestMonitoringRejectsWhenTokenNotConfigured(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest("GET", "/monitoring/observation", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()
	h.Monitoring(rec, req)
	if rec.Code != 401 {
		t.Fatalf("expected 401 when ZZIRA_MONITORING_TOKEN is unset, got %d", rec.Code)
	}
}
