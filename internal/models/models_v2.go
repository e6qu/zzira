package models

import "encoding/json"

// EntityComment consts and SchemaVersion live in models.go.

// ChangeItem mirrors one Jira changelog item.
type ChangeItem struct {
	Field      string `json:"field"`
	FieldType  string `json:"fieldtype"`
	From       string `json:"from,omitempty"`
	FromString string `json:"fromString,omitempty"`
	To         string `json:"to,omitempty"`
	ToString   string `json:"toString,omitempty"`
}

// IssueUpdatePayload: field diff (changelog) + full snapshot (materialization).
type IssueUpdatePayload struct {
	Diff  map[string]ChangeItem `json:"diff"`
	Issue Issue                 `json:"issue"`
}

type CommentUpsertPayload struct {
	Comment Comment `json:"comment"`
}

type CommentDeletePayload struct {
	CommentID string `json:"commentId"`
	IssueID   string `json:"issueId"`
}

// Comment is the materialized comment. Wire tags match the sync payload so
// actions and bootstrap snapshots share one decode path.
type Comment struct {
	ID         string          `json:"id"`
	IssueID    string          `json:"issueId"`
	AuthorID   string          `json:"authorId"`
	AuthorName string          `json:"authorName"`
	Body       json.RawMessage `json:"body"`
	Created    string          `json:"created"`
	Local      bool            `json:"-"`
}

// ChangelogEntry is the derived changelog view of one action.
type ChangelogEntry struct {
	Seq      int64        `json:"-"`
	Author   *User        `json:"-"`
	AuthorID string       `json:"-"`
	Created  string       `json:"created"`
	Items    []ChangeItem `json:"items"`
}

// SortedDiffItems orders a diff map deterministically for rendering.
// Shared by the server changelog endpoint and the wasm worker's history view.
func SortedDiffItems(diff map[string]ChangeItem) []ChangeItem {
	order := []string{"summary", "description", "status", "assignee", "priority", "reporter"}
	items := make([]ChangeItem, 0, len(diff))
	seen := map[string]bool{}
	for _, f := range order {
		if it, ok := diff[f]; ok {
			items = append(items, it)
			seen[f] = true
		}
	}
	for f, it := range diff {
		if !seen[f] {
			items = append(items, it)
		}
	}
	return items
}
