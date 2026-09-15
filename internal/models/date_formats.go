package models

import (
	"strings"
	"unicode"
)

// Jira's default display layouts, as Go time layouts.
const (
	DefaultCompleteDateLayout = "02/Jan/06 3:04 PM"
	DefaultDayDateLayout      = "02/Jan/06"
)

// JavaDateLayout converts a Java SimpleDateFormat pattern, as Jira's look and
// feel stores it, to a Go time layout. It reports false for a pattern it
// cannot express exactly.
func JavaDateLayout(pattern string) (string, bool) {
	if strings.TrimSpace(pattern) == "" {
		return "", false
	}
	var layout strings.Builder
	runes := []rune(pattern)
	for i := 0; i < len(runes); {
		letter := runes[i]
		if letter == '\'' {
			end := i + 1
			for end < len(runes) && runes[end] != '\'' {
				end++
			}
			if end >= len(runes) {
				return "", false
			}
			literal := string(runes[i+1 : end])
			if literal == "" {
				layout.WriteRune('\'')
			} else {
				if strings.ContainsAny(literal, "0123456789") || containsLayoutWord(literal) {
					return "", false
				}
				layout.WriteString(literal)
			}
			i = end + 1
			continue
		}
		if !unicode.IsLetter(letter) {
			if unicode.IsDigit(letter) {
				return "", false
			}
			layout.WriteRune(letter)
			i++
			continue
		}
		count := 1
		for i+count < len(runes) && runes[i+count] == letter {
			count++
		}
		token, ok := javaDateToken(letter, count)
		if !ok {
			return "", false
		}
		layout.WriteString(token)
		i += count
	}
	return layout.String(), true
}

func javaDateToken(letter rune, count int) (string, bool) {
	switch letter {
	case 'y':
		if count == 2 {
			return "06", true
		}
		return "2006", true
	case 'M':
		switch count {
		case 1:
			return "1", true
		case 2:
			return "01", true
		case 3:
			return "Jan", true
		}
		return "January", true
	case 'd':
		if count == 1 {
			return "2", true
		}
		return "02", true
	case 'E':
		if count <= 3 {
			return "Mon", true
		}
		return "Monday", true
	case 'h':
		if count == 1 {
			return "3", true
		}
		return "03", true
	case 'H':
		return "15", true
	case 'm':
		if count == 1 {
			return "4", true
		}
		return "04", true
	case 's':
		if count == 1 {
			return "5", true
		}
		return "05", true
	case 'a':
		return "PM", true
	case 'Z':
		return "-0700", true
	case 'z':
		return "MST", true
	}
	return "", false
}

// containsLayoutWord reports literal text Go would read as a layout element.
func containsLayoutWord(literal string) bool {
	for _, word := range []string{"Jan", "Mon", "MST", "PM", "pm"} {
		if strings.Contains(literal, word) {
			return true
		}
	}
	return false
}

// ValidJavaScriptDateFormat reports whether a browser date picker format uses
// only the directives Jira's date picker supports.
func ValidJavaScriptDateFormat(format string) bool {
	if strings.TrimSpace(format) == "" {
		return false
	}
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		if i+1 >= len(format) || !strings.ContainsRune("aAbBdeHIjmMpPSuwyY%", rune(format[i+1])) {
			return false
		}
		i++
	}
	return true
}

// SiteDateLayouts returns the complete and day display layouts a site's look
// and feel sets, or Jira's defaults.
func SiteDateLayouts(properties map[string]string) (complete, day string) {
	complete, day = DefaultCompleteDateLayout, DefaultDayDateLayout
	if layout, ok := JavaDateLayout(properties["jira.lf.date.complete"]); ok {
		complete = layout
	}
	if layout, ok := JavaDateLayout(properties["jira.lf.date.dmy"]); ok {
		day = layout
	}
	return complete, day
}

// DefaultSiteTitle brands pages until an administrator sets jira.title.
const DefaultSiteTitle = "ZZIRA"
