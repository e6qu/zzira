package models

import "testing"

func TestJavaDateLayout(t *testing.T) {
	for pattern, want := range map[string]string{
		"dd/MMM/yy h:mm a": "02/Jan/06 3:04 PM",
		"EEEE h:mm a":      "Monday 3:04 PM",
		"yyyy-MM-dd HH:mm": "2006-01-02 15:04",
		"d/MMM/yy":         "2/Jan/06",
		"dd 'at' HH:mm":    "02 at 15:04",
	} {
		if layout, ok := JavaDateLayout(pattern); !ok || layout != want {
			t.Errorf("JavaDateLayout(%q) = %q %v, want %q", pattern, layout, ok, want)
		}
	}
	for _, pattern := range []string{"", "qq", "dd 'on Mon'", "dd 'unclosed", "yyyy1"} {
		if layout, ok := JavaDateLayout(pattern); ok {
			t.Errorf("JavaDateLayout(%q) = %q, want refused", pattern, layout)
		}
	}
	if !ValidJavaScriptDateFormat("%e/%b/%y %I:%M %p") || ValidJavaScriptDateFormat("%Q") || ValidJavaScriptDateFormat("%") {
		t.Error("browser date format validation is wrong")
	}
	complete, day := SiteDateLayouts(map[string]string{"jira.lf.date.complete": "yyyy-MM-dd HH:mm", "jira.lf.date.dmy": "qq"})
	if complete != "2006-01-02 15:04" || day != DefaultDayDateLayout {
		t.Errorf("site layouts = %q %q", complete, day)
	}
}
