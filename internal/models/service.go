package models

import "time"

// ServiceDesk is the Jira Service Management portal attached to a service project.
type ServiceDesk struct {
	ID                 string
	WorkspaceID        string
	ProjectID          string
	ProjectKey         string
	ProjectName        string
	ProjectTypeKey     string
	PortalName         string
	CustomerAccessOpen bool
}

type ServiceOrganization struct {
	ID, WorkspaceID, Name string
	CreatedAt             time.Time
}

type ServiceRequestTypeGroup struct {
	ID, ServiceDeskID, Name string
	Position                int
}

type ServiceRequestTypeField struct {
	ID, RequestTypeID, Name, Type, Description, HelpText string
	Required, Custom                                     bool
	Position                                             int
}

type ServiceKnowledgeArticle struct {
	PageID, SpaceID, SpaceKey, Title, Excerpt, Body string
}

// ServiceRequestType describes one customer-facing form backed by a Jira issue type.
type ServiceRequestType struct {
	ID            string
	ServiceDeskID string
	Name          string
	Description   string
	HelpText      string
	IssueTypeID   string
	GroupIDs      []string
}

// ServiceRequest attaches customer-facing service metadata to a regular Jira
// issue. The issue remains the canonical workflow and field record.
type ServiceRequest struct {
	Issue       *Issue
	ServiceDesk ServiceDesk
	RequestType ServiceRequestType
	Customer    *User
	Channel     string
	CreatedAt   time.Time
	SLAs        []ServiceSLA
}

// ServiceRequestComment records which Jira comments may cross the customer
// portal boundary. Comments without a row are agent-internal by default.
type ServiceRequestComment struct {
	Comment     Comment
	Public      bool
	Attachments []Attachment
}

type ServiceApprover struct {
	User      *User
	Decision  string
	DecidedAt *time.Time
}

type ServiceApproval struct {
	ID, RequestIssueID, Name, FinalDecision string
	CreatedAt                               time.Time
	CompletedAt                             *time.Time
	Approvers                               []ServiceApprover
}

type ServiceTemporaryAttachment struct {
	ID, WorkspaceID, ServiceDeskID, AuthorID string
	Filename, MimeType, BlobRef              string
	Size                                     int64
	CreatedAt, ExpiresAt                     time.Time
}

type ServiceRequestAttachment struct {
	Attachment Attachment
	CommentID  string
	Public     bool
}

type ServiceRequestFeedback struct {
	RequestIssueID, ReporterID, Type, Comment string
	Rating                                    int
	CreatedAt, UpdatedAt                      time.Time
}

// ServiceQueue is an ordered agent work view for one service desk.
type ServiceQueue struct {
	ID, ServiceDeskID, Name, JQL, Kind string
	Fields                             []string
	Position                           int
	IssueCount                         int
}

type ServiceCalendar struct {
	ID, ServiceDeskID, Name, TimeZone string
	Weekdays                          []int16
	StartMinute, EndMinute            int16
	Holidays                          map[string]string
}

type ServiceSLAMetric struct {
	ID, ServiceDeskID, CalendarID, Name, Kind string
	GoalMillis                                int64
	Position                                  int
}

type ServiceSLAGoal struct {
	ID, MetricID, Name, JQL string
	GoalMillis              int64
	Position                int
}

type ServiceSLACycle struct {
	ID, GoalID, GoalName                       string
	GoalLabel, ElapsedLabel, RemainingLabel    string
	StartTime, BreachTime                      time.Time
	StopTime                                   *time.Time
	GoalMillis, ElapsedMillis, RemainingMillis int64
	Breached, Paused, WithinCalendarHours      bool
}

type ServiceSLA struct {
	ServiceSLAMetric
	CompletedCycles []ServiceSLACycle
	OngoingCycle    *ServiceSLACycle
}

type ServiceReportDay struct {
	Day   string
	Count int
}

type ServiceReport struct {
	WindowDays                                    int
	TotalRequests, OpenRequests, ResolvedRequests int
	BreachedRequests, SatisfactionResponses       int
	AverageSatisfaction                           float64
	Daily                                         []ServiceReportDay
}
