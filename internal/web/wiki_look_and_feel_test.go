package web

import (
	"strings"
	"testing"
)

func TestWikiLookAndFeelCSS(t *testing.T) {
	if css := wikiLookAndFeelCSS(nil); css != "" {
		t.Fatalf("the default look wrote CSS: %s", css)
	}
	css := string(wikiLookAndFeelCSS(map[string]any{
		"headings":           map[string]any{"color": "#123456"},
		"links":              map[string]any{"color": "rgb(0, 82, 204)"},
		"bordersAndDividers": map[string]any{"color": "red;} body{display:none"},
		"header":             map[string]any{"backgroundColor": "#0747A6", "primaryNavigation": map[string]any{"color": "white"}},
	}))
	for _, want := range []string{
		".page-wiki .global-header{background:#0747A6}",
		".page-wiki .global-header a,.page-wiki .global-header .icon-button{color:white}",
		`@media (prefers-color-scheme: light){:root:not([data-theme="dark"]) .page-wiki main h1,`,
		`{color:#123456}`,
		`:root[data-theme="light"] .page-wiki main a:not(.btn){color:rgb(0, 82, 204)}`,
	} {
		if !strings.Contains(css, want) {
			t.Fatalf("look and feel CSS lacks %q: %s", want, css)
		}
	}
	if strings.Contains(css, "display:none") || strings.Contains(css, "border-color") {
		t.Fatalf("a setting that is not a colour reached the CSS: %s", css)
	}
	if strings.Contains(strings.Split(css, `:root[data-theme="light"]`)[0], `:root[data-theme="dark"]`) {
		t.Fatalf("dark theme rules were written: %s", css)
	}
}
