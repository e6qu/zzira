// Package render is the single HTML renderer for ZZIRA. It is compiled into both
// the server binary and the client wasm worker and must stay pure: no net/http,
// no database, no os I/O beyond embedded templates.
package render

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"slices"
	"strings"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/wikimarkup"
)

//go:embed templates/*.gohtml
var files embed.FS

var set *template.Template

func init() {
	funcs := template.FuncMap{
		"wikiHTML": func(storage string) (template.HTML, error) {
			value, err := wikimarkup.Render(storage)
			return template.HTML(value), err // #nosec G203 -- strict tag/attribute validation and escaping in wikimarkup.
		},
		"statusClass": func(category string) string {
			switch category {
			case "done":
				return "lozenge-success"
			case "indeterminate":
				return "lozenge-current"
			default:
				return "lozenge-default"
			}
		},
		"initials": func(name string) string {
			parts := strings.Fields(name)
			if len(parts) == 0 {
				return "?"
			}
			if len(parts) == 1 {
				return strings.ToUpper(parts[0][:1])
			}
			return strings.ToUpper(parts[0][:1] + parts[len(parts)-1][:1])
		},
		"adfToText": adfToText,
		"adfHTML": func(raw json.RawMessage) template.HTML {
			// Safe by construction: adf.ToHTML html-escapes every text node and
			// attribute value (see internal/adf tests); output is golden-locked.
			return template.HTML(adf.ToHTML(raw)) // #nosec G203 -- reviewed trust boundary
		},
		"timeSpent": func(seconds int) string {
			return models.TimeSpentLabel(seconds)
		},
		"join":            strings.Join,
		"selectedVersion": func(csv, id string) bool { return slices.Contains(strings.Split(csv, ","), id) },
		"humanSize": func(n int64) string {
			const kb, mb = 1 << 10, 1 << 20
			switch {
			case n >= mb:
				return fmt.Sprintf("%.1f MB", float64(n)/mb)
			case n >= kb:
				return fmt.Sprintf("%.1f KB", float64(n)/kb)
			default:
				return fmt.Sprintf("%d B", n)
			}
		},
	}
	t, err := template.New("zzira").Funcs(funcs).ParseFS(files, "templates/*.gohtml")
	if err != nil {
		panic(fmt.Sprintf("render: parse templates: %v", err))
	}
	set = t
}

// Fragment writes the named template block (an HTMX-swappable piece of HTML).
func Fragment(w io.Writer, name string, data any) error {
	return set.ExecuteTemplate(w, name, data)
}

// Page writes a full HTML document. Pages embed fragments by name.
func Page(w io.Writer, name string, data any) error {
	return set.ExecuteTemplate(w, name, data)
}

func adfToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var doc struct {
		Content []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	var b strings.Builder
	for _, block := range doc.Content {
		for _, inline := range block.Content {
			b.WriteString(inline.Text)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
