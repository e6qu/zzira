// Package models holds shared structs used by the renderer, the server, and the
// wasm client. It must stay free of server-only imports (database, net/http).
package models

import "encoding/json"

const (
	SchemaVersion = 2

	OpUpsert = "upsert"
	OpDelete = "delete"

	EntityIssue   = "issue"
	EntityComment = "comment"
)

type User struct {
	ID          string `json:"accountId"`
	Email       string `json:"emailAddress,omitempty"`
	DisplayName string `json:"displayName"`
	TimeZone    string `json:"timeZone,omitempty"`
	Active      bool   `json:"active"`
	AccountType string `json:"accountType"`
	// Username is a display handle for the UI's account control, not a Jira
	// Cloud REST API field (accountId is the API identity); excluded from JSON.
	Username string `json:"-"`
}

type Status struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

type IssueType struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon"`
}

type Priority struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Project struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"-"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	WorkflowID  string `json:"-"`

	SecuritySchemeID string `json:"-"`
}

// Issue is the materialized issue. Description is an ADF document stored verbatim.
type Issue struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"-"`
	ProjectID   string          `json:"-"`
	Key         string          `json:"key"`
	Summary     string          `json:"summary"`
	Description json.RawMessage `json:"description"`
	Status      Status          `json:"status"`
	IssueType   IssueType       `json:"issuetype"`
	Priority    *Priority       `json:"priority"`
	Assignee    *User           `json:"assignee"`
	Reporter    *User           `json:"reporter"`
	Labels      []string        `json:"labels"`
	Rank        string          `json:"rank"`

	SecurityLevelID string                     `json:"securityLevelId,omitempty"`
	Fields          map[string]json.RawMessage `json:"fields,omitempty"`

	UpdatedSeq int64  `json:"-"`
	UpdatedAt  string `json:"updated"`
}

// Action is one immutable ordered record of a change.
type Action struct {
	WorkspaceID string          `json:"-"`
	Seq         int64           `json:"seq"`
	EntityType  string          `json:"entityType"`
	EntityID    string          `json:"entityId"`
	Op          string          `json:"op"`
	SchemaV     int             `json:"schemaV"`
	Payload     json.RawMessage `json:"payload"`
	ActorID     string          `json:"actorId"`
	CreatedAt   string          `json:"createdAt,omitempty"`
}

// IssueUpsertPayload is the V0 payload shape: the full current value of the issue.
// Field-level diffs arrive in V1; shape is versioned by SchemaV.
type IssueUpsertPayload struct {
	Issue Issue `json:"issue"`
}

type DeletePayload struct {
	Reason string `json:"reason"`
}

// SyncResponse is the /sync wire contract.
type SyncResponse struct {
	Workspace       string   `json:"workspace"`
	From            int64    `json:"from"`
	To              int64    `json:"to"`
	Head            int64    `json:"head"`
	RendererVersion string   `json:"rendererVersion"`
	Actions         []Action `json:"actions"`
	Truncated       bool     `json:"truncated"`
}

// ---- View models (render-only) ----

type IssueView struct {
	Issue             Issue
	ProjectKey        string
	CanEdit           bool
	CanTriage         bool
	CurrentUserID     string
	Comments          []Comment
	Transitions       []WorkflowTransition
	History           []ChangelogEntry
	Attachments       []Attachment
	Worklogs          []Worklog
	Activity          []IssueActivityItem
	Members           []User
	Priorities        []Priority
	SecurityLevels    []WorkflowTransition
	SecurityLevelName string
	CustomFields      []CustomFieldView
	Watchers          []User
	IsWatching        bool
	Links             []IssueLinkView
	LinkTypes         []LinkType
}

// IssueActivityItem is one entry in the issue's chronological activity ledger.
type IssueActivityItem struct {
	Kind             string
	ID               string
	AuthorID         string
	AuthorName       string
	Created          string
	Body             json.RawMessage
	TimeSpentSeconds int
	Items            []ChangeItem
	CanDelete        bool
}

// IssueLinkView resolves an issue link from the current issue's perspective.
type IssueLinkView struct {
	ID           string
	Relationship string
	IssueKey     string
	Summary      string
	Status       Status
}

// WorkflowTransition decouples the view from the workflow package.
type WorkflowTransition struct {
	ID   string
	Name string
}

// EditDialogView drives the edit-issue dialog; rendered by both server and
// wasm worker (offline editing). Members/levels/fields may be empty offline.
type EditDialogView struct {
	Issue          Issue
	Members        []User
	SecurityLevels []WorkflowTransition // reuse shape: ID+Name pairs
	CustomFields   []CustomFieldView
	Error          string
}

// CustomFieldView is a render-only custom field descriptor with the value.
type CustomFieldView struct {
	ID    string
	Name  string
	Value string
}
