package models

import (
	"encoding/json"
	"strconv"
)

// DashboardShare is the delivered user/authenticated subset of SharePermission.
type DashboardShare struct {
	ID      int64                  `json:"id,omitempty"`
	Type    string                 `json:"type"`
	User    *DashboardShareUser    `json:"user,omitempty"`
	Group   *DashboardShareGroup   `json:"group,omitempty"`
	Project *DashboardShareProject `json:"project,omitempty"`
	Role    *DashboardShareRole    `json:"role,omitempty"`
}

// DashboardShareGroup names a group a dashboard is shared with.
type DashboardShareGroup struct {
	GroupID string `json:"groupId"`
	Name    string `json:"name,omitempty"`
}

// DashboardShareProject names a project whose browsers, or one of whose
// roles, a dashboard is shared with.
type DashboardShareProject struct {
	ID   string `json:"id"`
	Key  string `json:"key,omitempty"`
	Name string `json:"name,omitempty"`
}

// DashboardShareRole names the project role of a project-role share.
type DashboardShareRole struct {
	ID   FlexibleID `json:"id"`
	Name string     `json:"name,omitempty"`
}

// FlexibleID accepts an identifier sent as a JSON number or string and writes
// numeric identifiers back as numbers.
type FlexibleID string

func (id *FlexibleID) UnmarshalJSON(raw []byte) error {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		*id = FlexibleID(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return err
	}
	*id = FlexibleID(number.String())
	return nil
}

func (id FlexibleID) MarshalJSON() ([]byte, error) {
	if _, err := strconv.ParseInt(string(id), 10, 64); err == nil {
		return []byte(id), nil
	}
	return json.Marshal(string(id))
}

type DashboardShareUser struct {
	AccountID string `json:"accountId"`
}
type Dashboard struct {
	ID               string
	WorkspaceID      string
	OwnerID          string
	OwnerName        string
	Name             string
	Description      string
	SharePermissions []DashboardShare
	EditPermissions  []DashboardShare
	Layout           string
	RefreshMS        int
	Favourite        bool
	Popularity       int
	Writable         bool
}

func (d Dashboard) Columns() int {
	switch d.Layout {
	case "A":
		return 1
	case "AAA":
		return 3
	default:
		return 2
	}
}

type GadgetPosition struct {
	Column int `json:"column"`
	Row    int `json:"row"`
}
type DashboardGadget struct {
	ID        int64          `json:"id"`
	ModuleKey string         `json:"moduleKey"`
	Title     string         `json:"title"`
	Color     string         `json:"color"`
	Position  GadgetPosition `json:"position"`
}
type GadgetDefinition struct {
	ModuleKey   string `json:"moduleKey"`
	Title       string `json:"title"`
	Description string `json:"-"`
	Thumbnail   string `json:"-"`
}

func GadgetCatalog() []GadgetDefinition {
	return []GadgetDefinition{
		{"com.zzira:filter-results", "Filter results", "A list of work items from a saved filter or JQL query.", ""},
		{"com.zzira:issue-statistics", "Issue statistics", "Compare work by status, priority, type or assignee.", ""},
		{"com.zzira:pie-chart", "Pie chart", "See how work is distributed, with an accessible data table.", ""},
		{"com.zzira:assigned-to-me", "Assigned to me", "Your assigned work, evaluated for whoever views the dashboard.", ""},
	}
}

type GadgetConfig struct {
	JQL      string `json:"jql"`
	FilterID string `json:"filterId"`
	GroupBy  string `json:"groupBy"`
	Limit    int    `json:"limit"`
}
