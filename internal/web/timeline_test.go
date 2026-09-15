package web

import (
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/models"
)

func TestTimelineLaysWorkAcrossWholeMonths(t *testing.T) {
	epic := models.TimelineItem{Issue: &models.Issue{Key: "ZZ-1"}, StartDate: "2026-10-01", DueDate: "2026-10-31", Children: []models.TimelineItem{
		{Issue: &models.Issue{Key: "ZZ-2"}, StartDate: "2026-11-01"},
		{Issue: &models.Issue{Key: "ZZ-3"}, DueDate: "2026-10-15"},
		{Issue: &models.Issue{Key: "ZZ-4"}},
	}}
	data := newTimelineData(&models.Project{Key: "ZZ"}, models.ProjectTimeline{StartFieldID: "customfield_1", Epics: []models.TimelineItem{epic}}, time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC), "02/Jan/06")
	// October to November, stretched to the three-month minimum.
	if len(data.Months) != 3 || data.Months[0].Label != "Oct 2026" || data.Months[2].Label != "Dec 2026" || data.Width != 480 {
		t.Fatalf("months = %+v width=%v", data.Months, data.Width)
	}
	if len(data.Rows) != 4 || data.Rows[0].Child || !data.Rows[1].Child {
		t.Fatalf("rows = %+v", data.Rows)
	}
	// October is 31 of the 92 days shown.
	if bar := data.Rows[0].Bar; bar == nil || bar.X != 0 || bar.Width != 161.7 || bar.Open {
		t.Fatalf("epic bar = %+v", bar)
	}
	if bar := data.Rows[1].Bar; bar == nil || !bar.Open || bar.X != 161.7 || bar.Width != 318.3 {
		t.Fatalf("open-ended bar = %+v", bar)
	}
	if bar := data.Rows[2].Bar; bar == nil || !bar.Open || bar.X != 0 {
		t.Fatalf("due-only bar = %+v", bar)
	}
	if data.Rows[3].Bar != nil || !data.Rows[3].Unscheduled || data.Rows[0].StartLabel != "01/Oct/26" {
		t.Fatalf("unscheduled row = %+v", data.Rows[3])
	}
	if !data.ShowToday || data.TodayX != 78.3 {
		t.Fatalf("today = %v at %v", data.ShowToday, data.TodayX)
	}

	empty := newTimelineData(&models.Project{Key: "ZZ"}, models.ProjectTimeline{}, time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC), "2006-01-02")
	if len(empty.Months) != 3 || empty.Months[0].Label != "Jan 2026" || len(empty.Rows) != 0 {
		t.Fatalf("empty timeline = %+v", empty)
	}
}
