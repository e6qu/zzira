package web

import (
	"html/template"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// lookAndFeelSpaceID is the space whose look and feel a wiki page shows.
func (d wikiData) lookAndFeelSpaceID() string {
	if d.Space == nil {
		return ""
	}
	return d.Space.ID
}

var lookAndFeelColour = regexp.MustCompile(`^(#([0-9a-fA-F]{3}|[0-9a-fA-F]{4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})|rgba?\(\s*\d{1,3}\s*,\s*\d{1,3}\s*,\s*\d{1,3}\s*(,\s*(0|1|0?\.\d+)\s*)?\)|[a-zA-Z]{3,20})$`)

// lookAndFeelValue reads a colour at a path in Confluence's look and feel
// settings, or "" when it is missing or not a plain colour.
func lookAndFeelValue(settings map[string]any, path ...string) string {
	var value any = settings
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = object[key]
	}
	colour, _ := value.(string)
	if colour = strings.TrimSpace(colour); !lookAndFeelColour.MatchString(colour) {
		return ""
	}
	return colour
}

// wikiLookAndFeelCSS turns Confluence's custom look and feel into CSS for wiki
// pages: the header's background and navigation colour always, and heading,
// link and divider colours in the light theme, where the chosen colours were
// designed to be read. Only plain colours are written, so a setting cannot
// inject other CSS.
func wikiLookAndFeelCSS(settings map[string]any) template.CSS {
	if len(settings) == 0 {
		return ""
	}
	var css strings.Builder
	if background := lookAndFeelValue(settings, "header", "backgroundColor"); background != "" {
		css.WriteString(".page-wiki .global-header{background:" + background + "}")
	}
	if navigation := lookAndFeelValue(settings, "header", "primaryNavigation", "color"); navigation != "" {
		css.WriteString(".page-wiki .global-header a,.page-wiki .global-header .icon-button{color:" + navigation + "}")
	}
	var light strings.Builder
	rule := func(selector, property, colour string) {
		if colour != "" {
			light.WriteString(selector + "{" + property + ":" + colour + "}")
		}
	}
	for _, theme := range []string{`:root:not([data-theme="dark"])`, `:root[data-theme="light"]`} {
		rule(theme+" .page-wiki main h1,"+theme+" .page-wiki main h2,"+theme+" .page-wiki main h3,"+theme+" .page-wiki main h4", "color", lookAndFeelValue(settings, "headings", "color"))
		rule(theme+" .page-wiki main a:not(.btn)", "color", lookAndFeelValue(settings, "links", "color"))
		rule(theme+" .page-wiki main hr,"+theme+" .page-wiki main th,"+theme+" .page-wiki main td", "border-color", lookAndFeelValue(settings, "bordersAndDividers", "color"))
	}
	if light.Len() > 0 {
		css.WriteString("@media (prefers-color-scheme: light){" + strings.Split(light.String(), `:root[data-theme="light"]`)[0] + "}")
		if index := strings.Index(light.String(), `:root[data-theme="light"]`); index >= 0 {
			css.WriteString(light.String()[index:])
		}
	}
	return template.CSS(css.String())
}

// whiteTextContrast is the WCAG contrast ratio of white text on a hex or
// rgb() colour, or 0 when the colour cannot be read as one. Primary buttons
// carry white labels, so a hero colour darker than 4.5:1 leaves them unreadable.
func whiteTextContrast(colour string) float64 {
	var channels [3]float64
	switch {
	case strings.HasPrefix(colour, "#"):
		hex := colour[1:]
		if len(hex) == 3 || len(hex) == 4 {
			hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
		}
		if len(hex) != 6 && len(hex) != 8 {
			return 0
		}
		for index := range channels {
			value, err := strconv.ParseUint(hex[index*2:index*2+2], 16, 8)
			if err != nil {
				return 0
			}
			channels[index] = float64(value)
		}
	case strings.HasPrefix(colour, "rgb"):
		parts := strings.FieldsFunc(colour[strings.Index(colour, "(")+1:], func(r rune) bool { return r == ',' || r == ')' || r == ' ' })
		if len(parts) < 3 {
			return 0
		}
		for index := range channels {
			value, err := strconv.ParseFloat(parts[index], 64)
			if err != nil || value < 0 || value > 255 {
				return 0
			}
			channels[index] = value
		}
	default:
		return 0
	}
	linear := func(channel float64) float64 {
		channel /= 255
		if channel <= 0.03928 {
			return channel / 12.92
		}
		return math.Pow((channel+0.055)/1.055, 2.4)
	}
	luminance := 0.2126*linear(channels[0]) + 0.7152*linear(channels[1]) + 0.0722*linear(channels[2])
	return 1.05 / (luminance + 0.05)
}
