package wikimarkup

import (
	"html"
	"regexp"
	"strings"
)

// Confluence accepts page bodies written in its wiki markup, the notation of
// `h1.` headings, `*bold*` text and `[title|https://…]` links, and keeps them
// as storage format. FromNotation is that conversion.

var (
	notationHeading  = regexp.MustCompile(`^h([1-6])\.\s*(.*)$`)
	notationList     = regexp.MustCompile(`^([*#-]+)\s+(.*)$`)
	notationCodeOpen = regexp.MustCompile(`^\{(code|noformat)(?::([^}]*))?\}(.*)$`)
	notationLink     = regexp.MustCompile(`\[([^\[\]|]+)\|([^\[\]]+)\]|\[((?:https?://|mailto:)[^\[\]\s]+)\]`)
	notationMono     = regexp.MustCompile(`\{\{(.+?)\}\}`)
)

type notationSpan struct {
	marker, tag string
}

var notationSpans = []notationSpan{
	{"*", "strong"}, {"_", "em"}, {"-", "del"}, {"+", "u"}, {"^", "sup"}, {"~", "sub"}, {"??", "cite"},
}

// FromNotation converts Confluence wiki markup to storage format.
func FromNotation(source string) string {
	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	var out strings.Builder
	var paragraph []string
	var listStack []string
	flushParagraph := func() {
		if len(paragraph) == 0 {
			return
		}
		out.WriteString("<p>" + strings.Join(paragraph, "<br />") + "</p>")
		paragraph = nil
	}
	closeLists := func(depth int) {
		for len(listStack) > depth {
			tag := listStack[len(listStack)-1]
			listStack = listStack[:len(listStack)-1]
			out.WriteString("</li></" + tag + ">")
		}
	}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		trimmed := strings.TrimSpace(line)
		if match := notationCodeOpen.FindStringSubmatch(trimmed); match != nil {
			flushParagraph()
			closeLists(0)
			closing := "{" + match[1] + "}"
			body := []string{}
			rest := match[3]
			if end := strings.Index(rest, closing); end >= 0 {
				body = append(body, rest[:end])
			} else {
				if rest != "" {
					body = append(body, rest)
				}
				for i++; i < len(lines); i++ {
					if end := strings.Index(lines[i], closing); end >= 0 {
						if end > 0 {
							body = append(body, lines[i][:end])
						}
						break
					}
					body = append(body, lines[i])
				}
			}
			out.WriteString("<pre>" + html.EscapeString(strings.Join(body, "\n")) + "</pre>")
			continue
		}
		if trimmed == "{quote}" {
			flushParagraph()
			closeLists(0)
			quoted := []string{}
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "{quote}"; i++ {
				quoted = append(quoted, lines[i])
			}
			out.WriteString("<blockquote>" + FromNotation(strings.Join(quoted, "\n")) + "</blockquote>")
			continue
		}
		switch {
		case trimmed == "":
			flushParagraph()
			closeLists(0)
		case trimmed == "----":
			flushParagraph()
			closeLists(0)
			out.WriteString("<hr />")
		case notationHeading.MatchString(trimmed):
			flushParagraph()
			closeLists(0)
			match := notationHeading.FindStringSubmatch(trimmed)
			out.WriteString("<h" + match[1] + ">" + notationInline(match[2]) + "</h" + match[1] + ">")
		case strings.HasPrefix(trimmed, "bq. "):
			flushParagraph()
			closeLists(0)
			out.WriteString("<blockquote><p>" + notationInline(strings.TrimPrefix(trimmed, "bq. ")) + "</p></blockquote>")
		case strings.HasPrefix(trimmed, "|"):
			flushParagraph()
			closeLists(0)
			out.WriteString("<table><tbody>")
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
				out.WriteString("<tr>" + notationRow(strings.TrimSpace(lines[i])) + "</tr>")
			}
			i--
			out.WriteString("</tbody></table>")
		case notationList.MatchString(trimmed) && !notationIsEmphasis(trimmed):
			flushParagraph()
			match := notationList.FindStringSubmatch(trimmed)
			markers := match[1]
			if strings.Trim(markers, "-") == "" {
				markers = "*"
			}
			depth := len(markers)
			tag := "ul"
			if markers[len(markers)-1] == '#' {
				tag = "ol"
			}
			if depth > len(listStack) {
				for len(listStack) < depth {
					kind := "ul"
					if markers[len(listStack)] == '#' {
						kind = "ol"
					}
					listStack = append(listStack, kind)
					out.WriteString("<" + kind + "><li>")
				}
			} else {
				closeLists(depth)
				if listStack[depth-1] != tag {
					closeLists(depth - 1)
					listStack = append(listStack, tag)
					out.WriteString("<" + tag + "><li>")
				} else {
					out.WriteString("</li><li>")
				}
			}
			out.WriteString(notationInline(match[2]))
		default:
			closeLists(0)
			paragraph = append(paragraph, notationInline(trimmed))
		}
	}
	flushParagraph()
	closeLists(0)
	return out.String()
}

// notationIsEmphasis tells a bold sentence such as "*Note* this" from a list
// item, which needs a space after its markers.
func notationIsEmphasis(line string) bool {
	return strings.HasPrefix(line, "*") && strings.Count(line, "*") >= 2 && !strings.HasPrefix(line, "* ") && !strings.HasPrefix(line, "** ")
}

func notationRow(line string) string {
	var row strings.Builder
	rest := line
	for len(rest) > 0 {
		header := strings.HasPrefix(rest, "||")
		if header {
			rest = rest[2:]
		} else {
			rest = rest[1:]
		}
		if strings.TrimSpace(rest) == "" {
			break
		}
		end := strings.Index(rest, "|")
		cell := rest
		if end >= 0 {
			cell, rest = rest[:end], rest[end:]
		} else {
			rest = ""
		}
		tag := "td"
		if header {
			tag = "th"
		}
		row.WriteString("<" + tag + ">" + notationInline(strings.TrimSpace(cell)) + "</" + tag + ">")
	}
	return row.String()
}

// notationInline converts the markup inside one line. The text is escaped
// first, so markup can only ever produce the elements it names.
func notationInline(text string) string {
	escaped := html.EscapeString(text)
	escaped = strings.ReplaceAll(escaped, `\\`, "<br />")
	escaped = notationMono.ReplaceAllString(escaped, "<code>$1</code>")
	escaped = notationLink.ReplaceAllStringFunc(escaped, func(match string) string {
		parts := notationLink.FindStringSubmatch(match)
		title, target := parts[1], parts[2]
		if parts[3] != "" {
			title, target = parts[3], parts[3]
		}
		unescaped := html.UnescapeString(target)
		if !strings.HasPrefix(unescaped, "http://") && !strings.HasPrefix(unescaped, "https://") && !strings.HasPrefix(unescaped, "mailto:") {
			return match
		}
		return `<a href="` + html.EscapeString(unescaped) + `">` + title + "</a>"
	})
	for _, span := range notationSpans {
		escaped = replaceNotationSpan(escaped, span)
	}
	return escaped
}

// replaceNotationSpan wraps text between a pair of markers when the markers
// sit at word boundaries, so a hyphenated word or an arithmetic expression is
// left alone.
func replaceNotationSpan(text string, span notationSpan) string {
	marker := html.EscapeString(span.marker)
	var out strings.Builder
	for {
		start := notationMarkerIndex(text, marker, true)
		if start < 0 {
			out.WriteString(text)
			return out.String()
		}
		end := notationMarkerIndex(text[start+len(marker):], marker, false)
		if end < 0 {
			out.WriteString(text)
			return out.String()
		}
		end += start + len(marker)
		inner := text[start+len(marker) : end]
		out.WriteString(text[:start] + "<" + span.tag + ">" + inner + "</" + span.tag + ">")
		text = text[end+len(marker):]
	}
}

func notationMarkerIndex(text, marker string, opening bool) int {
	offset := 0
	for {
		index := strings.Index(text[offset:], marker)
		if index < 0 {
			return -1
		}
		index += offset
		before := byte(' ')
		if index > 0 {
			before = text[index-1]
		}
		after := byte(' ')
		if index+len(marker) < len(text) {
			after = text[index+len(marker)]
		}
		if opening && notationBoundary(before) && !notationSpace(after) && index+len(marker) < len(text) {
			return index
		}
		if !opening && !notationSpace(before) && notationBoundary(after) && index > 0 {
			return index
		}
		offset = index + len(marker)
	}
}

func notationSpace(b byte) bool { return b == ' ' || b == '\t' }

func notationBoundary(b byte) bool {
	return notationSpace(b) || strings.IndexByte("()[]{}.,;:!?\"'>", b) >= 0
}
