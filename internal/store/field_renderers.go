package store

// NormalizeFieldRenderer accepts the legacy API's short plugin names and the
// newer field scheme API's rendererType identifiers. Unknown plugins are
// refused so configuration cannot promise an unavailable renderer.
func NormalizeFieldRenderer(renderer string) (string, bool) {
	switch renderer {
	case "":
		return "", true
	case "wiki-renderer", "atlassian-wiki-renderer":
		return "atlassian-wiki-renderer", true
	case "text-renderer", "jira-text-renderer":
		return "jira-text-renderer", true
	default:
		return "", false
	}
}

func rendererValue(renderer *string) string {
	if renderer == nil {
		return ""
	}
	return *renderer
}
