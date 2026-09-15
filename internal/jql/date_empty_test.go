package jql

import (
	"strings"
	"testing"
)

func TestDateFieldEmptinessIsAbsence(t *testing.T) {
	for _, source := range []string{"duedate is EMPTY", "due is not EMPTY", "resolutiondate is EMPTY", "resolved is not EMPTY"} {
		query, err := Parse(source)
		if err != nil {
			t.Fatal(err)
		}
		compiled := Compile(query, "u1", DefaultResolver())
		if compiled.Err != nil || strings.Contains(compiled.Where, "''") || !strings.Contains(compiled.Where, "NULL") {
			t.Fatalf("%s compiled to %q, %v", source, compiled.Where, compiled.Err)
		}
	}
}
