// Package wikimarkup validates and renders the supported Confluence storage
// format. It is pure Go and shared by the server and WASM renderer.
package wikimarkup

import (
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/url"
	"strings"
)

var tags = map[string]bool{"p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "ul": true, "ol": true, "li": true, "blockquote": true, "pre": true, "code": true, "strong": true, "em": true, "b": true, "i": true, "u": true, "s": true, "a": true, "br": true, "hr": true, "table": true, "thead": true, "tbody": true, "tr": true, "th": true, "td": true}

// Render rejects unsupported markup rather than silently dropping content.
// Every emitted tag is allowlisted and every text/attribute is escaped.
func Render(storage string) (string, error) {
	if len(storage) > 1<<20 {
		return "", fmt.Errorf("page body must be at most 1 MiB")
	}
	d := xml.NewDecoder(strings.NewReader("<root>" + storage + "</root>"))
	var b strings.Builder
	depth := 0
	rootSeen := false
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("invalid storage markup: %w", err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			depth++
			if depth > 100 {
				return "", fmt.Errorf("page markup is nested too deeply")
			}
			if depth == 1 {
				if rootSeen {
					return "", fmt.Errorf("storage must be one content fragment")
				}
				rootSeen = true
				continue
			}
			tag := t.Name.Local
			if t.Name.Space != "" || !tags[tag] {
				return "", fmt.Errorf("unsupported storage element: %s", tag)
			}
			b.WriteString("<" + tag)
			for _, a := range t.Attr {
				if tag != "a" || a.Name.Local != "href" || a.Name.Space != "" {
					return "", fmt.Errorf("unsupported attribute %s on %s", a.Name.Local, tag)
				}
				u, err := url.Parse(a.Value)
				if err != nil || u.User != nil || strings.HasPrefix(a.Value, "//") || strings.ContainsAny(a.Value, "\\\r\n\t") || (u.Scheme != "" && u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "mailto") {
					return "", fmt.Errorf("links must use http, https, mailto or a relative page URL")
				}
				b.WriteString(` href="` + html.EscapeString(a.Value) + `"`)
			}
			b.WriteString(">")
		case xml.EndElement:
			if depth > 1 && t.Name.Local != "br" && t.Name.Local != "hr" {
				b.WriteString("</" + t.Name.Local + ">")
			}
			depth--
		case xml.CharData:
			b.WriteString(html.EscapeString(string(t)))
		case xml.Comment:
			return "", fmt.Errorf("storage comments are not supported")
		default:
			return "", fmt.Errorf("storage directives are not supported")
		}
	}
	return b.String(), nil
}
