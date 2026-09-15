package web

import (
	"net/http/httptest"
	"testing"
)

func TestReportComparisonsDescribeTheChangeFromThePreviousPeriod(t *testing.T) {
	for got, want := range map[string]string{
		compareCount(5, 3, 7):             "Up 2 from 3 in the previous 7 days",
		compareCount(1, 4, 30):            "Down 3 from 4 in the previous 30 days",
		compareCount(2, 2, 90):            "Same as the previous 90 days",
		compareDuration(0, 0, 7):          "Nothing to measure in the previous 7 days",
		compareDuration(0, 7200, 7):       "Was 2h in the previous 7 days",
		compareDuration(90000, 86400, 14): "Longer by 1h than 1d in the previous 14 days",
		compareDuration(3600, 7200, 14):   "Shorter by 1h than 2h in the previous 14 days",
		compareRate(25, 12.5, 8, 30, "%"): "Up 12.5% from 12.5% in the previous 30 days",
		compareRate(3.5, 4, 2, 30, ""):    "Down 0.5 from 4.0 in the previous 30 days",
		compareRate(3.5, 0, 0, 30, ""):    "Nothing to measure in the previous 30 days",
		compareRate(50.04, 50, 3, 7, "%"): "Same as the previous 7 days",
	} {
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if !wantsComparison(httptest.NewRequest("GET", "/x?compare=previous", nil)) || wantsComparison(httptest.NewRequest("GET", "/x?compare=1", nil)) {
		t.Fatal("wantsComparison")
	}
}
