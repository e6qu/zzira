package api3

import (
	"regexp"
	"strings"
)

type anonymousOperationRow struct {
	Method  string
	Path    string
	Pattern string
}

var anonymousOperationPatterns = func() []*regexp.Regexp {
	patterns := make([]*regexp.Regexp, len(anonymousOperationTable))
	for i, row := range anonymousOperationTable {
		patterns[i] = regexp.MustCompile(row.Pattern)
	}
	return patterns
}()

// anonymousOperation reports whether Jira lets a caller without credentials
// use the operation, as Jira's anonymous user.
func anonymousOperation(method, path string) bool {
	if !strings.HasPrefix(path, "/rest/api/3/") {
		return false
	}
	for i, row := range anonymousOperationTable {
		if row.Method == method && anonymousOperationPatterns[i].MatchString(path) {
			return true
		}
	}
	return false
}
