package web

import (
	"strings"
	"testing"
)

func TestSiteLookForJiraLookAndFeel(t *testing.T) {
	plain := siteLookFor(map[string]string{})
	if plain.ShowTitle || plain.FaviconHiResURL != "" || plain.ButtonStyle != "" {
		t.Fatalf("the default look = %+v", plain)
	}
	look := siteLookFor(map[string]string{
		"jira.title":                          "Acme Delivery",
		"jira.lf.logo.show.application.title": "true",
		"jira.lf.favicon.hires.url":           "https://acme.example/icon-64.png",
		"jira.lf.hero.button.base.bg.colour":  "#0052CC",
	})
	if look.Title != "Acme Delivery" || !look.ShowTitle || look.FaviconHiResURL != "https://acme.example/icon-64.png" {
		t.Fatalf("look = %+v", look)
	}
	if style := string(look.ButtonStyle); !strings.Contains(style, `:root[data-theme="light"] .btn-primary{background:#0052CC;border-color:#0052CC}`) || !strings.Contains(style, "@media (prefers-color-scheme: light)") || strings.Contains(style, `[data-theme="dark"] .btn-primary`) {
		t.Fatalf("button style = %s", style)
	}
	if injected := siteLookFor(map[string]string{"jira.lf.hero.button.base.bg.colour": "red}body{display:none"}); injected.ButtonStyle != "" {
		t.Fatalf("a value that is not a colour styled buttons: %s", injected.ButtonStyle)
	}
	// White labels need 4.5:1: Jira's default hero blue is too light for them.
	if light := siteLookFor(map[string]string{"jira.lf.hero.button.base.bg.colour": "#3b7fc4"}); light.ButtonStyle != "" {
		t.Fatalf("a hero colour white labels cannot be read on styled buttons: %s", light.ButtonStyle)
	}
	for colour, want := range map[string]bool{"#0052CC": true, "#206B4E": true, "rgb(32, 107, 78)": true, "#3b7fc4": false, "#FFF": false, "navy": false} {
		if got := whiteTextContrast(colour) >= 4.5; got != want {
			t.Fatalf("white text on %s readable = %t (contrast %.2f)", colour, got, whiteTextContrast(colour))
		}
	}
	if hidden := siteLookFor(map[string]string{"jira.lf.logo.show.application.title": "false"}); hidden.ShowTitle {
		t.Fatal("a turned-off application title is shown")
	}
}
