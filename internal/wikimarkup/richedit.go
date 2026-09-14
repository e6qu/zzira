package wikimarkup

import (
	"encoding/xml"
	"io"
	"strings"
)

// RichEditable reports whether the rich text editor can hold a storage body
// without losing any of it. The editor keeps text, formatting, links and
// mentions; a body with macros, task lists or dates is edited as storage so
// they survive.
func RichEditable(storage string) bool {
	if _, err := Render(storage); err != nil {
		return false
	}
	decoder := xml.NewDecoder(strings.NewReader("<root>" + storage + "</root>"))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch {
		case start.Name.Space == "ac" && (start.Name.Local == "link" || start.Name.Local == "plain-text-link-body"):
		case start.Name.Space == "ri" && start.Name.Local == "user":
		case start.Name.Space != "" || start.Name.Local == "time":
			return false
		}
	}
}
