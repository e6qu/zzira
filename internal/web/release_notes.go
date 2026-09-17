package web

import (
	"net/url"
	"sort"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

// releaseNoteFormats are the forms Jira gives release notes in: styled for
// reading on the page, and plain text or Markdown for copying elsewhere.
var releaseNoteFormats = []automationOption{{"styled", "Styled"}, {"text", "Plain text"}, {"markdown", "Markdown"}}

// releaseNoteType is a work type a version holds, and whether the notes
// include it.
type releaseNoteType struct {
	ID, Name string
	Included bool
}

// releaseNoteGroup is the work of one type in the notes.
type releaseNoteGroup struct {
	Name   string
	Issues []*models.Issue
}

// releaseNotes are a version's notes as the person asked for them.
type releaseNotes struct {
	Format  string
	Formats []automationOption
	Types   []releaseNoteType
	Groups  []releaseNoteGroup
	// Text is the notes in the chosen text format, for copying; empty when
	// the notes are styled.
	Text string
}

// buildReleaseNotes groups a version's work by work type, keeping only the
// types chosen (every type when none is), and writes it out in the format
// chosen, as Jira's release notes do.
func buildReleaseNotes(projectName, versionName, baseURL string, issues []*models.Issue, query url.Values) releaseNotes {
	notes := releaseNotes{Format: query.Get("notesFormat"), Formats: releaseNoteFormats}
	if notes.Format != "text" && notes.Format != "markdown" {
		notes.Format = "styled"
	}
	chosen := map[string]bool{}
	for _, id := range query["notesType"] {
		chosen[id] = true
	}

	byType := map[string]*releaseNoteGroup{}
	seen := map[string]bool{}
	for _, issue := range issues {
		if !seen[issue.IssueType.ID] {
			seen[issue.IssueType.ID] = true
			notes.Types = append(notes.Types, releaseNoteType{ID: issue.IssueType.ID, Name: issue.IssueType.Name,
				Included: len(chosen) == 0 || chosen[issue.IssueType.ID]})
		}
		if len(chosen) > 0 && !chosen[issue.IssueType.ID] {
			continue
		}
		group, ok := byType[issue.IssueType.ID]
		if !ok {
			group = &releaseNoteGroup{Name: issue.IssueType.Name}
			byType[issue.IssueType.ID] = group
		}
		group.Issues = append(group.Issues, issue)
	}
	sort.Slice(notes.Types, func(i, j int) bool {
		return strings.ToLower(notes.Types[i].Name) < strings.ToLower(notes.Types[j].Name)
	})
	for _, kind := range notes.Types {
		if group, ok := byType[kind.ID]; ok {
			notes.Groups = append(notes.Groups, *group)
		}
	}

	switch notes.Format {
	case "text":
		var b strings.Builder
		b.WriteString("Release notes - " + projectName + " - Version " + versionName + "\n")
		for _, group := range notes.Groups {
			b.WriteString("\n** " + group.Name + "\n")
			for _, issue := range group.Issues {
				b.WriteString("    * [" + issue.Key + "] - " + issue.Summary + "\n")
			}
		}
		notes.Text = b.String()
	case "markdown":
		var b strings.Builder
		b.WriteString("# Release notes - " + projectName + " - " + versionName + "\n")
		for _, group := range notes.Groups {
			b.WriteString("\n## " + group.Name + "\n\n")
			for _, issue := range group.Issues {
				b.WriteString("- [" + issue.Key + "](" + strings.TrimSuffix(baseURL, "/") + "/browse/" + issue.Key + ") " + markdownEscape(issue.Summary) + "\n")
			}
		}
		notes.Text = b.String()
	}
	return notes
}

// markdownEscape keeps a summary from being read as Markdown syntax.
func markdownEscape(text string) string {
	replacer := strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "[", `\[`, "]", `\]`, "#", `\#`, "<", `\<`, ">", `\>`)
	return replacer.Replace(text)
}
