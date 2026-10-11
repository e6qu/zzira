// Package fieldrender applies field configuration renderers without changing
// the stored value. All returned markup is escaped or sanitized by construction.
package fieldrender

import (
	"encoding/json"
	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/wikimarkup"
	"html"
	"strings"
)

func HTML(value any, renderer string) string {
	var raw json.RawMessage
	if encoded, ok := value.(json.RawMessage); ok {
		raw = encoded
	} else {
		raw, _ = json.Marshal(value)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if renderer == "atlassian-wiki-renderer" {
			markup, err := wikimarkup.Render(wikimarkup.FromNotation(text))
			if err == nil {
				return markup
			}
		}
		return strings.ReplaceAll(html.EscapeString(text), "\n", "<br>")
	}
	var document struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &document) != nil || document.Type != "doc" {
		return ""
	}
	if renderer == "jira-text-renderer" {
		return strings.ReplaceAll(html.EscapeString(adf.PlainText(raw)), "\n", "<br>")
	}
	return adf.ToHTML(raw)
}
