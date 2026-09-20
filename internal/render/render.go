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
	"strconv"
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
		"site": siteLook,
		"dec":  func(value int) int { return value - 1 },
		"inc":  func(value int) int { return value + 1 },
		// hasString reports whether a list holds a value, which a template
		// cannot ask on its own.
		"hasString": func(list []string, value string) bool {
			for _, item := range list {
				if item == value {
					return true
				}
			}
			return false
		},
		"wikiHTML": func(storage string) (template.HTML, error) {
			value, err := wikimarkup.Render(storage)
			return template.HTML(value), err // #nosec G203 -- strict tag/attribute validation and escaping in wikimarkup.
		},
		// wikiPermission reads a space permission key the way Confluence names
		// it: "create/blogpost" is "Create blog post". The key itself is still
		// shown beside it, because that is what the API speaks.
		"wikiPermission": func(key string) string {
			operation, target, found := strings.Cut(key, "/")
			if !found {
				return key
			}
			// The content targets read as plurals, the way the space
			// permission form has always named them ("Update pages"), and the
			// space itself stays singular.
			targets := map[string]string{
				"space": "space", "page": "pages", "blogpost": "blog posts", "comment": "comments",
				"attachment": "attachments", "folder": "folders", "embed": "Smart Links",
				"database": "databases", "whiteboard": "whiteboards", "custom": "custom content",
			}
			name, ok := targets[target]
			if !ok {
				name = target
			}
			if operation == "" {
				return name
			}
			return strings.ToUpper(operation[:1]) + operation[1:] + " " + name
		},
		// planNumber shows a plan estimate or capacity without trailing zeros.
		"planNumber": func(value any) string {
			switch number := value.(type) {
			case float64:
				return strconv.FormatFloat(number, 'f', -1, 64)
			case *float64:
				if number == nil {
					return ""
				}
				return strconv.FormatFloat(*number, 'f', -1, 64)
			}
			return fmt.Sprint(value)
		},
		// deref64 reads an optional whole number.
		"deref64": func(value *int64) int64 {
			if value == nil {
				return 0
			}
			return *value
		},
		// planMax is the top of a capacity meter: the capacity, or the planned
		// work when it is more.
		"planMax": func(capacity, planned float64) float64 {
			if planned > capacity {
				return planned
			}
			if capacity <= 0 {
				return 1
			}
			return capacity
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
		"slaMinutes": func(millis int64) int64 { return millis / 60000 },
		"clockMinute": func(minute int16) string {
			return fmt.Sprintf("%02d:%02d", minute/60, minute%60)
		},
		"workingDay": func(days []int16, day int) bool {
			for _, configured := range days {
				if int(configured) == day {
					return true
				}
			}
			return false
		},
		"join":            strings.Join,
		"selectedVersion": func(csv, id string) bool { return slices.Contains(strings.Split(csv, ","), id) },
		// customFieldControl passes a custom field to its control with the
		// element id prefix and the input name, which defaults to the field id.
		"customFieldControl": func(field any, prefix, name string) map[string]any {
			return map[string]any{"Field": field, "Prefix": prefix, "Name": name}
		},
		// detailField passes one create-form field to its control along with the
		// values and flags the control needs, so tabs and the plain list render
		// the same markup.
		"detailField": func(field any, values any, subtask bool) map[string]any {
			return map[string]any{"Field": field, "Values": values, "SelectedIssueTypeSubtask": subtask}
		},
		// holds reports whether a list of ids contains one, for checkboxes that
		// show what a scheme already carries.
		"holds": func(ids []string, id string) bool { return slices.Contains(ids, id) },
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
