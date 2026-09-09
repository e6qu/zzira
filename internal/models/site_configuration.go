package models

type AnnouncementBanner struct {
	HashID        string `json:"hashId"`
	IsDismissible bool   `json:"isDismissible"`
	IsEnabled     bool   `json:"isEnabled"`
	Message       string `json:"message"`
	Visibility    string `json:"visibility"`
}

type TimeTrackingProvider struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type TimeTrackingConfiguration struct {
	DefaultUnit        string  `json:"defaultUnit"`
	TimeFormat         string  `json:"timeFormat"`
	WorkingDaysPerWeek float64 `json:"workingDaysPerWeek"`
	WorkingHoursPerDay float64 `json:"workingHoursPerDay"`
}

type JiraSiteConfiguration struct {
	Announcement            AnnouncementBanner
	AttachmentsEnabled      bool
	IssueLinkingEnabled     bool
	SubTasksEnabled         bool
	TimeTrackingEnabled     bool
	UnassignedIssuesAllowed bool
	VotingEnabled           bool
	WatchingEnabled         bool
	TimeTrackingProvider    string
	TimeTracking            TimeTrackingConfiguration
	NavigatorColumns        []string
	ApplicationProperties   map[string]string
}

type ApplicationProperty struct {
	AllowedValues []string `json:"allowedValues,omitempty"`
	DefaultValue  string   `json:"defaultValue"`
	Description   string   `json:"desc"`
	Example       string   `json:"example,omitempty"`
	ID            string   `json:"id"`
	Key           string   `json:"key"`
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	Value         string   `json:"value"`
}

type ColumnItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
