package models

const (
	EntityTombstone = "tombstone"

	CustomFieldText     = "text"
	CustomFieldNumber   = "number"
	CustomFieldDatetime = "datetime"
)

// Tombstone actions are per-user: only excluded users receive them, telling
// their replica to drop an issue they can no longer see.
type TombstonePayload struct {
	IssueID string `json:"issueId"`
	UserID  string `json:"userId"`
	Reason  string `json:"reason"`
}

// SecurityLevel restricts an issue to a set of accounts.
type SecurityLevel struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

type SecurityScheme struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Levels []SecurityLevel `json:"levels"`
}

type CustomField struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type Webhook struct {
	ID       string   `json:"id"`
	URL      string   `json:"url"`
	Events   []string `json:"events"`
	JQL      string   `json:"jql"`
	Active   bool     `json:"active"`
	StartSeq int64    `json:"-"`
}

const EntityIssueLink = "issue_link"

type IssueLink struct {
	ID          string `json:"id"`
	TypeID      string `json:"typeId"`
	TypeName    string `json:"typeName"`
	Inward      string `json:"inward"`
	Outward     string `json:"outward"`
	InwardID    string `json:"inwardIssueId"`
	OutwardID   string `json:"outwardIssueId"`
	WorkspaceID string `json:"-"`
}

type IssueLinkPayload struct {
	Link IssueLink `json:"link"`
}

type IssueLinkDeletePayload struct {
	LinkID string `json:"linkId"`
}

type LinkType struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Inward  string `json:"inward"`
	Outward string `json:"outward"`
}
