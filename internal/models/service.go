package models

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
