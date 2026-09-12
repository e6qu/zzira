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

// macroElements and macroAttributes are Confluence's structured macro markup:
// the macro, the parameters that configure it, and the body it wraps.
var macroElements = map[string]bool{
	"structured-macro": true, "parameter": true, "rich-text-body": true, "plain-text-body": true,
}

var macroAttributes = map[string]bool{
	"name": true, "macro-id": true, "schema-version": true, "local-id": true,
}

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
	suppressed := 0
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
			// Confluence's storage format carries macros in the ac namespace.
			// They are structure rather than markup: a macro renders as the
			// body it holds, and its parameters describe it without being
			// shown, so none of these elements reach the HTML.
			if t.Name.Space == "ac" {
				if !macroElements[tag] {
					return "", fmt.Errorf("unsupported storage element: ac:%s", tag)
				}
				for _, a := range t.Attr {
					if !macroAttributes[a.Name.Local] {
						return "", fmt.Errorf("unsupported attribute %s on ac:%s", a.Name.Local, tag)
					}
				}
				if tag == "parameter" {
					suppressed++
				}
				continue
			}
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
			if t.Name.Space == "ac" {
				if t.Name.Local == "parameter" {
					suppressed--
				}
				depth--
				continue
			}
			if depth > 1 && t.Name.Local != "br" && t.Name.Local != "hr" {
				b.WriteString("</" + t.Name.Local + ">")
			}
			depth--
		case xml.CharData:
			// A parameter names how a macro behaves; it is not part of what a
			// reader sees, so its text is kept in the stored body and left out
			// of the rendering.
			if suppressed == 0 {
				b.WriteString(html.EscapeString(string(t)))
			}
		case xml.Comment:
			return "", fmt.Errorf("storage comments are not supported")
		default:
			return "", fmt.Errorf("storage directives are not supported")
		}
	}
	return b.String(), nil
}

// Text returns readable plain text from validated storage markup for search
// excerpts and accessible summaries.
func Text(storage string) (string, error) {
	if _, err := Render(storage); err != nil {
		return "", err
	}
	decoder := xml.NewDecoder(strings.NewReader("<root>" + storage + "</root>"))
	var value strings.Builder
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if value.Len() > 0 && typed.Name.Local != "root" && typed.Name.Local != "a" && typed.Name.Local != "strong" && typed.Name.Local != "em" && typed.Name.Local != "b" && typed.Name.Local != "i" && typed.Name.Local != "u" && typed.Name.Local != "s" {
				value.WriteByte(' ')
			}
		case xml.CharData:
			value.Write(typed)
		}
	}
	return strings.Join(strings.Fields(value.String()), " "), nil
}
