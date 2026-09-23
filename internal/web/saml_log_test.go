package web

import (
	"strings"
	"testing"
)

// A refusal says why single sign-on failed, and the reason comes from an
// assertion somebody else wrote. It reaches the log as one printable line, so
// nothing in it can pass itself off as another entry.
func TestLogLineKeepsAValueToOnePrintableLine(t *testing.T) {
	got := logLine("audience \"urn:x\" is not this site\nFATAL: site compromised\r\nmore")
	if strings.ContainsAny(got, "\n\r") {
		t.Fatalf("a line break survived: %q", got)
	}
	if got != `audience "urn:x" is not this site FATAL: site compromised more` {
		t.Fatalf("logLine = %q", got)
	}
	if control := logLine("start\x00\x07\x1bend"); control != "start end" {
		t.Fatalf("control characters survived: %q", control)
	}
	// A long reason is cut by characters, so the cut never lands inside one.
	long := logLine(strings.Repeat("é", 600))
	if runes := []rune(long); len(runes) != 501 || runes[500] != '…' {
		t.Fatalf("a long reason was cut to %d characters", len([]rune(long)))
	}
}
