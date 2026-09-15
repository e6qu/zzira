package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestReportTargetKeepsOnlyReportPagesAndTheirChoices(t *testing.T) {
	for _, tc := range []struct{ path, query, want string }{
		{"/projects/ZZ/reports/velocity", "board=12&format=csv&emailError=access", "/projects/ZZ/reports/velocity?board=12"},
		{"/projects/ZZ/reports/created-vs-resolved", "days=7&cumulative=true", "/projects/ZZ/reports/created-vs-resolved?cumulative=true&days=7"},
		{"/service/agent/desk_1/reports", "", "/service/agent/desk_1/reports"},
	} {
		if got, err := reportTarget(tc.path, tc.query); err != nil || got != tc.want {
			t.Fatalf("reportTarget(%q, %q) = %q, %v", tc.path, tc.query, got, err)
		}
	}
	for _, path := range []string{"/projects/ZZ/reports", "/projects/ZZ/reports/apps/x", "//evil.test/projects/ZZ/reports/dora", "/admin/users", "/projects/ZZ/reports/dora/../../admin"} {
		if _, err := reportTarget(path, ""); err == nil {
			t.Fatalf("reportTarget accepted %q", path)
		}
	}
}

func TestRenderReportDrawsTheDownloadAsTheRecipient(t *testing.T) {
	var seen string
	h := &Handler{ReportRoutes: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = r.Context().Value(reportRecipientKey{}).(string)
		if r.URL.Query().Get("format") != "csv" || r.URL.Query().Get("board") != "12" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if seen != "acct-ana" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		writeReportCSV(w, "ZZ velocity chart", []string{"Sprint", "Commitment", "Completed"}, [][]string{{"Sprint 1", "5", "3"}})
	})}
	title, data, err := h.RenderReport(context.Background(), "acct-ana", "/projects/ZZ/reports/velocity?board=12")
	if err != nil || title != "ZZ velocity chart" || data != "Sprint,Commitment,Completed\nSprint 1,5,3\n" || seen != "acct-ana" {
		t.Fatalf("RenderReport = %q, %q, %v (as %q)", title, data, err, seen)
	}
	if _, _, err := h.RenderReport(context.Background(), "acct-bo", "/projects/ZZ/reports/velocity?board=12"); err == nil {
		t.Fatal("a recipient who cannot open the report received it")
	}
	if _, _, err := h.RenderReport(context.Background(), "acct-ana", "/admin/users"); err == nil || !strings.Contains(err.Error(), "not a report") {
		t.Fatalf("non-report target error = %v", err)
	}
}
