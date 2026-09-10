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
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	IsDefault   bool                  `json:"isDefault,omitempty"`
	Members     []string              `json:"members,omitempty"`
	Grants      []SecurityLevelMember `json:"-"`
}

type SecurityScheme struct {
	ID             string          `json:"id"`
	WorkspaceID    string          `json:"-"`
	Name           string          `json:"name"`
	Description    string          `json:"description,omitempty"`
	DefaultLevelID string          `json:"defaultLevelId,omitempty"`
	ProjectIDs     []string        `json:"projectIds,omitempty"`
	Levels         []SecurityLevel `json:"levels"`
}

type SecurityLevelMember struct {
	ID              int64  `json:"id"`
	SchemeID        string `json:"issueSecuritySchemeId"`
	LevelID         string `json:"issueSecurityLevelId"`
	HolderType      string `json:"type"`
	HolderParameter string `json:"parameter,omitempty"`
	HolderValue     string `json:"value,omitempty"`
	Managed         bool   `json:"managed,omitempty"`
}

type CustomField struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	Description       string `json:"description,omitempty"`
	WorkspaceID       string `json:"-"`
	AppInstallationID string `json:"-"`
	AppKey            string `json:"-"`
	AppModuleKey      string `json:"-"`
	Dynamic           bool   `json:"-"`
	Active            bool   `json:"-"`
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
	LinkID    string `json:"linkId"`
	InwardID  string `json:"inwardIssueId"`
	OutwardID string `json:"outwardIssueId"`
}

type LinkType struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Inward  string `json:"inward"`
	Outward string `json:"outward"`
}

// DashboardStats powers the home dashboard (server-rendered).
type DashboardStats struct {
	StatusCounts []struct {
		Status Status
		Count  int
	}
	MyOpenIssues int
	Recent       []RecentActivity
}

type RecentActivity struct {
	ActorName string
	Summary   string
	Op        string
	IssueKey  string
	Created   string
}

// Screen groups the fields an administrator exposes on a work item form. Tabs
// order the groups; ScreenField rows order the fields inside one tab.
type Screen struct {
	ID          string      `json:"id"`
	WorkspaceID string      `json:"-"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	IsDefault   bool        `json:"-"`
	Tabs        []ScreenTab `json:"tabs,omitempty"`
}

type ScreenTab struct {
	ID       string        `json:"id"`
	ScreenID string        `json:"-"`
	Name     string        `json:"name"`
	Position int           `json:"-"`
	Fields   []ScreenField `json:"fields,omitempty"`
}

type ScreenField struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"-"`
	Custom   bool   `json:"-"`
}
