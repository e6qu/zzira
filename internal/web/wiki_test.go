package web

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"
)

func TestWikiWebErrorEscapesLogInput(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	status, message := wikiWebError(errors.New("database value\r\nforged entry\x1b[31m"))
	if status != 500 || strings.Contains(message, "database value") {
		t.Fatalf("unexpected error response: %d %q", status, message)
	}
	logged := output.String()
	if strings.Count(logged, "\n") != 1 || strings.ContainsAny(logged, "\r\x1b") || !strings.Contains(logged, `database value\r\nforged entry\x1b[31m`) {
		t.Fatalf("unsafe log entry: %q", logged)
	}
}
