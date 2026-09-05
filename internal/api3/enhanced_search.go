package api3

import "strings"

// Enhanced search defaults to IDs only, unlike the legacy search endpoint.
func enhancedIssueFields(bean map[string]any, requested []string) map[string]any {
	if len(requested) == 0 || (len(requested) == 1 && requested[0] == "id") {
		return map[string]any{"id": bean["id"]}
	}
	all := false
	include, exclude := map[string]bool{}, map[string]bool{}
	positive := false
	for _, raw := range requested {
		field := strings.TrimSpace(raw)
		if strings.HasPrefix(field, "-") {
			exclude[strings.TrimPrefix(field, "-")] = true
			continue
		}
		positive = true
		if field == "*all" || field == "*navigable" {
			all = true
		} else {
			include[field] = true
		}
	}
	if !positive {
		all = true
	}
	fields := map[string]any{}
	for field, value := range bean["fields"].(map[string]any) {
		if (all || include[field]) && !exclude[field] {
			fields[field] = value
		}
	}
	out := map[string]any{"id": bean["id"]}
	if all || include["key"] || len(fields) > 0 {
		out["key"] = bean["key"]
		out["self"] = bean["self"]
	}
	if all || len(fields) > 0 {
		out["fields"] = fields
	}
	return out
}
