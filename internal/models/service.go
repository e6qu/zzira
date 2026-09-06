package models

import "time"

// ServiceDesk is the Jira Service Management portal attached to a service project.
type ServiceDesk struct {
	ID             string
	WorkspaceID    string
	ProjectID      string
	ProjectKey     string
	ProjectName    string
	ProjectTypeKey string
	PortalName     string
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
}

// ServiceRequestComment records which Jira comments may cross the customer
// portal boundary. Comments without a row are agent-internal by default.
type ServiceRequestComment struct {
	Comment Comment
	Public  bool
}

// ServiceQueue is an ordered agent work view for one service desk.
type ServiceQueue struct {
	ID, ServiceDeskID, Name, JQL, Kind string
	Fields                             []string
	Position                           int
	IssueCount                         int
}
