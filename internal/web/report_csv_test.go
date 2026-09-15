package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReportCSVIsSafeToOpenInASpreadsheet(t *testing.T) {
	for value, want := range map[string]string{"=SUM(A1)": "'=SUM(A1)", "+1": "'+1", "-2": "'-2", "@cmd": "'@cmd", "Plan": "Plan", "": ""} {
		if got := spreadsheetSafe(value); got != want {
			t.Fatalf("spreadsheetSafe(%q) = %q, want %q", value, got, want)
		}
	}
	request := httptest.NewRequest("GET", "/projects/ZZ/reports/created-vs-resolved?days=7&cumulative=true", nil)
	if got := csvURL(request); got != "/projects/ZZ/reports/created-vs-resolved?cumulative=true&days=7&format=csv" {
		t.Fatalf("csvURL = %q", got)
	}
	if !wantsCSV(httptest.NewRequest("GET", "/x?format=csv", nil)) || wantsCSV(request) {
		t.Fatal("wantsCSV")
	}
	response := httptest.NewRecorder()
	writeReportCSV(response, "ZZ created vs. resolved", []string{"Date", "Summary"}, [][]string{{"2026-09-15", "=cmd,with comma"}})
	if response.Header().Get("Content-Type") != "text/csv; charset=utf-8" || !strings.HasPrefix(response.Header().Get("Content-Disposition"), `attachment; filename="ZZ-created-vs.-resolved-`) {
		t.Fatalf("headers = %v", response.Header())
	}
	if body := response.Body.String(); body != "Date,Summary\n2026-09-15,\"'=cmd,with comma\"\n" {
		t.Fatalf("body = %q", body)
	}
}
