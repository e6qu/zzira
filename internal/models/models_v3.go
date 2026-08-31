package models

// Snapshot is the bootstrap payload: a permission-filtered dump of
// materialized rows at a known action seq (taken in one repeatable-read tx).
type Snapshot struct {
	Seq         int64        `json:"-"`
	Issues      []Issue      `json:"issues"`
	Comments    []Comment    `json:"comments"`
	Attachments []Attachment `json:"attachments"`
	Worklogs    []Worklog    `json:"worklogs"`
}

// Filter is a saved JQL filter (Jira FilterBean subset).
type Filter struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	JQL         string `json:"jql"`
	Description string `json:"description,omitempty"`
	OwnerID     string `json:"-"`
	OwnerName   string `json:"-"`
	Favourite   bool   `json:"favourite"`
}
