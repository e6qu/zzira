package models

// DashboardShare is the delivered user/authenticated subset of SharePermission.
type DashboardShare struct {
	ID   int64               `json:"id,omitempty"`
	Type string              `json:"type"`
	User *DashboardShareUser `json:"user,omitempty"`
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
}

func GadgetCatalog() []GadgetDefinition {
	return []GadgetDefinition{
		{"com.zzira:filter-results", "Filter results", "A list of work items from a saved filter or JQL query."},
		{"com.zzira:issue-statistics", "Issue statistics", "Compare work by status, priority, type or assignee."},
		{"com.zzira:pie-chart", "Pie chart", "See how work is distributed, with an accessible data table."},
		{"com.zzira:assigned-to-me", "Assigned to me", "Your assigned work, evaluated for whoever views the dashboard."},
	}
}

type GadgetConfig struct {
	JQL      string `json:"jql"`
	FilterID string `json:"filterId"`
	GroupBy  string `json:"groupBy"`
	Limit    int    `json:"limit"`
}
