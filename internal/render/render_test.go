package render

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func fixtureIssue() models.IssueView {
	return models.IssueView{
		Issue: models.Issue{
			ID:          "iss_abc123",
			ProjectID:   "prj_default",
			Key:         "ZZ-1",
			Summary:     "Walking skeleton comes online",
			Description: json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"First issue created through the Jira API."}]}]}`),
			Status:      models.Status{ID: "st_todo", Name: "To Do", Category: "new"},
			IssueType:   models.IssueType{ID: "it_task", Name: "Task"},
			Priority:    &models.Priority{ID: "pr_medium", Name: "Medium"},
			Assignee:    &models.User{ID: "usr_1", DisplayName: "Demo User", AccountType: "atlassian"},
			Reporter:    &models.User{ID: "usr_1", DisplayName: "Demo User", AccountType: "atlassian"},
			Labels:      []string{"frontend", "parity"},
			UpdatedAt:   "2026-08-28T09:00:00Z",
		},
		ProjectKey:    "ZZ",
		CanEdit:       true,
		CanTriage:     true,
		CurrentUserID: "usr_1",
		Members:       []models.User{{ID: "usr_1", DisplayName: "Demo User"}},
		Priorities:    []models.Priority{{ID: "pr_medium", Name: "Medium"}},
		LinkTypes:     []models.LinkType{{ID: "lt_blocks", Name: "Blocks", Inward: "is blocked by", Outward: "blocks"}},
		Watchers:      []models.User{{ID: "usr_1", DisplayName: "Demo User"}},
		IsWatching:    true,
		Attachments:   []models.Attachment{{ID: "att_1", Filename: "design.png", MimeType: "image/png", Size: 2048, AuthorID: "usr_1", AuthorName: "Demo User", Created: "2026-08-28T10:00:00Z"}},
		Comments:      []models.Comment{{ID: "cmt_1", AuthorID: "usr_1", AuthorName: "Demo User", Body: json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Ready for review."}]}]}`), Created: "2026-08-28T11:00:00Z"}},
		Worklogs:      []models.Worklog{{ID: "wl_1", AuthorID: "usr_1", AuthorName: "Demo User", TimeSpentSeconds: 3600, Created: "2026-08-28T12:00:00Z"}},
		Links:         []models.IssueLinkView{{ID: "lnk_1", Relationship: "blocks", IssueKey: "ZZ-2", Summary: "Dependent work", Status: models.Status{Name: "To Do", Category: "new"}}},
		Activity: []models.IssueActivityItem{
			{Kind: "worklog", ID: "wl_1", AuthorID: "usr_1", AuthorName: "Demo User", Created: "2026-08-28T12:00:00Z", TimeSpentSeconds: 3600, CanDelete: true},
			{Kind: "comment", ID: "cmt_1", AuthorID: "usr_1", AuthorName: "Demo User", Created: "2026-08-28T11:00:00Z", Body: json.RawMessage(`{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"Ready for review."}]}]}`)},
		},
	}
}

func TestFragmentGoldenIssueView(t *testing.T) {
	var buf bytes.Buffer
	if err := Fragment(&buf, "issue_view", fixtureIssue()); err != nil {
		t.Fatalf("render fragment: %v", err)
	}
	goldenPath := "testdata/issue_view.golden"
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		_ = os.WriteFile(goldenPath, buf.Bytes(), 0o644)
		t.Skipf("golden written; re-run to verify (%s)", goldenPath)
		return
	}
	if buf.String() != string(want) {
		t.Fatalf("fragment output drifted from golden.\n--- want ---\n%s\n--- got ---\n%s", want, buf.String())
	}
}

func TestADFToText(t *testing.T) {
	raw := json.RawMessage(`{"type":"doc","version":1,"content":[
		{"type":"paragraph","content":[{"type":"text","text":"line one"}]},
		{"type":"paragraph","content":[{"type":"text","text":"line "},{"type":"text","text":"two"}]}
	]}`)
	if got := adfToText(raw); got != "line one\nline two" {
		t.Fatalf("adfToText = %q", got)
	}
	if got := adfToText(nil); got != "" {
		t.Fatalf("adfToText(nil) = %q", got)
	}
}
