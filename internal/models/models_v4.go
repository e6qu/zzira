package models

import "encoding/json"

const (
	EntityAttachment = "attachment"
	EntityWorklog    = "worklog"
)

// Attachment metadata syncs to replicas; bytes live in object storage.
type Attachment struct {
	ID         string `json:"id"`
	IssueID    string `json:"issueId"`
	Filename   string `json:"filename"`
	MimeType   string `json:"mimeType"`
	Size       int64  `json:"size"`
	AuthorID   string `json:"authorId"`
	AuthorName string `json:"authorName"`
	Created    string `json:"created"`
}

type AttachmentUpsertPayload struct {
	Attachment Attachment `json:"attachment"`
}

type AttachmentDeletePayload struct {
	AttachmentID string `json:"attachmentId"`
	IssueID      string `json:"issueId"`
}

// Worklog is time logged against an issue.
type Worklog struct {
	ID               string          `json:"id"`
	IssueID          string          `json:"issueId"`
	AuthorID         string          `json:"authorId"`
	AuthorName       string          `json:"authorName"`
	Comment          json.RawMessage `json:"comment,omitempty"`
	TimeSpentSeconds int             `json:"timeSpentSeconds"`
	Created          string          `json:"created"`
}

type WorklogUpsertPayload struct {
	Worklog Worklog `json:"worklog"`
}

type WorklogDeletePayload struct {
	WorklogID string `json:"worklogId"`
	IssueID   string `json:"issueId"`
}

// TimeSpentLabel renders seconds as Jira's "1h 20m" style label.
func TimeSpentLabel(seconds int) string {
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	out := ""
	if h > 0 {
		out += itoa(h) + "h "
	}
	if m > 0 {
		out += itoa(m) + "m "
	}
	if s > 0 || out == "" {
		out += itoa(s) + "s"
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
