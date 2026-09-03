package web

import "testing"

func TestSanitizeLogValueEscapesLineSeparators(t *testing.T) {
	t.Parallel()

	got := sanitizeLogValue("first\r\nsecond\nthird\rfourth")
	if want := `first\r\nsecond\nthird\rfourth`; got != want {
		t.Fatalf("sanitizeLogValue() = %q, want %q", got, want)
	}
}
