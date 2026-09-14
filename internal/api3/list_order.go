package api3

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// orderByNameOrID applies Jira's name and id orderings to a list. A leading +
// arrives as a space once the query string is decoded, so both are accepted.
func orderByNameOrID[T any](w http.ResponseWriter, raw string, items []T, name, id func(T) string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	field := strings.TrimLeft(raw, "+- ")
	if field != "name" && field != "id" {
		jiraError(w, http.StatusBadRequest, "orderBy must be name or id, optionally prefixed with + or -.")
		return false
	}
	descending := strings.HasPrefix(raw, "-")
	sort.SliceStable(items, func(i, j int) bool {
		left, right := id(items[i]), id(items[j])
		if field == "name" {
			left, right = strings.ToLower(name(items[i])), strings.ToLower(name(items[j]))
		} else if a, errA := strconv.ParseInt(left, 10, 64); errA == nil {
			if b, errB := strconv.ParseInt(right, 10, 64); errB == nil {
				if descending {
					return a > b
				}
				return a < b
			}
		}
		if descending {
			return left > right
		}
		return left < right
	})
	return true
}

// pageOf is Jira's page bean for a complete, unpaged list.
func pageOf[T any](values []T) map[string]any {
	return map[string]any{"isLast": true, "maxResults": len(values), "startAt": 0, "total": len(values), "values": values}
}
