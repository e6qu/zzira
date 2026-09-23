package wikimarkup

import (
	"encoding/xml"
	"io"
	"strings"
)

// editableMacros are the macros the rich text editor draws and writes back
// unchanged: a panel around a body, a status word, a code block and a table of
// contents. Anything else is edited as storage so it survives.
var editableMacros = map[string]bool{"info": true, "note": true, "warning": true, "tip": true, "panel": true, "status": true, "code": true, "toc": true}

// editableParameters are the parameters the editor keeps for each of those
// macros. A macro configured in any other way is edited as storage, because
// writing it back would drop what the editor does not hold.
var editableParameters = map[string]map[string]bool{
	"info": {"title": true}, "note": {"title": true}, "warning": {"title": true},
	"tip": {"title": true}, "panel": {"title": true},
	"status": {"title": true, "colour": true},
	"code":   {"language": true},
	"toc":    {},
}

// RichEditable reports whether the rich text editor can hold a storage body
// without losing any of it. The editor keeps text, formatting, links,
// mentions, the macros it draws and page layouts; a body with task lists,
// dates or a macro it does not draw is edited as storage so they survive.
func RichEditable(storage string) bool {
	if _, err := Render(storage); err != nil {
		return false
	}
	decoder := xml.NewDecoder(strings.NewReader("<root>" + storage + "</root>"))
	// The macros being read, so a parameter is judged against the macro it
	// belongs to.
	var macros []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		if end, ok := token.(xml.EndElement); ok {
			if end.Name.Space == "ac" && end.Name.Local == "structured-macro" {
				macros = macros[:len(macros)-1]
			}
			continue
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch {
		case start.Name.Space == "ac" && (start.Name.Local == "link" || start.Name.Local == "plain-text-link-body"):
		case start.Name.Space == "ri" && start.Name.Local == "user":
		case start.Name.Space == "ac" && start.Name.Local == "structured-macro":
			name := strings.ToLower(acAttribute(start, "name"))
			macros = append(macros, name)
			if !editableMacros[name] {
				return false
			}
		case start.Name.Space == "ac" && start.Name.Local == "parameter":
			if len(macros) == 0 || !editableParameters[macros[len(macros)-1]][strings.ToLower(acAttribute(start, "name"))] {
				return false
			}
		case start.Name.Space == "ac" && (start.Name.Local == "rich-text-body" || start.Name.Local == "plain-text-body"):
		case start.Name.Space == "ac" && (start.Name.Local == "layout" || start.Name.Local == "layout-section" || start.Name.Local == "layout-cell"):
		case start.Name.Space != "" || start.Name.Local == "time":
			return false
		}
	}
}
