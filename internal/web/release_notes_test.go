package web

import (
	"net/url"
	"strings"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func releaseNoteIssue(key, summary, typeID, typeName string) *models.Issue {
	return &models.Issue{Key: key, Summary: summary, IssueType: models.IssueType{ID: typeID, Name: typeName}}
}

// Jira's release notes group a version's work by work type, include the types
// chosen, and come styled, as plain text, or as Markdown.
func TestReleaseNotesGroupByWorkTypeInTheChosenFormat(t *testing.T) {
	issues := []*models.Issue{
		releaseNoteIssue("ZZ-3", "Fix login", "it_bug", "Bug"),
		releaseNoteIssue("ZZ-1", "Add export", "it_story", "Story"),
		releaseNoteIssue("ZZ-2", "Crash on *save*", "it_bug", "Bug"),
	}

	styled := buildReleaseNotes("Zzira", "1.0", "https://zzira.test", issues, url.Values{})
	if styled.Format != "styled" || styled.Text != "" {
		t.Fatalf("default notes = %+v, want styled with no text", styled)
	}
	if len(styled.Groups) != 2 || styled.Groups[0].Name != "Bug" || styled.Groups[1].Name != "Story" {
		t.Fatalf("groups = %+v, want Bug then Story", styled.Groups)
	}
	if len(styled.Groups[0].Issues) != 2 || styled.Groups[0].Issues[0].Key != "ZZ-3" {
		t.Fatalf("bug group = %+v, want both bugs in version order", styled.Groups[0].Issues)
	}
	for _, kind := range styled.Types {
		if !kind.Included {
			t.Fatalf("with nothing chosen every type is included: %+v", styled.Types)
		}
	}

	text := buildReleaseNotes("Zzira", "1.0", "https://zzira.test", issues, url.Values{"notesFormat": {"text"}})
	want := "Release notes - Zzira - Version 1.0\n\n** Bug\n    * [ZZ-3] - Fix login\n    * [ZZ-2] - Crash on *save*\n\n** Story\n    * [ZZ-1] - Add export\n"
	if text.Text != want {
		t.Fatalf("plain text notes =\n%q\nwant\n%q", text.Text, want)
	}

	bugsOnly := buildReleaseNotes("Zzira", "1.0", "https://zzira.test", issues, url.Values{"notesFormat": {"markdown"}, "notesType": {"it_bug"}})
	if strings.Contains(bugsOnly.Text, "Story") || strings.Contains(bugsOnly.Text, "ZZ-1") {
		t.Fatalf("a type left out appeared: %s", bugsOnly.Text)
	}
	if !strings.Contains(bugsOnly.Text, "- [ZZ-2](https://zzira.test/browse/ZZ-2) Crash on \\*save\\*") {
		t.Fatalf("markdown notes did not link and escape: %s", bugsOnly.Text)
	}
	for _, kind := range bugsOnly.Types {
		if kind.Included != (kind.ID == "it_bug") {
			t.Fatalf("included types = %+v, want only Bug", bugsOnly.Types)
		}
	}

	if unknown := buildReleaseNotes("Zzira", "1.0", "", issues, url.Values{"notesFormat": {"pdf"}}); unknown.Format != "styled" {
		t.Fatalf("an unknown format = %q, want styled", unknown.Format)
	}
}
