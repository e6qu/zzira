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
		{"com.zzira:created-vs-resolved", "Created vs. resolved chart", "Work created against work resolved in a project, day by day.", ""},
		{"com.zzira:resolution-time", "Resolution time", "How long a project's work takes from creation to resolution.", ""},
		{"com.zzira:velocity", "Velocity chart", "Commitment against completed work for a scrum board's recent sprints.", ""},
		{"com.zzira:sprint-burndown", "Sprint burndown", "Remaining work in a scrum board's active sprint.", ""},
		{"com.zzira:two-dimensional-statistics", "Two dimensional filter statistics", "Count work across two groupings, such as status by assignee.", ""},
		{"com.zzira:heat-map", "Heat map", "See which values carry the most work, sized by their share.", ""},
		{"com.zzira:watched-issues", "Watched work items", "The work each viewer watches.", ""},
		{"com.zzira:voted-issues", "Voted work items", "The work each viewer voted for.", ""},
		{"com.zzira:in-progress", "Work in progress", "Each viewer's assigned work that is in progress.", ""},
		{"com.zzira:recently-created", "Recently created chart", "Work created each day in a project, split by whether it is resolved.", ""},
		{"com.zzira:average-age", "Average age chart", "How old a project's unresolved work was at the end of each day.", ""},
		{"com.zzira:time-since", "Time since chart", "Work created, updated or resolved each day in a project.", ""},
		{"com.zzira:days-remaining", "Days remaining in sprint", "How long is left in a scrum board's active sprint.", ""},
		{"com.zzira:sprint-health", "Sprint health", "Time elapsed, work complete and scope change in a scrum board's active sprint.", ""},
		{"com.zzira:activity-stream", "Activity stream", "Recent creations, changes and comments on work from a filter or JQL.", ""},
		{"com.zzira:calendar", "Calendar", "This month's due work and release dates from a filter or JQL.", ""},
		{"com.zzira:road-map", "Road map", "A project's unreleased versions due soon, with their progress.", ""},
		{"com.zzira:bubble-chart", "Bubble chart", "Work from a filter or JQL by how recently it changed, its participants and its votes.", ""},
		{"com.zzira:dora-metrics", "DORA metrics", "A project's four delivery metrics: how often it deploys, how long a change takes to reach production, how often a change fails, and how long recovery takes.", ""},
		{"com.zzira:deployment-frequency", "Deployment frequency", "A project's successful production deployments, day by day.", ""},
	}
}

// GadgetGrouping is a field that chart gadgets count work by.
type GadgetGrouping struct {
	Key, Name string
}

// GadgetGroupings are the fields chart gadgets can group work by.
var GadgetGroupings = []GadgetGrouping{
	{"status", "Status"}, {"priority", "Priority"}, {"issuetype", "Work type"}, {"assignee", "Assignee"},
	{"reporter", "Reporter"}, {"resolution", "Resolution"}, {"project", "Project"}, {"labels", "Labels"},
}

// GadgetGroupingName names a grouping for people, or "" when it is unknown.
func GadgetGroupingName(key string) string {
	for _, grouping := range GadgetGroupings {
		if grouping.Key == key {
			return grouping.Name
		}
	}
	return ""
}

// GadgetScopeJQL is the JQL a list gadget adds for whoever views it, such as
// the viewer's own assignments.
func GadgetScopeJQL(moduleKey string) string {
	switch moduleKey {
	case "com.zzira:assigned-to-me":
		return "assignee = currentUser()"
	case "com.zzira:watched-issues":
		return "issue in watchedIssues()"
	case "com.zzira:voted-issues":
		return "issue in votedIssues()"
	case "com.zzira:in-progress":
		return "assignee = currentUser() AND statusCategory = indeterminate"
	}
	return ""
}

// ListGadget reports whether a gadget lists work items rather than counting
// them.
func ListGadget(moduleKey string) bool {
	return moduleKey == "com.zzira:filter-results" || GadgetScopeJQL(moduleKey) != ""
}

// ListGadget reports whether the gadget lists work items.
func (g DashboardGadget) ListGadget() bool {
	return ListGadget(g.ModuleKey)
}

// ReportGadget reports whether a gadget draws a project or board report
// rather than the results of a work item query.
func ReportGadget(moduleKey string) bool {
	switch moduleKey {
	case "com.zzira:velocity", "com.zzira:sprint-burndown", "com.zzira:days-remaining", "com.zzira:sprint-health":
		return true
	}
	return ProjectReportGadget(moduleKey)
}

// ProjectReportGadget reports whether a gadget draws a report for a project
// over a window of days, rather than for a scrum board.
func ProjectReportGadget(moduleKey string) bool {
	switch moduleKey {
	case "com.zzira:created-vs-resolved", "com.zzira:resolution-time", "com.zzira:recently-created", "com.zzira:average-age", "com.zzira:time-since", "com.zzira:road-map",
		"com.zzira:dora-metrics", "com.zzira:deployment-frequency":
		return true
	}
	return false
}

// ChartGadget reports whether a gadget counts query results by a grouping.
func ChartGadget(moduleKey string) bool {
	switch moduleKey {
	case "com.zzira:activity-stream", "com.zzira:calendar", "com.zzira:bubble-chart":
		return false
	}
	return !ListGadget(moduleKey) && !ReportGadget(moduleKey)
}

// ChartGadget reports whether the gadget counts work by a grouping.
func (g DashboardGadget) ChartGadget() bool {
	return ChartGadget(g.ModuleKey)
}

// ProjectReportGadget reports whether the gadget draws a project report.
func (g DashboardGadget) ProjectReportGadget() bool {
	return ProjectReportGadget(g.ModuleKey)
}

// TimeSinceFields are the dates the time since chart counts work by.
var TimeSinceFields = []GadgetGrouping{{"created", "Created"}, {"updated", "Updated"}, {"resolved", "Resolved"}}

// TimeSinceFieldName names a time since date for people, or "" when unknown.
func TimeSinceFieldName(key string) string {
	for _, field := range TimeSinceFields {
		if field.Key == key {
			return field.Name
		}
	}
	return ""
}

// ReportGadget reports whether the gadget draws a report.
func (g DashboardGadget) ReportGadget() bool {
	return ReportGadget(g.ModuleKey)
}

// GadgetConfig is a gadget's settings: a work item query for list and chart
// gadgets, or the project or board and time window a report gadget draws.
type GadgetConfig struct {
	JQL      string `json:"jql"`
	FilterID string `json:"filterId"`
	GroupBy  string `json:"groupBy"`
	// YGroupBy is the second grouping of two dimensional statistics, counted
	// down its rows.
	YGroupBy   string `json:"yGroupBy,omitempty"`
	Limit      int    `json:"limit"`
	ProjectKey string `json:"projectKey,omitempty"`
	BoardID    string `json:"boardId,omitempty"`
	Days       int    `json:"days,omitempty"`
	Cumulative bool   `json:"cumulative,omitempty"`
	// DateField is the date the time since chart counts: created, updated or
	// resolved.
	DateField string `json:"dateField,omitempty"`
	// BubbleAxis is what the bubble chart's vertical axis counts, participants
	// or votes; bubbles are sized by the other.
	BubbleAxis string `json:"bubbleAxis,omitempty"`
}

// GroupLabel names the chart grouping for people.
func (c GadgetConfig) GroupLabel() string { return GadgetGroupingName(c.GroupBy) }

// YGroupLabel names the second grouping for people.
func (c GadgetConfig) YGroupLabel() string { return GadgetGroupingName(c.YGroupBy) }
