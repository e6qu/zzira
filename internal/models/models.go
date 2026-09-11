// Package models holds shared structs used by the renderer, the server, and the
// wasm client. It must stay free of server-only imports (database, net/http).
package models

import (
	"encoding/json"
	"sort"
)

const (
	SchemaVersion = 2

	OpUpsert = "upsert"
	OpDelete = "delete"

	EntityIssue   = "issue"
	EntityComment = "comment"
)

type User struct {
	ID               string `json:"accountId"`
	Email            string `json:"emailAddress,omitempty"`
	DisplayName      string `json:"displayName"`
	TimeZone         string `json:"timeZone,omitempty"`
	Active           bool   `json:"active"`
	AccountType      string `json:"accountType"`
	AccountActive    bool   `json:"-"`
	AddedAt          string `json:"-"`
	SuspendedAt      string `json:"-"`
	DeactivatedAt    string `json:"-"`
	ManagementSource string `json:"-"`
	Nickname         string `json:"-"`
	JobTitle         string `json:"-"`
	Department       string `json:"-"`
	OrganizationName string `json:"-"`
	Location         string `json:"-"`
	PictureURL       string `json:"-"`
	AvatarURL        string `json:"-"`
	EmailVerified    bool   `json:"-"`
	MFAEnabled       bool   `json:"-"`
	// Username is a display handle for the UI's account control, not a Jira
	// Cloud REST API field (accountId is the API identity); excluded from JSON.
	Username string `json:"-"`
}

type Status struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category"`
	ProjectID   string `json:"-"`
	Protected   bool   `json:"-"`
}

type IssueType struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Icon    string `json:"icon"`
	Subtask bool   `json:"subtask"`
}

type IssueParent struct {
	ID      string `json:"id"`
	JiraID  int64  `json:"-"`
	Key     string `json:"key"`
	Summary string `json:"summary"`
}

type Priority struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Project struct {
	ID             string `json:"id"`
	WorkspaceID    string `json:"-"`
	Key            string `json:"key"`
	Name           string `json:"name"`
	WorkflowID     string `json:"-"`
	Description    string `json:"description"`
	URL            string `json:"url"`
	LeadAccountID  string `json:"leadAccountId,omitempty"`
	AssigneeType   string `json:"assigneeType"`
	ProjectTypeKey string `json:"projectTypeKey"`
	CategoryID     string `json:"categoryId,omitempty"`
	SenderEmail    string `json:"-"`
	LifecycleState string `json:"-"`
	ArchivedAt     string `json:"-"`
	TrashedAt      string `json:"-"`
	LifecycleActor string `json:"-"`

	SecuritySchemeID string `json:"-"`
}

type ProjectCategory struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"-"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ProjectProperty struct {
	ProjectID string          `json:"-"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
}

type ProjectFeature struct {
	Key           string   `json:"feature"`
	Name          string   `json:"localisedName"`
	Description   string   `json:"localisedDescription"`
	State         string   `json:"state"`
	Prerequisites []string `json:"prerequisites"`
	ToggleLocked  bool     `json:"toggleLocked"`
}

type ProjectRole struct {
	ID          int64              `json:"id"`
	WorkspaceID string             `json:"-"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Admin       bool               `json:"admin"`
	Default     bool               `json:"default"`
	Actors      []ProjectRoleActor `json:"actors,omitempty"`
}

type ProjectRoleActor struct {
	ID            int64  `json:"id"`
	PrincipalType string `json:"-"`
	PrincipalID   string `json:"-"`
	DisplayName   string `json:"displayName"`
	Active        bool   `json:"-"`
}

type PermissionScheme struct {
	ID           int64             `json:"id"`
	WorkspaceID  string            `json:"-"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Default      bool              `json:"default"`
	ProjectCount int               `json:"projectCount"`
	Grants       []PermissionGrant `json:"permissions,omitempty"`
}

type PermissionGrant struct {
	ID              int64  `json:"id"`
	Permission      string `json:"permission"`
	HolderType      string `json:"-"`
	HolderParameter string `json:"-"`
	HolderValue     string `json:"-"`
}

type NotificationScheme struct {
	ID           int64                     `json:"id"`
	WorkspaceID  string                    `json:"-"`
	Name         string                    `json:"name"`
	Description  string                    `json:"description"`
	Default      bool                      `json:"default"`
	ProjectCount int                       `json:"projectCount"`
	Events       []NotificationSchemeEvent `json:"notificationSchemeEvents,omitempty"`
}

type NotificationSchemeEvent struct {
	EventID       int64                     `json:"eventId"`
	Notifications []NotificationSchemeEntry `json:"notifications"`
}

type NotificationSchemeEntry struct {
	ID               int64  `json:"id"`
	EventID          int64  `json:"eventId"`
	NotificationType string `json:"notificationType"`
	Parameter        string `json:"parameter,omitempty"`
	Recipient        string `json:"recipient,omitempty"`
}

// Issue is the materialized issue. Description is an ADF document stored verbatim.
type Issue struct {
	ID          string          `json:"id"`
	JiraID      int64           `json:"-"`
	WorkspaceID string          `json:"-"`
	ProjectID   string          `json:"-"`
	Key         string          `json:"key"`
	Summary     string          `json:"summary"`
	Description json.RawMessage `json:"description"`
	Status      Status          `json:"status"`
	IssueType   IssueType       `json:"issuetype"`
	Parent      *IssueParent    `json:"parent,omitempty"`
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
	Issue               Issue
	ProjectKey          string
	ProjectName         string
	BoardID             string
	CanEdit             bool
	CanTriage           bool
	AttachmentsEnabled  bool
	IssueLinkingEnabled bool
	TimeTrackingEnabled bool
	VotingEnabled       bool
	WatchingEnabled     bool
	CurrentUserID       string
	Comments            []Comment
	Transitions         []WorkflowTransition
	History             []ChangelogEntry
	Attachments         []Attachment
	Worklogs            []Worklog
	Activity            []IssueActivityItem
	Members             []User
	Priorities          []Priority
	SecurityLevels      []WorkflowTransition
	SecurityLevelName   string
	CustomFields        []CustomFieldView
	Watchers            []User
	IsWatching          bool
	Voters              []User
	HasVoted            bool
	Links               []IssueLinkView
	LinkTypes           []LinkType
	Children            []Issue
	ParentOptions       []CreateFieldOption
	Forms               []IssueForm
	Development         []DevelopmentItem
	Delivery            []DeliveryItem
	AppPanels           []AppModule
	AppActivityTabs     []AppModule
	AppContexts         []AppModule
	AppIssueContent     []AppIssueContent
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
	ID           string
	Name         string
	ScreenFields []string
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
	ID          string
	Name        string
	Type        string
	Description string
	Value       string
}

// CreateFieldOption is one selectable value exposed by create metadata.
// Key is populated for project options; ID is used by every other registry.
type CreateFieldOption struct {
	ID   string `json:"id,omitempty"`
	Key  string `json:"key,omitempty"`
	Name string `json:"name"`
}

// CreateFieldMeta is the canonical create-form schema shared by the browser
// and Jira-compatible REST endpoints. Section controls presentation only; the
// field ID, type, requirement and options remain the validation contract.
type CreateFieldMeta struct {
	ID          string              `json:"fieldId"`
	Name        string              `json:"name"`
	Type        string              `json:"type"`
	Description string              `json:"description,omitempty"`
	Required    bool                `json:"required"`
	Custom      bool                `json:"custom,omitempty"`
	Section     string              `json:"-"`
	Options     []CreateFieldOption `json:"allowedValues,omitempty"`
	// Default is the raw JSON the field's applicable context supplies, empty
	// when the context sets none.
	Default string `json:"-"`
}

type CreateProjectMeta struct {
	Project    Project           `json:"project"`
	IssueTypes []IssueType       `json:"issueTypes"`
	Fields     []CreateFieldMeta `json:"fields"`
	// ScreenFields holds the ordered field IDs the project's screen scheme
	// exposes per work type, keyed by issue type ID. An absent or empty entry
	// means no screen governs that form and every project field applies.
	ScreenFields map[string][]string `json:"-"`
	// FieldBehaviour holds the project's field configuration rules per work
	// type. A field without a rule is optional and visible.
	FieldBehaviour map[string]map[string]FieldBehaviour `json:"-"`
	// CustomFieldContexts maps work type to the custom fields whose context
	// applies there, and what that context supplies. A work type present here
	// governs which custom fields the form may show.
	CustomFieldContexts map[string]map[string]CustomFieldContextInfo `json:"-"`
}

// CustomFieldContextInfo is what the governing context contributes to one
// custom field's metadata.
type CustomFieldContextInfo struct {
	Default string
	Options []CreateFieldOption
}

// FieldsForIssueType narrows and orders the project's fields to what the work
// type's form screen shows. Context fields and summary always survive: the
// command path cannot create work without a project, a work type, and a
// summary, so a screen may not hide them.
func (m CreateProjectMeta) FieldsForIssueType(issueTypeID string) []CreateFieldMeta {
	screen := m.ScreenFields[issueTypeID]
	if len(screen) == 0 {
		return m.applyFieldBehaviour(issueTypeID, m.applyCustomFieldContexts(issueTypeID, m.Fields))
	}
	rank := make(map[string]int, len(screen))
	for index, id := range screen {
		rank[id] = index
	}
	context, selected := []CreateFieldMeta{}, []CreateFieldMeta{}
	summary, summaryOnScreen := CreateFieldMeta{}, false
	for _, field := range m.Fields {
		if field.Section == "context" {
			context = append(context, field)
			continue
		}
		if field.ID == "summary" {
			summary = field
			_, summaryOnScreen = rank[field.ID]
		}
		if _, shown := rank[field.ID]; shown {
			selected = append(selected, field)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool { return rank[selected[i].ID] < rank[selected[j].ID] })
	out := context
	if summary.ID != "" && !summaryOnScreen {
		out = append(out, summary)
	}
	return m.applyFieldBehaviour(issueTypeID, m.applyCustomFieldContexts(issueTypeID, append(out, selected...)))
}

// applyCustomFieldContexts drops custom fields whose context does not reach this
// project and work type, and stamps the default the governing context supplies.
// System fields have no context and always survive.
func (m CreateProjectMeta) applyCustomFieldContexts(issueTypeID string, fields []CreateFieldMeta) []CreateFieldMeta {
	applicable, governed := m.CustomFieldContexts[issueTypeID]
	if !governed {
		return fields
	}
	out := make([]CreateFieldMeta, 0, len(fields))
	for _, field := range fields {
		if !field.Custom {
			out = append(out, field)
			continue
		}
		info, applies := applicable[field.ID]
		if !applies {
			continue
		}
		field.Default = info.Default
		// A select field offers exactly the options its governing context holds.
		if field.Type == "option" {
			field.Options = info.Options
		}
		out = append(out, field)
	}
	return out
}

// applyFieldBehaviour stamps the project's field configuration onto a resolved
// field list: hidden fields drop out, required fields are marked, and an
// administrator's description replaces the built-in help text. Context fields
// and summary are exempt, because work cannot be created without them.
func (m CreateProjectMeta) applyFieldBehaviour(issueTypeID string, fields []CreateFieldMeta) []CreateFieldMeta {
	rules := m.FieldBehaviour[issueTypeID]
	if len(rules) == 0 {
		return fields
	}
	out := make([]CreateFieldMeta, 0, len(fields))
	for _, field := range fields {
		rule, governed := rules[field.ID]
		exempt := field.Section == "context" || field.ID == "summary"
		if governed && rule.IsHidden && !exempt {
			continue
		}
		if governed {
			if rule.IsRequired {
				field.Required = true
			}
			if rule.Description != "" {
				field.Description = rule.Description
			}
		}
		out = append(out, field)
	}
	return out
}

// FieldRules exposes the project's field configuration for one work type so the
// command path can enforce the same rules the forms advertise.
func (m CreateProjectMeta) FieldRules(issueTypeID string) map[string]FieldBehaviour {
	return m.FieldBehaviour[issueTypeID]
}

type IssueCreateMetadata struct {
	Projects []CreateProjectMeta `json:"projects"`
}
