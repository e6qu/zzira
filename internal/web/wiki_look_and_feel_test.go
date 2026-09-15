package web

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

func TestWikiLookFor(t *testing.T) {
	if look := wikiLookFor(nil); look != nil {
		t.Fatalf("the default look = %+v", look)
	}
	look := wikiLookFor(map[string]any{
		"headings":           map[string]any{"color": "#123456"},
		"links":              map[string]any{"color": "rgb(0, 82, 204)"},
		"bordersAndDividers": map[string]any{"color": "red;} body{display:none"},
		"header":             map[string]any{"backgroundColor": "#0747A6", "primaryNavigation": map[string]any{"color": "white"}},
	})
	if look == nil || *look != (wikiLookView{HeaderBackground: "#0747A6", HeaderText: "white", Heading: "#123456", Link: "rgb(0, 82, 204)"}) {
		t.Fatalf("look = %+v", look)
	}
	if none := wikiLookFor(map[string]any{"headings": map[string]any{"color": "url(javascript:alert(1))"}}); none != nil {
		t.Fatalf("a look with no plain colours = %+v", none)
	}
	// Even a value that slipped past the colour check stays a value: the
	// template escapes it inside the fixed rule.
	page := template.Must(template.New("style").Parse(`<style>.page-wiki main a{color:{{.}}}</style>`))
	var out bytes.Buffer
	if err := page.Execute(&out, "red} body{display:none"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "display:none") {
		t.Fatalf("the template wrote a stored value as CSS: %s", out.String())
	}
}
