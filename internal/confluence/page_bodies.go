package confluence

import (
	"encoding/json"
	"net/http"
	"slices"

	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// A page body is written as storage, the document format or wiki markup, flat
// (`representation` and `value`) or nested under the name of its format, and
// is kept as storage. It can be read back in any of Confluence's primary
// formats.

var pageListBodyFormats = []string{"storage", "atlas_doc_format"}
var pageBodyFormats = []string{"storage", "atlas_doc_format", "view", "export_view", "anonymous_export_view", "styled_view", "editor"}

// pageBodyFormat reads `body-format`: a single page may ask for any primary
// format, a page list for storage or the document format.
func pageBodyFormat(w http.ResponseWriter, r *http.Request, single bool) (string, bool) {
	format := r.URL.Query().Get("body-format")
	allowed := pageListBodyFormats
	if single {
		allowed = pageBodyFormats
	}
	if format != "" && !slices.Contains(allowed, format) {
		failure(w, 400, "Unsupported body format.")
		return "", false
	}
	return format, true
}

// pageBeanWithFormat renders a page with its body in the requested format.
func (h *Handler) pageBeanWithFormat(p *models.WikiPage, format string) (map[string]any, error) {
	bean := h.pageBean(p, false)
	if format == "" {
		return bean, nil
	}
	value := p.Body.Value
	if format != "storage" {
		target := format
		if target == "anonymous_export_view" {
			target = "export_view"
		}
		converted, err := store.ConvertWikiBody(p.Body.Value, "storage", target)
		if err != nil {
			return nil, err
		}
		value = converted
	}
	bean["body"] = map[string]any{format: models.WikiBody{Representation: format, Value: value}}
	return bean, nil
}

type pageBodyWrite struct {
	Representation string `json:"representation"`
	Value          string `json:"value"`
}

// decodePageBody reads either body form and converts it to storage.
func decodePageBody(raw json.RawMessage) (models.WikiBody, string) {
	body := models.WikiBody{Representation: "storage"}
	if len(raw) == 0 || string(raw) == "null" {
		return body, ""
	}
	var flat pageBodyWrite
	if err := json.Unmarshal(raw, &flat); err != nil {
		return body, "The page body must be an object."
	}
	if flat.Representation == "" && flat.Value == "" {
		var nested map[string]pageBodyWrite
		if err := json.Unmarshal(raw, &nested); err != nil {
			return body, "The page body must be an object."
		}
		if len(nested) != 1 {
			return body, "Give the page body in exactly one format."
		}
		for format, value := range nested {
			flat = value
			if flat.Representation == "" {
				flat.Representation = format
			}
			if flat.Representation != format {
				return body, "The nested body representation must match its format."
			}
		}
	}
	switch flat.Representation {
	case "storage", "":
		body.Value = flat.Value
	case "atlas_doc_format", "wiki":
		converted, err := store.ConvertWikiBody(flat.Value, flat.Representation, "storage")
		if err != nil {
			return body, err.Error()
		}
		body.Value = converted
	default:
		return body, "The page body must be storage, atlas_doc_format or wiki."
	}
	return body, ""
}

// pageWebResources names what a client needs to render page content the way
// this site does: its stylesheets and the script that enhances rendered
// content.
func pageWebResources(base string) map[string]any {
	css := `<link rel="stylesheet" href="` + base + `/static/css/tokens.css">` +
		`<link rel="stylesheet" href="` + base + `/static/css/workspace.css">`
	js := `<script src="` + base + `/static/app.js" defer></script>`
	tags := map[string]string{"css": css, "js": js, "data": ""}
	return map[string]any{
		"contexts": []string{"main", "page"}, "keys": []string{"workspace-styles", "workspace-app"},
		"tags": tags, "superbatch": map[string]any{"tags": tags, "metatags": ""},
	}
}

func stringOrNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}
